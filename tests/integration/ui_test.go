package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/cli"
)

var ansiCodes = regexp.MustCompile(`\x1b\[`)

func TestHelpIsGroupedOrderedAndPlainWhenPiped(t *testing.T) {
	r := run(t, t.TempDir(), nil, "--help")
	if r.code != 0 || ansiCodes.MatchString(r.stdout) {
		t.Fatalf("exit %d, styled output off a terminal: %q", r.code, r.stdout)
	}
	// Groups appear in order, and commands are in a sensible order within them
	// (not alphabetical).
	order := []string{"Get started", "inspect", "init", "doctor", "Run and watch", "run", "watch", "status",
		"Investigate", "diagnose", "timeline", "verify", "sessions", "Manage", "config", "cleanup", "mcp", "version"}
	pos := 0
	for _, want := range order {
		i := strings.Index(r.stdout[pos:], want)
		if i < 0 {
			t.Fatalf("%q missing or out of order after offset %d in:\n%s", want, pos, r.stdout)
		}
		pos += i + len(want)
	}
	// Aliases are shown next to their command, and global flags are listed.
	for _, want := range []string{"diagnose (diag, why)", "watch (w, start)", "--no-color", "Use \"kestrel [command] --help\""} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	// A subcommand's help has its own usage, aliases, and the global flags.
	r = run(t, t.TempDir(), nil, "diagnose", "--help")
	for _, want := range []string{"Usage", "kestrel diagnose [id|last] [flags]", "Aliases", "diagnose, diag", "Exit codes: 0 no incidents"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("diagnose help lacks %q:\n%s", want, r.stdout)
		}
	}
	r = run(t, t.TempDir(), nil, "help", "verify")
	if !strings.Contains(r.stdout, "--allow-exec") {
		t.Errorf("`kestrel help verify` should show verify's flags:\n%s", r.stdout)
	}
}

func TestGroupCommandsRejectUnknownSubcommands(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		suggest string
	}{
		{[]string{"sessions", "shwo"}, "show"},
		{[]string{"config", "valdiate"}, "validate"},
		{[]string{"sessions", "nonsense"}, ""},
	} {
		r := run(t, t.TempDir(), nil, tc.args...)
		if r.code != 2 || !strings.Contains(r.stderr, `unknown command "`+tc.args[1]+`" for "kestrel `+tc.args[0]+`"`) {
			t.Errorf("%v: exit %d\n%s", tc.args, r.code, r.stderr)
		}
		if tc.suggest != "" && !strings.Contains(r.stderr, "Did you mean this?\n\t"+tc.suggest) {
			t.Errorf("%v: no suggestion %q:\n%s", tc.args, tc.suggest, r.stderr)
		}
	}
	// `config` with no subcommand shows its help; `sessions` lists (see TestBareSessionsLists).
	if r := run(t, t.TempDir(), nil, "config"); r.code != 0 || !strings.Contains(r.stdout, "validate") || !strings.Contains(r.stdout, "show") {
		t.Fatalf("`kestrel config` should show its subcommands: exit %d\n%s", r.code, r.stdout)
	}
}

func TestErrorsSuggestANextStep(t *testing.T) {
	t.Setenv("KESTREL_HOME", t.TempDir())
	dir := t.TempDir()
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"diagnose"}, "next: kestrel run -- <command>"},
		{[]string{"timeline"}, "next: kestrel run -- <command>"},
		{[]string{"watch"}, "next: kestrel init"},
		{[]string{"run"}, "next: kestrel run -- npm run dev"},
	}
	for _, tt := range tests {
		r := run(t, dir, nil, tt.args...)
		if r.code != 2 || !strings.Contains(r.stderr, tt.want) {
			t.Errorf("%v: exit %d, want a %q hint:\n%s", tt.args, r.code, tt.want, r.stderr)
		}
		if ansiCodes.MatchString(r.stderr) {
			t.Errorf("%v: styled error off a terminal: %q", tt.args, r.stderr)
		}
	}
}

