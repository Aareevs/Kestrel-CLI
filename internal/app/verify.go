package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/collectors"
	"github.com/Aareevs/Kestrel-CLI/internal/config"
	"github.com/Aareevs/Kestrel-CLI/internal/correlation"
	"github.com/Aareevs/Kestrel-CLI/internal/diagnosis"
	"github.com/Aareevs/Kestrel-CLI/internal/diagnosis/rules"
	"github.com/Aareevs/Kestrel-CLI/internal/events"
	"github.com/Aareevs/Kestrel-CLI/internal/graph"
	"github.com/Aareevs/Kestrel-CLI/internal/health"
	"github.com/Aareevs/Kestrel-CLI/internal/privacy"
	"github.com/Aareevs/Kestrel-CLI/internal/storage"
	"github.com/Aareevs/Kestrel-CLI/internal/verification"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Verification defaults.
const (
	DefaultVerifyWindow   = 30 * time.Second
	DefaultVerifyInterval = 2 * time.Second
	// verifySettle lets the running session's recorder flush the last
	// round's events before the final comparison.
	verifySettle = 500 * time.Millisecond
)

// VerifyOptions control `kestrel verify`.
type VerifyOptions struct {
	Window   time.Duration
	Interval time.Duration
	// Incident is the incident number to verify; 0 means the latest.
	Incident int
	// Authorize is asked once per configured command, before the window
	// starts. It returns nil (with a reason) unless the user allowed that
	// exact command. A nil Authorize runs no commands.
	Authorize func(verification.Command) (*verification.Authorization, string)
	// OnPlan is called once the checks are chosen, before sampling starts.
	OnPlan func(VerifyPlan)
	// OnRound reports sampling progress.
	OnRound func(round, rounds int)
	// OnCommand is called before an authorized command starts.
	OnCommand func(verification.Command)
}

// VerifyPlan describes what verify is about to do.
type VerifyPlan struct {
	SessionID string
	Live      bool
	Incident  int
	Probes    int
	Commands  int
	Window    time.Duration
	Interval  time.Duration
}

