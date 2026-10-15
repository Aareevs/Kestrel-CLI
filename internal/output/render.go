package output

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// TextOptions control human-readable rendering.
type TextOptions struct {
	Style Style
	Quiet bool // omit hints and footers
}

func sourceRef(s api.Source) string {
	if s.Line > 0 {
		return s.File + ":" + strconv.Itoa(s.Line)
	}
	return s.File
}

func (o TextOptions) confidence(c api.Confidence) string {
	switch c {
	case api.ConfidenceHigh:
		return o.Style.Green(string(c))
	case api.ConfidenceMedium:
		return o.Style.Yellow(string(c))
	default:
		return o.Style.Dim(string(c))
	}
}

const valueWidth = 44

// row writes "    label    value    note" with the note column aligned.
func (o TextOptions) row(w io.Writer, label, value, note string) {
	pad := valueWidth - utf8.RuneCountInString(value)
	if pad < 2 {
		pad = 2
	}
	line := fmt.Sprintf("    %-8s %s", label, value)
	if note != "" {
		line += strings.Repeat(" ", pad) + o.Style.Dim(note)
	}
	fmt.Fprintln(w, strings.TrimRight(line, " "))
}

func withNote(src api.Source) string {
	ref := sourceRef(src)
	if src.Note != "" {
		return ref + " · " + src.Note
	}
	return ref
}

// Inspect renders an inspect result.
func Inspect(w io.Writer, r *api.InspectResult, o TextOptions) {
	s := o.Style
	fmt.Fprintf(w, "%s  %s\n", s.Bold("Project"), r.Root)
	stack := "nothing recognized"
	if len(r.Technologies) > 0 {
		stack = strings.Join(r.Technologies, ", ")
	}
	fmt.Fprintf(w, "%s    %s\n", s.Bold("Stack"), stack)
	if r.ConfigFile != "" {
		fmt.Fprintf(w, "%s   %s\n", s.Bold("Config"), r.ConfigFile)
	}
	fmt.Fprintln(w)

	if len(r.Candidates) == 0 {
		fmt.Fprintln(w, "No runnable services detected.")
	} else {
		fmt.Fprintf(w, "%s (%d)\n", s.Bold("Service candidates"), len(r.Candidates))
	}
	for _, c := range r.Candidates {
		fmt.Fprintln(w)
		tech := c.Technology
		if c.Framework != "" {
			tech += " (" + c.Framework + ")"
		}
		head := fmt.Sprintf("  %s  %s · %s", s.Bold(s.Cyan(c.Name)), tech, c.Dir)
		visible := fmt.Sprintf("  %s  %s · %s", c.Name, tech, c.Dir)
		pad := 4 + 8 + 1 + valueWidth - utf8.RuneCountInString(visible)
		if pad < 2 {
			pad = 2
		}
		fmt.Fprintln(w, head+strings.Repeat(" ", pad)+o.confidence(c.Confidence))

		if c.Command != nil {
			o.row(w, "command", c.Command.Value, sourceRef(c.Command.Source))
		} else {
			o.row(w, "command", s.Dim("unknown"), "")
		}
		for _, alt := range c.Alternatives {
			o.row(w, "", "alt: "+alt.Value, sourceRef(alt.Source))
		}
		if c.Port != nil {
			o.row(w, "port", strconv.Itoa(c.Port.Value)+" ("+string(c.Port.Confidence)+")", withNote(c.Port.Source))
		}
		if c.Health != nil {
			o.row(w, "health", c.Health.Type+" "+c.Health.Target, c.Health.Reason)
		}
		for i, d := range c.DependsOn {
			label := ""
			if i == 0 {
				label = "needs"
			}
			v := d.Name + " (" + string(d.Kind) + ")"
			if !d.Resolved {
				v = d.Name + "? (" + string(d.Kind) + ", not detected as a service)"
			}
			o.row(w, label, v, withNote(d.Source))
		}
		if len(c.EnvKeys) > 0 {
			o.row(w, "env", strings.Join(c.EnvKeys, ", "), "")
		}
	}

	if len(r.Warnings) > 0 {
		fmt.Fprintln(w)
		for _, wm := range r.Warnings {
			fmt.Fprintf(w, "%s %s\n", s.Yellow("!"), wm)
		}
	}
	if r.Scan.Truncated {
		fmt.Fprintf(w, "\n%s scan stopped early (%s); results may be incomplete\n", s.Yellow("!"), r.Scan.TruncatedReason)
	}
	if o.Quiet {
		return
	}
	fmt.Fprintf(w, "\n%s\n", s.Dim(fmt.Sprintf("Scanned %d file%s in %d director%s (depth ≤ %d). Nothing was executed.",
		r.Scan.FilesSeen, plural(r.Scan.FilesSeen), r.Scan.DirsSeen, pluralY(r.Scan.DirsSeen), r.Scan.MaxDepth)))
	switch {
	case r.ConfigFile != "":
		fmt.Fprintf(w, "%s %s\n", s.Dim("Next:"), "kestrel config validate")
	case len(r.Candidates) > 0:
		fmt.Fprintf(w, "%s %s\n", s.Dim("Next:"), "kestrel init   # propose a kestrel.yaml from these candidates")
	default:
		fmt.Fprintf(w, "%s %s\n", s.Dim("Next:"), "describe services by hand in kestrel.yaml (see docs/configuration.md)")
	}
}

// ConfigValidation renders the result of `config validate`.
func ConfigValidation(w io.Writer, v *api.ConfigValidation, services int, o TextOptions) {
	s := o.Style
	if v.Valid {
		fmt.Fprintf(w, "%s %s is valid (%d service%s)\n", s.Green("✓"), v.Path, services, plural(services))
		return
	}
	fmt.Fprintf(w, "%s %s has %d problem%s:\n", s.Red("✗"), v.Path, len(v.Issues), plural(len(v.Issues)))
	for _, is := range v.Issues {
		loc := v.Path
		if is.Line > 0 {
			loc += ":" + strconv.Itoa(is.Line)
		}
		msg := is.Message
		if is.Field != "" {
			msg = s.Bold(is.Field) + ": " + msg
		}
		fmt.Fprintf(w, "  %s  %s\n", s.Dim(loc), msg)
	}
}

// Doctor renders a doctor report.
func Doctor(w io.Writer, r *api.DoctorReport, o TextOptions) {
	s := o.Style
	for _, c := range r.Checks {
		var mark string
		switch c.Status {
		case api.CheckOK:
			mark = s.Green("✓")
		case api.CheckWarn:
			mark = s.Yellow("!")
		case api.CheckFail:
			mark = s.Red("✗")
		default:
			mark = s.Dim("-")
		}
		fmt.Fprintf(w, "%s %-10s %s\n", mark, c.Name, c.Message)
		if c.Hint != "" {
			fmt.Fprintf(w, "  %-10s %s\n", "", s.Dim(c.Hint))
		}
	}
	if o.Quiet {
		return
	}
	if r.OK {
		fmt.Fprintf(w, "\n%s\n", s.Green("Kestrel is ready."))
	} else {
		fmt.Fprintf(w, "\n%s\n", s.Red("Kestrel found problems that will prevent it from working."))
	}
}

// Version renders version information.
func Version(w io.Writer, v *api.VersionInfo) {
	fmt.Fprintf(w, "kestrel %s (commit %s, built %s) %s %s\n", v.Version, v.Commit, v.Date, v.GoVersion, v.Platform)
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