// With nothing to check there is nothing to wait for: the stability window is
// skipped (it used to be waited out, 30 seconds by default).
func TestVerifyWithNothingToCheckDoesNotWaitOutTheWindow(t *testing.T) {
	t.Setenv("KESTREL_HOME", t.TempDir())
	dir := t.TempDir()
	start := time.Now()
	r := run(t, dir, nil, "verify", "--window", "20s", "--interval", "5s")
	took := time.Since(start)
	if r.code != 3 || took > 5*time.Second {
		t.Fatalf("exit %d after %v (a 20s window must not be waited out)\n%s%s", r.code, took, r.stdout, r.stderr)
	}
	for _, want := range []string{"nothing to sample, so no stability window", "No recorded session for this project.", "Checks none could be chosen", "inconclusive"} {
		if !strings.Contains(r.stdout+r.stderr, want) {
			t.Errorf("output lacks %q:\n%s%s", want, r.stdout, r.stderr)
		}
	}
	if strings.Contains(r.stdout, "across a") {
		t.Errorf("an empty checks section should not mention a window:\n%s", r.stdout)
	}
}

// Commands that need no sampling still run (when authorized) without the wait.
func TestVerifyCommandsRunWithoutWaitingForAnEmptyWindow(t *testing.T) {
	t.Setenv("KESTREL_HOME", t.TempDir())
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	os.WriteFile(filepath.Join(project, "kestrel.yaml"), []byte("version: 1\nverify:\n  checks:\n    - { type: command, run: \"echo hi\", timeout: 10s }\n"), 0o644)
	start := time.Now()
	r := run(t, project, nil, "verify", "--allow-exec", "--window", "20s", "--interval", "5s")
	if took := time.Since(start); r.code != 0 || took > 8*time.Second || !strings.Contains(r.stdout, "passed") {
		t.Fatalf("exit %d after %v\n%s%s", r.code, took, r.stdout, r.stderr)
	}
}

// twoIncidents records one session with two separate failures, far enough apart
// (with a 500ms correlation window) to be two incidents.
func twoIncidents(t *testing.T) (home, project string) {
	t.Helper()
	_, fake := buildBins(t)
	home, project = t.TempDir(), t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	os.WriteFile(filepath.Join(project, "kestrel.yaml"), []byte("version: 1\ncorrelation:\n  window: 500ms\n"), 0o644)
	code, out, errs := kestrelCmd(t, home, project, "run", "--quiet",
		"--svc", "first="+fake+` --name first --exit-after 300ms --exit-code 1 --exit-message "fatal: the first failure"`,
		"--svc", "second="+fake+` --name second --exit-after 2500ms --exit-code 1 --exit-message "fatal: the second failure"`)
	if code != 1 {
		t.Fatalf("run exit %d\n%s%s", code, out, errs)
	}
	return home, project
}

