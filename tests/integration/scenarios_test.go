package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/cli"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// A scenario is a real stack of fakesvc services run under `kestrel watch`,
// then diagnosed from the recorded session. Nothing about the session is
// synthesized: the goldens are rendered from what Kestrel actually recorded,
// with run-specific values (times, IDs, PIDs, ports, durations, repeat counts)
// normalized away.
type scenario struct {
	name   string
	config string // {{P:name}} becomes a free port; commands use ./bin/fakesvc
	// until is the event that completes the scenario; watch is stopped
	// `settle` after it arrives. Waiting for events rather than sleeping
	// keeps the scenario independent of machine load.
	until func(e api.Event) bool
	// alsoUntil, if set, must also have arrived (any order).
	alsoUntil func(e api.Event) bool
	settle    time.Duration
	exitCode  int // expected `kestrel diagnose` exit code
	check     func(t *testing.T, rep api.DiagnosisReport)
	// hold names a port that "another process" (the test) already listens
	// on. The listener is opened on port 0 before the config is written, so
	// no other test can take the port in between.
	hold string
}

var scenarios = []scenario{
	{
		name: "A-database-startup-failure",
		config: `version: 1
services:
  - name: database
    command: './bin/fakesvc --name database --exit-after 300ms --exit-code 1 --exit-message "FATAL:  database files are incompatible with server"'
    health: { type: tcp, address: "127.0.0.1:{{P:database}}", interval: 100ms, timeout: 100ms, retries: 2, startup_grace: 2s }
  - name: api
    command: './bin/fakesvc --name api --connect 127.0.0.1:{{P:database}} --connect-every 150ms --listen 127.0.0.1:{{P:api}} --listen-after 60s'
    depends_on: [database]
    health: { type: http, url: "http://127.0.0.1:{{P:api}}/health", interval: 100ms, timeout: 100ms, retries: 2, startup_grace: 2s }
  - name: frontend
    command: './bin/fakesvc --name frontend --listen 127.0.0.1:{{P:frontend}} --connect 127.0.0.1:{{P:api}} --connect-every 200ms --style node'
    depends_on: [api]
    health: { type: http, url: "http://127.0.0.1:{{P:frontend}}/health", interval: 100ms, timeout: 100ms, retries: 2, startup_grace: 1s }
`,
		// The database's probe gives up 2s after it starts. api starts once the
		// database has crashed (300ms in) and is refused at once, so its errors
		// come first. That order follows from causality, not machine speed.
		until:     func(e api.Event) bool { return e.ServiceID == "frontend" && e.Type == api.EventDependencyConnFailed },
		alsoUntil: func(e api.Event) bool { return e.ServiceID == "database" && e.Type == api.EventStartupTimeout },
		settle:    time.Second,
		exitCode:  1,
		check: func(t *testing.T, rep api.DiagnosisReport) {
			d := onlyIncident(t, rep)
			top := d.Hypotheses[0]
			if d.Status != api.ClaimHypothesis || !d.Incident.InitiatingKnown || d.Incident.InitiatingService != "database" ||
				top.RuleID != "K002-dependency-down" || top.Confidence.Level != api.ConfidenceHigh || top.Services[0] != "database" {
				t.Fatalf("expected database as the high-confidence initiating failure; got status=%s initiating=%s top=%s/%s/%v",
					d.Status, d.Incident.InitiatingService, top.RuleID, top.Confidence.Level, top.Services)
			}
			if got := symptomServices(d); got != "api,frontend" {
				t.Fatalf("downstream symptoms should be api and frontend, got %s", got)
			}
		},
	},
	{
		name: "B-port-conflict",
		config: `version: 1
services:
  - name: web
    command: './bin/fakesvc --name web --listen 127.0.0.1:{{P:web}} --style node'
`,
		until:    func(e api.Event) bool { return e.ServiceID == "web" && e.Type == api.EventProcessCrashed },
		exitCode: 1,
		hold:     "web",
		check: func(t *testing.T, rep api.DiagnosisReport) {
			d := onlyIncident(t, rep)
			top := d.Hypotheses[0]
			wantCmd := "lsof -nP -iTCP:"
			if runtime.GOOS == "windows" {
				wantCmd = "netstat -ano | findstr :"
			}
			if top.RuleID != "K001-port-conflict" || top.Confidence.Level != api.ConfidenceHigh || !strings.HasPrefix(top.NextStep.Command, wantCmd) {
				t.Fatalf("top: %s %s %q", top.RuleID, top.Confidence.Level, top.NextStep.Command)
			}
		},
	},
	{
		name: "C-crash-after-healthy-start",
		config: `version: 1
services:
  - name: worker
    command: './bin/fakesvc --name worker --listen 127.0.0.1:{{P:worker}} --exit-after 1500ms --exit-code 2 --exit-message "panic: runtime error: invalid memory address or nil pointer dereference"'
    health: { type: http, url: "http://127.0.0.1:{{P:worker}}/health", interval: 100ms, timeout: 100ms, retries: 2, startup_grace: 5s }
`,
		// After the crash its probe goes from ready to failing: wait for that too.
		until:    func(e api.Event) bool { return e.ServiceID == "worker" && e.Type == api.EventProbeFailed },
		exitCode: 1,
		check: func(t *testing.T, rep api.DiagnosisReport) {
			d := onlyIncident(t, rep)
			top := d.Hypotheses[0]
			if top.RuleID != "K003-crash-after-start" || top.Confidence.Level != api.ConfidenceHigh ||
				!strings.HasPrefix(top.Explanation, "Observed:") || !strings.Contains(top.Explanation, "Hypothesis:") {
				t.Fatalf("top: %s %s %q", top.RuleID, top.Confidence.Level, top.Explanation)
			}
		},
	},
	{
		name: "D-noisy-but-healthy",
		config: `version: 1
services:
  - name: api
    command: './bin/fakesvc --name api --listen 127.0.0.1:{{P:api}} --say "WARN slow query took 1200ms" --say "level=warn msg=cache-miss-storm" --say-every 100ms'
    health: { type: http, url: "http://127.0.0.1:{{P:api}}/health", interval: 100ms, timeout: 100ms, startup_grace: 5s }
`,
		until:    func(e api.Event) bool { return e.ServiceID == "api" && e.Type == api.EventProbeReady },
		settle:   1500 * time.Millisecond, // let the warnings pile up
		exitCode: 0,
		check: func(t *testing.T, rep api.DiagnosisReport) {
			if len(rep.Incidents) != 0 {
				t.Fatalf("warnings must not produce an incident; got %d", len(rep.Incidents))
			}
		},
	},
	{
		name: "I-ambiguous-simultaneous-failures",
		config: `version: 1
services:
  - name: billing
    command: './bin/fakesvc --name billing --exit-after 800ms --exit-code 1 --exit-message "ERROR: could not load pricing table"'
  - name: search
    command: './bin/fakesvc --name search --exit-after 1300ms --exit-code 1 --exit-message "ERROR: index directory is locked"'
`,
		until:    func(e api.Event) bool { return e.ServiceID == "search" && e.Type == api.EventProcessCrashed },
		settle:   300 * time.Millisecond, // billing crashed ~500ms earlier
		exitCode: 3,
		check: func(t *testing.T, rep api.DiagnosisReport) {
			d := onlyIncident(t, rep)
			if d.Status != api.ClaimUnresolved || d.Incident.InitiatingKnown || d.Incident.CorrelationStrength != api.CorrelationWeak || len(d.Hypotheses) < 2 {
				t.Fatalf("expected unresolved with competing hypotheses: status=%s known=%v strength=%s n=%d",
					d.Status, d.Incident.InitiatingKnown, d.Incident.CorrelationStrength, len(d.Hypotheses))
			}
			if d.Hypotheses[0].Services[0] == d.Hypotheses[1].Services[0] {
				t.Fatal("the competing hypotheses should blame different services")
			}
		},
	},
}

