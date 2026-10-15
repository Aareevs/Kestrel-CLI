package storage

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

var ctx = context.Background()

// clock is a controllable time source.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newClock() *clock { return &clock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)} }

func openStore(t *testing.T, o Options) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(ctx, dir, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func ulid(i int) string { return fmt.Sprintf("01TESTSESSION%013d", i) }

// record writes a finished session with the given events.
func record(t *testing.T, s *Store, id string, started time.Time, evs []api.Event) {
	t.Helper()
	if err := s.CreateSession(ctx, NewSession{ID: id, Mode: "run", ProjectRoot: "/proj", PID: os.Getpid(), StartedAt: started, Services: []string{"api", "db"}}); err != nil {
		t.Fatal(err)
	}
	r := s.NewRecorder(id)
	for _, e := range evs {
		r.Emit(e)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("recorder: %v", err)
	}
	if err := s.FinishSession(ctx, id, api.SessionClosed, started.Add(time.Minute), []api.ServiceStatus{{Name: "api", Liveness: api.LivenessExited, Readiness: api.ReadinessUnknown}}); err != nil {
		t.Fatal(err)
	}
}

// events makes n numbered events spread across two services.
func events(id string, start time.Time, n int, msg func(i int) string) []api.Event {
	var out []api.Event
	for i := 1; i <= n; i++ {
		svc := "api"
		if i%2 == 0 {
			svc = "db"
		}
		sev, typ := api.SeverityInfo, api.EventLogLine
		if i%10 == 0 {
			sev = api.SeverityError
		}
		out = append(out, api.Event{
			ID: fmt.Sprintf("%s-E%06d", id, i), SessionID: id, Seq: uint64(i), Time: start.Add(time.Duration(i) * time.Millisecond),
			ServiceID: svc, PID: 100, Type: typ, Severity: sev, Source: api.SourceStdout, Message: msg(i),
			Fingerprint: "fp", Attrs: api.Attrs{Port: i},
		})
	}
	return out
}

func TestMigrationsApplyOnceAndAreRecorded(t *testing.T) {
	s, dir := openStore(t, Options{})
	v, err := s.SchemaVersion(ctx)
	if err != nil || v != 1 {
		t.Fatalf("version %d, %v", v, err)
	}
	for _, table := range []string{"sessions", "events", "segments", "schema_migrations"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing", table)
		}
	}
	for _, idx := range []string{"idx_events_service_time", "idx_events_type", "idx_sessions_project"} {
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name = ?`, idx).Scan(&n)
		if n != 1 {
			t.Errorf("index %s missing", idx)
		}
	}
	var mode string
	s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	if mode != "wal" {
		t.Errorf("journal_mode = %s, want wal", mode)
	}
	s.Close()
	// Reopening applies nothing new.
	s2, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	var n int
	s2.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n)
	if n != 1 {
		t.Fatalf("schema_migrations rows = %d", n)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	dir := t.TempDir()
	good := builtinMigrations()
	bad := append(append([]Migration{}, good...), Migration{Version: 2, Name: "broken", SQL: `
		CREATE TABLE half_done (x INTEGER);
		INSERT INTO half_done VALUES (1);
		THIS IS NOT SQL;`})
	_, err := Open(ctx, dir, Options{Migrations: bad})
	if err == nil || !strings.Contains(err.Error(), "migration 002_broken failed") || !strings.Contains(err.Error(), "left at version 1") {
		t.Fatalf("got %v", err)
	}
	// Reopen with only the good migrations: v1 is intact and nothing of v2 leaked.
	s, err := Open(ctx, dir, Options{Migrations: good})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'half_done'`).Scan(&n)
	if n != 0 {
		t.Fatal("a failed migration left its partial changes behind")
	}
	if v, _ := s.SchemaVersion(ctx); v != 1 {
		t.Fatalf("version %d", v)
	}
	// And the broken one is retried (not recorded as applied) when present again.
	s.Close()
	if _, err := Open(ctx, dir, Options{Migrations: bad}); err == nil {
		t.Fatal("broken migration should fail again, not be skipped")
	}
}

