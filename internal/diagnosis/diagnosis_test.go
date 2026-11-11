package diagnosis

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/correlation"
	"github.com/Aareevs/Kestrel-CLI/internal/diagnosis/rules"
	"github.com/Aareevs/Kestrel-CLI/internal/events"
	"github.com/Aareevs/Kestrel-CLI/internal/graph"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// stream builds a deterministic session with an injected clock.
type stream struct {
	t0  time.Time
	seq uint64
	evs []api.Event
}

func newStream() *stream { return &stream{t0: time.Date(2026, 10, 1, 10, 32, 0, 0, time.UTC)} }

func (s *stream) add(ms int, svc string, typ api.EventType, sev api.Severity, msg string, a api.Attrs) {
	s.seq++
	s.evs = append(s.evs, api.Event{ID: fmt.Sprintf("01EVT%021d", s.seq), Seq: s.seq, Time: s.t0.Add(time.Duration(ms) * time.Millisecond),
		ServiceID: svc, Type: typ, Severity: sev, Message: msg, Attrs: a, Fingerprint: events.Fingerprint(msg)})
}

// line classifies a raw log line the way the collector would.
func (s *stream) line(ms int, svc, msg string) {
	c := events.Classify(msg)
	s.add(ms, svc, c.Type, c.Severity, msg, api.Attrs{PatternID: c.PatternID, Port: c.Port, Target: c.Target})
}
func (s *stream) started(ms int, svc string) {
	s.add(ms, svc, api.EventProcessStarted, api.SeverityInfo, "started", api.Attrs{})
}
func (s *stream) ready(ms int, svc string) {
	s.add(ms, svc, api.EventProbeReady, api.SeverityInfo, "ready: probe passing", api.Attrs{})
}
func (s *stream) crash(ms int, svc string, code int) {
	s.add(ms, svc, api.EventProcessCrashed, api.SeverityError, fmt.Sprintf("crashed (exit code %d)", code), api.Attrs{ExitCode: &code})
}
func (s *stream) timeout(ms int, svc string) {
	s.add(ms, svc, api.EventStartupTimeout, api.SeverityError, "never became ready", api.Attrs{})
}

// diagnose correlates the stream and diagnoses every incident.
func diagnose(t *testing.T, topo api.Topology, s *stream) []api.IncidentDiagnosis {
	t.Helper()
	g := graph.Build(topo, s.evs)
	var out []api.IncidentDiagnosis
	for _, in := range correlation.Correlate(s.evs, g, 0) {
		tail := map[string][]api.Event{}
		for svc, first := range in.First {
			for _, e := range s.evs {
				if e.ServiceID == svc && e.Seq < first.Seq+20 {
					tail[svc] = append(tail[svc], e)
				}
			}
		}
		d := Diagnose(&rules.Context{Incident: in, Graph: g, Events: s.evs, Tail: tail, GOOS: "linux"})
		checkInvariants(t, d)
		out = append(out, d)
	}
	return out
}

// checkInvariants: every hypothesis cites evidence, ranks are 1..n, levels valid.
func checkInvariants(t *testing.T, d api.IncidentDiagnosis) {
	t.Helper()
	for i, h := range d.Hypotheses {
		if len(h.Supporting) == 0 {
			t.Fatalf("hypothesis %q has no evidence", h.Title)
		}
		for _, e := range h.Supporting {
			if e.EventID == "" {
				t.Fatalf("hypothesis %q cites evidence without an event ID", h.Title)
			}
		}
		if h.Rank != i+1 || h.Claim != api.ClaimHypothesis || h.Confidence.Level == "" || len(h.Confidence.Factors) == 0 || h.NextStep.Description == "" {
			t.Fatalf("malformed hypothesis: %+v", h)
		}
	}
}

func ruleIDs(d api.IncidentDiagnosis) string {
	var ids []string
	for _, h := range d.Hypotheses {
		ids = append(ids, fmt.Sprintf("%s:%s[%s]", h.RuleID, h.Confidence.Level, h.Title))
	}
	return strings.Join(ids, " ")
}

