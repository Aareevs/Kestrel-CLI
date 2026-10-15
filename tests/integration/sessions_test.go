package integration

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// kestrelCmd runs the real binary to completion and returns its exit code,
// stdout and stderr.
func kestrelCmd(t *testing.T, home, dir string, args ...string) (int, string, string) {
	t.Helper()
	k, _ := buildBins(t)
	cmd := exec.Command(k, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "KESTREL_HOME="+home, "NO_COLOR=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, s)
	}
	return v
}

// everyStoredByte returns the contents of every file under home, with .gz
// files decompressed, so secrets can't hide in compressed segments.
func everyStoredByte(t *testing.T, home string) string {
	t.Helper()
	var all bytes.Buffer
	filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		if strings.HasSuffix(p, ".gz") {
			if zr, err := gzip.NewReader(bytes.NewReader(b)); err == nil {
				b, _ = io.ReadAll(zr)
			}
		}
		all.Write(b)
		all.WriteByte('\n')
		return nil
	})
	return all.String()
}

var fakeSecrets = []string{"sup3rs3cret", "sk_live_fakefakefakefakefake1234", "AKIAFAKEFAKEFAKEFAKE", "c2lnbmF0dXJlZmFrZQ"}

// runSession records a short crashing session and returns its ID.
func runSession(t *testing.T, home, dir string, extra ...string) string {
	t.Helper()
	_, fake := buildBins(t)
	args := append([]string{"run", "--quiet", "--name", "api", "--", fake, "--name", "api", "--secrets", "--hostile",
		"--connect", "127.0.0.1:1", "--connect-every", "100ms", "--exit-after", "700ms", "--exit-code", "2", "--exit-message", "fatal: giving up"}, extra...)
	if code, out, errs := kestrelCmd(t, home, dir, args...); code != 1 {
		t.Fatalf("run exit %d\n%s%s", code, out, errs)
	}
	code, out, _ := kestrelCmd(t, home, dir, "sessions", "list", "--format", "json", "-n", "1")
	if code != 0 {
		t.Fatalf("list exit %d", code)
	}
	l := decode[api.SessionList](t, out)
	if len(l.Sessions) != 1 {
		t.Fatalf("sessions: %+v", l)
	}
	return l.Sessions[0].ID
}

