package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const workloadsMaxSeries = 10000
const workloadsMaxAge = 180

var workloadQuerySlots = make(chan struct{}, 4)

type workloadsCacheEntry = jsonCacheEntry
type workloadSource struct {
	State          string     `json:"state"`
	OldestSampleAt *time.Time `json:"oldestSampleAt,omitempty"`
	MaxAgeSeconds  int        `json:"maxAgeSeconds"`
}
type workloadCounts struct {
	Ready       int `json:"ready"`
	NotReady    int `json:"notReady"`
	Unscheduled int `json:"unscheduled"`
	Completed   int `json:"completed"`
	Failed      int `json:"failed"`
	Unknown     int `json:"unknown"`
}
type workloadPod struct {
	Name           string     `json:"name"`
	UID            string     `json:"uid,omitempty"`
	Node           string     `json:"node,omitempty"`
	OwnerKind      string     `json:"ownerKind,omitempty"`
	OwnerName      string     `json:"ownerName,omitempty"`
	Phase          string     `json:"phase"`
	State          string     `json:"state"`
	WaitingReasons []string   `json:"waitingReasons"`
	CPUCores       *float64   `json:"cpuCores"`
	MemoryBytes    *float64   `json:"memoryBytes"`
	ObservedAt     *time.Time `json:"observedAt,omitempty"`
}
type workloadController struct {
	Kind       string     `json:"kind"`
	Name       string     `json:"name"`
	Desired    *float64   `json:"desired"`
	Ready      *float64   `json:"ready"`
	Available  *float64   `json:"available"`
	State      string     `json:"state"`
	ObservedAt *time.Time `json:"observedAt,omitempty"`
}
type workloadService struct {
	Name                string     `json:"name"`
	Type                string     `json:"type,omitempty"`
	ClusterIP           string     `json:"clusterIP,omitempty"`
	EndpointsReady      *int       `json:"endpointsReady"`
	Reachability        string     `json:"reachability"`
	ProbeTargets        int        `json:"probeTargets"`
	FailedProbeTargets  int        `json:"failedProbeTargets"`
	UnknownProbeTargets int        `json:"unknownProbeTargets"`
	ObservedAt          *time.Time `json:"observedAt,omitempty"`
}
type workloadNamespace struct {
	Namespace   string               `json:"namespace"`
	Pods        []workloadPod        `json:"pods"`
	Controllers []workloadController `json:"controllers"`
	Services    []workloadService    `json:"services"`
	Counts      workloadCounts       `json:"counts"`
}
type workloadsResult struct {
	GeneratedAt time.Time           `json:"generatedAt"`
	Incomplete  bool                `json:"incomplete"`
	Warnings    []string            `json:"warnings"`
	Source      workloadSource      `json:"source"`
	Namespaces  []workloadNamespace `json:"namespaces"`
}
type workloadSample struct {
	Labels map[string]string
	Value  float64
	At     time.Time
	Fresh  bool
}
type workloadVector struct {
	Metric map[string]string  `json:"metric"`
	Value  [2]json.RawMessage `json:"value"`
}
type workloadEnvelope struct {
	Status   string   `json:"status"`
	Warnings []string `json:"warnings"`
	Data     struct {
		ResultType string           `json:"resultType"`
		Result     []workloadVector `json:"result"`
	} `json:"data"`
}
type workloadQuery struct {
	name, expr string
	timestamps bool
}

