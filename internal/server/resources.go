package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Resource metrics are observational: missing samples are never synthesized as zero.
// Container allocations are not scheduler-effective pod requirements (init containers/overhead).
type resourcePoint struct {
	At                     time.Time `json:"at"`
	CPUUsageCores          *float64  `json:"cpuUsageCores"`
	MemoryUsageBytes       *float64  `json:"memoryUsageBytes"`
	CPURequestsCores       *float64  `json:"cpuRequestsCores"`
	MemoryRequestsBytes    *float64  `json:"memoryRequestsBytes"`
	CPUAllocatableCores    *float64  `json:"cpuAllocatableCores,omitempty"`
	MemoryAllocatableBytes *float64  `json:"memoryAllocatableBytes,omitempty"`
}
type resourceQuota struct {
	Name     string   `json:"name"`
	Resource string   `json:"resource"`
	Hard     *float64 `json:"hard"`
	Used     *float64 `json:"used"`
}
type resourceNamespace struct {
	Quotas                    []resourceQuota `json:"quotas"`
	Namespace                 string          `json:"namespace"`
	CPUUsageCores             *float64        `json:"cpuUsageCores"`
	MemoryUsageBytes          *float64        `json:"memoryUsageBytes"`
	CPURequestsCores          *float64        `json:"cpuRequestsCores"`
	MemoryRequestsBytes       *float64        `json:"memoryRequestsBytes"`
	CPULimitsCores            *float64        `json:"cpuLimitsCores"`
	MemoryLimitsBytes         *float64        `json:"memoryLimitsBytes"`
	PVCRequestedBytes         *float64        `json:"pvcRequestedBytes"`
	PVCCapacityBytes          *float64        `json:"pvcCapacityBytes"`
	PVCUsedBytes              *float64        `json:"pvcUsedBytes"`
	UnlimitedCPUContainers    *int            `json:"unlimitedCPUContainers"`
	UnlimitedMemoryContainers *int            `json:"unlimitedMemoryContainers"`
	Trend                     []resourcePoint `json:"trend"`
}
type resourceNode struct {
	Node                   string   `json:"node"`
	CPUUsageCores          *float64 `json:"cpuUsageCores"`
	CPUAllocatableCores    *float64 `json:"cpuAllocatableCores"`
	MemoryUsageBytes       *float64 `json:"memoryUsageBytes"`
	MemoryAllocatableBytes *float64 `json:"memoryAllocatableBytes"`
}
type resourceCluster struct {
	CPUUsageCores          *float64        `json:"cpuUsageCores"`
	CPUAllocatableCores    *float64        `json:"cpuAllocatableCores"`
	MemoryUsageBytes       *float64        `json:"memoryUsageBytes"`
	MemoryAllocatableBytes *float64        `json:"memoryAllocatableBytes"`
	Trend                  []resourcePoint `json:"trend"`
}
type resourceResult struct {
	GeneratedAt       time.Time           `json:"generatedAt"`
	Hours             int                 `json:"hours"`
	ResolutionSeconds int                 `json:"resolutionSeconds"`
	Incomplete        bool                `json:"incomplete"`
	Warnings          []string            `json:"warnings"`
	Notes             []string            `json:"notes"`
	Cluster           *resourceCluster    `json:"cluster"`
	Nodes             []resourceNode      `json:"nodes"`
	Namespaces        []resourceNamespace `json:"namespaces"`
}
type resourceCacheEntry = jsonCacheEntry
type resourceOptions struct {
	hours     int
	namespace string
}
type resourceQuery struct {
	name, query          string
	history              bool
	node                 bool
	rangeStart, rangeEnd time.Time
}
type resourceSample struct {
	Metric map[string]string    `json:"metric"`
	Value  [2]json.RawMessage   `json:"value"`
	Values [][2]json.RawMessage `json:"values"`
}
type resourceQueryResult struct {
	rows []resourceSample
	err  error
}

var resourceSlots = make(chan struct{}, 2)

