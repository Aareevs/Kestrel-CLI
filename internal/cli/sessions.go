package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Aareevs/Kestrel-CLI/internal/output"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// sessionRef picks the session reference: a positional argument wins over
// --session, and the default is the newest session.
func (g *globals) sessionRef(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	if g.session != "" {
		return g.session
	}
	return "last"
}

func newSessionsCmd(g *globals) *cobra.Command {
	var (
		all   bool
		limit int
	)
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List, show, export, or delete recorded sessions",
		Long: "Every 'kestrel run' and 'kestrel watch' is recorded locally. Sessions are referenced by full ID,\n" +
			"a unique prefix, or 'last'. By default only this project's sessions are considered; use --all for every project.",
	}
	cmd.PersistentFlags().BoolVar(&all, "all", false, "consider sessions from every project, not just this one")

	list := &cobra.Command{
		Use:   "list",
		Short: "List recorded sessions, newest first",
		Args:  cobra.NoArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			res, err := a.SessionsList(cmd.Context(), all, limit)
			if err != nil {
				return appErr(err)
			}
			if g.json() {
				return output.WriteJSON(g.streams.Out, res)
			}
			output.SessionList(g.streams.Out, res, a.Now(), g.text())
			return nil
		}),
	}
	list.Flags().IntVarP(&limit, "limit", "n", 0, "show at most this many sessions (0 = all)")

	show := &cobra.Command{
		Use:   "show [id|last]",
		Short: "Show one session: final state, event counts, notable events",
		Args:  cobra.MaximumNArgs(1),
		RunE: runE(func(cmd *cobra.Command, args []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			d, err := a.SessionShow(cmd.Context(), g.sessionRef(args), all)
			if err != nil {
				return appErr(err)
			}
			if g.json() {
				return output.WriteJSON(g.streams.Out, d)
			}
			output.SessionDetail(g.streams.Out, d, a.Now(), g.text())
			return nil
		}),
	}

	var out string
	export := &cobra.Command{
		Use:   "export [id|last]",
		Short: "Write a redacted JSON bundle of a session (stdout, or --out file)",
		Long: "The bundle contains the session summary, final service states, and every event with its full message.\n" +
			"Secrets are redacted before storage and again on export. Redaction is best-effort: review before sharing.\n" +
			"--out never overwrites an existing file.",
		Args: cobra.MaximumNArgs(1),
		RunE: runE(func(cmd *cobra.Command, args []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			var w io.Writer = g.streams.Out
			var f *os.File
			if out != "" {
				f, err = os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
				if err != nil {
					if errors.Is(err, os.ErrExist) {
						return usageErr(fmt.Errorf("%s already exists; Kestrel never overwrites it", out))
					}
					return usageErr(err)
				}
				w = f
			}
			id, err := a.SessionExport(cmd.Context(), g.sessionRef(args), all, w)
			if f != nil {
				if cerr := f.Close(); err == nil {
					err = cerr
				}
				if err != nil {
					os.Remove(out) // never leave a half-written bundle
				}
			}
			if err != nil {
				return appErr(err)
			}
			if f != nil && !g.quiet {
				fmt.Fprintf(g.streams.Err, "exported session %s to %s\n", id, out)
			}
			return nil
		}),
	}
	export.Flags().StringVarP(&out, "out", "o", "", "write to this file instead of stdout")

	var yes bool
	del := &cobra.Command{
		Use:   "delete <id|prefix>...",
		Short: "Delete sessions and their stored logs (active sessions are refused)",
		Args:  cobra.MinimumNArgs(1),
		RunE: runE(func(cmd *cobra.Command, args []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			if !yes {
				if !g.streams.InIsTTY {
					return usageErr(errors.New("not deleting without confirmation; re-run with --yes"))
				}
				fmt.Fprintf(g.streams.Out, "Delete %d session(s): %s? This cannot be undone. [y/N] ", len(args), strings.Join(args, ", "))
				line, _ := bufio.NewReader(g.streams.In).ReadString('\n')
				if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
					fmt.Fprintln(g.streams.Out, "Nothing deleted.")
					return nil
				}
			}
			removed, err := a.SessionsDelete(cmd.Context(), args, all)
			if g.json() {
				_ = output.WriteJSON(g.streams.Out, struct {
					SchemaVersion int                  `json:"schema_version"`
					Removed       []api.RemovedSession `json:"removed"`
				}{api.SchemaVersion, removed})
			} else {
				var freed int64
				for _, r := range removed {
					fmt.Fprintf(g.streams.Out, "%s deleted %s (%s)\n", g.style.Green("✓"), r.ID, output.HumanBytes(r.SizeBytes))
					freed += r.SizeBytes
				}
			}
			return appErr(err)
		}),
	}
	del.Flags().BoolVarP(&yes, "yes", "y", false, "delete without asking for confirmation")

	cmd.AddCommand(list, show, export, del)
	return cmd
}

func newCleanupCmd(g *globals) *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Apply the retention policy now (retention_days, max_total_mb)",
		Long: "Removes the oldest closed sessions beyond retention_days, then, while the total exceeds max_total_mb,\n" +
			"the oldest remaining. Active sessions are never removed. Limits come from kestrel.yaml (defaults: 7 days, 500 MB).\n" +
			"The same policy runs automatically whenever a session ends.",
		Args: cobra.NoArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			res, err := a.Cleanup(cmd.Context(), dry)
			if res != nil {
				if g.json() {
					if jerr := output.WriteJSON(g.streams.Out, res); jerr != nil {
						return jerr
					}
				} else {
					output.Retention(g.streams.Out, res, g.text())
				}
			}
			return appErr(err)
		}),
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "show what would be removed without deleting anything")
	return cmd
}
