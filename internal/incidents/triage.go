package incidents

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"
)

type Decision struct {
	InputHash     string          `json:"inputHash"`
	Model         string          `json:"model"`
	PolicyVersion string          `json:"policyVersion"`
	IncidentID    string          `json:"incidentId"`
	Decisions     json.RawMessage `json:"decisions"`
	InputTokens   int64           `json:"inputTokens"`
	CostUSD       float64         `json:"costUSD"`
	CreatedAt     time.Time       `json:"createdAt"`
	Applied       bool            `json:"applied"`
	Team          string          `json:"team,omitempty"`
	Runbook       string          `json:"runbook,omitempty"`
}
type Usage struct {
	Month       string  `json:"month"`
	ReservedUSD float64 `json:"reservedUSD"`
	SpentUSD    float64 `json:"spentUSD"`
	InputTokens int64   `json:"inputTokens"`
	Requests    int64   `json:"requests"`
}

func (s *Store) HasDecision(hash string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	err := s.db.QueryRow("SELECT count(*) FROM decisions WHERE input_hash=?", hash).Scan(&n)
	return n > 0, err
}
func (s *Store) RecordDecision(d Decision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO decisions(input_hash,created_at,body) VALUES(?,?,?) ON CONFLICT(input_hash) DO UPDATE SET body=excluded.body", d.InputHash, d.CreatedAt.UTC().Format(dbTimeFormat), b)
	return err
}
func (s *Store) Decisions(limit int) ([]Decision, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT body FROM decisions ORDER BY created_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Decision{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var d Decision
		if err = json.Unmarshal(b, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) ReserveBudget(month, reservationID string, amount, budget float64) (bool, error) {
	if amount < 0 || budget <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || math.IsNaN(budget) || math.IsInf(budget, 0) {
		return false, errors.New("invalid budget")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT OR IGNORE INTO usage(month) VALUES(?)", month); err != nil {
		return false, err
	}
	var used float64
	if err = tx.QueryRow("SELECT reserved+spent FROM usage WHERE month=?", month).Scan(&used); err != nil {
		return false, err
	}
	var n int
	if err = tx.QueryRow("SELECT count(*) FROM reservations WHERE id=?", reservationID).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 || used+amount > budget {
		return false, nil
	}
	if _, err = tx.Exec("INSERT INTO reservations(id,month,amount,settled) VALUES(?,?,?,0)", reservationID, month, amount); err != nil {
		return false, err
	}
	if _, err = tx.Exec("UPDATE usage SET reserved=reserved+? WHERE month=?", amount, month); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
func (s *Store) SettleBudget(month, reservationID string, cost float64, tokens int64) error {
	if cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) || tokens < 0 {
		return errors.New("invalid usage")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var amount float64
	var settled int
	if err = tx.QueryRow("SELECT amount,settled FROM reservations WHERE id=? AND month=?", reservationID, month).Scan(&amount, &settled); err != nil {
		return err
	}
	if settled != 0 {
		return nil
	}
	if _, err = tx.Exec("UPDATE usage SET reserved=max(0,reserved-?),spent=spent+?,input_tokens=input_tokens+?,requests=requests+1 WHERE month=?", amount, cost, tokens, month); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE reservations SET settled=1 WHERE id=?", reservationID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Usage(month string) (Usage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := Usage{Month: month}
	err := s.db.QueryRow("SELECT reserved,spent,input_tokens,requests FROM usage WHERE month=?", month).Scan(&u.ReservedUSD, &u.SpentUSD, &u.InputTokens, &u.Requests)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return u, err
}
func (s *Store) ApplyTriage(identity string, version int64, proposedUrgency, team, runbook string, d Decision, now time.Time) error {
	if proposedUrgency != "immediate" && proposedUrgency != "today" && proposedUrgency != "monitor" {
		return errors.New("invalid urgency")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	i, err := get(tx, identity)
	if err != nil {
		return err
	}
	if i.Version != version {
		return ErrConflict
	}
	if i.ClosedAt != nil {
		return errors.New("closed incident")
	}
	i.Urgency = proposedUrgency
	if i.SourceSeverity == "critical" {
		i.Urgency = "immediate"
	}
	d.Applied = true
	d.Team = team
	d.Runbook = runbook
	i.Model = &d
	i.Version++
	event(&i, now, "jev", "triage_applied", i.Urgency)
	if err = save(tx, i); err != nil {
		return err
	}
	b, _ := json.Marshal(d)
	if _, err = tx.Exec("INSERT INTO decisions(input_hash,created_at,body) VALUES(?,?,?) ON CONFLICT(input_hash) DO UPDATE SET body=excluded.body", d.InputHash, d.CreatedAt.UTC().Format(dbTimeFormat), b); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ObserveFailure() error {
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
	for _, i := range all {
		if i.Missing > 0 && i.ClearedAt == nil {
			i.Missing = 0
			if err = save(tx, i); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) GetDecision(hash string) (Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b []byte
	if err := s.db.QueryRow("SELECT body FROM decisions WHERE input_hash=?", hash).Scan(&b); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Decision{}, ErrNotFound
		}
		return Decision{}, err
	}
	var d Decision
	err := json.Unmarshal(b, &d)
	return d, err
}

// ReservationAttempts includes unsettled attempts so restart cannot reset retry limits.
func (s *Store) ReservationAttempts(prefix string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	err := s.db.QueryRow("SELECT count(*) FROM reservations WHERE substr(id,1,length(?))=?", prefix, prefix).Scan(&n)
	return n, err
}
