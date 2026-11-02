package incidents

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func fixture(fp, name, ns, severity string) Member {
	return Member{Fingerprint: fp, Labels: map[string]string{"alertname": name, "namespace": ns, "severity": severity}, Annotations: map[string]string{"summary": name}, Status: map[string]any{"state": "active"}}
}
func one(t *testing.T, s *Store) Incident {
	t.Helper()
	a, e := s.List()
	if e != nil || len(a) != 1 {
		t.Fatalf("list: %v %v", a, e)
	}
	return a[0]
}
func TestLifecycleAndRecoveryGaps(t *testing.T) {
	s := openTest(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	m := fixture("f", "DiskFull", "a", "critical")
	if e := s.Observe([]Member{m, m}, now); e != nil {
		t.Fatal(e)
	}
	a := one(t, s)
	if len(a.Members) != 1 || a.Urgency != "immediate" {
		t.Fatal(a)
	}
	if e := s.Observe([]Member{m}, now.Add(time.Minute)); e != nil {
		t.Fatal(e)
	}
	if one(t, s).Version != a.Version {
		t.Fatal("identical snapshot wrote a version")
	}
	if _, e := s.Update(a.ID, a.Version, Mutation{Close: true}, "operator", now); !errors.Is(e, ErrActive) {
		t.Fatal(e)
	}
	s.Observe(nil, now.Add(time.Minute))
	s.ObserveFailure()
	s.Observe(nil, now.Add(2*time.Minute))
	if one(t, s).ClearedAt != nil {
		t.Fatal("gap falsely cleared")
	}
	s.Observe(nil, now.Add(3*time.Minute))
	a = one(t, s)
	if a.ClearedAt == nil {
		t.Fatal("not cleared")
	}
	a, e := s.Update(a.ID, a.Version, Mutation{Close: true}, "alice", now.Add(4*time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	s.Observe([]Member{m}, now.Add(time.Hour))
	a = one(t, s)
	if a.ClosedAt != nil || a.ClearedAt != nil {
		t.Fatal("not reopened")
	}
	s.Observe(nil, now.Add(2*time.Hour))
	s.Observe(nil, now.Add(2*time.Hour+time.Minute))
	s.Observe([]Member{m}, now.Add(48*time.Hour))
	all, e := s.List()
	if e != nil || len(all) != 2 {
		t.Fatalf("new recurrence %v %v", all, e)
	}
	if all[1].PreviousID != a.ID {
		t.Fatal("missing previous link")
	}
}
func TestPersistenceLockAndVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	if x, e := Open(path); e == nil {
		x.Close()
		t.Fatal("second writer allowed")
	}
	now := time.Now().UTC()
	s.Observe([]Member{fixture("1", "Rule", "a", "warning")}, now)
	a := one(t, s)
	owner := "Alice"
	updated, e := s.Update(a.ID, a.Version, Mutation{Owner: &owner, Acknowledge: true}, "Alice", now)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Update(a.ID, a.Version, Mutation{Owner: &owner}, "Bob", now); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a = one(t, s)
	if a.Owner != "Alice" || a.Version != updated.Version || a.AcknowledgedAt == nil {
		t.Fatal(a)
	}
}
func TestOutboxRevisionRetryAndCancellation(t *testing.T) {
	s := openTest(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s.Observe([]Member{fixture("1", "Rule", "a", "warning")}, now)
	a := one(t, s)
	date := now.Format(time.RFC3339)
	a, e := s.Update(a.ID, a.Version, Mutation{ReviewAt: &date}, "alice", now)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.EnqueueDue(now, "https://alerts.example/alerts/"); e != nil {
		t.Fatal(e)
	}
	s.EnqueueDue(now, "https://alerts.example")
	ds, e := s.PendingDeliveries(now)
	if e != nil || len(ds) != 1 {
		t.Fatal(ds, e)
	}
	d := ds[0]
	if d.ID == "" || d.Link == "" {
		t.Fatal(d)
	}
	s.CompleteDelivery(d.ID, false, "temporarily unavailable", now)
	ds, _ = s.PendingDeliveries(now)
	if len(ds) != 0 {
		t.Fatal("retry immediate")
	}
	ds, _ = s.PendingDeliveries(now.Add(time.Minute))
	if len(ds) != 1 {
		t.Fatal(ds)
	}
	s.CompleteDelivery(d.ID, false, "unavailable", now.Add(24*time.Hour))
	ds, _ = s.Deliveries(a.ID)
	if ds[0].Status != "failed" {
		t.Fatal(ds)
	}
	if e = s.RetryDelivery(d.ID, now.Add(25*time.Hour)); e != nil {
		t.Fatal(e)
	}
	ds, _ = s.PendingDeliveries(now.Add(25 * time.Hour))
	if len(ds) != 1 || ds[0].ID != d.ID {
		t.Fatal(ds)
	}
	later := now.Add(48 * time.Hour).Format(time.RFC3339)
	a, e = s.Update(a.ID, a.Version, Mutation{ReviewAt: &later}, "alice", now)
	if e != nil {
		t.Fatal(e)
	}
	ds, _ = s.PendingDeliveries(now.Add(25 * time.Hour))
	if len(ds) != 0 {
		t.Fatal("obsolete delivery remains")
	}
	s.EnqueueDue(now.Add(48*time.Hour), "https://alerts.example")
	ds, _ = s.PendingDeliveries(now.Add(48 * time.Hour))
	if len(ds) != 1 || ds[0].ID == d.ID {
		t.Fatal(ds)
	}
	s.CompleteDelivery(ds[0].ID, true, "", now.Add(48*time.Hour))
	ds, _ = s.Deliveries(a.ID)
	accepted := false
	for _, x := range ds {
		accepted = accepted || x.Status == "accepted"
	}
	if !accepted {
		t.Fatal(ds)
	}
}
func TestMembershipAndModelGuard(t *testing.T) {
	s := openTest(t)
	now := time.Now().UTC()
	members := []Member{fixture("1", "A", "ns", "critical"), fixture("2", "B", "ns", "warning"), fixture("3", "C", "other", "warning")}
	s.Observe(members, now)
	all, _ := s.List()
	byName := map[string]Incident{}
	for _, a := range all {
		byName[a.Title] = a
	}
	a, b, c := byName["A"], byName["B"], byName["C"]
	if e := s.MoveMember(a.ID, c.ID, "1", a.Version, c.Version, "alice", now); e == nil {
		t.Fatal("cross namespace allowed")
	}
	if e := s.MoveMember(a.ID, b.ID, "1", a.Version, b.Version, "alice", now); e != nil {
		t.Fatal(e)
	}
	s.Observe(members, now.Add(time.Minute))
	b, _ = s.Get(b.ID)
	if len(b.Members) != 2 || b.SourceSeverity != "critical" || b.Urgency != "immediate" {
		t.Fatal(b)
	}
	a, _ = s.Get(a.ID)
	if e := s.MoveModelMember(b.ID, a.ID, "1", b.Version, a.Version, now); e == nil {
		t.Fatal("model overrode operator")
	}
	d := Decision{InputHash: "hash", CreatedAt: now, Model: "jev", Decisions: json.RawMessage(`{"urgency":"monitor"}`)}
	if e := s.ApplyTriage(b.ID, b.Version, "monitor", "team", "runbook", d, now); e != nil {
		t.Fatal(e)
	}
	b, _ = s.Get(b.ID)
	if b.Urgency != "immediate" {
		t.Fatal("critical downgraded")
	}
	ok, e := s.HasDecision("hash")
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
}
func TestBudgetReservationIsDurableAndIdempotent(t *testing.T) {
	s := openTest(t)
	ok, e := s.ReserveBudget("2026-10", "a", 6, 10)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	ok, e = s.ReserveBudget("2026-10", "b", 5, 10)
	if e != nil || ok {
		t.Fatal(ok, e)
	}
	ok, e = s.ReserveBudget("2026-10", "a", 1, 10)
	if e != nil || ok {
		t.Fatal("duplicate", ok, e)
	}
	if e = s.SettleBudget("2026-10", "a", 2, 123); e != nil {
		t.Fatal(e)
	}
	s.SettleBudget("2026-10", "a", 2, 123)
	u, e := s.Usage("2026-10")
	if e != nil || u.SpentUSD != 2 || u.ReservedUSD != 0 || u.Requests != 1 {
		t.Fatal(u, e)
	}
}
func TestPruneKeepsOpen(t *testing.T) {
	s := openTest(t)
	now := time.Now().UTC()
	s.Observe([]Member{fixture("1", "A", "ns", "warning")}, now)
	if e := s.Prune(now.AddDate(0, 0, 100), 90); e != nil {
		t.Fatal(e)
	}
	a := one(t, s)
	s.Observe(nil, now)
	s.Observe(nil, now.Add(time.Minute))
	a = one(t, s)
	s.Update(a.ID, a.Version, Mutation{Close: true}, "operator", now)
	s.Prune(now.AddDate(0, 0, 100), 90)
	all, _ := s.List()
	if len(all) != 0 {
		t.Fatal(all)
	}
}

func TestMovedGroupRecurrenceDoesNotCreatePhantom(t *testing.T) {
	s := openTest(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	members := []Member{fixture("a1", "A", "ns", "critical"), fixture("a2", "A", "ns", "warning"), fixture("b1", "B", "ns", "info")}
	s.Observe(members, now)
	all, _ := s.List()
	var a, b Incident
	for _, x := range all {
		if x.Title == "A" {
			a = x
		} else {
			b = x
		}
	}
	for _, fp := range []string{"a1", "a2"} {
		if e := s.MoveMember(a.ID, b.ID, fp, a.Version, b.Version, "alice", now); e != nil {
			t.Fatal(e)
		}
		a, _ = s.Get(a.ID)
		b, _ = s.Get(b.ID)
	}
	for n := 1; n <= 3; n++ {
		if e := s.Observe(members, now.Add(time.Duration(n)*time.Minute)); e != nil {
			t.Fatal(e)
		}
	}
	all, _ = s.List()
	if len(all) != 2 {
		t.Fatal("phantom created", all)
	}
	a, _ = s.Get(a.ID)
	b, _ = s.Get(b.ID)
	if len(a.Members) != 0 || a.ClearedAt == nil || len(b.Members) != 3 || b.SourceSeverity != "critical" {
		t.Fatal(a, b)
	}
	s.Observe(nil, now.Add(time.Hour))
	s.Observe(nil, now.Add(time.Hour+time.Minute))
	if e := s.Observe(members, now.Add(2*time.Hour)); e != nil {
		t.Fatal(e)
	}
	all, _ = s.List()
	if len(all) != 2 {
		t.Fatal("short recurrence duplicated", all)
	}
	s.Observe(nil, now.Add(3*time.Hour))
	s.Observe(nil, now.Add(3*time.Hour+time.Minute))
	if e := s.Observe(members, now.Add(30*time.Hour)); e != nil {
		t.Fatal(e)
	}
	all, _ = s.List()
	if len(all) != 3 {
		t.Fatal("expected exactly one new grouped generation", len(all), all)
	}
	var newest Incident
	for _, i := range all {
		if i.FirstSeen.After(newest.FirstSeen) {
			newest = i
		}
	}
	if len(newest.Members) != 3 || newest.PreviousID != b.ID {
		t.Fatal(newest)
	}
	s.Observe(members, now.Add(31*time.Hour))
	all, _ = s.List()
	if len(all) != 3 {
		t.Fatal("stable recurrence duplicated", all)
	}
}
func TestOutboxExpiresAcrossRestartAndBackupRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s.Observe([]Member{fixture("1", "A", "ns", "warning")}, now)
	a := one(t, s)
	review := now.Format(time.RFC3339)
	s.Update(a.ID, a.Version, Mutation{ReviewAt: &review}, "alice", now)
	s.EnqueueDue(now, "https://alerts.example")
	ds, _ := s.PendingDeliveries(now.Add(time.Nanosecond))
	if len(ds) != 1 {
		t.Fatal("fractional timestamp missed delivery", ds)
	}
	s.Close()
	// A stopped database is a self-contained backup; restore into a separate directory.
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	restored := filepath.Join(t.TempDir(), "restored.db")
	if e = os.WriteFile(restored, data, 0600); e != nil {
		t.Fatal(e)
	}
	s, e = Open(restored)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	restoredIncident := one(t, s)
	if restoredIncident.ID != a.ID {
		t.Fatal("restore lost incident")
	}
	ds, e = s.PendingDeliveries(now.Add(25 * time.Hour))
	if e != nil || len(ds) != 0 {
		t.Fatal("expired delivery sent after restart", ds, e)
	}
	history, e := s.Deliveries(a.ID)
	if e != nil || len(history) != 1 || history[0].Status != "failed" {
		t.Fatal(history, e)
	}
}

func TestConcurrentOwnershipAndBudget(t *testing.T) {
	s := openTest(t)
	now := time.Now().UTC()
	if e := s.Observe([]Member{fixture("one", "A", "ns", "warning")}, now); e != nil {
		t.Fatal(e)
	}
	a := one(t, s)
	results := make(chan error, 2)
	for _, name := range []string{"Alice", "Bob"} {
		go func(owner string) {
			_, e := s.Update(a.ID, a.Version, Mutation{Owner: &owner}, owner, now)
			results <- e
		}(name)
	}
	success, conflicts := 0, 0
	for range 2 {
		e := <-results
		if e == nil {
			success++
		} else if errors.Is(e, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal(success, conflicts)
	}
	reserved := make(chan bool, 2)
	for _, key := range []string{"first", "second"} {
		go func(id string) {
			ok, e := s.ReserveBudget("2026-10", id, 6, 10)
			if e != nil {
				t.Error(e)
			}
			reserved <- ok
		}(key)
	}
	n := 0
	for range 2 {
		if <-reserved {
			n++
		}
	}
	if n != 1 {
		t.Fatal("overspent concurrent reserve", n)
	}
}

func TestScopedPageAndDecisionLookup(t *testing.T) {
	s := openTest(t)
	now := time.Now().UTC()
	for n, ns := range []string{"a", "b", "a", ""} {
		if e := s.Observe([]Member{fixture(fmt.Sprint(n), fmt.Sprint(n), ns, "warning")}, now.Add(time.Duration(n)*time.Minute)); e != nil {
			t.Fatal(e)
		}
	}
	list, total, e := s.Page([]string{"a"}, 1, 0)
	if e != nil || len(list) != 1 || total != 2 || list[0].Namespace != "a" || len(list[0].Timeline) != 0 {
		t.Fatal(list, total, e)
	}
	first := list[0].ID
	list, total, e = s.Page([]string{"a"}, 1, 1)
	if e != nil || len(list) != 1 || total != 2 || list[0].ID == first {
		t.Fatal(list, total, e)
	}
	list, total, e = s.Page([]string{}, 200, 0)
	if e != nil || total != 0 || len(list) != 0 {
		t.Fatal("empty grants leaked", list, total, e)
	}
	list, total, e = s.Page(nil, 200, 0)
	if e != nil || total != 4 || len(list) != 4 {
		t.Fatal(list, total, e)
	}
	d := Decision{InputHash: "shadow", Model: "pinned", IncidentID: first, CreatedAt: now, Decisions: json.RawMessage(`{"urgency":"today"}`)}
	if e = s.RecordDecision(d); e != nil {
		t.Fatal(e)
	}
	loaded, e := s.GetDecision("shadow")
	if e != nil || loaded.Model != "pinned" || loaded.Applied {
		t.Fatal(loaded, e)
	}
	if _, e = s.GetDecision("missing"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}

func TestReservationAttemptPrefixLiteral(t *testing.T) {
	s := openTest(t)
	for _, id := range []string{"hash%:1", "hash%:2", "hashX:3"} {
		if _, e := s.ReserveBudget("2026-10", id, .1, 10); e != nil {
			t.Fatal(e)
		}
	}
	n, e := s.ReservationAttempts("hash%:")
	if e != nil || n != 2 {
		t.Fatal(n, e)
	}
}

func TestPagePrioritizesCriticalBeyondFirst200(t *testing.T) {
	s := openTest(t)
	now := time.Now().UTC()
	members := []Member{}
	for n := range 201 {
		members = append(members, fixture(fmt.Sprint(n), "Warning"+fmt.Sprint(n), "ns", "warning"))
	}
	if e := s.Observe(members, now); e != nil {
		t.Fatal(e)
	}
	members = append(members, fixture("critical", "Critical", "ns", "critical"))
	if e := s.Observe(members, now.Add(time.Minute)); e != nil {
		t.Fatal(e)
	}
	page, total, e := s.Page(nil, 1, 0)
	if e != nil || total != 202 || len(page) != 1 || page[0].SourceSeverity != "critical" {
		t.Fatal(page, total, e)
	}
	critical := page[0]
	s.Observe(members[:201], now.Add(2*time.Minute))
	s.Observe(members[:201], now.Add(3*time.Minute))
	critical, _ = s.Get(critical.ID)
	if _, e = s.Update(critical.ID, critical.Version, Mutation{Close: true}, "alice", now); e != nil {
		t.Fatal(e)
	}
	page, _, e = s.Page(nil, 1, 201)
	if e != nil || len(page) != 1 || page[0].ID != critical.ID {
		t.Fatal("closed not last", page, e)
	}
}