func TestNewerDatabaseIsRefused(t *testing.T) {
	dir := t.TempDir()
	future := append(builtinMigrations(), Migration{Version: 2, Name: "future", SQL: `CREATE TABLE future (x INTEGER);`})
	s, err := Open(ctx, dir, Options{Migrations: future})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(ctx, dir, Options{}); !errors.Is(err, ErrTooNew) {
		t.Fatalf("got %v, want ErrTooNew", err)
	}
	if h := Inspect(ctx, dir); !errors.Is(h.Problem, ErrTooNew) {
		t.Fatalf("Inspect problem = %v", h.Problem)
	}
}

func TestRoundTripListShowEvents(t *testing.T) {
	clk := newClock()
	s, _ := openStore(t, Options{Now: clk.now})
	id := ulid(1)
	record(t, s, id, clk.now(), events(id, clk.now(), 100, func(i int) string { return fmt.Sprintf("line %d", i) }))

	list, err := s.ListSessions(ctx, Filter{ProjectRoot: "/proj"})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}
	sm := list[0]
	if sm.ID != id || sm.Status != api.SessionClosed || sm.Events != 100 || sm.Errors != 10 || sm.Mode != "run" || sm.SizeBytes <= 0 || sm.WriteErrors != 0 {
		t.Fatalf("summary: %+v", sm)
	}
	if got, _ := s.ListSessions(ctx, Filter{ProjectRoot: "/other"}); len(got) != 0 {
		t.Fatal("project filter should exclude other projects")
	}
	evs, err := s.Events(ctx, id, EventQuery{Service: "db"})
	if err != nil || len(evs) != 50 || evs[0].Seq != 2 || evs[0].Attrs.Port != 2 || evs[0].SessionID != id {
		t.Fatalf("db events: %v n=%d first=%+v", err, len(evs), evs[0])
	}
	errsOnly, _ := s.Events(ctx, id, EventQuery{MinSeverity: api.SeverityError})
	if len(errsOnly) != 10 {
		t.Fatalf("error events: %d", len(errsOnly))
	}
	newest, _ := s.Events(ctx, id, EventQuery{Limit: 3, Newest: true})
	if len(newest) != 3 || newest[0].Seq != 98 || newest[2].Seq != 100 {
		t.Fatalf("newest: %+v", newest)
	}
	counts, _ := s.CountsByType(ctx, id)
	if counts["log_line"] != 100 {
		t.Fatalf("counts: %v", counts)
	}
	_, final, err := s.GetSession(ctx, id)
	if err != nil || len(final) != 1 || final[0].Name != "api" {
		t.Fatalf("final status: %v %+v", err, final)
	}
}

func TestResolveReferences(t *testing.T) {
	clk := newClock()
	s, _ := openStore(t, Options{Now: clk.now})
	a, b := "01AAAAAAAAAAAAAAAAAAAAAAA1", "01AAAAAAAAAAAAAAAAAAAAAAA2"
	record(t, s, a, clk.now(), nil)
	record(t, s, b, clk.now().Add(time.Hour), nil)
	for ref, want := range map[string]string{"last": b, "": b, "LATEST": b, a: a, "01aaaaaaaaaaaaaaaaaaaaaaa1": a} {
		if got, err := s.Resolve(ctx, ref, "/proj"); err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", ref, got, err, want)
		}
	}
	var amb *AmbiguousError
	if _, err := s.Resolve(ctx, "01AAAA", ""); !errors.As(err, &amb) || len(amb.Matches) != 2 {
		t.Fatalf("ambiguous prefix: %v", err)
	}
	for _, bad := range []string{"nope", "%", "01%"} {
		if _, err := s.Resolve(ctx, bad, ""); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q): %v", bad, err)
		}
	}
	if _, err := s.Resolve(ctx, "last", "/nowhere"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("last in empty project: %v", err)
	}
	// Full IDs and prefixes are scoped to the project too; "" widens to all.
	for _, ref := range []string{a, a[:12]} {
		if _, err := s.Resolve(ctx, ref, "/other"); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) from another project: %v", ref, err)
		}
	}
	if got, err := s.Resolve(ctx, a, ""); err != nil || got != a {
		t.Errorf("Resolve with all projects: %q %v", got, err)
	}
}

