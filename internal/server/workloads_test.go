package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aai-labs/aai-alertmanager-ui/internal/config"
)

func wlSample(now time.Time, labels map[string]string, value float64) workloadSample {
	return workloadSample{Labels: labels, Value: value, At: now.Add(-10 * time.Second), Fresh: true}
}
func wlPodSamples(now time.Time, name, phase, ready, scheduled string) map[string][]workloadSample {
	out := map[string][]workloadSample{}
	base := map[string]string{"namespace": "ns", "pod": name, "uid": name + "-uid", "node": "worker"}
	out["kube_pod_info"] = []workloadSample{wlSample(now, base, 1)}
	for metric, pair := range map[string][2]string{"kube_pod_status_phase": {"phase", phase}, "kube_pod_status_ready": {"condition", ready}, "kube_pod_status_scheduled": {"condition", scheduled}} {
		l := cloneWorkloadLabels(base)
		l[pair[0]] = pair[1]
		out[metric] = []workloadSample{wlSample(now, l, 1)}
	}
	return out
}
func TestWorkloadPodLifecycleStatesExclusive(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct{ phase, ready, scheduled, want string }{{"Running", "true", "true", "ready"}, {"Running", "false", "true", "notReady"}, {"Pending", "false", "false", "unscheduled"}, {"Succeeded", "false", "false", "completed"}, {"Failed", "false", "false", "failed"}, {"Unknown", "true", "true", "unknown"}, {"Running", "", "true", "unknown"}} {
		t.Run(tc.want+tc.phase, func(t *testing.T) {
			samples := wlPodSamples(now, "pod", tc.phase, tc.ready, tc.scheduled)
			result := assembleWorkloads(samples, now, access{operator: true}, nil)
			if len(result.Namespaces) != 1 || len(result.Namespaces[0].Pods) != 1 || result.Namespaces[0].Pods[0].State != tc.want {
				t.Fatalf("%+v", result)
			}
			counts := result.Namespaces[0].Counts
			if counts.Ready+counts.NotReady+counts.Unscheduled+counts.Completed+counts.Failed+counts.Unknown != 1 {
				t.Fatal("double counted", counts)
			}
		})
	}
}
func TestWorkloadMissingStaleAndConflictingMetricsUnknown(t *testing.T) {
	now := time.Now().UTC()
	for _, mode := range []string{"missing", "stale", "conflict", "stale-info"} {
		samples := wlPodSamples(now, "pod", "Running", "true", "true")
		switch mode {
		case "missing":
			delete(samples, "kube_pod_status_ready")
		case "stale":
			samples["kube_pod_status_ready"][0].Fresh = false
		case "stale-info":
			samples["kube_pod_info"][0].Fresh = false
		case "conflict":
			other := wlSample(now, map[string]string{"namespace": "ns", "pod": "pod", "uid": "pod-uid", "condition": "false"}, 1)
			samples["kube_pod_status_ready"] = append(samples["kube_pod_status_ready"], other)
		}
		r := assembleWorkloads(samples, now, access{operator: true}, nil)
		if r.Namespaces[0].Pods[0].State != "unknown" || !r.Incomplete {
			t.Fatal(mode, r)
		}
	}
	empty := assembleWorkloads(nil, now, access{operator: true}, nil)
	if !empty.Incomplete || empty.Source.State != "unavailable" {
		t.Fatal(empty)
	}
}
func TestWorkloadDedupResourceAndPodIdentity(t *testing.T) {
	now := time.Now().UTC()
	samples := wlPodSamples(now, "pod", "Running", "true", "true")
	copyInfo := samples["kube_pod_info"][0]
	copyInfo.Labels = cloneWorkloadLabels(copyInfo.Labels)
	copyInfo.Labels["instance"] = "replica-two"
	samples["kube_pod_info"] = append(samples["kube_pod_info"], copyInfo)
	for _, container := range []string{"app", "app", "sidecar"} {
		samples["cpu"] = append(samples["cpu"], wlSample(now, map[string]string{"namespace": "ns", "pod": "pod", "container": container}, .1))
	}
	result := assembleWorkloads(samples, now, access{}, nil)
	pods := result.Namespaces[0].Pods
	if len(pods) != 1 || pods[0].CPUCores == nil || *pods[0].CPUCores != .2 || pods[0].MemoryBytes != nil || pods[0].Node != "" {
		t.Fatal(pods)
	}
	replacement := copyInfo
	replacement.Labels = cloneWorkloadLabels(copyInfo.Labels)
	replacement.Labels["uid"] = "new-uid"
	samples["kube_pod_info"] = append(samples["kube_pod_info"], replacement)
	result = assembleWorkloads(samples, now, access{operator: true}, nil)
	if len(result.Namespaces[0].Pods) != 2 || result.Namespaces[0].Pods[0].CPUCores != nil || result.Namespaces[0].Pods[1].CPUCores != nil {
		t.Fatal("UID merged", result)
	}
}
func TestWorkloadReadinessDoesNotImplyReachability(t *testing.T) {
	now := time.Now().UTC()
	samples := wlPodSamples(now, "pod", "Running", "true", "true")
	samples["kube_service_info"] = []workloadSample{wlSample(now, map[string]string{"namespace": "ns", "service": "web", "cluster_ip": "10.0.0.1"}, 1)}
	samples["kube_endpointslice_endpoints"] = []workloadSample{wlSample(now, map[string]string{"namespace": "ns", "endpointslice": "web-abc", "service": "web", "address": "10.0.0.2", "ready": "true"}, 1)}
	r := assembleWorkloads(samples, now, access{operator: true}, nil)
	svc := r.Namespaces[0].Services[0]
	if svc.EndpointsReady != nil || svc.Reachability != "unknown" {
		t.Fatal("inferred service health", svc)
	}
	samples["kube_endpointslice_labels"] = []workloadSample{wlSample(now, map[string]string{"namespace": "ns", "endpointslice": "web-abc", "label_kubernetes_io_service_name": "web"}, 1)}
	r = assembleWorkloads(samples, now, access{operator: true}, nil)
	svc = r.Namespaces[0].Services[0]
	if svc.EndpointsReady == nil || *svc.EndpointsReady != 1 || svc.Reachability != "unknown" {
		t.Fatal(svc)
	}
	samples["probe_success"] = []workloadSample{wlSample(now, map[string]string{"namespace": "ns", "service": "web", "target_namespace": "ns", "target_service": "web", "instance": "https://web/"}, 1), wlSample(now, map[string]string{"namespace": "ns", "service": "web", "target_namespace": "ns", "target_service": "web", "instance": "https://web/"}, 1), wlSample(now, map[string]string{"namespace": "ns", "service": "web", "target_namespace": "ns", "target_service": "web", "instance": "https://web/health"}, 0)}
	r = assembleWorkloads(samples, now, access{operator: true}, nil)
	svc = r.Namespaces[0].Services[0]
	if svc.Reachability != "degraded" || svc.ProbeTargets != 2 || svc.FailedProbeTargets != 1 {
		t.Fatal(svc)
	}
	for i := range samples["probe_success"] {
		samples["probe_success"][i].Fresh = false
	}
	r = assembleWorkloads(samples, now, access{operator: true}, nil)
	if r.Namespaces[0].Services[0].Reachability != "unknown" {
		t.Fatal("stale probes trusted")
	}
}
func TestWorkloadQueryScopingAndActualSampleFreshness(t *testing.T) {
	now := time.Now().UTC()
	var requests atomic.Int32
	var badScope atomic.Bool
	var expectedNamespace atomic.Value
	expectedNamespace.Store("team-a")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		q := r.URL.Query().Get("query")
		if strings.Contains(q, "probe_success") && (!strings.Contains(q, "target_namespace=~"+strconv.Quote(expectedNamespace.Load().(string))) || !strings.Contains(q, `target_service!=""`)) {
			t.Error("probe query must restrict target namespace before retrieval", q)
		}
		if !strings.Contains(q, "namespace=~"+strconv.Quote(expectedNamespace.Load().(string))) {
			badScope.Store(true)
		}
		results := []any{}
		if strings.Contains(q, "kube_pod_info") {
			for _, ns := range []string{"team-a", "secret"} {
				for metric, extra := range map[string]map[string]string{"kube_pod_info": {}, "kube_pod_status_phase": {"phase": "Running"}, "kube_pod_status_ready": {"condition": "true"}, "kube_pod_status_scheduled": {"condition": "true"}} {
					labels := map[string]string{"__name__": metric, "namespace": ns, "pod": "example", "uid": "uid"}
					for k, v := range extra {
						labels[k] = v
					}
					value := "1"
					if strings.HasPrefix(q, "timestamp(") {
						delete(labels, "__name__")
						labels["workload_metric"] = metric
						value = strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
					}
					results = append(results, map[string]any{"metric": labels, "value": []any{now.Unix(), value}})
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": results}})
	}))
	defer up.Close()
	source, e := newSource(config.Source{URL: up.URL})
	if e != nil {
		t.Fatal(e)
	}
	s := &Server{prom: source, config: config.Config{Prometheus: config.Source{URL: up.URL}}}
	a := access{namespaces: map[string]bool{"team-a": true}}
	r := httptest.NewRequest("GET", "/api/workloads", nil)
	first := httptest.NewRecorder()
	s.workloads(first, r, a)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body)
	}
	if badScope.Load() {
		t.Fatal("unscoped query")
	}
	var result workloadsResult
	if e = json.Unmarshal(first.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if len(result.Namespaces) != 1 || result.Namespaces[0].Namespace != "team-a" || result.Namespaces[0].Pods[0].State != "unknown" || !result.Incomplete {
		t.Fatal(result)
	}
	if strings.Contains(first.Body.String(), "secret") {
		t.Fatal("tenant leak")
	}
	count := requests.Load()
	s.workloads(httptest.NewRecorder(), r, a)
	if requests.Load() != count {
		t.Fatal("cache miss")
	}
	b := access{namespaces: map[string]bool{"different": true}}
	expectedNamespace.Store("different")
	s.workloads(httptest.NewRecorder(), r, b)
	if requests.Load() == count {
		t.Fatal("scope cache reused")
	}
	if badScope.Load() {
		t.Fatal("namespace scope was not applied to queries")
	}
}
func TestWorkloadOptionsAndMalformedUpstream(t *testing.T) {
	a := access{namespaces: map[string]bool{"team-a": true}}
	for _, q := range []string{"?query=up", "?namespace=secret", "?namespace=team-a&namespace=team-a"} {
		if _, e := workloadNamespaceFor(httptest.NewRequest("GET", "/api/workloads"+q, nil), a); e == nil {
			t.Fatal(q)
		}
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[0,"1"]}}`))
	}))
	defer up.Close()
	source, _ := newSource(config.Source{URL: up.URL})
	s := &Server{prom: source}
	r := s.collectWorkloads(httptest.NewRequest("GET", "/", nil), access{operator: true}, "", time.Now().UTC())
	if !r.Incomplete || r.Source.State != "unavailable" || len(r.Namespaces) != 0 {
		t.Fatal(r)
	}
}

func TestWorkloadPartialProbeCoverageDoesNotBecomeReachable(t *testing.T) {
	now := time.Now().UTC()
	samples := wlPodSamples(now, "pod", "Running", "true", "true")
	samples["kube_service_info"] = []workloadSample{wlSample(now, map[string]string{"namespace": "ns", "service": "web"}, 1)}
	fresh := wlSample(now, map[string]string{"namespace": "ns", "service": "web", "target_namespace": "ns", "target_service": "web", "instance": "https://one/"}, 1)
	stale := wlSample(now, map[string]string{"namespace": "ns", "service": "web", "target_namespace": "ns", "target_service": "web", "instance": "https://two/"}, 1)
	stale.Fresh = false
	samples["probe_success"] = []workloadSample{fresh, stale}
	svc := assembleWorkloads(samples, now, access{operator: true}, nil).Namespaces[0].Services[0]
	if svc.Reachability != "unknown" || svc.UnknownProbeTargets != 1 || svc.ProbeTargets != 2 {
		t.Fatal(svc)
	}
	fresh.Value = 0
	samples["probe_success"][0] = fresh
	svc = assembleWorkloads(samples, now, access{operator: true}, nil).Namespaces[0].Services[0]
	if svc.Reachability != "degraded" {
		t.Fatal("observed failure hidden", svc)
	}
}

func TestWorkloadEndpointAmbiguityRemainsUnknown(t *testing.T) {
	now := time.Now().UTC()
	label := wlSample(now, map[string]string{"namespace": "ns", "endpointslice": "slice", "label_kubernetes_io_service_name": "web"}, 1)
	endpoint := wlSample(now, map[string]string{"namespace": "ns", "endpointslice": "slice", "address": "10.0.0.1", "ready": "true", "terminating": "false"}, 1)
	for _, mode := range []string{"stale-slice", "conflicting-owner", "conflicting-ready"} {
		samples := map[string][]workloadSample{"kube_endpointslice_labels": {label}, "kube_endpointslice_endpoints": {endpoint}}
		switch mode {
		case "stale-slice":
			other := wlSample(now, map[string]string{"namespace": "ns", "endpointslice": "other-slice", "label_kubernetes_io_service_name": "web"}, 1)
			other.Fresh = false
			samples["kube_endpointslice_labels"] = append(samples["kube_endpointslice_labels"], other)
		case "conflicting-owner":
			other := wlSample(now, map[string]string{"namespace": "ns", "endpointslice": "slice", "label_kubernetes_io_service_name": "other"}, 1)
			samples["kube_endpointslice_labels"] = append(samples["kube_endpointslice_labels"], other)
		case "conflicting-ready":
			other := wlSample(now, map[string]string{"namespace": "ns", "endpointslice": "slice", "address": "10.0.0.1", "ready": "false", "terminating": "false"}, 1)
			samples["kube_endpointslice_endpoints"] = append(samples["kube_endpointslice_endpoints"], other)
		}
		if got := workloadEndpoints(samples, "ns", "web"); got != nil {
			t.Fatal(mode, *got)
		}
	}
}

func TestProbeScrapeLabelsAreNotTargetIdentity(t *testing.T) {
	now := time.Now().UTC()
	samples := wlPodSamples(now, "pod", "Running", "true", "true")
	samples["kube_service_info"] = []workloadSample{wlSample(now, map[string]string{"namespace": "ns", "service": "blackbox-exporter"}, 1)}
	samples["probe_success"] = []workloadSample{wlSample(now, map[string]string{"namespace": "ns", "service": "blackbox-exporter", "instance": "https://unrelated.example/"}, 1)}
	svc := assembleWorkloads(samples, now, access{operator: true}, nil).Namespaces[0].Services[0]
	if svc.Reachability != "unknown" || svc.ProbeTargets != 0 {
		t.Fatal("scrape labels attributed external probe to exporter service", svc)
	}
}
