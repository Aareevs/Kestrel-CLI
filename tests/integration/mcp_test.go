package integration

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Aareevs/Kestrel-CLI/internal/cli"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

var mcpTools = []string{"get_diagnosis", "get_failure_timeline", "get_recent_errors", "get_service_status",
	"get_session_summary", "get_verification_results", "inspect_project", "list_services"}

// connectMCP starts the real `kestrel mcp` binary and connects the official
// SDK client to it over stdio.
func connectMCP(t *testing.T, home, project string) *sdk.ClientSession {
	t.Helper()
	k, _ := buildBins(t)
	cmd := exec.Command(k, "mcp", "--project", project)
	cmd.Env = append(os.Environ(), "KESTREL_HOME="+home)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "kestrel-test", Version: "0"}, nil).
		Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call invokes a tool and returns its result plus the full wire text of
// everything the server sent for it (content and structured content).
func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (*sdk.CallToolResult, string) {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	b, _ := json.Marshal(res)
	return res, string(b)
}

func structured[T any](t *testing.T, res *sdk.CallToolResult) T {
	t.Helper()
	b, _ := json.Marshal(res.StructuredContent)
	return decode[T](t, string(b))
}

// TestMCPRealDiagnosisAndRedaction is the Phase 6 exit criterion: an MCP
// client retrieves a real diagnosis, and no tool's output carries a secret.
func TestMCPRealDiagnosisAndRedaction(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	id := runSession(t, home, project, "--say", "ticket INTERNAL-4242 opened")
	if code, out, errs := kestrelCmd(t, home, project, "verify", "--window", "500ms", "--interval", "250ms"); code == 2 || code == 4 {
		t.Fatalf("verify exit %d\n%s%s", code, out, errs)
	}
	// What the CLI says, to compare with what the MCP client gets.
	_, cliDiag, _ := kestrelCmd(t, home, project, "diagnose", "--format", "json")
	want := decode[api.DiagnosisReport](t, cliDiag)

	cs := connectMCP(t, home, project)
	if got := cs.InitializeResult(); got.ServerInfo.Name != "kestrel" || !strings.Contains(got.Instructions, "untrusted data") {
		t.Fatalf("initialize: %+v", got)
	}

	// Exactly the eight documented tools, every one read-only and closed-world.
	tl, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tl.Tools {
		names = append(names, tool.Name)
		a := tool.Annotations
		if a == nil || !a.ReadOnlyHint || a.OpenWorldHint == nil || *a.OpenWorldHint || tool.OutputSchema == nil || tool.Description == "" {
			t.Errorf("%s: must be read-only, closed-world, described, with an output schema: %+v", tool.Name, a)
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(mcpTools, ",") {
		t.Fatalf("tools: %v", names)
	}

	// The real diagnosis, the same one the CLI gives.
	res, wire := call(t, cs, "get_diagnosis", nil)
	diag := structured[api.DiagnosisReport](t, res)
	if res.IsError || diag.Session.ID != id || len(diag.Incidents) != len(want.Incidents) || len(diag.Incidents) == 0 {
		t.Fatalf("diagnosis over MCP: %s", wire)
	}
	for i, d := range diag.Incidents {
		w := want.Incidents[i]
		if d.Status != w.Status || len(d.Hypotheses) != len(w.Hypotheses) {
			t.Fatalf("incident %d differs from the CLI's", i+1)
		}
		for j, h := range d.Hypotheses {
			if h.RuleID != w.Hypotheses[j].RuleID || h.Confidence.Level != w.Hypotheses[j].Confidence.Level || len(h.Supporting) == 0 ||
				h.Supporting[0].EventID != w.Hypotheses[j].Supporting[0].EventID {
				t.Fatalf("hypothesis %d.%d differs from the CLI's: %+v", i+1, j+1, h)
			}
		}
	}
	// Text content mirrors the structured result, for clients that read only text.
	if tc, ok := res.Content[0].(*sdk.TextContent); !ok || !strings.Contains(tc.Text, diag.Incidents[0].Hypotheses[0].RuleID) {
		t.Fatalf("text content: %+v", res.Content)
	}
	all := wire

	// Every other tool works on the same session.
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"get_failure_timeline", map[string]any{"service": "api"}},
		{"get_recent_errors", map[string]any{"limit": 5}},
		{"get_session_summary", nil},
		{"get_verification_results", nil},
		{"get_service_status", nil},
		{"list_services", nil},
		{"inspect_project", nil},
	} {
		res, wire := call(t, cs, c.tool, c.args)
		if res.IsError {
			t.Fatalf("%s: %s", c.tool, wire)
		}
		all += wire
	}
	res, _ = call(t, cs, "get_verification_results", nil)
	if vr := structured[api.VerificationResults](t, res); len(vr.Verifications) != 1 || vr.Latest == nil || vr.Latest.Session.ID != id {
		t.Fatalf("verification results: %+v", vr)
	}
	res, _ = call(t, cs, "get_recent_errors", map[string]any{"limit": 5})
	if re := structured[api.RecentErrors](t, res); len(re.Events) == 0 || len(re.Events) > 5 {
		t.Fatalf("recent errors: %d", len(re.Events))
	}

	// Redaction: no fake secret anywhere in what the client received, and the
	// placeholders are there. Hostile lines arrive as inert text.
	for _, s := range fakeSecrets {
		if strings.Contains(all, s) {
			t.Errorf("secret %q reached the MCP client", s)
		}
	}
	if !strings.Contains(all, "[REDACTED:") || !strings.Contains(all, "rm -rf ~") {
		t.Errorf("expected redaction placeholders and the hostile line as data")
	}

	// Errors are tool errors with a usable message, not protocol failures.
	res, wire = call(t, cs, "get_diagnosis", map[string]any{"session": "nosuch"})
	if !res.IsError || !strings.Contains(wire, "no such session") {
		t.Fatalf("unknown session: %s", wire)
	}
	res, wire = call(t, cs, "get_failure_timeline", map[string]any{"since": "yesterday"})
	if !res.IsError || !strings.Contains(wire, "not a positive duration") {
		t.Fatalf("bad since: %s", wire)
	}
	if _, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "run_command", Arguments: map[string]any{}}); err == nil {
		t.Fatal("there must be no such tool")
	}
}

