package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aai-labs/aai-alertmanager-ui/internal/incidents"
	"github.com/aai-labs/aai-alertmanager-ui/internal/triage"
)

func (s *Server) initEnhanced() error {
	if !s.config.Enhanced.Enabled && !s.config.Demo {
		return nil
	}
	path := s.config.Enhanced.DatabasePath
	if s.config.Demo {
		dir, e := os.MkdirTemp("", "alerts-demo-*")
		if e != nil {
			return e
		}
		s.tempDir = dir
		path = filepath.Join(dir, "demo.db")
	}
	st, e := incidents.Open(path)
	if e != nil {
		if s.tempDir != "" {
			_ = os.RemoveAll(s.tempDir)
		}
		return e
	}
	s.store = st
	cleanup := func(err error) error { _ = s.Close(); return err }
	if s.config.Triage.Enabled {
		s.triageClient, e = triage.New(s.config.Triage)
		if e != nil {
			return cleanup(e)
		}
		s.triageState = "waiting"
	}
	if s.config.FollowUp.URL != "" {
		b, e := os.ReadFile(s.config.FollowUp.BearerTokenFile)
		if e != nil {
			return cleanup(errors.New("cannot read follow-up token"))
		}
		s.webhookToken = strings.TrimSpace(string(b))
		if s.webhookToken == "" {
			return cleanup(errors.New("follow-up token is empty"))
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	// Demo data uses the same store and handlers, but no outbound integrations.
	if s.config.Demo {
		s.observe(ctx, time.Now().UTC().Add(-8*24*time.Hour))
		now := time.Now().UTC()
		s.observe(ctx, now)
		// Explicit synthetic workflow examples; never applied to a production store.
		examples, _ := s.store.List()
		for n, i := range examples {
			if i.SourceSeverity == "critical" || i.SourceSeverity == "info" {
				continue
			}
			owner := "Demo operator"
			due := now.Add(-48 * time.Hour).Format(time.RFC3339)
			if n%2 == 0 {
				owner = ""
				due = now.Add(4 * time.Hour).Format(time.RFC3339)
			}
			_, _ = s.store.Update(i.ID, i.Version, incidents.Mutation{Owner: &owner, ReviewAt: &due, Acknowledge: owner != ""}, "demo", now.Add(-72*time.Hour))
		}
		return nil
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		s.observe(ctx, time.Now().UTC())
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		cycles := 0
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				s.observe(ctx, now.UTC())
				cycles++
				if cycles%120 == 0 {
					if e := s.store.Prune(now, s.config.Enhanced.RetentionDays); e != nil {
						s.logger.Error("incident pruning failed")
					}
				}
			}
		}
	}()
	if s.triageClient != nil {
		s.runPeriodic(ctx, 60*time.Second, s.evaluateTriage)
	}
	if s.config.FollowUp.URL != "" && !s.config.ReadOnly {
		s.runPeriodic(ctx, 30*time.Second, s.deliverReviews)
	}

	return nil
}
func (s *Server) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.workers.Wait()
	var e error
	if s.store != nil {
		e = s.store.Close()
	}
	if s.tempDir != "" {
		_ = os.RemoveAll(s.tempDir)
	}
	return e
}
func (s *Server) observe(ctx context.Context, now time.Time) {
	r, _ := http.NewRequestWithContext(ctx, "GET", "http://internal/", nil)
	aa, e := s.getAlerts(r, access{operator: true})
	if e != nil {
		_ = s.store.ObserveFailure()
		s.workflowMu.Lock()
		s.sourceState = "unavailable"
		s.workflowMu.Unlock()
		return
	}
	mm := []incidents.Member{}
	for _, a := range aa {
		var m incidents.Member
		b, _ := json.Marshal(a)
		if json.Unmarshal(b, &m) != nil || len(m.Labels) == 0 {
			_ = s.store.ObserveFailure()
			s.workflowMu.Lock()
			s.sourceState = "unavailable"
			s.workflowMu.Unlock()
			return
		}
		mm = append(mm, m)
	}
	e = s.store.Observe(mm, now)
	s.workflowMu.Lock()
	defer s.workflowMu.Unlock()
	if e != nil {
		s.sourceState = "storage_error"
		s.logger.Error("incident observation failed")
	} else {
		s.sourceState = "connected"
		s.lastSuccess = now
	}
}
func (s *Server) sourceStatus() map[string]any {
	s.workflowMu.Lock()
	defer s.workflowMu.Unlock()
	state := s.sourceState
	if state == "connected" && time.Since(s.lastSuccess) > 2*time.Minute {
		state = "stale"
	}
	return map[string]any{"state": state, "lastSuccess": s.lastSuccess}
}
func sanitizeIncident(i incidents.Incident, a access) incidents.Incident {
	if a.operator {
		return i
	}
	i.Model = nil
	// Audit details can contain model relationships or operator identities; tenants see state only.
	i.Timeline = []incidents.Event{}
	i.Members = slices.Clone(i.Members)
	for n := range i.Members {
		i.Members[n].Labels = maps.Clone(i.Members[n].Labels)
		i.Members[n].Annotations = maps.Clone(i.Members[n].Annotations)
		i.Members[n].Status = map[string]any{"state": i.Members[n].Status["state"]}
	}
	return i
}
func decodeMutation(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		fail(w, 400, "invalid JSON request")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		fail(w, 400, "request must contain one JSON object")
		return false
	}
	return true
}
func incidentError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, incidents.ErrConflict):
		fail(w, 409, e.Error())
	case errors.Is(e, incidents.ErrNotFound):
		fail(w, 404, "incident not found")
	case errors.Is(e, incidents.ErrActive):
		fail(w, 409, e.Error())
	default:
		fail(w, 400, "incident operation failed; check fields and reload")
	}
}
func (s *Server) incidentAPI(w http.ResponseWriter, r *http.Request, a access, route string) {
	if s.store == nil {
		fail(w, 404, "enhanced mode is not enabled")
		return
	}
	if route == "/api/incidents" && r.Method == "GET" {
		limit, offset := 100, 0
		for k, values := range r.URL.Query() {
			if len(values) != 1 {
				fail(w, 400, "repeated query parameter")
				return
			}
			switch k {
			case "limit":
				n, e := strconv.Atoi(values[0])
				if e != nil || n < 1 || n > 200 {
					fail(w, 400, "limit must be 1..200")
					return
				}
				limit = n
			case "offset":
				n, e := strconv.Atoi(values[0])
				if e != nil || n < 0 || n > 1000000 {
					fail(w, 400, "invalid offset")
					return
				}
				offset = n
			case "namespace":
				if !a.operator && !a.namespaces[values[0]] {
					fail(w, 403, "namespace not authorized")
					return
				}
			default:
				fail(w, 400, "unsupported incident query parameter")
				return
			}
		}
		var scope []string
		if !a.operator {
			scope = a.namespaceList()
			if scope == nil {
				scope = []string{}
			}
		}
		if ns := r.URL.Query().Get("namespace"); ns != "" {
			scope = []string{ns}
		}
		list, total, e := s.store.Page(scope, limit, offset)
		if e != nil {
			fail(w, 503, "incident storage unavailable")
			return
		}
		out := []incidents.Incident{}
		for _, i := range list {
			out = append(out, sanitizeIncident(i, a))
		}
		jsonResponse(w, 200, map[string]any{"incidents": out, "source": s.sourceStatus(), "readOnly": !a.operator || s.config.ReadOnly || s.config.Demo, "total": total, "limit": limit, "offset": offset})
		return
	}
	if route == "/api/triage/status" && r.Method == "GET" {
		if !a.operator {
			fail(w, 403, "operator access required")
			return
		}
		usage, e := s.store.Usage(time.Now().UTC().Format("2006-01"))
		if e != nil {
			fail(w, 503, "usage unavailable")
			return
		}
		s.workflowMu.Lock()
		state := s.triageState
		s.workflowMu.Unlock()
		decisions, e := s.store.Decisions(100)
		if e != nil {
			fail(w, 503, "decisions unavailable")
			return
		}
		jsonResponse(w, 200, map[string]any{"enabled": s.config.Triage.Enabled, "apply": s.config.Triage.Apply && !s.config.ReadOnly, "state": state, "usage": usage, "monthlyBudgetUSD": s.config.Triage.MonthlyBudgetUSD, "decisions": decisions})
		return
	}
	if strings.HasPrefix(route, "/api/deliveries/") && strings.HasSuffix(route, "/retry") && r.Method == "POST" {
		if !s.canWrite(w, r, a) {
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(route, "/api/deliveries/"), "/retry")
		if e := s.store.RetryDelivery(id, time.Now().UTC()); e != nil {
			incidentError(w, e)
			return
		}
		jsonResponse(w, 200, map[string]bool{"queued": true})
		return
	}
	parts := strings.Split(strings.TrimPrefix(route, "/api/incidents/"), "/")
	if len(parts) < 1 || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(parts[0]) {
		fail(w, 404, "incident not found")
		return
	}
	id := parts[0]
	i, e := s.store.Get(id)
	if e != nil {
		incidentError(w, e)
		return
	}
	if !a.allowed(map[string]string{"namespace": i.Namespace}) {
		fail(w, 404, "incident not found")
		return
	}
	if len(parts) == 1 && r.Method == "GET" {
		jsonResponse(w, 200, sanitizeIncident(i, a))
		return
	}
	if len(parts) == 2 && parts[1] == "deliveries" && r.Method == "GET" {
		if !a.operator {
			fail(w, 403, "operator access required")
			return
		}
		d, e := s.store.Deliveries(id)
		if e != nil {
			fail(w, 503, "deliveries unavailable")
			return
		}
		jsonResponse(w, 200, d)
		return
	}
	if r.Method != "PATCH" {
		fail(w, 405, "method not allowed")
		return
	}
	if !s.canWrite(w, r, a) {
		return
	}
	if len(parts) == 1 {
		var m struct {
			Version int64 `json:"version"`
			incidents.Mutation
		}
		if !decodeMutation(w, r, &m) {
			return
		}
		updated, e := s.store.Update(id, m.Version, m.Mutation, a.identity, time.Now().UTC())
		if e != nil {
			incidentError(w, e)
			return
		}
		jsonResponse(w, 200, updated)
		return
	}
	if len(parts) == 2 && parts[1] == "membership" {
		var m struct {
			Version          int64  `json:"version"`
			Fingerprint      string `json:"fingerprint"`
			TargetIncidentID string `json:"targetIncidentId"`
			TargetVersion    int64  `json:"targetVersion"`
		}
		if !decodeMutation(w, r, &m) {
			return
		}
		if e := s.store.MoveMember(id, m.TargetIncidentID, m.Fingerprint, m.Version, m.TargetVersion, a.identity, time.Now().UTC()); e != nil {
			incidentError(w, e)
			return
		}
		updated, err := s.store.Get(id)
		if err != nil {
			incidentError(w, err)
			return
		}
		jsonResponse(w, 200, updated)
		return
	}
	fail(w, 404, "endpoint not found")
}

func (s *Server) runPeriodic(ctx context.Context, every time.Duration, fn func(context.Context, time.Time)) {
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				fn(ctx, now.UTC())
			}
		}
	}()
}
