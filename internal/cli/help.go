package cli

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Aareevs/Kestrel-CLI/internal/output"
)

// Command groups, in the order `kestrel --help` shows them.
var helpGroups = []struct{ id, title string }{
	{"start", "Get started"},
	{"run", "Run and watch"},
	{"investigate", "Investigate"},
	{"manage", "Manage"},
}

// commandOrder is the order commands appear in within their group (anything
// not listed follows, alphabetically).
var commandOrder = []string{
	"inspect", "init", "doctor",
	"run", "watch", "status",
	"diagnose", "timeline", "verify", "sessions",
	"config", "cleanup", "mcp", "version", "completion", "help",
}

// groupOf says which group each top-level command belongs to.
var groupOf = map[string]string{
	"inspect": "start", "init": "start", "doctor": "start",
	"run": "run", "watch": "run", "status": "run",
	"diagnose": "investigate", "timeline": "investigate", "verify": "investigate", "sessions": "investigate",
	"config": "manage", "cleanup": "manage", "mcp": "manage", "version": "manage",
}

func assignGroups(root *cobra.Command) {
	for _, gr := range helpGroups {
		root.AddGroup(&cobra.Group{ID: gr.id, Title: gr.title})
	}
	for _, c := range root.Commands() {
		c.GroupID = groupOf[c.Name()]
	}
	root.SetHelpCommandGroupID("manage")
	root.SetCompletionCommandGroupID("manage")
}

// installHelp replaces cobra's plain help with a grouped, colored one. Output
// that isn't a terminal gets the same text without color.
func (g *globals) installHelp(root *cobra.Command) {
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) { g.renderHelp(cmd.OutOrStdout(), cmd) })
	root.SetUsageFunc(func(cmd *cobra.Command) error {
		g.renderUsage(cmd.OutOrStderr(), cmd)
		return nil
	})
}

func (g *globals) helpStyle(w io.Writer) output.Style {
	return output.NewStyle(output.ColorEnabled(w, g.noColor, g.streams.Getenv))
}

// renderHelp prints a command's help: what it does, then its usage.
func (g *globals) renderHelp(w io.Writer, cmd *cobra.Command) {
	desc := cmd.Long
	if desc == "" {
		desc = cmd.Short
	}
	if desc != "" {
		fmt.Fprintln(w, strings.TrimRight(desc, "\n"))
		fmt.Fprintln(w)
	}
	g.renderUsage(w, cmd)
}

func (g *globals) renderUsage(w io.Writer, cmd *cobra.Command) {
	s := g.helpStyle(w)
	head := func(t string) { fmt.Fprintf(w, "%s\n", s.Bold(t)) }

	head("Usage")
	if cmd.Runnable() && !cmd.HasAvailableSubCommands() {
		fmt.Fprintf(w, "  %s\n", s.Cyan(cmd.UseLine()))
	}
	if cmd.HasAvailableSubCommands() {
		fmt.Fprintf(w, "  %s\n", s.Cyan(cmd.CommandPath()+" [command]"))
	}
	if len(cmd.Aliases) > 0 {
		fmt.Fprintf(w, "\n%s\n  %s\n", s.Bold("Aliases"), s.Cyan(strings.Join(append([]string{cmd.Name()}, cmd.Aliases...), ", ")))
	}
	if cmd.Example != "" {
		fmt.Fprintf(w, "\n%s\n%s\n", s.Bold("Examples"), cmd.Example)
	}

	if cmd.HasAvailableSubCommands() {
		g.renderCommands(w, cmd, s)
	}
	cols := output.TerminalWidth(w) // 0 off a terminal: pflag then doesn't wrap
	if fl := cmd.LocalFlags().FlagUsagesWrapped(cols); strings.TrimSpace(fl) != "" {
		fmt.Fprintf(w, "\n%s\n%s", s.Bold("Flags"), colorFlags(fl, s))
	}
	if fl := cmd.InheritedFlags().FlagUsagesWrapped(cols); strings.TrimSpace(fl) != "" {
		fmt.Fprintf(w, "\n%s\n%s", s.Bold("Global flags"), colorFlags(fl, s))
	}
	if cmd.HasAvailableSubCommands() {
		fmt.Fprintf(w, "\n%s\n", s.Dim(fmt.Sprintf("Use \"%s [command] --help\" for more about a command.", cmd.CommandPath())))
	}
}

