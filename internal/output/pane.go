package output

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Pane is a service table pinned to the top of the terminal while logs scroll
// below it. The top rows are reserved with a scroll region (DECSTBM), so the
// log lines the renderer writes never touch the table, and a redraw saves and
// restores the cursor so it never disturbs the log. The table follows the
// events it is shown: a start, a probe passing or failing, a crash.
//
// It is built for a real terminal only (the caller checks); Close always puts
// the terminal back, and a terminal that is too small simply isn't pinned.
type Pane struct {
	w     io.Writer
	style Style
	names []string
	size  func() (cols, rows int)
	now   func() time.Time

	svc        map[string]*paneService
	cols, rows int
	active     bool
}

type paneService struct {
	state   string // waiting, running, ready, not ready, crashed, exited
	started time.Time
	ended   time.Time
	errors  int
	detail  string
}

const (
	maxPaneServices = 8
	minPaneRows     = 6 // log lines that must remain below the table
	minPaneCols     = 50
	maxNameCols     = 16
)

// NewPane returns a pane for the named services. size reports the terminal's
// current columns and rows (0, 0 when unknown).
func NewPane(w io.Writer, style Style, names []string, size func() (int, int), now func() time.Time) *Pane {
	p := &Pane{w: w, style: style, names: names, size: size, now: now, svc: map[string]*paneService{}}
	for _, n := range names {
		p.svc[n] = &paneService{state: "waiting"}
	}
	return p
}

// height is the table's lines: header, one per service (capped, with a "more"
// line when cut), and a rule under them.
func (p *Pane) height() int {
	n := len(p.names)
	if n > maxPaneServices {
		return 1 + maxPaneServices + 1 + 1
	}
	return 1 + n + 1
}

// fits says whether a terminal of cols x rows has room for the table and some
// log below it.
func (p *Pane) fits(cols, rows int) bool {
	return cols >= minPaneCols && rows >= p.height()+minPaneRows
}

// Start reserves the top rows and returns whether the pane is on. When the
// terminal is too small (or its size is unknown) nothing is written and the
// caller carries on with plain lines.
func (p *Pane) Start() bool {
	cols, rows := p.size()
	if !p.fits(cols, rows) {
		return false
	}
	p.cols, p.rows, p.active = cols, rows, true
	// Scroll what is on screen into the scrollback (nothing is erased), then
	// reserve the top rows and put the cursor under them.
	var b strings.Builder
	b.WriteString(strings.Repeat("\n", rows))
	p.frame(&b, true)
	fmt.Fprintf(&b, "\x1b[%d;1H", p.height()+1) // the log starts right under the table
	io.WriteString(p.w, b.String())
	return true
}

// Active reports whether the pane is currently pinned.
func (p *Pane) Active() bool { return p.active }

// Observe updates the table from one event and reports whether it changed.
func (p *Pane) Observe(ev api.Event) bool {
	s := p.svc[ev.ServiceID]
	if s == nil {
		return false
	}
	before := *s
	switch ev.Type {
	case api.EventProcessStarted:
		s.state, s.started, s.ended, s.detail = "running", ev.Time, time.Time{}, "started"
	case api.EventProbeReady, api.EventProbeRecovered:
		s.state, s.detail = "ready", ev.Message
	case api.EventProbeFailed, api.EventStartupTimeout:
		s.state, s.detail = "not ready", ev.Message
	case api.EventProcessCrashed:
		s.state, s.ended, s.detail = "crashed", ev.Time, ev.Message
	case api.EventProcessExited:
		s.state, s.ended, s.detail = "exited", ev.Time, ev.Message
	}
	if ev.Severity.Rank() >= api.SeverityError.Rank() {
		s.errors++
		if ev.Type == api.EventLogLine {
			s.detail = ev.Message
		}
	}
	return *s != before
}

// Redraw repaints the table (also what keeps uptimes ticking). If the terminal
// was resized it re-reserves the rows, or lets go when it became too small.
func (p *Pane) Redraw() {
	if !p.active {
		return
	}
	cols, rows := p.size()
	var b strings.Builder
	if cols != p.cols || rows != p.rows {
		if !p.fits(cols, rows) {
			p.active = false
			b.WriteString("\x1b[r")
			io.WriteString(p.w, b.String())
			return
		}
		p.cols, p.rows = cols, rows
		p.frame(&b, true)
	} else {
		p.frame(&b, false)
	}
	io.WriteString(p.w, b.String())
}

// Close gives the terminal back: the scroll region is removed and the cursor
// left at the start of the last row, below the table, which stays on screen.
func (p *Pane) Close() {
	if !p.active {
		return
	}
	p.active = false
	_, rows := p.size()
	if rows == 0 {
		rows = p.rows
	}
	fmt.Fprintf(p.w, "\x1b[r\x1b[%d;1H\r", rows)
}

// frame writes the table at the top of the screen, keeping the cursor where it
// was. With region it also (re)sets the scroll region below the table.
func (p *Pane) frame(b *strings.Builder, region bool) {
	if region {
		fmt.Fprintf(b, "\x1b[%d;%dr", p.height()+1, p.rows)
	}
	b.WriteString("\x1b7")
	for i, line := range p.lines() {
		fmt.Fprintf(b, "\x1b[%d;1H\x1b[2K%s", i+1, line)
	}
	b.WriteString("\x1b8")
}

// lines is the table, each line fitted to the terminal width.
func (p *Pane) lines() []string {
	s := p.style
	names := p.names
	more := 0
	if len(names) > maxPaneServices {
		more, names = len(names)-maxPaneServices, names[:maxPaneServices]
	}
	nameW := len("SERVICE")
	for _, n := range names {
		nameW = max(nameW, min(utf8.RuneCountInString(n), maxNameCols))
	}
	const stateW, upW, errW = 11, 7, 4
	fixed := 2 + nameW + 2 + stateW + 2 + upW + 2 + errW + 2
	fit := func(text string) string { return Fit(text, max(p.cols-fixed, 4)) }

	out := []string{s.Dim(fmt.Sprintf("  %-*s  %-*s  %*s  %*s  %s", nameW, "SERVICE", stateW, "STATE", upW, "UP", errW, "ERR", "DETAIL"))}
	for _, n := range names {
		v := p.svc[n]
		mark, color := "◌", s.Dim
		switch v.state {
		case "running":
			mark, color = "●", s.Cyan
		case "ready":
			mark, color = "●", s.Green
		case "not ready":
			mark, color = "▲", s.Yellow
		case "crashed":
			mark, color = "✗", s.Red
		case "exited":
			mark, color = "■", s.Dim
		}
		state := color(mark + " " + v.state)
		state += strings.Repeat(" ", stateW-visibleLen(state))
		up := "-"
		if !v.started.IsZero() {
			end := p.now()
			if !v.ended.IsZero() {
				end = v.ended
			}
			up = humanDuration(max(end.Sub(v.started), 0))
		}
		errs := fmt.Sprintf("%*d", errW, v.errors)
		if v.errors > 0 {
			errs = s.Red(errs)
		} else {
			errs = s.Dim(errs)
		}
		out = append(out, fmt.Sprintf("  %-*s  %s  %*s  %s  %s", nameW, Fit(n, nameW), state, upW, up, errs, s.Dim(fit(v.detail))))
	}
	if more > 0 {
		out = append(out, s.Dim(fmt.Sprintf("  … and %d more service%s", more, plural(more))))
	}
	rule := max(min(p.cols, 100)-2, 1)
	out = append(out, s.Dim("  "+strings.Repeat("─", rule)))
	return out
}