func TestIncidentFilter(t *testing.T) {
	home, project := twoIncidents(t)
	numbers := func(args ...string) (int, []int) {
		code, out, errs := kestrelCmd(t, home, project, append(args, "--format", "json")...)
		if code != 0 && code != 1 && code != 3 { // timeline exits 0; diagnose 1 or 3
			t.Fatalf("%v: exit %d\n%s", args, code, errs)
		}
		var rep struct {
			Incidents []struct {
				Number   int `json:"number"`
				Incident struct {
					Number int `json:"number"`
				} `json:"incident"`
			} `json:"incidents"`
		}
		decode0 := func(v any) {
			if err := json.Unmarshal([]byte(out), v); err != nil {
				t.Fatalf("%v: %v\n%s", args, err, out)
			}
		}
		decode0(&rep)
		var ns []int
		for _, in := range rep.Incidents {
			n := in.Number
			if in.Incident.Number != 0 {
				n = in.Incident.Number
			}
			ns = append(ns, n)
		}
		return code, ns
	}
	if _, ns := numbers("diagnose"); len(ns) != 2 || ns[0] != 1 || ns[1] != 2 {
		t.Fatalf("diagnose without a filter: incidents %v, want [1 2]", ns)
	}
	for _, tc := range []struct {
		args []string
		want []int
	}{
		{[]string{"diagnose", "-I", "2"}, []int{2}},
		{[]string{"diagnose", "--incident", "1"}, []int{1}},
		{[]string{"why", "-I", "2"}, []int{2}}, // the alias
		{[]string{"timeline", "-I", "2"}, []int{2}},
		{[]string{"timeline", "--incident", "1"}, []int{1}},
	} {
		if _, ns := numbers(tc.args...); len(ns) != 1 || ns[0] != tc.want[0] {
			t.Errorf("%v: incidents %v, want %v", tc.args, ns, tc.want)
		}
	}
	// The text view shows only the chosen incident.
	code, out, _ := kestrelCmd(t, home, project, "diagnose", "-I", "2", "--no-color")
	if code != 1 || !strings.Contains(out, "Incident #2") || strings.Contains(out, "Incident #1") || !strings.Contains(out, "the second failure") || strings.Contains(out, "the first failure") {
		t.Fatalf("diagnose -I 2:\n%s", out)
	}
	// An incident that doesn't exist is a usage error that says what to do.
	for _, args := range [][]string{{"diagnose", "-I", "9"}, {"timeline", "-I", "9"}} {
		code, _, errs := kestrelCmd(t, home, project, args...)
		if code != 2 || !strings.Contains(errs, "no incident #9 in this session (it has 2)") || !strings.Contains(errs, "next: kestrel timeline") {
			t.Errorf("%v: exit %d\n%s", args, code, errs)
		}
	}
}

func TestShortFlagsAndAliases(t *testing.T) {
	home, project := twoIncidents(t)
	// why is diagnose.
	_, a, _ := kestrelCmd(t, home, project, "diagnose", "--no-color")
	_, b, _ := kestrelCmd(t, home, project, "why", "--no-color")
	if a != b || a == "" {
		t.Fatalf("`why` and `diagnose` differ:\n%s\n---\n%s", a, b)
	}
	// -a is --all, and -s is --service.
	if code, out, errs := kestrelCmd(t, home, project, "diagnose", "-a", "--format", "json"); code != 1 || !strings.Contains(out, `"incidents"`) {
		t.Fatalf("diagnose -a: exit %d\n%s%s", code, out, errs)
	}
	_, long, _ := kestrelCmd(t, home, project, "timeline", "--service", "second", "--no-color")
	_, short, _ := kestrelCmd(t, home, project, "timeline", "-s", "second", "--no-color")
	if long != short || !strings.Contains(long, "the second failure") {
		t.Fatalf("timeline -s:\n%s\n---\n%s", long, short)
	}
	// The short flags are in the help.
	for cmd, want := range map[string]string{"diagnose": "-I, --incident", "timeline": "-s, --service", "verify": "-w, --window", "run": "-n, --name"} {
		if h := run(t, t.TempDir(), nil, cmd, "--help").stdout; !strings.Contains(h, want) {
			t.Errorf("`%s --help` lacks %q", cmd, want)
		}
	}
	if h := run(t, t.TempDir(), nil, "diagnose", "--help").stdout; !strings.Contains(h, "-a, --all") || !strings.Contains(h, "diagnose, diag, why") {
		t.Errorf("diagnose help:\n%s", h)
	}
	// run -n names the service, and -s adds one.
	_, fake := buildBins(t)
	code, out, errs := kestrelCmd(t, home, project, "run", "--quiet", "-n", "solo", "--format", "json", "--", fake, "--name", "solo", "--exit-after", "100ms")
	if code != 0 || !strings.Contains(out, `"service":"solo"`) {
		t.Fatalf("run -n: exit %d\n%s%s", code, out, errs)
	}
}