func onlyIncident(t *testing.T, rep api.DiagnosisReport) api.IncidentDiagnosis {
	t.Helper()
	if len(rep.Incidents) != 1 {
		t.Fatalf("expected exactly one incident, got %d", len(rep.Incidents))
	}
	if len(rep.Incidents[0].Hypotheses) == 0 {
		t.Fatal("incident has no hypotheses")
	}
	return rep.Incidents[0]
}

func symptomServices(d api.IncidentDiagnosis) string {
	var s []string
	for _, x := range d.Symptoms {
		s = append(s, x.Service)
	}
	sort.Strings(s)
	return strings.Join(s, ",")
}

var portRefRe = regexp.MustCompile(`\{\{P:([a-z]+)\}\}`)

// normalizer masks values that legitimately differ between runs.
type normalizer struct{ ports map[string]int }

var (
	startedRe  = regexp.MustCompile(`started \(pid \d+\): .*$`)
	sessionRe  = regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{26}\b`)
	shortIDRe  = regexp.MustCompile(`…[0-9A-HJKMNP-TV-Z]{6}\b`)
	rowTimeRe  = regexp.MustCompile(`(?m)^  \d\d:\d\d:\d\d(?:–\d\d:\d\d:\d\d|\s{9})  `)
	clockRe    = regexp.MustCompile(`\d\d:\d\d:\d\d`)
	pidRe      = regexp.MustCompile(`pid \d+`)
	countRe    = regexp.MustCompile(`×\d+`)
	timesRe    = regexp.MustCompile(`\d+ time\(s\)`)
	durationRe = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ns|µs|ms|s)\b`)
	evtPadRe   = regexp.MustCompile(`\s{2,}evt `)
)

