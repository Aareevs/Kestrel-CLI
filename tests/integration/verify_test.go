//go:build !windows

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/cli"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// verifyJSON runs `kestrel verify --format json` and returns its exit code,
// report, and how long it took.
func verifyJSON(t *testing.T, home, project string, args ...string) (int, api.VerificationReport, time.Duration) {
	t.Helper()
	start := time.Now()
	code, out, errs := kestrelCmd(t, home, project, append([]string{"verify", "--format", "json"}, args...)...)
	took := time.Since(start)
	if code != 0 && code != 1 && code != 3 {
		t.Fatalf("verify exit %d\n%s\n%s", code, out, errs)
	}
	return code, decode[api.VerificationReport](t, out), took
}

// waitStored waits until the latest session has stored an event of type typ.
func waitStored(t *testing.T, home, project string, typ api.EventType) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		_, out, _ := kestrelCmd(t, home, project, "sessions", "show", "--format", "json")
		var d api.SessionDetail
		if json.Unmarshal([]byte(out), &d) == nil && d.CountsByType[string(typ)] > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s event was stored:\n%s", typ, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func checkOf(rep api.VerificationReport, kind, service string) *api.CheckResult {
	for i, c := range rep.Checks {
		if c.Kind == kind && c.Service == service {
			return &rep.Checks[i]
		}
	}
	return nil
}

