// Package cli defines Kestrel's commands. Commands parse flags, call
// internal/app, and render results; they contain no business logic.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Aareevs/Kestrel-CLI/internal/app"
	"github.com/Aareevs/Kestrel-CLI/internal/buildinfo"
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
	// Next is a concrete next step to try, shown under the error.
	Next string
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
	// OutIsTTY is true when Out is an interactive terminal (the pickers need
	// both ends).
	OutIsTTY bool
	// MakeRaw puts the terminal in raw mode for the pickers and returns a
	// function that restores it. Nil means input is already a stream of keys.
	MakeRaw func() (restore func(), err error)
	// TermSize reports the terminal's columns and rows for the pinned status
	// view. Nil means ask Out (tests supply a fixed size).
	TermSize func() (cols, rows int)
}

// termSize is the terminal's current size, (0, 0) when unknown.
func (g *globals) termSize() (int, int) {
	if g.streams.TermSize != nil {
		return g.streams.TermSize()
	}
	return output.TerminalWidth(g.streams.Out), output.TerminalHeight(g.streams.Out)
}

// DefaultStreams returns the real process streams.
func DefaultStreams() Streams {
	return Streams{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr,
		Getenv: os.Getenv, Getwd: os.Getwd,
		InIsTTY: output.IsTerminal(os.Stdin), OutIsTTY: output.IsTerminal(os.Stdout),
		MakeRaw: rawStdin,
	}
}

// rawStdin switches the terminal on stdin to raw mode (keys arrive one at a
// time, unechoed) and returns the function that puts it back.
func rawStdin() (func(), error) {
	fd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return func() { _ = term.Restore(fd, state) }, nil
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
	keys    *bufio.Reader // stdin for pickers, shared so typed-ahead keys carry over
}

func (g *globals) json() bool { return output.Format(g.format) == output.FormatJSON }

func (g *globals) text() output.TextOptions {
	return output.TextOptions{Style: g.style, Quiet: g.quiet, Width: output.TerminalWidth(g.streams.Out)}
}

// projectRoot is the resolved project directory, or "" if it can't be resolved.
func (g *globals) projectRoot() string {
	a, err := g.app()
	if err != nil {
		return ""
	}
	return a.ProjectRoot
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

// canWelcome reports whether the welcome screen fits: output (and input) is an
// interactive terminal and the user asked for neither JSON nor quiet.
func (g *globals) canWelcome() bool {
	return g.streams.OutIsTTY && g.streams.InIsTTY && !g.jsonFlag && g.format != "json" && !g.quiet
}

// NewRootCmd builds the command tree.
func NewRootCmd(s Streams) *cobra.Command {
	root, _ := newRoot(s)
	return root
}

func newRoot(s Streams) (*cobra.Command, *globals) {
	g := &globals{streams: s}
	root := &cobra.Command{
		Use:   "kestrel",
		Short: "Every failure has a trail.",
		Long: "Kestrel watches your local dev stack, reconstructs what broke and in what order,\n" +
			"ranks evidence-backed root-cause hypotheses, and verifies whether your fix worked.",
		SilenceErrors: true,
		SilenceUsage:  true,
		// A bare `kestrel` at a terminal opens the welcome screen. Anywhere
		// else (pipes, --json, --quiet) it prints help, as it always did.
		RunE: runE(func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 || !g.canWelcome() {
				return cmd.Help()
			}
			a, err := g.app()
			if err != nil {
				return err
			}
			output.Welcome(g.streams.Out, a.Welcome(cmd.Context(), buildinfo.Info().Version),
				output.TextOptions{Style: g.style, Width: output.TerminalWidth(g.streams.Out)})
			return nil
		}),
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
		newSchemaCmd(g),
		newDoctorCmd(g),
		newVersionCmd(g),
	)
	assignGroups(root)
	g.installHelp(root)
	return root, g
}

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context, s Streams, args []string) int {
	root, g := newRoot(s)
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
		st := output.NewStyle(output.ColorEnabled(s.Err, g.noColor, s.Getenv))
		fmt.Fprintf(s.Err, "%s %v\n", st.Red("kestrel:"), ee.Err)
		if ee.Next != "" {
			fmt.Fprintf(s.Err, "  %s %s\n", st.Bold("next:"), ee.Next)
		}
		if ee.Hint {
			fmt.Fprintln(s.Err, st.Dim("Run 'kestrel --help' for usage."))
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