func TestBareSessionsLists(t *testing.T) {
	home, project := twoIncidents(t)
	_, list, _ := kestrelCmd(t, home, project, "sessions", "list", "--no-color")
	code, bare, errs := kestrelCmd(t, home, project, "sessions", "--no-color")
	if code != 0 || bare != list || !strings.Contains(bare, "run") {
		t.Fatalf("`kestrel sessions` should be `kestrel sessions list`: exit %d\n%s\n--- vs ---\n%s%s", code, bare, list, errs)
	}
	// With nothing recorded it is a friendly empty list, not an error or help.
	empty := t.TempDir()
	if code, out, errs := kestrelCmd(t, t.TempDir(), empty, "sessions", "--no-color"); code != 0 || errs != "" || strings.Contains(out, "Usage") {
		t.Fatalf("empty sessions: exit %d\n%s%s", code, out, errs)
	}
}

// pickRun runs kestrel in process with keystrokes on stdin. tty says whether
// stdin and stdout look like a terminal.
func pickRun(t *testing.T, home, project, keys string, tty bool, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("KESTREL_HOME", home)
	var out, errb bytes.Buffer
	s := cli.Streams{In: strings.NewReader(keys), Out: &out, Err: &errb, InIsTTY: tty, OutIsTTY: tty,
		MakeRaw: func() (func(), error) { return func() {}, nil },
		Getenv:  func(string) string { return "" },
		Getwd:   func() (string, error) { return project, nil }}
	code := cli.Execute(t.Context(), s, args)
	return code, out.String(), errb.String()
}

func TestInteractivePicker(t *testing.T) {
	home, project := twoIncidents(t)

	// Session picker, then incident picker: enter, then 2 (the first incident).
	code, out, errs := pickRun(t, home, project, "\r2\r", true, "diagnose", "-i", "--no-color")
	if code != 1 || !strings.Contains(out, "the first failure") || strings.Contains(out, "the second failure") {
		t.Fatalf("picking incident 1: exit %d\n%s%s", code, out, errs)
	}
	// "All" (the first row) keeps every incident.
	code, out, _ = pickRun(t, home, project, "\r\r", true, "diagnose", "-i", "--no-color")
	if code != 1 || !strings.Contains(out, "the first failure") || !strings.Contains(out, "the second failure") {
		t.Fatalf("picking All: exit %d\n%s", code, out)
	}
	// The picker leaves the terminal as it found it: every hidden cursor is shown again.
	if strings.Count(out, "\x1b[?25l") != 2 || strings.Count(out, "\x1b[?25h") != 2 {
		t.Errorf("cursor hidden/shown mismatch: %q", out)
	}
	// -I on the command line skips the incident picker; keys are the session choice only.
	code, out, _ = pickRun(t, home, project, "\r", true, "diagnose", "-i", "-I", "2", "--no-color")
	if code != 1 || !strings.Contains(out, "the second failure") || strings.Contains(out, "the first failure") {
		t.Fatalf("-i -I 2: exit %d\n%s", code, out)
	}
	// Cancelling exits quietly with 130 and prints no report.
	for _, keys := range []string{"q", "\x1b", "\r" + "q", ""} {
		code, out, errs = pickRun(t, home, project, keys, true, "diagnose", "-i", "--no-color")
		if code != 130 || strings.Contains(out, "failure") || strings.Contains(errs, "Error") {
			t.Errorf("cancel with %q: exit %d\n%s%s", keys, code, out, errs)
		}
	}
	// sessions show and timeline take -i too.
	if code, out, errs = pickRun(t, home, project, "\r", true, "sessions", "show", "-i", "--no-color"); code != 0 || !strings.Contains(out, "first") {
		t.Errorf("sessions show -i: exit %d\n%s%s", code, out, errs)
	}
	if code, out, errs = pickRun(t, home, project, "\r3\r", true, "timeline", "-i", "--no-color"); code != 0 || !strings.Contains(out, "the second failure") {
		t.Errorf("timeline -i: exit %d\n%s%s", code, out, errs)
	}
	// Without a terminal there is no picker: say so, and use the latest session.
	code, out, errs = pickRun(t, home, project, "\r\r", false, "diagnose", "-i", "--no-color")
	if code != 1 || !strings.Contains(errs, "not a terminal") || !strings.Contains(out, "the second failure") || strings.Contains(out, "\x1b[?25l") {
		t.Fatalf("non-TTY -i: exit %d\n%s%s", code, out, errs)
	}
}
