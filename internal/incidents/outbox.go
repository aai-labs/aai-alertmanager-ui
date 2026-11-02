package incidents

import (
	"encoding/json"
	"errors"
	"net/url"
	"time"
)

type Delivery struct {
	ID          string     `json:"id"`
	IncidentID  string     `json:"incidentId"`
	Revision    int64      `json:"revision"`
	Owner       string     `json:"owner"`
	Urgency     string     `json:"urgency"`
	DueAt       time.Time  `json:"dueAt"`
	Link        string     `json:"link"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	CreatedAt   time.Time  `json:"createdAt"`
	NextAttempt time.Time  `json:"nextAttempt"`
	AcceptedAt  *time.Time `json:"acceptedAt,omitempty"`
	LastError   string     `json:"lastError,omitempty"`
}

func (s *Store) EnqueueDue(now time.Time, baseURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	all, err := loadAll(tx)
	if err != nil {
		return err
	}
	now = now.UTC()
	for _, i := range all {
		if i.ReviewAt == nil || i.ReviewAt.After(now) || i.ClosedAt != nil {
			continue
		}
		link := baseURL
		if u, e := url.Parse(baseURL); e == nil {
			q := u.Query()
			q.Set("incident", i.ID)
			u.RawQuery = q.Encode()
			link = u.String()
		}
		d := Delivery{ID: id(), IncidentID: i.ID, Revision: i.ReviewRevision, Owner: i.Owner, Urgency: i.Urgency, DueAt: *i.ReviewAt, Link: link, Status: "pending", CreatedAt: now, NextAttempt: now}
		b, _ := json.Marshal(d)
		if _, err = tx.Exec("INSERT OR IGNORE INTO outbox(id,incident_id,revision,status,next_attempt,body) VALUES(?,?,?,?,?,?)", d.ID, i.ID, d.Revision, d.Status, now.Format(dbTimeFormat), b); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Deliveries(incidentID string) ([]Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := "SELECT body,status FROM outbox"
	args := []any{}
	if incidentID != "" {
		q += " WHERE incident_id=?"
		args = append(args, incidentID)
	}
	q += " ORDER BY next_attempt DESC LIMIT 1000"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var b []byte
		var status string
		if err = rows.Scan(&b, &status); err != nil {
			return nil, err
		}
		var d Delivery
		if err = json.Unmarshal(b, &d); err != nil {
			return nil, err
		}
		d.Status = status
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) PendingDeliveries(now time.Time) ([]Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT body FROM outbox WHERE status='pending' AND next_attempt<=? ORDER BY next_attempt LIMIT 1000", now.UTC().Format(dbTimeFormat))
	if err != nil {
		return nil, err
	}
	pending := []Delivery{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return nil, err
		}
		var d Delivery
		if err = json.Unmarshal(b, &d); err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []Delivery{}
	for _, d := range pending {
		if now.Sub(d.CreatedAt) >= 24*time.Hour {
			d.Status = "failed"
			d.LastError = "Delivery window expired"
			b, _ := json.Marshal(d)
			if _, err = s.db.Exec("UPDATE outbox SET status='failed',body=? WHERE id=? AND status='pending'", b, d.ID); err != nil {
				return nil, err
			}
			continue
		}
		if len(out) < 20 {
			out = append(out, d)
		}
	}
	return out, nil
}
func (s *Store) CompleteDelivery(identity string, accepted bool, message string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var b []byte
	var state string
	if err = tx.QueryRow("SELECT body,status FROM outbox WHERE id=?", identity).Scan(&b, &state); err != nil {
		return err
	}
	if state != "pending" {
		return nil
	}
	var d Delivery
	if err = json.Unmarshal(b, &d); err != nil {
		return err
	}
	now = now.UTC()
	d.Attempts++
	if accepted {
		d.Status = "accepted"
		d.AcceptedAt = &now
		d.LastError = ""
	} else {
		if len(message) > 200 {
			message = message[:200]
		}
		d.LastError = message
		if now.Sub(d.CreatedAt) >= 24*time.Hour {
			d.Status = "failed"
		} else {
			d.Status = "pending"
			delay := time.Minute * time.Duration(1<<min(d.Attempts-1, 9))
			if delay > time.Hour {
				delay = time.Hour
			}
			d.NextAttempt = now.Add(delay)
			deadline := d.CreatedAt.Add(24 * time.Hour)
			if d.NextAttempt.After(deadline) {
				d.NextAttempt = deadline
			}
		}
	}
	b, _ = json.Marshal(d)
	if _, err = tx.Exec("UPDATE outbox SET status=?,next_attempt=?,body=? WHERE id=?", d.Status, d.NextAttempt.Format(dbTimeFormat), b, identity); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) RetryDelivery(identity string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var b []byte
	var state string
	if err = tx.QueryRow("SELECT body,status FROM outbox WHERE id=?", identity).Scan(&b, &state); err != nil {
		return err
	}
	if state != "failed" {
		return errors.New("only failed deliveries can be retried")
	}
	var d Delivery
	if err = json.Unmarshal(b, &d); err != nil {
		return err
	}
	i, err := get(tx, d.IncidentID)
	if err != nil {
		return err
	}
	if i.ClosedAt != nil || i.ReviewAt == nil || i.ReviewRevision != d.Revision {
		return errors.New("review is no longer current")
	}
	now = now.UTC()
	d.Status = "pending"
	d.CreatedAt = now
	d.NextAttempt = now
	d.LastError = ""
	b, _ = json.Marshal(d)
	if _, err = tx.Exec("UPDATE outbox SET status='pending',next_attempt=?,body=? WHERE id=?", now.Format(dbTimeFormat), b, identity); err != nil {
		return err
	}
	return tx.Commit()
}

// DeliveryCurrent guards queued work immediately before network delivery.
func (s *Store) DeliveryCurrent(identity string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var status string
	err := s.db.QueryRow("SELECT status FROM outbox WHERE id=?", identity).Scan(&status)
	return status == "pending", err
}
