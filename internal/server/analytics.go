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
	"time"
)

const analyticsDefaultCoverage = `up and on (job, instance) prometheus_build_info`

const analyticsMaxSamples = 2_000_000
const analyticsMaxSeries = 10_000

// Bound aggregate historical-query pressure even across distinct users/cache keys.
var analyticsSlots = make(chan struct{}, 2)

type analyticsCacheEntry = jsonCacheEntry
type analyticsDay struct {
	Date            string   `json:"date"`
	KnownSeconds    int      `json:"knownSeconds"`
	ExpectedSeconds int      `json:"expectedSeconds"`
	FiringSeconds   int      `json:"firingSeconds"`
	FiringPercent   *float64 `json:"firingPercent"`
	Episodes        int      `json:"episodes"`
}
type analyticsNamespace struct {
	Namespace      string         `json:"namespace"`
	KnownSeconds   int            `json:"knownSeconds"`
	FiringSeconds  int            `json:"firingSeconds"`
	FiringPercent  *float64       `json:"firingPercent"`
	Episodes       int            `json:"episodes"`
	DaysWithAlerts int            `json:"daysWithAlerts"`
	Days           []analyticsDay `json:"days"`
}
type analyticsCoverage struct {
	KnownSeconds    int      `json:"knownSeconds"`
	ExpectedSeconds int      `json:"expectedSeconds"`
	Percent         *float64 `json:"percent"`
	Kind            string   `json:"kind"`
}
type analyticsResult struct {
	WindowDays        int                  `json:"windowDays"`
	Start             time.Time            `json:"start"`
	End               time.Time            `json:"end"`
	GeneratedAt       time.Time            `json:"generatedAt"`
	TimeZone          string               `json:"timeZone"`
	ResolutionSeconds int                  `json:"resolutionSeconds"`
	Incomplete        bool                 `json:"incomplete"`
	Warnings          []string             `json:"warnings"`
	Coverage          analyticsCoverage    `json:"coverage"`
	Namespaces        []analyticsNamespace `json:"namespaces"`
}
type analyticsSeries struct {
	Metric map[string]string    `json:"metric"`
	Values [][2]json.RawMessage `json:"values"`
}
type analyticsOptions struct {
	days                int
	namespace, severity string
}

