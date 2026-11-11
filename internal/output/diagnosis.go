package output

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// shortID shows the tail of a ULID: enough to tell events apart on screen.
// --format json always carries the full IDs.
func shortID(id string) string {
	if len(id) <= 6 {
		return id
	}
	return "…" + id[len(id)-6:]
}

func hm(t time.Time) string { return t.Local().Format("15:04:05") }

func (o TextOptions) level(c api.Confidence) string {
	switch c {
	case api.ConfidenceHigh:
		return o.Style.Green(string(c))
	case api.ConfidenceMedium:
		return o.Style.Yellow(string(c))
	}
	return o.Style.Dim(string(c))
}

func sessionLine(w io.Writer, s api.SessionSummary, o TextOptions) {
	end := "running"
	if !s.EndedAt.IsZero() {
		end = hm(s.EndedAt)
	}
	segs := []string{o.Style.Bold("Session") + " " + s.ID, s.Mode, o.status(s.Status), hm(s.StartedAt) + "–" + end, strings.Join(s.Services, ", ")}
	if o.Width <= 0 || visibleLen(strings.Join(segs, " · ")) <= o.Width {
		fmt.Fprintln(w, strings.Join(segs, " · "))
		return
	}
	// Too narrow for one line: pack the segments, services last.
	cur := ""
	for _, seg := range segs {
		switch {
		case cur == "":
			cur = seg
		case visibleLen(cur)+3+visibleLen(seg) <= o.Width:
			cur += " · " + seg
		default:
			fmt.Fprintln(w, cur)
			cur = "  " + seg
		}
	}
	fmt.Fprintln(w, cur)
}