var chain = api.Topology{Services: []api.ServiceTopology{
	{Name: "database", Ports: []int{5432}},
	{Name: "api", DependsOn: []string{"database"}, Ports: []int{8080}},
	{Name: "frontend", DependsOn: []string{"api"}, Ports: []int{5173}},
}}

// Scenario A in miniature: the database fails to initialize; api and
// frontend fail after it.
func TestDatabaseStartupFailureLeadsWithDatabase(t *testing.T) {
	s := newStream()
	s.started(0, "database")
	s.line(200, "database", "FATAL:  database files are incompatible with server")
	s.crash(210, "database", 1)
	s.started(300, "api")
	for i := 0; i < 14; i++ {
		s.line(400+i*100, "api", "dial tcp 127.0.0.1:5432: connect: connection refused")
	}
	s.timeout(1900, "api")
	s.started(2000, "frontend")
	s.ready(2100, "frontend")
	s.line(2200, "frontend", "Error: connect ECONNREFUSED 127.0.0.1:8080")

	ds := diagnose(t, chain, s)
	if len(ds) != 1 {
		t.Fatalf("incidents: %d", len(ds))
	}
	d := ds[0]
	if d.Status != api.ClaimHypothesis {
		t.Fatalf("status %s", d.Status)
	}
	top := d.Hypotheses[0]
	if top.RuleID != "K002-dependency-down" || top.Confidence.Level != api.ConfidenceHigh || top.Services[0] != "database" ||
		!strings.Contains(top.Title, "database failed first; api, frontend failed because of it") {
		t.Fatalf("top hypothesis: %s / %s / %s\nall: %s", top.RuleID, top.Confidence.Level, top.Title, ruleIDs(d))
	}
	if len(top.Contradicting) != 0 {
		t.Fatalf("no contradictions expected: %+v", top.Contradicting)
	}
	// The repeated refusals are one collapsed evidence line.
	found := false
	for _, e := range top.Supporting {
		if e.Service == "api" && e.Count == 14 && strings.Contains(e.Summary, "connection refused to 127.0.0.1:5432") {
			found = true
		}
	}
	if !found {
		t.Fatalf("collapsed api evidence missing: %+v", top.Supporting)
	}
	// The database's own startup failure is also proposed, high, from its FATAL line.
	second := d.Hypotheses[1]
	if second.RuleID != "K004-startup-failure" || second.Confidence.Level != api.ConfidenceHigh || !strings.Contains(second.Title, "database failed during startup: FATAL") {
		t.Fatalf("second: %s %s %s", second.RuleID, second.Confidence.Level, second.Title)
	}
	// api's own startup failure is recognized as probably downstream.
	for _, h := range d.Hypotheses {
		if h.RuleID == "K004-startup-failure" && h.Services[0] == "api" {
			if h.Confidence.Level != api.ConfidenceLow || len(h.Contradicting) == 0 {
				t.Fatalf("api's startup failure should be low and contradicted: %+v", h)
			}
		}
	}
	for _, h := range d.Hypotheses {
		if h.RuleID == "K005-dependency-not-running" {
			t.Fatalf("every refused port here belongs to a supervised service; K005 must stay quiet: %q", h.Title)
		}
	}
	if d.NextStep.Command != "kestrel timeline --service database" {
		t.Fatalf("next step: %+v", d.NextStep)
	}
	if len(d.Symptoms) != 2 || d.Symptoms[0].Service != "api" || d.Symptoms[0].Claim != api.ClaimCorrelated {
		t.Fatalf("symptoms: %+v", d.Symptoms)
	}
}

