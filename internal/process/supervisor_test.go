//go:build !windows

package process

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/collectors"
	"github.com/Aareevs/Kestrel-CLI/internal/config"
	"github.com/Aareevs/Kestrel-CLI/internal/events"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

var fakesvc string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kestrel-fakesvc")
	if err != nil {
		panic(err)
	}
	fakesvc = filepath.Join(dir, "fakesvc")
	if out, err := exec.Command("go", "build", "-o", fakesvc, "../../tests/fakesvc").CombinedOutput(); err != nil {
		panic(fmt.Sprintf("building fakesvc: %v\n%s", err, out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

type evlog struct {
	mu  sync.Mutex
	evs []api.Event
}

func (l *evlog) Emit(e api.Event) { l.mu.Lock(); l.evs = append(l.evs, e); l.mu.Unlock() }

func (l *evlog) find(svc string, typ api.EventType) (int, *api.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.evs {
		if l.evs[i].ServiceID == svc && l.evs[i].Type == typ {
			e := l.evs[i]
			return i, &e
		}
	}
	return -1, nil
}

func (l *evlog) dump() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var b strings.Builder
	for _, e := range l.evs {
		fmt.Fprintf(&b, "%d %s %s %s %s\n", e.Seq, e.ServiceID, e.Type, e.Severity, e.Message)
	}
	return b.String()
}

// harness runs a supervisor with a collector feeding an evlog.
func harness(t *testing.T, specs []ServiceSpec, opts Options) (*Supervisor, *evlog, context.CancelFunc, chan struct{}) {
	t.Helper()
	log := &evlog{}
	st := events.NewStamper(nil)
	var sup *Supervisor
	sink := collectors.SinkFunc(func(e api.Event) { sup.Sink(log).Emit(e) })
	coll := collectors.New(st, sink, collectors.Options{})
	var err error
	sup, err = NewSupervisor(specs, coll, opts)
	if err != nil {
		t.Fatal(err)
	}
	go coll.Run()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sup.Run(ctx)
		coll.Close()
		coll.Wait()
		close(done)
	}()
	return sup, log, cancel, done
}

func waitEvent(t *testing.T, log *evlog, svc string, typ api.EventType) *api.Event {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := log.find(svc, typ); e != nil {
			return e
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no %s/%s event; got:\n%s", svc, typ, log.dump())
	return nil
}

func fast(h *config.Health) *config.Health {
	h.Interval = config.Duration(50 * time.Millisecond)
	h.Timeout = config.Duration(40 * time.Millisecond)
	h.Retries = 2
	h.StartupGrace = config.Duration(3 * time.Second)
	if h.Type == config.HealthHTTP {
		h.ExpectStatus = "200-399"
	}
	return h
}

func TestDependencyOrderAndReadiness(t *testing.T) {
	dbPort, apiPort := freePort(t), freePort(t)
	dbAddr := fmt.Sprintf("127.0.0.1:%d", dbPort)
	specs := []ServiceSpec{
		// Declared first but depends on db: must start second.
		{Name: "api", DependsOn: []string{"db"},
			Run:    &Spec{Argv: []string{fakesvc, "--name", "api", "--listen", fmt.Sprintf("127.0.0.1:%d", apiPort), "--connect", dbAddr}},
			Health: fast(&config.Health{Type: "http", URL: fmt.Sprintf("http://127.0.0.1:%d/health", apiPort)})},
		{Name: "db",
			Run:    &Spec{Argv: []string{fakesvc, "--name", "db", "--listen", dbAddr, "--listen-after", "300ms"}},
			Health: fast(&config.Health{Type: "tcp", Address: dbAddr})},
	}
	sup, log, cancel, done := harness(t, specs, Options{StopGrace: time.Second})
	waitEvent(t, log, "api", api.EventProbeReady)

	dbReady, _ := log.find("db", api.EventProbeReady)
	apiStart, _ := log.find("api", api.EventProcessStarted)
	if dbReady < 0 || apiStart < dbReady {
		t.Fatalf("api must start after db is ready:\n%s", log.dump())
	}
	for _, st := range sup.Status() {
		if st.Liveness != api.LivenessRunning || st.Readiness != api.ReadinessReady {
			t.Errorf("status %+v", st)
		}
	}
	cancel()
	<-done
	for _, name := range []string{"db", "api"} {
		_, e := log.find(name, api.EventProcessExited)
		if e == nil || !e.Attrs.Requested {
			t.Errorf("%s: expected a requested exit event:\n%s", name, log.dump())
		}
	}
}

func TestCrashIsRecordedAndDependentsStillStart(t *testing.T) {
	specs := []ServiceSpec{
		{Name: "db", Run: &Spec{Argv: []string{fakesvc, "--name", "db", "--exit-after", "100ms", "--exit-code", "1", "--exit-message", "FATAL: data directory has wrong ownership"}},
			Health: fast(&config.Health{Type: "tcp", Address: fmt.Sprintf("127.0.0.1:%d", freePort(t))})},
		{Name: "api", DependsOn: []string{"db"}, Run: &Spec{Argv: []string{fakesvc, "--name", "api", "--exit-after", "300ms"}}},
	}
	sup, log, cancel, done := harness(t, specs, Options{ExitWhenDone: true, StopGrace: time.Second})
	defer cancel()
	<-done
	crash := waitEvent(t, log, "db", api.EventProcessCrashed)
	if crash.Attrs.ExitCode == nil || *crash.Attrs.ExitCode != 1 || crash.Severity != api.SeverityError {
		t.Fatalf("crash event: %+v", crash)
	}
	if _, e := log.find("api", api.EventProcessStarted); e == nil {
		t.Fatalf("api should start even though its dependency failed:\n%s", log.dump())
	}
	if !strings.Contains(log.dump(), "starting anyway: dependency db") {
		t.Fatalf("expected a starting-anyway warning:\n%s", log.dump())
	}
	if !sup.AnyFailed() {
		t.Fatal("AnyFailed should be true")
	}
}

func TestAutoProbeFromLogs(t *testing.T) {
	port := freePort(t)
	specs := []ServiceSpec{{Name: "web", AutoProbe: true,
		Run: &Spec{Argv: []string{fakesvc, "--name", "web", "--listen", fmt.Sprintf("127.0.0.1:%d", port)}}}}
	sup, log, cancel, done := harness(t, specs, Options{StopGrace: time.Second})
	waitEvent(t, log, "web", api.EventProbeReady)
	if st := sup.Status()[0]; st.Readiness != api.ReadinessReady || !strings.Contains(st.Probe, fmt.Sprint(port)) {
		t.Fatalf("status %+v", st)
	}
	cancel()
	<-done
}

func TestExternalServiceIsOnlyProbed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	specs := []ServiceSpec{{Name: "redis", Health: fast(&config.Health{Type: "tcp", Address: ln.Addr().String()})}}
	sup, log, cancel, done := harness(t, specs, Options{})
	waitEvent(t, log, "redis", api.EventProbeReady)
	if st := sup.Status()[0]; st.Liveness != api.LivenessExternal || st.PID != 0 {
		t.Fatalf("status %+v", st)
	}
	cancel()
	<-done
	if _, e := log.find("redis", api.EventProcessExited); e != nil {
		t.Fatal("external services are never started or stopped")
	}
}

func TestShutdownLeavesNoOrphans(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "pids")
	specs := []ServiceSpec{
		{Name: "a", Run: &Spec{Argv: []string{fakesvc, "--children", "2", "--pidfile", pidfile}}},
		{Name: "b", Run: &Spec{Argv: []string{fakesvc, "--children", "1", "--ignore-term", "--pidfile", pidfile}}},
		{Name: "c", Run: &Spec{Shell: fakesvc + " --pidfile " + pidfile}},
	}
	_, _, cancel, done := harness(t, specs, Options{StopGrace: 300 * time.Millisecond})
	pids := readPIDs(t, pidfile, 6) // a + 2 children, b + 1 child, c
	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown hung")
	}
	// b ignores SIGTERM, so shutdown must have waited the grace and escalated.
	if el := time.Since(start); el < 300*time.Millisecond {
		t.Fatalf("shutdown took %v; b should have needed SIGKILL after the 300ms grace", el)
	}
	assertAllGone(t, pids)
}

func TestNewSupervisorRejectsBadGraphs(t *testing.T) {
	coll := collectors.New(events.NewStamper(nil), collectors.SinkFunc(func(api.Event) {}), collectors.Options{})
	if _, err := NewSupervisor([]ServiceSpec{{Name: "a", DependsOn: []string{"b"}}, {Name: "b", DependsOn: []string{"a"}}}, coll, Options{}); err == nil {
		t.Error("cycle should fail")
	}
	if _, err := NewSupervisor([]ServiceSpec{{Name: "a", DependsOn: []string{"zzz"}}}, coll, Options{}); err == nil {
		t.Error("unknown dependency should fail")
	}
	if _, err := NewSupervisor([]ServiceSpec{{Name: "a"}, {Name: "a"}}, coll, Options{}); err == nil {
		t.Error("duplicate should fail")
	}
}
