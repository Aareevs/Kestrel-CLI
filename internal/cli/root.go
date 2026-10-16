// Package cli defines Kestrel's commands. Commands parse flags, call
// internal/app, and render results; they contain no business logic.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/Aareevs/Kestrel-CLI/internal/app"
	"github.com/Aareevs/Kestrel-CLI/internal/output"
)

// Exit codes (documented in README).
const (
	ExitOK           = 0
	ExitFailure      = 1 // project failure detected / verification failed
	ExitUsage        = 2 // invalid usage or config
	ExitInconclusive = 3 // incomplete or inconclusive investigation
	ExitInternal     = 4 // internal error
)

// ExitError carries an exit code. Message-less errors exit silently (the
// command already printed what the user needs).
type ExitError struct {
	Code int
	Err  error
	// Hint adds a pointer to --help (for command-line usage mistakes).
	Hint bool
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

func usageErr(err error) error { return &ExitError{Code: ExitUsage, Err: err} }

// Streams are the process's standard streams, injectable for tests.
type Streams struct {
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
	Getenv  func(string) string
	Getwd   func() (string, error)
	InIsTTY bool
}

// DefaultStreams returns the real process streams.
func DefaultStreams() Streams {
	return Streams{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr,
		Getenv: os.Getenv, Getwd: os.Getwd,
		InIsTTY: output.IsTerminal(os.Stdin),
	}
}

// globals are the persistent flags shared by every command.
type globals struct {
	project  string
	session  string
	format   string
	jsonFlag bool
	verbose  bool
	quiet    bool
	noColor  bool

	streams Streams
	style   output.Style
}

func (g *globals) json() bool { return output.Format(g.format) == output.FormatJSON }

func (g *globals) text() output.TextOptions {
	return output.TextOptions{Style: g.style, Quiet: g.quiet}
}

// app resolves the project and returns the use-case layer.
func (g *globals) app() (*app.App, error) {
	cwd, err := g.streams.Getwd()
	if err != nil {
		return nil, err
	}
	a, err := app.New(g.project, cwd)
	if err != nil {
		return nil, usageErr(err)
	}
	a.Getenv = g.streams.Getenv
	return a, nil
}

// NewRootCmd builds the command tree.
func NewRootCmd(s Streams) *cobra.Command {
	g := &globals{streams: s}
	root := &cobra.Command{
		Use:   "kestrel",
		Short: "Every failure has a trail.",
		Long: "Kestrel watches your local dev stack, reconstructs what broke and in what order,\n" +
			"ranks evidence-backed root-cause hypotheses, and verifies whether your fix worked.",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if g.jsonFlag {
				g.format = "json"
			}
			switch output.Format(g.format) {
			case output.FormatText, output.FormatJSON:
			default:
				return usageErr(fmt.Errorf("invalid --format %q (use text or json)", g.format))
			}
			if g.verbose && g.quiet {
				return usageErr(errors.New("--verbose and --quiet cannot be used together"))
			}
			level := slog.LevelWarn
			if g.verbose {
				level = slog.LevelDebug
			}
			slog.SetDefault(slog.New(slog.NewTextHandler(s.Err, &slog.HandlerOptions{Level: level})))
			g.style = output.NewStyle(output.ColorEnabled(s.Out, g.noColor, s.Getenv))
			return nil
		},
	}
	root.SetIn(s.In)
	root.SetOut(s.Out)
	root.SetErr(s.Err)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &ExitError{Code: ExitUsage, Err: err, Hint: true}
	})

	pf := root.PersistentFlags()
	pf.StringVar(&g.project, "project", "", "project directory (default: nearest ancestor with kestrel.yaml, else the current directory)")
	pf.StringVar(&g.session, "session", "", "session to act on: full ID, unique prefix, or 'last' (default: last)")
	pf.StringVar(&g.format, "format", "text", "output format: text or json")
	pf.BoolVarP(&g.jsonFlag, "json", "j", false, "output JSON (shorthand for --format json)")
	pf.BoolVarP(&g.verbose, "verbose", "v", false, "verbose diagnostic logging to stderr")
	pf.BoolVarP(&g.quiet, "quiet", "q", false, "print only essential output")
	pf.BoolVar(&g.noColor, "no-color", false, "disable colored output (also honors NO_COLOR)")

	root.AddCommand(
		newInspectCmd(g),
		newInitCmd(g),
		newRunCmd(g),
		newWatchCmd(g),
		newStatusCmd(g),
		newTimelineCmd(g),
		newDiagnoseCmd(g),
		newVerifyCmd(g),
		newMCPCmd(g),
		newSessionsCmd(g),
		newCleanupCmd(g),
		newConfigCmd(g),
		newDoctorCmd(g),
		newVersionCmd(g),
	)
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context, s Streams, args []string) int {
	root := NewRootCmd(s)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if !errors.As(err, &ee) {
		// Errors that did not come from our commands come from cobra's own
		// argument and command parsing.
		ee = &ExitError{Code: ExitUsage, Err: err, Hint: true}
	}
	if ee.Err != nil {
		fmt.Fprintf(s.Err, "kestrel: %v\n", ee.Err)
		if ee.Hint {
			fmt.Fprintln(s.Err, "Run 'kestrel --help' for usage.")
		}
	}
	return ee.Code
}

// runE adapts a command body so that any error it returns without an
// explicit exit code is reported as an internal error.
func runE(fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		err := fn(cmd, args)
		if err == nil {
			return nil
		}
		var ee *ExitError
		if errors.As(err, &ee) {
			return err
		}
		return &ExitError{Code: ExitInternal, Err: err}
	}
}