// Verify checks whether the failure recorded in a session is fixed: it
// samples probes across a stability window, compares new events from a live
// session with the incident's, and runs configured commands only when
// authorized. It never starts or signals services.
func (a *App) Verify(ctx context.Context, ref string, all bool, o VerifyOptions) (*api.VerificationReport, error) {
	w := verification.Window{Duration: o.Window, Interval: o.Interval, Settle: verifySettle, OnRound: o.OnRound}
	if w.Duration == 0 {
		w.Duration = DefaultVerifyWindow
	}
	if w.Interval == 0 {
		w.Interval = DefaultVerifyInterval
	}
	if err := w.Validate(); err != nil {
		return nil, &UsageError{Err: err}
	}

	cfg, _, err := a.LoadConfig()
	switch {
	case errors.Is(err, ErrNoConfig):
		cfg = nil
	case err != nil:
		return nil, &UsageError{Err: err}
	}
	red := privacy.Default
	if cfg != nil {
		if red, err = privacy.New(cfg.Redaction.ExtraPatterns); err != nil {
			return nil, &UsageError{Err: err}
		}
	}

	rep := &api.VerificationReport{SchemaVersion: api.SchemaVersion, Window: w.Duration.String(), Interval: w.Interval.String(),
		Checks: []api.CheckResult{}, Before: []api.Evidence{}, Recovery: []api.Evidence{}, After: []api.Evidence{}}

	// The session to compare against. Verification still works without one
	// (it then checks current health only).
	var (
		an  *analysis
		in  *correlation.Incident
		inN int
	)
	st, err := a.openStoreRecovered(ctx)
	if err != nil {
		rep.Notes = append(rep.Notes, "session storage unavailable ("+storageHint(err)+"); checking current health only, and this result is not saved")
	} else {
		defer st.Close()
		id, err := a.resolveForVerify(ctx, st, ref, all)
		if err != nil {
			return nil, err
		}
		if id == "" {
			rep.Notes = append(rep.Notes, "no recorded session for this project; checking current health only, and this result is not saved")
		} else if an, err = a.load(ctx, st, id); err != nil {
			return nil, err
		}
	}
	if an != nil {
		sum := an.summary
		rep.Session = &sum
		rep.Live = sum.Status == api.SessionActive
		if in, inN, err = pickIncident(an.incidents, o.Incident); err != nil {
			return nil, err
		}
		if in != nil {
			rep.Incident = &in.Incident
			if rep.Before, err = beforeEvidence(ctx, an, in); err != nil {
				return nil, err
			}
			rep.Recovery = recoveryEvidence(in)
		} else {
			rep.Notes = append(rep.Notes, "the session recorded no incident; checking that everything is healthy")
		}
		if rep.Live && w.Duration < collectors.DefaultRepeatWindow {
			rep.Notes = append(rep.Notes, fmt.Sprintf("an error repeating unchanged is recorded as one summary every %s; "+
				"this %s window may not see it recur (the default %s window does)", collectors.DefaultRepeatWindow, w.Duration, DefaultVerifyWindow))
		}
		if !rep.Live {
			rep.Notes = append(rep.Notes, fmt.Sprintf("session %s is not running, so only probes and commands are compared; "+
				"verify while 'kestrel watch' is running to also confirm the errors stopped", sum.ID))
		}
	}

	probes := a.verifyProbes(cfg, an, in, rep.Live, w.Interval)
	cmds := a.verifyCommands(cfg, red)

	// Authorization is settled before sampling, so nobody is prompted halfway
	// through the window.
	auths := make([]*verification.Authorization, len(cmds))
	reasons := make([]string, len(cmds))
	for i, c := range cmds {
		if o.Authorize != nil {
			auths[i], reasons[i] = o.Authorize(c)
		} else {
			reasons[i] = "not authorized"
		}
	}

	var poll func(context.Context) ([]api.Event, error)
	if rep.Live {
		poll, err = livePoll(ctx, an)
		if err != nil {
			return nil, err
		}
	}
	w.Poll = poll
	if o.OnPlan != nil {
		p := VerifyPlan{Live: rep.Live, Incident: inN, Probes: len(probes), Commands: len(cmds), Window: w.Duration, Interval: w.Interval}
		if an != nil {
			p.SessionID = an.summary.ID
		}
		o.OnPlan(p)
	}

	rep.StartedAt = a.Now()
	sampled := verification.Run(ctx, w, probes)
	rep.Checks = append(rep.Checks, sampled.Checks...)
	if sampled.PollErr != nil {
		rep.Notes = append(rep.Notes, "could not read new session events: "+sampled.PollErr.Error())
	}
	for i, c := range cmds {
		if auths[i] == nil || ctx.Err() != nil {
			reason := reasons[i]
			if ctx.Err() != nil && auths[i] != nil {
				reason = "interrupted"
			}
			rep.Checks = append(rep.Checks, verification.Skipped(c, reason))
			continue
		}
		if o.OnCommand != nil {
			o.OnCommand(c)
		}
		rep.Checks = append(rep.Checks, verification.RunCommand(ctx, c, auths[i], red))
	}
	rep.EndedAt = a.Now()
	if rep.Live && an != nil {
		rep.After = afterEvidence(sampled.Events, in)
		if ended, _ := a.sessionEnded(ctx, st, an.summary.ID); ended {
			rep.Notes = append(rep.Notes, "the session stopped during verification")
		}
	}
	for i := range rep.Checks {
		rep.Checks[i].Target = red.Redact(rep.Checks[i].Target)
		rep.Checks[i].Detail = red.Redact(rep.Checks[i].Detail)
	}

	rep.Verdict, rep.Reasons = verification.Decide(verification.Input{
		Checks: rep.Checks, Complete: sampled.Complete && ctx.Err() == nil, Live: rep.Live && sampled.PollErr == nil,
		After: rep.After, HasIncident: in != nil,
	})
	rep.Claim = verification.Claim(rep.Verdict)
	var gen events.IDGen
	rep.ID = gen.New(rep.StartedAt)

	if an != nil {
		// A cancelled ctx must not stop us recording what was observed.
		rep.Saved = true
		if err := st.SaveVerification(context.WithoutCancel(ctx), rep); err != nil {
			rep.Saved = false
			rep.Notes = append(rep.Notes, "this result could not be saved: "+err.Error())
			slog.Warn("could not save verification", "err", err)
		}
	}
	return rep, nil
}

// resolveForVerify resolves the session reference. With no explicit
// reference and no sessions, it returns "" (verify then checks health only).
func (a *App) resolveForVerify(ctx context.Context, st *storage.Store, ref string, all bool) (string, error) {
	if ref == "" || ref == "last" {
		root := a.ProjectRoot
		if all {
			root = ""
		}
		id, err := st.Resolve(ctx, ref, root)
		if errors.Is(err, storage.ErrNotFound) {
			return "", nil
		}
		if err == nil {
			return id, nil
		}
	}
	return a.resolve(ctx, st, ref, all)
}

func (a *App) sessionEnded(ctx context.Context, st *storage.Store, id string) (bool, error) {
	sum, _, err := st.GetSession(context.WithoutCancel(ctx), id)
	if err != nil {
		return false, err
	}
	return sum.Status != api.SessionActive, nil
}