// TestSessionPersistListShowExportDelete is the Phase 3 exit criterion: a
// session is persisted, listed, shown, exported (redacted), and deleted, and
// secrets never reach any stored byte (scenario F, storage half).
func TestSessionPersistListShowExportDelete(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	id := runSession(t, home, dir)

	// Persisted: the data directory holds a database and a gzipped segment.
	var gz []string
	filepath.WalkDir(filepath.Join(home, "sessions"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".log.gz") {
			gz = append(gz, p)
		}
		return nil
	})
	if _, err := os.Stat(filepath.Join(home, "kestrel.db")); err != nil || len(gz) != 1 {
		t.Fatalf("expected kestrel.db and one gzipped segment, got gz=%v err=%v", gz, err)
	}
	if stored := everyStoredByte(t, home); !strings.Contains(stored, "[REDACTED:") {
		t.Fatal("stored data should contain redaction placeholders")
	} else {
		for _, secret := range fakeSecrets {
			if strings.Contains(stored, secret) {
				t.Errorf("secret %q found in stored data (database, WAL, or segment)", secret)
			}
		}
		if !strings.Contains(stored, "rm -rf ~") {
			t.Error("hostile text should be stored verbatim as inert data")
		}
	}

	// Listed (text and JSON), scoped to this project.
	_, out, _ := kestrelCmd(t, home, dir, "sessions", "list")
	if !strings.Contains(out, id) || !strings.Contains(out, "closed") {
		t.Fatalf("list:\n%s", out)
	}
	other := t.TempDir()
	if _, out, _ := kestrelCmd(t, home, other, "sessions", "list"); strings.Contains(out, id) || !strings.Contains(out, "No sessions recorded for this project") {
		t.Fatalf("list from another project:\n%s", out)
	}
	if _, out, _ := kestrelCmd(t, home, other, "sessions", "list", "--all"); !strings.Contains(out, id) {
		t.Fatalf("list --all from another project:\n%s", out)
	}

	// Shown.
	code, out, _ := kestrelCmd(t, home, dir, "sessions", "show", id[:12])
	if code != 0 || !strings.Contains(out, "crashed") || !strings.Contains(out, "dependency_conn_failed") || !strings.Contains(out, "process_crashed") {
		t.Fatalf("show exit %d:\n%s", code, out)
	}
	_, out, _ = kestrelCmd(t, home, dir, "sessions", "show", "--format", "json", "--session", id)
	detail := decode[api.SessionDetail](t, out)
	if detail.Session.ID != id || detail.Session.Status != api.SessionClosed || detail.Session.Errors == 0 ||
		detail.CountsByType["process_crashed"] != 1 || len(detail.Services) != 1 || detail.Services[0].Liveness != api.LivenessCrashed {
		t.Fatalf("detail: %+v", detail)
	}

	// Exported, redacted, complete and in order.
	bundlePath := filepath.Join(t.TempDir(), "bundle.json")
	if code, _, errs := kestrelCmd(t, home, dir, "sessions", "export", "--out", bundlePath); code != 0 {
		t.Fatalf("export exit %d: %s", code, errs)
	}
	raw, _ := os.ReadFile(bundlePath)
	for _, secret := range fakeSecrets {
		if strings.Contains(string(raw), secret) {
			t.Errorf("secret %q in export", secret)
		}
	}
	bundle := decode[api.ExportBundle](t, string(raw))
	if bundle.Session.ID != id || len(bundle.Events) != detail.Session.Events || len(bundle.Warnings) != 0 {
		t.Fatalf("bundle: events=%d (session says %d) warnings=%v", len(bundle.Events), detail.Session.Events, bundle.Warnings)
	}
	for i, e := range bundle.Events {
		if e.Seq != uint64(i+1) || e.SessionID != id {
			t.Fatalf("event %d: seq=%d session=%s", i, e.Seq, e.SessionID)
		}
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(bundlePath); fi.Mode().Perm() != 0o600 {
			t.Errorf("export file mode %v, want 0600", fi.Mode().Perm())
		}
	}
	// Export refuses to overwrite and to the same stdout path works.
	if code, _, errs := kestrelCmd(t, home, dir, "sessions", "export", "--out", bundlePath); code != 2 || !strings.Contains(errs, "never overwrites") {
		t.Fatalf("overwrite: exit %d %s", code, errs)
	}
	if _, out, _ := kestrelCmd(t, home, dir, "sessions", "export", "last"); decode[api.ExportBundle](t, out).Session.ID != id {
		t.Fatal("stdout export of 'last' should match")
	}

	// Deleted: needs --yes without a TTY; then nothing remains.
	if code, _, errs := kestrelCmd(t, home, dir, "sessions", "delete", id); code != 2 && code != 0 {
		t.Fatalf("delete without --yes: exit %d %s", code, errs)
	}
	if code, out, errs := kestrelCmd(t, home, dir, "sessions", "delete", id, "--yes"); code != 0 || !strings.Contains(out, "deleted "+id) {
		t.Fatalf("delete: exit %d\n%s%s", code, out, errs)
	}
	if _, out, _ := kestrelCmd(t, home, dir, "sessions", "list", "--all", "--format", "json"); len(decode[api.SessionList](t, out).Sessions) != 0 {
		t.Fatalf("session still listed after delete:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, "sessions", id)); !os.IsNotExist(err) {
		t.Fatal("segment directory still exists after delete")
	}
	if code, _, errs := kestrelCmd(t, home, dir, "sessions", "show", id); code != 2 || !strings.Contains(errs, "no such session") {
		t.Fatalf("show deleted: exit %d %s", code, errs)
	}
}

// backdate makes a closed session look like it ended `ago` ago, by editing
// the database the way time passing would.
func backdate(t *testing.T, home, id string, ago time.Duration) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(home, "kestrel.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ended := time.Now().Add(-ago).UnixNano()
	if _, err := db.Exec(`UPDATE sessions SET ended_ns = ?, started_ns = ? WHERE id = ?`, ended, ended-int64(time.Second), id); err != nil {
		t.Fatal(err)
	}
}

