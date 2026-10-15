package verification

import (
	"bufio"
	"context"
	"io"
	"sync"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/privacy"
	"github.com/Aareevs/Kestrel-CLI/internal/process"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Output bounds for command checks.
const (
	OutputTailLines = 20
	maxOutputLine   = 300
	stopGrace       = 2 * time.Second
)

// Command is a configured command check. Kestrel runs it only when the caller
// passes an authorization for it; RunCommand never decides that itself.
type Command struct {
	Run     string
	Dir     string // absolute
	Timeout time.Duration
	Env     []string
	// Display is the command as shown to the user (redacted).
	Display string
	// DisplayDir is Dir as shown to the user (relative to the project).
	DisplayDir string
}

// Authorization is proof that the user allowed this exact command to run,
// via --allow-exec or by confirming a prompt that showed it.
type Authorization struct{ Via string }

// Skipped reports a command check that was not run.
func Skipped(c Command, reason string) api.CheckResult {
	return api.CheckResult{Kind: api.CheckKindCommand, Target: c.Display, Origin: "verify.checks", Dir: c.DisplayDir,
		Timeout: c.Timeout.String(), Status: api.CheckSkipped, SkipReason: reason}
}

// RunCommand runs an authorized command in its own process group, bounded by
// its timeout. On timeout or cancellation the whole group is stopped (it is a
// group Kestrel started). Output is kept only as a redacted tail.
func RunCommand(ctx context.Context, c Command, auth *Authorization, red *privacy.Redactor) api.CheckResult {
	res := api.CheckResult{Kind: api.CheckKindCommand, Target: c.Display, Origin: "verify.checks", Dir: c.DisplayDir, Timeout: c.Timeout.String()}
	if auth == nil {
		res.Status, res.SkipReason = api.CheckSkipped, "not authorized"
		return res
	}
	start := time.Now()
	p, stdout, stderr, err := process.Start(process.Spec{Shell: c.Run, Dir: c.Dir, Env: c.Env})
	if err != nil {
		res.Status, res.Detail = api.CheckFailed, "could not start: "+err.Error()
		return res
	}
	tail := &tailBuf{max: OutputTailLines}
	var wg sync.WaitGroup
	for _, r := range []io.ReadCloser{stdout, stderr} {
		wg.Add(1)
		go func(r io.ReadCloser) {
			defer wg.Done()
			defer r.Close()
			tail.read(r, red.NewStream())
		}(r)
	}

	timer := time.NewTimer(c.Timeout)
	defer timer.Stop()
	timedOut, cancelled := false, false
	select {
	case <-p.Done():
	case <-timer.C:
		timedOut = true
	case <-ctx.Done():
		cancelled = true
	}
	// Always stop the group: it ends anything the command left behind, and
	// it is ours to stop.
	p.Stop(stopGrace)
	waitOrGiveUp(&wg, time.Second)
	ex := p.Exit()
	res.DurationMS = time.Since(start).Milliseconds()
	res.Output = tail.lines()
	switch {
	case timedOut:
		res.Status, res.Detail = api.CheckTimedOut, "exceeded its "+c.Timeout.String()+" timeout and was stopped"
	case cancelled:
		res.Status, res.Detail = api.CheckSkipped, "interrupted"
		res.SkipReason = "interrupted"
	default:
		code := ex.Code
		res.ExitCode = &code
		res.Detail = ex.String()
		res.Status = api.CheckFailed
		if ex.Success() {
			res.Status = api.CheckPassed
		}
	}
	return res
}

// waitOrGiveUp waits for the output readers, but not forever: a process that
// escaped the group could hold a pipe open.
func waitOrGiveUp(wg *sync.WaitGroup, d time.Duration) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

// tailBuf keeps the last max lines of combined output, redacted.
type tailBuf struct {
	mu  sync.Mutex
	max int
	buf []string
}

// read consumes one output stream, redacting with that stream's own state
// (a private key block on stderr must not affect stdout).
func (t *tailBuf) read(r io.Reader, rs *privacy.Stream) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		t.add(rs.Line(sc.Text()))
	}
}

func (t *tailBuf) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r := []rune(line); len(r) > maxOutputLine {
		line = string(r[:maxOutputLine]) + "…"
	}
	t.buf = append(t.buf, line)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
}

func (t *tailBuf) lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.buf...)
}
