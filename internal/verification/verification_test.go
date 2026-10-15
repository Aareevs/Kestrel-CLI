package verification

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/health"
	"github.com/Aareevs/Kestrel-CLI/internal/privacy"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// scripted passes or fails per call, from a script ("ffpp" = fail, fail,
// pass, pass; the last entry repeats).
type scripted struct {
	script string
	n      atomic.Int32
}

func (s *scripted) String() string { return "scripted" }

func (s *scripted) Probe(context.Context) health.Result {
	i := int(s.n.Add(1)) - 1
	if i >= len(s.script) {
		i = len(s.script) - 1
	}
	if s.script[i] == 'p' {
		return health.Result{OK: true, Detail: "ok"}
	}
	return health.Result{Detail: "refused"}
}

func probe(name, script string) Probe {
	return Probe{Result: api.CheckResult{Kind: api.CheckKindProbe, Service: name, Target: "x"}, Prober: &scripted{script: script}, Timeout: time.Second}
}

// fakeSleep records the waits instead of sleeping.
type fakeSleep struct{ waits []time.Duration }

func (f *fakeSleep) sleep(ctx context.Context, d time.Duration) error {
	f.waits = append(f.waits, d)
	return ctx.Err()
}

func TestWindowValidate(t *testing.T) {
	cases := []struct {
		d, i time.Duration
		ok   bool
	}{
		{30 * time.Second, 2 * time.Second, true},
		{2 * time.Second, time.Second, true}, // 3 samples
		{time.Second, time.Second, false},    // 2 samples
		{0, time.Second, false},
		{time.Second, 0, false},
	}
	for _, c := range cases {
		err := Window{Duration: c.d, Interval: c.i}.Validate()
		if (err == nil) != c.ok {
			t.Errorf("window %v / %v: err %v", c.d, c.i, err)
		}
	}
	if n := (Window{Duration: 30 * time.Second, Interval: 2 * time.Second}).Rounds(); n != 16 {
		t.Errorf("rounds = %d, want 16", n)
	}
}

func TestRunSamplesTheWholeWindow(t *testing.T) {
	fs := &fakeSleep{}
	var rounds []int
	polls := 0
	w := Window{Duration: 2 * time.Second, Interval: 500 * time.Millisecond, Sleep: fs.sleep, Settle: 100 * time.Millisecond,
		OnRound: func(r, _ int) { rounds = append(rounds, r) },
		Poll: func(context.Context) ([]api.Event, error) {
			polls++
			return []api.Event{{Seq: uint64(polls)}}, nil
		}}
	got := Run(context.Background(), w, []Probe{probe("steady", "p"), probe("late", "ffp"), probe("down", "f"), probe("blip", "ppfp")})
	if !got.Complete {
		t.Fatal("window should be complete")
	}
	// 5 samples, 4 interval waits, then the settle wait. Even the steady
	// probe is sampled to the end: an early pass ends nothing.
	if len(fs.waits) != 5 || fs.waits[4] != 100*time.Millisecond || len(rounds) != 5 {
		t.Fatalf("waits %v rounds %v", fs.waits, rounds)
	}
	want := map[string]string{"steady": api.CheckPassed, "late": api.CheckFlaky, "down": api.CheckFailed, "blip": api.CheckFlaky}
	for _, c := range got.Checks {
		if c.Samples != 5 || c.Status != want[c.Service] {
			t.Errorf("%s: %d samples, status %s, want %s", c.Service, c.Samples, c.Status, want[c.Service])
		}
	}
	if got.Checks[1].Passed != 3 || got.Checks[1].Detail != "refused" {
		t.Errorf("late: %+v (detail should be the last failure)", got.Checks[1])
	}
	if polls != 6 || len(got.Events) != 6 {
		t.Errorf("polled %d times (%d events), want once per round plus a final poll", polls, len(got.Events))
	}
}

func TestRunInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	w := Window{Duration: 2 * time.Second, Interval: 500 * time.Millisecond, Sleep: func(ctx context.Context, _ time.Duration) error {
		n++
		if n == 2 {
			cancel()
		}
		return ctx.Err()
	}}
	got := Run(ctx, w, []Probe{probe("a", "p")})
	if got.Complete || got.Checks[0].Samples != 2 {
		t.Fatalf("complete %v samples %d", got.Complete, got.Checks[0].Samples)
	}
	if v, _ := Decide(Input{Checks: got.Checks, Complete: got.Complete}); v != api.VerdictInconclusive {
		t.Fatalf("interrupted window: %s", v)
	}
	// Cancelled before the first sample: the check is marked as never sampled.
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	got = Run(ctx2, w, []Probe{probe("a", "p")})
	if got.Checks[0].Status != api.CheckSkipped {
		t.Fatalf("status %s", got.Checks[0].Status)
	}
}

func check(kind, svc, status string) api.CheckResult {
	return api.CheckResult{Kind: kind, Service: svc, Target: "t", Status: status}
}

