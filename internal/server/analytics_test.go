package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aai-labs/aai-alertmanager-ui/internal/config"
)

func matrixSeries(labels map[string]string, times ...int64) analyticsSeries {
	s := analyticsSeries{Metric: labels}
	for _, ts := range times {
		a, _ := json.Marshal(ts)
		s.Values = append(s.Values, [2]json.RawMessage{a, json.RawMessage(`"1"`)})
	}
	return s
}
func TestAnalyticsUnionEpisodesAndCoverage(t *testing.T) {
	now := time.Date(2026, 10, 31, 0, 6, 0, 0, time.UTC)
	o := analyticsOptions{days: 7, severity: "all"}
	b := newAnalyticsAccum(now, o, access{operator: true})
	base := b.start
	// A five-minute excerpt at the window boundary plus an unknown sixth minute.
	b.addCoverage([]analyticsSeries{matrixSeries(nil, base, base+60, base+120, base+180, base+240)})
	labels := map[string]string{"namespace": "payments", "alertname": "First", "alertstate": "firing"}
	if err := b.addAlerts([]analyticsSeries{matrixSeries(labels, base, base+60, base+180, base+240, base+300), matrixSeries(map[string]string{"namespace": "payments", "alertname": "Second", "alertstate": "firing"}, base+60, base+120)}); err != nil {
		t.Fatal(err)
	}
	result := b.result(now, nil)
	row := result.Namespaces[0]
	if row.FiringSeconds != 300 {
		t.Fatalf("overlaps counted twice or gap included: %+v", row)
	}
	if row.Episodes != 2 {
		t.Fatalf("want Second start and First restart, not window boundary: %d", row.Episodes)
	}
	if !result.Incomplete || result.Coverage.KnownSeconds != 300 {
		t.Fatal("missing coverage must be incomplete")
	}
	if row.DaysWithAlerts != 1 || row.Days[0].FiringPercent == nil || *row.Days[0].FiringPercent != 100 {
		t.Fatal("wrong daily union")
	}
}
func TestAnalyticsGapDoesNotCreateEpisode(t *testing.T) {
	now := time.Date(2026, 10, 31, 0, 6, 0, 0, time.UTC)
	b := newAnalyticsAccum(now, analyticsOptions{days: 7, severity: "all"}, access{operator: true})
	base := b.start
	b.addCoverage([]analyticsSeries{matrixSeries(nil, base, base+60, base+180, base+240)})
	b.addAlerts([]analyticsSeries{matrixSeries(map[string]string{"namespace": "a", "alertstate": "firing"}, base, base+60, base+180, base+240)})
	if got := b.result(now, nil).Namespaces[0].Episodes; got != 0 {
		t.Fatalf("gap became %d false episodes", got)
	}
}
func TestAnalyticsZeroCoverageIsUnknown(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newAnalyticsAccum(now, analyticsOptions{days: 7, severity: "all", namespace: "a"}, access{operator: true})
	result := b.result(now, nil)
	if !result.Incomplete || result.Namespaces[0].FiringPercent != nil {
		t.Fatal("no coverage was shown as healthy")
	}
}
func TestAnalyticsDayBoundaryDoesNotRestart(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newAnalyticsAccum(now, analyticsOptions{days: 7, severity: "all"}, access{operator: true})
	base := b.start + 86400
	b.addCoverage([]analyticsSeries{matrixSeries(nil, base-120, base-60, base, base+60)})
	labels := map[string]string{"namespace": "a", "alertstate": "firing"}
	b.addAlerts([]analyticsSeries{matrixSeries(labels, base-60)})
	b.addAlerts([]analyticsSeries{matrixSeries(labels, base, base+60)})
	if got := b.result(now, nil).Namespaces[0].Episodes; got != 1 {
		t.Fatalf("daily chunks produced %d episodes", got)
	}
}
func TestAnalyticsTenantQueryScopingAndEscaping(t *testing.T) {
	a := access{namespaces: map[string]bool{"team-a": true, "team.b": true}}
	query := analyticsQuery(analyticsOptions{days: 7, severity: "actionable"}, a)
	if !strings.Contains(query, `namespace=~"team-a|team\\.b"`) {
		t.Fatalf("missing escaped authorization matcher: %s", query)
	}
	if _, e := analyticsOptionsFor(httptest.NewRequest("GET", "/api/analytics?namespace=secret", nil), a); e == nil {
		t.Fatal("unauthorized namespace accepted")
	}
	for _, q := range []string{"?query=ALERTS", "?days=365", "?severity=arbitrary", "?days=7&days=30"} {
		if _, e := analyticsOptionsFor(httptest.NewRequest("GET", "/api/analytics"+q, nil), a); e == nil {
			t.Fatalf("accepted %s", q)
		}
	}
}
func TestAnalyticsDefensiveTenantFiltering(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	b := newAnalyticsAccum(now, analyticsOptions{days: 7, severity: "actionable"}, access{namespaces: map[string]bool{"team-a": true}})
	for _, ns := range []string{"team-a", "team-b", ""} {
		b.addAlerts([]analyticsSeries{matrixSeries(map[string]string{"namespace": ns, "alertstate": "firing"}, b.start)})
	}
	result := b.result(now, nil)
	if len(result.Namespaces) != 1 || result.Namespaces[0].Namespace != "team-a" {
		t.Fatalf("tenant data leaked: %+v", result.Namespaces)
	}
}
func TestAnalyticsHandlerQueriesAreBoundedAndCached(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if q.Get("step") != "60" || q.Get("timeout") != "8s" {
			t.Error("unbounded query")
		}
		query := q.Get("query")
		if strings.HasPrefix(query, "ALERTS") && !strings.Contains(query, `namespace=~"team-a"`) {
			t.Errorf("query missing tenant boundary: %s", query)
		}
		jsonResponse(w, 200, map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{}}})
	}))
	defer up.Close()
	s, err := New(config.Config{ListenAddr: ":8080", Alertmanager: config.Source{URL: up.URL}, Prometheus: config.Source{URL: up.URL}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/analytics?days=7", nil)
	w := httptest.NewRecorder()
	s.analytics(w, r, access{namespaces: map[string]bool{"team-a": true}})
	if w.Code != 200 || calls < 12 || calls > 14 {
		t.Fatalf("unexpected response or request count: %d %d", w.Code, calls)
	}
	var result analyticsResult
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Incomplete {
		t.Fatal("missing coverage not explicit")
	}
}
func TestAnalyticsDemoCachePartitionsScope(t *testing.T) {
	s, err := New(config.Config{ListenAddr: ":8080", Demo: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/analytics?days=7", nil)
	w := httptest.NewRecorder()
	s.analytics(w, r, access{operator: true})
	var result analyticsResult
	json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Namespaces) != 4 {
		t.Fatalf("missing demo rows: %s", w.Body)
	}
	w = httptest.NewRecorder()
	s.analytics(w, httptest.NewRequest("GET", "/api/analytics?namespace=payments", nil), access{operator: true})
	json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Namespaces) != 1 || result.Namespaces[0].Namespace != "payments" {
		t.Fatal("namespace cache collision")
	}
}

func TestAnalyticsCompleteHistoryCacheIsScopePartitioned(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		rows := []analyticsSeries{}
		if strings.HasPrefix(q.Get("query"), "max(") {
			start, _ := strconv.ParseInt(q.Get("start"), 10, 64)
			end, _ := strconv.ParseInt(q.Get("end"), 10, 64)
			ts := []int64{}
			for t := start; t <= end; t += 60 {
				ts = append(ts, t)
			}
			rows = append(rows, matrixSeries(nil, ts...))
		}
		jsonResponse(w, 200, map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": rows}})
	}))
	defer up.Close()
	s, err := New(config.Config{ListenAddr: ":8080", Alertmanager: config.Source{URL: up.URL}, Prometheus: config.Source{URL: up.URL}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := func(ns string) analyticsResult {
		w := httptest.NewRecorder()
		s.analytics(w, httptest.NewRequest("GET", "/api/analytics", nil), access{namespaces: map[string]bool{ns: true}})
		var got analyticsResult
		if json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Incomplete {
			t.Fatalf("unexpected result: %s", w.Body)
		}
		return got
	}
	run("a")
	first := calls
	run("a")
	if calls != first {
		t.Fatal("identical scope missed cache")
	}
	got := run("b")
	if calls == first || len(got.Namespaces) != 1 || got.Namespaces[0].Namespace != "b" {
		t.Fatal("authorization scope shared cache")
	}
}
func TestAnalyticsFailedAlertChunkIsNotHealthy(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if strings.HasPrefix(q.Get("query"), "ALERTS") {
			w.WriteHeader(500)
			return
		}
		start, _ := strconv.ParseInt(q.Get("start"), 10, 64)
		end, _ := strconv.ParseInt(q.Get("end"), 10, 64)
		ts := []int64{}
		for t := start; t <= end; t += 60 {
			ts = append(ts, t)
		}
		jsonResponse(w, 200, map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []analyticsSeries{matrixSeries(nil, ts...)}}})
	}))
	defer up.Close()
	s, err := New(config.Config{ListenAddr: ":8080", Alertmanager: config.Source{URL: up.URL}, Prometheus: config.Source{URL: up.URL}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := s.collectAnalytics(httptest.NewRequest("GET", "/api/analytics", nil), access{operator: true}, analyticsOptions{days: 7, severity: "all", namespace: "a"}, time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	if !got.Incomplete || got.Coverage.KnownSeconds != 0 || got.Namespaces[0].FiringPercent != nil {
		t.Fatal("failed query became zero alerts/healthy")
	}
}

func TestAnalyticsDemoSeverityFilters(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	result := demoAnalytics(now, analyticsOptions{days: 7, severity: "warning"})
	for _, row := range result.Namespaces {
		if row.Namespace == "rook-ceph" && row.FiringSeconds != 0 {
			t.Fatal("critical demonstration leaked into warning filter")
		}
	}
	result = demoAnalytics(now, analyticsOptions{days: 7, severity: "critical"})
	for _, row := range result.Namespaces {
		if row.Namespace != "rook-ceph" && row.FiringSeconds != 0 {
			t.Fatal("warning demonstration leaked into critical filter")
		}
	}
}
