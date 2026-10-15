package process

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/collectors"
	"github.com/Aareevs/Kestrel-CLI/internal/config"
	"github.com/Aareevs/Kestrel-CLI/internal/health"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// ServiceSpec is one service under supervision.
type ServiceSpec struct {
	Name string
	// Run is how to start the service; nil means the service is external:
	// Kestrel only probes it and never starts or signals it.
	Run       *Spec
	Command   string // for display only (redacted before printing)
	Health    *config.Health
	DependsOn []string
	// AutoProbe attaches a TCP readiness probe to the first port the service
	// announces in its own logs ("listening on :8080"). Used by zero-config run.
	AutoProbe bool
}

// Options configure a Supervisor.
type Options struct {
	StopGrace time.Duration // SIGTERM → SIGKILL grace (default 5s)
	// DepWaitMax caps how long a service waits for its dependencies to become
	// ready before starting anyway (default 60s).
	DepWaitMax time.Duration
	// ExitWhenDone makes Run return once every started process has exited
	// (zero-config run). Otherwise Run lasts until ctx is cancelled (watch).
	ExitWhenDone bool
	// OnChange is called after any liveness or readiness change.
	OnChange func()
}

type service struct {
	spec    ServiceSpec
	proc    *Proc
	monitor *health.Monitor
	mon     context.CancelFunc

	mu        sync.Mutex
	liveness  api.Liveness
	exit      *Exit
	started   time.Time
	changed   time.Time
	autoProbe bool
	readers   sync.WaitGroup
	pipes     []io.Closer
}

// Supervisor starts services in dependency order, collects their output,
// probes their readiness, and stops everything it started on shutdown.
type Supervisor struct {
	opts     Options
	coll     *collectors.Collector
	order    []*service
	byName   map[string]*service
	exitedCh chan struct{}
	wg       sync.WaitGroup

	mu       sync.Mutex // guards ctx and stopping
	ctx      context.Context
	stopping bool
}

// NewSupervisor validates the service graph. coll receives every event.
func NewSupervisor(specs []ServiceSpec, coll *collectors.Collector, opts Options) (*Supervisor, error) {
	if opts.StopGrace <= 0 {
		opts.StopGrace = 5 * time.Second
	}
	if opts.DepWaitMax <= 0 {
		opts.DepWaitMax = 60 * time.Second
	}
	s := &Supervisor{opts: opts, coll: coll, byName: map[string]*service{}, exitedCh: make(chan struct{}, len(specs))}
	for _, sp := range specs {
		if _, dup := s.byName[sp.Name]; dup {
			return nil, fmt.Errorf("duplicate service %q", sp.Name)
		}
		live := api.LivenessNotStarted
		if sp.Run == nil {
			live = api.LivenessExternal
		}
		s.byName[sp.Name] = &service{spec: sp, liveness: live, autoProbe: sp.AutoProbe && sp.Health == nil}
	}
	order, err := topoOrder(specs)
	if err != nil {
		return nil, err
	}
	for _, n := range order {
		s.order = append(s.order, s.byName[n])
	}
	return s, nil
}