// TestRetentionOnCleanupAndSessionClose is the retention half of the exit
// criterion: `cleanup` removes expired sessions (dry run first), the same
// policy runs when a session ends, and a kestrel.yaml can change the window.
func TestRetentionOnCleanupAndSessionClose(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	day := 24 * time.Hour
	oldA := runSession(t, home, dir)
	oldB := runSession(t, home, dir)
	young := runSession(t, home, dir)
	backdate(t, home, oldA, 10*day)
	backdate(t, home, oldB, 8*day)
	backdate(t, home, young, 2*day)

	// Dry run: reports, changes nothing.
	code, out, _ := kestrelCmd(t, home, dir, "cleanup", "--dry-run", "--format", "json")
	res := decode[api.RetentionResult](t, out)
	if code != 0 || !res.DryRun || len(res.Removed) != 2 || res.RetentionDays != 7 || res.MaxTotalMB != 500 {
		t.Fatalf("dry run: exit %d %+v", code, res)
	}
	if _, out, _ := kestrelCmd(t, home, dir, "sessions", "list", "--format", "json"); len(decode[api.SessionList](t, out).Sessions) != 3 {
		t.Fatal("dry run removed sessions")
	}
	// A kestrel.yaml narrows the window to 1 day: now `young` is expired too.
	os.WriteFile(filepath.Join(dir, "kestrel.yaml"), []byte("version: 1\nstorage: { retention_days: 1 }\n"), 0o644)
	_, out, _ = kestrelCmd(t, home, dir, "cleanup", "--dry-run", "--format", "json")
	if res := decode[api.RetentionResult](t, out); len(res.Removed) != 3 || res.RetentionDays != 1 {
		t.Fatalf("config retention_days ignored: %+v", res)
	}
	os.Remove(filepath.Join(dir, "kestrel.yaml"))

	// Real cleanup.
	code, out, _ = kestrelCmd(t, home, dir, "cleanup", "--format", "json")
	res = decode[api.RetentionResult](t, out)
	removed := map[string]string{}
	for _, r := range res.Removed {
		removed[r.ID] = r.Reason
	}
	if code != 0 || len(removed) != 2 || removed[oldA] != "age" || removed[oldB] != "age" || res.Kept != 1 || res.TotalAfter >= res.TotalBefore {
		t.Fatalf("cleanup: exit %d %+v", code, res)
	}
	for _, id := range []string{oldA, oldB} {
		if _, err := os.Stat(filepath.Join(home, "sessions", id)); !os.IsNotExist(err) {
			t.Errorf("files for %s survived cleanup", id)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "sessions", young)); err != nil {
		t.Errorf("a session inside the window lost its files: %v", err)
	}

	// Retention also runs when a session ends: expire `young`, then record a new one.
	backdate(t, home, young, 9*day)
	_, fake := buildBins(t)
	code, out, errs := kestrelCmd(t, home, dir, "run", "--name", "x", "--", fake, "--exit-after", "200ms")
	if code != 0 || !strings.Contains(out, "retention removed 1 older session") {
		t.Fatalf("run: exit %d\n%s%s", code, out, errs)
	}
	_, out, _ = kestrelCmd(t, home, dir, "sessions", "list", "--format", "json")
	left := decode[api.SessionList](t, out).Sessions
	if len(left) != 1 || left[0].ID == young {
		t.Fatalf("after automatic retention: %+v", left)
	}
}

