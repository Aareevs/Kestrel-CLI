package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Live renders the event stream of a running session: one line per event,
// prefixed with time and service. In JSON mode it writes one event per line
// (NDJSON) for tools and agents.
type Live struct {
	mu     sync.Mutex
	w      io.Writer
	style  Style
	quiet  bool
	json   bool
	width  int
	colors map[string]func(string) string
	enc    *json.Encoder
}

// NewLive returns a renderer. names sets the column width and color order.
func NewLive(w io.Writer, style Style, quiet, jsonMode bool, names []string) *Live {
	l := &Live{w: w, style: style, quiet: quiet, json: jsonMode, colors: map[string]func(string) string{}}
	palette := []func(string) string{style.Cyan, style.Yellow, style.Green, style.magenta, style.blue}
	l.width = len("kestrel")
	for i, n := range names {
		l.colors[n] = palette[i%len(palette)]
		if len(n) > l.width {
			l.width = len(n)
		}
	}
	if jsonMode {
		l.enc = json.NewEncoder(w)
		l.enc.SetEscapeHTML(false)
	}
	return l
}

func (s Style) magenta(v string) string { return s.wrap("35", v) }
func (s Style) blue(v string) string    { return s.wrap("34", v) }

// Emit renders one event. Write errors (e.g. a closed pipe) are ignored;
// rendering must never stop supervision.
func (l *Live) Emit(ev api.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.json {
		_ = l.enc.Encode(ev)
		return
	}
	if l.quiet && !important(ev) {
		return
	}
	s := l.style
	name := ev.ServiceID
	if name == "" {
		name = "kestrel"
	}
	color := l.colors[name]
	if color == nil {
		color = s.Dim
	}
	prefix := s.Dim(ev.Time.Local().Format("15:04:05")) + " " + color(fmt.Sprintf("%-*s", l.width, name)) + " "

	var line string
	switch ev.Type {
	case api.EventProcessStarted:
		line = s.Cyan("▶ ") + ev.Message
	case api.EventProcessExited:
		line = s.Dim("■ " + ev.Message)
	case api.EventProcessCrashed:
		line = s.Red("✗ " + ev.Message)
	case api.EventProbeReady, api.EventProbeRecovered:
		line = s.Green("✓ ") + ev.Message
	case api.EventProbeFailed, api.EventStartupTimeout:
		line = s.Red("✗ " + ev.Message)
	case api.EventCollectorOverload:
		line = s.Yellow(fmt.Sprintf("! dropped %d debug/info lines: output arrived faster than it could be shown (errors are never dropped)", ev.Attrs.Dropped))
	default:
		if ev.Source == api.SourceSupervisor || ev.Source == api.SourceKestrel {
			mark := s.Dim("· ")
			if ev.Severity.Rank() >= api.SeverityWarn.Rank() {
				mark = s.Yellow("! ")
			}
			line = mark + ev.Message
			break
		}
		bar := s.Dim("│ ")
		switch {
		case ev.Severity.Rank() >= api.SeverityError.Rank():
			bar = s.Red("│ ")
		case ev.Severity == api.SeverityWarn:
			bar = s.Yellow("│ ")
		}
		if ev.Attrs.Repeats > 0 {
			// Streams fold separately, so name the line rather than say "previous".
			msg := ev.Message
			if r := []rune(msg); len(r) > 60 {
				msg = string(r[:59]) + "…"
			}
			line = bar + s.Dim(fmt.Sprintf("↻ ×%d more over %s: %s", ev.Attrs.Repeats, (time.Duration(ev.Attrs.DurationMS)*time.Millisecond).Round(100*time.Millisecond), msg))
		} else {
			line = bar + ev.Message + annotation(s, ev)
		}
	}
	fmt.Fprintln(l.w, prefix+line)
}

// important is what --quiet keeps: warnings and above, and lifecycle.
func important(ev api.Event) bool {
	if ev.Severity.Rank() >= api.SeverityWarn.Rank() {
		return true
	}
	switch ev.Type {
	case api.EventProcessStarted, api.EventProcessExited, api.EventProbeReady, api.EventProbeRecovered:
		return true
	}
	return false
}

// annotation shows how Kestrel classified a line, so the evidence behind
// later diagnosis is visible as it happens.
func annotation(s Style, ev api.Event) string {
	if ev.Attrs.PatternID == "" || ev.Attrs.PatternID == "L001-listening" {
		return ""
	}
	var parts []string
	if ev.Type != api.EventLogLine {
		parts = append(parts, string(ev.Type))
	}
	if ev.Attrs.Target != "" {
		parts = append(parts, ev.Attrs.Target)
	} else if ev.Attrs.Port != 0 {
		parts = append(parts, ":"+strconv.Itoa(ev.Attrs.Port))
	}
	parts = append(parts, ev.Attrs.PatternID)
	return "  " + s.Dim("‹"+strings.Join(parts, " ")+"›")
}

// StatusTable renders `kestrel status` and the end-of-session summary.
func StatusTable(w io.Writer, services []api.ServiceStatus, o TextOptions) {
	s := o.Style
	width := 7
	for _, sv := range services {
		width = max(width, len(sv.Name))
	}
	fmt.Fprintf(w, "  %-*s  %-12s %-10s %s\n", width, "SERVICE", "LIVENESS", "READINESS", "DETAIL")
	for _, sv := range services {
		live := string(sv.Liveness)
		switch sv.Liveness {
		case api.LivenessRunning:
			live = s.Green(fmt.Sprintf("%-12s", live))
		case api.LivenessCrashed:
			live = s.Red(fmt.Sprintf("%-12s", live))
		default:
			live = s.Dim(fmt.Sprintf("%-12s", live))
		}
		ready := string(sv.Readiness)
		switch sv.Readiness {
		case api.ReadinessReady:
			ready = s.Green(fmt.Sprintf("%-10s", ready))
		case api.ReadinessNotReady:
			ready = s.Red(fmt.Sprintf("%-10s", ready))
		default:
			ready = s.Dim(fmt.Sprintf("%-10s", ready))
		}
		var detail []string
		if sv.PID > 0 && sv.Liveness == api.LivenessRunning {
			detail = append(detail, "pid "+strconv.Itoa(sv.PID))
		}
		if sv.Probe != "" {
			detail = append(detail, sv.Probe)
		}
		if sv.Detail != "" {
			detail = append(detail, sv.Detail)
		}
		fmt.Fprintf(w, "  %-*s  %s %s %s\n", width, sv.Name, live, ready, strings.Join(detail, " · "))
	}
}