func plural2(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// packSegments joins segments with a separator on one line, or, when a terminal
// is too narrow for that, packs them onto as few lines as fit.
func packSegments(segs []string, width int) []string {
	one := strings.Join(segs, "  ·  ")
	if width <= 0 || visibleLen(one) <= width {
		return []string{one}
	}
	var lines []string
	cur := ""
	for _, seg := range segs {
		switch {
		case cur == "":
			cur = seg
		case visibleLen(cur)+3+visibleLen(seg) <= width:
			cur += " · " + seg
		default:
			lines = append(lines, cur)
			cur = seg
		}
	}
	return append(lines, cur)
}

func incidentHeader(w io.Writer, in api.Incident, o TextOptions) {
	s := o.Style
	fmt.Fprintln(w)
	for _, line := range packSegments([]string{
		s.Bold(fmt.Sprintf("Incident #%d", in.Number)),
		fmt.Sprintf("%d %s affected", len(in.Services), plural2(len(in.Services), "service", "services")),
		"started " + hm(in.Start),
		"correlation " + in.CorrelationStrength,
	}, o.Width) {
		fmt.Fprintln(w, line)
	}
	if in.InitiatingKnown {
		fmt.Fprintf(w, "  Initiating failure: %s %s\n", s.Bold(in.InitiatingService), s.Dim("(evt "+shortID(in.InitiatingEventID)+")"))
	} else {
		fmt.Fprintf(w, "  Initiating failure: %s. %s\n", s.Yellow("unknown"), in.InitiatingNote)
	}
}

func evidenceLine(w io.Writer, mark, indent string, e api.Evidence, o TextOptions) {
	text := e.Summary
	count := ""
	if e.Count > 1 {
		count = fmt.Sprintf(" ×%d", e.Count)
	}
	id := "evt " + shortID(e.EventID)
	col := 58 // the column the ID aligns to
	if o.Width > 0 {
		// Keep the evidence ID on the same line: the text (and its repeat
		// count) must fit beside it.
		col = min(col, max(o.Width-len([]rune(indent))-2-len([]rune(id))-2, 24))
		text = Fit(text, max(col-len([]rune(count))-2, 12))
	}
	text += count
	pad := col - len([]rune(text))
	if pad < 2 {
		pad = 2
	}
	fmt.Fprintf(w, "%s%s %s%s%s\n", indent, mark, text, strings.Repeat(" ", pad), o.Style.Dim(id))
}

func hypothesisBody(w io.Writer, h api.Hypothesis, indent string, maxEvidence int, o TextOptions) {
	s := o.Style
	for i, e := range h.Supporting {
		if maxEvidence > 0 && i == maxEvidence {
			fmt.Fprintf(w, "%s%s\n", indent, s.Dim(fmt.Sprintf("  … %d more (see --format json)", len(h.Supporting)-maxEvidence)))
			break
		}
		evidenceLine(w, s.Green("✓"), indent, e, o)
	}
	if len(h.Contradicting) == 0 {
		fmt.Fprintf(w, "%s%s %s\n", indent, s.Dim("✗"), s.Dim("no contradicting evidence found"))
	}
	for _, e := range h.Contradicting {
		evidenceLine(w, s.Red("✗"), indent, e, o)
	}
}

// Diagnosis renders `kestrel diagnose`.
func Diagnosis(w io.Writer, r *api.DiagnosisReport, o TextOptions) {
	s := o.Style
	sessionLine(w, r.Session, o)
	if r.Note != "" {
		wrapped(w, s.Yellow("!")+" ", "  ", r.Note, o, plain)
	}
	if len(r.Incidents) == 0 {
		fmt.Fprintf(w, "\nNo incidents: nothing in this session failed. Warnings alone don't make an incident.\n")
		return
	}
	for _, d := range r.Incidents {
		incidentHeader(w, d.Incident, o)
		if len(d.Hypotheses) == 0 {
			fmt.Fprintf(w, "\n  %s no rule matched this incident\n", s.Yellow("Unresolved:"))
		}
		for i, h := range d.Hypotheses {
			switch {
			case i == 0 && d.Status == api.ClaimHypothesis:
				fmt.Fprintf(w, "\n  %s  [%s · %s]\n", s.Bold("Likely initiating failure"), h.Claim, o.level(h.Confidence.Level))
				fmt.Fprintf(w, "  %s\n", s.Bold(h.Title))
				hypothesisBody(w, h, "    ", 0, o)
				wrapped(w, "    ", "        ", fmt.Sprintf("%s because: %s", h.Confidence.Level, strings.Join(h.Confidence.Factors, "; ")), o, s.Dim)
				fmt.Fprintf(w, "    %s\n", s.Dim("rule "+h.RuleID))
			case i == 0:
				fmt.Fprintf(w, "\n  %s %s\n", s.Yellow("Unresolved:"), "competing hypotheses, ranked by evidence")
				fallthrough
			default:
				if i == 1 && d.Status == api.ClaimHypothesis {
					fmt.Fprintf(w, "\n  %s\n", s.Bold("Other hypotheses"))
				}
				title := h.Title
				if o.Width > 0 { // keep the rule ID visible: shorten the title, not the ID
					fixed := len(fmt.Sprintf("  %d. [%s · %s]  ", h.Rank, h.Claim, h.Confidence.Level)) + len(h.RuleID)
					title = Fit(title, max(o.Width-fixed, 20))
				}
				fmt.Fprintf(w, "  %d. [%s · %s] %s  %s\n", h.Rank, h.Claim, o.level(h.Confidence.Level), title, s.Dim(h.RuleID))
				hypothesisBody(w, h, "       ", 3, o)
			}
		}
		if len(d.Symptoms) > 0 {
			width := 0
			for _, sy := range d.Symptoms {
				width = max(width, len(sy.Service))
			}
			fmt.Fprintf(w, "\n  %s  [%s]\n", s.Bold("Downstream symptoms"), api.ClaimCorrelated)
			for _, sy := range d.Symptoms {
				fmt.Fprintf(w, "    %-*s  %s\n", width, sy.Service, sy.Summary)
			}
		}
		fmt.Fprintf(w, "\n  %s\n", s.Bold("Next step"))
		wrapped(w, "    ", "    ", d.NextStep.Description, o, plain)
		if d.NextStep.Command != "" {
			fmt.Fprintf(w, "    %s %s\n", s.Dim("$"), s.Cyan(d.NextStep.Command))
		}
	}
	if !o.Quiet {
		fmt.Fprintln(w)
		wrapped(w, "", "", "Kestrel only suggests commands; it never runs them. Full evidence: --format json", o, s.Dim)
	}
}

var roleLabel = map[string]string{
	api.RoleHealthy:      "last healthy",
	api.RoleFirstAnomaly: "first anomaly",
	api.RoleKey:          "key",
	api.RoleSymptom:      "symptom",
	api.RoleRecovery:     "recovery",
	api.RoleOutput:       "",
}

func (o TextOptions) roleMark(e api.TimelineEntry) string {
	s := o.Style
	switch e.Role {
	case api.RoleHealthy:
		return s.Green("●")
	case api.RoleRecovery:
		return s.Green("✓")
	case api.RoleSymptom:
		return s.Yellow("↳")
	case api.RoleFirstAnomaly, api.RoleKey:
		return s.Red("✗")
	}
	switch {
	case e.Severity.Rank() >= api.SeverityError.Rank():
		return s.Red("│")
	case e.Severity == api.SeverityWarn:
		return s.Yellow("│")
	}
	return s.Dim("│")
}

func timelineRows(w io.Writer, entries []api.TimelineEntry, showService, showRole bool, o TextOptions) {
	width := 0
	for _, e := range entries {
		width = max(width, len(e.Service))
	}
	// Message limit: 100 columns when output isn't a terminal (stable text);
	// otherwise whatever the terminal leaves after the fixed columns.
	fixed := 2 + 17 + 2 + 2 // indent, time range, gap, mark
	if showService {
		fixed += width + 2
	}
	if showRole {
		fixed += 14
	}
	for _, e := range entries {
		when := hm(e.Time)
		if !e.Until.IsZero() && hm(e.Until) != when {
			when += "–" + hm(e.Until)
		} else {
			when += strings.Repeat(" ", 9)
		}
		msg := e.Message
		limit := 100
		if o.Width > 0 {
			limit = max(o.Width-fixed-7, 20) // 7: room for the "  ×N" repeat count
		}
		msg = Fit(msg, limit)
		if e.Count > 1 {
			msg += o.Style.Dim(fmt.Sprintf("  ×%d", e.Count))
		}
		line := "  " + o.Style.Dim(when) + "  "
		if showService {
			line += fmt.Sprintf("%-*s  ", width, e.Service)
		}
		line += o.roleMark(e) + " "
		if showRole {
			label := roleLabel[e.Role]
			if e.Role == api.RoleHealthy && e.Type == api.EventProcessStarted {
				label = "started" // the last known good state, but not proof of health
			}
			line += fmt.Sprintf("%-13s ", label)
		}
		fmt.Fprintln(w, line+msg)
	}
}

// Timeline renders `kestrel timeline`.
func Timeline(w io.Writer, r *api.TimelineReport, o TextOptions) {
	s := o.Style
	sessionLine(w, r.Session, o)
	if !r.Since.IsZero() {
		fmt.Fprintf(w, "%s\n", s.Dim("showing events since "+hm(r.Since)))
	}
	if r.Service != "" {
		fmt.Fprintf(w, "\n%s %s\n", s.Bold(r.Service), s.Dim(fmt.Sprintf("· its recorded output and events (%d entries, repeats collapsed)", len(r.Events))))
		if len(r.Events) == 0 {
			fmt.Fprintln(w, "  (nothing recorded in this range)")
		}
		timelineRows(w, r.Events, false, false, o)
	}
	if len(r.Incidents) == 0 {
		scope := "in this session"
		if r.Service != "" {
			scope = "involving " + r.Service
		}
		fmt.Fprintf(w, "\nNo incidents %s.\n", scope)
	}
	for _, in := range r.Incidents {
		incidentHeader(w, in, o)
		timelineRows(w, in.Timeline, true, true, o)
	}
	verificationLines(w, r.Verifications, o)
	if len(r.Graph) > 0 && !o.Quiet {
		var parts []string
		for _, e := range r.Graph {
			parts = append(parts, fmt.Sprintf("%s → %s (%s)", e.From, e.To, e.Kind))
		}
		fmt.Fprintf(w, "\n%s %s\n", s.Dim("Dependencies:"), s.Dim(strings.Join(parts, ", ")))
	}
}
