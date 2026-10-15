package correlation

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/graph"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// stream builds events with an injected clock: each helper call advances
// time by the given offset from t0.
type stream struct {
	t0  time.Time
	seq uint64
	evs []api.Event
}

func newStream() *stream { return &stream{t0: time.Date(2026, 10, 1, 10, 32, 0, 0, time.UTC)} }

func (s *stream) at(ms int, svc string, typ api.EventType, sev api.Severity, msg string, attrs api.Attrs) api.Event {
	s.seq++
	e := api.Event{ID: fmt.Sprintf("E%03d", s.seq), Seq: s.seq, Time: s.t0.Add(time.Duration(ms) * time.Millisecond),
		ServiceID: svc, Type: typ, Severity: sev, Message: msg, Attrs: attrs, Fingerprint: "fp-" + typeKey(typ, msg)}
	s.evs = append(s.evs, e)
	return e
}

func typeKey(t api.EventType, msg string) string {
	// Numbers don't matter for repetition, like the real fingerprint.
	return string(t) + strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return -1
		}
		return r
	}, msg)
}

func (s *stream) started(ms int, svc string) {
	s.at(ms, svc, api.EventProcessStarted, api.SeverityInfo, "started", api.Attrs{})
}
func (s *stream) ready(ms int, svc string) {
	s.at(ms, svc, api.EventProbeReady, api.SeverityInfo, "ready", api.Attrs{})
}
func (s *stream) crash(ms int, svc string, code int) {
	s.at(ms, svc, api.EventProcessCrashed, api.SeverityError, fmt.Sprintf("crashed (exit code %d)", code), api.Attrs{ExitCode: &code})
}
func (s *stream) refused(ms int, svc string, port int) {
	s.at(ms, svc, api.EventDependencyConnFailed, api.SeverityError, fmt.Sprintf("dial tcp 127.0.0.1:%d: connect: connection refused", port), api.Attrs{Port: port, PatternID: "P006-connection-refused"})
}
func (s *stream) warn(ms int, svc, msg string) {
	s.at(ms, svc, api.EventLogLine, api.SeverityWarn, msg, api.Attrs{})
}

var topo3 = api.Topology{Services: []api.ServiceTopology{
	{Name: "db", Ports: []int{5432}},
	{Name: "api", DependsOn: []string{"db"}, Ports: []int{8080}},
	{Name: "web", DependsOn: []string{"api"}, Ports: []int{5173}},
}}

func roles(in *Incident) string {
	var b strings.Builder
	for _, e := range in.Timeline {
		fmt.Fprintf(&b, "%s/%s/%s×%d ", e.Service, e.Role, e.Type, e.Count)
	}
	return strings.TrimSpace(b.String())
}

func TestNoisyButHealthyHasNoIncident(t *testing.T) {
	s := newStream()
	s.started(0, "api")
	s.ready(100, "api")
	for i := 0; i < 50; i++ {
		s.warn(200+i*100, "api", fmt.Sprintf("WARN slow query took %dms", 1000+i))
	}
	s.at(6000, "api", api.EventCollectorOverload, api.SeverityWarn, "dropped", api.Attrs{Dropped: 3})
	s.at(6100, "api", api.EventProcessExited, api.SeverityInfo, "stopped by Kestrel", api.Attrs{Requested: true})
	if inc := Correlate(s.evs, graph.Build(topo3, s.evs), 0); len(inc) != 0 {
		t.Fatalf("warnings, overload, and requested stops must not create incidents: %d", len(inc))
	}
}

