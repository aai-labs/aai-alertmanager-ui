// Package incidents keeps optional workflow state separate from Prometheus history.
package incidents

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	_ "modernc.org/sqlite"
)

const dbTimeFormat = "2006-01-02T15:04:05.000000000Z"

var ErrConflict = errors.New("incident changed; reload before updating")
var ErrNotFound = errors.New("incident not found")
var ErrActive = errors.New("active incidents cannot be closed")

type Member struct {
	Fingerprint string            `json:"fingerprint"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Status      map[string]any    `json:"status"`
}
type Event struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail"`
}
type Incident struct {
	ID             string     `json:"id"`
	Namespace      string     `json:"namespace"`
	Title          string     `json:"title"`
	SourceSeverity string     `json:"sourceSeverity"`
	Urgency        string     `json:"urgency"`
	Owner          string     `json:"owner"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
	ReviewAt       *time.Time `json:"reviewAt,omitempty"`
	FirstSeen      time.Time  `json:"firstSeen"`
	LastSeen       time.Time  `json:"lastSeen"`
	ClearedAt      *time.Time `json:"clearedAt,omitempty"`
	ClosedAt       *time.Time `json:"closedAt,omitempty"`
	PreviousID     string     `json:"previousId,omitempty"`
	Version        int64      `json:"version"`
	Members        []Member   `json:"members"`
	Timeline       []Event    `json:"timeline"`
	Model          *Decision  `json:"model,omitempty"`
	GroupKey       string     `json:"-"`
	Missing        int        `json:"-"`
	ReviewRevision int64      `json:"-"`
}
type Mutation struct {
	Owner       *string `json:"owner,omitempty"`
	ReviewAt    *string `json:"reviewAt,omitempty"`
	Acknowledge bool    `json:"acknowledge,omitempty"`
	Close       bool    `json:"close,omitempty"`
}
type Store struct {
	db   *sql.DB
	lock *flock.Flock
	mu   sync.Mutex
}
type record struct {
	Incident
	Group    string `json:"groupKey"`
	Absent   int    `json:"missingSnapshots"`
	Revision int64  `json:"reviewRevision"`
}

func Open(path string) (*Store, error) {
	if path == "" || path == ":memory:" {
		return nil, errors.New("a persistent database file is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	lock := flock.New(abs + ".lock")
	ok, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("incident database already has an application writer")
	}
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		_ = lock.Unlock()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, lock: lock}
	fail := func(e error) (*Store, error) { _ = s.Close(); return nil, e }
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON"} {
		if _, err = db.Exec(q); err != nil {
			return fail(err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return fail(err)
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`,
		`INSERT INTO schema_version(version) SELECT 1 WHERE NOT EXISTS(SELECT 1 FROM schema_version)`,
		`CREATE TABLE IF NOT EXISTS incidents (id TEXT PRIMARY KEY, group_key TEXT NOT NULL, namespace TEXT NOT NULL, closed_at TEXT, body BLOB NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS incidents_group ON incidents(group_key)`,
		`CREATE TABLE IF NOT EXISTS assignments (fingerprint TEXT PRIMARY KEY, incident_id TEXT NOT NULL, operator_fixed INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS outbox (id TEXT PRIMARY KEY, incident_id TEXT NOT NULL, revision INTEGER NOT NULL, status TEXT NOT NULL, next_attempt TEXT NOT NULL, body BLOB NOT NULL, UNIQUE(incident_id,revision))`,
		`CREATE TABLE IF NOT EXISTS decisions (input_hash TEXT PRIMARY KEY, created_at TEXT NOT NULL, body BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS reservations (id TEXT PRIMARY KEY, month TEXT NOT NULL, amount REAL NOT NULL, settled INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS usage (month TEXT PRIMARY KEY, reserved REAL NOT NULL DEFAULT 0, spent REAL NOT NULL DEFAULT 0, input_tokens INTEGER NOT NULL DEFAULT 0, requests INTEGER NOT NULL DEFAULT 0)`,
	}
	for _, q := range statements {
		if _, err = tx.Exec(q); err != nil {
			_ = tx.Rollback()
			return fail(err)
		}
	}
	var version int
	if err = tx.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil || version != 1 {
		_ = tx.Rollback()
		return fail(errors.New("unsupported incident database version"))
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	_ = os.Chmod(abs, 0600)
	return s, nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if s.db != nil {
		err = s.db.Close()
		s.db = nil
	}
	if s.lock != nil {
		if e := s.lock.Unlock(); err == nil {
			err = e
		}
		s.lock = nil
	}
	return err
}
func id() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func group(m Member) string { return m.Labels["namespace"] + "\x00" + m.Labels["alertname"] }
func Fingerprint(m Member) string {
	if m.Fingerprint != "" {
		return m.Fingerprint
	}
	b, _ := json.Marshal(m.Labels)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}
