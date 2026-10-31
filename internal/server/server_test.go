package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/aai-labs/aai-alertmanager-ui/internal/config"
)

const sampleAlerts = `[
 {"fingerprint":"one","labels":{"alertname":"Failure","namespace":"team-a","severity":"critical"},"status":{"state":"suppressed","silencedBy":["secret-id"],"inhibitedBy":["secret-fingerprint"]},"receivers":[{"name":"private-recipient"}]},
 {"fingerprint":"two","labels":{"alertname":"Failure","namespace":"team-b"}},
 {"fingerprint":"global","labels":{"alertname":"NodeDown"}},
 {"fingerprint":"one","labels":{"alertname":"Failure","namespace":"team-a"}}
]`

func testServer(t *testing.T, c config.Config, upstream http.HandlerFunc) *Server {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	c.Alertmanager.URL = up.URL
	s, err := New(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func request(s *Server, method, path, body, user, remote string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = remote
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if user != "" {
		r.Header.Set("X-User", user)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func tenantConfig() config.Config {
	return config.Config{ListenAddr: ":8080", Tenancy: config.Tenancy{Enabled: true, IdentityHeader: "X-User", GroupsHeader: "X-Groups", TrustedProxyCIDRs: []string{"127.0.0.1/32"}, Users: map[string][]string{"alice": {"team-a"}}, Groups: map[string][]string{"team-b-readers": {"team-b"}}, OperatorUsers: []string{"admin"}}}
}
func TestTenantBoundary(t *testing.T) {
	s := testServer(t, tenantConfig(), func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(sampleAlerts)) })
	for _, test := range []struct {
		user, remote string
		code         int
	}{{"alice", "127.0.0.1:4000", 200}, {"", "127.0.0.1:4000", 403}, {"nobody", "127.0.0.1:4000", 403}, {"alice", "10.0.0.2:4000", 403}} {
		w := request(s, "GET", "/api/alerts", "", test.user, test.remote)
		if w.Code != test.code {
			t.Fatalf("%+v: %d %s", test, w.Code, w.Body)
		}
		if test.code == 200 {
			var alerts []alert
			if err := json.Unmarshal(w.Body.Bytes(), &alerts); err != nil {
				t.Fatal(err)
			}
			if len(alerts) != 1 {
				t.Fatalf("unexpected alerts: %s", w.Body)
			}
			for _, secret := range []string{"team-b", "global", "secret-id", "secret-fingerprint", "private-recipient"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatalf("tenant leaked %s", secret)
				}
			}
		}
	}
	for _, test := range []struct{ method, path, body string }{{"GET", "/api/silences", ""}, {"POST", "/api/silences", "{}"}, {"DELETE", "/api/silences/abc", ""}, {"POST", "/api/silences/preview", `{"matchers":[{"name":"namespace","value":"team-b"}]}`}, {"GET", "/api/v2/status", ""}} {
		w := request(s, test.method, test.path, test.body, "alice", "127.0.0.1:4000")
		if w.Code != 403 && w.Code != 404 {
			t.Fatalf("tenant endpoint allowed: %+v %d", test, w.Code)
		}
	}
	w := request(s, "GET", "/api/context", "", "alice", "127.0.0.1:4000")
	if !strings.Contains(w.Body.String(), `"readOnly":true`) || !strings.Contains(w.Body.String(), `"namespaces":["team-a"]`) {
		t.Fatalf("bad context: %s", w.Body)
	}
	w = request(s, "GET", "/api/alerts", "", "admin", "127.0.0.1:4000")
	var alerts []alert
	_ = json.Unmarshal(w.Body.Bytes(), &alerts)
	if len(alerts) != 3 {
		t.Fatalf("operator expected three deduped alerts: %s", w.Body)
	}
}
func TestGroupGrantsAndDuplicateIdentity(t *testing.T) {
	s := testServer(t, tenantConfig(), func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(sampleAlerts)) })
	r := httptest.NewRequest("GET", "/api/alerts", nil)
	r.RemoteAddr = "127.0.0.1:4000"
	r.Header.Set("X-User", "bob")
	r.Header.Set("X-Groups", "team-b-readers")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "team-a") || !strings.Contains(w.Body.String(), "team-b") {
		t.Fatalf("bad group grants: %s", w.Body)
	}
	r.Header.Add("X-User", "admin")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("duplicate identity accepted")
	}
}
func TestPrometheusTenantScope(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/alerts" {
			t.Errorf("bad Prometheus path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"alerts":` + sampleAlerts + `}}`))
	}))
	defer prom.Close()
	c := tenantConfig()
	c.Prometheus.URL = prom.URL
	s := testServer(t, c, func(w http.ResponseWriter, r *http.Request) {})
	w := request(s, "GET", "/api/prometheus", "", "alice", "127.0.0.1:4000")
	if w.Code != 200 || strings.Contains(w.Body.String(), "team-b") || strings.Contains(w.Body.String(), "global") {
		t.Fatalf("Prometheus leaked: %s", w.Body)
	}
}
func TestReadOnlyAndCrossOrigin(t *testing.T) {
	calls := 0
	s := testServer(t, config.Config{ListenAddr: ":8080", ReadOnly: true}, func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = w.Write([]byte(`[]`)) })
	for _, method := range []string{"POST", "DELETE"} {
		path := "/api/silences"
		if method == "DELETE" {
			path += "/abc"
		}
		w := request(s, method, path, "{}", "", "127.0.0.1:1")
		if w.Code != 403 {
			t.Fatalf("readOnly returned %d", w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("readonly mutations reached upstream")
	}
	s.config.ReadOnly = false
	for _, origin := range []string{"https://evil.example", "null"} {
		r := httptest.NewRequest("POST", "http://localhost/api/silences", strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("origin %s allowed", origin)
		}
	}
	r := httptest.NewRequest("POST", "/api/silences", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal("non-json mutation accepted")
	}
}
func TestSilencePreviewUsesAnchoredRE2(t *testing.T) {
	s := testServer(t, config.Config{ListenAddr: ":8080", ReadOnly: true}, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(sampleAlerts)) })
	for _, test := range []struct {
		body        string
		code, count int
	}{{`{"matchers":[{"name":"namespace","value":"team","isRegex":true}]}`, 200, 0}, {`{"matchers":[{"name":"namespace","value":"team-.*","isRegex":true}]}`, 200, 2}, {`{"matchers":[{"name":"namespace","value":"team-a","isEqual":false}]}`, 200, 2}, {`{"matchers":[{"name":"namespace","value":"(?=a)","isRegex":true}]}`, 400, 0}} {
		w := request(s, "POST", "/api/silences/preview", test.body, "", "127.0.0.1:1")
		if w.Code != test.code {
			t.Fatalf("preview %s: %d %s", test.body, w.Code, w.Body)
		}
		if w.Code == 200 {
			var body struct {
				Count int `json:"count"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if body.Count != test.count {
				t.Fatalf("count=%d expected %d", body.Count, test.count)
			}
		}
	}
}
func TestSilenceLifecycleForwarding(t *testing.T) {
	calls := []string{}
	s := testServer(t, config.Config{ListenAddr: ":8080"}, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == "POST" {
			var body silence
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.CreatedBy != "operator" {
				t.Error("invalid forwarded body")
			}
			_, _ = w.Write([]byte(`{"silenceID":"abcd"}`))
		} else if r.Method == "DELETE" {
			w.WriteHeader(200)
		} else {
			_, _ = w.Write([]byte(`[]`))
		}
	})
	body := `{"matchers":[{"name":"namespace","value":"team-a","isRegex":false}],"startsAt":"` + time.Now().UTC().Format(time.RFC3339) + `","endsAt":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `","createdBy":"operator","comment":"maintenance"}`
	for _, test := range []struct{ method, path, body string }{{"GET", "/api/silences", ""}, {"POST", "/api/silences", body}, {"DELETE", "/api/silences/abcd", ""}} {
		w := request(s, test.method, test.path, test.body, "", "127.0.0.1:1")
		if w.Code != 200 {
			t.Fatalf("%s failed: %d %s", test.path, w.Code, w.Body)
		}
	}
	if strings.Join(calls, ",") != "GET /api/v2/silences,POST /api/v2/silences,DELETE /api/v2/silence/abcd" {
		t.Fatalf("wrong upstream paths %v", calls)
	}
}
func TestSafeErrorsAndNoRedirects(t *testing.T) {
	s := testServer(t, config.Config{ListenAddr: ":8080"}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://127.0.0.1:1/secrets")
		w.WriteHeader(302)
		_, _ = w.Write([]byte("SECRET config"))
	})
	w := request(s, "GET", "/api/alerts", "", "", "127.0.0.1:1")
	if w.Code != 502 || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatalf("unsafe upstream error: %s", w.Body)
	}
}
func TestSubpathAndHealth(t *testing.T) {
	s, err := New(config.Config{Demo: true, ListenAddr: ":8080", BasePath: "/alerts"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.assets = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(`<!doctype html><html><head></head><body>Test UI</body></html>`)}}

	for _, test := range []struct {
		path string
		code int
	}{{"/healthz", 200}, {"/readyz", 200}, {"/alerts", 307}, {"/alerts/", 200}, {"/alerts/api/alerts", 200}, {"/api/alerts", 404}, {"/alerts/api/unknown", 404}} {
		w := request(s, "GET", test.path, "", "", "127.0.0.1:1")
		if w.Code != test.code {
			t.Errorf("%s: %d wanted %d", test.path, w.Code, test.code)
		}
	}
	w := request(s, "GET", "/alerts/api/context", "", "", "127.0.0.1:1")
	if !strings.Contains(w.Body.String(), `"readOnly":true`) {
		t.Fatal("demo should be readonly")
	}
}

func TestTenantContextFiltersNamespacePriorityRules(t *testing.T) {
	c := tenantConfig()
	c.Priorities = []config.Priority{
		{Name: "private-team-b", Match: map[string]string{"namespace": "team-b"}, Category: "Availability"},
		{Name: "own-rule", Match: map[string]string{"namespace": "team-a"}, Category: "Availability"},
		{Name: "shared-rule", Match: map[string]string{"alertname": "KubePodNotReady"}, Category: "Availability"},
	}
	s := testServer(t, c, func(w http.ResponseWriter, r *http.Request) {})
	w := request(s, "GET", "/api/context", "", "alice", "127.0.0.1:4000")
	if strings.Contains(w.Body.String(), "private-team-b") || !strings.Contains(w.Body.String(), "own-rule") || !strings.Contains(w.Body.String(), "shared-rule") {
		t.Fatalf("bad scoped priorities: %s", w.Body)
	}
}