// pickIncident returns incident n (1-based), or the latest when n is 0.
func pickIncident(ins []*correlation.Incident, n int) (*correlation.Incident, int, error) {
	if n == 0 {
		if len(ins) == 0 {
			return nil, 0, nil
		}
		return ins[len(ins)-1], ins[len(ins)-1].Number, nil
	}
	for _, in := range ins {
		if in.Number == n {
			return in, n, nil
		}
	}
	return nil, 0, usage("no incident #%d in this session (it has %d)", n, len(ins))
}

// beforeEvidence is what was failing: the leading hypothesis's supporting
// evidence, or the incident's anomalies when no rule matched.
func beforeEvidence(ctx context.Context, an *analysis, in *correlation.Incident) ([]api.Evidence, error) {
	tail, err := an.tails(ctx, in)
	if err != nil {
		return nil, err
	}
	d := diagnosis.Diagnose(&rules.Context{Incident: in, Graph: an.graph, Events: an.events, Tail: tail})
	if len(d.Hypotheses) > 0 && len(d.Hypotheses[0].Supporting) > 0 {
		return d.Hypotheses[0].Supporting, nil
	}
	out := []api.Evidence{}
	for _, e := range in.Timeline {
		switch e.Role {
		case api.RoleFirstAnomaly, api.RoleKey, api.RoleSymptom:
			out = append(out, api.Evidence{EventID: e.EventID, Time: e.Time, Service: e.Service, Summary: e.Message, Count: e.Count})
		}
	}
	return out, nil
}

// recoveryEvidence is recovery the session recorded for the incident's members.
func recoveryEvidence(in *correlation.Incident) []api.Evidence {
	out := []api.Evidence{}
	for _, e := range in.Timeline {
		if e.Role == api.RoleRecovery {
			out = append(out, api.Evidence{EventID: e.EventID, Time: e.Time, Service: e.Service, Summary: e.Message})
		}
	}
	return out
}

// afterEvidence: anomalies during the window from the incident's services
// (from any service when there was no incident), repeats collapsed.
func afterEvidence(evs []api.Event, in *correlation.Incident) []api.Evidence {
	out := []api.Evidence{}
	idx := map[string]int{}
	for _, e := range evs {
		if !correlation.IsAnomaly(e) || (in != nil && !in.Has(e.ServiceID)) {
			continue
		}
		key := e.ServiceID + "|" + string(e.Type) + "|" + e.Fingerprint
		if i, ok := idx[key]; ok && e.Fingerprint != "" {
			out[i].Count += e.Lines()
			continue
		}
		idx[key] = len(out)
		out = append(out, api.Evidence{EventID: e.ID, Time: e.Time, Service: e.ServiceID, Summary: e.Message, Count: e.Lines()})
	}
	return out
}

// livePoll returns a function yielding the session's events recorded since
// the previous call, starting from now.
func livePoll(ctx context.Context, an *analysis) (func(context.Context) ([]api.Event, error), error) {
	latest, err := an.store.Events(ctx, an.summary.ID, storage.EventQuery{Limit: 1, Newest: true})
	if err != nil {
		return nil, err
	}
	var after uint64
	if len(latest) > 0 {
		after = latest[0].Seq
	}
	return func(ctx context.Context) ([]api.Event, error) {
		evs, err := an.store.Events(context.WithoutCancel(ctx), an.summary.ID, storage.EventQuery{AfterSeq: after})
		if err != nil {
			return nil, err
		}
		if len(evs) > 0 {
			after = evs[len(evs)-1].Seq
		}
		return evs, nil
	}, nil
}

