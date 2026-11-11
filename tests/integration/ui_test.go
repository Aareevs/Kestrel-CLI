package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
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
	for _, want := range []string{"diagnose (diag)", "watch (w, start)", "--no-color", "Use \"kestrel [command] --help\""} {
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
	// With no subcommand they still show their help.
	if r := run(t, t.TempDir(), nil, "sessions"); r.code != 0 || !strings.Contains(r.stdout, "list") || !strings.Contains(r.stdout, "delete") {
		t.Fatalf("`kestrel sessions` should show its subcommands: exit %d\n%s", r.code, r.stdout)
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