// TestWatchSessionIsActiveThenClosed checks that a running watch session is
// visible as active from another process and is closed on Ctrl-C.
func TestWatchSessionIsActiveThenClosed(t *testing.T) {
	_, fake := buildBins(t)
	project, home := t.TempDir(), t.TempDir()
	port := freePort(t)
	cfg := fmt.Sprintf("version: 1\nservices:\n  - name: web\n    command: '%s --name web --listen 127.0.0.1:%d --say-every 100ms --say heartbeat'\n    health: { type: http, url: \"http://127.0.0.1:%d/health\", interval: 100ms, timeout: 100ms }\n", fake, port, port)
	os.WriteFile(filepath.Join(project, "kestrel.yaml"), []byte(cfg), 0o644)

	k := startKestrel(t, home, "watch", "--project", project, "--format", "json")
	evs := readEvents(k.stdout)
	evs.waitFor(t, "web ready", 20*time.Second, func(e api.Event) bool { return e.ServiceID == "web" && e.Type == api.EventProbeReady })

	var sm api.SessionSummary
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		_, out, _ := kestrelCmd(t, home, project, "sessions", "list", "--format", "json")
		if l := decode[api.SessionList](t, out); len(l.Sessions) == 1 && l.Sessions[0].Events > 0 {
			sm = l.Sessions[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("running session never became visible with events")
		}
	}
	if sm.Status != api.SessionActive || sm.Mode != "watch" {
		t.Fatalf("running session: %+v", sm)
	}
	if code, _, errs := kestrelCmd(t, home, project, "sessions", "delete", sm.ID, "--yes"); code != 2 || !strings.Contains(errs, "active") {
		t.Fatalf("deleting an active session: exit %d %s", code, errs)
	}
	// Cleanup must not touch it either, however old it looks.
	backdate(t, home, sm.ID, 30*24*time.Hour)
	if _, out, _ := kestrelCmd(t, home, project, "cleanup", "--format", "json"); len(decode[api.RetentionResult](t, out).Removed) != 0 {
		t.Fatal("cleanup removed an active session")
	}

	k.ctrlC()
	if code := k.wait(t, 20*time.Second); code != 0 {
		t.Fatalf("watch exit %d\n%s", code, k.stderr)
	}
	_, out, _ := kestrelCmd(t, home, project, "sessions", "show", "last", "--format", "json")
	d := decode[api.SessionDetail](t, out)
	if d.Session.Status != api.SessionClosed || d.Session.EndedAt.IsZero() || d.Session.Events < 5 || d.CountsByType["process_exited"] != 1 {
		t.Fatalf("closed session: %+v counts=%v", d.Session, d.CountsByType)
	}
}

// TestKilledKestrelLeavesRecoverableSession: kill -9 mid-session leaves an
// active row and an uncompressed segment; the next command marks it
// interrupted, seals the segment, and the data is still exportable.
func TestKilledKestrelLeavesRecoverableSession(t *testing.T) {
	_, fake := buildBins(t)
	home, dir := t.TempDir(), t.TempDir()
	k := startKestrel(t, home, "run", "--format", "json", "--", fake, "--name", "svc", "--say-every", "100ms", "--say", "still alive", "--exit-after", "4s")
	evs := readEvents(k.stdout)
	evs.waitFor(t, "output", 20*time.Second, func(e api.Event) bool { return strings.Contains(e.Message, "still alive") })
	time.Sleep(400 * time.Millisecond) // let the recorder flush a batch
	// Kill only Kestrel itself (not its process group), like an OOM kill.
	if err := k.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	k.cmd.Wait()

	_, out, _ := kestrelCmd(t, home, dir, "sessions", "list", "--all", "--format", "json")
	l := decode[api.SessionList](t, out)
	if len(l.Sessions) != 1 || l.Sessions[0].Status != api.SessionInterrupted || l.Sessions[0].EndedAt.IsZero() || l.Sessions[0].Events == 0 {
		t.Fatalf("after kill -9: %+v", l.Sessions)
	}
	_, out, _ = kestrelCmd(t, home, dir, "sessions", "export", l.Sessions[0].ID, "--all")
	b := decode[api.ExportBundle](t, out)
	if !strings.Contains(out, "still alive") || len(b.Events) != l.Sessions[0].Events {
		t.Fatalf("interrupted session not fully readable: %d events", len(b.Events))
	}
	_, out, _ = kestrelCmd(t, home, dir, "sessions", "show", "last", "--all")
	if !strings.Contains(out, "interrupted") || !strings.Contains(out, "stopped without closing") {
		t.Fatalf("show should explain the interruption:\n%s", out)
	}
	var plain int
	filepath.WalkDir(filepath.Join(home, "sessions"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".log") {
			plain++
		}
		return nil
	})
	if plain != 0 {
		t.Errorf("%d uncompressed segment(s) left after recovery", plain)
	}
}

