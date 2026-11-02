package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

func (s *Server) deliverReviews(ctx context.Context, now time.Time) {
	if s.config.ReadOnly || s.config.Demo || s.config.FollowUp.URL == "" {
		return
	}
	if e := s.store.EnqueueDue(now, s.config.FollowUp.PublicURL); e != nil {
		s.logger.Error("review enqueue failed")
		return
	}
	deliveries, e := s.store.PendingDeliveries(now)
	if e != nil {
		s.logger.Error("review outbox unavailable")
		return
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, d := range deliveries {
		if ctx.Err() != nil {
			return
		}
		current, e := s.store.DeliveryCurrent(d.ID)
		if e != nil || !current {
			continue
		}
		incident, err := s.store.Get(d.IncidentID)
		if err != nil || incident.ClosedAt != nil {
			continue
		}
		d.Owner, d.Urgency = incident.Owner, incident.Urgency
		b, _ := json.Marshal(map[string]any{"eventId": d.ID, "type": "incident.review_due", "incidentId": d.IncidentID, "owner": d.Owner, "urgency": d.Urgency, "dueAt": d.DueAt, "link": d.Link})
		req, e := http.NewRequestWithContext(ctx, "POST", s.config.FollowUp.URL, bytes.NewReader(b))
		if e != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+s.webhookToken)
		req.Header.Set("Idempotency-Key", d.ID)
		resp, e := client.Do(req)
		accepted := false
		message := "webhook unavailable"
		if e == nil {
			accepted = resp.StatusCode >= 200 && resp.StatusCode < 300
			message = "webhook rejected delivery"
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
		}
		if accepted {
			message = "webhook accepted"
		}
		if e = s.store.CompleteDelivery(d.ID, accepted, message, time.Now().UTC()); e != nil {
			s.logger.Error("review delivery state update failed")
		}
	}
}