func TestPortConflict(t *testing.T) {
	s := newStream()
	s.started(0, "web")
	s.line(100, "web", "Error: listen EADDRINUSE: address already in use :::3000")
	s.crash(120, "web", 1)
	d := diagnose(t, api.Topology{Services: []api.ServiceTopology{{Name: "web"}}}, s)[0]
	top := d.Hypotheses[0]
	if top.RuleID != "K001-port-conflict" || top.Confidence.Level != api.ConfidenceHigh || !strings.Contains(top.Title, "port 3000 is already in use") {
		t.Fatalf("top: %+v\nall: %s", top, ruleIDs(d))
	}
	if top.NextStep.Command != "lsof -nP -iTCP:3000 -sTCP:LISTEN" || !strings.Contains(top.NextStep.Description, "never signals processes it didn't start") {
		t.Fatalf("next step: %+v", top.NextStep)
	}
	for _, h := range d.Hypotheses {
		if h.RuleID == "K004-startup-failure" {
			t.Fatal("K004 must defer to the more precise K001")
		}
	}
	// Windows gets a Windows command.
	g := graph.Build(api.Topology{}, s.evs)
	in := correlation.Correlate(s.evs, g, 0)[0]
	h := rules.PortConflict{}.Evaluate(&rules.Context{Incident: in, Graph: g, Events: s.evs, GOOS: "windows"})[0]
	if h.NextStep.Command != "netstat -ano | findstr :3000" {
		t.Fatalf("windows command: %s", h.NextStep.Command)
	}
}

func TestPortConflictContradictedByLaterReadiness(t *testing.T) {
	s := newStream()
	s.line(100, "web", "Port 5173 is in use, trying another one...")
	s.ready(400, "web")
	d := diagnose(t, api.Topology{}, s)[0]
	h := d.Hypotheses[0]
	if h.RuleID != "K001-port-conflict" || h.Confidence.Level != api.ConfidenceLow || len(h.Contradicting) != 1 || d.Status != api.ClaimUnresolved {
		t.Fatalf("got %s %s contradicting=%d status=%s", h.RuleID, h.Confidence.Level, len(h.Contradicting), d.Status)
	}
}

func TestCrashAfterHealthyStart(t *testing.T) {
	s := newStream()
	s.started(0, "worker")
	s.ready(300, "worker")
	s.line(2000, "worker", "processing job 42")
	s.line(2100, "worker", "panic: runtime error: invalid memory address or nil pointer dereference")
	s.crash(2110, "worker", 2)
	d := diagnose(t, api.Topology{}, s)[0]
	h := d.Hypotheses[0]
	if h.RuleID != "K003-crash-after-start" || h.Confidence.Level != api.ConfidenceHigh || !strings.Contains(h.Title, "panic: runtime error") {
		t.Fatalf("got %s %s %q", h.RuleID, h.Confidence.Level, h.Title)
	}
	// Facts and possibilities stay separate in the explanation.
	if !strings.HasPrefix(h.Explanation, "Observed: worker was ready at") || !strings.Contains(h.Explanation, "Hypothesis:") {
		t.Fatalf("explanation: %s", h.Explanation)
	}
	if len(h.Supporting) != 3 { // ready, panic line, crash
		t.Fatalf("evidence: %+v", h.Supporting)
	}
	for _, other := range d.Hypotheses {
		if other.RuleID == "K004-startup-failure" {
			t.Fatal("a service that was ready did not fail during startup")
		}
	}
}

func TestCrashAfterStartWithoutVisibleCause(t *testing.T) {
	s := newStream()
	s.ready(100, "worker")
	s.line(500, "worker", "all good")
	s.crash(600, "worker", 137)
	d := diagnose(t, api.Topology{}, s)[0]
	h := d.Hypotheses[0]
	if h.RuleID != "K003-crash-after-start" || h.Confidence.Level != api.ConfidenceLow || !strings.Contains(h.Title, "cause not visible") || d.Status != api.ClaimUnresolved {
		t.Fatalf("got %s %s %q status=%s", h.RuleID, h.Confidence.Level, h.Title, d.Status)
	}
}