// TestStorageFailureNeverStopsSupervision: with a corrupt database, run still
// runs and exits with the process's result, the corrupt file is untouched,
// and doctor says exactly what's wrong.
func TestStorageFailureNeverStopsSupervision(t *testing.T) {
	_, fake := buildBins(t)
	home, dir := t.TempDir(), t.TempDir()
	junk := bytes.Repeat([]byte("definitely not sqlite "), 300)
	dbPath := filepath.Join(home, "kestrel.db")
	os.WriteFile(dbPath, junk, 0o600)

	code, out, errs := kestrelCmd(t, home, dir, "run", "--name", "svc", "--", fake, "--exit-after", "200ms", "--exit-code", "3")
	if code != 1 || !strings.Contains(out, "crashed (exit code 3)") {
		t.Fatalf("supervision should be unaffected: exit %d\n%s", code, out)
	}
	if !strings.Contains(errs, "session storage unavailable") || !strings.Contains(errs, "corrupt") {
		t.Fatalf("expected a storage warning, got stderr:\n%s", errs)
	}
	if strings.Contains(out, "saved as session") {
		t.Fatal("must not claim the session was saved")
	}
	if b, _ := os.ReadFile(dbPath); !bytes.Equal(b, junk) {
		t.Fatal("corrupt database was modified")
	}
	code, out, _ = kestrelCmd(t, home, dir, "doctor")
	if code != 4 || !strings.Contains(out, "✗ storage") || !strings.Contains(out, "corrupt") || !strings.Contains(out, "untouched") {
		t.Fatalf("doctor: exit %d\n%s", code, out)
	}
	if code, _, errs := kestrelCmd(t, home, dir, "sessions", "list"); code == 0 || !strings.Contains(errs, "corrupt") {
		t.Fatalf("sessions list on a corrupt database: exit %d %s", code, errs)
	}
}

func TestStorageUnwritableNeverStopsSupervision(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced here")
	}
	_, fake := buildBins(t)
	home := filepath.Join(t.TempDir(), "kestrel-home")
	os.MkdirAll(home, 0o700)
	os.Chmod(home, 0o500)
	t.Cleanup(func() { os.Chmod(home, 0o700) })

	code, out, errs := kestrelCmd(t, home, t.TempDir(), "run", "--name", "svc", "--", fake, "--exit-after", "200ms")
	if code != 0 || !strings.Contains(out, "exited (exit code 0)") {
		t.Fatalf("exit %d\n%s%s", code, out, errs)
	}
	if !strings.Contains(errs, "session storage unavailable") {
		t.Fatalf("stderr:\n%s", errs)
	}
	if code, out, _ := kestrelCmd(t, home, t.TempDir(), "doctor"); code != 4 || !strings.Contains(out, "not writable") {
		t.Fatalf("doctor: exit %d\n%s", code, out)
	}
}

