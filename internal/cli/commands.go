package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Aareevs/Kestrel-CLI/internal/app"
	"github.com/Aareevs/Kestrel-CLI/internal/buildinfo"
	"github.com/Aareevs/Kestrel-CLI/internal/config"
	"github.com/Aareevs/Kestrel-CLI/internal/discovery"
	"github.com/Aareevs/Kestrel-CLI/internal/output"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func inspectFlags(cmd *cobra.Command, o *app.InspectOptions) {
	cmd.Flags().IntVar(&o.MaxDepth, "max-depth", discovery.DefaultMaxDepth, "maximum directory depth to scan")
	cmd.Flags().IntVar(&o.MaxFiles, "max-files", discovery.DefaultMaxFiles, "stop after enumerating this many files")
	cmd.Flags().DurationVar(&o.Budget, "timeout", discovery.DefaultBudget, "time budget for the scan")
}

func newInspectCmd(g *globals) *cobra.Command {
	var o app.InspectOptions
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Detect technologies and service candidates (reads files only)",
		Long: "Scan the project and report detected technologies and probable services, each with its\n" +
			"start command, port, health probe, dependencies, the file that said so, and a confidence.\n" +
			"Discovery never executes project scripts.",
		Args: cobra.NoArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			res, err := a.Inspect(cmd.Context(), o)
			if err != nil {
				return err
			}
			if g.json() {
				return output.WriteJSON(g.streams.Out, res)
			}
			output.Inspect(g.streams.Out, res, g.text())
			return nil
		}),
	}
	inspectFlags(cmd, &o)
	return cmd
}

func newInitCmd(g *globals) *cobra.Command {
	var (
		o         app.InspectOptions
		yes       bool
		printOnly bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Propose a kestrel.yaml from discovery (asks before writing; never overwrites)",
		Args:  cobra.NoArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			if existing, err := a.ConfigPath(); err != nil {
				return usageErr(err)
			} else if existing != "" {
				return usageErr(fmt.Errorf("%s already exists; Kestrel never overwrites it (use 'kestrel config validate' to check it)", existing))
			}
			p, err := a.ProposeConfig(cmd.Context(), o)
			if err != nil {
				return err
			}
			out := g.streams.Out
			if printOnly {
				_, err := out.Write(p.YAML)
				return err
			}
			st := g.style
			fmt.Fprintf(out, "%s %s\n\n", st.Bold("Proposed"), p.Path)
			for _, l := range strings.SplitAfter(string(p.YAML), "\n") {
				if l != "" {
					fmt.Fprint(out, "  "+l)
				}
			}
			fmt.Fprintln(out)
			fmt.Fprintf(out, "%d service(s) enabled", len(p.Enabled))
			if len(p.Disabled) > 0 {
				fmt.Fprintf(out, ", %d low-confidence left commented out", len(p.Disabled))
			}
			fmt.Fprintln(out, ".")

			if !yes {
				if !g.streams.InIsTTY {
					return usageErr(errors.New("not writing without confirmation; re-run with --yes, or use --print to output the YAML"))
				}
				fmt.Fprintf(out, "Write %s? [y/N] ", p.Path)
				line, _ := bufio.NewReader(g.streams.In).ReadString('\n')
				if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
					fmt.Fprintln(out, "Nothing written.")
					return nil
				}
			}
			if err := a.WriteConfig(p); err != nil {
				if errors.Is(err, app.ErrConfigExists) {
					return usageErr(err)
				}
				return err
			}
			fmt.Fprintf(out, "%s wrote %s\n", st.Green("✓"), p.Path)
			return nil
		}),
	}
	inspectFlags(cmd, &o)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "write without asking for confirmation")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the proposed YAML to stdout and write nothing")
	return cmd
}

func newConfigCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show, validate, or locate kestrel.yaml (secrets are always masked)",
	}

	show := &cobra.Command{
		Use:   "show",
		Short: "Print the effective config with defaults applied and secrets masked",
		Args:  cobra.NoArgs,
		RunE: runE(func(_ *cobra.Command, _ []string) error {
			cfg, path, err := loadValid(g)
			if err != nil {
				return err
			}
			masked := cfg.Masked()
			if g.json() {
				return output.WriteJSON(g.streams.Out, struct {
					SchemaVersion int            `json:"schema_version"`
					Path          string         `json:"path"`
					Config        *config.Config `json:"config"`
				}{api.SchemaVersion, path, masked})
			}
			b, err := yaml.Marshal(masked)
			if err != nil {
				return err
			}
			if !g.quiet {
				fmt.Fprintf(g.streams.Out, "%s\n", g.style.Dim("# "+path+" (defaults applied, secrets masked)"))
			}
			_, err = g.streams.Out.Write(b)
			return err
		}),
	}

	validate := &cobra.Command{
		Use:   "validate",
		Short: "Check kestrel.yaml for errors (exit code 2 if invalid)",
		Args:  cobra.NoArgs,
		RunE: runE(func(_ *cobra.Command, _ []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			res, cfg, err := a.ValidateConfig()
			if err != nil {
				return configErr(err)
			}
			if g.json() {
				if err := output.WriteJSON(g.streams.Out, res); err != nil {
					return err
				}
			} else {
				n := 0
				if cfg != nil {
					n = len(cfg.Services)
				}
				output.ConfigValidation(g.streams.Out, res, n, g.text())
			}
			if !res.Valid {
				return &ExitError{Code: ExitUsage}
			}
			return nil
		}),
	}

	path := &cobra.Command{
		Use:   "path",
		Short: "Print the config file path Kestrel uses for this project",
		Args:  cobra.NoArgs,
		RunE: runE(func(_ *cobra.Command, _ []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			p, err := a.ConfigPath()
			if err != nil {
				return usageErr(err)
			}
			res := api.ConfigPath{SchemaVersion: api.SchemaVersion, ProjectRoot: a.ProjectRoot, Path: p, Exists: p != ""}
			if g.json() {
				return output.WriteJSON(g.streams.Out, res)
			}
			if p != "" {
				fmt.Fprintln(g.streams.Out, p)
			} else {
				fmt.Fprintf(g.streams.Out, "no config file in %s (zero-config mode); 'kestrel init' would create %s\n",
					a.ProjectRoot, config.FileNames[0])
			}
			return nil
		}),
	}
	cmd.AddCommand(show, validate, path)
	return cmd
}

// loadValid loads a config that must exist and be valid.
func loadValid(g *globals) (*config.Config, string, error) {
	a, err := g.app()
	if err != nil {
		return nil, "", err
	}
	cfg, p, err := a.LoadConfig()
	if err != nil {
		return nil, "", configErr(err)
	}
	return cfg, p, nil
}

func configErr(err error) error {
	var ve *config.ValidationError
	switch {
	case errors.Is(err, app.ErrNoConfig):
		return usageErr(fmt.Errorf("%w (run 'kestrel init' to create one)", err))
	case errors.As(err, &ve), errors.Is(err, config.ErrAmbiguous):
		return usageErr(err)
	}
	return err
}

func newDoctorCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that Kestrel can run here: platform, data directory, config, tools",
		Args:  cobra.NoArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			r := a.Doctor(cmd.Context())
			if g.json() {
				if err := output.WriteJSON(g.streams.Out, r); err != nil {
					return err
				}
			} else {
				output.Doctor(g.streams.Out, r, g.text())
			}
			if !r.OK {
				return &ExitError{Code: ExitInternal}
			}
			return nil
		}),
	}
}

func newVersionCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: runE(func(_ *cobra.Command, _ []string) error {
			v := buildinfo.Info()
			if g.json() {
				return output.WriteJSON(g.streams.Out, v)
			}
			output.Version(g.streams.Out, &v)
			return nil
		}),
	}
}