// topoOrder returns names with dependencies first; ties keep input order.
func topoOrder(specs []ServiceSpec) ([]string, error) {
	idx := map[string]ServiceSpec{}
	for _, sp := range specs {
		idx[sp.Name] = sp
	}
	state := map[string]int{} // 0 new, 1 visiting, 2 done
	var out []string
	var visit func(n string, path []string) error
	visit = func(n string, path []string) error {
		switch state[n] {
		case 1:
			return fmt.Errorf("dependency cycle through %q", n)
		case 2:
			return nil
		}
		sp, ok := idx[n]
		if !ok {
			return fmt.Errorf("%s depends on unknown service %q", path[len(path)-1], n)
		}
		state[n] = 1
		for _, d := range sp.DependsOn {
			if err := visit(d, append(path, n)); err != nil {
				return err
			}
		}
		state[n] = 2
		out = append(out, n)
		return nil
	}
	for _, sp := range specs {
		if err := visit(sp.Name, []string{sp.Name}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Sink returns a sink that inspects events on their way to next: it attaches
// auto-probes when a service announces its port. Pass it to the collector.
func (s *Supervisor) Sink(next collectors.Sink) collectors.Sink {
	return collectors.SinkFunc(func(ev api.Event) {
		next.Emit(ev)
		if ev.Attrs.PatternID != "L001-listening" || ev.Attrs.Port == 0 {
			return
		}
		svc := s.byName[ev.ServiceID]
		if svc == nil {
			return
		}
		svc.mu.Lock()
		attach := svc.autoProbe && svc.monitor == nil
		if attach {
			svc.autoProbe = false
		}
		svc.mu.Unlock()
		if attach {
			addr := net.JoinHostPort("localhost", strconv.Itoa(ev.Attrs.Port))
			s.emit(svc, api.EventLogLine, api.SeverityInfo, fmt.Sprintf("detected listening port %d in output; probing tcp %s", ev.Attrs.Port, addr), api.Attrs{Port: ev.Attrs.Port})
			s.startMonitor(svc, &health.TCP{Address: addr}, health.Policy{
				Interval: 2 * time.Second, Timeout: time.Second, Retries: 3, StartupGrace: 0,
			})
		}
	})
}

func (s *Supervisor) emit(svc *service, typ api.EventType, sev api.Severity, msg string, attrs api.Attrs) {
	ev := api.Event{ServiceID: svc.spec.Name, Type: typ, Severity: sev, Message: msg, Source: api.SourceSupervisor, Attrs: attrs}
	if svc.proc != nil {
		ev.PID = svc.proc.PID
	}
	s.coll.Emit(ev)
}

func (s *Supervisor) changed() {
	if s.opts.OnChange != nil {
		s.opts.OnChange()
	}
}

// Run starts every service and blocks until ctx is cancelled (or, with
// ExitWhenDone, until all started processes exit). It always shuts down
// everything it started before returning.
func (s *Supervisor) Run(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	started := 0
	for _, svc := range s.order {
		if ctx.Err() != nil {
			break
		}
		s.waitForDeps(ctx, svc)
		if ctx.Err() != nil {
			break
		}
		if svc.spec.Run != nil {
			if s.start(svc) {
				started++
			}
		}
		if svc.spec.Health != nil {
			var alive func() bool
			if svc.proc != nil {
				p := svc.proc
				alive = func() bool {
					select {
					case <-p.Done():
						return false
					default:
						return true
					}
				}
			}
			if pr, err := health.FromConfig(svc.spec.Health, alive); err == nil {
				s.startMonitor(svc, pr, health.PolicyFromConfig(svc.spec.Health))
			}
		}
	}
	s.changed()

	if s.opts.ExitWhenDone {
		for i := 0; i < started && ctx.Err() == nil; i++ {
			select {
			case <-s.exitedCh:
			case <-ctx.Done():
			}
		}
	} else {
		<-ctx.Done()
	}
	s.shutdown()
}

func (s *Supervisor) start(svc *service) bool {
	p, stdout, stderr, err := Start(*svc.spec.Run)
	if err != nil {
		svc.mu.Lock()
		svc.liveness, svc.changed = api.LivenessCrashed, time.Now()
		code := -1
		svc.exit = &Exit{Code: code, Err: err}
		svc.mu.Unlock()
		s.emit(svc, api.EventProcessCrashed, api.SeverityError, fmt.Sprintf("failed to start: %v", err), api.Attrs{ExitCode: &code})
		s.exitedCh <- struct{}{}
		s.changed()
		return false
	}
	svc.mu.Lock()
	svc.proc, svc.liveness, svc.started, svc.changed = p, api.LivenessRunning, p.Started, time.Now()
	svc.pipes = []io.Closer{stdout, stderr}
	svc.mu.Unlock()
	msg := fmt.Sprintf("started (pid %d)", p.PID)
	if svc.spec.Command != "" {
		msg += ": " + svc.spec.Command
	}
	s.emit(svc, api.EventProcessStarted, api.SeverityInfo, msg, api.Attrs{})
	s.changed()

	svc.readers.Add(2)
	go func() {
		defer svc.readers.Done()
		s.coll.ReadStream(svc.spec.Name, p.PID, api.SourceStdout, stdout)
	}()
	go func() {
		defer svc.readers.Done()
		s.coll.ReadStream(svc.spec.Name, p.PID, api.SourceStderr, stderr)
	}()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		e := p.Exit()
		svc.mu.Lock()
		svc.exit, svc.changed = &e, time.Now()
		code := e.Code
		attrs := api.Attrs{ExitCode: &code, Signal: e.Signal, Requested: e.Requested, DurationMS: time.Since(p.Started).Milliseconds()}
		if e.Requested || e.Success() {
			svc.liveness = api.LivenessExited
		} else {
			svc.liveness = api.LivenessCrashed
		}
		live := svc.liveness
		svc.mu.Unlock()
		switch {
		case e.Requested:
			s.emit(svc, api.EventProcessExited, api.SeverityInfo, "stopped by Kestrel ("+e.String()+")", attrs)
		case live == api.LivenessExited:
			s.emit(svc, api.EventProcessExited, api.SeverityInfo, "exited ("+e.String()+")", attrs)
		default:
			s.emit(svc, api.EventProcessCrashed, api.SeverityError, "crashed ("+e.String()+")", attrs)
		}
		s.exitedCh <- struct{}{}
		s.changed()
	}()
	return true
}

func (s *Supervisor) startMonitor(svc *service, pr health.Prober, pol health.Policy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping || s.ctx == nil {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	m := &health.Monitor{Service: svc.spec.Name, Prober: pr, Policy: pol}
	m.OnChange = func(ch health.Change) {
		sev := api.SeverityInfo
		msg := "ready: " + pr.String() + " (" + ch.Result.Detail + ")"
		switch ch.Type {
		case api.EventProbeFailed:
			sev, msg = api.SeverityError, fmt.Sprintf("not ready: %s failed %d times (%s)", pr, ch.Fails, ch.Result.Detail)
		case api.EventStartupTimeout:
			sev, msg = api.SeverityError, fmt.Sprintf("never became ready: %s (%s)", pr, ch.Result.Detail)
		case api.EventProbeRecovered:
			msg = "recovered: " + pr.String() + " (" + ch.Result.Detail + ")"
		}
		s.emit(svc, ch.Type, sev, msg, api.Attrs{HTTPStatus: ch.Result.HTTPStatus, DurationMS: ch.Result.Latency.Milliseconds()})
		svc.mu.Lock()
		svc.changed = time.Now()
		svc.mu.Unlock()
		s.changed()
	}
	svc.mu.Lock()
	svc.monitor, svc.mon = m, cancel
	svc.mu.Unlock()
	s.wg.Add(1)
	go func() { defer s.wg.Done(); m.Run(ctx) }()
}

// waitForDeps waits until each dependency with a probe is ready, its process
// has exited, or the wait budget runs out. A service is started anyway after
// that, so downstream symptoms of an upstream failure are still observed.
func (s *Supervisor) waitForDeps(ctx context.Context, svc *service) {
	for _, name := range svc.spec.DependsOn {
		dep := s.byName[name]
		if dep.spec.Health == nil {
			continue
		}
		pol := health.PolicyFromConfig(dep.spec.Health)
		budget := pol.StartupGrace + time.Duration(pol.Retries+1)*pol.Interval
		if budget > s.opts.DepWaitMax {
			budget = s.opts.DepWaitMax
		}
		deadline := time.Now().Add(budget)
		for {
			state, last := s.readiness(dep)
			if state == api.ReadinessReady {
				break
			}
			dep.mu.Lock()
			gone := dep.liveness == api.LivenessCrashed || dep.liveness == api.LivenessExited
			dep.mu.Unlock()
			if gone || state == api.ReadinessNotReady || time.Now().After(deadline) {
				why := "did not become ready within " + budget.String()
				switch {
				case gone:
					why = "has exited"
				case state == api.ReadinessNotReady:
					why = "is not ready (" + last + ")"
				}
				s.emit(svc, api.EventLogLine, api.SeverityWarn, fmt.Sprintf("starting anyway: dependency %s %s", name, why), api.Attrs{Target: name})
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}

func (s *Supervisor) readiness(svc *service) (api.Readiness, string) {
	svc.mu.Lock()
	m := svc.monitor
	svc.mu.Unlock()
	if m == nil {
		return api.ReadinessUnknown, ""
	}
	st, r, _ := m.State()
	return st, r.Detail
}

// shutdown stops probes, then every process group Kestrel started (in
// parallel, each with the grace period), then drains the readers.
func (s *Supervisor) shutdown() {
	s.mu.Lock()
	s.stopping = true // no new monitors from here on
	s.mu.Unlock()
	for _, svc := range s.order {
		svc.mu.Lock()
		if svc.mon != nil {
			svc.mon()
		}
		svc.mu.Unlock()
	}
	var stops sync.WaitGroup
	for _, svc := range s.order {
		if svc.proc == nil {
			continue
		}
		stops.Add(1)
		go func(p *Proc) { defer stops.Done(); p.Stop(s.opts.StopGrace) }(svc.proc)
	}
	stops.Wait()
	s.wg.Wait()
	for _, svc := range s.order {
		done := make(chan struct{})
		go func() { svc.readers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			// A process that left our group may still hold the pipe; stop
			// reading rather than hang.
			for _, c := range svc.pipes {
				c.Close()
			}
			<-done
		}
	}
	s.changed()
}

// Status reports liveness and readiness for every service.
func (s *Supervisor) Status() []api.ServiceStatus {
	out := make([]api.ServiceStatus, 0, len(s.order))
	for _, svc := range s.order {
		st := api.ServiceStatus{Name: svc.spec.Name, Readiness: api.ReadinessUnknown}
		svc.mu.Lock()
		st.Liveness, st.StartedAt, st.LastChange = svc.liveness, svc.started, svc.changed
		if svc.proc != nil {
			st.PID = svc.proc.PID
		}
		if svc.exit != nil {
			code := svc.exit.Code
			st.ExitCode = &code
			st.Detail = svc.exit.String()
		}
		m, exit := svc.monitor, svc.exit
		svc.mu.Unlock()
		if m != nil {
			r, res, _ := m.State()
			st.Readiness, st.Probe = r, m.Prober.String()
			if res.Detail != "" && exit == nil {
				st.Detail = res.Detail
			}
		}
		// A probe result from before the process ended says nothing about now.
		if svc.spec.Run != nil && exit != nil && st.Readiness != api.ReadinessUnknown {
			if exit.Requested {
				st.Readiness, st.Detail = api.ReadinessUnknown, "stopped by Kestrel"
			} else {
				st.Readiness = api.ReadinessNotReady
			}
		}
		out = append(out, st)
	}
	return out
}

// AnyFailed reports whether a process crashed or a probe ended not ready.
func (s *Supervisor) AnyFailed() bool {
	for _, st := range s.Status() {
		if st.Liveness == api.LivenessCrashed {
			return true
		}
	}
	return false
}