// verifyProbes chooses what to sample:
//   - probe checks listed in verify.checks;
//   - the health probe of every affected service (every service when there
//     is no incident), or, without one, a TCP check of the port it announced;
//   - each address the incident's services failed to connect to;
//   - with a live session, that each affected supervised process still runs.
func (a *App) verifyProbes(cfg *config.Config, an *analysis, in *correlation.Incident, live bool, interval time.Duration) []verification.Probe {
	var out []verification.Probe
	covered := map[int]bool{} // ports already checked
	probed := map[string]bool{}
	timeout := func(d time.Duration) time.Duration {
		if d <= 0 {
			d = config.DefaultHealthTimeout
		}
		return min(d, interval)
	}
	addHealth := func(svc *config.Service, origin string) {
		if svc == nil || svc.Health == nil || svc.Health.Type == config.HealthProcess || probed[svc.Name] {
			return
		}
		pr, err := health.FromConfig(svc.Health, nil)
		if err != nil {
			return
		}
		probed[svc.Name] = true
		if p := probePort(svc.Health); p > 0 {
			covered[p] = true
		}
		target := strings.TrimPrefix(pr.String(), "http ")
		target = strings.TrimPrefix(target, "tcp ")
		out = append(out, verification.Probe{Result: api.CheckResult{Kind: api.CheckKindProbe, Service: svc.Name, Target: target, Origin: origin},
			Prober: pr, Timeout: timeout(svc.Health.Timeout.D())})
	}
	addTCP := func(svc, addr, origin string, port int) {
		if covered[port] {
			return
		}
		covered[port] = true
		out = append(out, verification.Probe{Result: api.CheckResult{Kind: api.CheckKindTCP, Service: svc, Target: addr, Origin: origin},
			Prober: &health.TCP{Address: addr}, Timeout: timeout(0)})
	}

	if cfg != nil {
		for _, ch := range cfg.Verify.Checks {
			if ch.Type == config.CheckProbe {
				addHealth(cfg.Service(ch.Service), "verify.checks")
			}
		}
	}

	// The services to check.
	var targets []string
	switch {
	case in != nil:
		targets = in.Services
	case an != nil:
		for _, s := range an.topo.Services {
			targets = append(targets, s.Name)
		}
	case cfg != nil:
		for _, s := range cfg.Services {
			targets = append(targets, s.Name)
		}
	}
	listening := map[string]int{}
	if an != nil {
		for _, e := range an.events {
			if e.Attrs.PatternID == "L001-listening" && e.Attrs.Port > 0 {
				listening[e.ServiceID] = e.Attrs.Port
			}
		}
	}
	for _, name := range targets {
		if cfg != nil {
			if svc := cfg.Service(name); svc != nil && svc.Health != nil && svc.Health.Type != config.HealthProcess {
				addHealth(svc, "its health probe")
				continue
			}
		}
		if p := listening[name]; p > 0 {
			addTCP(name, net.JoinHostPort("localhost", strconv.Itoa(p)), "the port it announced", p)
		}
	}

	if in != nil {
		var g *graph.Graph
		if an != nil {
			g = an.graph
		}
		for _, svc := range in.Services {
			for _, e := range in.ByService[svc] {
				if e.Type != api.EventDependencyConnFailed {
					continue
				}
				port := graph.ConnPort(e)
				if port <= 0 {
					continue
				}
				host := "localhost"
				if h, _, err := net.SplitHostPort(e.Attrs.Target); err == nil && h != "" {
					host = h
				}
				owner := ""
				if g != nil {
					owner, _ = g.PortOwner(port)
				}
				addTCP(owner, net.JoinHostPort(host, strconv.Itoa(port)), "a connection "+svc+" failed to make", port)
			}
		}
	}

	if live && an != nil {
		external := map[string]bool{}
		for _, s := range an.topo.Services {
			external[s.Name] = s.External
		}
		names := targets
		if in != nil {
			names = in.Services
		}
		for _, name := range names {
			if external[name] {
				continue
			}
			out = append(out, verification.Probe{
				Result: api.CheckResult{Kind: api.CheckKindLiveness, Service: name, Target: "process", Origin: "the running session"},
				Prober: &health.Liveness{Alive: a.liveAlive(an.summary.ID, name)}, Timeout: timeout(0),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return kindOrder(out[i].Result.Kind) < kindOrder(out[j].Result.Kind) })
	return out
}

func kindOrder(k string) int {
	return map[string]int{api.CheckKindLiveness: 0, api.CheckKindProbe: 1, api.CheckKindTCP: 2}[k]
}

// liveAlive reads the running session's live status file: is the service's
// process still running?
func (a *App) liveAlive(sessionID, svc string) func() bool {
	return func() bool {
		st, err := readState(a.ProjectRoot)
		if err != nil || st == nil || st.SessionID != sessionID {
			return false
		}
		for _, s := range st.Services {
			if s.Name == svc {
				return s.Liveness == api.LivenessRunning
			}
		}
		return false
	}
}

// verifyCommands lists the configured command checks (none run yet).
func (a *App) verifyCommands(cfg *config.Config, red *privacy.Redactor) []verification.Command {
	if cfg == nil {
		return nil
	}
	var out []verification.Command
	for _, ch := range cfg.Verify.Checks {
		if ch.Type != config.CheckCommand {
			continue
		}
		dir, disp := a.ProjectRoot, "."
		if ch.Dir != "" {
			dir, disp = filepath.Join(a.ProjectRoot, ch.Dir), ch.Dir
			if filepath.IsAbs(ch.Dir) {
				dir = ch.Dir
			}
		}
		t := ch.Timeout.D()
		if t <= 0 {
			t = config.DefaultCommandTimeout
		}
		out = append(out, verification.Command{Run: ch.Run, Dir: dir, Timeout: t, Env: os.Environ(),
			Display: red.Redact(ch.Run), DisplayDir: disp})
	}
	return out
}
