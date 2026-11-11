package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Aareevs/Kestrel-CLI/internal/app"
	"github.com/Aareevs/Kestrel-CLI/internal/output"
	"github.com/Aareevs/Kestrel-CLI/internal/verification"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func newVerifyCmd(g *globals) *cobra.Command {
	var (
		all         bool
		allowExec   bool
		interactive bool
		mdFile      string
		junitFile   string
		o           app.VerifyOptions
	)
	cmd := &cobra.Command{
		Use:     "verify [id|last]",
		Aliases: []string{"v", "check"},
		Short:   "Check whether a fix worked: probes across a stability window, before/after comparison",
		Long: "Re-checks the services involved in a recorded session's latest incident (or --incident N):\n" +
			"their health probes, the addresses they failed to reach, and (while 'kestrel watch' is still running)\n" +
			"whether their errors keep occurring. Every check is sampled across --window; one lucky probe is not\n" +
			"a fix. Commands in verify.checks run only with --allow-exec, or after you confirm each one in a\n" +
			"terminal. Verify never starts, restarts, or signals your services.\n\n" +
			"Verdicts: resolved, partially_resolved, still_failing, inconclusive.\n" +
			"Exit codes: 0 resolved, 1 still failing or partially resolved, 3 inconclusive.\n\n" +
			"For CI: --format markdown (a job summary) or --format junit prints that report instead of the text\n" +
			"one, and --markdown FILE / --junit FILE also write them to files, whatever --format is. The exit code\n" +
			"is the same either way.",
		Args: cobra.MaximumNArgs(1),
		RunE: runE(func(cmd *cobra.Command, args []string) error {
			if o.Window <= 0 || o.Interval <= 0 {
				return usageErr(fmt.Errorf("--window and --interval must be positive"))
			}
			a, err := g.app()
			if err != nil {
				return err
			}
			o.Authorize = g.authorizer(allowExec)
			if !g.json() && !g.quiet {
				o.OnPlan = func(p app.VerifyPlan) { verifyPlanLine(g, p) }
				o.OnCommand = func(c verification.Command) {
					fmt.Fprintf(g.streams.Err, "%s %s %s\n", g.style.Dim("running"), g.style.Cyan(c.Display), g.style.Dim("in "+c.DisplayDir+" (timeout "+c.Timeout.String()+")"))
				}
			}
			ref, inc, err := g.choose(cmd.Context(), a, args, all, interactive, o.Incident, true)
			if err != nil {
				return err
			}
			o.Incident = inc
			rep, err := a.Verify(cmd.Context(), ref, all, o)
			if err != nil {
				return appErr(err)
			}
			switch g.format {
			case "json":
				if err := output.WriteJSON(g.streams.Out, rep); err != nil {
					return err
				}
			case "markdown":
				output.VerificationMarkdown(g.streams.Out, rep)
			case "junit":
				if err := output.VerificationJUnit(g.streams.Out, rep); err != nil {
					return err
				}
			default:
				output.Verification(g.streams.Out, rep, g.text())
			}
			// Files for CI, written whatever the console format is, and before the
			// exit code is decided so a failing run still leaves its report.
			if err := writeReport(mdFile, func(w io.Writer) error { output.VerificationMarkdown(w, rep); return nil }); err != nil {
				return err
			}
			if err := writeReport(junitFile, func(w io.Writer) error { return output.VerificationJUnit(w, rep) }); err != nil {
				return err
			}
			return verifyExit(rep.Verdict)
		}),
	}
	f := cmd.Flags()
	f.BoolVarP(&all, "all", "a", false, "resolve the session among every project's sessions")
	f.BoolVar(&allowExec, "allow-exec", false, "run the command checks in verify.checks without asking")
	f.IntVarP(&o.Incident, "incident", "I", 0, "incident number to verify (default: the latest)")
	f.BoolVarP(&interactive, "interactive", "i", false, "pick the session and incident from a list (in a terminal)")
	f.DurationVarP(&o.Window, "window", "w", app.DefaultVerifyWindow, "stability window: how long every check must keep passing")
	f.StringVar(&mdFile, "markdown", "", "also write the report as Markdown to this file (overwrites it)")
	f.StringVar(&junitFile, "junit", "", "also write the report as JUnit XML to this file (overwrites it)")
	f.DurationVar(&o.Interval, "interval", app.DefaultVerifyInterval, "time between samples within the window")
	return cmd
}