func workloadNamespaceFor(r *http.Request, a access) (string, error) {
	ns := ""
	for k, v := range r.URL.Query() {
		if k != "namespace" || len(v) != 1 {
			return "", errors.New("only one namespace query parameter is supported")
		}
		ns = v[0]
	}
	if len(ns) > 253 {
		return "", errors.New("namespace is too long")
	}
	if ns != "" && !a.operator && !a.namespaces[ns] {
		return "", errors.New("namespace is not authorized")
	}
	return ns, nil
}
func workloadScope(ns string, a access) string {
	if ns != "" {
		return "namespace=" + strconv.Quote(ns)
	}
	if a.operator {
		return `namespace!=""`
	}
	allowed := a.namespaceList()
	if len(allowed) == 0 {
		return `namespace=~"a^"`
	}
	for i, n := range allowed {
		allowed[i] = regexp.QuoteMeta(n)
	}
	return "namespace=~" + strconv.Quote(strings.Join(allowed, "|"))
}
func workloadSelector(names []string, scope string) string {
	escaped := make([]string, len(names))
	for i, n := range names {
		escaped[i] = regexp.QuoteMeta(n)
	}
	return `{__name__=~` + strconv.Quote(strings.Join(escaped, "|")) + `,` + scope + `}`
}
func workloadKey(labels map[string]string) string { b, _ := json.Marshal(labels); return string(b) }
func (s *Server) workloads(w http.ResponseWriter, r *http.Request, a access) {
	ns, err := workloadNamespaceFor(r, a)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if s.config.Prometheus.URL == "" && !s.config.Demo {
		fail(w, 404, "Prometheus is required for workloads")
		return
	}
	now := time.Now().UTC()
	keyBytes, _ := json.Marshal([]any{ns, a.operator, a.namespaceList()})
	key := string(keyBytes)
	s.workloadsCacheMu.Lock()
	entry, ok := s.workloadsCache[key]
	s.workloadsCacheMu.Unlock()
	if ok && now.Sub(entry.at) < 30*time.Second {
		jsonResponse(w, 200, entry.body)
		return
	}
	var result workloadsResult
	if s.config.Demo {
		result = demoWorkloads(now, ns, a)
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		result = s.collectWorkloads(r.WithContext(ctx), a, ns, now)
	}
	if r.Context().Err() != nil {
		return
	}
	s.workloadsCacheMu.Lock()
	putJSONCache(&s.workloadsCache, key, result, time.Now())
	s.workloadsCacheMu.Unlock()
	jsonResponse(w, 200, result)
}
func (s *Server) queryWorkload(r *http.Request, expr string) ([]workloadVector, bool, error) {
	select {
	case workloadQuerySlots <- struct{}{}:
		defer func() { <-workloadQuerySlots }()
	case <-r.Context().Done():
		return nil, false, r.Context().Err()
	}
	params := url.Values{"query": {expr}, "timeout": {"8s"}, "limit": {strconv.Itoa(workloadsMaxSeries + 1)}}
	body, status, err := s.prom.request(r, "GET", "/api/v1/query?"+params.Encode(), nil)
	if err != nil || status != 200 {
		return nil, false, errors.New("workload metric query unavailable")
	}
	var response workloadEnvelope
	if err = json.Unmarshal(body, &response); err != nil || response.Status != "success" || response.Data.ResultType != "vector" {
		return nil, false, errors.New("invalid workload metric response")
	}
	if len(response.Data.Result) > workloadsMaxSeries {
		return nil, false, errors.New("workload metric series limit exceeded")
	}
	return response.Data.Result, len(response.Warnings) > 0, nil
}
func (s *Server) collectWorkloads(r *http.Request, a access, ns string, now time.Time) workloadsResult {
	scope := workloadScope(ns, a)
	groups := [][]string{
		{"kube_pod_info", "kube_pod_container_status_waiting_reason", "kube_pod_owner"},
		{"kube_pod_status_phase"},
		{"kube_pod_status_ready"},
		{"kube_pod_status_scheduled"},
		{"kube_deployment_spec_replicas", "kube_deployment_status_replicas_ready", "kube_deployment_status_replicas_available", "kube_statefulset_replicas", "kube_statefulset_status_replicas_ready", "kube_statefulset_status_replicas_available", "kube_daemonset_status_desired_number_scheduled", "kube_daemonset_status_number_ready", "kube_daemonset_status_number_available"},
		{"kube_service_info", "kube_service_spec_type", "kube_endpointslice_labels", "kube_endpointslice_endpoints"},
		{"container_memory_working_set_bytes"},
		{"probe_success"},
	}
	queries := []workloadQuery{}
	for i, g := range groups {
		expr := workloadSelector(g, scope)
		if g[0] == "probe_success" {
			// Scrape namespace/service labels describe the exporter, not the probed target.
			targetScope := strings.Replace(scope, "namespace", "target_namespace", 1)
			expr = workloadSelector(g, targetScope+`,target_service!=""`)
			expr = `label_replace(label_replace(` + expr + `,"namespace","$1","target_namespace","(.+)"),"service","$1","target_service","(.+)")`
		}
		if len(g) == 1 && g[0] == "container_memory_working_set_bytes" {
			expr = strings.TrimSuffix(expr, "}") + `,container!="",container!="POD",pod!=""}`
		}
		name := fmt.Sprint(i)
		queries = append(queries, workloadQuery{name, expr, false}, workloadQuery{name, `timestamp(label_replace(` + expr + `,"workload_metric","$1","__name__","(.+)"))`, true})
	}
	cpuSelector := `container_cpu_usage_seconds_total{` + scope + `,container!="",container!="POD",pod!="",cpu=~"total|"}`
	queries = append(queries, workloadQuery{"cpu", `rate(` + cpuSelector + `[5m])`, false}, workloadQuery{"cpu", `timestamp(` + cpuSelector + `)`, true})
	type answer struct {
		q       workloadQuery
		vectors []workloadVector
		warning bool
		err     error
	}
	ch := make(chan answer, len(queries))
	var wg sync.WaitGroup
	for _, q := range queries {
		wg.Add(1)
		go func(q workloadQuery) {
			defer wg.Done()
			v, w, e := s.queryWorkload(r, q.expr)
			ch <- answer{q, v, w, e}
		}(q)
	}
	wg.Wait()
	close(ch)
	raw := map[string][]workloadVector{}
	stamps := map[string]map[string]time.Time{}
	warnings := []string{}
	for ans := range ch {
		if ans.err != nil {
			warnings = append(warnings, "Some workload metrics could not be loaded.")
			continue
		}
		if ans.warning {
			warnings = append(warnings, "Prometheus returned a partial workload query warning.")
		}
		if !ans.q.timestamps {
			raw[ans.q.name] = ans.vectors
			continue
		}
		stamps[ans.q.name] = map[string]time.Time{}
		for _, v := range ans.vectors {
			labels := cloneWorkloadLabels(v.Metric)
			if ans.q.name != "cpu" {
				metric := labels["workload_metric"]
				if metric == "" {
					warnings = append(warnings, "Metric sample timestamps are incomplete.")
					continue
				}
				labels["__name__"] = metric
				delete(labels, "workload_metric")
			} else {
				delete(labels, "__name__")
			}
			val, err := workloadValue(v.Value[1])
			if err != nil || val <= 0 {
				continue
			}
			stamps[ans.q.name][workloadKey(labels)] = time.Unix(0, int64(val*1e9)).UTC()
		}
	}
	samples := map[string][]workloadSample{}
	for group, vectors := range raw {
		for _, v := range vectors {
			labels := cloneWorkloadLabels(v.Metric)
			if labels["namespace"] == "" || !a.allowed(labels) || (ns != "" && labels["namespace"] != ns) {
				continue
			}
			metric := labels["__name__"]
			if group == "cpu" {
				metric = "cpu"
				delete(labels, "__name__")
			}
			val, err := workloadValue(v.Value[1])
			if err != nil {
				warnings = append(warnings, "Invalid metric values were excluded.")
				continue
			}
			at := stamps[group][workloadKey(labels)]
			fresh := !at.IsZero() && now.Sub(at) <= workloadsMaxAge*time.Second && at.Sub(now) <= time.Minute
			if !fresh {
				warnings = append(warnings, "Some workload samples are stale or lack source timestamps.")
			}
			samples[metric] = append(samples[metric], workloadSample{labels, val, at, fresh})
		}
	}
	return assembleWorkloads(samples, now, a, warnings)
}
func cloneWorkloadLabels(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
func workloadValue(raw json.RawMessage) (float64, error) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return 0, errors.New("metric value is not a string")
	}
	v, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, errors.New("invalid metric value")
	}
	return v, nil
}
func workloadTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	v := t
	return &v
}
func workloadNumber(v float64) *float64 { return &v }
func workloadPodKey(l map[string]string) string {
	return l["namespace"] + "\x00" + l["pod"] + "\x00" + l["uid"]
}
func workloadMatchesPod(sample workloadSample, namespace string, p workloadPod) bool {
	return sample.Labels["namespace"] == namespace && sample.Labels["pod"] == p.Name && (sample.Labels["uid"] == "" || p.UID == "" || sample.Labels["uid"] == p.UID)
}
func workloadActiveValues(samples []workloadSample, namespace string, p workloadPod, label string) []string {
	values := map[string]bool{}
	for _, x := range samples {
		if x.Fresh && x.Value == 1 && workloadMatchesPod(x, namespace, p) {
			if v := x.Labels[label]; v != "" {
				values[v] = true
			}
		}
	}
	out := []string{}
	for v := range values {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
func singleWorkloadValue(values []string) string {
	if len(values) == 1 {
		return values[0]
	}
	return ""
}
func assembleWorkloads(samples map[string][]workloadSample, now time.Time, a access, warnings []string) workloadsResult {
	result := workloadsResult{GeneratedAt: now, Warnings: []string{}, Namespaces: []workloadNamespace{}, Source: workloadSource{State: "current", MaxAgeSeconds: workloadsMaxAge}}
	spaces := map[string]*workloadNamespace{}
	ensure := func(ns string) *workloadNamespace {
		if spaces[ns] == nil {
			spaces[ns] = &workloadNamespace{Namespace: ns, Pods: []workloadPod{}, Controllers: []workloadController{}, Services: []workloadService{}}
		}
		return spaces[ns]
	}
	for _, xs := range samples {
		for _, x := range xs {
			if !x.At.IsZero() && (result.Source.OldestSampleAt == nil || x.At.Before(*result.Source.OldestSampleAt)) {
				result.Source.OldestSampleAt = workloadTime(x.At)
			}
		}
	}
	pods := map[string]workloadSample{}
	for _, x := range samples["kube_pod_info"] {
		if x.Labels["pod"] == "" || x.Value != 1 {
			continue
		}
		key := workloadPodKey(x.Labels)
		if old, ok := pods[key]; !ok || x.At.After(old.At) {
			pods[key] = x
		}
	}
	podNames := map[string]int{}
	for _, x := range pods {
		podNames[x.Labels["namespace"]+"\x00"+x.Labels["pod"]]++
	}
	for _, x := range pods {
		ns := x.Labels["namespace"]
		p := workloadPod{Name: x.Labels["pod"], UID: x.Labels["uid"], OwnerKind: x.Labels["created_by_kind"], OwnerName: x.Labels["created_by_name"], Phase: "Unknown", State: "unknown", WaitingReasons: []string{}, ObservedAt: workloadTime(x.At)}
		if a.operator {
			p.Node = x.Labels["node"]
		}
		phase := singleWorkloadValue(workloadActiveValues(samples["kube_pod_status_phase"], ns, p, "phase"))
		if phase != "" {
			p.Phase = phase
		}
		scheduled := singleWorkloadValue(workloadActiveValues(samples["kube_pod_status_scheduled"], ns, p, "condition"))
		ready := singleWorkloadValue(workloadActiveValues(samples["kube_pod_status_ready"], ns, p, "condition"))
		if x.Fresh {
			switch {
			case phase == "Succeeded":
				p.State = "completed"
			case phase == "Failed":
				p.State = "failed"
			case phase == "Pending" && scheduled == "false":
				p.State = "unscheduled"
			case phase == "Running" && scheduled == "true" && ready == "true":
				p.State = "ready"
			case (phase == "Running" || phase == "Pending") && scheduled == "true" && ready == "false":
				p.State = "notReady"
			}
		}
		p.WaitingReasons = workloadActiveValues(samples["kube_pod_container_status_waiting_reason"], ns, p, "reason")
		if p.OwnerName == "" {
			for _, owner := range samples["kube_pod_owner"] {
				if owner.Fresh && owner.Value == 1 && workloadMatchesPod(owner, ns, p) && owner.Labels["owner_is_controller"] != "false" {
					p.OwnerKind = owner.Labels["owner_kind"]
					p.OwnerName = owner.Labels["owner_name"]
					break
				}
			}
		}
		if podNames[ns+"\x00"+p.Name] == 1 {
			p.CPUCores = workloadPodResource(samples["cpu"], ns, p)
			p.MemoryBytes = workloadPodResource(samples["container_memory_working_set_bytes"], ns, p)
		} else {
			warnings = append(warnings, "Resource usage is unknown for ambiguous pod identities.")
		}
		if p.State == "unknown" {
			warnings = append(warnings, "Some pod lifecycle metrics are missing, stale, or conflicting.")
		}
		space := ensure(ns)
		space.Pods = append(space.Pods, p)
		switch p.State {
		case "ready":
			space.Counts.Ready++
		case "notReady":
			space.Counts.NotReady++
		case "unscheduled":
			space.Counts.Unscheduled++
		case "completed":
			space.Counts.Completed++
		case "failed":
			space.Counts.Failed++
		default:
			space.Counts.Unknown++
		}
	}
	kinds := []struct{ kind, label, desired, ready, available string }{{"Deployment", "deployment", "kube_deployment_spec_replicas", "kube_deployment_status_replicas_ready", "kube_deployment_status_replicas_available"}, {"StatefulSet", "statefulset", "kube_statefulset_replicas", "kube_statefulset_status_replicas_ready", "kube_statefulset_status_replicas_available"}, {"DaemonSet", "daemonset", "kube_daemonset_status_desired_number_scheduled", "kube_daemonset_status_number_ready", "kube_daemonset_status_number_available"}}
	for _, kind := range kinds {
		keys := map[string]map[string]string{}
		for _, metric := range []string{kind.desired, kind.ready, kind.available} {
			for _, x := range samples[metric] {
				if name := x.Labels[kind.label]; name != "" {
					keys[x.Labels["namespace"]+"\x00"+name] = x.Labels
				}
			}
		}
		for _, l := range keys {
			ns, name := l["namespace"], l[kind.label]
			c := workloadController{Kind: kind.kind, Name: name, State: "unknown"}
			c.Desired, c.ObservedAt = workloadGauge(samples[kind.desired], ns, kind.label, name)
			c.Ready, _ = workloadGauge(samples[kind.ready], ns, kind.label, name)
			c.Available, _ = workloadGauge(samples[kind.available], ns, kind.label, name)
			if c.Desired != nil && c.Ready != nil && c.Available != nil {
				if *c.Ready >= *c.Desired && *c.Available >= *c.Desired {
					c.State = "ready"
				} else {
					c.State = "notReady"
				}
			}
			if c.State == "unknown" {
				warnings = append(warnings, "Some controller replica metrics are missing, stale, or conflicting.")
			}
			ensure(ns).Controllers = append(ensure(ns).Controllers, c)
		}
	}
	services := map[string]workloadSample{}
	for _, x := range samples["kube_service_info"] {
		ns, name := x.Labels["namespace"], x.Labels["service"]
		if name == "" || x.Value != 1 {
			continue
		}
		key := ns + "\x00" + name
		if old, ok := services[key]; !ok || x.At.After(old.At) {
			services[key] = x
		}
	}
	for _, x := range services {
		ns, name := x.Labels["namespace"], x.Labels["service"]
		svc := workloadService{Name: name, ClusterIP: x.Labels["cluster_ip"], Reachability: "unknown", ObservedAt: workloadTime(x.At)}
		types := map[string]bool{}
		for _, t := range samples["kube_service_spec_type"] {
			if t.Fresh && t.Value == 1 && t.Labels["namespace"] == ns && t.Labels["service"] == name {
				types[t.Labels["type"]] = true
			}
		}
		if len(types) == 1 {
			for typ := range types {
				svc.Type = typ
			}
		}
		if x.Fresh {
			svc.EndpointsReady = workloadEndpoints(samples, ns, name)
			targets := map[string]float64{}
			observedTargets := map[string]bool{}
			for _, probe := range samples["probe_success"] {
				if probe.Labels["namespace"] != ns || probe.Labels["service"] != name || probe.Labels["target_namespace"] != ns || probe.Labels["target_service"] != name {
					continue
				}
				target := probe.Labels["target"]
				if target == "" {
					target = probe.Labels["instance"]
				}
				if target == "" {
					continue
				}
				observedTargets[target] = true
				if !probe.Fresh || (probe.Value != 0 && probe.Value != 1) {
					continue
				}
				if old, ok := targets[target]; !ok || probe.Value < old {
					targets[target] = probe.Value
				}
			}
			svc.ProbeTargets = len(observedTargets)
			svc.UnknownProbeTargets = len(observedTargets) - len(targets)
			for _, value := range targets {
				if value == 0 {
					svc.FailedProbeTargets++
				}
			}
			if len(targets) > 0 {
				switch {
				case svc.UnknownProbeTargets > 0:
					if svc.FailedProbeTargets > 0 {
						svc.Reachability = "degraded"
					}
				case svc.FailedProbeTargets == 0:
					svc.Reachability = "reachable"
				case svc.FailedProbeTargets == len(targets):
					svc.Reachability = "failed"
				default:
					svc.Reachability = "degraded"
				}
			}
		}
		ensure(ns).Services = append(ensure(ns).Services, svc)
	}
	if len(pods) == 0 {
		warnings = append(warnings, "No pod inventory metrics were returned; workload coverage is unknown.")
	}
	seen := map[string]bool{}
	for _, w := range warnings {
		if !seen[w] {
			result.Warnings = append(result.Warnings, w)
			seen[w] = true
		}
	}
	sort.Strings(result.Warnings)
	if len(result.Warnings) > 0 {
		result.Incomplete = true
		result.Source.State = "partial"
	}
	if len(spaces) == 0 {
		result.Source.State = "unavailable"
		result.Incomplete = true
	}
	names := []string{}
	for ns := range spaces {
		names = append(names, ns)
	}
	sort.Strings(names)
	for _, ns := range names {
		space := spaces[ns]
		sort.Slice(space.Pods, func(i, j int) bool {
			if space.Pods[i].Name == space.Pods[j].Name {
				return space.Pods[i].UID < space.Pods[j].UID
			}
			return space.Pods[i].Name < space.Pods[j].Name
		})
		sort.Slice(space.Controllers, func(i, j int) bool {
			return space.Controllers[i].Kind+space.Controllers[i].Name < space.Controllers[j].Kind+space.Controllers[j].Name
		})
		sort.Slice(space.Services, func(i, j int) bool { return space.Services[i].Name < space.Services[j].Name })
		result.Namespaces = append(result.Namespaces, *space)
	}
	return result
}
func workloadGauge(xs []workloadSample, ns, label, name string) (*float64, *time.Time) {
	var value *float64
	var at *time.Time
	for _, x := range xs {
		if !x.Fresh || x.Labels["namespace"] != ns || x.Labels[label] != name || x.Value < 0 {
			continue
		}
		if value != nil && *value != x.Value {
			return nil, nil
		}
		value = workloadNumber(x.Value)
		if at == nil || x.At.Before(*at) {
			at = workloadTime(x.At)
		}
	}
	return value, at
}
func workloadPodResource(xs []workloadSample, ns string, p workloadPod) *float64 {
	containers := map[string]float64{}
	for _, x := range xs {
		if !x.Fresh || x.Value < 0 || !workloadMatchesPod(x, ns, p) {
			continue
		}
		name := x.Labels["container"]
		if name == "" || name == "POD" {
			continue
		}
		if old, ok := containers[name]; !ok || x.Value > old {
			containers[name] = x.Value
		}
	}
	if len(containers) == 0 {
		return nil
	}
	total := 0.0
	for _, v := range containers {
		total += v
	}
	return &total
}
func workloadEndpoints(samples map[string][]workloadSample, ns, name string) *int {
	slices := map[string]bool{}
	observed := map[string]bool{}
	owners := map[string]map[string]bool{}
	for _, x := range samples["kube_endpointslice_labels"] {
		slice, owner := x.Labels["endpointslice"], x.Labels["label_kubernetes_io_service_name"]
		if x.Value != 1 || x.Labels["namespace"] != ns || slice == "" || owner == "" {
			continue
		}
		if owner == name {
			observed[slice] = true
		}
		if x.Fresh {
			if owners[slice] == nil {
				owners[slice] = map[string]bool{}
			}
			owners[slice][owner] = true
			if owner == name {
				slices[slice] = true
			}
		}
	}
	for slice := range observed {
		if !slices[slice] || len(owners[slice]) != 1 {
			return nil
		}
	}
	if len(slices) == 0 {
		return nil
	}
	seen := map[string]bool{}
	present := map[string]bool{}
	states := map[string]string{}
	for _, x := range samples["kube_endpointslice_endpoints"] {
		slice := x.Labels["endpointslice"]
		if !x.Fresh || x.Value != 1 || x.Labels["namespace"] != ns || !slices[slice] {
			continue
		}
		present[slice] = true
		identity := slice + "\x00" + x.Labels["address"] + "\x00" + x.Labels["targetref_uid"]
		state := x.Labels["ready"] + "/" + x.Labels["terminating"]
		if previous, exists := states[identity]; exists && previous != state {
			return nil
		}
		states[identity] = state
		if x.Labels["ready"] == "true" && x.Labels["terminating"] != "true" {
			key := x.Labels["address"] + "\x00" + x.Labels["targetref_uid"]
			if key != "\x00" {
				seen[key] = true
			}
		}
	}
	for slice := range slices {
		if !present[slice] {
			return nil
		}
	}
	n := len(seen)
	return &n
}
func demoWorkloads(now time.Time, ns string, a access) workloadsResult {
	samples := map[string][]workloadSample{}
	add := func(metric, namespace string, labels map[string]string, value float64) {
		if ns != "" && ns != namespace {
			return
		}
		labels["namespace"] = namespace
		if !a.allowed(labels) {
			return
		}
		samples[metric] = append(samples[metric], workloadSample{labels, value, now.Add(-15 * time.Second), true})
	}
	for _, spec := range []struct{ ns, name, phase, ready, scheduled string }{{"payments", "checkout-0", "Running", "true", "true"}, {"payments", "checkout-1", "Running", "false", "true"}, {"data-platform", "import-pending", "Pending", "false", "false"}, {"data-platform", "backup-complete", "Succeeded", "false", "true"}, {"data-platform", "import-failed", "Failed", "false", "true"}, {"rook-ceph", "ceph-manager", "Running", "true", "true"}} {
		base := map[string]string{"pod": spec.name, "uid": spec.name + "-uid", "node": "worker-01", "created_by_kind": "ReplicaSet", "created_by_name": spec.name + "-rs"}
		add("kube_pod_info", spec.ns, cloneWorkloadLabels(base), 1)
		for metric, pair := range map[string][2]string{"kube_pod_status_phase": {"phase", spec.phase}, "kube_pod_status_ready": {"condition", spec.ready}, "kube_pod_status_scheduled": {"condition", spec.scheduled}} {
			l := cloneWorkloadLabels(base)
			l[pair[0]] = pair[1]
			add(metric, spec.ns, l, 1)
		}
		l := cloneWorkloadLabels(base)
		l["container"] = "app"
		add("cpu", spec.ns, cloneWorkloadLabels(l), .08)
		add("container_memory_working_set_bytes", spec.ns, l, 96*1024*1024)
	}
	for metric, value := range map[string]float64{"kube_deployment_spec_replicas": 2, "kube_deployment_status_replicas_ready": 1, "kube_deployment_status_replicas_available": 1} {
		add(metric, "payments", map[string]string{"deployment": "checkout"}, value)
	}
	add("kube_service_info", "payments", map[string]string{"service": "checkout", "cluster_ip": "10.43.0.10"}, 1)
	add("kube_service_spec_type", "payments", map[string]string{"service": "checkout", "type": "ClusterIP"}, 1)
	return assembleWorkloads(samples, now, a, nil)
}
