package output

import (
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// Renderers of a verification report for CI: Markdown (for a job summary or a
// PR comment) and JUnit XML (for test-report viewers). Both are plain
// functions of the report. Process output in the report (evidence, command
// output) is data from the user's own programs: it is shown quoted, never as
// markup.

// ---------------------------------------------------------------------------
// Markdown

var verdictMark = map[api.Verdict]string{
	api.VerdictResolved:          "✅",
	api.VerdictPartiallyResolved: "⚠️",
	api.VerdictStillFailing:      "❌",
	api.VerdictInconclusive:      "❔",
}

var statusMark = map[string]string{
	api.CheckPassed:   "✅",
	api.CheckFlaky:    "⚠️",
	api.CheckFailed:   "❌",
	api.CheckTimedOut: "❌",
	api.CheckSkipped:  "➖",
}

var backtickRun = regexp.MustCompile("`{2,}")

// mdCode quotes s as one inline code span, safe inside a table cell: a span
// can't be closed from inside, a pipe can't end the cell, and a line break
// can't end the row. Long text is cut.
func mdCode(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 160 {
		s = string(r[:159]) + "…"
	}
	s = strings.ReplaceAll(s, "|", `\|`)
	// A run of two or more backticks in the data could need a fence of three or
	// more, which at the start of a line opens a fenced code block. Such runs are
	// replaced by look-alike characters, so the fence is never longer than two.
	s = backtickRun.ReplaceAllStringFunc(s, func(run string) string { return strings.Repeat("ˋ", len(run)) })
	fence := "`"
	if strings.Contains(s, "`") {
		fence = "``"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return fence + s + fence
}

// mdText is plain text (Kestrel's own wording) made safe for a table cell.
func mdText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.NewReplacer("|", `\|`, "<", "&lt;", ">", "&gt;").Replace(s)
}

// checkTitle names a check for a person.
func checkTitle(c api.CheckResult) string {
	switch {
	case c.Kind == api.CheckKindCommand:
		return "command " + mdCode(c.Target)
	case c.Service != "":
		return mdText(c.Service) + " " + mdText(c.Kind) + " " + mdCode(c.Target)
	}
	return mdText(c.Kind) + " " + mdCode(c.Target)
}

func checkResultText(c api.CheckResult) string {
	switch c.Status {
	case api.CheckPassed:
		if c.Samples > 0 {
			return fmt.Sprintf("passed %d/%d", c.Passed, c.Samples)
		}
		return "passed"
	case api.CheckFlaky:
		return fmt.Sprintf("unstable %d/%d", c.Passed, c.Samples)
	case api.CheckFailed:
		if c.Samples > 0 {
			return fmt.Sprintf("failed %d/%d", c.Passed, c.Samples)
		}
		return "failed"
	case api.CheckTimedOut:
		return "timed out"
	case api.CheckSkipped:
		return "not run"
	}
	return c.Status
}

func checkDetail(c api.CheckResult) string {
	switch {
	case c.Status == api.CheckSkipped && c.SkipReason != "":
		return mdText(c.SkipReason)
	case c.Status == api.CheckPassed:
		return ""
	case c.Detail != "":
		return mdCode(c.Detail)
	case c.ExitCode != nil:
		return fmt.Sprintf("exit code %d", *c.ExitCode)
	}
	return ""
}

// VerificationMarkdown renders r as GitHub-flavored Markdown.
func VerificationMarkdown(w io.Writer, r *api.VerificationReport) {
	fmt.Fprintf(w, "## %s Kestrel verify: %s\n\n", verdictMark[r.Verdict], r.Verdict)

	var facts []string
	facts = append(facts, fmt.Sprintf("**%s**", mdText(string(r.Claim))))
	if r.Window != "" {
		facts = append(facts, fmt.Sprintf("window %s every %s", mdText(r.Window), mdText(r.Interval)))
	}
	if r.Session != nil {
		facts = append(facts, "session "+mdCode(r.Session.ID))
	} else {
		facts = append(facts, "no recorded session")
	}
	if r.Incident != nil {
		facts = append(facts, fmt.Sprintf("incident #%d", r.Incident.Number))
	}
	if r.Live {
		facts = append(facts, "live")
	}
	fmt.Fprintf(w, "%s\n\n", strings.Join(facts, " · "))

	if len(r.Checks) > 0 {
		fmt.Fprintln(w, "| | Check | Result | Detail |")
		fmt.Fprintln(w, "|---|---|---|---|")
		for _, c := range r.Checks {
			fmt.Fprintf(w, "| %s | %s | %s | %s |\n", statusMark[c.Status], checkTitle(c), checkResultText(c), checkDetail(c))
		}
		fmt.Fprintln(w)
	}

	// What a failing command printed (its last lines, already redacted), which
	// is usually why it failed.
	for _, c := range r.Checks {
		if c.Kind == api.CheckKindCommand && len(c.Output) > 0 && (c.Status == api.CheckFailed || c.Status == api.CheckTimedOut) {
			fmt.Fprintf(w, "**Output of command** %s\n\n", mdCode(c.Target))
			mdFenced(w, c.Output)
		}
	}

	if len(r.Reasons) > 0 {
		fmt.Fprintln(w, "**Why**")
		fmt.Fprintln(w)
		for _, why := range r.Reasons {
			fmt.Fprintf(w, "- %s\n", mdText(why))
		}
		fmt.Fprintln(w)
	}
	mdEvidence(w, "Still occurring during the window", r.After)
	mdEvidence(w, "Before: what was failing", r.Before)
	mdEvidence(w, "Recovery recorded", r.Recovery)

	if len(r.Notes) > 0 {
		fmt.Fprintln(w, "**Notes**")
		fmt.Fprintln(w)
		for _, n := range r.Notes {
			fmt.Fprintf(w, "- %s\n", mdText(n))
		}
		fmt.Fprintln(w)
	}
}

// mdFenced writes lines as a fenced code block whose fence is longer than any
// run of backticks in them, so nothing inside can close it early.
func mdFenced(w io.Writer, lines []string) {
	longest, run := 0, 0
	for _, l := range lines {
		for _, r := range l {
			if r == '`' {
				run++
				longest = max(longest, run)
			} else {
				run = 0
			}
		}
		run = 0
	}
	fence := strings.Repeat("`", max(3, longest+1))
	fmt.Fprintln(w, fence+"text")
	for _, l := range lines {
		fmt.Fprintln(w, strings.NewReplacer("\r", "", "\n", " ").Replace(xmlSafe(l)))
	}
	fmt.Fprintf(w, "%s\n\n", fence)
}

func mdEvidence(w io.Writer, title string, ev []api.Evidence) {
	if len(ev) == 0 {
		return
	}
	fmt.Fprintf(w, "<details><summary>%s (%d)</summary>\n\n", title, len(ev))
	for _, e := range ev {
		count := ""
		if e.Count > 1 {
			count = fmt.Sprintf(" ×%d", e.Count)
		}
		fmt.Fprintf(w, "- %s%s\n", mdCode(e.Summary), count)
	}
	fmt.Fprintf(w, "\n</details>\n\n")
}

// ---------------------------------------------------------------------------
// JUnit XML

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Time     string       `xml:"time,attr"`
	Suite    []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name      string         `xml:"name,attr"`
	Tests     int            `xml:"tests,attr"`
	Failures  int            `xml:"failures,attr"`
	Errors    int            `xml:"errors,attr"`
	Skipped   int            `xml:"skipped,attr"`
	Time      string         `xml:"time,attr"`
	Timestamp string         `xml:"timestamp,attr,omitempty"`
	Props     []junitProp    `xml:"properties>property"`
	Cases     []junitCase    `xml:"testcase"`
	SystemOut *junitSystemIO `xml:"system-out"`
}

