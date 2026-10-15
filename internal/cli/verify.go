package cli

import (
	"bufio"
	"fmt"
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
		all       bool
		allowExec bool
		o         app.VerifyOptions
	)
	cmd := &cobra.Command{
		Use:   "verify [id|last]",
		Short: "Check whether a fix worked: probes across a stability window, before/after comparison",
		Long: "Re-checks the services involved in a recorded session's latest incident (or --incident N):\n" +
			"their health probes, the addresses they failed to reach, and (while 'kestrel watch' is still running)\n" +
			"whether their errors keep occurring. Every check is sampled across --window; one lucky probe is not\n" +
			"a fix. Commands in verify.checks run only with --allow-exec, or after you confirm each one in a\n" +
			"terminal. Verify never starts, restarts, or signals your services.\n\n" +
			"Verdicts: resolved, partially_resolved, still_failing, inconclusive.\n" +
			"Exit codes: 0 resolved, 1 still failing or partially resolved, 3 inconclusive.",
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
			rep, err := a.Verify(cmd.Context(), g.sessionRef(args), all, o)
			if err != nil {
				return appErr(err)
			}
			if g.json() {
				if err := output.WriteJSON(g.streams.Out, rep); err != nil {
					return err
				}
			} else {
				output.Verification(g.streams.Out, rep, g.text())
			}
			return verifyExit(rep.Verdict)
		}),
	}
	f := cmd.Flags()
	f.BoolVar(&all, "all", false, "resolve the session among every project's sessions")
	f.BoolVar(&allowExec, "allow-exec", false, "run the command checks in verify.checks without asking")
	f.IntVar(&o.Incident, "incident", 0, "incident number to verify (default: the latest)")
	f.DurationVar(&o.Window, "window", app.DefaultVerifyWindow, "stability window: how long every check must keep passing")
	f.DurationVar(&o.Interval, "interval", app.DefaultVerifyInterval, "time between samples within the window")
	return cmd
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
	fmt.Fprintf(g.streams.Err, "%s %s: %d %s every %s for %s",
		s.Bold("Verifying"), what, p.Probes, plural(p.Probes, "check", "checks"), p.Interval, roundDur(p.Window))
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