func TestAmbiguousSimultaneousFailures(t *testing.T) {
	s := newStream()
	s.started(0, "billing")
	s.started(0, "search")
	s.line(900, "billing", "ERROR: could not load pricing table")
	s.crash(950, "billing", 1)
	s.line(980, "search", "ERROR: index directory is locked")
	s.crash(1000, "search", 1)
	d := diagnose(t, api.Topology{Services: []api.ServiceTopology{{Name: "billing"}, {Name: "search"}}}, s)[0]
	if d.Status != api.ClaimUnresolved || d.Incident.InitiatingKnown || d.Incident.CorrelationStrength != api.CorrelationWeak {
		t.Fatalf("status=%s known=%v strength=%s", d.Status, d.Incident.InitiatingKnown, d.Incident.CorrelationStrength)
	}
	if len(d.Hypotheses) != 2 || d.Hypotheses[0].Confidence.Level != d.Hypotheses[1].Confidence.Level {
		t.Fatalf("expected two competing hypotheses of equal confidence: %s", ruleIDs(d))
	}
	if d.Hypotheses[0].Services[0] == d.Hypotheses[1].Services[0] {
		t.Fatal("competing hypotheses should be about different services")
	}
	if !strings.Contains(d.NextStep.Description, "failed independently") || d.NextStep.Command != "kestrel timeline --service billing" {
		t.Fatalf("lead next step: %+v", d.NextStep)
	}
	if len(d.Symptoms) != 0 {
		t.Fatal("nothing is a downstream symptom when the initiator is unknown")
	}
}

func TestDependencyNotRunning(t *testing.T) {
	s := newStream()
	s.started(0, "api")
	for i := 0; i < 5; i++ {
		s.line(100+i*100, "api", "dial tcp 127.0.0.1:6379: connect: connection refused")
	}
	d := diagnose(t, api.Topology{Services: []api.ServiceTopology{{Name: "api"}}}, s)[0]
	h := d.Hypotheses[0]
	if h.RuleID != "K005-dependency-not-running" || h.Confidence.Level != api.ConfidenceMedium || !strings.Contains(h.Title, "127.0.0.1:6379 isn't running") || h.NextStep.Command != "lsof -nP -iTCP:6379 -sTCP:LISTEN" {
		t.Fatalf("got %+v", h)
	}
	if h.Supporting[0].Count != 5 {
		t.Fatalf("collapsed count: %+v", h.Supporting)
	}
}

func TestExternalDependencyDown(t *testing.T) {
	topo := api.Topology{Services: []api.ServiceTopology{{Name: "api"}, {Name: "redis", External: true, Ports: []int{6379}}}}
	s := newStream()
	s.add(0, "redis", api.EventStartupTimeout, api.SeverityError, "never became ready", api.Attrs{})
	s.line(100, "api", "Could not connect to Redis at 127.0.0.1:6379: Connection refused")
	d := diagnose(t, topo, s)[0]
	var h *api.Hypothesis
	for i := range d.Hypotheses {
		if d.Hypotheses[i].RuleID == "K005-dependency-not-running" {
			h = &d.Hypotheses[i]
		}
	}
	if h == nil || h.Confidence.Level != api.ConfidenceHigh || !strings.Contains(h.Title, "which Kestrel doesn't start") {
		t.Fatalf("got %s", ruleIDs(d))
	}
}

func TestMissingModule(t *testing.T) {
	s := newStream()
	s.started(0, "web")
	s.line(100, "web", "Error: Cannot find module 'express'")
	s.crash(120, "web", 1)
	d := diagnose(t, api.Topology{}, s)[0]
	found := false
	for _, h := range d.Hypotheses {
		if h.RuleID == "K006-missing-module" && h.Confidence.Level == api.ConfidenceHigh {
			found = true
		}
	}
	if !found {
		t.Fatalf("got %s", ruleIDs(d))
	}
}

// Java and Kotlin: Spring Boot's own wording for a taken port is recognized, so
// the port-conflict diagnosis (and its lsof next step) works for JVM services.
func TestSpringPortConflict(t *testing.T) {
	s := newStream()
	s.started(0, "orders")
	s.line(100, "orders", "***************************")
	s.line(110, "orders", "APPLICATION FAILED TO START")
	s.line(120, "orders", "Web server failed to start. Port 8080 was already in use.")
	s.crash(150, "orders", 1)
	d := diagnose(t, api.Topology{Services: []api.ServiceTopology{{Name: "orders"}}}, s)[0]
	top := d.Hypotheses[0]
	if top.RuleID != "K001-port-conflict" || top.Confidence.Level != api.ConfidenceHigh || !strings.Contains(top.Title, "port 8080 is already in use") ||
		top.NextStep.Command != "lsof -nP -iTCP:8080 -sTCP:LISTEN" {
		t.Fatalf("top: %+v\nall: %s", top, ruleIDs(d))
	}
}