func segmentFiles(t *testing.T, dir, id string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(dir, "sessions", id))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestSegmentsRotateCompressAndHoldFullText(t *testing.T) {
	clk := newClock()
	s, dir := openStore(t, Options{Now: clk.now, SegmentMax: 8 << 10})
	id := ulid(2)
	long := strings.Repeat("x", 5000)
	record(t, s, id, clk.now(), events(id, clk.now(), 300, func(i int) string {
		if i == 150 {
			return long
		}
		return fmt.Sprintf("line %d with some padding to take space %s", i, strings.Repeat("p", 60))
	}))

	files := segmentFiles(t, dir, id)
	if len(files) < 4 {
		t.Fatalf("expected several rotated segments, got %v", files)
	}
	for _, f := range files {
		if !strings.HasSuffix(f, ".gz") {
			t.Errorf("segment %s was not compressed on close", f)
		}
		if strings.HasPrefix(f, ".compress-") {
			t.Errorf("temp file left behind: %s", f)
		}
	}
	// Every indexed segment exists, is closed+compressed, and covers a seq range.
	rows, _ := s.db.Query(`SELECT path, first_seq, last_seq, compressed, closed, bytes, disk_bytes FROM segments WHERE session_id = ? ORDER BY first_seq`, id)
	defer rows.Close()
	for rows.Next() {
		var path string
		var first, last uint64
		var comp, closed int
		var bytes, disk int64
		rows.Scan(&path, &first, &last, &comp, &closed, &bytes, &disk)
		if comp != 1 || closed != 1 || disk >= bytes || first > last {
			t.Errorf("segment %s: comp=%d closed=%d bytes=%d disk=%d seq %d-%d", path, comp, closed, bytes, disk, first, last)
		}
		if _, err := os.Stat(filepath.Join(dir, "sessions", filepath.FromSlash(path))); err != nil {
			t.Errorf("indexed segment missing: %v", err)
		}
	}
	// The row in SQLite is truncated; the segment holds the full message.
	evs, _ := s.Events(ctx, id, EventQuery{AfterSeq: 149, Limit: 1})
	if len(evs[0].Message) >= 5000 || !strings.HasSuffix(evs[0].Message, truncMarker) {
		t.Fatalf("sqlite message should be truncated: len=%d", len(evs[0].Message))
	}
	var buf bytes.Buffer
	if err := s.Export(ctx, id, nil, &buf); err != nil {
		t.Fatal(err)
	}
	var bundle api.ExportBundle
	if err := json.Unmarshal(buf.Bytes(), &bundle); err != nil {
		t.Fatalf("export is not valid JSON: %v", err)
	}
	if len(bundle.Events) != 300 || bundle.Events[149].Message != long {
		t.Fatalf("export has %d events; long message restored: %v", len(bundle.Events), bundle.Events[149].Message == long)
	}
	for i, e := range bundle.Events {
		if e.Seq != uint64(i+1) {
			t.Fatalf("export order: position %d has seq %d", i, e.Seq)
		}
	}
	// Size accounting is exact once every segment is sealed: the session's
	// size is its compressed segments plus the per-row estimate, and it is
	// smaller than storing the raw segments would have been.
	sm, _, _ := s.GetSession(ctx, id)
	var disk, raw, rowBytes int64
	s.db.QueryRow(`SELECT SUM(disk_bytes), SUM(bytes) FROM segments WHERE session_id = ?`, id).Scan(&disk, &raw)
	s.db.QueryRow(`SELECT SUM(length(CAST(message AS BLOB)) + length(CAST(attrs AS BLOB)) + ?) FROM events WHERE session_id = ?`, rowOverhead, id).Scan(&rowBytes)
	if sm.SizeBytes != disk+rowBytes {
		t.Errorf("session size %d != compressed segments %d + row estimate %d", sm.SizeBytes, disk, rowBytes)
	}
	if disk >= raw {
		t.Errorf("compressed segments (%d) should be smaller than raw (%d)", disk, raw)
	}
	if n := strings.Count(strings.Join(segmentFiles(t, dir, id), " "), ".gz"); n < 4 {
		t.Errorf("only %d compressed segments", n)
	}
}

