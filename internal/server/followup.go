package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/aai-labs/aai-alertmanager-ui/internal/incidents"
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
		current, eligible, err := s.store.PrepareDelivery(d.ID)
		if err != nil {
			s.logger.Error("review delivery preparation failed")
			continue
		}
		if !eligible {
			continue
		}
		accepted, message := s.sendReview(ctx, client, current)
		if e = s.store.CompleteDelivery(d.ID, accepted, message, time.Now().UTC()); e != nil {
			s.logger.Error("review delivery state update failed")
		}
	}
}

// reviewEvent is the public webhook contract, separate from stored delivery state.
type reviewEvent struct {
	EventID    string    `json:"eventId"`
	Type       string    `json:"type"`
	IncidentID string    `json:"incidentId"`
	Owner      string    `json:"owner"`
	Urgency    string    `json:"urgency"`
	DueAt      time.Time `json:"dueAt"`
	Link       string    `json:"link"`
}

func (s *Server) sendReview(ctx context.Context, client *http.Client, d incidents.Delivery) (bool, string) {
	payload := reviewEvent{EventID: d.ID, Type: "incident.review_due", IncidentID: d.IncidentID, Owner: d.Owner, Urgency: d.Urgency, DueAt: d.DueAt, Link: d.Link}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, "webhook payload invalid"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.config.FollowUp.URL, bytes.NewReader(body))
	if err != nil {
		return false, "webhook request invalid"
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.webhookToken)
	req.Header.Set("Idempotency-Key", d.ID)
	resp, err := client.Do(req)
	if err != nil {
		return false, "webhook unavailable"
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, "webhook rejected delivery"
	}
	return true, "webhook accepted"
}
