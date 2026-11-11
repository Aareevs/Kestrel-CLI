package output

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// logoRows is KESTREL in a 4-wide, 5-high block font.
var logoRows = func() []string {
	glyphs := map[rune][5]string{
		'K': {"█  █", "█ █ ", "██  ", "█ █ ", "█  █"},
		'E': {"████", "█   ", "███ ", "█   ", "████"},
		'S': {" ███", "█   ", " ██ ", "   █", "███ "},
		'T': {"████", " ██ ", " ██ ", " ██ ", " ██ "},
		'R': {"███ ", "█  █", "███ ", "█ █ ", "█  █"},
		'L': {"█   ", "█   ", "█   ", "█   ", "████"},
	}
	rows := make([]string, 5)
	for i, r := range "KESTREL" {
		for y := range rows {
			if i > 0 {
				rows[y] += " "
			}
			rows[y] += glyphs[r][y]
		}
	}
	return rows
}()

// gradient runs from cyan through blue to violet in the 256-color palette.
var gradient = []int{51, 45, 39, 33, 63, 99, 135, 171}

// Logo is the KESTREL wordmark. With color each column takes the next shade of
// the gradient, so it fades left to right; without color it is plain blocks. A
// terminal too narrow for it gets a one-line wordmark instead.
func Logo(st Style, width int) []string {
	if width > 0 && width < utf8.RuneCountInString(logoRows[0])+2 {
		return []string{st.Bold(st.Cyan("KESTREL"))}
	}
	out := make([]string, len(logoRows))
	for y, row := range logoRows {
		var b strings.Builder
		cols := []rune(row)
		for x, r := range cols {
			if r == ' ' {
				b.WriteRune(r)
				continue
			}
			b.WriteString(st.Color256(gradient[x*len(gradient)/len(cols)], string(r)))
		}
		out[y] = b.String()
	}
	return out
}

// Welcome renders the welcome screen: the logo, the tagline, a live summary of
// this project and a few next steps. It is static: no animation, no cursor
// control, so it is safe to capture.
func Welcome(w io.Writer, v *api.Welcome, o TextOptions) {
	s := o.Style
	fit := func(line string, indent int) string {
		if o.Width > 0 {
			return Fit(line, o.Width-indent)
		}
		return line
	}
	fmt.Fprintln(w)
	for _, l := range Logo(s, o.Width) {
		fmt.Fprintln(w, "  "+l)
	}
	const tagline = "Every failure has a trail."
	if o.Width > 0 && 2+len(tagline)+2+utf8.RuneCountInString(v.Version) > o.Width {
		fmt.Fprintf(w, "\n  %s\n  %s\n\n", s.Bold(tagline), s.Dim(fit(v.Version, 2)))
	} else {
		fmt.Fprintf(w, "\n  %s  %s\n\n", s.Bold(tagline), s.Dim(v.Version))
	}

	row := func(label, value string) {
		fmt.Fprintf(w, "  %s  %s\n", s.Dim(fmt.Sprintf("%-8s", label)), fit(value, 12))
	}
	row("project", v.Root)
	tech := "nothing recognized yet"
	if len(v.Technologies) > 0 {
		tech = strings.Join(v.Technologies, ", ")
	}
	if v.Services > 0 {
		tech += fmt.Sprintf(" · %d service%s found", v.Services, plural(v.Services))
	}
	row("stack", tech)
	if v.ConfigFile != "" {
		row("config", s.Green("✓")+" "+v.ConfigFile)
	} else {
		row("config", "no kestrel.yaml")
	}
	if ls := v.LastSession; ls != nil {
		row("last run", lastRun(ls, v.GeneratedAt, s))
	} else {
		row("last run", "nothing recorded for this project yet")
	}

	fmt.Fprintf(w, "\n  %s\n", s.Bold("Try"))
	cmdW := 0
	for _, t := range v.Tips {
		cmdW = max(cmdW, utf8.RuneCountInString(t.Command))
	}
	for _, t := range v.Tips {
		pad := strings.Repeat(" ", cmdW-utf8.RuneCountInString(t.Command))
		fmt.Fprintf(w, "  %s %s%s  %s\n", s.Cyan("›"), s.Bold(t.Command), pad, s.Dim(fit(t.Why, 6+cmdW)))
	}
	fmt.Fprintln(w)
}

func lastRun(ls *api.SessionSummary, now time.Time, s Style) string {
	ago := now.Sub(ls.StartedAt).Round(time.Minute)
	when := "just now"
	switch {
	case ago >= 48*time.Hour:
		when = fmt.Sprintf("%d days ago", int(ago.Hours()/24))
	case ago >= time.Hour:
		when = fmt.Sprintf("%dh ago", int(ago.Hours()))
	case ago >= time.Minute:
		when = fmt.Sprintf("%dm ago", int(ago.Minutes()))
	}
	state := ls.Status
	switch {
	case ls.Errors > 0:
		state = s.Red(fmt.Sprintf("%d error%s", ls.Errors, plural(ls.Errors)))
	case ls.Status == api.SessionActive:
		state = s.Green(ls.Status)
	}
	return fmt.Sprintf("%s · %s · %s", when, strings.Join(ls.Services, ", "), state)
}
