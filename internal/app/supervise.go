package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/collectors"
	"github.com/Aareevs/Kestrel-CLI/internal/config"
	"github.com/Aareevs/Kestrel-CLI/internal/events"
	"github.com/Aareevs/Kestrel-CLI/internal/health"
	"github.com/Aareevs/Kestrel-CLI/internal/privacy"
	"github.com/Aareevs/Kestrel-CLI/internal/process"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// UsageError marks problems with the user's request or config (exit code 2).
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

func usage(format string, a ...any) error { return &UsageError{Err: fmt.Errorf(format, a...)} }

// NamedCommand is a service given on the command line (run --svc name=cmd).
type NamedCommand struct {
	Name    string
	Command string
}

// SuperviseOptions are shared by run and watch.
type SuperviseOptions struct {
	StopGrace time.Duration
	// Sink receives every event (redacted) in order; typically the live renderer.
	Sink collectors.Sink
	// OnSession is called once the session ID is known, before anything starts.
	OnSession func(sessionID string, services []string)
	// Warn receives problems the user should see while the session runs
	// (e.g. session storage unavailable).
	Warn func(msg string)
}

// Outcome summarizes a finished session.
type Outcome struct {
	SessionID   string
	Interrupted bool
	Failed      bool // a process crashed or a service ended not ready
	Services    []api.ServiceStatus
	Dropped     int
	// Recorded is true when the session was persisted. WriteErrors counts
	// events that could not be fully stored (the record is then incomplete).
	Recorded    bool
	WriteErrors int
	// Retention is set when closing the session removed older ones.
	Retention *api.RetentionResult
}

// RunCommands is zero-config supervision: each command becomes a service, its
// output is captured and classified, and any port it announces is probed.
// Commands run in the current working directory.
func (a *App) RunCommands(ctx context.Context, cwd string, argv []string, named []NamedCommand, name string, o SuperviseOptions) (*Outcome, error) {
	var specs []process.ServiceSpec
	if len(argv) > 0 {
		if name == "" {
			name = cleanServiceName(filepath.Base(argv[0]))
		}
		specs = append(specs, process.ServiceSpec{
			Name: name, Run: &process.Spec{Argv: argv, Dir: cwd, Env: os.Environ()},
			Command: displayArgv(argv), AutoProbe: true,
		})
	}
	for _, n := range named {
		specs = append(specs, process.ServiceSpec{
			Name: n.Name, Run: &process.Spec{Shell: n.Command, Dir: cwd, Env: os.Environ()},
			Command: n.Command, AutoProbe: true,
		})
	}
	if len(specs) == 0 {
		return nil, usage("nothing to run: pass a command after --, or --svc name=\"command\"")
	}
	seen := map[string]bool{}
	for _, s := range specs {
		if seen[s.Name] {
			return nil, usage("service name %q is used twice; set distinct names with --name/--svc", s.Name)
		}
		seen[s.Name] = true
	}
	red, err := a.redactor()
	if err != nil {
		return nil, err
	}
	return a.supervise(ctx, "run", specs, true, red, o)
}

// Watch supervises the configured services named (all if none), plus their
// dependencies.
func (a *App) Watch(ctx context.Context, names []string, o SuperviseOptions) (*Outcome, error) {
	cfg, _, err := a.LoadConfig()
	if errors.Is(err, ErrNoConfig) {
		return nil, usage("%v; watch needs one (try 'kestrel init', or 'kestrel run -- <command>' without config)", err)
	}
	if err != nil {
		return nil, &UsageError{Err: err}
	}
	selected, err := selectServices(cfg, names)
	if err != nil {
		return nil, err
	}
	env, err := cfg.ExpandEnv(a.lookupEnv())
	if err != nil {
		return nil, &UsageError{Err: err}
	}
	var specs []process.ServiceSpec
	for _, s := range cfg.Services {
		if !selected[s.Name] {
			continue
		}
		sp := process.ServiceSpec{Name: s.Name, Health: s.Health, DependsOn: s.DependsOn, Command: s.Command}
		if s.Command != "" {
			dir := a.ProjectRoot
			if s.Dir != "" {
				dir = filepath.Join(a.ProjectRoot, s.Dir)
			}
			sp.Run = &process.Spec{Shell: s.Command, Dir: dir, Env: mergeEnv(os.Environ(), env[s.Name])}
		}
		specs = append(specs, sp)
	}
	red, err := privacy.New(cfg.Redaction.ExtraPatterns)
	if err != nil {
		return nil, &UsageError{Err: err}
	}
	return a.supervise(ctx, "watch", specs, false, red, o)
}