func TestDependencyChainIsOneStrongIncident(t *testing.T) {
	s := newStream()
	s.started(0, "db")
	s.at(300, "db", api.EventLogLine, api.SeverityFatal, "FATAL: database files are incompatible with server", api.Attrs{})
	s.crash(310, "db", 1)
	s.started(400, "api")
	for i := 0; i < 14; i++ {
		s.refused(500+i*100, "api", 5432)
	}
	s.at(2600, "api", api.EventStartupTimeout, api.SeverityError, "never became ready", api.Attrs{})
	s.started(2700, "web")
	s.ready(2800, "web")
	s.refused(2900, "web", 8080)
	s.refused(3000, "web", 8080)

	g := graph.Build(topo3, s.evs)
	inc := Correlate(s.evs, g, 0)
	if len(inc) != 1 {
		t.Fatalf("incidents: %d", len(inc))
	}
	in := inc[0]
	if in.CorrelationStrength != api.CorrelationStrong || !in.InitiatingKnown || in.InitiatingService != "db" || in.InitiatingEventID != "E002" {
		t.Fatalf("incident: strength=%s known=%v svc=%s evt=%s roots=%v", in.CorrelationStrength, in.InitiatingKnown, in.InitiatingService, in.InitiatingEventID, in.Roots)
	}
	if strings.Join(in.Services, ",") != "api,db,web" {
		t.Fatalf("services %v", in.Services)
	}
	// Last-healthy entries come only from before the first anomaly (api and web
	// started later), and web becoming ready before failing is not a recovery.
	want := "db/last_healthy/process_started×1 db/first_anomaly/log_line×1 db/key/process_crashed×1 " +
		"api/downstream_symptom/dependency_conn_failed×14 api/downstream_symptom/startup_timeout×1 web/downstream_symptom/dependency_conn_failed×2"
	if got := roles(in); got != want {
		t.Fatalf("timeline:\n got  %s\n want %s", got, want)
	}
	if c := in.Timeline[3]; c.Claim != api.ClaimCorrelated || c.Until.IsZero() || !c.Until.After(c.Time) {
		t.Fatalf("collapsed symptom entry: %+v", c)
	}
}

func TestSimultaneousIndependentFailuresAreUnresolved(t *testing.T) {
	topo := api.Topology{Services: []api.ServiceTopology{{Name: "billing"}, {Name: "search"}}}
	s := newStream()
	s.started(0, "billing")
	s.started(0, "search")
	s.crash(1000, "billing", 1)
	s.crash(1050, "search", 2)
	inc := Correlate(s.evs, graph.Build(topo, s.evs), 0)
	if len(inc) != 1 {
		t.Fatalf("simultaneous failures should be looked at together: %d incidents", len(inc))
	}
	in := inc[0]
	if in.CorrelationStrength != api.CorrelationWeak || in.InitiatingKnown || len(in.Roots) != 2 || !strings.Contains(in.InitiatingNote, "can't be determined") {
		t.Fatalf("incident: %+v", in.Incident)
	}
	for _, e := range in.Timeline {
		if e.Role == api.RoleSymptom {
			t.Fatalf("with no known initiator nothing is labeled a downstream symptom: %+v", e)
		}
	}
}

func TestQuietGapSplitsIncidents(t *testing.T) {
	s := newStream()
	s.crash(0, "db", 1)
	s.crash(20000, "api", 1) // 20s later, beyond the default 10s window
	if inc := Correlate(s.evs, graph.Build(topo3, s.evs), 0); len(inc) != 2 || inc[0].Number != 1 || inc[1].Number != 2 {
		t.Fatalf("expected two incidents, got %d", len(inc))
	}
	if inc := Correlate(s.evs, graph.Build(topo3, s.evs), 30*time.Second); len(inc) != 1 {
		t.Fatalf("a 30s window joins them: %d", len(inc))
	}
}

func TestEarlierDownstreamFailureMakesTwoRoots(t *testing.T) {
	// api fails first, then db: api is not explained by db (wrong order).
	s := newStream()
	s.refused(0, "api", 5432)
	s.crash(500, "db", 1)
	in := Correlate(s.evs, graph.Build(topo3, s.evs), 0)[0]
	if in.InitiatingKnown || len(in.Roots) != 2 {
		t.Fatalf("roots %v known %v", in.Roots, in.InitiatingKnown)
	}
}

func TestRecoveryAndRepeatSummaries(t *testing.T) {
	s := newStream()
	s.ready(0, "api")
	s.at(1000, "api", api.EventProbeFailed, api.SeverityError, "not ready", api.Attrs{})
	s.refused(1100, "api", 5432)
	s.at(3000, "api", api.EventDependencyConnFailed, api.SeverityError, "dial tcp 127.0.0.1:5432: connect: connection refused", api.Attrs{Port: 5432, Repeats: 9, DurationMS: 1800})
	s.at(4000, "api", api.EventProbeRecovered, api.SeverityInfo, "recovered", api.Attrs{})
	in := Correlate(s.evs, graph.Build(topo3, s.evs), 0)[0]
	got := roles(in)
	want := "api/last_healthy/probe_ready×1 api/first_anomaly/probe_failed×1 api/key/dependency_conn_failed×11 api/recovery/probe_recovered×1"
	if got != want {
		t.Fatalf("timeline:\n got  %s\n want %s", got, want)
	}
}