func TestMissingJavaClassSuggestsABuild(t *testing.T) {
	s := newStream()
	s.started(0, "orders")
	s.line(100, "orders", `Exception in thread "main" java.lang.NoClassDefFoundError: org/springframework/boot/SpringApplication`)
	s.crash(120, "orders", 1)
	d := diagnose(t, api.Topology{}, s)[0]
	var found *api.Hypothesis
	for i, h := range d.Hypotheses {
		if h.RuleID == "K006-missing-module" {
			found = &d.Hypotheses[i]
		}
	}
	if found == nil || found.Confidence.Level != api.ConfidenceHigh || !strings.Contains(found.NextStep.Description, "./gradlew build") || strings.Contains(found.NextStep.Description, "npm install") {
		t.Fatalf("got %s", ruleIDs(d))
	}
}

// A JDBC "Connection to host:port refused" names its target, so a service that
// can't reach the database is linked to the database by port, and the database
// is diagnosed as the initiating failure.
func TestJdbcRefusalLinksServiceToDatabase(t *testing.T) {
	s := newStream()
	s.started(0, "database")
	s.line(100, "database", "FATAL:  database files are incompatible with server")
	s.crash(110, "database", 1)
	s.started(200, "api")
	for i := 0; i < 6; i++ {
		s.line(300+i*100, "api", "org.postgresql.util.PSQLException: Connection to localhost:5432 refused. Check that the hostname and port are correct and that the postmaster is accepting TCP/IP connections.")
	}
	s.line(1000, "api", "HikariPool-1 - Exception during pool initialization.")
	s.crash(1100, "api", 1)
	topo := api.Topology{Services: []api.ServiceTopology{{Name: "database", Ports: []int{5432}}, {Name: "api"}}} // no declared dependency
	d := diagnose(t, topo, s)[0]
	if !d.Incident.InitiatingKnown || d.Incident.InitiatingService != "database" {
		t.Fatalf("initiating = %q (known %v)\n%s", d.Incident.InitiatingService, d.Incident.InitiatingKnown, ruleIDs(d))
	}
	top := d.Hypotheses[0]
	if top.RuleID != "K002-dependency-down" || top.Confidence.Level != api.ConfidenceHigh || top.Title != "database failed first; api failed because of it" ||
		!strings.Contains(strings.Join(top.Confidence.Factors, " "), "api's connection errors target port 5432, which database owns") {
		t.Fatalf("top: %s\nfactors: %v", ruleIDs(d), top.Confidence.Factors)
	}
}

// Kestrel's wording for a taken port carries the port (the generic address-in-use
// shape would not), so the port-conflict diagnosis names it.
func TestDotnetPortConflictNamesThePort(t *testing.T) {
	s := newStream()
	s.started(0, "web")
	s.line(100, "web", "Unable to start Kestrel. System.IO.IOException: Failed to bind to address http://127.0.0.1:5000: address already in use.")
	s.crash(130, "web", 134)
	d := diagnose(t, api.Topology{Services: []api.ServiceTopology{{Name: "web"}}}, s)[0]
	top := d.Hypotheses[0]
	if top.RuleID != "K001-port-conflict" || !strings.Contains(top.Title, "port 5000 is already in use") || top.NextStep.Command != "lsof -nP -iTCP:5000 -sTCP:LISTEN" {
		t.Fatalf("top: %+v\nall: %s", top, ruleIDs(d))
	}
}