// renderCommands lists subcommands: grouped at the top level, flat below.
func (g *globals) renderCommands(w io.Writer, cmd *cobra.Command, s output.Style) {
	cols := output.TerminalWidth(w)
	label := func(c *cobra.Command) string {
		if len(c.Aliases) == 0 {
			return c.Name()
		}
		return c.Name() + " (" + strings.Join(c.Aliases, ", ") + ")"
	}
	width := 0
	for _, c := range cmd.Commands() {
		if c.IsAvailableCommand() || c.Name() == "help" {
			width = max(width, len(label(c)))
		}
	}
	cmds := append([]*cobra.Command(nil), cmd.Commands()...)
	rank := func(c *cobra.Command) int {
		for i, n := range commandOrder {
			if n == c.Name() {
				return i
			}
		}
		return len(commandOrder)
	}
	sort.SliceStable(cmds, func(i, j int) bool {
		if ri, rj := rank(cmds[i]), rank(cmds[j]); ri != rj {
			return ri < rj
		}
		return cmds[i].Name() < cmds[j].Name()
	})
	row := func(c *cobra.Command) {
		l := label(c)
		pad := strings.Repeat(" ", width-len(l))
		name := s.Cyan(c.Name())
		if len(c.Aliases) > 0 {
			name += s.Dim(" (" + strings.Join(c.Aliases, ", ") + ")")
		}
		indent := strings.Repeat(" ", 2+width+2)
		desc := []string{c.Short}
		if cols > 0 {
			desc = output.Wrap(c.Short, max(cols-len(indent), 24))
		}
		for i, line := range desc {
			if i == 0 {
				fmt.Fprintf(w, "  %s%s  %s\n", name, pad, line)
			} else {
				fmt.Fprintf(w, "%s%s\n", indent, line)
			}
		}
	}
	if len(cmd.Groups()) == 0 {
		fmt.Fprintf(w, "\n%s\n", s.Bold("Commands"))
		for _, c := range cmds {
			if c.IsAvailableCommand() || c.Name() == "help" {
				row(c)
			}
		}
		return
	}
	for _, gr := range cmd.Groups() {
		first := true
		for _, c := range cmds {
			if c.GroupID != gr.ID || !(c.IsAvailableCommand() || c.Name() == "help") {
				continue
			}
			if first {
				fmt.Fprintf(w, "\n%s\n", s.Bold(gr.Title))
				first = false
			}
			row(c)
		}
	}
}

var flagLineRe = regexp.MustCompile(`^(\s+)(\S.*?\S)(\s{2,})(\S.*)$`)

// colorFlags colors the flag names in pflag's usage text.
func colorFlags(usage string, s output.Style) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(usage, "\n"), "\n") {
		if m := flagLineRe.FindStringSubmatch(line); m != nil {
			line = m[1] + s.Cyan(m[2]) + m[3] + m[4]
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// groupCommand makes a command that only groups subcommands (sessions,
// config): no subcommand shows its help, and an unknown one is an error with
// a suggestion, instead of silently printing help.
func groupCommand(cmd *cobra.Command) {
	rejectUnknownSubcommands(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
}

// rejectUnknownSubcommands makes a mistyped subcommand an error with a
// suggestion. For a command that also does something itself (kestrel mcp).
func rejectUnknownSubcommands(cmd *cobra.Command) {
	// cobra only applies its default (2) inside its own unknown-command path.
	cmd.SuggestionsMinimumDistance = 2
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
		if sug := cmd.SuggestionsFor(args[0]); len(sug) > 0 {
			msg += fmt.Sprintf("\n\nDid you mean this?\n\t%s", strings.Join(sug, "\n\t"))
		}
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("%s", msg), Hint: true}
	}
}