func (n normalizer) apply(s string) string {
	// Ports first, by name, longest numbers first so 5432 can't eat 15432.
	type pn struct {
		name string
		port int
	}
	var ps []pn
	for name, p := range n.ports {
		ps = append(ps, pn{name, p})
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].port > ps[j].port })
	for _, p := range ps {
		s = regexp.MustCompile(`\b`+strconv.Itoa(p.port)+`\b`).ReplaceAllString(s, "<"+p.name+"-port>")
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = startedRe.ReplaceAllString(l, "started (pid N): <command>")
	}
	s = strings.Join(lines, "\n")
	s = sessionRe.ReplaceAllString(s, "<session>")
	s = shortIDRe.ReplaceAllString(s, "…<evt>")
	s = rowTimeRe.ReplaceAllString(s, "  <time>  ")
	s = clockRe.ReplaceAllString(s, "hh:mm:ss")
	s = pidRe.ReplaceAllString(s, "pid N")
	s = countRe.ReplaceAllString(s, "×N")
	s = timesRe.ReplaceAllString(s, "N time(s)")
	s = durationRe.ReplaceAllString(s, "<dur>")
	s = evtPadRe.ReplaceAllString(s, "  evt ")
	s = regexp.MustCompile(`connectex: No connection could be made because the target machine actively refused it\.?`).ReplaceAllString(s, "connect: connection refused")
	return s
}

