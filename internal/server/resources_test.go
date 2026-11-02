package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aai-labs/aai-alertmanager-ui/internal/config"
)

func resourceTestSample(at time.Time, labels map[string]string, value float64) resourceSample {
	ts, _ := json.Marshal(at.Unix())
	v, _ := json.Marshal(strconv.FormatFloat(value, 'f', -1, 64))
	return resourceSample{Metric: labels, Value: [2]json.RawMessage{ts, v}}
}
func resourceTestContainer(container string) map[string]string {
	return map[string]string{"namespace": "payments", "pod": "checkout", "container": container}
}
func TestResourcesDeduplicateAndIncompleteLimits(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	o := resourceOptions{hours: 24}
	a := access{operator: true}
	queries := resourceQueries(o, a)
	data := map[string]resourceQueryResult{}
	for _, q := range queries {
		data[q.name] = resourceQueryResult{}
	}
	data["containers"] = resourceQueryResult{rows: []resourceSample{resourceTestSample(now, resourceTestContainer("app"), 1), resourceTestSample(now, resourceTestContainer("app"), 1), resourceTestSample(now, resourceTestContainer("sidecar"), 1)}}
	data["cpuUsage"] = resourceQueryResult{rows: []resourceSample{resourceTestSample(now, resourceTestContainer("app"), 2), resourceTestSample(now, resourceTestContainer("app"), 2), resourceTestSample(now, resourceTestContainer("sidecar"), .5)}}
	data["cpuRequests"] = resourceQueryResult{rows: []resourceSample{resourceTestSample(now, resourceTestContainer("app"), 1)}}
	data["cpuLimits"] = resourceQueryResult{rows: []resourceSample{resourceTestSample(now, resourceTestContainer("app"), 4), resourceTestSample(now, resourceTestContainer("app"), 4)}}
	pvc := map[string]string{"namespace": "payments", "persistentvolumeclaim": "data"}
	data["pvcs"] = resourceQueryResult{rows: []resourceSample{resourceTestSample(now, pvc, 1)}}
	data["pvcUsed"] = resourceQueryResult{rows: []resourceSample{resourceTestSample(now, pvc, 20), resourceTestSample(now, pvc, 20)}}
	result := buildResources(now, o, a, queries, data)
	if len(result.Namespaces) != 1 {
		t.Fatalf("rows: %+v", result.Namespaces)
	}
	row := result.Namespaces[0]
	if row.CPUUsageCores == nil || *row.CPUUsageCores != 2.5 {
		t.Fatalf("duplicate scrape copies inflated usage: %+v", row.CPUUsageCores)
	}
	if row.CPULimitsCores != nil || row.UnlimitedCPUContainers == nil || *row.UnlimitedCPUContainers != 1 {
		t.Fatal("missing container limit became a misleading total")
	}
	if row.CPURequestsCores != nil {
		t.Fatal("missing requests became complete namespace requests")
	}
	if row.PVCUsedBytes == nil || *row.PVCUsedBytes != 20 {
		t.Fatal("PVC duplicates inflated used bytes")
	}
	if row.PVCCapacityBytes != nil || !result.Incomplete {
		t.Fatal("missing storage metric became zero/complete")
	}
	if row.Trend[0].CPUUsageCores != nil {
		t.Fatal("history gap filled with zero")
	}
}
func TestResourcesZeroIsDifferentFromMissing(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	o := resourceOptions{hours: 24}
	a := access{namespaces: map[string]bool{"payments": true}}
	queries := resourceQueries(o, a)
	data := map[string]resourceQueryResult{}
	for _, q := range queries {
		data[q.name] = resourceQueryResult{}
	}
	data["cpuUsage"] = resourceQueryResult{rows: []resourceSample{resourceTestSample(now, resourceTestContainer("app"), 0)}}
	got := buildResources(now, o, a, queries, data)
	row := got.Namespaces[0]
	if row.CPUUsageCores == nil || *row.CPUUsageCores != 0 {
		t.Fatal("real zero lost")
	}
	if row.MemoryUsageBytes != nil {
		t.Fatal("absent memory is not zero")
	}
	if got.Cluster != nil || len(got.Nodes) != 0 {
		t.Fatal("tenant got cluster details")
	}
}
func TestResourcesNamespaceScopingBeforeQuery(t *testing.T) {
	a := access{namespaces: map[string]bool{"team.a": true, "team-b": true}}
	q := resourceQueries(resourceOptions{hours: 24}, a)
	for _, item := range q {
		if item.node || strings.Contains(item.query, "kube_node_status") || strings.Contains(item.query, `id="/"`) {
			t.Fatalf("tenant node query: %s", item.query)
		}
		if !strings.Contains(item.query, `namespace=~"team-b|team\\.a"`) {
			t.Fatalf("scope missing: %s", item.query)
		}
	}
	for _, suffix := range []string{"?namespace=private", "?query=up", "?hours=720", "?hours=24&hours=168"} {
		if _, err := resourceOptionsFor(httptest.NewRequest("GET", "/api/resources"+suffix, nil), a); err == nil {
			t.Fatalf("accepted %s", suffix)
		}
	}
}
func TestResourcesDefensiveScopeAndNodeIdentity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	o := resourceOptions{hours: 24, namespace: "payments"}
	op := access{operator: true}
	queries := resourceQueries(o, op)
	data := map[string]resourceQueryResult{}
	for _, q := range queries {
		data[q.name] = resourceQueryResult{}
	}
	data["nodeCPUAllocatable"] = resourceQueryResult{rows: []resourceSample{resourceTestSample(now, map[string]string{"node": "worker-a", "namespace": "monitoring"}, 8), resourceTestSample(now, map[string]string{"node": "worker-a", "namespace": "monitoring-copy"}, 8)}}
	data["cpuUsage"] = resourceQueryResult{rows: []resourceSample{resourceTestSample(now, map[string]string{"namespace": "private", "pod": "x", "container": "app"}, 999)}}
	got := buildResources(now, o, op, queries, data)
	if len(got.Nodes) != 1 || got.Cluster.CPUAllocatableCores == nil || *got.Cluster.CPUAllocatableCores != 8 {
		t.Fatal("node exporter namespace was treated as resource ownership or duplicates counted")
	}
	if len(got.Namespaces) != 1 || got.Namespaces[0].CPUUsageCores != nil {
		t.Fatal("unselected namespace leaked")
	}
	tenant := access{namespaces: map[string]bool{"payments": true}}
	got = buildResources(now, o, tenant, queries, data)
	if len(got.Nodes) != 0 || got.Cluster != nil {
		t.Fatal("tenant defensive node boundary failed")
	}
}
func TestResourcesFailuresRemainUnknown(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	a := access{namespaces: map[string]bool{"payments": true}}
	o := resourceOptions{hours: 168}
	queries := resourceQueries(o, a)
	data := map[string]resourceQueryResult{}
	for _, q := range queries {
		data[q.name] = resourceQueryResult{err: errors.New("failed")}
	}
	got := buildResources(now, o, a, queries, data)
	if !got.Incomplete || len(got.Warnings) == 0 || got.Namespaces[0].CPUUsageCores != nil || got.Namespaces[0].UnlimitedCPUContainers != nil {
		t.Fatal("upstream failure is not an unknown result")
	}
	if got.ResolutionSeconds != 1800 || len(got.Namespaces[0].Trend) != 337 {
		t.Fatal("7-day bounded resolution wrong")
	}
}
func TestResourcesSnapshotAndHistoryRequestsBounded(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	active := 0
	peak := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		params := r.URL.Query()
		if params.Get("timeout") != "8s" {
			t.Error("missing query timeout")
		}
		query := params.Get("query")
		if !strings.Contains(query, `namespace=~"payments"`) {
			t.Errorf("query not scoped: %s", query)
		}
		kind := "vector"
		if strings.HasSuffix(r.URL.Path, "query_range") {
			kind = "matrix"
			if params.Get("step") != "300" {
				t.Error("unexpected resolution")
			}
		}
		time.Sleep(time.Millisecond)
		jsonResponse(w, 200, map[string]any{"status": "success", "data": map[string]any{"resultType": kind, "result": []any{}}})
	}))
	defer up.Close()
	s, err := New(config.Config{ListenAddr: ":8080", Alertmanager: config.Source{URL: up.URL}, Prometheus: config.Source{URL: up.URL}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	run := func() {
		w := httptest.NewRecorder()
		s.resources(w, httptest.NewRequest("GET", "/api/resources", nil), access{namespaces: map[string]bool{"payments": true}})
		if w.Code != 200 {
			t.Fatalf("status%d %s", w.Code, w.Body)
		}
	}
	run()
	mu.Lock()
	first := calls
	limit := peak
	mu.Unlock()
	if first != 16 || limit > 4 {
		t.Fatalf("query budget calls%d concurrency%d", first, limit)
	}
	run()
	mu.Lock()
	second := calls
	mu.Unlock()
	if second != first {
		t.Fatal("cache did not suppress identical requests")
	}
}
func TestResourcesDemoScopes(t *testing.T) {
	now := time.Now().UTC()
	got := demoResources(now, resourceOptions{hours: 24}, access{namespaces: map[string]bool{"payments": true}})
	if len(got.Namespaces) != 1 || got.Namespaces[0].Namespace != "payments" || got.Cluster != nil || len(got.Nodes) > 0 {
		t.Fatal("demo scope failed")
	}
}

func TestResourcesQuotasDeduplicateAndKeepDistinctBudgets(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	o := resourceOptions{hours: 24}
	a := access{operator: true}
	q := resourceQueries(o, a)
	data := map[string]resourceQueryResult{}
	data["quotas"] = resourceQueryResult{rows: []resourceSample{resourceTestSample(now, map[string]string{"namespace": "payments", "resourcequota": "budget", "resource": "requests.cpu", "type": "hard"}, 4), resourceTestSample(now, map[string]string{"namespace": "payments", "resourcequota": "budget", "resource": "requests.cpu", "type": "hard"}, 4), resourceTestSample(now, map[string]string{"namespace": "payments", "resourcequota": "budget", "resource": "requests.cpu", "type": "used"}, 1), resourceTestSample(now, map[string]string{"namespace": "payments", "resourcequota": "another-budget", "resource": "requests.cpu", "type": "hard"}, 2)}}
	got := buildResources(now, o, a, q, data)
	if len(got.Namespaces) != 1 || len(got.Namespaces[0].Quotas) != 2 {
		t.Fatal("overlapping quotas merged")
	}
	quotas := got.Namespaces[0].Quotas
	if *quotas[1].Hard != 4 || *quotas[1].Used != 1 || quotas[0].Used != nil {
		t.Fatal("quota duplicate or missing-value semantics wrong")
	}
}
func TestResourcesQueryDedupAndActiveAllocations(t *testing.T) {
	for _, q := range resourceQueries(resourceOptions{hours: 24}, access{operator: true}) {
		switch q.name {
		case "cpuUsageHistory", "memoryUsageHistory":
			if !strings.Contains(q.query, "sum by (namespace) (max by (namespace,pod,container)") {
				t.Errorf("sum before scrape dedup: %s", q.query)
			}
		case "cpuUsage":
			for _, want := range []string{`container!=""`, `container!="POD"`, `cpu=~"total|"`, `[5m]`} {
				if !strings.Contains(q.query, want) {
					t.Errorf("CPU query missing %s", want)
				}
			}
		case "cpuRequests", "memoryRequests", "cpuLimits", "memoryLimits":
			if !strings.Contains(q.query, `phase=~"Pending|Running"`) {
				t.Errorf("completed pod allocations included: %s", q.query)
			}
		}
	}
}

func TestResourcesNodeExporterMappingAndHistoryCompleteness(t *testing.T) {
	queries := resourceQueries(resourceOptions{hours: 24}, access{operator: true})
	for _, q := range queries {
		if q.name == "nodeCPUUsage" || q.name == "nodeMemoryUsage" {
			for _, want := range []string{"node_uname_info", "count by (instance,job)", "== 1", `"node", "$1", "nodename", "(.+)"`, "and on (node)", "kube_node_status_allocatable"} {
				if !strings.Contains(q.query, want) {
					t.Errorf("missing host identity/completeness guard %s: %s", want, q.query)
				}
			}
			if strings.Contains(q.query, "container_memory") || strings.Contains(q.query, "container_cpu") {
				t.Error("host definition silently falls back to different cgroup metric")
			}
		}
		if q.node && q.history {
			if !strings.Contains(q.query, "and (count (") || !strings.Contains(q.query, ") == count (") {
				t.Fatalf("partial-node history sum allowed: %s", q.query)
			}
		}
	}
}

func TestResourcesSevenDayRequestHistoryUsesDailyChunks(t *testing.T) {
	var mu sync.Mutex
	chunks := 0
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		kind := "vector"
		if strings.HasSuffix(r.URL.Path, "query_range") {
			kind = "matrix"
			if strings.Contains(q.Get("query"), "kube_pod_container_resource_requests") {
				start, _ := strconv.ParseInt(q.Get("start"), 10, 64)
				end, _ := strconv.ParseInt(q.Get("end"), 10, 64)
				if end-start > 86400 || q.Get("step") != "1800" {
					t.Errorf("unbounded request-history chunk: %s", r.URL.RawQuery)
				}
				mu.Lock()
				chunks++
				mu.Unlock()
			}
		}
		mu.Lock()
		calls++
		mu.Unlock()
		jsonResponse(w, 200, map[string]any{"status": "success", "data": map[string]any{"resultType": kind, "result": []any{}}})
	}))
	defer up.Close()
	s, err := New(config.Config{ListenAddr: ":8080", Alertmanager: config.Source{URL: up.URL}, Prometheus: config.Source{URL: up.URL}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := s.collectResources(httptest.NewRequest("GET", "/api/resources", nil), access{namespaces: map[string]bool{"payments": true}}, resourceOptions{hours: 168}, time.Now().UTC().Truncate(time.Minute))
	if chunks != 14 || calls != 28 || got.ResolutionSeconds != 1800 {
		t.Fatalf("daily chunk budget: chunks%d calls%d", chunks, calls)
	}
}
func TestResourcesPartialHistoryPreservesKnownSamples(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	start := now.Add(-24 * time.Hour)
	sample := resourceTestSample(start, map[string]string{"namespace": "payments"}, 1.5)
	sample.Values = [][2]json.RawMessage{sample.Value}
	sample.Value = [2]json.RawMessage{}
	data := map[string]resourceQueryResult{"cpuRequestsHistory": {rows: []resourceSample{sample}, err: errors.New("a different chunk failed")}}
	got := buildResources(now, resourceOptions{hours: 24}, access{namespaces: map[string]bool{"payments": true}}, []resourceQuery{{name: "cpuRequestsHistory", history: true}}, data)
	if !got.Incomplete || got.Namespaces[0].Trend[0].CPURequestsCores == nil || *got.Namespaces[0].Trend[0].CPURequestsCores != 1.5 || got.Namespaces[0].Trend[1].CPURequestsCores != nil {
		t.Fatal("partial history lost valid samples or fabricated missing samples")
	}
}
