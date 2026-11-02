package server

import (
	"bytes"
	"encoding/json"
	"github.com/aai-labs/aai-alertmanager-ui/internal/config"
	"github.com/aai-labs/aai-alertmanager-ui/internal/incidents"
	"github.com/aai-labs/aai-alertmanager-ui/internal/triage"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func workflowTestServer(t *testing.T) *Server {
	t.Helper()
	store, e := incidents.Open(filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &Server{store: store, sourceState: "connected", lastSuccess: time.Now(), config: config.Config{}}
}
func TestIncidentScopeAndVersion(t *testing.T) {
	s := workflowTestServer(t)
	now := time.Now()
	if e := s.store.Observe([]incidents.Member{{Fingerprint: "a", Labels: map[string]string{"namespace": "alpha", "alertname": "Broken", "severity": "critical"}, Status: map[string]any{"state": "suppressed", "inhibitedBy": []string{"secret"}}}, {Fingerprint: "b", Labels: map[string]string{"namespace": "beta", "alertname": "Private"}}}, now); e != nil {
		t.Fatal(e)
	}
	list, err := s.store.List()
	if err != nil || len(list) == 0 {
		t.Fatalf("list incidents: %v, %v", list, err)
	}
	var alpha, beta incidents.Incident
	for _, i := range list {
		if i.Namespace == "alpha" {
			alpha = i
		} else {
			beta = i
		}
	}
	tenant := access{identity: "tenant", namespaces: map[string]bool{"alpha": true}}
	rr := httptest.NewRecorder()
	s.incidentAPI(rr, httptest.NewRequest("GET", "/api/incidents", nil), tenant, "/api/incidents")
	if rr.Code != 200 || bytes.Contains(rr.Body.Bytes(), []byte("Private")) || bytes.Contains(rr.Body.Bytes(), []byte("secret")) {
		t.Fatal(rr.Body.String())
	}
	rr = httptest.NewRecorder()
	s.incidentAPI(rr, httptest.NewRequest("GET", "/", nil), tenant, "/api/incidents/"+beta.ID)
	if rr.Code != 404 {
		t.Fatal("tenant detail leak")
	}
	for _, actor := range []access{tenant, {operator: true, identity: "operator"}} {
		payload := map[string]any{"version": alpha.Version, "owner": "Alice"}
		b, _ := json.Marshal(payload)
		r := httptest.NewRequest("PATCH", "/api/incidents/"+alpha.ID, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		rr = httptest.NewRecorder()
		s.incidentAPI(rr, r, actor, "/api/incidents/"+alpha.ID)
		want := 200
		if !actor.operator {
			want = 403
		}
		if rr.Code != want {
			t.Fatalf("got%d want%d %s", rr.Code, want, rr.Body.String())
		}
	}
	b, _ := json.Marshal(map[string]any{"version": alpha.Version, "owner": "Bob"})
	r := httptest.NewRequest("PATCH", "/", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	s.incidentAPI(rr, r, access{operator: true}, "/api/incidents/"+alpha.ID)
	if rr.Code != 409 {
		t.Fatalf("stale edit got %d", rr.Code)
	}
	persisted, err := s.store.Get(alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Owner != "Alice" || persisted.Version != alpha.Version+1 {
		t.Fatalf("stale edit overwrote accepted update: %+v", persisted)
	}
}
func TestFollowupAcceptedDeliveryIsNotSentAgain(t *testing.T) {
	s := workflowTestServer(t)
	now := time.Now()
	if err := s.store.Observe([]incidents.Member{{Fingerprint: "a", Labels: map[string]string{"namespace": "alpha", "alertname": "Broken"}}}, now); err != nil {
		t.Fatal(err)
	}
	list, err := s.store.List()
	if err != nil || len(list) == 0 {
		t.Fatalf("list incidents: %v, %v", list, err)
	}
	due := now.Add(-time.Minute).Format(time.RFC3339)
	_, e := s.store.Update(list[0].ID, list[0].Version, incidents.Mutation{ReviewAt: &due}, "operator", now)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Idempotency-Key") == "" || r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing delivery authentication")
		}
		var payload struct {
			EventID    string    `json:"eventId"`
			Type       string    `json:"type"`
			IncidentID string    `json:"incidentId"`
			DueAt      time.Time `json:"dueAt"`
			Link       string    `json:"link"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.EventID != r.Header.Get("Idempotency-Key") || payload.Type != "incident.review_due" || payload.IncidentID != list[0].ID || !payload.DueAt.Equal(now.Add(-time.Minute).Truncate(time.Second)) || payload.Link == "" {
			t.Errorf("invalid public review event: %+v", payload)
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	s.config.FollowUp = config.FollowUp{URL: srv.URL, PublicURL: "https://alerts.example/"}
	s.webhookToken = "test"
	s.deliverReviews(t.Context(), now)
	s.deliverReviews(t.Context(), now.Add(time.Minute))
	d, err := s.store.Deliveries(list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(d) != 1 || d[0].Status != "accepted" {
		t.Fatalf("delivery %d %+v", calls, d)
	}
}

func TestShadowDecisionReusedAndReadOnlyGated(t *testing.T) {
	s := workflowTestServer(t)
	now := time.Now().UTC()
	_ = s.store.Observe([]incidents.Member{{Fingerprint: "a", Labels: map[string]string{"namespace": "alpha", "alertname": "Broken", "severity": "critical"}}}, now)
	key := filepath.Join(t.TempDir(), "key")
	if e := os.WriteFile(key, []byte("not-used"), 0600); e != nil {
		t.Fatal(e)
	}
	s.config.Triage = config.Triage{Enabled: true, Apply: true, APIKeyFile: key, Model: "typesafe/jev-1.13", PolicyVersion: "1", MonthlyBudgetUSD: 10}
	var e error
	s.triageClient, e = triage.New(s.config.Triage)
	if e != nil {
		t.Fatal(e)
	}
	list, err := s.store.List()
	if err != nil || len(list) == 0 {
		t.Fatalf("list incidents: %v, %v", list, err)
	}
	i := list[0]
	_, _, hash, e := s.triageClient.Prepare(triage.Input{Incident: candidate(i), Candidates: []triage.Candidate{}})
	if e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(map[string]triage.Answer{"urgency": {Type: "choice", Choice: "monitor", Confidence: 1, Probabilities: map[string]float64{"monitor": 1, "immediate": 0, "today": 0, "unknown": 0}}})
	e = s.store.RecordDecision(incidents.Decision{InputHash: hash, IncidentID: i.ID, Model: "typesafe/jev-1.13", PolicyVersion: "1", Decisions: body, CreatedAt: now})
	if e != nil {
		t.Fatal(e)
	}
	s.config.ReadOnly = true
	s.evaluateTriage(t.Context(), now)
	got, _ := s.store.Get(i.ID)
	if got.Model != nil {
		t.Fatal("read-only applied shadow decision")
	}
	s.config.ReadOnly = false
	s.evaluateTriage(t.Context(), now)
	got, _ = s.store.Get(i.ID)
	if got.Model == nil || !got.Model.Applied || got.Urgency != "immediate" {
		t.Fatalf("cached model not applied safely: %+v", got)
	}
	usage, _ := s.store.Usage(now.Format("2006-01"))
	if usage.Requests != 0 || usage.ReservedUSD != 0 {
		t.Fatal("cached decision caused provider call")
	}
}

func TestTenantProjectionDoesNotMutateOperatorSnapshot(t *testing.T) {
	original := incidents.Incident{ID: "case", Namespace: "alpha", Members: []incidents.Member{{Labels: map[string]string{"namespace": "alpha"}, Annotations: map[string]string{"summary": "Observed condition"}, Status: map[string]any{"state": "suppressed", "inhibitedBy": []string{"operator-only"}}}}}
	before, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	tenant := sanitizeIncident(original, access{namespaces: map[string]bool{"alpha": true}})
	if len(tenant.Members[0].Status) != 1 || tenant.Members[0].Status["state"] != "suppressed" {
		t.Fatal("tenant projection must retain only suppression state")
	}
	after, _ := json.Marshal(original)
	if !bytes.Equal(before, after) {
		t.Fatal("tenant projection changed the original operator snapshot")
	}
	tenant.Members[0].Labels["namespace"] = "changed"
	tenant.Members[0].Annotations["summary"] = "changed"
	if original.Members[0].Labels["namespace"] != "alpha" || original.Members[0].Annotations["summary"] != "Observed condition" {
		t.Fatal("projected member maps share mutable ownership")
	}
}

func TestFollowupRedirectDoesNotTransmitEventToAnotherEndpoint(t *testing.T) {
	s := workflowTestServer(t)
	now := time.Now().UTC()
	if err := s.store.Observe([]incidents.Member{{Fingerprint: "a", Labels: map[string]string{"namespace": "alpha", "alertname": "Broken"}}}, now); err != nil {
		t.Fatal(err)
	}
	list, err := s.store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list incidents: %v, %v", list, err)
	}
	due := now.Add(-time.Minute).Format(time.RFC3339)
	if _, err := s.store.Update(list[0].ID, list[0].Version, incidents.Mutation{ReviewAt: &due}, "operator", now); err != nil {
		t.Fatal(err)
	}
	redirectedCalls := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirectedCalls++; w.WriteHeader(http.StatusNoContent) }))
	defer destination.Close()
	originalCalls := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originalCalls++
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing receiver authentication")
		}
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer receiver.Close()
	s.config.FollowUp = config.FollowUp{URL: receiver.URL, PublicURL: "https://alerts.example/"}
	s.webhookToken = "test"
	s.deliverReviews(t.Context(), now)
	if originalCalls != 1 || redirectedCalls != 0 {
		t.Fatalf("receiver calls=%d, redirect destination calls=%d", originalCalls, redirectedCalls)
	}
	deliveries, err := s.store.Deliveries(list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != "pending" || deliveries[0].Attempts != 1 {
		t.Fatalf("redirect must remain unaccepted for retry: %+v", deliveries)
	}
}
