package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Aareevs/Kestrel-CLI/internal/app"
	"github.com/Aareevs/Kestrel-CLI/internal/output"
	"github.com/Aareevs/Kestrel-CLI/internal/picker"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// canPick reports whether pickers can be shown: both stdin and stdout must be
// an interactive terminal.
func (g *globals) canPick() bool { return g.streams.InIsTTY && g.streams.OutIsTTY }

// maxPickSessions bounds the session picker's list.
const maxPickSessions = 30

// choose resolves the session and (when withIncident) the incident for a
// command run with -i. Without a terminal there is no picker: it says so and
// falls back to the usual defaults, so a script that passes -i keeps working.
// A session given as an argument or with --session is never second-guessed.
func (g *globals) choose(ctx context.Context, a *app.App, args []string, all, interactive bool, incident int, withIncident bool) (ref string, inc int, err error) {
	ref, inc = g.sessionRef(args), incident
	if !interactive {
		return ref, inc, nil
	}
	if !g.canPick() {
		fmt.Fprintln(g.streams.Err, "kestrel: not a terminal, so no picker: using the latest session")
		return ref, inc, nil
	}
	if len(args) == 0 && g.session == "" {
		if ref, err = g.pickSession(ctx, a, all); err != nil {
			return "", 0, err
		}
	}
	if withIncident && incident == 0 {
		if inc, err = g.pickIncident(ctx, a, ref, all); err != nil {
			return "", 0, err
		}
	}
	return ref, inc, nil
}

// pick runs the picker on the terminal and maps a cancel to a quiet exit.
func (g *globals) pick(title string, items []picker.Item) (int, error) {
	rows := output.TerminalHeight(g.streams.Out)
	height := 12
	if rows > 0 {
		height = max(3, min(rows-4, 15)) // title, scroll hints, help line, prompt
	}
	if g.keys == nil {
		g.keys = bufio.NewReader(g.streams.In)
	}
	i, err := picker.Pick(g.keys, g.streams.Out, picker.Options{
		Title: title, Items: items, Style: g.style, Width: output.TerminalWidth(g.streams.Out), Height: height, Raw: g.streams.MakeRaw,
	})
	if errors.Is(err, picker.ErrCancelled) {
		return 0, &ExitError{Code: 130} // like Ctrl-C: quiet
	}
	return i, err
}

func (g *globals) pickSession(ctx context.Context, a *app.App, all bool) (string, error) {
	sessions, err := a.PickableSessions(ctx, all, maxPickSessions)
	if err != nil {
		return "", appErr(err)
	}
	i, err := g.pick("Pick a session", sessionItems(sessions))
	if err != nil {
		return "", err
	}
	return sessions[i].ID, nil
}

func (g *globals) pickIncident(ctx context.Context, a *app.App, ref string, all bool) (int, error) {
	rep, err := a.Timeline(ctx, ref, all, app.TimelineOptions{})
	if err != nil {
		return 0, appErr(err)
	}
	if len(rep.Incidents) < 2 {
		return 0, nil // nothing to choose between
	}
	i, err := g.pick("Pick an incident", incidentItems(rep.Incidents))
	if err != nil {
		return 0, err
	}
	if i == 0 {
		return 0, nil // "all incidents"
	}
	return rep.Incidents[i-1].Number, nil
}

// sessionItems describes sessions for the picker: when, then what happened.
func sessionItems(sessions []api.SessionSummary) []picker.Item {
	items := make([]picker.Item, len(sessions))
	for i, s := range sessions {
		when := s.StartedAt.Local().Format("Jan 02 15:04")
		parts := []string{s.Mode, s.Status}
		if !s.EndedAt.IsZero() {
			parts = append(parts, s.EndedAt.Sub(s.StartedAt).Round(time.Second).String())
		}
		parts = append(parts, strings.Join(s.Services, ", "), fmt.Sprintf("%d %s", s.Events, plural(s.Events, "event", "events")))
		if s.Errors > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", s.Errors, plural(s.Errors, "error", "errors")))
		}
		parts = append(parts, "…"+tail(s.ID, 6))
		items[i] = picker.Item{Label: when, Detail: strings.Join(parts, " · ")}
	}
	return items
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// incidentItems describes incidents for the picker, after an "all" row.
func incidentItems(incidents []api.Incident) []picker.Item {
	items := []picker.Item{{Label: "All", Detail: fmt.Sprintf("%d incidents", len(incidents))}}
	for _, in := range incidents {
		detail := fmt.Sprintf("%d %s", len(in.Services), plural(len(in.Services), "service", "services"))
		if in.InitiatingKnown {
			detail += " · " + in.InitiatingService + " first"
		} else {
			detail += " · initiating failure unknown"
		}
		detail += " · correlation " + in.CorrelationStrength
		items = append(items, picker.Item{Label: fmt.Sprintf("#%d %s", in.Number, in.Start.Local().Format("15:04:05")), Detail: detail})
	}
	return items
}
