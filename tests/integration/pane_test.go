package integration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/cli"
	"github.com/Aareevs/Kestrel-CLI/internal/termtest"
)

// screenTap is a terminal that remembers what the screen looked like the
// moment Kestrel gave the scroll region back (just before the final summary).
type screenTap struct {
	*termtest.VT
	raw    bytes.Buffer
	atExit string
	tables []string // each distinct state of the top four rows, in order
}

func (s *screenTap) Write(p []byte) (int, error) {
	s.raw.Write(p)
	if bytes.Contains(p, []byte("\x1b[r")) && s.atExit == "" {
		s.atExit = s.VT.Text()
	}
	n, err := s.VT.Write(p)
	if head := strings.Join(strings.Split(s.VT.Text(), "\n")[:4], "\n"); strings.Contains(head, "SERVICE") &&
		(len(s.tables) == 0 || s.tables[len(s.tables)-1] != head) {
		s.tables = append(s.tables, head)
	}
	return n, err
}

func watchProject(t *testing.T) string {
	t.Helper()
	_, fake := buildBins(t)
	dir := t.TempDir()
	port := freePort(t)
	cfg := fmt.Sprintf(`version: 1
services:
  - name: web
    command: '%[1]s --name web --listen 127.0.0.1:%[2]d --say-every 300ms --say "request handled ok"'
    health: { type: http, url: "http://127.0.0.1:%[2]d/health", interval: 100ms, timeout: 100ms }
  - name: worker
    command: '%[1]s --name worker --exit-after 800ms --exit-code 1 --exit-message "fatal: queue unreachable"'
`, fake, port)
	if err := os.WriteFile(filepath.Join(dir, "kestrel.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// watchTTY runs `kestrel watch` in process as if on a 90x30 terminal, for
// about 2.5 seconds (the deadline plays the part of Ctrl-C).
func watchTTY(t *testing.T, dir string, tty bool, args ...string) *screenTap {
	t.Helper()
	t.Setenv("KESTREL_HOME", t.TempDir())
	tap := &screenTap{VT: termtest.New(90, 30)}
	ctx, cancel := context.WithTimeout(t.Context(), 2500*time.Millisecond)
	defer cancel()
	var errb bytes.Buffer
	cli.Execute(ctx, cli.Streams{
		In: strings.NewReader(""), Out: tap, Err: &errb, InIsTTY: tty, OutIsTTY: tty,
		TermSize: func() (int, int) { return 90, 30 },
		Getenv:   func(k string) string { return map[string]string{"TERM": "xterm-256color"}[k] },
		Getwd:    func() (string, error) { return dir, nil },
	}, append([]string{"--no-color"}, args...))
	return tap
}

func TestWatchPinsAStatusTableAboveTheLog(t *testing.T) {
	tap := watchTTY(t, watchProject(t), true, "watch")
	rows := strings.Split(tap.atExit, "\n")
	if len(rows) < 8 {
		t.Fatalf("no screen captured before the pane closed (raw %d bytes)", tap.raw.Len())
	}
	head := strings.Join(rows[:4], "\n")
	for _, want := range []string{"SERVICE", "web", "worker", "■ exited", "✗ crashed", "crashed (exit code 1)"} {
		if !strings.Contains(head, want) {
			t.Errorf("table lacks %q:\n%s", want, tap.atExit)
		}
	}
	// Over the run the table went from waiting, to running, to ready.
	var seen []string
	for _, want := range []string{"web      ◌ waiting", "web      ● running", "web      ● ready", "worker   ✗ crashed"} {
		found := false
		for _, tb := range tap.tables {
			found = found || strings.Contains(tb, want)
		}
		if !found {
			seen = append(seen, want)
		}
	}
	if len(seen) > 0 {
		t.Errorf("the table never showed %q; it showed:\n%s", seen, strings.Join(tap.tables, "\n--\n"))
	}
	if !strings.Contains(rows[3], "───") {
		t.Errorf("row 4 should be the rule: %q", rows[3])
	}
	below := strings.Join(rows[4:], "\n")
	for _, want := range []string{"kestrel session", "▶ started", "request handled ok", "fatal: queue unreachable"} {
		if !strings.Contains(below, want) {
			t.Errorf("log below the table lacks %q:\n%s", want, tap.atExit)
		}
	}
	if strings.Contains(head, "request handled") || strings.Contains(head, "▶ started") {
		t.Errorf("log text landed in the table:\n%s", head)
	}
	// The terminal is given back and the usual summary follows, below.
	if top, bottom := tap.Region(); top != 0 || bottom != 29 {
		t.Errorf("scroll region left at %d-%d", top, bottom)
	}
	if !strings.Contains(tap.Text(), "saved as session") {
		t.Errorf("summary missing:\n%s", tap.Text())
	}
}

func TestWatchWithoutAPinnedTable(t *testing.T) {
	dir := watchProject(t)
	cases := map[string]*screenTap{
		"--plain":     watchTTY(t, dir, true, "watch", "--plain"),
		"not a tty":   watchTTY(t, dir, false, "watch"),
		"--json":      watchTTY(t, dir, true, "watch", "--json"),
		"--quiet":     watchTTY(t, dir, true, "watch", "--quiet"),
		"run command": nil,
	}
	delete(cases, "run command")
	for name, tap := range cases {
		raw := tap.raw.String()
		if strings.Contains(raw, "\x1b7") || strings.Contains(raw, "\x1b[5;30r") || strings.Contains(raw, "SERVICE  STATE") {
			t.Errorf("%s: the pinned table should be off:\n%q", name, raw[:min(len(raw), 300)])
		}
	}
	if !strings.Contains(cases["--plain"].raw.String(), "request handled ok") {
		t.Error("--plain should still stream the log")
	}
	// run never pins: it has no service list to hold.
	_, fake := buildBins(t)
	t.Setenv("KESTREL_HOME", t.TempDir())
	tap := &screenTap{VT: termtest.New(90, 30)}
	cli.Execute(t.Context(), cli.Streams{In: strings.NewReader(""), Out: tap, Err: &bytes.Buffer{}, InIsTTY: true, OutIsTTY: true,
		TermSize: func() (int, int) { return 90, 30 }, Getenv: func(string) string { return "" },
		Getwd: func() (string, error) { return t.TempDir(), nil }}, []string{"run", "--", fake, "--exit-after", "200ms"})
	if strings.Contains(tap.raw.String(), "\x1b7") {
		t.Error("run should not pin a table")
	}
}