func TestExportIsRedactedAgain(t *testing.T) {
	clk := newClock()
	s, _ := openStore(t, Options{Now: clk.now})
	id := ulid(3)
	record(t, s, id, clk.now(), events(id, clk.now(), 3, func(i int) string { return fmt.Sprintf("token=abc%d leaked", i) }))
	var buf bytes.Buffer
	redact := func(x string) string { return strings.ReplaceAll(x, "abc", "[R]") }
	if err := s.Export(ctx, id, redact, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "abc") || !strings.Contains(buf.String(), "[R]1") {
		t.Fatalf("export not redacted:\n%s", buf.String())
	}
}

func TestExportWarnsAboutMissingSegment(t *testing.T) {
	clk := newClock()
	s, dir := openStore(t, Options{Now: clk.now})
	id := ulid(4)
	long := strings.Repeat("y", 3000)
	record(t, s, id, clk.now(), events(id, clk.now(), 5, func(i int) string { return long }))
	for _, f := range segmentFiles(t, dir, id) {
		os.Remove(filepath.Join(dir, "sessions", id, f))
	}
	var buf bytes.Buffer
	if err := s.Export(ctx, id, nil, &buf); err != nil {
		t.Fatal(err)
	}
	var bundle api.ExportBundle
	if err := json.Unmarshal(buf.Bytes(), &bundle); err != nil {
		t.Fatalf("export must stay valid JSON even with missing data: %v", err)
	}
	if len(bundle.Events) != 5 || len(bundle.Warnings) == 0 || !strings.Contains(strings.Join(bundle.Warnings, " "), "missing") {
		t.Fatalf("events %d, warnings %v", len(bundle.Events), bundle.Warnings)
	}
}

