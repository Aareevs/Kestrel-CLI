// Package rules holds Kestrel's diagnosis rules. Each rule is a small type
// with a stable ID (documented in docs/diagnosis-rules.md) that looks at one
// incident and proposes hypotheses. A hypothesis must cite the events that
// support it; the engine discards any that cite none.
package rules

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/correlation"
	"github.com/Aareevs/Kestrel-CLI/internal/events"
	"github.com/Aareevs/Kestrel-CLI/internal/graph"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Context is everything a rule may look at. Rules never read storage or the
// system themselves, which keeps them deterministic and easy to test.
type Context struct {
	Incident *correlation.Incident
	Graph    *graph.Graph
	// Events are the session's diagnostic events in seq order.
	Events []api.Event
	// Tail holds, per member service, the lines (any severity) leading up to
	// its first crash or startup failure.
	Tail map[string][]api.Event
	// GOOS selects platform-specific suggested commands (default: runtime.GOOS).
	GOOS string
}

// Rule proposes hypotheses for an incident.
type Rule interface {
	ID() string
	Evaluate(c *Context) []api.Hypothesis
}

// All returns every rule, in ID order.
func All() []Rule {
	return []Rule{PortConflict{}, DependencyDown{}, CrashAfterStart{}, StartupFailure{}, DependencyNotRunning{}, MissingModule{}}
}

func (c *Context) goos() string {
	if c.GOOS != "" {
		return c.GOOS
	}
	return runtime.GOOS
}

// eventsOf returns the session events of one service with the given types.
func (c *Context) eventsOf(svc string, types ...api.EventType) []api.Event {
	var out []api.Event
	for _, e := range c.Events {
		if e.ServiceID != svc {
			continue
		}
		for _, t := range types {
			if e.Type == t {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

// readyBefore returns the last readiness success of svc before seq.
func (c *Context) readyBefore(svc string, seq uint64) (api.Event, bool) {
	var found api.Event
	ok := false
	for _, e := range c.eventsOf(svc, api.EventProbeReady, api.EventProbeRecovered) {
		if e.Seq < seq {
			found, ok = e, true
		}
	}
	return found, ok
}

// firstOf returns the first anomaly of svc in the incident with one of types.
func firstOf(in *correlation.Incident, svc string, types ...api.EventType) (api.Event, bool) {
	for _, e := range in.ByService[svc] {
		for _, t := range types {
			if e.Type == t {
				return e, true
			}
		}
	}
	return api.Event{}, false
}

// cause returns the most telling error line in lines: the last fatal one,
// else the last error one.
func cause(lines []api.Event) (api.Event, bool) {
	var best api.Event
	found := false
	for _, e := range lines {
		if e.Type != api.EventLogLine || e.Severity.Rank() < api.SeverityError.Rank() {
			continue
		}
		if !found || e.Severity.Rank() >= best.Severity.Rank() {
			best, found = e, true
		}
	}
	return best, found
}

func shorten(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func evidence(e api.Event, summary string) api.Evidence {
	n := 1 + e.Attrs.Repeats
	if n == 1 {
		n = 0
	}
	return api.Evidence{EventID: e.ID, Time: e.Time, Service: e.ServiceID, Summary: summary, Count: n}
}

// collapsed turns repeated events (same fingerprint) into one evidence item
// that cites the first event and counts the rest.
func collapsed(evs []api.Event, summary func(api.Event) string) []api.Evidence {
	var out []api.Evidence
	index := map[string]int{}
	for _, e := range evs {
		key := e.ServiceID + "|" + string(e.Type) + "|" + e.Fingerprint
		if i, ok := index[key]; ok && e.Fingerprint != "" {
			if out[i].Count == 0 {
				out[i].Count = 1
			}
			out[i].Count += 1 + e.Attrs.Repeats
			continue
		}
		index[key] = len(out)
		out = append(out, evidence(e, summary(e)))
	}
	return out
}

// describe is a short, factual summary of an event for evidence lines.
func describe(e api.Event) string {
	switch e.Type {
	case api.EventProcessCrashed:
		return fmt.Sprintf("%s %s", e.ServiceID, strings.TrimPrefix(e.Message, "crashed "))
	case api.EventProcessExited:
		return e.ServiceID + " " + e.Message
	case api.EventStartupTimeout:
		return e.ServiceID + " never became ready"
	case api.EventProbeFailed:
		return e.ServiceID + " readiness probe failing"
	case api.EventProbeReady, api.EventProbeRecovered:
		return e.ServiceID + " was ready (probe passing)"
	case api.EventDependencyConnFailed:
		if t := targetOf(e); t != "" {
			return fmt.Sprintf("%s: %s %s", e.ServiceID, connVerb(e), t)
		}
	case api.EventPortBindFailed:
		if e.Attrs.Port > 0 {
			return fmt.Sprintf("%s: could not bind port %d", e.ServiceID, e.Attrs.Port)
		}
	}
	return e.ServiceID + ": " + shorten(e.Message, 90)
}

func connVerb(e api.Event) string {
	switch e.Attrs.PatternID {
	case "P007-host-not-found":
		return "host not found"
	case "P008-connection-timeout":
		return "connection timed out to"
	}
	return "connection refused to"
}

func targetOf(e api.Event) string {
	if e.Attrs.Target != "" {
		return e.Attrs.Target
	}
	if e.Attrs.Port > 0 {
		return ":" + strconv.Itoa(e.Attrs.Port)
	}
	return ""
}

func exitDetail(e api.Event) string {
	switch {
	case e.Attrs.Signal != "":
		return "was killed by " + e.Attrs.Signal
	case e.Attrs.ExitCode != nil:
		return fmt.Sprintf("exited with code %d", *e.Attrs.ExitCode)
	}
	return "exited"
}

func clock(t time.Time) string { return t.Local().Format("15:04:05") }

func lower(l api.Confidence) api.Confidence {
	switch l {
	case api.ConfidenceHigh:
		return api.ConfidenceMedium
	default:
		return api.ConfidenceLow
	}
}

func timelineStep(svc, why string) api.NextStep {
	return api.NextStep{Description: why, Command: "kestrel timeline --service " + svc}
}

func listenerCommand(goos string, port int) string {
	if goos == "windows" {
		return fmt.Sprintf("netstat -ano | findstr :%d", port)
	}
	return fmt.Sprintf("lsof -nP -iTCP:%d -sTCP:LISTEN", port)
}

// patternDescription names what a pattern recognized, for explanations.
func patternDescription(id string) string {
	if p := events.PatternByID(id); p != nil {
		return p.Description
	}
	return ""
}
