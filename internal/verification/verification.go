// Package verification decides whether a fix worked.
//
// One passing probe proves little, so checks are sampled across a stability
// window and a check passes only if every sample did. When the session being
// verified is still running, new events from the incident's services are
// compared with the incident's own: if they keep happening, it isn't fixed.
// Commands run only when the caller has authorization for them (see Command).
package verification

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/health"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// MinSamples is the fewest probe rounds a window may have.
const MinSamples = 3

// Probe is one check sampled across the window.
type Probe struct {
	Result  api.CheckResult // Kind, Service, Target, Origin; filled in by Run
	Prober  health.Prober
	Timeout time.Duration
}

// Window controls sampling.
type Window struct {
	Duration time.Duration
	Interval time.Duration
	// Sleep waits between rounds (injectable for tests).
	Sleep func(ctx context.Context, d time.Duration) error
	// Poll, if set, is called after every round and once more at the end;
	// it returns events recorded since the previous call.
	Poll func(ctx context.Context) ([]api.Event, error)
	// Settle is how long to wait before the final Poll so events from the
	// last round reach storage.
	Settle time.Duration
	// OnRound is called after each round (progress display).
	OnRound func(round, rounds int)
}

// Rounds is how many samples a window takes: one at the start and one per
// interval after it, so the last sample lands at the end of the window.
func (w Window) Rounds() int { return int(w.Duration/w.Interval) + 1 }

// Validate rejects windows too short to show stability.
func (w Window) Validate() error {
	if w.Interval <= 0 || w.Duration <= 0 {
		return fmt.Errorf("window and interval must be positive")
	}
	if w.Rounds() < MinSamples {
		return fmt.Errorf("a %s window at %s intervals gives %d samples; stability needs at least %d (lengthen --window or shorten --interval)",
			w.Duration, w.Interval, w.Rounds(), MinSamples)
	}
	return nil
}

// Sampled is the outcome of a window.
type Sampled struct {
	Checks []api.CheckResult
	Events []api.Event // everything Poll returned, in order
	// Complete is false when the window was cut short (ctx cancelled).
	Complete bool
	PollErr  error
}