// TestFloodIsPersistedAndRetentionHolds records a flood and checks that the
// stored session is bounded and consistent with what was reported.
func TestFloodIsPersistedWithDropAccounting(t *testing.T) {
	if testing.Short() {
		t.Skip("flood test skipped in -short mode")
	}
	_, fake := buildBins(t)
	home, dir := t.TempDir(), t.TempDir()
	k := startKestrel(t, home, "run", "--quiet", "--", fake, "--name", "flood", "--flood-bytes-per-min", fmt.Sprint(50<<20), "--secrets")
	go io.Copy(io.Discard, k.stdout)
	time.Sleep(6 * time.Second)
	k.ctrlC()
	if code := k.wait(t, 30*time.Second); code != 0 {
		t.Fatalf("exit %d\n%s", code, k.stderr)
	}
	_, out, errs := kestrelCmd(t, home, dir, "sessions", "show", "last", "--all", "--format", "json")
	if out == "" {
		t.Fatalf("show failed: %s", errs)
	}
	d := decode[api.SessionDetail](t, out)
	s := d.Session
	t.Logf("flood session: %d events stored, %d dropped, %s on disk, write errors %d", s.Events, s.Dropped, humanKiB(s.SizeBytes), s.WriteErrors)
	if s.Events < 1000 || s.WriteErrors != 0 || s.Status != api.SessionClosed {
		t.Fatalf("session: %+v", s)
	}
	if s.SizeBytes <= 0 || s.SizeBytes > 200<<20 {
		t.Fatalf("session size %d out of bounds", s.SizeBytes)
	}
	// max_total_mb is only meaningful if the size Kestrel reports for a session
	// matches what it really occupies (database + WAL + segments).
	var actual int64
	filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.Contains(p, string(filepath.Separator)+"run"+string(filepath.Separator)) {
			if fi, err := d.Info(); err == nil {
				actual += fi.Size()
			}
		}
		return nil
	})
	if ratio := float64(s.SizeBytes) / float64(actual); ratio < 0.85 || ratio > 1.15 {
		t.Errorf("reported session size %s vs %s actually on disk (ratio %.2f); retention limits would be unreliable",
			humanKiB(s.SizeBytes), humanKiB(actual), ratio)
	} else {
		t.Logf("size accounting: reported %s, actual %s (ratio %.2f)", humanKiB(s.SizeBytes), humanKiB(actual), ratio)
	}
	// Secrets printed once at startup are still redacted at flood volume.
	for _, secret := range fakeSecrets {
		if strings.Contains(everyStoredByte(t, home), secret) {
			t.Errorf("secret %q stored", secret)
		}
	}
	// A segment index exists and every file it names is present and gzipped.
	var segs int
	filepath.WalkDir(filepath.Join(home, "sessions"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			segs++
			if !strings.HasSuffix(p, ".gz") {
				t.Errorf("uncompressed segment after clean shutdown: %s", p)
			}
		}
		return nil
	})
	if segs == 0 {
		t.Fatal("no segments written")
	}
}

func humanKiB(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }

