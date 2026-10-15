package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func (o TextOptions) verdict(v api.Verdict) string {
	switch v {
	case api.VerdictResolved:
		return o.Style.Green(string(v))
	case api.VerdictInconclusive:
		return o.Style.Yellow(string(v))
	}
	return o.Style.Red(string(v))
}

func (o TextOptions) checkMark(status string) string {
	s := o.Style
	switch status {
	case api.CheckPassed:
		return s.Green("✓")
	case api.CheckFlaky:
		return s.Yellow("~")
	case api.CheckSkipped:
		return s.Dim("-")
	}
	return s.Red("✗")
}

func checkCount(c api.CheckResult) string {
	switch {
	case c.Kind == api.CheckKindCommand && c.Status == api.CheckSkipped:
		return "skipped: " + c.SkipReason
	case c.Kind == api.CheckKindCommand:
		return fmt.Sprintf("%s · %s · %.1fs", c.Status, c.Detail, float64(c.DurationMS)/1000)
	case c.Samples == 0:
		return "not sampled"
	}
	out := fmt.Sprintf("%d/%d samples passed", c.Passed, c.Samples)
	if c.Status != api.CheckPassed && c.Detail != "" {
		out += " · last failure: " + c.Detail
	}
	return out
}

func evidenceBlock(w io.Writer, title string, ev []api.Evidence, mark string, o TextOptions) {
	if len(ev) == 0 {
		return
	}
	fmt.Fprintf(w, "\n  %s\n", o.Style.Bold(title))
	for i, e := range ev {
		if i == 6 {
			fmt.Fprintf(w, "    %s\n", o.Style.Dim(fmt.Sprintf("… %d more (see --format json)", len(ev)-6)))
			break
		}
		e.Summary = e.Service + ": " + e.Summary
		evidenceLine(w, mark, "    ", e, o)
	}
}

// Verification renders `kestrel verify`.
func Verification(w io.Writer, r *api.VerificationReport, o TextOptions) {
	s := o.Style
	if r.Session != nil {
		sessionLine(w, *r.Session, o)
	} else {
		fmt.Fprintf(w, "%s no recorded session\n", s.Bold("Session"))
	}
	if r.Incident != nil {
		incidentHeader(w, *r.Incident, o)
	}

	evidenceBlock(w, "Before: what was failing", r.Before, s.Red("✗"), o)
	evidenceBlock(w, "Recovery recorded", r.Recovery, s.Green("✓"), o)

	fmt.Fprintf(w, "\n  %s %s\n", s.Bold("Checks"), s.Dim(fmt.Sprintf("(every %s across a %s window)", r.Interval, r.Window)))
	if len(r.Checks) == 0 {
		fmt.Fprintln(w, "    (none could be chosen)")
	}
	width := 0
	for _, c := range r.Checks {
		width = max(width, len([]rune(c.Service)))
	}
	for _, c := range r.Checks {
		label := c.Kind + " " + c.Target
		if c.Kind == api.CheckKindCommand {
			label = "command " + c.Target + s.Dim(" (in "+c.Dir+")")
		}
		fmt.Fprintf(w, "    %s %-*s  %s\n", o.checkMark(c.Status), width, c.Service, label)
		fmt.Fprintf(w, "      %*s%s\n", width, "", s.Dim(checkCount(c)+" · "+c.Origin))
		if c.Kind == api.CheckKindCommand && c.Status != api.CheckPassed {
			for _, l := range c.Output {
				fmt.Fprintf(w, "      %*s%s %s\n", width, "", s.Dim("│"), l)
			}
		}
	}

	if r.Live {
		if len(r.After) == 0 {
			fmt.Fprintf(w, "\n  %s %s\n", s.Bold("During the window"), s.Green("no new errors from the affected services"))
		} else {
			evidenceBlock(w, "During the window: still occurring", r.After, s.Red("✗"), o)
		}
	}

	fmt.Fprintf(w, "\n%s %s  [%s]\n", s.Bold("Verdict:"), o.verdict(r.Verdict), r.Claim)
	for _, why := range r.Reasons {
		fmt.Fprintf(w, "  %s %s\n", s.Dim("·"), why)
	}
	for _, n := range r.Notes {
		fmt.Fprintf(w, "%s %s\n", s.Yellow("!"), n)
	}
	if !o.Quiet && r.Saved {
		fmt.Fprintf(w, "\n%s\n", s.Dim("Saved as verification "+r.ID+" on session "+r.Session.ID+". Full evidence: --format json"))
	}
}

// verificationLines renders past verifications in a timeline.
func verificationLines(w io.Writer, vs []api.VerificationSummary, o TextOptions) {
	if len(vs) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s\n", o.Style.Bold("Verification"))
	for _, v := range vs {
		inc := ""
		if v.Incident > 0 {
			inc = fmt.Sprintf(" incident #%d", v.Incident)
		}
		fmt.Fprintf(w, "  %s  %s%s  %s %s\n", o.Style.Dim(hm(v.StartedAt)+"–"+hm(v.EndedAt)), o.verdict(v.Verdict), inc,
			o.Style.Dim(strings.TrimSpace("window "+v.Window)), o.Style.Dim(shortID(v.ID)))
	}
}
