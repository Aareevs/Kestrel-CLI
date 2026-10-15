package output

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// HumanBytes renders a size like "1.4 MiB".
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return "<1s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func clock(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func (o TextOptions) status(s string) string {
	switch s {
	case api.SessionActive:
		return o.Style.Green(s)
	case api.SessionInterrupted:
		return o.Style.Yellow(s)
	}
	return s
}

// SessionList renders `kestrel sessions list`.
func SessionList(w io.Writer, l *api.SessionList, now time.Time, o TextOptions) {
	s := o.Style
	if len(l.Sessions) == 0 {
		scope := "for this project"
		if l.AllProjects {
			scope = "yet"
		}
		fmt.Fprintf(w, "No sessions recorded %s. Start one with 'kestrel run -- <command>' or 'kestrel watch'.\n", scope)
		return
	}
	fmt.Fprintf(w, "%s\n", s.Bold(fmt.Sprintf("Sessions (%d)", len(l.Sessions))))
	fmt.Fprintf(w, "  %-26s  %-19s  %-8s  %-5s  %-11s  %6s  %6s  %9s  %s\n", "ID", "STARTED", "DURATION", "MODE", "STATUS", "EVENTS", "ERRORS", "SIZE", "SERVICES")
	for _, x := range l.Sessions {
		end := x.EndedAt
		if end.IsZero() {
			end = now
		}
		errs := fmt.Sprintf("%6d", x.Errors)
		if x.Errors > 0 {
			errs = s.Red(errs)
		}
		flag := ""
		if x.WriteErrors > 0 {
			flag = s.Yellow("  ! incomplete")
		}
		fmt.Fprintf(w, "  %-26s  %-19s  %-8s  %-5s  %s  %6d  %s  %9s  %s%s\n",
			x.ID, clock(x.StartedAt), humanDuration(end.Sub(x.StartedAt)), x.Mode,
			pad(o.status(x.Status), x.Status, 11), x.Events, errs, HumanBytes(x.SizeBytes), strings.Join(x.Services, ","), flag)
	}
	if !o.Quiet {
		fmt.Fprintf(w, "\n%s\n", s.Dim("Details: kestrel sessions show <id|last>   Export: kestrel sessions export <id|last>"))
	}
}

// pad right-pads styled text using the length of its plain form.
func pad(styled, plain string, width int) string {
	if n := width - len(plain); n > 0 {
		return styled + strings.Repeat(" ", n)
	}
	return styled
}

// SessionDetail renders `kestrel sessions show`.
func SessionDetail(w io.Writer, d *api.SessionDetail, now time.Time, o TextOptions) {
	s := o.Style
	x := d.Session
	end := x.EndedAt
	if end.IsZero() {
		end = now
	}
	fmt.Fprintf(w, "%s  %s\n", s.Bold("Session"), x.ID)
	fmt.Fprintf(w, "  %-9s %s · %s\n", "mode", x.Mode, o.status(x.Status))
	fmt.Fprintf(w, "  %-9s %s\n", "project", x.ProjectRoot)
	fmt.Fprintf(w, "  %-9s %s → %s (%s)\n", "time", clock(x.StartedAt), clock(x.EndedAt), humanDuration(end.Sub(x.StartedAt)))
	fmt.Fprintf(w, "  %-9s %d events, %d errors, %d info lines dropped under load, %s on disk\n", "recorded", x.Events, x.Errors, x.Dropped, HumanBytes(x.SizeBytes))
	if x.WriteErrors > 0 {
		fmt.Fprintf(w, "  %s %d event(s) could not be fully stored; this record is incomplete\n", s.Yellow("!"), x.WriteErrors)
	}
	if x.Status == api.SessionInterrupted {
		fmt.Fprintf(w, "  %s Kestrel stopped without closing this session (crash or kill); events up to that point are intact\n", s.Yellow("!"))
	}
	if len(d.Services) > 0 {
		fmt.Fprintf(w, "\n%s\n", s.Bold("Final service state"))
		StatusTable(w, d.Services, o)
	}
	if len(d.CountsByType) > 0 {
		types := make([]string, 0, len(d.CountsByType))
		for t := range d.CountsByType {
			types = append(types, t)
		}
		sort.Strings(types)
		var parts []string
		for _, t := range types {
			parts = append(parts, fmt.Sprintf("%s %d", t, d.CountsByType[t]))
		}
		fmt.Fprintf(w, "\n%s\n  %s\n", s.Bold("Events by type"), strings.Join(parts, " · "))
	}
	if len(d.Notable) > 0 {
		fmt.Fprintf(w, "\n%s %s\n", s.Bold("Notable events"), s.Dim(fmt.Sprintf("(warnings and above, lifecycle, probes; last %d at most)", d.NotableCap)))
		for _, e := range d.Notable {
			mark := s.Dim("·")
			switch {
			case e.Severity.Rank() >= api.SeverityError.Rank():
				mark = s.Red("✗")
			case e.Severity == api.SeverityWarn:
				mark = s.Yellow("!")
			case e.Type == api.EventProbeReady || e.Type == api.EventProbeRecovered:
				mark = s.Green("✓")
			}
			msg := e.Message
			if r := []rune(msg); len(r) > 110 {
				msg = string(r[:109]) + "…"
			}
			fmt.Fprintf(w, "  %s %s %-8s %-22s %s\n", mark, s.Dim(e.Time.Local().Format("15:04:05")), e.ServiceID, string(e.Type), msg)
		}
	}
	if !o.Quiet {
		fmt.Fprintf(w, "\n%s\n", s.Dim("Full redacted record: kestrel sessions export "+x.ID))
	}
}

// Retention renders `kestrel cleanup` and the end-of-session retention notice.
func Retention(w io.Writer, r *api.RetentionResult, o TextOptions) {
	s := o.Style
	verb := "Removed"
	if r.DryRun {
		verb = "Would remove"
	}
	if len(r.Removed) == 0 {
		fmt.Fprintf(w, "Nothing to remove: %d session(s) kept, %s recorded (limits: %d days, %d MB).\n",
			r.Kept, HumanBytes(r.TotalAfter), r.RetentionDays, r.MaxTotalMB)
	} else {
		var freed int64
		fmt.Fprintf(w, "%s %d session(s) (limits: %d days, %d MB):\n", s.Bold(verb), len(r.Removed), r.RetentionDays, r.MaxTotalMB)
		for _, x := range r.Removed {
			why := map[string]string{"age": "older than the retention window", "size": "over the size limit"}[x.Reason]
			fmt.Fprintf(w, "  %s  %9s  %s\n", x.ID, HumanBytes(x.SizeBytes), s.Dim(why))
			freed += x.SizeBytes
		}
		fmt.Fprintf(w, "%s freed; %d session(s) kept, %s recorded.\n", HumanBytes(freed), r.Kept, HumanBytes(r.TotalAfter))
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(w, "%s still over the size limit, but %d session(s) are active or protected and are never removed.\n", s.Yellow("!"), len(r.Skipped))
	}
	if r.DryRun && len(r.Removed) > 0 {
		fmt.Fprintf(w, "%s\n", s.Dim("Dry run: nothing was deleted. Run 'kestrel cleanup' to apply."))
	}
}