// TestScenarioERecovery is the Phase 5 exit criterion: a service fails, a fix
// lands, the recovery is recorded, and verify says resolved only once every
// check has passed across the whole stability window. A fix landing midway
// through the window, or a relapse during it, is never resolved.
func TestScenarioERecovery(t *testing.T) {
	_, fake := buildBins(t)
	home, project := t.TempDir(), t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	os.MkdirAll(filepath.Join(project, "bin"), 0o755)
	if err := os.Symlink(fake, filepath.Join(project, "bin", "fakesvc")); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	fixed := filepath.Join(project, "fixed")
	cfg := fmt.Sprintf(`version: 1
services:
  - name: api
    command: './bin/fakesvc --name api --listen 127.0.0.1:%d --recover-file fixed'
    health: { type: http, url: "http://127.0.0.1:%d/health", interval: 100ms, timeout: 100ms, retries: 2, startup_grace: 500ms }
`, port, port)
	if err := os.WriteFile(filepath.Join(project, "kestrel.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	k := startKestrel(t, home, "watch", "--project", project, "--format", "json")
	evs := readEvents(k.stdout)
	evs.waitFor(t, "api to fail its startup", 30*time.Second, func(e api.Event) bool {
		return e.ServiceID == "api" && e.Type == api.EventStartupTimeout
	})
	// The event reaches stdout before the recorder's next flush (every
	// 100ms) puts it in the database verify reads.
	waitStored(t, home, project, api.EventStartupTimeout)

	// 1. Still broken: still_failing, exit 1, compared live.
	code, rep, _ := verifyJSON(t, home, project, "--window", "1s", "--interval", "250ms")
	if code != 1 || rep.Verdict != api.VerdictStillFailing || !rep.Live || rep.Incident == nil || len(rep.Before) == 0 {
		t.Fatalf("before the fix: exit %d verdict %s live %v incident %v before %d", code, rep.Verdict, rep.Live, rep.Incident != nil, len(rep.Before))
	}
	if c := checkOf(rep, api.CheckKindProbe, "api"); c == nil || c.Status != api.CheckFailed || c.Samples != 5 {
		t.Fatalf("api probe before the fix: %+v", c)
	}

	// 2. The fix lands a third of the way into the window: the probe passes
	// from then on, but the window as a whole wasn't stable.
	type res struct {
		code int
		rep  api.VerificationReport
	}
	mid := make(chan res, 1)
	go func() {
		c, r, _ := verifyJSON(t, home, project, "--window", "1500ms", "--interval", "250ms")
		mid <- res{c, r}
	}()
	time.Sleep(500 * time.Millisecond)
	if err := os.WriteFile(fixed, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m := <-mid
	if m.rep.Verdict == api.VerdictResolved || m.code == 0 {
		t.Fatalf("a fix landing mid-window must not be resolved: %s %v", m.rep.Verdict, m.rep.Reasons)
	}
	if c := checkOf(m.rep, api.CheckKindProbe, "api"); c == nil || c.Status != api.CheckFlaky {
		t.Fatalf("mid-window fix should make the probe flaky: %+v", c)
	}

	// 3. Recovery is recorded by the running session.
	evs.waitFor(t, "api to recover", 30*time.Second, func(e api.Event) bool {
		return e.ServiceID == "api" && e.Type == api.EventProbeRecovered
	})
	var recovered string
	evs.count(func(e api.Event) bool {
		if e.ServiceID == "api" && e.Type == api.EventProbeRecovered && recovered == "" {
			recovered = e.ID
		}
		return false
	})

	// 4. Stable for the full window: resolved, exit 0, and not before the
	// window has elapsed.
	const window = 2 * time.Second
	code, rep, took := verifyJSON(t, home, project, "--window", window.String(), "--interval", "250ms")
	if code != 0 || rep.Verdict != api.VerdictResolved || rep.Claim != api.ClaimVerified {
		t.Fatalf("after the fix: exit %d verdict %s %v", code, rep.Verdict, rep.Reasons)
	}
	if took < window || rep.EndedAt.Sub(rep.StartedAt) < window {
		t.Fatalf("resolved after %v (report says %v); the window is %v", took, rep.EndedAt.Sub(rep.StartedAt), window)
	}
	if c := checkOf(rep, api.CheckKindProbe, "api"); c == nil || c.Status != api.CheckPassed || c.Samples != 9 || c.Passed != 9 {
		t.Fatalf("api probe after the fix: %+v", c)
	}
	if len(rep.Recovery) == 0 || rep.Recovery[0].EventID != recovered {
		t.Fatalf("recovery evidence should cite the probe_recovered event %s: %+v", recovered, rep.Recovery)
	}
	if len(rep.After) != 0 {
		t.Fatalf("no errors should recur after the fix: %+v", rep.After)
	}
	resolvedID := rep.ID

	// 5. A relapse during the window: not resolved, and the new failure is
	// cited from the live session.
	mid2 := make(chan res, 1)
	go func() {
		c, r, _ := verifyJSON(t, home, project, "--window", "2s", "--interval", "250ms")
		mid2 <- res{c, r}
	}()
	time.Sleep(600 * time.Millisecond)
	if err := os.Remove(fixed); err != nil {
		t.Fatal(err)
	}
	m = <-mid2
	if m.rep.Verdict == api.VerdictResolved || m.code == 0 {
		t.Fatalf("a relapse mid-window must not be resolved: %s", m.rep.Verdict)
	}
	if len(m.rep.After) == 0 || m.rep.After[0].Service != "api" {
		t.Fatalf("the relapse should be cited as a recurring failure: %+v", m.rep.After)
	}

	k.ctrlC()
	if code := k.wait(t, 30*time.Second); code != 0 && code != 1 {
		t.Fatalf("watch exit %d; stderr:\n%s", code, k.stderr)
	}
	<-evs.done

	// Every cited event exists in the session, and the verifications are
	// kept with it and shown in its timeline.
	_, out, _ := kestrelCmd(t, home, project, "sessions", "export", rep.Session.ID)
	bundle := decode[api.ExportBundle](t, out)
	ids := map[string]bool{}
	for _, e := range bundle.Events {
		ids[e.ID] = true
	}
	for _, e := range append(append(append([]api.Evidence{}, rep.Before...), rep.Recovery...), m.rep.After...) {
		if !ids[e.EventID] {
			t.Errorf("verification cites event %s, which is not in the session", e.EventID)
		}
	}
	_, out, _ = kestrelCmd(t, home, project, "timeline", "--format", "json")
	tl := decode[api.TimelineReport](t, out)
	if len(tl.Verifications) != 4 || tl.Verifications[2].ID != resolvedID || tl.Verifications[2].Verdict != api.VerdictResolved {
		t.Fatalf("timeline verifications: %+v", tl.Verifications)
	}
	_, text, _ := kestrelCmd(t, home, project, "timeline", "--no-color")
	if !strings.Contains(text, "recovery") || !strings.Contains(text, "Verification") || !strings.Contains(text, "resolved incident #1") {
		t.Fatalf("timeline text should show the recovery and the verifications:\n%s", text)
	}

	// The session has ended: verify says so, and can't compare events.
	_, rep, _ = verifyJSON(t, home, project, "--window", "500ms", "--interval", "250ms")
	if rep.Live || rep.Verdict == api.VerdictResolved || !strings.Contains(strings.Join(rep.Notes, " "), "not running") {
		t.Fatalf("after the session ended: live %v verdict %s notes %v", rep.Live, rep.Verdict, rep.Notes)
	}
}

// listenTCP keeps a port open for the duration of the test (an "external"
// service that is healthy).
func listenTCP(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func execProject(t *testing.T, checks string) string {
	t.Helper()
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	cfg := fmt.Sprintf(`version: 1
services:
  - name: db
    health: { type: tcp, address: "127.0.0.1:%d" }
verify:
  checks:
%s`, listenTCP(t), checks)
	if err := os.WriteFile(filepath.Join(project, "kestrel.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return project
}

// TestVerifyRefusesExecWithoutAuthorization is the other Phase 5 exit
// criterion: a configured command never runs without --allow-exec or an
// explicit yes at a prompt.
func TestVerifyRefusesExecWithoutAuthorization(t *testing.T) {
	home := t.TempDir()
	project := execProject(t, `    - { type: command, run: "touch ran-marker", timeout: 5s }
`)
	marker := filepath.Join(project, "ran-marker")

	// No flag, no terminal (stdin is /dev/null): refused.
	code, rep, _ := verifyJSON(t, home, project, "--window", "500ms", "--interval", "250ms")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the command ran without authorization")
	}
	c := checkOf(rep, api.CheckKindCommand, "")
	if c == nil || c.Status != api.CheckSkipped || !strings.Contains(c.SkipReason, "--allow-exec") || c.Target != "touch ran-marker" {
		t.Fatalf("command check: %+v", c)
	}
	if code != 0 || rep.Verdict != api.VerdictResolved || rep.Session != nil || rep.Saved {
		t.Fatalf("health-only verify: exit %d verdict %s session %v saved %v", code, rep.Verdict, rep.Session, rep.Saved)
	}
	if !strings.Contains(strings.Join(rep.Reasons, " "), "not run: command `touch ran-marker`") {
		t.Fatalf("the skipped command should be listed among the reasons: %v", rep.Reasons)
	}

	// With --allow-exec it runs.
	code, rep, _ = verifyJSON(t, home, project, "--window", "500ms", "--interval", "250ms", "--allow-exec")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the authorized command did not run: %v", err)
	}
	if c := checkOf(rep, api.CheckKindCommand, ""); code != 0 || c == nil || c.Status != api.CheckPassed || c.ExitCode == nil || *c.ExitCode != 0 {
		t.Fatalf("authorized command: exit %d check %+v", code, c)
	}
}

func TestVerifyCommandFailureTimeoutAndRedaction(t *testing.T) {
	_, fake := buildBins(t)
	home := t.TempDir()
	pidfile := filepath.Join(t.TempDir(), "pids")
	project := execProject(t, fmt.Sprintf(`    - { type: command, run: "echo 'FAIL: login with token=sup3rs3cret'; exit 3", timeout: 5s }
    - { type: command, run: "%s --sleep --pidfile %s", timeout: 300ms }
`, fake, pidfile))

	code, rep, _ := verifyJSON(t, home, project, "--window", "500ms", "--interval", "250ms", "--allow-exec")
	if code != 1 || rep.Verdict != api.VerdictPartiallyResolved {
		t.Fatalf("exit %d verdict %s %v", code, rep.Verdict, rep.Reasons)
	}
	failed, timedOut := rep.Checks[1], rep.Checks[2]
	if failed.Status != api.CheckFailed || failed.ExitCode == nil || *failed.ExitCode != 3 || len(failed.Output) != 1 {
		t.Fatalf("failing command: %+v", failed)
	}
	all := fmt.Sprint(rep)
	if strings.Contains(all, "sup3rs3cret") || !strings.Contains(failed.Output[0], "token=[REDACTED") {
		t.Fatalf("command output must be redacted: %q", failed.Output)
	}
	if timedOut.Status != api.CheckTimedOut {
		t.Fatalf("slow command: %+v", timedOut)
	}
	// The timed-out command was stopped: it was Kestrel's to stop.
	pids := pidsFrom(t, pidfile, 1)
	deadline := time.Now().Add(5 * time.Second)
	for alive(pids[0]) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(pids[0]) {
		t.Fatalf("timed-out command (pid %d) is still running", pids[0])
	}
}

// The interactive path, in process: the prompt shows the exact command, its
// directory and timeout, and only an explicit yes runs it.
func TestVerifyPromptInProcess(t *testing.T) {
	t.Setenv("KESTREL_HOME", t.TempDir())
	project := execProject(t, `    - { type: command, run: "touch ran-marker", timeout: 7s }
`)
	marker := filepath.Join(project, "ran-marker")
	runWith := func(answer string) (int, string, string) {
		var out, errb bytes.Buffer
		s := cli.Streams{In: strings.NewReader(answer), Out: &out, Err: &errb, InIsTTY: true,
			Getenv: func(string) string { return "" }, Getwd: func() (string, error) { return project, nil }}
		code := cli.Execute(t.Context(), s, []string{"verify", "--no-color", "--window", "500ms", "--interval", "250ms"})
		return code, out.String(), errb.String()
	}

	for _, answer := range []string{"\n", "n\n", "nope\n", ""} {
		code, out, errs := runWith(answer)
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("answer %q ran the command", answer)
		}
		if !strings.Contains(errs, "touch ran-marker") || !strings.Contains(errs, project) || !strings.Contains(errs, "7s") {
			t.Fatalf("the prompt must show the command, directory and timeout:\n%s", errs)
		}
		if code != 0 || !strings.Contains(out, "declined at the prompt") {
			t.Fatalf("answer %q: exit %d\n%s", answer, code, out)
		}
	}

	code, out, _ := runWith("y\n")
	if _, err := os.Stat(marker); err != nil || code != 0 {
		t.Fatalf("yes should run the command: exit %d, %v\n%s", code, err, out)
	}
	if !strings.Contains(out, "Verdict: resolved") || !strings.Contains(out, "passed · exit code 0") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestVerifyUsageErrors(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	for _, args := range [][]string{
		{"verify", "--window", "1s", "--interval", "1s"}, // 2 samples
		{"verify", "--interval", "0s"},
		{"verify", "nosuchsession"},
	} {
		if code, _, errs := kestrelCmd(t, home, project, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2\n%s", args, code, errs)
		}
	}
	// No config and no session: nothing to check is inconclusive, not success.
	code, rep, _ := verifyJSON(t, home, project, "--window", "500ms", "--interval", "250ms")
	if code != 3 || rep.Verdict != api.VerdictInconclusive {
		t.Fatalf("empty project: exit %d verdict %s", code, rep.Verdict)
	}
}

// In-process run of verify against a live session, so coverage tooling sees
// the live comparison, liveness and connection-target checks, and rendering.
func TestVerifyLiveInProcess(t *testing.T) {
	_, fake := buildBins(t)
	t.Setenv("KESTREL_HOME", t.TempDir())
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	apiPort, nobody := freePort(t), freePort(t)
	cfg := fmt.Sprintf(`version: 1
services:
  - name: api
    command: '%[1]s --name api --listen 127.0.0.1:%[2]d --recover-file fixed'
    health: { type: http, url: "http://127.0.0.1:%[2]d/health", interval: 100ms, timeout: 100ms, retries: 2, startup_grace: 300ms }
  - name: client
    command: '%[1]s --name client --connect 127.0.0.1:%[3]d --connect-every 100ms'
`, fake, apiPort, nobody)
	os.WriteFile(filepath.Join(dir, "kestrel.yaml"), []byte(cfg), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watched := make(chan int, 1)
	var wout, werr bytes.Buffer
	go func() {
		watched <- cli.Execute(ctx, cli.Streams{In: strings.NewReader(""), Out: &wout, Err: &werr,
			Getenv: func(string) string { return "" }, Getwd: func() (string, error) { return dir, nil }},
			[]string{"watch", "--format", "json"})
	}()
	for deadline := time.Now().Add(20 * time.Second); ; {
		select {
		case code := <-watched:
			t.Fatalf("watch ended early (exit %d):\n%s\n%s", code, wout.String(), werr.String())
		default:
		}
		r := run(t, dir, nil, "sessions", "show", "--format", "json")
		var d api.SessionDetail
		if json.Unmarshal([]byte(r.stdout), &d) == nil && d.CountsByType[string(api.EventStartupTimeout)] > 0 &&
			d.CountsByType[string(api.EventDependencyConnFailed)] > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session never recorded the failures:\n%s\n%s\nwatch stderr:\n%s\nwatch stdout (head):\n%.2000s", r.stdout, r.stderr, werr.String(), wout.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	r := run(t, dir, nil, "verify", "--format", "json", "--window", "600ms", "--interval", "200ms")
	rep := decode[api.VerificationReport](t, r.stdout)
	// client's refusals repeat unchanged, so they are folded into summaries
	// (every 10s) and this short window can't see them recur: it says so.
	// Both start at once and fail together: one incident with both members.
	if rep.Incident == nil || len(rep.Incident.Services) != 2 {
		t.Fatalf("expected one incident with api and client: %+v", rep.Incident)
	}
	if r.code != 1 || !rep.Live || rep.Verdict == api.VerdictResolved || !strings.Contains(strings.Join(rep.Notes, " "), "one summary every 10s") {
		t.Fatalf("live verify: exit %d %+v", r.code, rep)
	}
	if c := checkOf(rep, api.CheckKindLiveness, "api"); c == nil || c.Status != api.CheckPassed {
		t.Fatalf("api liveness: %+v", c)
	}
	var target *api.CheckResult
	for i, c := range rep.Checks {
		if c.Kind == api.CheckKindTCP && strings.HasSuffix(c.Target, fmt.Sprintf(":%d", nobody)) {
			target = &rep.Checks[i]
		}
	}
	if target == nil || target.Status != api.CheckFailed || !strings.Contains(target.Origin, "client failed to make") {
		t.Fatalf("the address client failed to reach should be re-checked: %+v", rep.Checks)
	}

	r = run(t, dir, nil, "verify", "--no-color", "--window", "400ms", "--interval", "200ms")
	for _, want := range []string{"Before: what was failing", "During the window", "Verdict:", "Saved as verification"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("verify text lacks %q:\n%s", want, r.stdout)
		}
	}
	if !strings.Contains(r.stderr, "Verifying session") {
		t.Errorf("progress line missing from stderr:\n%s", r.stderr)
	}
	if r = run(t, dir, nil, "verify", "--incident", "99"); r.code != 2 || !strings.Contains(r.stderr, "no incident #99") {
		t.Fatalf("unknown incident: exit %d %s", r.code, r.stderr)
	}

	cancel()
	if code := <-watched; code != 0 && code != 1 {
		t.Fatalf("watch exit %d", code)
	}
	r = run(t, dir, nil, "timeline", "--no-color")
	if !strings.Contains(r.stdout, "Verification") || !strings.Contains(r.stdout, "still_failing") && !strings.Contains(r.stdout, "partially_resolved") {
		t.Fatalf("timeline:\n%s", r.stdout)
	}
}