// A panic in a service that was healthy is a crash after a healthy start with a
// fatal cause, whatever the language prints.
func TestPanicsAfterAHealthyStartAreHighConfidence(t *testing.T) {
	for name, line := range map[string]string{
		"rust":   "thread 'main' panicked at src/main.rs:12:5:",
		"dotnet": "Unhandled exception. System.InvalidOperationException: Sequence contains no elements",
		"ruby":   "app.rb:3:in `<main>': undefined method `foo' for nil (NoMethodError)",
		"php":    "PHP Fatal error:  Uncaught Error: Call to undefined function foo() in /app/index.php:3",
		"jvm":    `Exception in thread "main" java.lang.IllegalStateException: boom`,
	} {
		s := newStream()
		s.started(0, "svc")
		s.ready(100, "svc")
		s.line(900, "svc", line)
		s.crash(920, "svc", 1)
		d := diagnose(t, api.Topology{}, s)[0]
		top := d.Hypotheses[0]
		if top.RuleID != "K003-crash-after-start" || top.Confidence.Level != api.ConfidenceHigh {
			t.Errorf("%s: %s", name, ruleIDs(d))
		}
	}
}

// The Rails PG gem words a refused connection without the psql parentheses; the
// port is still captured, so the service is linked to the database by port.
func TestRailsPostgresRefusalLinksByPort(t *testing.T) {
	s := newStream()
	s.started(0, "db")
	s.line(100, "db", "FATAL:  database files are incompatible with server")
	s.crash(110, "db", 1)
	s.started(200, "web")
	for i := 0; i < 5; i++ {
		s.line(300+i*100, "web", `PG::ConnectionBad: connection to server at "127.0.0.1", port 5432 failed: Connection refused`)
	}
	s.crash(900, "web", 1)
	topo := api.Topology{Services: []api.ServiceTopology{{Name: "db", Ports: []int{5432}}, {Name: "web"}}}
	d := diagnose(t, topo, s)[0]
	top := d.Hypotheses[0]
	if !d.Incident.InitiatingKnown || d.Incident.InitiatingService != "db" || top.RuleID != "K002-dependency-down" ||
		!strings.Contains(strings.Join(top.Confidence.Factors, " "), "web's connection errors target port 5432, which db owns") {
		t.Fatalf("initiating %q\n%s\nfactors: %v", d.Incident.InitiatingService, ruleIDs(d), top.Confidence.Factors)
	}
}

func TestMissingCodeAdviceMatchesTheRuntime(t *testing.T) {
	for line, want := range map[string]string{
		"Unhandled exception. System.IO.FileNotFoundException: Could not load file or assembly 'Dapper'": "dotnet restore",
		"You must install or update .NET to run this application.":                                       "dotnet --list-runtimes",
		"Could not find gem 'rails (~> 7.0)' in locally installed gems.":                                 "bundle install",
		"/app/app.rb:1:in `require': cannot load such file -- sinatra (LoadError)":                       "bundle install",
		"PHP Fatal error:  Uncaught Error: Failed opening required 'vendor/autoload.php'":                "composer install",
		"Error: Cannot find module 'express'":                                                            "npm install",
	} {
		s := newStream()
		s.started(0, "svc")
		s.line(100, "svc", line)
		s.crash(120, "svc", 1)
		var step string
		for _, h := range diagnose(t, api.Topology{}, s)[0].Hypotheses {
			if h.RuleID == "K006-missing-module" {
				step = h.NextStep.Description
			}
		}
		if !strings.Contains(step, want) {
			t.Errorf("%q: advice %q, want it to mention %q", line, step, want)
		}
	}
}

// Docker's "port is already allocated" names a host port that is often held by
// another container, so the next step lists containers before asking about processes.
func TestDockerPortAllocatedSuggestsDockerPs(t *testing.T) {
	s := newStream()
	s.started(0, "stack")
	s.line(100, "stack", "db-1  | ")
	s.line(200, "stack", "Error response from daemon: driver failed programming external connectivity on endpoint db (ab12cd): Bind for 0.0.0.0:5432 failed: port is already allocated")
	s.crash(250, "stack", 1)
	d := diagnose(t, api.Topology{Services: []api.ServiceTopology{{Name: "stack"}}}, s)[0]
	top := d.Hypotheses[0]
	if top.RuleID != "K001-port-conflict" || !strings.Contains(top.Title, "port 5432 is already in use") ||
		top.NextStep.Command != `docker ps --filter "publish=5432"` || !strings.Contains(top.Explanation, "Docker could not publish port 5432") ||
		!strings.Contains(top.NextStep.Description, "never stops containers") {
		t.Fatalf("top: %+v\nall: %s", top, ruleIDs(d))
	}
}

