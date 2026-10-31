package server

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/aai-labs/aai-alertmanager-ui/internal/config"
	"github.com/aai-labs/aai-alertmanager-ui/internal/demo"
)

//go:embed ui
var assets embed.FS

type Server struct {
	config   config.Config
	am, prom *source
	logger   *slog.Logger
	assets   fs.FS
}

func New(c config.Config, logger *slog.Logger) (*Server, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	am, err := newSource(c.Alertmanager)
	if err != nil {
		return nil, err
	}
	prom, err := newSource(c.Prometheus)
	if err != nil {
		return nil, err
	}
	ui, err := fs.Sub(assets, "ui")
	if err != nil {
		return nil, err
	}
	return &Server{config: c, am: am, prom: prom, logger: logger, assets: ui}, nil
}
func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
		if r.Method != http.MethodGet {
			fail(w, 405, "method not allowed")
			return
		}
		jsonResponse(w, 200, map[string]string{"status": "ok"})
		return
	}
	prefix := s.config.BasePath
	if prefix != "" && r.URL.Path == prefix {
		http.Redirect(w, r, prefix+"/", http.StatusTemporaryRedirect)
		return
	}
	if prefix != "" && !strings.HasPrefix(r.URL.Path, prefix+"/") {
		http.NotFound(w, r)
		return
	}
	route := strings.TrimPrefix(r.URL.Path, prefix)
	a, ok := s.authorize(r)
	if !ok {
		fail(w, 403, "access denied: authenticated identity and namespace grants required")
		return
	}
	if !strings.HasPrefix(route, "/api/") {
		if r.Method != "GET" && r.Method != "HEAD" {
			fail(w, 405, "method not allowed")
			return
		}
		s.serveUI(w, r, route)
		return
	}
	switch {
	case route == "/api/context" && r.Method == "GET":
		priorities := []config.Priority{}
		for _, rule := range s.config.Priorities {
			if ns, scoped := rule.Match["namespace"]; !a.operator && scoped && !a.namespaces[ns] {
				continue
			}
			priorities = append(priorities, rule)
		}
		jsonResponse(w, 200, map[string]any{"readOnly": s.config.ReadOnly || s.config.Demo || !a.operator, "tenantMode": s.config.Tenancy.Enabled && !a.operator, "identity": a.identity, "namespaces": a.namespaceList(), "prometheusEnabled": s.config.Prometheus.URL != "" || s.config.Demo, "demo": s.config.Demo, "priorities": priorities})
	case route == "/api/alerts" && r.Method == "GET":
		alerts, err := s.getAlerts(r, a)
		if err != nil {
			s.upstreamFailure(w, "alertmanager")
			return
		}
		jsonResponse(w, 200, alerts)
	case route == "/api/prometheus" && r.Method == "GET":
		s.prometheus(w, r, a)
	case route == "/api/silences/preview" && r.Method == "POST":
		if !a.operator {
			fail(w, 403, "silence access requires operator permissions")
			return
		}
		if !safeMutation(w, r) {
			return
		}
		s.preview(w, r, a)
	case route == "/api/silences" && r.Method == "GET":
		if !a.operator {
			fail(w, 403, "silence access requires operator permissions")
			return
		}
		if s.config.Demo {
			jsonResponse(w, 200, demo.Silences())
			return
		}
		s.forward(w, r, "GET", "/api/v2/silences", nil)
	case route == "/api/silences" && r.Method == "POST":
		if !s.canWrite(w, r, a) {
			return
		}
		s.saveSilence(w, r)
	case strings.HasPrefix(route, "/api/silences/") && r.Method == "DELETE":
		if !s.canWrite(w, r, a) {
			return
		}
		id := strings.TrimPrefix(route, "/api/silences/")
		if !regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`).MatchString(id) {
			fail(w, 400, "invalid silence ID")
			return
		}
		s.forward(w, r, "DELETE", "/api/v2/silence/"+id, nil)
	default:
		fail(w, 404, "endpoint not found")
	}
}
func (s *Server) serveUI(w http.ResponseWriter, r *http.Request, route string) {
	name := strings.TrimPrefix(path.Clean(route), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(s.assets, name)
	if err != nil {
		if strings.Contains(path.Base(name), ".") {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
		data, err = fs.ReadFile(s.assets, name)
	}
	if err != nil {
		fail(w, 500, "frontend assets unavailable")
		return
	}
	if name == "index.html" {
		base := s.config.BasePath + "/"
		// Vite emits relative assets; a base tag also makes bookmarks work beneath a subpath.
		data = bytes.Replace(data, []byte("<head>"), []byte(`<head><base href="`+base+`">`), 1)
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if r.Method == "HEAD" {
		w.WriteHeader(200)
		return
	}
	_, _ = w.Write(data)
}
func (s *Server) upstreamFailure(w http.ResponseWriter, source string) {
	s.logger.Warn("upstream request failed", "source", source)
	fail(w, 502, source+" request failed; check upstream connectivity and server configuration")
}
func (s *Server) forward(w http.ResponseWriter, r *http.Request, method, path string, body []byte) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	b, status, err := s.am.request(r, method, path, reader)
	if err != nil {
		s.upstreamFailure(w, "alertmanager")
		return
	}
	if status < 200 || status >= 300 {
		if status == 400 || status == 404 || status == 422 {
			fail(w, status, "Alertmanager rejected the request; verify silence fields and ID")
		} else {
			s.upstreamFailure(w, "alertmanager")
		}
		return
	}
	if len(b) == 0 {
		jsonResponse(w, 200, map[string]bool{"ok": true})
		return
	}
	if !json.Valid(b) {
		s.upstreamFailure(w, "alertmanager")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
func safeMutation(w http.ResponseWriter, r *http.Request) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Method == "POST" && (err != nil || media != "application/json") {
		fail(w, 415, "application/json required")
		return false
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(w, 403, "cross-site requests are not allowed")
		return false
	}
	if raw := r.Header.Get("Origin"); raw != "" {
		origin, err := url.Parse(raw)
		if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || !strings.EqualFold(origin.Host, r.Host) {
			fail(w, 403, "cross-origin requests are not allowed")
			return false
		}
	}
	return true
}
func (s *Server) canWrite(w http.ResponseWriter, r *http.Request, a access) bool {
	if !a.operator || s.config.ReadOnly || s.config.Demo {
		fail(w, 403, "silence changes are disabled")
		return false
	}
	return safeMutation(w, r)
}

type alert map[string]any

func labelsOf(a map[string]any) map[string]string {
	result := map[string]string{}
	if labels, ok := a["labels"].(map[string]any); ok {
		for k, v := range labels {
			if str, ok := v.(string); ok {
				result[k] = str
			}
		}
	}
	return result
}
func (s *Server) getAlerts(r *http.Request, a access) ([]alert, error) {
	var b []byte
	if s.config.Demo {
		b, _ = json.Marshal(demo.Alerts())
	} else {
		var status int
		var err error
		b, status, err = s.am.request(r, "GET", "/api/v2/alerts?active=true&silenced=true&inhibited=true&unprocessed=true", nil)
		if err != nil {
			return nil, err
		}
		if status != 200 {
			return nil, errors.New("unexpected upstream status")
		}
	}
	var incoming []alert
	if err := json.Unmarshal(b, &incoming); err != nil {
		return nil, err
	}
	result := []alert{}
	seen := map[string]bool{}
	for _, item := range incoming {
		if !a.allowed(labelsOf(item)) {
			continue
		}
		if fingerprint, ok := item["fingerprint"].(string); ok && fingerprint != "" {
			if seen[fingerprint] {
				continue
			}
			seen[fingerprint] = true
		}
		if !a.operator {
			if status, ok := item["status"].(map[string]any); ok {
				status["silencedBy"] = []string{}
				status["inhibitedBy"] = []string{}
			}
			delete(item, "receivers")
		}
		result = append(result, item)
	}
	return result, nil
}
func (s *Server) prometheus(w http.ResponseWriter, r *http.Request, a access) {
	if s.config.Demo {
		jsonResponse(w, 200, demo.Prometheus())
		return
	}
	if s.config.Prometheus.URL == "" {
		fail(w, 404, "Prometheus is not configured")
		return
	}
	b, status, err := s.prom.request(r, "GET", "/api/v1/alerts", nil)
	if err != nil || status != 200 {
		s.upstreamFailure(w, "prometheus")
		return
	}
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Alerts []alert `json:"alerts"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &payload) != nil || payload.Status != "success" {
		s.upstreamFailure(w, "prometheus")
		return
	}
	filtered := []alert{}
	for _, item := range payload.Data.Alerts {
		if a.allowed(labelsOf(item)) {
			filtered = append(filtered, item)
		}
	}
	payload.Data.Alerts = filtered
	jsonResponse(w, 200, payload)
}