func analyticsOptionsFor(r *http.Request, a access) (analyticsOptions, error) {
	o := analyticsOptions{days: 7, severity: "actionable"}
	for k, v := range r.URL.Query() {
		if len(v) != 1 {
			return o, errors.New("query parameters must not be repeated")
		}
		switch k {
		case "days":
			n, e := strconv.Atoi(v[0])
			if e != nil || (n != 7 && n != 30) {
				return o, errors.New("days must be 7 or 30")
			}
			o.days = n
		case "namespace":
			if len(v[0]) > 253 {
				return o, errors.New("namespace is too long")
			}
			o.namespace = v[0]
		case "severity":
			o.severity = v[0]
		default:
			return o, errors.New("unsupported analytics query parameter")
		}
	}
	switch o.severity {
	case "actionable", "all", "critical", "warning", "info":
	default:
		return o, errors.New("invalid severity")
	}
	if o.namespace != "" && !a.operator && !a.namespaces[o.namespace] {
		return o, errors.New("namespace is not authorized")
	}
	return o, nil
}
func analyticsQuery(o analyticsOptions, a access) string {
	matchers := []string{`alertstate="firing"`}
	if o.namespace != "" {
		matchers = append(matchers, "namespace="+strconv.Quote(o.namespace))
	} else if !a.operator {
		ns := a.namespaceList()
		for i := range ns {
			ns[i] = regexp.QuoteMeta(ns[i])
		}
		if len(ns) == 0 {
			matchers = append(matchers, `namespace="__no_authorized_namespace__"`)
		} else {
			matchers = append(matchers, "namespace=~"+strconv.Quote(strings.Join(ns, "|")))
		}
	}
	if o.severity == "actionable" {
		matchers = append(matchers, `severity!="info"`, `alertname!~"Watchdog|InfoInhibitor"`)
	} else if o.severity != "all" {
		matchers = append(matchers, "severity="+strconv.Quote(o.severity))
	}
	return "ALERTS{" + strings.Join(matchers, ",") + "}"
}
func (s *Server) analytics(w http.ResponseWriter, r *http.Request, a access) {
	o, err := analyticsOptionsFor(r, a)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if s.config.Prometheus.URL == "" && !s.config.Demo {
		fail(w, 404, "Prometheus is required for analytics")
		return
	}
	now := time.Now().UTC()
	scope := a.namespaceList()
	keyBytes, _ := json.Marshal([]any{o.days, o.namespace, o.severity, a.operator, scope, now.Format("2006-01-02")})
	key := string(keyBytes)
	s.analyticsCacheMu.Lock()
	entry, ok := s.analyticsCache[key]
	s.analyticsCacheMu.Unlock()
	if ok && now.Sub(entry.at) < 5*time.Minute {
		jsonResponse(w, 200, entry.body)
		return
	}
	var result analyticsResult
	if s.config.Demo {
		result = demoAnalytics(now, o)
	} else {
		select {
		case analyticsSlots <- struct{}{}:
			defer func() { <-analyticsSlots }()
		default:
			w.Header().Set("Retry-After", "5")
			fail(w, http.StatusServiceUnavailable, "analytics is busy; retry shortly")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		result = s.collectAnalytics(r.WithContext(ctx), a, o, now)
	}
	if r.Context().Err() != nil {
		return
	}
	s.analyticsCacheMu.Lock()
	if !result.Incomplete {
		putJSONCache(&s.analyticsCache, key, result, time.Now())
	}
	s.analyticsCacheMu.Unlock()
	jsonResponse(w, 200, result)
}
func (s *Server) analyticsRange(r *http.Request, query string, start, end int64) ([]analyticsSeries, error) {
	params := url.Values{"query": {query}, "start": {strconv.FormatInt(start, 10)}, "end": {strconv.FormatInt(end, 10)}, "step": {"60"}, "timeout": {"8s"}}
	b, status, err := s.prom.request(r, http.MethodGet, "/api/v1/query_range?"+params.Encode(), nil)
	if err != nil || status != 200 {
		return nil, errors.New("Prometheus history request failed")
	}
	var data struct {
		Status   string   `json:"status"`
		Warnings []string `json:"warnings"`
		Data     struct {
			ResultType string            `json:"resultType"`
			Result     []analyticsSeries `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &data) != nil || data.Status != "success" || data.Data.ResultType != "matrix" {
		return nil, errors.New("Prometheus returned invalid history")
	}
	if len(data.Warnings) > 0 {
		return nil, errors.New("Prometheus returned partial history")
	}
	if len(data.Data.Result) > analyticsMaxSeries {
		return nil, errors.New("history series limit reached")
	}
	return data.Data.Result, nil
}
func point(v [2]json.RawMessage) (int64, float64, bool) {
	var ts float64
	var raw string
	if json.Unmarshal(v[0], &ts) != nil || json.Unmarshal(v[1], &raw) != nil {
		return 0, 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	return int64(ts), value, err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)
}
func analyticsBounds(now time.Time, days int) (time.Time, time.Time) {
	end := now.UTC().Truncate(time.Minute)
	start := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1-days)
	return start, end
}

type analyticsAccum struct {
	start, end int64
	covered    []bool
	present    map[string][]bool
	episodes   map[string]map[int]int
	lastSeen   map[string]int64
	samples    int
	scope      access
	options    analyticsOptions
}

func newAnalyticsAccum(now time.Time, o analyticsOptions, a access) *analyticsAccum {
	start, end := analyticsBounds(now, o.days)
	n := int((end.Unix() - start.Unix()) / 60)
	return &analyticsAccum{start: start.Unix(), end: end.Unix(), covered: make([]bool, n), present: map[string][]bool{}, episodes: map[string]map[int]int{}, lastSeen: map[string]int64{}, scope: a, options: o}
}
func (b *analyticsAccum) ensure(ns string) {
	if _, ok := b.present[ns]; !ok {
		b.present[ns] = make([]bool, len(b.covered))
		b.episodes[ns] = map[int]int{}
	}
}
func (b *analyticsAccum) addCoverage(series []analyticsSeries) error {
	for _, s := range series {
		for _, v := range s.Values {
			b.samples++
			if b.samples > analyticsMaxSamples {
				return errors.New("history sample limit reached")
			}
			ts, value, ok := point(v)
			i := int((ts - b.start) / 60)
			if ok && ts >= b.start && ts < b.end && i >= 0 && i < len(b.covered) && value > 0 {
				b.covered[i] = true
			}
		}
	}
	return nil
}
func (b *analyticsAccum) addAlerts(series []analyticsSeries) error {
	for _, s := range series {
		if !b.scope.allowed(s.Metric) {
			continue
		}
		ns := s.Metric["namespace"]
		if b.options.namespace != "" && ns != b.options.namespace {
			continue
		}
		sev, name := s.Metric["severity"], s.Metric["alertname"]
		if b.options.severity == "actionable" && (sev == "info" || name == "Watchdog" || name == "InfoInhibitor") {
			continue
		}
		if b.options.severity != "actionable" && b.options.severity != "all" && sev != b.options.severity {
			continue
		}
		if s.Metric["alertstate"] != "firing" {
			continue
		}
		if _, exists := b.present[ns]; !exists && len(b.present) >= 512 {
			return errors.New("history namespace limit reached")
		}
		b.ensure(ns)
		// JSON sorts map keys, so label identity is independent of Prometheus encoding order.
		id, _ := json.Marshal(s.Metric)
		key := string(id)
		for _, v := range s.Values {
			b.samples++
			if b.samples > analyticsMaxSamples {
				return errors.New("history sample limit reached")
			}
			ts, value, ok := point(v)
			i := int((ts - b.start) / 60)
			if !ok {
				return errors.New("invalid alert history sample")
			}
			if value <= 0 || ts < b.start || ts >= b.end || i < 0 || i >= len(b.covered) {
				continue
			}
			last, seen := b.lastSeen[key]
			if seen && ts <= last {
				continue
			}
			b.lastSeen[key] = ts
			if b.covered[i] {
				b.present[ns][i] = true
			}
			// A gap in source coverage cannot establish a new episode. Neither can a window boundary.
			if i > 0 && b.covered[i] && b.covered[i-1] && (!seen || last != ts-60) {
				b.episodes[ns][i]++
			}
		}
	}
	return nil
}
func percentage(n, d int) *float64 {
	if d == 0 {
		return nil
	}
	v := float64(n) * 100 / float64(d)
	return &v
}
func (b *analyticsAccum) result(now time.Time, warnings []string) analyticsResult {
	if warnings == nil {
		warnings = []string{}
	}
	result := analyticsResult{WindowDays: b.options.days, Start: time.Unix(b.start, 0).UTC(), End: time.Unix(b.end, 0).UTC(), GeneratedAt: now, TimeZone: "UTC", ResolutionSeconds: 60, Warnings: warnings, Incomplete: len(warnings) > 0, Namespaces: []analyticsNamespace{}, Coverage: analyticsCoverage{Kind: "source", ExpectedSeconds: len(b.covered) * 60}}
	baseDays := make([]analyticsDay, b.options.days)
	for i := range baseDays {
		baseDays[i].Date = result.Start.AddDate(0, 0, i).Format("2006-01-02")
	}
	for i, known := range b.covered {
		day := i / 1440
		baseDays[day].ExpectedSeconds += 60
		if known {
			baseDays[day].KnownSeconds += 60
			result.Coverage.KnownSeconds += 60
		}
	}
	result.Coverage.Percent = percentage(result.Coverage.KnownSeconds, result.Coverage.ExpectedSeconds)
	if result.Coverage.KnownSeconds < result.Coverage.ExpectedSeconds {
		result.Incomplete = true
		result.Warnings = append(result.Warnings, "Some requested time has no confirmed source coverage; gaps are unknown, not healthy.")
	}
	// Explicit grants can be shown even with no firing history. This is not namespace inventory.
	if b.options.namespace != "" {
		b.ensure(b.options.namespace)
	} else if !b.scope.operator {
		for _, ns := range b.scope.namespaceList() {
			b.ensure(ns)
		}
	}
	namespaces := make([]string, 0, len(b.present))
	for ns := range b.present {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)
	for _, ns := range namespaces {
		row := analyticsNamespace{Namespace: ns, KnownSeconds: result.Coverage.KnownSeconds, Days: append([]analyticsDay{}, baseDays...)}
		for i, firing := range b.present[ns] {
			if firing && b.covered[i] {
				row.FiringSeconds += 60
				row.Days[i/1440].FiringSeconds += 60
			}
			if b.covered[i] && i > 0 && b.covered[i-1] {
				row.Episodes += b.episodes[ns][i]
				row.Days[i/1440].Episodes += b.episodes[ns][i]
			}
		}
		row.FiringPercent = percentage(row.FiringSeconds, row.KnownSeconds)
		for i := range row.Days {
			d := &row.Days[i]
			d.FiringPercent = percentage(d.FiringSeconds, d.KnownSeconds)
			if d.FiringSeconds > 0 {
				row.DaysWithAlerts++
			}
		}
		result.Namespaces = append(result.Namespaces, row)
	}
	return result
}
func (s *Server) collectAnalytics(r *http.Request, a access, o analyticsOptions, now time.Time) analyticsResult {
	b := newAnalyticsAccum(now, o, a)
	coverage := s.config.Analytics.CoverageQuery
	if coverage == "" {
		coverage = analyticsDefaultCoverage
	}
	coverage = "max(" + coverage + ")"
	warnings := []string{}
	for start := b.start; start < b.end; start += 86400 {
		end := start + 86400
		if end > b.end {
			end = b.end
		}
		end -= 60
		c, err := s.analyticsRange(r, coverage, start, end)
		if err == nil {
			err = b.addCoverage(c)
		}
		if err != nil {
			for ts := start; ts <= end; ts += 60 {
				b.covered[int((ts-b.start)/60)] = false
			}
			warnings = append(warnings, fmt.Sprintf("%s: coverage unavailable or limited", time.Unix(start, 0).UTC().Format("2006-01-02")))
			if r.Context().Err() != nil || b.samples > analyticsMaxSamples {
				break
			}
			continue
		}
		rows, err := s.analyticsRange(r, analyticsQuery(o, a), start, end)
		if err == nil {
			err = b.addAlerts(rows)
		}
		if err != nil {
			// Failed alert retrieval makes the entire chunk unknown; coverage alone does not prove no firing alerts.
			for ts := start; ts <= end; ts += 60 {
				b.covered[int((ts-b.start)/60)] = false
			}
			warnings = append(warnings, fmt.Sprintf("%s: alert history unavailable or limited", time.Unix(start, 0).UTC().Format("2006-01-02")))
			if r.Context().Err() != nil || b.samples > analyticsMaxSamples {
				break
			}
		}
	}
	return b.result(now, warnings)
}
func demoAnalytics(now time.Time, o analyticsOptions) analyticsResult {
	b := newAnalyticsAccum(now, o, access{operator: true})
	for i := range b.covered {
		b.covered[i] = true
	}
	for n, ns := range []string{"data-platform", "payments", "rook-ceph", "kube-system"} {
		if o.namespace != "" && o.namespace != ns {
			continue
		}
		b.ensure(ns)
		for i := range b.covered {
			minute := i % 1440
			firing := n == 0 || n == 1 && minute%180 < 20 || n == 2 && i/1440 == 2 && minute < 120 || n == 3 && i/1440 == 4 && minute < 15
			if o.severity == "info" {
				firing = false
			}
			if o.severity == "critical" {
				firing = n == 2 && firing
			}
			if o.severity == "warning" && n == 2 {
				firing = false
			}
			b.present[ns][i] = firing
			if i > 0 && firing && !b.present[ns][i-1] {
				b.episodes[ns][i] = 1
			}
		}
	}
	result := b.result(now, nil)
	result.Warnings = append(result.Warnings, "Synthetic demonstration history; no production analytics.")
	return result
}