type junitProp struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitSystemIO struct {
	Text string `xml:",chardata"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure"`
	Skipped   *junitSkipped `xml:"skipped"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr,omitempty"`
}

// xmlSafe drops characters XML 1.0 can't carry (control characters other than
// tab, newline and carriage return, and unpaired surrogates).
func xmlSafe(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return r
		case r < 0x20, r == utf8.RuneError, r >= 0xD800 && r <= 0xDFFF, r == 0xFFFE, r == 0xFFFF:
			return -1
		}
		return r
	}, s)
}

func seconds(d time.Duration) string { return fmt.Sprintf("%.3f", d.Seconds()) }

// VerificationJUnit renders r as JUnit XML: one test suite, a test case per
// check (a failed, timed-out or unstable check is a failure, a check that did
// not run is skipped), and, for a live session, one for "no new errors during
// the window". The overall verdict is a property of the suite; the exit code
// of `kestrel verify` is still what decides a job.
func VerificationJUnit(w io.Writer, r *api.VerificationReport) error {
	window, _ := time.ParseDuration(r.Window)
	suite := junitSuite{Name: "kestrel verify"}
	if !r.StartedAt.IsZero() {
		suite.Timestamp = r.StartedAt.UTC().Format("2006-01-02T15:04:05")
	}
	suite.Props = []junitProp{{"verdict", string(r.Verdict)}, {"claim", string(r.Claim)}, {"window", r.Window}, {"interval", r.Interval}, {"live", fmt.Sprint(r.Live)}}
	if r.Session != nil {
		suite.Props = append(suite.Props, junitProp{"session", r.Session.ID})
	}
	if r.Incident != nil {
		suite.Props = append(suite.Props, junitProp{"incident", fmt.Sprint(r.Incident.Number)})
	}

	for _, c := range r.Checks {
		name := c.Kind + " " + c.Target
		if c.Service != "" {
			name = c.Service + " " + name
		}
		tc := junitCase{Name: xmlSafe(name), Classname: "kestrel.verify." + c.Kind, Time: seconds(window)}
		if c.Kind == api.CheckKindCommand {
			tc.Time = seconds(time.Duration(c.DurationMS) * time.Millisecond)
		}
		switch c.Status {
		case api.CheckPassed:
		case api.CheckSkipped:
			tc.Skipped = &junitSkipped{Message: xmlSafe(c.SkipReason)}
		default:
			f := &junitFailure{Type: c.Status, Message: xmlSafe(checkResultText(c))}
			text := c.Detail
			if len(c.Output) > 0 {
				text += "\n" + strings.Join(c.Output, "\n")
			}
			f.Text = xmlSafe(strings.TrimSpace(text))
			if f.Text == "" && c.ExitCode != nil {
				f.Text = fmt.Sprintf("exit code %d", *c.ExitCode)
			}
			tc.Failure = f
		}
		suite.Cases = append(suite.Cases, tc)
	}
	if r.Live {
		tc := junitCase{Name: "no new errors from the affected services during the window", Classname: "kestrel.verify.errors", Time: seconds(window)}
		if len(r.After) > 0 {
			n := 0
			var lines []string
			for _, e := range r.After {
				n += max(e.Count, 1)
				lines = append(lines, e.Summary)
			}
			tc.Failure = &junitFailure{Type: "recurring", Message: fmt.Sprintf("%d new error event(s)", n), Text: xmlSafe(strings.Join(lines, "\n"))}
		}
		suite.Cases = append(suite.Cases, tc)
	}
	if r.Verdict == api.VerdictInconclusive && len(suite.Cases) == 0 {
		// Nothing could be checked: say so as a skipped test, not as silence.
		suite.Cases = append(suite.Cases, junitCase{Name: "verdict", Classname: "kestrel.verify", Time: "0.000",
			Skipped: &junitSkipped{Message: xmlSafe("inconclusive: " + strings.Join(r.Reasons, "; "))}})
	}

	for _, c := range suite.Cases {
		suite.Tests++
		switch {
		case c.Failure != nil:
			suite.Failures++
		case c.Skipped != nil:
			suite.Skipped++
		}
	}
	suite.Time = seconds(r.EndedAt.Sub(r.StartedAt))
	if r.EndedAt.IsZero() || r.StartedAt.IsZero() {
		suite.Time = seconds(window)
	}
	if len(r.Reasons) > 0 {
		suite.SystemOut = &junitSystemIO{Text: xmlSafe(strings.Join(r.Reasons, "\n"))}
	}
	all := junitSuites{Name: "kestrel verify", Tests: suite.Tests, Failures: suite.Failures, Skipped: suite.Skipped, Time: suite.Time, Suite: []junitSuite{suite}}

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(all); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}
