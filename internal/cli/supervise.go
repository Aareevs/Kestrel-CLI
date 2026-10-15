package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Aareevs/Kestrel-CLI/internal/app"
	"github.com/Aareevs/Kestrel-CLI/internal/output"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// appErr maps use-case errors to exit codes.
func appErr(err error) error {
	var ue *app.UsageError
	if errors.As(err, &ue) {
		return usageErr(err)
	}
	return err
}

func newRunCmd(g *globals) *cobra.Command {
	var (
		name  string
		svcs  []string
		grace time.Duration
	)
	cmd := &cobra.Command{
		Use:   "run [flags] -- <command> [args...]",
		Short: "Run a command under Kestrel with no config: capture, classify, probe, record",
		Long: "Wrap one command (or several with --svc). Kestrel captures and classifies its output, redacts\n" +
			"secrets, probes any port it announces, and stops its whole process tree on Ctrl-C.\n\n" +
			"Examples:\n" +
			"  kestrel run -- npm run dev\n" +
			"  kestrel run --svc api=\"go run ./cmd/api\" --svc web=\"npm run dev\"",
		Args: cobra.ArbitraryArgs,
		RunE: runE(func(cmd *cobra.Command, args []string) error {
			var named []app.NamedCommand
			for _, s := range svcs {
				n, c, ok := strings.Cut(s, "=")
				if !ok || strings.TrimSpace(n) == "" || strings.TrimSpace(c) == "" {
					return usageErr(fmt.Errorf("--svc %q: use name=\"command\"", s))
				}
				named = append(named, app.NamedCommand{Name: strings.TrimSpace(n), Command: c})
			}
			a, err := g.app()
			if err != nil {
				return err
			}
			cwd, err := g.streams.Getwd()
			if err != nil {
				return err
			}
			return g.supervise(cmd.Context(), func(o app.SuperviseOptions) (*app.Outcome, error) {
				return a.RunCommands(cmd.Context(), cwd, args, named, name, o)
			}, grace)
		}),
	}
	// Everything after the command is passed to it untouched.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().StringVar(&name, "name", "", "service name for the command (default: program name)")
	cmd.Flags().StringArrayVar(&svcs, "svc", nil, "additional service as name=\"shell command\" (repeatable)")
	cmd.Flags().DurationVar(&grace, "grace", 5*time.Second, "time to wait after SIGTERM before SIGKILL on shutdown")
	return cmd
}

func newWatchCmd(g *globals) *cobra.Command {
	var grace time.Duration
	cmd := &cobra.Command{
		Use:   "watch [service...]",
		Short: "Supervise configured services (and their dependencies) with live status",
		Long: "Start the services from kestrel.yaml in dependency order (all of them, or the ones named plus\n" +
			"what they depend on), stream their classified output, and report readiness changes.\n" +
			"Services without a command are probed only, never started. Ctrl-C stops everything Kestrel started.",
		RunE: runE(func(cmd *cobra.Command, args []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			return g.supervise(cmd.Context(), func(o app.SuperviseOptions) (*app.Outcome, error) {
				return a.Watch(cmd.Context(), args, o)
			}, grace)
		}),
	}
	cmd.Flags().DurationVar(&grace, "grace", 5*time.Second, "time to wait after SIGTERM before SIGKILL on shutdown")
	return cmd
}

// supervise renders a run/watch session and maps its outcome to an exit code.
func (g *globals) supervise(ctx context.Context, start func(app.SuperviseOptions) (*app.Outcome, error), grace time.Duration) error {
	var live *output.Live
	o := app.SuperviseOptions{
		StopGrace: grace,
		Warn: func(msg string) {
			fmt.Fprintf(g.streams.Err, "%s %s\n", g.style.Yellow("kestrel: warning:"), msg)
		},
		OnSession: func(id string, names []string) {
			live = output.NewLive(g.streams.Out, g.style, g.quiet, g.json(), names)
			if !g.json() && !g.quiet {
				fmt.Fprintf(g.streams.Out, "%s session %s · %s · Ctrl-C to stop\n",
					g.style.Bold("kestrel"), id, strings.Join(names, ", "))
			}
		},
	}
	o.Sink = sinkFunc(func(ev api.Event) { live.Emit(ev) })

	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			if !g.json() {
				fmt.Fprintln(g.streams.Err, "kestrel: stopping services (press Ctrl-C again to force quit)…")
			}
		case <-finished:
		}
	}()

	out, err := start(o)
	if err != nil {
		return appErr(err)
	}
	if !g.json() && !g.quiet {
		fmt.Fprintf(g.streams.Out, "\n%s session %s ended\n", g.style.Bold("kestrel"), out.SessionID)
		output.StatusTable(g.streams.Out, out.Services, g.text())
		if out.Dropped > 0 {
			fmt.Fprintf(g.streams.Out, "%s %d debug/info lines were dropped under load; no errors were dropped\n", g.style.Yellow("!"), out.Dropped)
		}
		if out.Recorded {
			fmt.Fprintf(g.streams.Out, "%s saved as session %s  %s\n", g.style.Green("✓"), out.SessionID, g.style.Dim("(kestrel sessions show last)"))
		}
		if out.Retention != nil {
			fmt.Fprintf(g.streams.Out, "%s retention removed %d older session(s)\n", g.style.Dim("·"), len(out.Retention.Removed))
		}
	}
	if out.Failed {
		return &ExitError{Code: ExitFailure}
	}
	return nil
}

type sinkFunc func(api.Event)

func (f sinkFunc) Emit(ev api.Event) { f(ev) }

func newStatusCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Liveness and readiness per service (live session, or a one-off probe of kestrel.yaml)",
		Args:  cobra.NoArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			rep, err := a.Status(cmd.Context())
			if err != nil {
				return appErr(err)
			}
			if g.json() {
				if err := output.WriteJSON(g.streams.Out, rep); err != nil {
					return err
				}
			} else {
				if rep.Note != "" {
					fmt.Fprintln(g.streams.Out, g.style.Dim(rep.Note))
				}
				if len(rep.Services) > 0 {
					output.StatusTable(g.streams.Out, rep.Services, g.text())
				}
			}
			for _, s := range rep.Services {
				if s.Liveness == api.LivenessCrashed || s.Readiness == api.ReadinessNotReady {
					return &ExitError{Code: ExitFailure}
				}
			}
			return nil
		}),
	}
}