func resourceOptionsFor(r *http.Request, a access) (resourceOptions, error) {
	o := resourceOptions{hours: 24}
	for k, v := range r.URL.Query() {
		if len(v) != 1 {
			return o, errors.New("query parameters must not be repeated")
		}
		switch k {
		case "hours":
			n, e := strconv.Atoi(v[0])
			if e != nil || (n != 24 && n != 168) {
				return o, errors.New("hours must be 24 or 168")
			}
			o.hours = n
		case "namespace":
			if len(v[0]) > 253 {
				return o, errors.New("namespace is too long")
			}
			o.namespace = v[0]
		default:
			return o, errors.New("unsupported resource query parameter")
		}
	}
	if o.namespace != "" && !a.operator && !a.namespaces[o.namespace] {
		return o, errors.New("namespace is not authorized")
	}
	return o, nil
}
func resourceScope(o resourceOptions, a access) string {
	if o.namespace != "" {
		return "namespace=" + strconv.Quote(o.namespace)
	}
	if a.operator {
		return `namespace!=""`
	}
	ns := a.namespaceList()
	for i := range ns {
		ns[i] = regexp.QuoteMeta(ns[i])
	}
	if len(ns) == 0 {
		return `namespace=~"a^"`
	}
	return "namespace=~" + strconv.Quote(strings.Join(ns, "|"))
}
func resourceQueries(o resourceOptions, a access) []resourceQuery {
	scope := resourceScope(o, a)
	containerScope := scope + `,container!="",container!="POD"`
	// The max-before-sum removes copies from multiple scrape jobs/endpoints.
	cpu := "max by (namespace,pod,container) (rate(container_cpu_usage_seconds_total{" + containerScope + `,cpu=~"total|"` + "}[5m]))"
	mem := "max by (namespace,pod,container) (container_memory_working_set_bytes{" + containerScope + "})"
	active := `max by (namespace,pod) (kube_pod_status_phase{` + scope + `,phase=~"Pending|Running"} == 1)`
	activeContainers := func(metric, extra string) string {
		return "max by (namespace,pod,container) (" + metric + "{" + containerScope + extra + "}) and on (namespace,pod) " + active
	}
	reqCPU := activeContainers("kube_pod_container_resource_requests", `,resource="cpu",unit="core"`)
	reqMem := activeContainers("kube_pod_container_resource_requests", `,resource="memory",unit="byte"`)
	inventory := activeContainers("kube_pod_container_info", "")
	requestHistory := func(query string) string {
		return "sum by (namespace) (" + query + ") and on (namespace) (count by (namespace) (" + query + ") == count by (namespace) (" + inventory + "))"
	}
	q := []resourceQuery{
		{name: "containers", query: activeContainers("kube_pod_container_info", "")},
		{name: "cpuUsage", query: cpu}, {name: "memoryUsage", query: mem},
		{name: "cpuRequests", query: reqCPU}, {name: "memoryRequests", query: reqMem},
		{name: "cpuLimits", query: activeContainers("kube_pod_container_resource_limits", `,resource="cpu",unit="core"`)},
		{name: "memoryLimits", query: activeContainers("kube_pod_container_resource_limits", `,resource="memory",unit="byte"`)},
		{name: "quotas", query: "max by (namespace,resourcequota,resource,type) (kube_resourcequota{" + scope + "})"},
		{name: "pvcs", query: "max by (namespace,persistentvolumeclaim) (kube_persistentvolumeclaim_info{" + scope + "})"},
		{name: "pvcRequests", query: "max by (namespace,persistentvolumeclaim) (kube_persistentvolumeclaim_resource_requests_storage_bytes{" + scope + "})"},
		{name: "pvcCapacity", query: "max by (namespace,persistentvolumeclaim) (kubelet_volume_stats_capacity_bytes{" + scope + "})"},
		{name: "pvcUsed", query: "max by (namespace,persistentvolumeclaim) (kubelet_volume_stats_used_bytes{" + scope + "})"},
		{name: "cpuUsageHistory", query: "sum by (namespace) (" + cpu + ")", history: true},
		{name: "memoryUsageHistory", query: "sum by (namespace) (" + mem + ")", history: true},
		{name: "cpuRequestsHistory", query: requestHistory(reqCPU), history: true},
		{name: "memoryRequestsHistory", query: requestHistory(reqMem), history: true},
	}
	if a.operator {
		nodeInventory := `max by (node) (kube_node_status_allocatable{node!="",resource="cpu",unit="core"})`
		// A scrape target mapping to multiple node names is ambiguous and must not be joined.
		uname := `max by (instance,job,nodename) (node_uname_info{nodename!=""})`
		uniqueUname := "(" + uname + " and on (instance,job) (count by (instance,job) (" + uname + ") == 1))"
		hostCPU := `sum by (instance,job) (max by (instance,job,cpu) (1 - clamp(rate(node_cpu_seconds_total{mode="idle"}[5m]), 0, 1)))`
		hostMemory := `clamp_min(max by (instance,job) (node_memory_MemTotal_bytes) - max by (instance,job) (node_memory_MemAvailable_bytes), 0)`
		hostNode := func(metric string) string {
			return `max by (node) (label_replace((` + metric + `) * on (instance,job) group_left (nodename) ` + uniqueUname + `, "node", "$1", "nodename", "(.+)")) and on (node) ` + nodeInventory
		}
		for _, v := range []struct{ name, query string }{
			{"nodeCPUAllocatable", nodeInventory},
			{"nodeMemoryAllocatable", `max by (node) (kube_node_status_allocatable{node!="",resource="memory",unit="byte"})`},
			{"nodeCPUUsage", hostNode(hostCPU)},
			{"nodeMemoryUsage", hostNode(hostMemory)},
		} {
			// Historical totals require every contemporaneously observed Kubernetes node.
			history := "sum (" + v.query + ") and (count (" + v.query + ") == count (" + nodeInventory + "))"
			q = append(q, resourceQuery{name: v.name, query: v.query, node: true}, resourceQuery{name: v.name + "History", query: history, history: true, node: true})
		}
	}
	return q
}
func (s *Server) resources(w http.ResponseWriter, r *http.Request, a access) {
	o, e := resourceOptionsFor(r, a)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	if s.config.Prometheus.URL == "" && !s.config.Demo {
		fail(w, 404, "Prometheus is required for resource analytics")
		return
	}
	now := time.Now().UTC().Truncate(time.Minute)
	keyData, _ := json.Marshal([]any{o.hours, o.namespace, a.operator, a.namespaceList()})
	key := string(keyData)
	s.resourceCacheMu.Lock()
	entry, ok := s.resourceCache[key]
	s.resourceCacheMu.Unlock()
	if ok && time.Since(entry.at) < time.Minute {
		jsonResponse(w, 200, entry.body)
		return
	}
	var result resourceResult
	if s.config.Demo {
		result = demoResources(now, o, a)
	} else {
		select {
		case resourceSlots <- struct{}{}:
			defer func() { <-resourceSlots }()
		default:
			w.Header().Set("Retry-After", "5")
			fail(w, 503, "resource analytics is busy; retry shortly")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		result = s.collectResources(r.WithContext(ctx), a, o, now)
	}
	if r.Context().Err() != nil {
		return
	}
	s.resourceCacheMu.Lock()
	putJSONCache(&s.resourceCache, key, result, time.Now())
	s.resourceCacheMu.Unlock()
	jsonResponse(w, 200, result)
}
func (s *Server) fetchResourceQuery(r *http.Request, q resourceQuery, now time.Time, hours, step int) resourceQueryResult {
	params := url.Values{"query": {q.query}, "timeout": {"8s"}}
	endpoint := "/api/v1/query"
	if q.history {
		endpoint = "/api/v1/query_range"
		start, end := now.Add(-time.Duration(hours)*time.Hour), now
		if !q.rangeStart.IsZero() {
			start, end = q.rangeStart, q.rangeEnd
		}
		params.Set("start", strconv.FormatInt(start.Unix(), 10))
		params.Set("end", strconv.FormatInt(end.Unix(), 10))
		params.Set("step", strconv.Itoa(step))
	} else {
		params.Set("time", strconv.FormatInt(now.Unix(), 10))
	}
	b, status, e := s.prom.request(r, "GET", endpoint+"?"+params.Encode(), nil)
	if e != nil || status != 200 {
		return resourceQueryResult{err: errors.New("metric request failed")}
	}
	var response struct {
		Status   string   `json:"status"`
		Warnings []string `json:"warnings"`
		Data     struct {
			ResultType string           `json:"resultType"`
			Result     []resourceSample `json:"result"`
		} `json:"data"`
	}
	kind := "vector"
	if q.history {
		kind = "matrix"
	}
	if json.Unmarshal(b, &response) != nil || response.Status != "success" || response.Data.ResultType != kind || len(response.Warnings) > 0 {
		return resourceQueryResult{err: errors.New("metric response invalid or partial")}
	}
	if len(response.Data.Result) > 20000 {
		return resourceQueryResult{err: errors.New("metric series limit reached")}
	}
	samples := 0
	for _, row := range response.Data.Result {
		samples += len(row.Values) + 1
		if samples > 300000 {
			return resourceQueryResult{err: errors.New("metric sample limit reached")}
		}
	}
	return resourceQueryResult{rows: response.Data.Result}
}
func resourcePointer(v float64) *float64 { return &v }
func resourceSum(m map[string]float64) *float64 {
	if len(m) == 0 {
		return nil
	}
	v := 0.
	for _, x := range m {
		v += x
	}
	return &v
}
func resourceWholeSum(m map[string]float64, inventory map[string]float64, positive bool) (*float64, *int) {
	if len(inventory) == 0 {
		return nil, nil
	}
	v := 0.
	missing := 0
	for id := range inventory {
		x, ok := m[id]
		if !ok || positive && x <= 0 {
			missing++
		} else {
			v += x
		}
	}
	if missing > 0 {
		return nil, &missing
	}
	return &v, &missing
}
func resourceStep(hours int) int {
	if hours == 168 {
		return 1800
	}
	return 300
}
func resourceEmpty(now time.Time, o resourceOptions) resourceResult {
	return resourceResult{GeneratedAt: now, Hours: o.hours, ResolutionSeconds: resourceStep(o.hours), Warnings: []string{}, Notes: []string{"CPU usage is a five-minute rate in cores; memory is working-set bytes, not allocation.", "Requests/limits sum regular containers in Pending or Running pods; scheduler-effective pod requirements can differ (init containers, pod-level resources, overhead).", "Limits are unknown when any inventoried container has no reported positive limit; missing metrics are never zero.", "PVC used/capacity require kubelet volume statistics for every inventoried claim; unmounted or unsupported volumes may be unknown.", "Host usage comes from node-exporter: CPU is five-minute non-idle logical cores (including I/O wait); memory is total minus available bytes. Node names require an unambiguous node_uname_info mapping to Kubernetes allocatable nodes. Host memory is not container working-set memory. Allocatable includes all observed nodes, not only Ready nodes; host usage/allocatable may exceed 100%.", "ResourceQuota usage is allocation/accounting, not measured runtime CPU or memory usage; separate quota objects may overlap and are not added together."}, Namespaces: []resourceNamespace{}, Nodes: []resourceNode{}}
}
func (s *Server) collectResources(r *http.Request, a access, o resourceOptions, now time.Time) resourceResult {
	queries := resourceQueries(o, a)
	results := make(map[string]resourceQueryResult, len(queries))
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan resourceQuery)
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for q := range jobs {
				result := s.fetchResourceQuery(r, q, now, o.hours, resourceStep(o.hours))
				mu.Lock()
				previous := results[q.name]
				previous.rows = append(previous.rows, result.rows...)
				if result.err != nil {
					previous.err = result.err
				}
				results[q.name] = previous
				mu.Unlock()
			}
		}()
	}
	for _, q := range queries {
		chunks := []resourceQuery{q}
		// Long request-history joins are expensive on high-churn clusters. Daily chunks
		// share the same four-worker pool and deadline as all other resource queries.
		if o.hours == 168 && strings.HasSuffix(q.name, "RequestsHistory") {
			chunks = nil
			for start := now.Add(-168 * time.Hour); start.Before(now); start = start.Add(24 * time.Hour) {
				part := q
				part.rangeStart = start
				part.rangeEnd = start.Add(24 * time.Hour)
				chunks = append(chunks, part)
			}
		}
		for _, part := range chunks {
			select {
			case jobs <- part:
			case <-r.Context().Done():
				mu.Lock()
				previous := results[q.name]
				previous.err = r.Context().Err()
				results[q.name] = previous
				mu.Unlock()
			}
		}
	}
	close(jobs)
	wg.Wait()
	return buildResources(now, o, a, queries, results)
}
func buildResources(now time.Time, o resourceOptions, a access, queries []resourceQuery, results map[string]resourceQueryResult) resourceResult {
	result := resourceEmpty(now, o)
	// Deduplicate defensively even if an upstream proxy ignores aggregation.
	metrics := map[string]map[string]map[string]float64{} // metric -> namespace -> resource identity -> maximum
	histories := map[string]map[string]map[int64]float64{}
	nodes := map[string]map[string]float64{}
	nodeHistory := map[string]map[int64]float64{}
	namespaceSet := map[string]bool{}
	quotaValues := map[string]map[string]resourceQuota{}
	for _, q := range queries {
		got, exists := results[q.name]
		if !exists || got.err != nil {
			result.Incomplete = true
			result.Warnings = append(result.Warnings, q.name+": unavailable or query limit reached")
			if len(got.rows) == 0 {
				continue
			}
		}
		metrics[q.name] = map[string]map[string]float64{}
		histories[q.name] = map[string]map[int64]float64{}
		nodes[q.name] = map[string]float64{}
		nodeHistory[q.name] = map[int64]float64{}
		for _, sample := range got.rows {
			ns := sample.Metric["namespace"]
			if !q.node && (ns == "" || !a.allowed(sample.Metric) || (o.namespace != "" && ns != o.namespace)) {
				continue
			}
			if q.node && !a.operator {
				continue
			}
			if !q.node {
				if len(namespaceSet) >= 512 && !namespaceSet[ns] {
					result.Incomplete = true
					continue
				}
				namespaceSet[ns] = true
			}
			if q.history {
				for _, pair := range sample.Values {
					ts, value, valid := point(pair)
					if !valid || value < 0 || ts < now.Add(-time.Duration(o.hours)*time.Hour).Unix() || ts > now.Unix() {
						continue
					}
					if q.node {
						old, ok := nodeHistory[q.name][ts]
						if !ok || value > old {
							nodeHistory[q.name][ts] = value
						}
					} else {
						if histories[q.name][ns] == nil {
							histories[q.name][ns] = map[int64]float64{}
						}
						old, ok := histories[q.name][ns][ts]
						if !ok || value > old {
							histories[q.name][ns][ts] = value
						}
					}
				}
				continue
			}
			ts, value, valid := point(sample.Value)
			if !valid || value < 0 || now.Unix()-ts > 300 || ts > now.Unix()+60 {
				continue
			}
			if q.name == "quotas" {
				name, resource, kind := sample.Metric["resourcequota"], sample.Metric["resource"], sample.Metric["type"]
				if name == "" || resource == "" || (kind != "hard" && kind != "used") {
					continue
				}
				if quotaValues[ns] == nil {
					quotaValues[ns] = map[string]resourceQuota{}
				}
				key := name + "\x00" + resource
				quota := quotaValues[ns][key]
				quota.Name = name
				quota.Resource = resource
				if kind == "hard" {
					if quota.Hard == nil || value > *quota.Hard {
						quota.Hard = resourcePointer(value)
					}
				} else if quota.Used == nil || value > *quota.Used {
					quota.Used = resourcePointer(value)
				}
				quotaValues[ns][key] = quota
				continue
			}
			if q.node {
				node := sample.Metric["node"]
				if node == "" {
					continue
				}
				old, ok := nodes[q.name][node]
				if !ok || value > old {
					nodes[q.name][node] = value
				}
				continue
			}
			identity := sample.Metric["pod"] + "\x00" + sample.Metric["container"]
			if strings.HasPrefix(q.name, "pvc") {
				identity = sample.Metric["persistentvolumeclaim"]
				if identity == "" {
					continue
				}
			} else if sample.Metric["container"] == "" || sample.Metric["container"] == "POD" || sample.Metric["pod"] == "" {
				continue
			}
			if metrics[q.name][ns] == nil {
				metrics[q.name][ns] = map[string]float64{}
			}
			old, ok := metrics[q.name][ns][identity]
			if !ok || value > old {
				metrics[q.name][ns][identity] = value
			}
		}
	}
	if o.namespace != "" {
		namespaceSet[o.namespace] = true
	} else if !a.operator {
		for _, ns := range a.namespaceList() {
			namespaceSet[ns] = true
		}
	}
	namespaces := []string{}
	for ns := range namespaceSet {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)
	lookup := func(name, ns string) map[string]float64 { return metrics[name][ns] }
	for _, ns := range namespaces {
		row := resourceNamespace{Namespace: ns, Trend: []resourcePoint{}, Quotas: []resourceQuota{}}
		for _, quota := range quotaValues[ns] {
			row.Quotas = append(row.Quotas, quota)
		}
		sort.Slice(row.Quotas, func(i, j int) bool {
			if row.Quotas[i].Name != row.Quotas[j].Name {
				return row.Quotas[i].Name < row.Quotas[j].Name
			}
			return row.Quotas[i].Resource < row.Quotas[j].Resource
		})
		row.CPUUsageCores = resourceSum(lookup("cpuUsage", ns))
		row.MemoryUsageBytes = resourceSum(lookup("memoryUsage", ns))
		inventory := lookup("containers", ns)
		row.CPURequestsCores, _ = resourceWholeSum(lookup("cpuRequests", ns), inventory, false)
		row.MemoryRequestsBytes, _ = resourceWholeSum(lookup("memoryRequests", ns), inventory, false)
		if qr, ok := results["cpuLimits"]; ok && qr.err == nil {
			row.CPULimitsCores, row.UnlimitedCPUContainers = resourceWholeSum(lookup("cpuLimits", ns), inventory, true)
		}
		if qr, ok := results["memoryLimits"]; ok && qr.err == nil {
			row.MemoryLimitsBytes, row.UnlimitedMemoryContainers = resourceWholeSum(lookup("memoryLimits", ns), inventory, true)
		}
		pvcInventory := lookup("pvcs", ns)
		row.PVCRequestedBytes, _ = resourceWholeSum(lookup("pvcRequests", ns), pvcInventory, false)
		row.PVCCapacityBytes, _ = resourceWholeSum(lookup("pvcCapacity", ns), pvcInventory, true)
		row.PVCUsedBytes, _ = resourceWholeSum(lookup("pvcUsed", ns), pvcInventory, false)
		if row.CPUUsageCores == nil || row.MemoryUsageBytes == nil || row.CPURequestsCores == nil || row.MemoryRequestsBytes == nil || row.CPULimitsCores == nil || row.MemoryLimitsBytes == nil {
			result.Incomplete = true
			result.Warnings = append(result.Warnings, ns+": some container metrics or configured limits are unavailable/incomplete")
		}
		if len(pvcInventory) > 0 && (row.PVCRequestedBytes == nil || row.PVCCapacityBytes == nil || row.PVCUsedBytes == nil) {
			result.Incomplete = true
			result.Warnings = append(result.Warnings, ns+": some PVC statistics are unavailable/incomplete")
		}
		// Aligned timeline retains null points for missing history, instead of connecting across gaps.
		for ts := now.Add(-time.Duration(o.hours) * time.Hour).Unix(); ts <= now.Unix(); ts += int64(result.ResolutionSeconds) {
			p := resourcePoint{At: time.Unix(ts, 0).UTC()}
			for name, dest := range map[string]**float64{"cpuUsageHistory": &p.CPUUsageCores, "memoryUsageHistory": &p.MemoryUsageBytes, "cpuRequestsHistory": &p.CPURequestsCores, "memoryRequestsHistory": &p.MemoryRequestsBytes} {
				if value, ok := histories[name][ns][ts]; ok {
					*dest = resourcePointer(value)
				}
			}
			row.Trend = append(row.Trend, p)
		}
		for _, p := range row.Trend {
			if p.CPUUsageCores == nil || p.MemoryUsageBytes == nil || p.CPURequestsCores == nil || p.MemoryRequestsBytes == nil {
				result.Incomplete = true
				result.Warnings = append(result.Warnings, ns+": resource history has missing samples; gaps are unknown")
				break
			}
		}
		result.Namespaces = append(result.Namespaces, row)
	}
	if a.operator {
		result.Cluster = &resourceCluster{Trend: []resourcePoint{}}
		nodeSet := map[string]bool{}
		for _, values := range nodes {
			for node := range values {
				nodeSet[node] = true
			}
		}
		nodeNames := []string{}
		for node := range nodeSet {
			nodeNames = append(nodeNames, node)
		}
		sort.Strings(nodeNames)
		nodeInventory := map[string]float64{}
		for _, node := range nodeNames {
			nodeInventory[node] = 1
			row := resourceNode{Node: node}
			for name, dest := range map[string]**float64{"nodeCPUAllocatable": &row.CPUAllocatableCores, "nodeMemoryAllocatable": &row.MemoryAllocatableBytes, "nodeCPUUsage": &row.CPUUsageCores, "nodeMemoryUsage": &row.MemoryUsageBytes} {
				if value, ok := nodes[name][node]; ok {
					*dest = resourcePointer(value)
				}
			}
			result.Nodes = append(result.Nodes, row)
		}
		for name, dest := range map[string]**float64{"nodeCPUAllocatable": &result.Cluster.CPUAllocatableCores, "nodeMemoryAllocatable": &result.Cluster.MemoryAllocatableBytes, "nodeCPUUsage": &result.Cluster.CPUUsageCores, "nodeMemoryUsage": &result.Cluster.MemoryUsageBytes} {
			*dest, _ = resourceWholeSum(nodes[name], nodeInventory, false)
			if *dest == nil {
				result.Incomplete = true
				result.Warnings = append(result.Warnings, name+": cluster total requires all observed nodes")
			}
		}
		for ts := now.Add(-time.Duration(o.hours) * time.Hour).Unix(); ts <= now.Unix(); ts += int64(result.ResolutionSeconds) {
			p := resourcePoint{At: time.Unix(ts, 0).UTC()}
			for name, dest := range map[string]**float64{"nodeCPUAllocatableHistory": &p.CPUAllocatableCores, "nodeMemoryAllocatableHistory": &p.MemoryAllocatableBytes, "nodeCPUUsageHistory": &p.CPUUsageCores, "nodeMemoryUsageHistory": &p.MemoryUsageBytes} {
				if value, ok := nodeHistory[name][ts]; ok {
					*dest = resourcePointer(value)
				}
			}
			result.Cluster.Trend = append(result.Cluster.Trend, p)
		}
		for _, p := range result.Cluster.Trend {
			if p.CPUUsageCores == nil || p.MemoryUsageBytes == nil || p.CPUAllocatableCores == nil || p.MemoryAllocatableBytes == nil {
				result.Incomplete = true
				result.Warnings = append(result.Warnings, "Cluster resource history has missing node samples; gaps are unknown")
				break
			}
		}
	}
	return result
}
func demoResources(now time.Time, o resourceOptions, a access) resourceResult {
	result := resourceEmpty(now, o)
	result.Notes = append(result.Notes, "Synthetic resource data; no cluster resource measurements.")
	for i, ns := range []string{"payments", "data-platform", "rook-ceph", "kube-system"} {
		if !a.allowed(map[string]string{"namespace": ns}) || o.namespace != "" && o.namespace != ns {
			continue
		}
		base := float64(i + 1)
		zero := 0
		row := resourceNamespace{Quotas: []resourceQuota{{Name: "namespace-budget", Resource: "requests.cpu", Hard: resourcePointer(base * 4), Used: resourcePointer(base)}, {Name: "namespace-budget", Resource: "requests.memory", Hard: resourcePointer(base * 8 * (1 << 30)), Used: resourcePointer(base * 2 * (1 << 30))}, {Name: "namespace-budget", Resource: "requests.storage", Hard: resourcePointer(base * 100 * (1 << 30)), Used: resourcePointer(base * 50 * (1 << 30))}}, Namespace: ns, CPUUsageCores: resourcePointer(base * .6), MemoryUsageBytes: resourcePointer(base * 1.8 * (1 << 30)), CPURequestsCores: resourcePointer(base), MemoryRequestsBytes: resourcePointer(base * 2 * (1 << 30)), CPULimitsCores: resourcePointer(base * 2), MemoryLimitsBytes: resourcePointer(base * 4 * (1 << 30)), PVCRequestedBytes: resourcePointer(base * 50 * (1 << 30)), PVCCapacityBytes: resourcePointer(base * 50 * (1 << 30)), PVCUsedBytes: resourcePointer(base * 31 * (1 << 30)), UnlimitedCPUContainers: &zero, UnlimitedMemoryContainers: &zero, Trend: []resourcePoint{}}
		for ts := now.Add(-time.Duration(o.hours) * time.Hour).Unix(); ts <= now.Unix(); ts += int64(result.ResolutionSeconds) {
			factor := .7 + float64((ts/int64(result.ResolutionSeconds))%11)/20
			row.Trend = append(row.Trend, resourcePoint{At: time.Unix(ts, 0).UTC(), CPUUsageCores: resourcePointer(base * .6 * factor), MemoryUsageBytes: resourcePointer(base * 1.8 * (1 << 30) * factor), CPURequestsCores: resourcePointer(base), MemoryRequestsBytes: resourcePointer(base * 2 * (1 << 30))})
		}
		result.Namespaces = append(result.Namespaces, row)
	}
	if a.operator {
		result.Cluster = &resourceCluster{CPUAllocatableCores: resourcePointer(32), MemoryAllocatableBytes: resourcePointer(120 * (1 << 30)), CPUUsageCores: resourcePointer(8.1), MemoryUsageBytes: resourcePointer(42 * (1 << 30)), Trend: []resourcePoint{}}
		for i := 0; i < 4; i++ {
			result.Nodes = append(result.Nodes, resourceNode{Node: fmt.Sprintf("worker-%02d", i+1), CPUAllocatableCores: resourcePointer(8), MemoryAllocatableBytes: resourcePointer(30 * (1 << 30)), CPUUsageCores: resourcePointer(8.1 / 4), MemoryUsageBytes: resourcePointer(10.5 * (1 << 30))})
		}
		for ts := now.Add(-time.Duration(o.hours) * time.Hour).Unix(); ts <= now.Unix(); ts += int64(result.ResolutionSeconds) {
			result.Cluster.Trend = append(result.Cluster.Trend, resourcePoint{At: time.Unix(ts, 0).UTC(), CPUUsageCores: resourcePointer(8.1), MemoryUsageBytes: resourcePointer(42 * (1 << 30)), CPUAllocatableCores: resourcePointer(32), MemoryAllocatableBytes: resourcePointer(120 * (1 << 30))})
		}
	}
	return result
}