func (a *App) lookupEnv() func(string) (string, bool) {
	if a.LookupEnv != nil {
		return a.LookupEnv
	}
	return os.LookupEnv
}

// selectServices returns the named services and everything they depend on.
func selectServices(cfg *config.Config, names []string) (map[string]bool, error) {
	sel := map[string]bool{}
	if len(names) == 0 {
		for _, s := range cfg.Services {
			sel[s.Name] = true
		}
		if len(sel) == 0 {
			return nil, usage("kestrel.yaml defines no services")
		}
		return sel, nil
	}
	var add func(string) error
	add = func(n string) error {
		if sel[n] {
			return nil
		}
		s := cfg.Service(n)
		if s == nil {
			var known []string
			for _, s := range cfg.Services {
				known = append(known, s.Name)
			}
			return usage("unknown service %q (configured: %s)", n, strings.Join(known, ", "))
		}
		sel[n] = true
		for _, d := range s.DependsOn {
			if err := add(d); err != nil {
				return err
			}
		}
		return nil
	}
	for _, n := range names {
		if err := add(n); err != nil {
			return nil, err
		}
	}
	return sel, nil
}

func mergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, override := extra[k]; !override {
			out = append(out, kv)
		}
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+extra[k])
	}
	return out
}

// redactor uses the project's extra patterns when a valid config exists.
func (a *App) redactor() (*privacy.Redactor, error) {
	cfg, _, err := a.LoadConfig()
	switch {
	case errors.Is(err, ErrNoConfig):
		return privacy.Default, nil
	case err != nil:
		return nil, &UsageError{Err: err}
	}
	r, err := privacy.New(cfg.Redaction.ExtraPatterns)
	if err != nil {
		return nil, &UsageError{Err: err}
	}
	return r, nil
}

func (a *App) supervise(ctx context.Context, mode string, specs []process.ServiceSpec, exitWhenDone bool, red *privacy.Redactor, o SuperviseOptions) (*Outcome, error) {
	stamper := events.NewStamper(a.Now)
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	if o.OnSession != nil {
		o.OnSession(stamper.SessionID, names)
	}
	sink := o.Sink
	if sink == nil {
		sink = collectors.SinkFunc(func(api.Event) {})
	}

	warn := func(msg string) {
		if o.Warn != nil {
			o.Warn(msg)
		}
	}
	rec, closeStore := a.openRecorder(stamper.SessionID, mode, names, topologyOf(specs), warn)
	emit := sink
	if rec != nil {
		emit = collectors.SinkFunc(func(ev api.Event) {
			rec.Emit(ev)
			sink.Emit(ev)
		})
	}

	state := newStateWriter(a.ProjectRoot, stamper.SessionID, mode, a.Now())
	var sup *process.Supervisor
	coll := collectors.New(stamper, collectors.SinkFunc(func(ev api.Event) { sup.Sink(emit).Emit(ev) }), collectors.Options{Redactor: red})
	sup, err := process.NewSupervisor(specs, coll, process.Options{
		StopGrace:    o.StopGrace,
		ExitWhenDone: exitWhenDone,
		OnChange:     func() { state.update(sup.Status()) },
	})
	if err != nil {
		if rec != nil {
			_ = rec.Close()
			closeStore(api.SessionClosed, nil)
		}
		return nil, &UsageError{Err: err}
	}
	go coll.Run()
	state.start()
	sup.Run(ctx)
	coll.Close()
	coll.Wait()
	final := sup.Status()
	state.finish(final)

	_, _, dropped := coll.Stats()
	out := &Outcome{SessionID: stamper.SessionID, Interrupted: ctx.Err() != nil, Services: final, Dropped: dropped}
	out.Failed = sup.AnyFailed()
	if rec != nil {
		_ = rec.Close() // errors are counted on the session and surfaced below
		out.Recorded, out.WriteErrors = true, rec.WriteErrors()
		out.Retention = closeStore(api.SessionClosed, final)
		if out.WriteErrors > 0 {
			warn(fmt.Sprintf("%d event(s) could not be fully stored (%v); the saved session is incomplete", out.WriteErrors, rec.Err()))
		}
	}
	return out, nil
}