func decode(b []byte) (Incident, error) {
	var r record
	err := json.Unmarshal(b, &r)
	r.Incident.GroupKey = r.Group
	r.Incident.Missing = r.Absent
	r.Incident.ReviewRevision = r.Revision
	return r.Incident, err
}
func save(tx *sql.Tx, i Incident) error {
	b, err := json.Marshal(record{Incident: i, Group: i.GroupKey, Absent: i.Missing, Revision: i.ReviewRevision})
	if err != nil {
		return err
	}
	var closed any
	if i.ClosedAt != nil {
		closed = i.ClosedAt.UTC().Format(dbTimeFormat)
	}
	_, err = tx.Exec(`INSERT INTO incidents(id,group_key,namespace,closed_at,body) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET group_key=excluded.group_key,namespace=excluded.namespace,closed_at=excluded.closed_at,body=excluded.body`, i.ID, i.GroupKey, i.Namespace, closed, b)
	return err
}
func loadAll(tx *sql.Tx) ([]Incident, error) {
	rows, err := tx.Query("SELECT body FROM incidents")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		i, e := decode(b)
		if e != nil {
			return nil, e
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func (s *Store) List() ([]Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out, err := loadAll(tx)
	sort.Slice(out, func(a, b int) bool { return out[a].FirstSeen.Before(out[b].FirstSeen) })
	return out, err
}
func get(tx *sql.Tx, identity string) (Incident, error) {
	var b []byte
	err := tx.QueryRow("SELECT body FROM incidents WHERE id=?", identity).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, err
	}
	return decode(b)
}
func (s *Store) Get(identity string) (Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback()
	return get(tx, identity)
}
func event(i *Incident, now time.Time, actor, kind, detail string) {
	i.Timeline = append(i.Timeline, Event{now.UTC(), actor, kind, detail})
}
func severity(members []Member) string {
	result := "unclassified"
	for _, m := range members {
		v := m.Labels["severity"]
		if v == "critical" {
			return v
		}
		if v == "warning" {
			result = v
		} else if v == "info" && result == "unclassified" {
			result = v
		}
	}
	return result
}
func urgency(sev string) string {
	switch sev {
	case "critical":
		return "immediate"
	case "info":
		return "monitor"
	default:
		return "today"
	}
}
func title(m Member) string {
	if v := m.Annotations["summary"]; v != "" {
		return v
	}
	if v := m.Labels["alertname"]; v != "" {
		return v
	}
	return "Unclassified alert"
}
func sameMembers(a, b []Member) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Observe must only be called for complete successful Alertmanager snapshots.
// It deliberately does not infer recovery from a failed fetch.
func (s *Store) Observe(members []Member, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now = now.UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	all, err := loadAll(tx)
	if err != nil {
		return err
	}
	byID := map[string]Incident{}
	byGroup := map[string]Incident{}
	for _, i := range all {
		byID[i.ID] = i
		if old, ok := byGroup[i.GroupKey]; !ok || i.FirstSeen.After(old.FirstSeen) {
			byGroup[i.GroupKey] = i
		}
	}
	assigned := map[string]string{}
	rows, err := tx.Query("SELECT fingerprint,incident_id FROM assignments")
	if err != nil {
		return err
	}
	for rows.Next() {
		var f, k string
		if err = rows.Scan(&f, &k); err != nil {
			rows.Close()
			return err
		}
		assigned[f] = k
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	groups := map[string][]Member{}
	seen := map[string]bool{}
	for _, m := range members {
		m.Fingerprint = Fingerprint(m)
		if seen[m.Fingerprint] {
			continue
		}
		seen[m.Fingerprint] = true
		k := group(m)
		chosen, ok := byGroup[k]
		if target, exists := assigned[m.Fingerprint]; exists {
			if i, found := byID[target]; found && i.Namespace == m.Labels["namespace"] {
				chosen = i
				k = i.GroupKey
				if latest, exists := byGroup[k]; exists && latest.FirstSeen.After(chosen.FirstSeen) {
					chosen = latest
				}
				ok = true
			}
		}
		if !ok || (chosen.ClearedAt != nil && now.Sub(*chosen.ClearedAt) > 24*time.Hour) {
			previous := ""
			if ok {
				previous = chosen.ID
			}
			chosen = Incident{ID: id(), Namespace: m.Labels["namespace"], Title: title(m), FirstSeen: now, LastSeen: now, Version: 0, GroupKey: k, PreviousID: previous, Members: []Member{}, Timeline: []Event{}}
			byGroup[k] = chosen
			byID[chosen.ID] = chosen
			if _, exists := assigned[m.Fingerprint]; exists {
				if _, err = tx.Exec("UPDATE assignments SET incident_id=? WHERE fingerprint=?", chosen.ID, m.Fingerprint); err != nil {
					return err
				}
				assigned[m.Fingerprint] = chosen.ID
			}
		}
		if target, exists := assigned[m.Fingerprint]; exists && target != chosen.ID {
			if _, err = tx.Exec("UPDATE assignments SET incident_id=? WHERE fingerprint=?", chosen.ID, m.Fingerprint); err != nil {
				return err
			}
			assigned[m.Fingerprint] = chosen.ID
		}
		groups[chosen.ID] = append(groups[chosen.ID], m)
	}
	for identity, i := range byID {
		current, present := groups[identity]
		if !present {
			if i.ClosedAt != nil || i.ClearedAt != nil {
				continue
			}
			i.Missing++
			if i.Missing >= 2 {
				i.ClearedAt = &now
				i.Version++
				event(&i, now, "system", "condition_cleared", "Absent from two successful snapshots")
			}
			if err = save(tx, i); err != nil {
				return err
			}
			continue
		}
		sort.Slice(current, func(a, b int) bool { return current[a].Fingerprint < current[b].Fingerprint })
		changed := !sameMembers(i.Members, current) || i.Missing != 0 || i.ClearedAt != nil || i.ClosedAt != nil
		if i.Version == 0 {
			event(&i, now, "system", "opened", "First observed locally")
			changed = true
		}
		if i.ClosedAt != nil || i.ClearedAt != nil {
			event(&i, now, "system", "reopened", "Condition returned")
			i.ClosedAt = nil
			i.ClearedAt = nil
			i.AcknowledgedAt = nil
		}
		i.Missing = 0
		if !changed {
			continue
		}
		i.LastSeen = now
		i.Members = current
		i.SourceSeverity = severity(current)
		if i.Model == nil {
			i.Urgency = urgency(i.SourceSeverity)
		}
		if i.SourceSeverity == "critical" {
			i.Urgency = "immediate"
		}
		i.Version++
		if err = save(tx, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Update(identity string, version int64, m Mutation, actor string, now time.Time) (Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback()
	i, err := get(tx, identity)
	if err != nil {
		return i, err
	}
	if i.Version != version {
		return i, ErrConflict
	}
	now = now.UTC()
	changed := false
	if m.Owner != nil {
		if len(*m.Owner) > 200 || strings.ContainsAny(*m.Owner, "\r\n") {
			return i, errors.New("invalid owner")
		}
		i.Owner = strings.TrimSpace(*m.Owner)
		event(&i, now, actor, "owner_changed", i.Owner)
		changed = true
	}
	if m.Acknowledge && i.AcknowledgedAt == nil {
		i.AcknowledgedAt = &now
		event(&i, now, actor, "acknowledged", "")
		changed = true
	}
	if m.ReviewAt != nil {
		var date *time.Time
		if *m.ReviewAt != "" {
			d, e := time.Parse(time.RFC3339, *m.ReviewAt)
			if e != nil {
				return i, errors.New("reviewAt must be RFC3339 or empty")
			}
			d = d.UTC()
			date = &d
		}
		i.ReviewAt = date
		i.ReviewRevision++
		if _, err = tx.Exec("UPDATE outbox SET status='cancelled' WHERE incident_id=? AND status IN ('pending','failed')", identity); err != nil {
			return i, err
		}
		event(&i, now, actor, "review_scheduled", *m.ReviewAt)
		changed = true
	}
	if m.Close {
		if i.ClearedAt == nil {
			return i, ErrActive
		}
		if i.ClosedAt == nil {
			i.ClosedAt = &now
			event(&i, now, actor, "closed", "")
			changed = true
		}
		if _, err = tx.Exec("UPDATE outbox SET status='cancelled' WHERE incident_id=? AND status IN ('pending','failed')", identity); err != nil {
			return i, err
		}
	}
	if changed {
		i.Version++
		if err = save(tx, i); err != nil {
			return i, err
		}
	}
	err = tx.Commit()
	return i, err
}

// MoveMember is an explicit, version-checked operator correction. It never joins namespaces.
func (s *Store) MoveMember(from, to, fingerprint string, fromVersion, toVersion int64, actor string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if from == to {
		return errors.New("source and destination must differ")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	a, err := get(tx, from)
	if err != nil {
		return err
	}
	b, err := get(tx, to)
	if err != nil {
		return err
	}
	if a.Version != fromVersion || b.Version != toVersion {
		return ErrConflict
	}
	if a.Namespace != b.Namespace {
		return errors.New("members cannot cross namespace boundaries")
	}
	if a.ClosedAt != nil || b.ClosedAt != nil || a.ClearedAt != nil || b.ClearedAt != nil {
		return errors.New("membership changes require active source and destination incidents")
	}
	found := false
	remaining := []Member{}
	for _, m := range a.Members {
		if m.Fingerprint == fingerprint {
			b.Members = append(b.Members, m)
			found = true
		} else {
			remaining = append(remaining, m)
		}
	}
	if !found {
		return errors.New("member not found")
	}
	a.Members = remaining
	a.Version++
	b.Version++
	a.SourceSeverity = severity(a.Members)
	b.SourceSeverity = severity(b.Members)
	if b.SourceSeverity == "critical" {
		b.Urgency = "immediate"
	}
	event(&a, now, actor, "member_moved", fingerprint+" → "+to)
	event(&b, now, actor, "member_added", fingerprint+" ← "+from)
	if _, err = tx.Exec("INSERT INTO assignments(fingerprint,incident_id,operator_fixed) VALUES(?,?,1) ON CONFLICT(fingerprint) DO UPDATE SET incident_id=excluded.incident_id,operator_fixed=1", fingerprint, to); err != nil {
		return err
	}
	if err = save(tx, a); err != nil {
		return err
	}
	if err = save(tx, b); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) MoveModelMember(from, to, fingerprint string, fromVersion, toVersion int64, now time.Time) error {
	actor := "jev"
	s.mu.Lock()
	defer s.mu.Unlock()
	if from == to {
		return errors.New("source and destination must differ")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var fixed int
	err = tx.QueryRow("SELECT operator_fixed FROM assignments WHERE fingerprint=?", fingerprint).Scan(&fixed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if fixed != 0 {
		return errors.New("operator membership correction takes precedence")
	}
	a, err := get(tx, from)
	if err != nil {
		return err
	}
	b, err := get(tx, to)
	if err != nil {
		return err
	}
	if a.Version != fromVersion || b.Version != toVersion {
		return ErrConflict
	}
	if a.Namespace != b.Namespace {
		return errors.New("members cannot cross namespace boundaries")
	}
	if a.ClosedAt != nil || b.ClosedAt != nil || a.ClearedAt != nil || b.ClearedAt != nil {
		return errors.New("membership changes require active source and destination incidents")
	}
	found := false
	remaining := []Member{}
	for _, m := range a.Members {
		if m.Fingerprint == fingerprint {
			b.Members = append(b.Members, m)
			found = true
		} else {
			remaining = append(remaining, m)
		}
	}
	if !found {
		return errors.New("member not found")
	}
	a.Members = remaining
	a.Version++
	b.Version++
	a.SourceSeverity = severity(a.Members)
	b.SourceSeverity = severity(b.Members)
	if b.SourceSeverity == "critical" {
		b.Urgency = "immediate"
	}
	event(&a, now, actor, "member_moved", fingerprint+" → "+to)
	event(&b, now, actor, "member_added", fingerprint+" ← "+from)
	if _, err = tx.Exec("INSERT INTO assignments(fingerprint,incident_id,operator_fixed) VALUES(?,?,0) ON CONFLICT(fingerprint) DO UPDATE SET incident_id=excluded.incident_id", fingerprint, to); err != nil {
		return err
	}
	if err = save(tx, a); err != nil {
		return err
	}
	if err = save(tx, b); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Prune(now time.Time, days int) error {
	if days <= 0 {
		days = 90
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cutoff := now.UTC().AddDate(0, 0, -days).Format(dbTimeFormat)
	for _, q := range []string{"DELETE FROM assignments WHERE incident_id IN (SELECT id FROM incidents WHERE closed_at < ?)", "DELETE FROM outbox WHERE incident_id IN (SELECT id FROM incidents WHERE closed_at < ?)", "DELETE FROM incidents WHERE closed_at < ?", "DELETE FROM decisions WHERE created_at < ?"} {
		if _, err = tx.Exec(q, cutoff); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Checkpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}
func (s *Store) String() string { return fmt.Sprintf("incident store") }

// Page reads a bounded, namespace-scoped page. A nil namespace slice is operator
// access; an empty non-nil slice grants no namespaces. Timelines belong to detail reads.
func (s *Store) Page(namespaces []string, limit, offset int) ([]Incident, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	if namespaces != nil && len(namespaces) == 0 {
		return []Incident{}, 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	where := ""
	args := []any{}
	if namespaces != nil {
		marks := make([]string, len(namespaces))
		for n, ns := range namespaces {
			marks[n] = "?"
			args = append(args, ns)
		}
		where = " WHERE namespace IN (" + strings.Join(marks, ",") + ")"
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var total int
	if err = tx.QueryRow("SELECT count(*) FROM incidents"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := tx.Query("SELECT body FROM incidents"+where+" ORDER BY CASE WHEN json_extract(body,'$.closedAt') IS NOT NULL THEN 2 WHEN json_extract(body,'$.clearedAt') IS NULL AND (json_extract(body,'$.sourceSeverity')='critical' OR json_extract(body,'$.urgency')='immediate') THEN 0 ELSE 1 END, julianday(json_extract(body,'$.firstSeen')),id LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, 0, err
		}
		i, e := decode(b)
		if e != nil {
			return nil, 0, e
		}
		i.Timeline = []Event{}
		out = append(out, i)
	}
	return out, total, rows.Err()
}