func TestDeleteSession(t *testing.T) {
	clk := newClock()
	s, dir := openStore(t, Options{Now: clk.now})
	id, keep := ulid(5), ulid(6)
	record(t, s, id, clk.now(), events(id, clk.now(), 20, func(i int) string { return "x" }))
	record(t, s, keep, clk.now(), events(keep, clk.now(), 20, func(i int) string { return "y" }))
	rem, err := s.DeleteSession(ctx, id)
	if err != nil || rem.ID != id || rem.SizeBytes <= 0 {
		t.Fatalf("delete: %v %+v", err, rem)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("segment directory should be gone")
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE session_id = ?`, id).Scan(&n)
	var m int
	s.db.QueryRow(`SELECT COUNT(*) FROM segments WHERE session_id = ?`, id).Scan(&m)
	if n != 0 || m != 0 {
		t.Fatalf("rows left behind: events=%d segments=%d", n, m)
	}
	if evs, _ := s.Events(ctx, keep, EventQuery{}); len(evs) != 20 {
		t.Fatal("other sessions must be untouched")
	}
	if _, err := s.DeleteSession(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	// Active sessions are never deleted.
	act := ulid(7)
	s.CreateSession(ctx, NewSession{ID: act, Mode: "watch", ProjectRoot: "/proj", PID: os.Getpid(), StartedAt: clk.now()})
	if _, err := s.DeleteSession(ctx, act); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("deleting an active session: %v", err)
	}
}

// TestRetention is the Phase 3 retention criterion.
func TestRetentionByAgeAndSize(t *testing.T) {
	clk := newClock()
	s, dir := openStore(t, Options{Now: clk.now})
	day := 24 * time.Hour
	// Ten closed sessions, 1 per day going back, ~equal size.
	ids := make([]string, 10)
	for i := range ids {
		ids[i] = ulid(100 + i)
		start := clk.now().Add(-time.Duration(10-i) * day) // ids[0] is 10 days old, ids[9] is 1 day old
		record(t, s, ids[i], start, events(ids[i], start, 200, func(n int) string { return strings.Repeat("z", 400) }))
	}
	act := ulid(200)
	s.CreateSession(ctx, NewSession{ID: act, Mode: "watch", ProjectRoot: "/proj", PID: os.Getpid(), StartedAt: clk.now().Add(-30 * day)})
	protected := ulid(201)
	record(t, s, protected, clk.now().Add(-20*day), nil)

	// Dry run changes nothing.
	res, err := s.Enforce(ctx, Policy{Days: 7, MaxMB: 500}, protected, true)
	if err != nil || !res.DryRun || len(res.Removed) != 3 {
		t.Fatalf("dry run: %v %+v", err, res)
	}
	if list, _ := s.ListSessions(ctx, Filter{}); len(list) != 12 {
		t.Fatalf("dry run removed sessions: %d left", len(list))
	}

	// Age: ids[0..2] ended more than 7 days ago (ended = start+1min).
	res, err = s.Enforce(ctx, Policy{Days: 7, MaxMB: 500}, protected, false)
	if err != nil {
		t.Fatal(err)
	}
	removed := map[string]string{}
	for _, r := range res.Removed {
		removed[r.ID] = r.Reason
	}
	if len(removed) != 3 || removed[ids[0]] != "age" || removed[ids[1]] != "age" || removed[ids[2]] != "age" {
		t.Fatalf("age removal: %v", removed)
	}
	if _, ok := removed[act]; ok {
		t.Fatal("active session removed")
	}
	if _, ok := removed[protected]; ok {
		t.Fatal("protected session removed")
	}
	for _, id := range ids[:3] {
		if _, err := os.Stat(filepath.Join(dir, "sessions", id)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("files for %s should be gone", id)
		}
	}

	// The size rule is verified with exact byte accounting in TestRetentionSizeOrder.
	if list, _ := s.ListSessions(ctx, Filter{}); len(list) != 9 { // 7 recent + active + protected
		t.Fatalf("%d sessions left, want 9", len(list))
	}
}

// TestRetentionSizeOrder checks the size rule with exact byte accounting by
// writing session sizes directly.
func TestRetentionSizeOrder(t *testing.T) {
	clk := newClock()
	s, _ := openStore(t, Options{Now: clk.now})
	mk := func(i int, age time.Duration, status string, mb int) string {
		id := ulid(300 + i)
		start := clk.now().Add(-age)
		if err := s.CreateSession(ctx, NewSession{ID: id, Mode: "run", ProjectRoot: "/p", PID: 1, StartedAt: start}); err != nil {
			t.Fatal(err)
		}
		if status != api.SessionActive {
			s.FinishSession(ctx, id, status, start.Add(time.Minute), nil)
		}
		s.db.Exec(`UPDATE sessions SET bytes = ? WHERE id = ?`, int64(mb)<<20, id)
		return id
	}
	oldest := mk(1, 5*24*time.Hour, api.SessionClosed, 100)
	mid := mk(2, 3*24*time.Hour, api.SessionClosed, 100)
	newest := mk(3, 1*24*time.Hour, api.SessionClosed, 100)
	active := mk(4, 6*24*time.Hour, api.SessionActive, 100) // old but active, must stay
	// total 400 MB, limit 250 MB: remove oldest closed first until <= 250.
	res, err := s.Enforce(ctx, Policy{Days: 30, MaxMB: 250}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range res.Removed {
		if r.Reason != "size" {
			t.Errorf("reason %q", r.Reason)
		}
		got = append(got, r.ID)
	}
	if fmt.Sprint(got) != fmt.Sprint([]string{oldest, mid}) {
		t.Fatalf("removed %v, want oldest then mid (%s, %s)", got, oldest, mid)
	}
	if res.TotalBefore != 400<<20 || res.TotalAfter != 200<<20 || res.Kept != 2 {
		t.Fatalf("totals: %+v", res)
	}
	left, _ := s.ListSessions(ctx, Filter{})
	ids := map[string]bool{}
	for _, sm := range left {
		ids[sm.ID] = true
	}
	if !ids[newest] || !ids[active] || len(ids) != 2 {
		t.Fatalf("remaining: %v", ids)
	}

	// Only an active session over the limit: nothing to remove, reported as skipped.
	res, _ = s.Enforce(ctx, Policy{Days: 30, MaxMB: 50}, newest, false)
	if len(res.Removed) != 0 || len(res.Skipped) != 2 {
		t.Fatalf("protected/active over limit: %+v", res)
	}
}

func TestRecoverStaleSessions(t *testing.T) {
	clk := newClock()
	s, dir := openStore(t, Options{Now: clk.now})
	dead, live := ulid(400), ulid(401)
	for _, id := range []string{dead, live} {
		s.CreateSession(ctx, NewSession{ID: id, Mode: "watch", ProjectRoot: "/p", PID: map[string]int{dead: 111, live: 222}[id], StartedAt: clk.now()})
	}
	// The dead session has an open (uncompressed) segment, as after a crash.
	r := s.NewRecorder(dead)
	for _, e := range events(dead, clk.now(), 10, func(i int) string { return "before the crash" }) {
		r.Emit(e)
	}
	r.mu.Lock()
	r.closed = true
	close(r.ch)
	r.mu.Unlock()
	<-r.done // flushed, but we then "lose" the recorder's finalize by resetting segments below
	s.db.Exec(`UPDATE segments SET closed = 0, compressed = 0 WHERE session_id = ?`, dead)
	// Recreate the plain file the crash would have left.
	var rel string
	s.db.QueryRow(`SELECT path FROM segments WHERE session_id = ? LIMIT 1`, dead).Scan(&rel)
	plain := strings.TrimSuffix(rel, ".gz")
	if rel != plain {
		gzf, _ := os.Open(filepath.Join(dir, "sessions", rel))
		zr, _ := gzip.NewReader(gzf)
		b := new(bytes.Buffer)
		b.ReadFrom(zr)
		gzf.Close()
		os.WriteFile(filepath.Join(dir, "sessions", plain), b.Bytes(), 0o600)
		os.Remove(filepath.Join(dir, "sessions", rel))
		s.db.Exec(`UPDATE segments SET path = ? WHERE session_id = ?`, plain, dead)
	}

	n, err := s.RecoverStale(ctx, func(pid int) bool { return pid == 222 })
	if err != nil || n != 1 {
		t.Fatalf("recovered %d, %v", n, err)
	}
	smDead, _, _ := s.GetSession(ctx, dead)
	smLive, _, _ := s.GetSession(ctx, live)
	if smDead.Status != api.SessionInterrupted || smDead.EndedAt.IsZero() || smLive.Status != api.SessionActive {
		t.Fatalf("dead=%+v live=%+v", smDead, smLive)
	}
	var comp, closed int
	s.db.QueryRow(`SELECT compressed, closed FROM segments WHERE session_id = ?`, dead).Scan(&comp, &closed)
	if comp != 1 || closed != 1 {
		t.Fatalf("stale segment not sealed: compressed=%d closed=%d", comp, closed)
	}
	var buf bytes.Buffer
	if err := s.Export(ctx, dead, nil, &buf); err != nil || !strings.Contains(buf.String(), "before the crash") {
		t.Fatalf("recovered session unreadable: %v", err)
	}
	// Idempotent.
	if n, _ := s.RecoverStale(ctx, func(int) bool { return false }); n != 1 { // only `live` is left
		t.Fatalf("second pass recovered %d", n)
	}
}

func TestPartialLastLineIsIgnored(t *testing.T) {
	s, dir := openStore(t, Options{})
	rel := "x/seg.log"
	os.MkdirAll(filepath.Join(dir, "sessions", "x"), 0o700)
	good, _ := json.Marshal(api.Event{Seq: 1, Message: "complete"})
	os.WriteFile(filepath.Join(dir, "sessions", rel), append(append(good, '\n'), []byte(`{"seq":2,"message":"cut sh`)...), 0o600)
	var got []uint64
	if err := s.scanSegment(rel, func(e api.Event) { got = append(got, e.Seq) }); err != nil || fmt.Sprint(got) != "[1]" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := s.segmentPath("../../etc/passwd"); err == nil {
		t.Fatal("path traversal must be refused")
	}
	if _, err := s.segmentPath("/etc/passwd"); err == nil {
		t.Fatal("absolute path must be refused")
	}
}

func TestCorruptDatabaseIsReportedNotFatal(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(DBPath(dir), bytes.Repeat([]byte("this is not a sqlite database "), 200), 0o600)
	if _, err := Open(ctx, dir, Options{}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open: %v", err)
	}
	h := Inspect(ctx, dir)
	if !h.Exists || !errors.Is(h.Problem, ErrCorrupt) {
		t.Fatalf("Inspect: %+v", h)
	}
	// Kestrel never touched the bad file.
	b, _ := os.ReadFile(DBPath(dir))
	if !bytes.HasPrefix(b, []byte("this is not a sqlite")) {
		t.Fatal("corrupt database was modified")
	}
}

func TestInspectHealthyAndMissing(t *testing.T) {
	dir := t.TempDir()
	if h := Inspect(ctx, filepath.Join(dir, "nope")); h.Exists || h.Problem != nil {
		t.Fatalf("missing: %+v", h)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("Inspect must not create anything")
	}
	clk := newClock()
	s, d2 := openStore(t, Options{Now: clk.now})
	record(t, s, ulid(9), clk.now(), events(ulid(9), clk.now(), 5, func(int) string { return "m" }))
	s.CreateSession(ctx, NewSession{ID: ulid(10), Mode: "run", ProjectRoot: "/p", PID: 1, StartedAt: clk.now()})
	h := Inspect(ctx, d2)
	if !h.Exists || h.Problem != nil || h.SchemaVersion != 1 || h.Sessions != 2 || h.Active != 1 {
		t.Fatalf("healthy: %+v", h)
	}
}

// TestWriteFailureIsCountedNotFatal makes the segment directory unwritable:
// events still reach SQLite, the failure is counted on the session, and the
// recorder reports it instead of crashing.
func TestWriteFailureIsCountedNotFatal(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced here")
	}
	clk := newClock()
	s, dir := openStore(t, Options{Now: clk.now})
	id := ulid(11)
	s.CreateSession(ctx, NewSession{ID: id, Mode: "run", ProjectRoot: "/p", PID: 1, StartedAt: clk.now()})
	if err := os.Chmod(filepath.Join(dir, "sessions"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(dir, "sessions"), 0o700) })

	r := s.NewRecorder(id)
	for _, e := range events(id, clk.now(), 20, func(i int) string { return "m" }) {
		r.Emit(e)
	}
	err := r.Close()
	if err == nil {
		t.Fatal("expected the storage failure to be reported")
	}
	s.FinishSession(ctx, id, api.SessionClosed, clk.now(), nil)
	sm, _, _ := s.GetSession(ctx, id)
	if sm.Events != 20 || sm.WriteErrors == 0 {
		t.Fatalf("events must still be indexed and failures counted: %+v", sm)
	}
	if r.WriteErrors() == 0 {
		t.Fatal("recorder should count write errors")
	}
}