// A stack whose Docker daemon is down fails at startup with the daemon's message
// as its cause, and no dependency link is invented from a message with no target.
func TestDockerDaemonDownIsAStartupFailureWithACause(t *testing.T) {
	s := newStream()
	s.started(0, "stack")
	s.line(100, "stack", "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?")
	s.crash(150, "stack", 1)
	d := diagnose(t, api.Topology{Services: []api.ServiceTopology{{Name: "stack"}}}, s)[0]
	var k004 *api.Hypothesis
	for i, h := range d.Hypotheses {
		if h.RuleID == "K004-startup-failure" {
			k004 = &d.Hypotheses[i]
		}
		if h.RuleID == "K005-dependency-not-running" {
			t.Fatalf("a daemon message has no target, so K005 must stay silent: %s", ruleIDs(d))
		}
	}
	if k004 == nil || k004.Confidence.Level != api.ConfidenceHigh || !strings.Contains(k004.Title, "Cannot connect to the Docker daemon") {
		t.Fatalf("got %s", ruleIDs(d))
	}
}

func TestDependentAlreadyFailingIsContradiction(t *testing.T) {
	s := newStream()
	s.line(0, "api", "dial tcp 127.0.0.1:5432: connect: connection refused") // before the db failed
	s.crash(500, "database", 1)
	s.line(600, "api", "dial tcp 127.0.0.1:5432: connect: connection refused")
	d := diagnose(t, chain, s)[0]
	for _, h := range d.Hypotheses {
		if h.RuleID == "K002-dependency-down" {
			if len(h.Contradicting) == 0 || h.Confidence.Level == api.ConfidenceHigh {
				t.Fatalf("api failing before the database must contradict K002: %+v", h)
			}
			return
		}
	}
	t.Fatalf("K002 not proposed: %s", ruleIDs(d))
}

func TestNoLinkNoDependencyHypothesis(t *testing.T) {
	// Two unrelated services failing in sequence: K002 must not invent a link.
	s := newStream()
	s.crash(0, "a", 1)
	s.crash(300, "b", 1)
	d := diagnose(t, api.Topology{Services: []api.ServiceTopology{{Name: "a"}, {Name: "b"}}}, s)[0]
	for _, h := range d.Hypotheses {
		if h.RuleID == "K002-dependency-down" {
			t.Fatalf("K002 without a dependency edge or port evidence: %+v", h)
		}
	}
}

// evidenceless is a rule that misbehaves, to prove the engine enforces the invariant.
type evidenceless struct{}

func (evidenceless) ID() string { return "TEST-evidenceless" }
func (evidenceless) Evaluate(*rules.Context) []api.Hypothesis {
	return []api.Hypothesis{{RuleID: "TEST-evidenceless", Title: "a guess", Confidence: api.ConfidenceDetail{Level: api.ConfidenceHigh}}}
}

func TestEngineDropsHypothesesWithoutEvidence(t *testing.T) {
	s := newStream()
	s.crash(0, "a", 1)
	g := graph.Build(api.Topology{}, s.evs)
	in := correlation.Correlate(s.evs, g, 0)[0]
	d := DiagnoseWith(&rules.Context{Incident: in, Graph: g, Events: s.evs}, []rules.Rule{evidenceless{}})
	if len(d.Hypotheses) != 0 || d.Status != api.ClaimUnresolved || d.NextStep.Command != "kestrel timeline" {
		t.Fatalf("got %+v", d)
	}
}

func TestEveryRuleIsDocumented(t *testing.T) {
	doc := readDoc(t)
	for _, r := range rules.All() {
		if !strings.Contains(doc, "`"+r.ID()+"`") {
			t.Errorf("docs/diagnosis-rules.md does not document %s", r.ID())
		}
	}
}