// Status reports liveness and readiness. With an active session it shows
// what that session observes; otherwise it probes configured services once.
func (a *App) Status(ctx context.Context) (*api.StatusReport, error) {
	rep := &api.StatusReport{SchemaVersion: api.SchemaVersion, Services: []api.ServiceStatus{}}
	cfg, _, err := a.LoadConfig()
	if err != nil && !errors.Is(err, ErrNoConfig) {
		return nil, &UsageError{Err: err}
	}
	st, _ := readState(a.ProjectRoot)
	if st != nil && st.EndedAt.IsZero() && process.Alive(st.KestrelPID) {
		rep.Active, rep.SessionID = true, st.SessionID
		rep.Services = st.Services
		rep.Note = fmt.Sprintf("live view of %s session %s (kestrel pid %d)", st.Mode, st.SessionID, st.KestrelPID)
		return rep, nil
	}
	if st != nil && st.EndedAt.IsZero() {
		var leftovers []string
		for _, s := range st.Services {
			if s.PID > 0 && process.Alive(s.PID) {
				leftovers = append(leftovers, fmt.Sprintf("%s (pid %d)", s.Name, s.PID))
			}
		}
		rep.Note = fmt.Sprintf("session %s (kestrel pid %d) ended without shutting down cleanly", st.SessionID, st.KestrelPID)
		if len(leftovers) > 0 {
			rep.Note += "; possibly still running: " + strings.Join(leftovers, ", ") + " (Kestrel will not signal processes it no longer owns)"
		}
	} else if st != nil {
		rep.Note = fmt.Sprintf("no active session; last session %s ended %s", st.SessionID, st.EndedAt.Local().Format("2006-01-02 15:04:05"))
	}
	if cfg == nil {
		if rep.Note == "" {
			rep.Note = "no kestrel.yaml and no session yet; start one with 'kestrel run -- <command>' or 'kestrel watch'"
		}
		return rep, nil
	}
	for _, s := range cfg.Services {
		ss := api.ServiceStatus{Name: s.Name, Liveness: api.LivenessNotStarted, Readiness: api.ReadinessUnknown}
		if s.Command == "" {
			ss.Liveness = api.LivenessExternal
		}
		if s.Health != nil && s.Health.Type != config.HealthProcess {
			if pr, err := health.FromConfig(s.Health, nil); err == nil {
				r := health.Once(ctx, pr, s.Health.Timeout.D())
				ss.Probe, ss.Detail = pr.String(), r.Detail
				ss.Readiness = api.ReadinessNotReady
				if r.OK {
					ss.Readiness = api.ReadinessReady
				}
			}
		}
		rep.Services = append(rep.Services, ss)
	}
	return rep, nil
}

// displayArgv renders argv for humans, quoting arguments that need it.
func displayArgv(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\"'$`\\|&;<>()*?[]{}~#") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

// topologyOf records what diagnosis needs to know about the services: names,
// declared dependencies, and the ports their probes target.
func topologyOf(specs []process.ServiceSpec) api.Topology {
	t := api.Topology{Services: []api.ServiceTopology{}}
	for _, sp := range specs {
		st := api.ServiceTopology{Name: sp.Name, DependsOn: sp.DependsOn, External: sp.Run == nil}
		if p := probePort(sp.Health); p > 0 {
			st.Ports = []int{p}
		}
		t.Services = append(t.Services, st)
	}
	return t
}

// probePort is the port a health probe targets, or 0.
func probePort(h *config.Health) int {
	if h == nil {
		return 0
	}
	var port string
	switch h.Type {
	case config.HealthHTTP:
		u, err := url.Parse(h.URL)
		if err != nil {
			return 0
		}
		port = u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
		}
	case config.HealthTCP:
		_, port, _ = net.SplitHostPort(h.Address)
	}
	n, _ := strconv.Atoi(port)
	return n
}

func cleanServiceName(s string) string {
	s = strings.TrimSuffix(strings.ToLower(s), ".exe")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" {
		return "app"
	}
	return out
}

// Redactor returns the redactor for this project: the built-in patterns plus
// redaction.extra_patterns from a valid kestrel.yaml.
func (a *App) Redactor() (*privacy.Redactor, error) { return a.redactor() }