func TestOpenFailsCleanlyWhenDataDirIsUnwritable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced here")
	}
	parent := t.TempDir()
	os.Chmod(parent, 0o500)
	t.Cleanup(func() { os.Chmod(parent, 0o700) })
	_, err := Open(ctx, filepath.Join(parent, "kestrel"), Options{})
	if err == nil || !strings.Contains(err.Error(), "data directory") {
		t.Fatalf("got %v", err)
	}
}

func TestEmitAfterCloseIsCountedNotPanic(t *testing.T) {
	clk := newClock()
	s, _ := openStore(t, Options{Now: clk.now})
	id := ulid(12)
	s.CreateSession(ctx, NewSession{ID: id, Mode: "run", ProjectRoot: "/p", PID: 1, StartedAt: clk.now()})
	r := s.NewRecorder(id)
	r.Close()
	r.Emit(api.Event{Seq: 1, ServiceID: "a", Type: api.EventLogLine, Severity: api.SeverityInfo, Message: "late"})
	if r.WriteErrors() != 1 {
		t.Fatalf("write errors = %d", r.WriteErrors())
	}
	r.Close() // idempotent
}

func TestTruncateMessageKeepsValidUTF8(t *testing.T) {
	m := strings.Repeat("é", 700) // 1400 bytes, 2 per rune
	got := truncateMessage(m)
	if !strings.HasSuffix(got, truncMarker) {
		t.Fatal("marker missing")
	}
	body := got[:strings.Index(got, " …[+")]
	if len(body) > maxRowMessage || strings.ToValidUTF8(body, "?") != body {
		t.Fatalf("invalid or oversized truncation: %d bytes", len(body))
	}
	if truncateMessage("short") != "short" {
		t.Fatal("short messages are unchanged")
	}
}
