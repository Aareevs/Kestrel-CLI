package cli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/Aareevs/Kestrel-CLI/internal/app"
	"github.com/Aareevs/Kestrel-CLI/internal/output"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func newDiagnoseCmd(g *globals) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "diagnose [id|last]",
		Short: "Ranked, evidence-backed hypotheses for each incident in a session",
		Long: "Correlates a recorded session (the latest by default) into incidents and proposes ranked hypotheses.\n" +
			"Each cites the events that support and contradict it, states its confidence and why, and suggests\n" +
			"one next step for you to run. Kestrel never runs it.\n\n" +
			"Exit codes: 0 no incidents, 1 incident with a likely cause, 3 unresolved (evidence is insufficient).",
		Args: cobra.MaximumNArgs(1),
		RunE: runE(func(cmd *cobra.Command, args []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			rep, err := a.Diagnose(cmd.Context(), g.sessionRef(args), all)
			if err != nil {
				return appErr(err)
			}
			if g.json() {
				if err := output.WriteJSON(g.streams.Out, rep); err != nil {
					return err
				}
			} else {
				output.Diagnosis(g.streams.Out, rep, g.text())
			}
			return diagnoseExit(rep)
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "resolve the session among every project's sessions")
	return cmd
}

// diagnoseExit: unresolved beats failure-detected beats ok.
func diagnoseExit(rep *api.DiagnosisReport) error {
	code := ExitOK
	for _, d := range rep.Incidents {
		switch {
		case d.Status == api.ClaimUnresolved:
			code = ExitInconclusive
		case code == ExitOK:
			code = ExitFailure
		}
	}
	if code == ExitOK {
		return nil
	}
	return &ExitError{Code: code}
}

func newTimelineCmd(g *globals) *cobra.Command {
	var (
		all     bool
		service string
		since   time.Duration
		limit   int
	)
	cmd := &cobra.Command{
		Use:   "timeline [id|last]",
		Short: "Incident timelines: last healthy state, first anomaly, symptoms, recovery",
		Long: "Shows each incident in a recorded session (the latest by default) in order: last known healthy state,\n" +
			"first anomaly, key events, downstream symptoms, and recovery, with repeats collapsed.\n" +
			"With --service, also shows everything that service printed (most recent first, up to --limit).",
		Args: cobra.MaximumNArgs(1),
		RunE: runE(func(cmd *cobra.Command, args []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			rep, err := a.Timeline(cmd.Context(), g.sessionRef(args), all, app.TimelineOptions{Service: service, Since: since, Limit: limit})
			if err != nil {
				return appErr(err)
			}
			if g.json() {
				return output.WriteJSON(g.streams.Out, rep)
			}
			output.Timeline(g.streams.Out, rep, g.text())
			return nil
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "resolve the session among every project's sessions")
	cmd.Flags().StringVar(&service, "service", "", "show this service's recorded output and its incidents")
	cmd.Flags().DurationVar(&since, "since", 0, "only the last part of the session, e.g. 5m (relative to its end)")
	cmd.Flags().IntVarP(&limit, "limit", "n", 200, "with --service: at most this many recent events")
	return cmd
}
