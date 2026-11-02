package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aai-labs/aai-alertmanager-ui/internal/config"
	"github.com/aai-labs/aai-alertmanager-ui/internal/incidents"
)

func TestReviewReadOnlyDoesNotDispatchWebhook(t *testing.T) {
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
	calls := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) }))
	defer receiver.Close()
	s.config.ReadOnly = true
	s.config.FollowUp = config.FollowUp{URL: receiver.URL, PublicURL: "https://alerts.example/"}
	s.webhookToken = "test"
	s.deliverReviews(t.Context(), now)
	if calls != 0 {
		t.Fatalf("read-only mode delivered %d external notifications", calls)
	}
}
func TestReviewTenantCannotReadModelOrWriteWorkflow(t *testing.T) {
	s := workflowTestServer(t)
	now := time.Now().UTC()
	if err := s.store.Observe([]incidents.Member{{Fingerprint: "a", Labels: map[string]string{"namespace": "alpha", "alertname": "Broken"}, Status: map[string]any{"state": "suppressed", "silencedBy": []string{"private-silence"}}}}, now); err != nil {
		t.Fatal(err)
	}
	list, err := s.store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list incidents: %v, %v", list, err)
	}
	i := list[0]
	d := incidents.Decision{InputHash: "test-hash", IncidentID: i.ID, Model: "jev", PolicyVersion: "1", Decisions: json.RawMessage(`{"related":{"choice":"secret-other-namespace-incident"}}`), CreatedAt: now}
	if err := s.store.ApplyTriage(i.ID, i.Version, "today", "team", "runbook", d, now); err != nil {
		t.Fatal(err)
	}
	tenant := access{identity: "tenant", namespaces: map[string]bool{"alpha": true}}
	w := httptest.NewRecorder()
	s.incidentAPI(w, httptest.NewRequest("GET", "/", nil), tenant, "/api/incidents/"+i.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("tenant detail: %d %s", w.Code, w.Body.String())
	}
	var visible incidents.Incident
	if err := json.Unmarshal(w.Body.Bytes(), &visible); err != nil {
		t.Fatal(err)
	}
	if visible.ID != i.ID || len(visible.Members) != 1 || visible.Members[0].Status["state"] != "suppressed" {
		t.Fatalf("tenant lost authorized evidence: %+v", visible)
	}
	before, err := s.store.Get(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"secret-other-namespace-incident", "test-hash", "private-silence", "triage_applied"} {
		if strings.Contains(w.Body.String(), private) {
			t.Errorf("tenant detail leaked %s", private)
		}
	}
	for _, path := range []string{"/api/triage/status", "/api/incidents/" + i.ID + "/deliveries"} {
		w = httptest.NewRecorder()
		s.incidentAPI(w, httptest.NewRequest("GET", path, nil), tenant, path)
		if w.Code != 403 {
			t.Errorf("tenant read %s: %d", path, w.Code)
		}
	}
	for _, actor := range []access{tenant, {operator: true, identity: "operator"}} {
		if actor.operator {
			s.config.ReadOnly = true
		}
		w = httptest.NewRecorder()
		r := httptest.NewRequest("PATCH", "/", bytes.NewBufferString(`{"version":2,"owner":"attacker"}`))
		r.Header.Set("Content-Type", "application/json")
		s.incidentAPI(w, r, actor, "/api/incidents/"+i.ID)
		if w.Code != 403 {
			t.Errorf("unauthorized workflow write: %d", w.Code)
		}
		after, err := s.store.Get(i.ID)
		if err != nil {
			t.Fatal(err)
		}
		if after.Version != before.Version || after.Owner != before.Owner || len(after.Timeline) != len(before.Timeline) {
			t.Fatalf("rejected write changed workflow: before=%+v after=%+v", before, after)
		}
	}
}
func TestReviewCrossOriginWorkflowRejected(t *testing.T) {
	s := workflowTestServer(t)
	now := time.Now().UTC()
	if err := s.store.Observe([]incidents.Member{{Fingerprint: "a", Labels: map[string]string{"namespace": "a", "alertname": "Broken"}}}, now); err != nil {
		t.Fatal(err)
	}
	list, err := s.store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list incidents: %v, %v", list, err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("PATCH", "http://alerts.example/api/incidents/"+list[0].ID, bytes.NewBufferString(`{"version":1,"owner":"attacker"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://attacker.example")
	s.incidentAPI(w, r, access{operator: true, identity: "operator"}, "/api/incidents/"+list[0].ID)
	if w.Code != 403 {
		t.Fatalf("cross-origin mutation status %d", w.Code)
	}
	after, err := s.store.Get(list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Owner != list[0].Owner || after.Version != list[0].Version {
		t.Fatalf("cross-origin write changed incident: %+v", after)
	}
}
