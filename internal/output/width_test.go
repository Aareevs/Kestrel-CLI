package output

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func TestFit(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is too long", 10, "this is t…"},
		{"word boundary  here", 10, "word boun…"},
		{"trailing space cut ", 12, "trailing sp…"},
		{"héllo wörld, ünïcode", 8, "héllo w…"}, // counts runes, not bytes
		{"anything", 0, "anything"},             // no limit
		{"anything", -3, "anything"},
		{"abc", 1, "…"},
	}
	for _, tt := range tests {
		got := Fit(tt.in, tt.max)
		if got != tt.want {
			t.Errorf("Fit(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
		}
		if tt.max > 0 && utf8.RuneCountInString(got) > tt.max {
			t.Errorf("Fit(%q, %d) = %q is %d columns", tt.in, tt.max, got, utf8.RuneCountInString(got))
		}
	}
	// The cut never leaves a dangling space before the ellipsis.
	if got := Fit("ab cd ef", 6); strings.Contains(got, " …") {
		t.Errorf("dangling space: %q", got)
	}
}

func TestWrap(t *testing.T) {
	got := Wrap("the quick brown fox jumps over the lazy dog", 16)
	want := []string{"the quick brown", "fox jumps over", "the lazy dog"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	// A word longer than the width stays whole (paths and URLs are never split).
	if got := Wrap("see /very/long/path/that/does/not/fit/anywhere ok", 10); strings.Join(got, "|") != "see|/very/long/path/that/does/not/fit/anywhere|ok" {
		t.Fatalf("long word: %q", got)
	}
	if got := Wrap("a   b\n c", 0); len(got) != 1 || got[0] != "a b c" {
		t.Fatalf("no width: %q", got)
	}
	if Wrap("   ", 10) != nil || Wrap("", 10) != nil {
		t.Fatal("blank text has no lines")
	}
	for _, line := range Wrap(strings.Repeat("word ", 40), 25) {
		if len(line) > 25 {
			t.Errorf("line over the width: %q", line)
		}
	}
}

func TestTidy(t *testing.T) {
	root, home := "/work/shop", "/home/ann"
	tests := []struct{ in, want string }{
		{"started (pid 1): /work/shop/bin/api --db /home/ann/data/x.db", "started (pid 1): ./bin/api --db ~/data/x.db"},
		{"in /work/shop", "in ."},
		{`cd "/work/shop" && run`, `cd "." && run`},
		{"/work/shop/a:/work/shop/b", "./a:./b"},
		// Only whole path elements: a longer name that merely starts with the root is left alone.
		{"/work/shopping/cart and /home/annabel/x", "/work/shopping/cart and /home/annabel/x"},
		{"nothing to change", "nothing to change"},
	}
	for _, tt := range tests {
		if got := Tidy(tt.in, root, home); got != tt.want {
			t.Errorf("Tidy(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	// An empty or root-level directory is never rewritten.
	if got := Tidy("/etc/x /usr/y", "/", ""); got != "/etc/x /usr/y" {
		t.Errorf("root dir: %q", got)
	}
}

func TestVisibleLenIgnoresStyling(t *testing.T) {
	s := NewStyle(true)
	if got := visibleLen(s.Red("abc") + " " + s.Bold(s.Dim("de"))); got != 6 {
		t.Fatalf("visibleLen = %d", got)
	}
}

func TestTerminalWidthIsZeroOffTerminal(t *testing.T) {
	var b bytes.Buffer
	if TerminalWidth(&b) != 0 || TerminalWidth(nil) != 0 {
		t.Fatal("a buffer is not a terminal")
	}
}

func liveEvent(msg string) api.Event {
	return api.Event{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), ServiceID: "api", Type: api.EventProcessStarted,
		Severity: api.SeverityInfo, Source: api.SourceSupervisor, Message: msg}
}

// On a terminal, a long `started` line is cut to the terminal's width with the
// project root and home abbreviated; anywhere else it is printed whole.
func TestLiveTidiesLifecycleLinesOnlyOnATerminal(t *testing.T) {
	long := "started (pid 4821): /work/shop/bin/server --config /home/ann/.config/shop/dev.yaml --flag " + strings.Repeat("x", 120)
	var plainOut, tty bytes.Buffer

	l := NewLive(&plainOut, NewStyle(false), false, false, []string{"api"})
	l.Emit(liveEvent(long))
	if !strings.Contains(plainOut.String(), long) {
		t.Fatalf("redirected output must keep the full line:\n%s", plainOut.String())
	}

	l = NewLive(&tty, NewStyle(false), false, false, []string{"api"})
	l.Tidy(80, "/work/shop", "/home/ann")
	l.Emit(liveEvent(long))
	got := strings.TrimRight(tty.String(), "\n")
	if n := utf8.RuneCountInString(got); n > 80 {
		t.Fatalf("line is %d columns on an 80-column terminal:\n%s", n, got)
	}
	// The root became ".", home became "~", and the cut falls at the terminal edge.
	if !strings.Contains(got, "▶ started (pid 4821): ./bin/server --config ~/.config/shop/") || !strings.HasSuffix(got, "…") || strings.Contains(got, "/work/shop") {
		t.Fatalf("tidied line:\n%s", got)
	}
	// Other lines are never touched.
	tty.Reset()
	l.Emit(api.Event{Time: time.Unix(0, 0), ServiceID: "api", Type: api.EventLogLine, Severity: api.SeverityInfo, Source: api.SourceStdout, Message: strings.Repeat("log ", 60)})
	if !strings.Contains(tty.String(), strings.Repeat("log ", 60)) {
		t.Fatal("log output must never be shortened")
	}
}

func probeFailed(msg string) api.Event {
	return api.Event{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), ServiceID: "api", Type: api.EventStartupTimeout,
		Severity: api.SeverityError, Source: api.SourceSupervisor, Message: msg}
}

// Kestrel's own messages word-wrap under their column on a terminal; off a
// terminal each is one line; program output is never wrapped.
func TestLiveWrapsKestrelMessagesButNotProgramOutput(t *testing.T) {
	msg := "never became ready: http http://127.0.0.1:18080/health (dial tcp 127.0.0.1:18080: connect: connection refused)"
	var plainOut, tty bytes.Buffer

	l := NewLive(&plainOut, NewStyle(false), false, false, []string{"api"})
	l.Emit(probeFailed(msg))
	if n := strings.Count(plainOut.String(), "\n"); n != 1 || !strings.Contains(plainOut.String(), msg) {
		t.Fatalf("off a terminal this is one whole line:\n%s", plainOut.String())
	}

	l = NewLive(&tty, NewStyle(false), false, false, []string{"api"})
	l.Tidy(60, "", "")
	l.Emit(probeFailed(msg))
	lines := strings.Split(strings.TrimRight(tty.String(), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected a wrapped message:\n%s", tty.String())
	}
	for i, line := range lines {
		if utf8.RuneCountInString(line) > 60 {
			t.Errorf("line %d is %d columns: %q", i, utf8.RuneCountInString(line), line)
		}
		if i > 0 && !strings.HasPrefix(line, strings.Repeat(" ", 17+2)) {
			t.Errorf("continuation line %d should hang under the message (col 19): %q", i, line)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(tty.String()), " "), "connection refused)") {
		t.Errorf("the message was cut instead of wrapped:\n%s", tty.String())
	}

	// A repeat summary is cut to fit; program output is not touched at all.
	tty.Reset()
	l.Emit(api.Event{Time: time.Unix(0, 0), ServiceID: "api", Type: api.EventLogLine, Severity: api.SeverityError, Source: api.SourceStderr,
		Message: strings.Repeat("dial tcp refused ", 8), Attrs: api.Attrs{Repeats: 14, DurationMS: 3200}})
	if got := strings.TrimRight(tty.String(), "\n"); utf8.RuneCountInString(got) > 60 || !strings.Contains(got, "↻ ×14 more over 3.2s:") || !strings.HasSuffix(got, "…") {
		t.Fatalf("repeat summary: %q", got)
	}
}

func TestStatusTableDetailFitsTheTerminal(t *testing.T) {
	svcs := []api.ServiceStatus{{Name: "api", Liveness: api.LivenessRunning, Readiness: api.ReadinessNotReady, PID: 42,
		Probe: "http http://127.0.0.1:18080/health", Detail: "dial tcp 127.0.0.1:18080: connect: connection refused"}}
	var narrow, plain bytes.Buffer
	StatusTable(&narrow, svcs, TextOptions{Style: NewStyle(false), Width: 70})
	StatusTable(&plain, svcs, TextOptions{Style: NewStyle(false)})
	for _, line := range strings.Split(narrow.String(), "\n") {
		if utf8.RuneCountInString(line) > 70 {
			t.Errorf("%d columns: %q", utf8.RuneCountInString(line), line)
		}
	}
	if !strings.Contains(plain.String(), "connection refused") || !strings.Contains(narrow.String(), "…") {
		t.Errorf("plain output keeps the whole detail; a narrow terminal is cut:\n%s\n%s", plain.String(), narrow.String())
	}
}