// In-process coverage of the sessions/cleanup commands and their renderers:
// the same behavior as the subprocess tests, but visible to coverage tooling
// and able to drive the interactive confirmation.
func TestSessionsCommandsInProcess(t *testing.T) {
	_, fake := buildBins(t)
	home := t.TempDir()
	t.Setenv("KESTREL_HOME", home)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Empty state is explained, not an error.
	if r := run(t, dir, nil, "sessions", "list", "--no-color"); r.code != 0 || !strings.Contains(r.stdout, "No sessions recorded for this project") {
		t.Fatalf("empty list: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, dir, nil, "sessions", "show"); r.code != 2 || !strings.Contains(r.stderr, "none recorded yet") {
		t.Fatalf("show with nothing: exit %d %s", r.code, r.stderr)
	}

	for _, args := range [][]string{
		{"run", "--quiet", "--name", "web", "--", fake, "--name", "web", "--exit-after", "200ms"},
		{"run", "--quiet", "--name", "api", "--", fake, "--name", "api", "--secrets", "--connect", "127.0.0.1:1", "--connect-every", "50ms", "--exit-after", "500ms", "--exit-code", "2"},
	} {
		run(t, dir, nil, args...)
		time.Sleep(10 * time.Millisecond) // distinct start times
	}

	r := run(t, dir, nil, "sessions", "list", "--no-color")
	if r.code != 0 || strings.Count(r.stdout, "closed") != 2 || !strings.Contains(r.stdout, "web") || !strings.Contains(r.stdout, "api") || !strings.Contains(r.stdout, "KiB") {
		t.Fatalf("list:\n%s", r.stdout)
	}
	list := decode[api.SessionList](t, run(t, dir, nil, "sessions", "list", "--format", "json", "-n", "1").stdout)
	if len(list.Sessions) != 1 || list.Sessions[0].Services[0] != "api" {
		t.Fatalf("-n 1 should return only the newest (api) session: %+v", list.Sessions)
	}
	apiID := list.Sessions[0].ID

	r = run(t, dir, nil, "sessions", "show", "--no-color")
	for _, want := range []string{"Session  " + apiID, "crashed", "Events by type", "Notable events", "dependency_conn_failed", "kestrel sessions export"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("show lacks %q:\n%s", want, r.stdout)
		}
	}
	if r := run(t, dir, nil, "sessions", "show", "--quiet", "--session", apiID[:10]); r.code != 0 || strings.Contains(r.stdout, "kestrel sessions export") {
		t.Fatalf("show --quiet/--session: exit %d\n%s", r.code, r.stdout)
	}
	// Prefixes shared by both sessions are ambiguous.
	if r := run(t, dir, nil, "sessions", "show", apiID[:6]); r.code != 2 || !strings.Contains(r.stderr, "ambiguous") {
		t.Fatalf("ambiguous prefix: exit %d %s", r.code, r.stderr)
	}
	// Another project can't see them unless --all.
	other, _ := filepath.EvalSymlinks(t.TempDir())
	if r := run(t, other, nil, "sessions", "show", apiID); r.code != 2 || !strings.Contains(r.stderr, "--all") {
		t.Fatalf("cross-project show: exit %d %s", r.code, r.stderr)
	}
	if r := run(t, other, nil, "sessions", "show", apiID, "--all"); r.code != 0 {
		t.Fatalf("show --all: exit %d %s", r.code, r.stderr)
	}

	exp := run(t, dir, nil, "sessions", "export", apiID)
	bundle := decode[api.ExportBundle](t, exp.stdout)
	if exp.code != 0 || bundle.Session.ID != apiID || strings.Contains(exp.stdout, "sup3rs3cret") || !strings.Contains(exp.stdout, "[REDACTED:") {
		t.Fatalf("export: exit %d, redacted=%v", exp.code, !strings.Contains(exp.stdout, "sup3rs3cret"))
	}

	// Delete: refuses without confirmation when not a terminal...
	if r := run(t, dir, nil, "sessions", "delete", apiID); r.code != 2 || !strings.Contains(r.stderr, "--yes") {
		t.Fatalf("delete without tty: exit %d %s", r.code, r.stderr)
	}
	// ...declining keeps it, confirming removes it.
	no, yes := "n\n", "yes\n"
	// The prompt names the real session even when asked about "last".
	if r := run(t, dir, &no, "sessions", "delete", "last"); r.code != 0 || !strings.Contains(r.stdout, apiID) || !strings.Contains(r.stdout, "Nothing deleted") {
		t.Fatalf("declined: exit %d\n%s", r.code, r.stdout)
	}
	if r := run(t, dir, nil, "sessions", "show", apiID); r.code != 0 {
		t.Fatal("declined delete must keep the session")
	}
	if r := run(t, dir, &yes, "sessions", "delete", apiID); r.code != 0 || !strings.Contains(r.stdout, "deleted "+apiID) {
		t.Fatalf("confirmed delete: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if r := run(t, dir, nil, "sessions", "delete", "nope", "--yes"); r.code != 2 {
		t.Fatalf("delete unknown: exit %d", r.code)
	}

	// Cleanup text and JSON, including the removal list.
	webID := decode[api.SessionList](t, run(t, dir, nil, "sessions", "list", "--format", "json").stdout).Sessions[0].ID
	if r := run(t, dir, nil, "cleanup", "--no-color"); r.code != 0 || !strings.Contains(r.stdout, "Nothing to remove: 1 session(s) kept") {
		t.Fatalf("cleanup nothing: exit %d\n%s", r.code, r.stdout)
	}
	backdate(t, home, webID, 30*24*time.Hour)
	r = run(t, dir, nil, "cleanup", "--dry-run", "--no-color")
	for _, want := range []string{"Would remove 1 session(s)", webID, "older than the retention window", "Dry run: nothing was deleted"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("dry-run lacks %q:\n%s", want, r.stdout)
		}
	}
	r = run(t, dir, nil, "cleanup", "--no-color")
	if r.code != 0 || !strings.Contains(r.stdout, "Removed 1 session(s)") || !strings.Contains(r.stdout, "freed") {
		t.Fatalf("cleanup: exit %d\n%s", r.code, r.stdout)
	}
	if r := run(t, dir, nil, "sessions", "list", "--all", "--no-color"); !strings.Contains(r.stdout, "No sessions recorded yet") {
		t.Fatalf("after cleanup:\n%s", r.stdout)
	}

	// Doctor reports storage.
	if r := run(t, dir, nil, "doctor", "--no-color"); r.code != 0 || !strings.Contains(r.stdout, "storage") || !strings.Contains(r.stdout, "0 session(s), schema v") {
		t.Fatalf("doctor: exit %d\n%s", r.code, r.stdout)
	}
}