type matcher struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex"`
	IsEqual *bool  `json:"isEqual,omitempty"`
}
type silence struct {
	ID        string    `json:"id,omitempty"`
	Matchers  []matcher `json:"matchers"`
	StartsAt  time.Time `json:"startsAt"`
	EndsAt    time.Time `json:"endsAt"`
	CreatedBy string    `json:"createdBy"`
	Comment   string    `json:"comment"`
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		fail(w, 400, "invalid JSON request body")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		fail(w, 400, "request must contain one JSON object")
		return false
	}
	return true
}
func compileMatchers(matchers []matcher) (func(map[string]string) bool, error) {
	if len(matchers) == 0 {
		return nil, errors.New("at least one matcher is required")
	}
	checks := []func(map[string]string) bool{}
	for _, m := range matchers {
		m := m
		if strings.TrimSpace(m.Name) == "" {
			return nil, errors.New("matcher name is required")
		}
		var pattern *regexp.Regexp
		if m.IsRegex {
			var err error
			pattern, err = regexp.Compile("^(?:" + m.Value + ")$")
			if err != nil {
				return nil, errors.New("invalid RE2 regular expression")
			}
		}
		checks = append(checks, func(labels map[string]string) bool {
			equal := labels[m.Name] == m.Value
			if pattern != nil {
				equal = pattern.MatchString(labels[m.Name])
			}
			if m.IsEqual != nil && !*m.IsEqual {
				return !equal
			}
			return equal
		})
	}
	return func(labels map[string]string) bool {
		for _, check := range checks {
			if !check(labels) {
				return false
			}
		}
		return true
	}, nil
}
func (s *Server) preview(w http.ResponseWriter, r *http.Request, a access) {
	var req struct {
		Matchers []matcher `json:"matchers"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	matches, err := compileMatchers(req.Matchers)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	alerts, err := s.getAlerts(r, a)
	if err != nil {
		s.upstreamFailure(w, "alertmanager")
		return
	}
	filtered := []alert{}
	for _, item := range alerts {
		if matches(labelsOf(item)) {
			filtered = append(filtered, item)
		}
	}
	jsonResponse(w, 200, map[string]any{"count": len(filtered), "alerts": filtered})
}
func (s *Server) saveSilence(w http.ResponseWriter, r *http.Request) {
	var req silence
	if !decodeBody(w, r, &req) {
		return
	}
	matches, err := compileMatchers(req.Matchers)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if matches(map[string]string{}) {
		fail(w, 400, "at least one matcher must not match an empty label value")
		return
	}
	if req.CreatedBy == "" || req.Comment == "" || req.StartsAt.IsZero() || req.EndsAt.IsZero() || !req.EndsAt.After(req.StartsAt) || !req.EndsAt.After(time.Now()) {
		fail(w, 400, "creator, comment and a valid future end time are required")
		return
	}
	if req.ID != "" && !regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`).MatchString(req.ID) {
		fail(w, 400, "invalid silence ID")
		return
	}
	body, _ := json.Marshal(req)
	s.forward(w, r, "POST", "/api/v2/silences", body)
}