func TestDecide(t *testing.T) {
	pass := check(api.CheckKindProbe, "api", api.CheckPassed)
	fail := check(api.CheckKindProbe, "db", api.CheckFailed)
	flaky := check(api.CheckKindTCP, "db", api.CheckFlaky)
	liveOK := check(api.CheckKindLiveness, "db", api.CheckPassed)
	skippedCmd := check(api.CheckKindCommand, "", api.CheckSkipped)
	timedOut := check(api.CheckKindCommand, "", api.CheckTimedOut)
	recur := []api.Evidence{{EventID: "e1", Service: "db", Count: 3}}

	cases := []struct {
		name string
		in   Input
		want api.Verdict
		why  string
	}{
		{"all pass, live, nothing recurs", Input{Checks: []api.CheckResult{pass}, Complete: true, Live: true, HasIncident: true}, api.VerdictResolved, "no new errors"},
		{"all pass, not live", Input{Checks: []api.CheckResult{pass}, Complete: true, HasIncident: true}, api.VerdictResolved, "could not be checked"},
		{"pass but errors recur", Input{Checks: []api.CheckResult{pass}, Complete: true, Live: true, After: recur}, api.VerdictPartiallyResolved, "3 new error event(s)"},
		{"some fail", Input{Checks: []api.CheckResult{pass, fail}, Complete: true}, api.VerdictPartiallyResolved, "failing: db"},
		{"flaky only", Input{Checks: []api.CheckResult{flaky}, Complete: true}, api.VerdictPartiallyResolved, "unstable"},
		{"all fail", Input{Checks: []api.CheckResult{fail}, Complete: true}, api.VerdictStillFailing, "failing"},
		{"command timed out", Input{Checks: []api.CheckResult{timedOut}, Complete: true}, api.VerdictStillFailing, "failing"},
		{"nothing ran", Input{Checks: []api.CheckResult{skippedCmd}, Complete: true}, api.VerdictInconclusive, "no check could run"},
		{"live, nothing to probe, quiet", Input{Complete: true, Live: true}, api.VerdictInconclusive, "no check could confirm"},
		{"live, nothing to probe, errors", Input{Complete: true, Live: true, After: recur}, api.VerdictStillFailing, "new error"},
		{"interrupted", Input{Checks: []api.CheckResult{pass}}, api.VerdictInconclusive, "interrupted"},
		// Alive but failing its probe is not partial progress.
		{"alive but probe fails", Input{Checks: []api.CheckResult{liveOK, fail}, Complete: true, Live: true}, api.VerdictStillFailing, "failing: db"},
		{"skipped command listed", Input{Checks: []api.CheckResult{pass, skippedCmd}, Complete: true}, api.VerdictResolved, "not run: command"},
	}
	for _, c := range cases {
		v, why := Decide(c.in)
		if v != c.want || !strings.Contains(strings.Join(why, " | "), c.why) {
			t.Errorf("%s: %s %q, want %s containing %q", c.name, v, why, c.want, c.why)
		}
	}
	if Claim(api.VerdictResolved) != api.ClaimVerified || Claim(api.VerdictInconclusive) != api.ClaimUnresolved || Claim(api.VerdictStillFailing) != api.ClaimObserved {
		t.Error("claim mapping")
	}
}

func shellCmd(run string, timeout time.Duration) Command {
	return Command{Run: run, Timeout: timeout, Display: run, DisplayDir: "."}
}

func TestRunCommandNeedsAuthorization(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	c := shellCmd("touch "+marker, time.Second)
	res := RunCommand(context.Background(), c, nil, privacy.Default)
	if res.Status != api.CheckSkipped || res.SkipReason != "not authorized" {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("ran without authorization")
	}
	if s := Skipped(c, "declined"); s.Status != api.CheckSkipped || s.SkipReason != "declined" || s.Timeout != "1s" {
		t.Fatalf("%+v", s)
	}
}

func TestRunCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	auth := &Authorization{Via: "test"}
	dir := t.TempDir()

	c := shellCmd("pwd; echo bye", 5*time.Second)
	c.Dir = dir
	res := RunCommand(context.Background(), c, auth, privacy.Default)
	if res.Status != api.CheckPassed || res.ExitCode == nil || *res.ExitCode != 0 || len(res.Output) != 2 {
		t.Fatalf("%+v", res)
	}
	if real, _ := filepath.EvalSymlinks(dir); res.Output[0] != dir && res.Output[0] != real {
		t.Errorf("ran in %q, want %q", res.Output[0], dir)
	}

	// Failing, with a secret and more output than the tail keeps.
	res = RunCommand(context.Background(), shellCmd(`for i in $(seq 1 30); do echo "line $i"; done; echo "password=hunter22" >&2; exit 4`, 5*time.Second), auth, privacy.Default)
	if res.Status != api.CheckFailed || *res.ExitCode != 4 || len(res.Output) != OutputTailLines {
		t.Fatalf("%+v", res)
	}
	joined := strings.Join(res.Output, "\n")
	if strings.Contains(joined, "hunter22") || !strings.Contains(joined, "line 30") || strings.Contains(joined, "line 1\n") {
		t.Fatalf("tail should be the last %d lines, redacted:\n%s", OutputTailLines, joined)
	}

	// Timeout: stopped, reported as timed_out.
	start := time.Now()
	res = RunCommand(context.Background(), shellCmd("sleep 30", 200*time.Millisecond), auth, privacy.Default)
	if res.Status != api.CheckTimedOut || time.Since(start) > 5*time.Second {
		t.Fatalf("%+v after %v", res, time.Since(start))
	}

	// Cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	res = RunCommand(ctx, shellCmd("sleep 30", 10*time.Second), auth, privacy.Default)
	if res.Status != api.CheckSkipped || res.SkipReason != "interrupted" {
		t.Fatalf("%+v", res)
	}

	// Unstartable (missing directory).
	bad := shellCmd("true", time.Second)
	bad.Dir = filepath.Join(dir, "missing")
	if res = RunCommand(context.Background(), bad, auth, privacy.Default); res.Status != api.CheckFailed || !strings.HasPrefix(res.Detail, "could not start") {
		t.Fatalf("%+v", res)
	}
}