// TestScenarios is the Phase 4 exit criterion: scenarios A, B, C, D and I,
// each a real fakesvc stack, diagnosed into reviewed golden output; and no
// hypothesis anywhere lacks evidence IDs that exist in the session.
func TestScenarios(t *testing.T) {
	_, fake := buildBins(t)
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			home, project := t.TempDir(), t.TempDir()
			project, _ = filepath.EvalSymlinks(project)
			binDir := filepath.Join(project, "bin")
			os.MkdirAll(binDir, 0o755)
			dest := filepath.Join(binDir, "fakesvc")
			if runtime.GOOS == "windows" {
				dest += ".exe"
				data, err := os.ReadFile(fake)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dest, data, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Symlink(fake, dest); err != nil {
					t.Fatal(err)
				}
			}

			ports := map[string]int{}
			if sc.hold != "" {
				ports[sc.hold] = holdPort(t)
			}
			cfg := portRefRe.ReplaceAllStringFunc(sc.config, func(m string) string {
				name := portRefRe.FindStringSubmatch(m)[1]
				if ports[name] == 0 {
					ports[name] = freePort(t)
				}
				return strconv.Itoa(ports[name])
			})
			if err := os.WriteFile(filepath.Join(project, "kestrel.yaml"), []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}

			k := startKestrel(t, home, "watch", "--project", project, "--format", "json")
			evs := readEvents(k.stdout)
			evs.waitFor(t, sc.name+" to play out", 60*time.Second, sc.until)
			if sc.alsoUntil != nil {
				evs.waitFor(t, sc.name+" to play out", 60*time.Second, sc.alsoUntil)
			}
			time.Sleep(sc.settle)
			k.ctrlC()
			if code := k.wait(t, 30*time.Second); code != 0 && code != 1 {
				t.Fatalf("watch exit %d; stderr:\n%s", code, k.stderr)
			}
			<-evs.done

			code, out, errs := kestrelCmd(t, home, project, "diagnose", "--format", "json")
			if code != sc.exitCode {
				_, show, _ := kestrelCmd(t, home, project, "sessions", "show", "--no-color")
				t.Fatalf("diagnose exit %d, want %d\n%s\nsession:\n%s\nwatch stderr:\n%s", code, sc.exitCode, errs, show, k.stderr)
			}
			rep := decode[api.DiagnosisReport](t, out)
			checkEvidenceExists(t, home, project, rep)
			sc.check(t, rep)

			n := normalizer{ports: ports}
			_, text, _ := kestrelCmd(t, home, project, "diagnose", "--no-color")
			golden(t, "scenarios/"+sc.name+".diagnose.txt", n.apply(text))
			_, tl, _ := kestrelCmd(t, home, project, "timeline", "--no-color")
			golden(t, "scenarios/"+sc.name+".timeline.txt", n.apply(tl))
		})
	}
}

// holdPort plays "another process" holding a port: it listens on a fresh
// port and serves until the test ends, then checks it was never disturbed.
func holdPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// The holder is still serving after Kestrel ran and shut down.
		c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
		if err != nil {
			t.Errorf("the process holding the port was disturbed: %v", err)
		} else {
			c.Close()
		}
		ln.Close()
	})
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

// checkEvidenceExists: every hypothesis cites at least one event, and every
// cited event ID (supporting, contradicting, initiating, symptom, edge) is a
// real event of this session.
func checkEvidenceExists(t *testing.T, home, project string, rep api.DiagnosisReport) {
	t.Helper()
	_, out, _ := kestrelCmd(t, home, project, "sessions", "export", rep.Session.ID)
	var bundle api.ExportBundle
	if err := json.Unmarshal([]byte(out), &bundle); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, e := range bundle.Events {
		ids[e.ID] = true
	}
	mustExist := func(what, id string) {
		if !ids[id] {
			t.Errorf("%s cites event %q, which is not in the session", what, id)
		}
	}
	for _, d := range rep.Incidents {
		if d.Incident.InitiatingKnown {
			mustExist("initiating event", d.Incident.InitiatingEventID)
		}
		for _, id := range d.Incident.EventIDs {
			mustExist("incident", id)
		}
		for _, h := range d.Hypotheses {
			if len(h.Supporting) == 0 {
				t.Errorf("hypothesis %q has no supporting evidence", h.Title)
			}
			for _, e := range append(append([]api.Evidence{}, h.Supporting...), h.Contradicting...) {
				mustExist("hypothesis "+h.RuleID, e.EventID)
			}
		}
		for _, s := range d.Symptoms {
			for _, id := range s.EventIDs {
				mustExist("symptom "+s.Service, id)
			}
		}
	}
	for _, e := range rep.Graph {
		for _, id := range e.Evidence {
			mustExist("graph edge", id)
		}
	}
}