// writeReport writes a CI report to path (when one was asked for), through a
// temporary file, so a reader never sees half a report. The directory must exist.
func writeReport(path string, render func(io.Writer) error) error {
	if path == "" {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kestrel-report-*")
	if err != nil {
		return &ExitError{Code: ExitInternal, Err: fmt.Errorf("cannot write %s: %w", path, err)}
	}
	defer os.Remove(tmp.Name())
	if err := render(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return &ExitError{Code: ExitInternal, Err: fmt.Errorf("cannot write %s: %w", path, err)}
	}
	return nil
}

// authorizer decides whether a configured command may run: --allow-exec, or
// an explicit "y" at a prompt that shows the exact command, its working
// directory, and its timeout. Without a terminal there is no prompt, and the
// command does not run.
func (g *globals) authorizer(allowExec bool) func(verification.Command) (*verification.Authorization, string) {
	var in *bufio.Reader
	return func(c verification.Command) (*verification.Authorization, string) {
		if allowExec {
			return &verification.Authorization{Via: "--allow-exec"}, ""
		}
		if !g.streams.InIsTTY {
			return nil, "not authorized: pass --allow-exec to run it (no terminal to ask in)"
		}
		if in == nil {
			in = bufio.NewReader(g.streams.In)
		}
		s, w := g.style, g.streams.Err
		fmt.Fprintf(w, "\n%s\n", s.Bold("verify wants to run a command from kestrel.yaml:"))
		masked := ""
		if c.Display != c.Run {
			masked = s.Dim("  (secrets masked)")
		}
		fmt.Fprintf(w, "  command  %s%s\n", s.Cyan(c.Display), masked)
		fmt.Fprintf(w, "  in       %s\n", c.Dir)
		fmt.Fprintf(w, "  timeout  %s\n", c.Timeout)
		fmt.Fprint(w, "Run it? [y/N] ")
		line, _ := in.ReadString('\n')
		if ans := strings.ToLower(strings.TrimSpace(line)); ans == "y" || ans == "yes" {
			return &verification.Authorization{Via: "confirmed at prompt"}, ""
		}
		return nil, "declined at the prompt"
	}
}

func verifyPlanLine(g *globals, p app.VerifyPlan) {
	s := g.style
	what := "current health (no recorded session)"
	if p.SessionID != "" {
		what = "session " + p.SessionID
		if p.Incident > 0 {
			what += fmt.Sprintf(", incident #%d", p.Incident)
		}
		if p.Live {
			what += ", live"
		}
	}
	if p.Window == 0 {
		fmt.Fprintf(g.streams.Err, "%s %s: nothing to sample, so no stability window", s.Bold("Verifying"), what)
	} else {
		fmt.Fprintf(g.streams.Err, "%s %s: %d %s every %s for %s",
			s.Bold("Verifying"), what, p.Probes, plural(p.Probes, "check", "checks"), p.Interval, roundDur(p.Window))
	}
	if p.Commands > 0 {
		fmt.Fprintf(g.streams.Err, ", then %d %s", p.Commands, plural(p.Commands, "command", "commands"))
	}
	fmt.Fprintln(g.streams.Err, "…")
}

func roundDur(d time.Duration) string { return d.Round(time.Second).String() }

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// verifyExit maps a verdict to the documented exit codes.
func verifyExit(v api.Verdict) error {
	switch v {
	case api.VerdictResolved:
		return nil
	case api.VerdictInconclusive:
		return &ExitError{Code: ExitInconclusive}
	}
	return &ExitError{Code: ExitFailure}
}
