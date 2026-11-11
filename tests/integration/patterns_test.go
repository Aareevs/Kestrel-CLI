package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

const shardPattern = `version: 1
patterns:
  - id: U001-shard-down
    description: a database shard is unreachable
    type: dependency_conn_failed
    severity: error
    regex: 'PRISMA: shard (?P<target>\S+) unreachable'
    match: ["PRISMA: shard db-7:5432 unreachable"]
    no_match: ["PRISMA: shard db-7:5432 healthy"]
`

// A project's own pattern recognizes a line Kestrel's built-ins don't, the
// event carries its attributes, and diagnosis uses it like any other
// connection failure.
func TestProjectPatternEndToEnd(t *testing.T) {
	_, fake := buildBins(t)
	home, project := t.TempDir(), t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	os.WriteFile(filepath.Join(project, "kestrel.yaml"), []byte(shardPattern), 0o644)

	run := func(dir string) (int, string, string) {
		return kestrelCmd(t, home, dir, "run", "--quiet", "--name", "api", "--format", "json", "--",
			fake, "--name", "api", "--say", "PRISMA: shard 127.0.0.1:59871 unreachable", "--say-every", "100ms",
			"--exit-after", "1500ms", "--exit-code", "2", "--exit-message", "fatal: giving up")
	}
	code, out, errs := run(project)
	if code != 1 {
		t.Fatalf("run exit %d\n%s%s", code, out, errs)
	}
	var found *api.Event
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		e := decode[api.Event](t, line)
		if e.Attrs.PatternID == "U001-shard-down" && found == nil {
			found = &e
		}
	}
	if found == nil || found.Type != api.EventDependencyConnFailed || found.Severity != api.SeverityError ||
		found.Attrs.Target != "127.0.0.1:59871" || found.Attrs.Port != 59871 {
		t.Fatalf("the project's pattern did not classify the line: %+v", found)
	}

	// Diagnosis treats it as a refused connection to a port nothing supervised owns.
	code, diag, _ := kestrelCmd(t, home, project, "diagnose", "--format", "json")
	rep := decode[api.DiagnosisReport](t, diag)
	var rules []string
	for _, d := range rep.Incidents {
		for _, h := range d.Hypotheses {
			rules = append(rules, h.RuleID)
		}
	}
	if code != 1 || !strings.Contains(strings.Join(rules, ","), "K005-dependency-not-running") {
		t.Fatalf("diagnose exit %d, rules %v", code, rules)
	}

	// Without the pattern, the same output is plain text with no pattern.
	other := t.TempDir()
	other, _ = filepath.EvalSymlinks(other)
	_, out, _ = run(other)
	if strings.Contains(out, "U001-shard-down") {
		t.Fatal("a project's patterns must not apply to other projects")
	}
}

// A bad pattern in kestrel.yaml is a configuration error, reported with its
// line, and nothing is started or recorded.
func TestBadProjectPatternIsAConfigError(t *testing.T) {
	_, fake := buildBins(t)
	home, project := t.TempDir(), t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	bad := strings.Replace(shardPattern, `["PRISMA: shard db-7:5432 unreachable"]`, `["unrelated"]`, 1)
	os.WriteFile(filepath.Join(project, "kestrel.yaml"), []byte(bad), 0o644)

	code, out, errs := kestrelCmd(t, home, project, "config", "validate")
	if code != 2 || !strings.Contains(out, "kestrel.yaml:3") || !strings.Contains(out, `should match "unrelated" but doesn't`) {
		t.Fatalf("config validate: exit %d\n%s%s", code, out, errs)
	}
	code, _, errs = kestrelCmd(t, home, project, "run", "--name", "api", "--", fake, "--name", "api", "--exit-after", "100ms")
	if code != 2 || !strings.Contains(errs, "patterns[0]") {
		t.Fatalf("run with a bad config: exit %d\n%s", code, errs)
	}
	_, out, _ = kestrelCmd(t, home, project, "sessions", "list", "--format", "json")
	if l := decode[api.SessionList](t, out); len(l.Sessions) != 0 {
		t.Fatalf("a rejected config must not create a session: %+v", l.Sessions)
	}
}

// A JVM service's own wording for a taken port (Spring Boot) is diagnosed as a
// port conflict, with the holder left alone.
func TestSpringPortConflictEndToEnd(t *testing.T) {
	_, fake := buildBins(t)
	home, project := t.TempDir(), t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	code, out, errs := kestrelCmd(t, home, project, "run", "--quiet", "--name", "orders", "--",
		fake, "--name", "orders",
		"--say", "APPLICATION FAILED TO START", "--say", "Web server failed to start. Port 18080 was already in use.",
		"--exit-after", "400ms", "--exit-code", "1")
	if code != 1 {
		t.Fatalf("run exit %d\n%s%s", code, out, errs)
	}
	code, out, _ = kestrelCmd(t, home, project, "diagnose", "--format", "json")
	rep := decode[api.DiagnosisReport](t, out)
	if code != 1 || len(rep.Incidents) != 1 {
		t.Fatalf("diagnose exit %d: %s", code, out)
	}
	top := rep.Incidents[0].Hypotheses[0]
	if top.RuleID != "K001-port-conflict" || !strings.Contains(top.Title, "port 18080 is already in use") ||
		top.NextStep.Command != "lsof -nP -iTCP:18080 -sTCP:LISTEN" && !strings.Contains(top.NextStep.Command, "18080") {
		t.Fatalf("top: %+v", top)
	}
}

// A Rust service that was serving and then panicked is a high-confidence
// "crash after a healthy start", with the panic as the cause. Without the Rust
// patterns the panic line is plain output and the cause isn't visible.
func TestRustPanicAfterHealthyStartEndToEnd(t *testing.T) {
	_, fake := buildBins(t)
	home, project := t.TempDir(), t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	port := freePort(t)
	code, out, errs := kestrelCmd(t, home, project, "run", "--quiet", "--name", "api", "--",
		fake, "--name", "api", "--listen", fmt.Sprintf("127.0.0.1:%d", port),
		"--exit-after", "1500ms", "--exit-code", "101", "--exit-message", "thread 'main' panicked at src/main.rs:12:5:")
	if code != 1 {
		t.Fatalf("run exit %d\n%s%s", code, out, errs)
	}
	_, out, _ = kestrelCmd(t, home, project, "diagnose", "--format", "json")
	rep := decode[api.DiagnosisReport](t, out)
	if len(rep.Incidents) != 1 {
		t.Fatalf("incidents: %s", out)
	}
	top := rep.Incidents[0].Hypotheses[0]
	if top.RuleID != "K003-crash-after-start" || top.Confidence.Level != api.ConfidenceHigh || !strings.Contains(top.Explanation, "panicked at src/main.rs:12:5") {
		t.Fatalf("top: %+v", top)
	}
}
