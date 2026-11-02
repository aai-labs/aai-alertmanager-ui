package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aai-labs/aai-alertmanager-ui/internal/incidents"
	"github.com/aai-labs/aai-alertmanager-ui/internal/triage"
	"sort"
	"time"
)

func candidate(i incidents.Incident) triage.Candidate {
	c := triage.Candidate{ID: i.ID, Namespace: i.Namespace, Labels: []map[string]string{}, Annotations: []map[string]string{}}
	for _, m := range i.Members {
		l, a := triage.SelectFields(m.Labels, m.Annotations)
		c.Labels = append(c.Labels, l)
		c.Annotations = append(c.Annotations, a)
	}
	return c
}
func (s *Server) setTriageState(state string) {
	s.workflowMu.Lock()
	s.triageState = state
	s.workflowMu.Unlock()
}
func (s *Server) evaluateTriage(ctx context.Context, now time.Time) {
	if s.sourceStatus()["state"] != "connected" {
		s.setTriageState("waiting_for_source")
		return
	}
	list, e := s.store.List()
	if e != nil {
		s.setTriageState("storage_error")
		return
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	requests := 0
	for _, i := range list {
		if ctx.Err() != nil {
			return
		}
		if i.ClosedAt != nil || i.ClearedAt != nil || len(i.Members) == 0 {
			continue
		}
		in := triage.Input{Incident: candidate(i), Candidates: []triage.Candidate{}}
		// Same-namespace candidates have first priority; bounded cross-namespace hypotheses remain operator-only.
		for _, same := range []bool{true, false} {
			for _, other := range list {
				if other.ID != i.ID && len(other.Members) > 0 && other.ClosedAt == nil && other.ClearedAt == nil && (other.Namespace == i.Namespace) == same && len(in.Candidates) < 20 {
					in.Candidates = append(in.Candidates, candidate(other))
				}
			}
		}
		req, b, hash, e := s.triageClient.Prepare(in)
		if e != nil {
			s.setTriageState("context_limit")
			continue
		}
		d, cachedErr := s.store.GetDecision(hash)
		var result triage.Result
		if cachedErr == nil {
			if !s.config.Triage.Apply || s.config.ReadOnly || d.Applied {
				continue
			}
			if json.Unmarshal(d.Decisions, &result.Answers) != nil {
				continue
			}
		} else {
			if !errors.Is(cachedErr, incidents.ErrNotFound) {
				s.setTriageState("storage_error")
				return
			}
			attempts, err := s.store.ReservationAttempts(hash + ":")
			if err != nil {
				s.setTriageState("storage_error")
				return
			}
			if attempts >= 3 {
				s.setTriageState("retry_limit")
				continue
			}
			if requests >= 5 {
				s.setTriageState("queued")
				return
			}
			requests++
			month := now.Format("2006-01")
			reservation := hash + ":" + now.Format(time.RFC3339Nano)
			ok, err := s.store.ReserveBudget(month, reservation, triage.ReservationUSD(b), s.config.Triage.MonthlyBudgetUSD)
			if err != nil {
				s.setTriageState("storage_error")
				return
			}
			if !ok {
				s.setTriageState("budget_exhausted")
				return
			}
			result, err = s.triageClient.Evaluate(ctx, req, b)
			if err != nil {
				s.setTriageState("provider_error")
				return
			}
			if err = s.store.SettleBudget(month, reservation, result.Usage.Cost, int64(result.Usage.InputTokens)); err != nil {
				s.setTriageState("storage_error")
				return
			}
			body, _ := json.Marshal(result.Answers)
			d = incidents.Decision{InputHash: hash, IncidentID: i.ID, Model: result.Model, PolicyVersion: s.config.Triage.PolicyVersion, Decisions: body, InputTokens: int64(result.Usage.InputTokens), CostUSD: result.Usage.Cost, CreatedAt: now}
			if err = s.store.RecordDecision(d); err != nil {
				s.setTriageState("storage_error")
				return
			}
		}
		if !s.config.Triage.Apply || s.config.ReadOnly {
			s.setTriageState("shadow")
			continue
		}
		// A stale observation or operator edit invalidates application, but usage remains recorded.
		if s.sourceStatus()["state"] != "connected" {
			continue
		}
		current, e := s.store.Get(i.ID)
		if e != nil || current.Version != i.Version {
			continue
		}
		u := result.Choice("urgency")
		if u == "" {
			u = "today"
			if i.SourceSeverity == "critical" {
				u = "immediate"
			} else if i.SourceSeverity == "info" {
				u = "monitor"
			}
		}
		if e = s.store.ApplyTriage(i.ID, i.Version, u, result.Choice("team"), result.Choice("runbook"), d, now); e != nil {
			continue
		}
		target := result.Choice("membership")
		if target != "" {
			expectedVersion := int64(0)
			for _, candidate := range list {
				if candidate.ID == target {
					expectedVersion = candidate.Version
				}
			}
			targetNow, err := s.store.Get(target)
			if err != nil || targetNow.Version != expectedVersion || len(targetNow.Members) == 0 {
				continue
			}
			for _, member := range i.Members {
				from, e1 := s.store.Get(i.ID)
				to, e2 := s.store.Get(target)
				if e1 != nil || e2 != nil || to.Namespace != i.Namespace {
					break
				}
				if e := s.store.MoveModelMember(from.ID, to.ID, member.Fingerprint, from.Version, to.Version, now); e != nil {
					break
				}
			}
		}
		s.setTriageState("active")
	}
}