// In-process run of a small failing stack so coverage tooling sees the
// diagnose/timeline code paths and their flags.
func TestDiagnoseTimelineInProcess(t *testing.T) {
	_, fake := buildBins(t)
	t.Setenv("KESTREL_HOME", t.TempDir())
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	port := freePort(t)
	cfg := fmt.Sprintf(`version: 1
correlation: { window: 30s }
services:
  - name: db
    command: '%[1]s --name db --exit-after 200ms --exit-code 1 --exit-message "FATAL:  could not open data directory"'
    health: { type: tcp, address: "127.0.0.1:%[2]d", interval: 100ms, timeout: 100ms, startup_grace: 5s }
  - name: api
    command: '%[1]s --name api --connect 127.0.0.1:%[2]d --connect-every 100ms --exit-after 900ms --exit-code 1 --exit-message "Error: Cannot find module pg"'
    depends_on: [db]
`, fake, port)
	os.WriteFile(filepath.Join(dir, "kestrel.yaml"), []byte(cfg), 0o644)

	// (Without db's probe, api wouldn't wait for it and would fail first;
	// Kestrel would then rightly call the incident unresolved.)

	// Nothing recorded yet.
	if r := run(t, dir, nil, "diagnose"); r.code != 2 || !strings.Contains(r.stderr, "none recorded yet") {
		t.Fatalf("diagnose with no sessions: exit %d %s", r.code, r.stderr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	var o, e bytes.Buffer
	if code := cli.Execute(ctx, cli.Streams{In: strings.NewReader(""), Out: &o, Err: &e, Getenv: func(string) string { return "" },
		Getwd: func() (string, error) { return dir, nil }}, []string{"watch", "--quiet"}); code != 0 && code != 1 {
		t.Fatalf("watch exit %d: %s", code, e.String())
	}

	r := run(t, dir, nil, "diagnose", "--no-color")
	for _, want := range []string{"Incident #1", "Initiating failure: db", "K004-startup-failure", "K006-missing-module", "Next step"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("diagnose lacks %q:\n%s", want, r.stdout)
		}
	}
	if r.code != 1 {
		t.Fatalf("diagnose exit %d", r.code)
	}
	rep := decode[api.DiagnosisReport](t, run(t, dir, nil, "diagnose", "--format", "json", "--session", "last").stdout)
	if len(rep.Incidents) != 1 || rep.Incidents[0].Incident.InitiatingService != "db" || len(rep.Graph) == 0 {
		t.Fatalf("report: %+v", rep.Incidents)
	}

	r = run(t, dir, nil, "timeline", "--no-color")
	if !strings.Contains(r.stdout, "first anomaly") || !strings.Contains(r.stdout, "Dependencies: api → db (declared)") {
		t.Fatalf("timeline:\n%s", r.stdout)
	}
	r = run(t, dir, nil, "timeline", "--no-color", "--service", "api", "-n", "50")
	if !strings.Contains(r.stdout, "api · its recorded output") || !strings.Contains(r.stdout, "Cannot find module") {
		t.Fatalf("timeline --service:\n%s", r.stdout)
	}
	tl := decode[api.TimelineReport](t, run(t, dir, nil, "timeline", "--format", "json", "--service", "api").stdout)
	if tl.Service != "api" || len(tl.Events) == 0 || len(tl.Incidents) != 1 {
		t.Fatalf("timeline json: %+v", tl)
	}
	if r := run(t, dir, nil, "timeline", "--service", "nope"); r.code != 2 || !strings.Contains(r.stderr, `no service "nope"`) {
		t.Fatalf("unknown service: exit %d %s", r.code, r.stderr)
	}
	// --since a moment: the incident ended earlier, so nothing is in range.
	r = run(t, dir, nil, "timeline", "--no-color", "--since", "1ms")
	if !strings.Contains(r.stdout, "showing events since") || !strings.Contains(r.stdout, "No incidents in this session") {
		t.Fatalf("timeline --since:\n%s", r.stdout)
	}
	// Another project needs --all.
	other, _ := filepath.EvalSymlinks(t.TempDir())
	if r := run(t, other, nil, "diagnose", rep.Session.ID); r.code != 2 {
		t.Fatalf("cross-project diagnose: exit %d", r.code)
	}
	if r := run(t, other, nil, "diagnose", rep.Session.ID, "--all", "--quiet"); r.code != 1 || strings.Contains(r.stdout, "never runs them") {
		t.Fatalf("diagnose --all --quiet: exit %d\n%s", r.code, r.stdout)
	}
}