// Run samples every probe once per round across the window. It always takes
// the full window: a fix isn't stable because the first probe passed.
func Run(ctx context.Context, w Window, probes []Probe) Sampled {
	sleep := w.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	out := Sampled{Checks: make([]api.CheckResult, len(probes))}
	lastFail := make([]string, len(probes))
	lastOK := make([]string, len(probes))
	for i, p := range probes {
		out.Checks[i] = p.Result
	}
	poll := func() {
		if w.Poll == nil || out.PollErr != nil {
			return
		}
		evs, err := w.Poll(ctx)
		if err != nil {
			out.PollErr = err
			return
		}
		out.Events = append(out.Events, evs...)
	}
	rounds := w.Rounds()
	for r := 0; r < rounds; r++ {
		if r > 0 {
			if err := sleep(ctx, w.Interval); err != nil {
				break
			}
		}
		var wg sync.WaitGroup
		res := make([]health.Result, len(probes))
		for i, p := range probes {
			wg.Add(1)
			go func(i int, p Probe) {
				defer wg.Done()
				res[i] = health.Once(ctx, p.Prober, p.Timeout)
			}(i, p)
		}
		wg.Wait()
		if ctx.Err() != nil {
			break
		}
		for i, rr := range res {
			c := &out.Checks[i]
			c.Samples++
			if rr.OK {
				c.Passed++
				lastOK[i] = rr.Detail
			} else {
				lastFail[i] = rr.Detail
			}
		}
		poll()
		if w.OnRound != nil {
			w.OnRound(r+1, rounds)
		}
		if r == rounds-1 {
			out.Complete = true
		}
	}
	if out.Complete && w.Poll != nil {
		if w.Settle > 0 {
			_ = sleep(ctx, w.Settle)
		}
		poll()
	}
	for i := range out.Checks {
		c := &out.Checks[i]
		switch {
		case c.Samples == 0:
			c.Status, c.Detail = api.CheckSkipped, "no samples taken"
			c.SkipReason = "interrupted before the first sample"
		case c.Passed == c.Samples:
			c.Status, c.Detail = api.CheckPassed, lastOK[i]
		case c.Passed == 0:
			c.Status, c.Detail = api.CheckFailed, lastFail[i]
		default:
			c.Status, c.Detail = api.CheckFlaky, lastFail[i]
		}
	}
	return out
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Input is everything a verdict is decided from.
type Input struct {
	Checks []api.CheckResult
	// Complete is false when the window was cut short.
	Complete bool
	// Live is true when new events could be compared.
	Live bool
	// After are anomalies from the incident's services during the window.
	After []api.Evidence
	// HasIncident is false when there was nothing recorded to compare against.
	HasIncident bool
}

// Decide turns the observations into a verdict and the reasons for it.
func Decide(in Input) (api.Verdict, []string) {
	var passed, failed, flaky, skipped []string
	// A running process proves little when the same service has a real
	// probe: alive but answering 503 is not progress. Its passing liveness
	// check then doesn't count toward a partial fix.
	probed := map[string]bool{}
	for _, c := range in.Checks {
		if c.Service != "" && c.Kind != api.CheckKindLiveness && c.Kind != api.CheckKindCommand && c.Status != api.CheckSkipped {
			probed[c.Service] = true
		}
	}
	for _, c := range in.Checks {
		name := checkName(c)
		switch c.Status {
		case api.CheckPassed:
			if c.Kind == api.CheckKindLiveness && probed[c.Service] {
				continue
			}
			passed = append(passed, name)
		case api.CheckFailed, api.CheckTimedOut:
			failed = append(failed, name)
		case api.CheckFlaky:
			flaky = append(flaky, name)
		default:
			skipped = append(skipped, name)
		}
	}
	ran := len(passed) + len(failed) + len(flaky)
	var reasons []string
	if !in.Complete {
		return api.VerdictInconclusive, []string{"verification was interrupted before the stability window ended"}
	}
	if ran == 0 && !in.Live {
		reasons = append(reasons, "no check could run: configure a health probe or verify.checks, or verify while 'kestrel watch' is running")
		if len(skipped) > 0 {
			reasons = append(reasons, "skipped: "+strings.Join(skipped, ", "))
		}
		return api.VerdictInconclusive, reasons
	}

	recurring := len(in.After) > 0
	if len(failed) > 0 {
		reasons = append(reasons, "failing: "+strings.Join(failed, ", "))
	}
	if len(flaky) > 0 {
		reasons = append(reasons, "unstable (passed on some samples only): "+strings.Join(flaky, ", "))
	}
	if recurring {
		n := 0
		for _, e := range in.After {
			n += max(e.Count, 1)
		}
		reasons = append(reasons, fmt.Sprintf("the affected services logged %d new error event(s) during the window", n))
	}
	if len(passed) > 0 {
		reasons = append(reasons, "passing on every sample: "+strings.Join(passed, ", "))
	}
	if len(skipped) > 0 {
		reasons = append(reasons, "not run: "+strings.Join(skipped, ", "))
	}

	switch {
	case len(failed) == 0 && len(flaky) == 0 && !recurring:
		if ran == 0 {
			// Live, nothing to probe, but nothing recurred either: not enough
			// to call it fixed.
			return api.VerdictInconclusive, append([]string{"no new errors during the window, but no check could confirm the services work"}, reasons...)
		}
		if in.Live {
			reasons = append(reasons, "no new errors from the affected services during the window")
		} else if in.HasIncident {
			reasons = append(reasons, "the session is not running, so recurrence of its errors could not be checked")
		}
		return api.VerdictResolved, reasons
	case len(passed) == 0 && len(flaky) == 0:
		return api.VerdictStillFailing, reasons
	}
	return api.VerdictPartiallyResolved, reasons
}

func checkName(c api.CheckResult) string {
	switch {
	case c.Kind == api.CheckKindCommand:
		return "command `" + c.Target + "`"
	case c.Service != "":
		return c.Service + " (" + c.Kind + " " + c.Target + ")"
	}
	return c.Kind + " " + c.Target
}

// Claim maps a verdict to a claim level.
func Claim(v api.Verdict) api.ClaimLevel {
	switch v {
	case api.VerdictResolved:
		return api.ClaimVerified
	case api.VerdictInconclusive:
		return api.ClaimUnresolved
	}
	return api.ClaimObserved
}
