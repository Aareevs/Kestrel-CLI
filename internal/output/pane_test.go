package output

import (
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Aareevs/Kestrel-CLI/internal/termtest"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func paneEvents() []api.Event {
	t0 := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	ev := func(sec int, svc string, typ api.EventType, sev api.Severity, msg string) api.Event {
		return api.Event{Time: t0.Add(time.Duration(sec) * time.Second), ServiceID: svc, Type: typ, Severity: sev, Message: msg}
	}
	return []api.Event{
		ev(0, "api", api.EventProcessStarted, api.SeverityInfo, "started"),
		ev(0, "db", api.EventProcessStarted, api.SeverityInfo, "started"),
		ev(2, "db", api.EventProbeReady, api.SeverityInfo, "db is ready"),
		ev(5, "api", api.EventLogLine, api.SeverityError, "connection refused"),
		ev(9, "api", api.EventProcessCrashed, api.SeverityError, "crashed (exit code 1)"),
	}
}

func newTestPane(w *termtest.VT, names []string) *Pane {
	t0 := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	return NewPane(w, NewStyle(false), names, w.Size, func() time.Time { return t0.Add(12 * time.Second) })
}

func TestPaneStaysPinnedWhileLogsScroll(t *testing.T) {
	term := termtest.New(70, 14)
	term.Write([]byte("earlier output\nmore earlier\n")) // already on screen before watch starts
	p := newTestPane(term, []string{"api", "db"})
	live := NewLive(term, NewStyle(false), false, false, []string{"api", "db"})
	if !live.Pin(p) {
		t.Fatal("pane should fit a 70x14 terminal")
	}
	for _, e := range paneEvents() {
		live.Emit(e)
	}
	// Far more log lines than the screen has rows.
	for i := 0; i < 40; i++ {
		live.Emit(api.Event{Time: paneEvents()[0].Time, ServiceID: "db", Type: api.EventLogLine, Severity: api.SeverityInfo, Source: api.SourceStdout, Message: "log line number " + strconv.Itoa(i)})
	}
	live.Close()

	scr := term.Text()
	lines := strings.Split(scr, "\n")
	// The table is on the first rows, intact, with the right states.
	for i, want := range []string{"SERVICE", "api", "db", "───"} {
		if !strings.Contains(lines[i], want) {
			t.Fatalf("row %d should hold %q:\n%s", i, want, scr)
		}
	}
	if !strings.Contains(lines[1], "✗ crashed") || !strings.Contains(lines[1], "9s") || !strings.Contains(lines[1], "crashed (exit code 1)") || !strings.Contains(lines[1], "2") {
		t.Errorf("api row: %q", lines[1])
	}
	if !strings.Contains(lines[2], "● ready") || !strings.Contains(lines[2], "db is ready") {
		t.Errorf("db row: %q", lines[2])
	}
	// The newest log lines fill everything below, and the log never touched the table.
	// (the last row is the empty one the cursor waits on)
	if lines[len(lines)-1] != "" || !strings.Contains(lines[len(lines)-2], "log line number 39") || !strings.Contains(scr, "log line number 31") {
		t.Errorf("log should scroll in the lower rows:\n%s", scr)
	}
	for _, l := range lines[:4] {
		if strings.Contains(l, "log line") {
			t.Errorf("a log line landed in the table: %q", l)
		}
	}
	// What was on the screen before is in the scrollback, not erased.
	if h := strings.Join(term.History(), "\n"); !strings.Contains(h, "earlier output") {
		t.Errorf("earlier output was lost: %q", h)
	}
	// The terminal is given back: no scroll region is left behind.
	if top, bottom := term.Region(); top != 0 || bottom != 13 {
		t.Errorf("scroll region left at %d-%d", top, bottom)
	}
}

func TestPaneUptimeTicksAndResizes(t *testing.T) {
	term := termtest.New(70, 14)
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	p := NewPane(term, NewStyle(false), []string{"api"}, term.Size, func() time.Time { return now })
	if !p.Start() {
		t.Fatal("start")
	}
	p.Observe(api.Event{Time: now, ServiceID: "api", Type: api.EventProcessStarted})
	p.Redraw()
	if !strings.Contains(term.Line(1), "<1s") {
		t.Fatalf("row: %q", term.Line(1))
	}
	now = now.Add(83 * time.Second) // a repaint with nothing new still moves the clock
	p.Redraw()
	if !strings.Contains(term.Line(1), "1m23s") {
		t.Fatalf("uptime did not advance: %q", term.Line(1))
	}
	// Shrinking below what the table needs lets the pane go, and Redraw is then a no-op.
	term.Resize(70, 5)
	p.Redraw()
	if top, bottom := term.Region(); p.Active() || top != 0 || bottom != 4 {
		t.Errorf("pane still active on a tiny terminal (region %d-%d)", top, bottom)
	}
	// An unchanged event doesn't ask for a repaint.
	if p.Observe(api.Event{ServiceID: "nobody", Type: api.EventProcessStarted}) {
		t.Error("an unknown service should change nothing")
	}
}

func TestPaneIsNotPinnedOnASmallTerminal(t *testing.T) {
	for _, size := range [][2]int{{70, 8}, {45, 30}, {0, 0}} {
		term := termtest.New(max(size[0], 10), max(size[1], 3))
		p := NewPane(term, NewStyle(false), []string{"api", "db"}, func() (int, int) { return size[0], size[1] }, time.Now)
		if p.Start() || term.Text() != termtest.New(max(size[0], 10), max(size[1], 3)).Text() || len(term.History()) != 0 {
			t.Errorf("%v: a too-small terminal must be left untouched", size)
		}
	}
}

func TestPaneManyServicesAreCapped(t *testing.T) {
	var names []string
	for i := 0; i < 12; i++ {
		names = append(names, "svc"+strconv.Itoa(i))
	}
	term := termtest.New(70, 30)
	p := newTestPane(term, names)
	if !p.Start() {
		t.Fatal("start")
	}
	if h := p.height(); h != 1+8+1+1 {
		t.Errorf("height = %d", h)
	}
	if !strings.Contains(term.Text(), "… and 4 more services") {
		t.Errorf("no overflow line:\n%s", term.Text())
	}
	for _, l := range strings.Split(term.Text(), "\n") {
		if utf8.RuneCountInString(l) > 70 {
			t.Errorf("line over 70 columns: %q", l)
		}
	}
}