// MCP output is redacted once more on the way out, with the project's
// current patterns: a pattern added after a session was recorded still
// protects that session's data over MCP.
func TestMCPRedactsWithCurrentProjectPatterns(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	runSession(t, home, project, "--say", "ticket INTERNAL-4242 opened")
	os.WriteFile(filepath.Join(project, "kestrel.yaml"), []byte("version: 1\nredaction:\n  extra_patterns: ['INTERNAL-[0-9]+']\n"), 0o644)

	// Stored before the pattern existed, so the stored text has it.
	if _, out, _ := kestrelCmd(t, home, project, "timeline", "--service", "api", "--format", "json"); !strings.Contains(out, "INTERNAL-4242") {
		t.Fatal("test premise: the stored session should contain the token")
	}
	cs := connectMCP(t, home, project)
	res, wire := call(t, cs, "get_failure_timeline", map[string]any{"service": "api"})
	if res.IsError || strings.Contains(wire, "INTERNAL-4242") || !strings.Contains(wire, "ticket [REDACTED:") {
		t.Fatalf("the project's pattern must apply to MCP output:\n%s", wire)
	}
}

// In process, so coverage tooling sees the server: the CLI's mcp command
// served over pipes to the SDK client.
func TestMCPInProcess(t *testing.T) {
	t.Setenv("KESTREL_HOME", t.TempDir())
	project := t.TempDir()
	os.WriteFile(filepath.Join(project, "kestrel.yaml"), []byte("version: 1\nservices:\n  - name: db\n    command: 'PASSWORD=hunter2 run-db'\n    health: { type: tcp, address: '127.0.0.1:1' }\n"), 0o644)

	cin, sout := io.Pipe()
	sin, cout := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- cli.Execute(ctx, cli.Streams{In: sin, Out: sout, Err: io.Discard, Getenv: func(string) string { return "" },
			Getwd: func() (string, error) { return project, nil }}, []string{"mcp"})
	}()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).
		Connect(ctx, &sdk.IOTransport{Reader: cin, Writer: cout}, nil)
	if err != nil {
		t.Fatal(err)
	}

	res, wire := call(t, cs, "list_services", nil)
	if sl := structured[api.ServiceList](t, res); res.IsError || sl.Source != "kestrel.yaml" || len(sl.Services) != 1 || strings.Contains(wire, "hunter2") {
		t.Fatalf("list_services: %s", wire)
	}
	res, wire = call(t, cs, "get_service_status", map[string]any{"service": "db"})
	if st := structured[api.StatusReport](t, res); res.IsError || len(st.Services) != 1 || st.Services[0].Readiness != api.ReadinessNotReady {
		t.Fatalf("status: %s", wire)
	}
	if res, _ = call(t, cs, "get_service_status", map[string]any{"service": "nope"}); !res.IsError {
		t.Fatal("unknown service should be a tool error")
	}
	// No session yet: a clear tool error, not a crash.
	if res, wire = call(t, cs, "get_diagnosis", nil); !res.IsError || !strings.Contains(wire, "none recorded yet") {
		t.Fatalf("no session: %s", wire)
	}
	if res, _ = call(t, cs, "inspect_project", map[string]any{"max_depth": 1}); res.IsError {
		t.Fatal("inspect_project failed")
	}

	// Record a failing session in process, then read it over MCP.
	_, fake := buildBins(t)
	if r := run(t, project, nil, "run", "--quiet", "--name", "api", "--", fake, "--name", "api", "--secrets",
		"--exit-after", "300ms", "--exit-code", "3", "--exit-message", "fatal: boom"); r.code != 1 {
		t.Fatalf("run: exit %d\n%s", r.code, r.stderr)
	}
	res, wire = call(t, cs, "get_recent_errors", map[string]any{"service": "api"})
	if re := structured[api.RecentErrors](t, res); res.IsError || len(re.Events) == 0 || strings.Contains(wire, "sup3rs3cret") {
		t.Fatalf("recent errors: %s", wire)
	}
	res, wire = call(t, cs, "get_verification_results", nil)
	if vr := structured[api.VerificationResults](t, res); res.IsError || len(vr.Verifications) != 0 || vr.Latest != nil {
		t.Fatalf("no verifications yet: %s", wire)
	}
	cs.Close()
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("mcp exit %d", code)
	}
}
