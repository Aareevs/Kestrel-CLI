package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Aareevs/Kestrel-CLI/internal/app"
	"github.com/Aareevs/Kestrel-CLI/internal/buildinfo"
	"github.com/Aareevs/Kestrel-CLI/internal/mcp"
	"github.com/Aareevs/Kestrel-CLI/internal/output"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func newMCPCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Read-only MCP server over stdio for AI agents (and `mcp init` to set it up)",
		Long: "Serves Kestrel's evidence to an MCP client (Claude Code, Cursor, …) over stdin/stdout.\n" +
			"Tools: inspect_project, list_services, list_sessions, get_service_status, get_recent_errors,\n" +
			"search_events, get_failure_timeline, get_diagnosis, get_session_summary, get_verification_results,\n" +
			"validate_config.\n" +
			"All tools are read-only: none starts, stops, signals, or executes anything. Every result is redacted.\n\n" +
			"Set up a client:  kestrel mcp init --client claude-code   (or: kestrel mcp config to just print it)",
		Args: cobra.NoArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			red, err := a.Redactor()
			if err != nil {
				return appErr(err)
			}
			return mcp.New(a, red, buildinfo.Info().Version).Serve(cmd.Context(), g.streams.In, g.streams.Out)
		}),
	}
	rejectUnknownSubcommands(cmd)
	cmd.AddCommand(newMCPConfigCmd(g), newMCPInitCmd(g))
	return cmd
}

// mcpBinary is how a client should start Kestrel.
func mcpBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return "kestrel"
	}
	return app.ResolveBinary(exe, exec.LookPath)
}

func clientFlag(cmd *cobra.Command, v *string) {
	cmd.Flags().StringVar(v, "client", api.MCPClaudeCode, "MCP client: "+strings.Join(app.MCPClients, ", "))
}

func newMCPConfigCmd(g *globals) *cobra.Command {
	var client string
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Print the MCP config for a client (writes nothing)",
		Long: "Prints the entry that points an MCP client at this project's Kestrel server, ready to paste.\n" +
			"For one that writes it into the project's config file, use `kestrel mcp init`.",
		Args: cobra.NoArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			setup, err := a.MCPConfig(client, mcpBinary())
			if err != nil {
				return appErr(err)
			}
			return g.renderMCP(setup, false)
		}),
	}
	clientFlag(cmd, &client)
	return cmd
}

func newMCPInitCmd(g *globals) *cobra.Command {
	var (
		client        string
		force, dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Add Kestrel's MCP server to a client's project config file",
		Long: "Writes the server entry into the client's project config (.mcp.json for claude-code,\n" +
			".cursor/mcp.json, .vscode/mcp.json). Other servers in the file are kept. A different existing\n" +
			"\"kestrel\" entry is never replaced without --force, and a file that isn't plain JSON is left alone.\n" +
			"Use --dry-run to see the result first. claude-desktop and generic have no project file: use\n" +
			"`kestrel mcp config` for those.",
		Args: cobra.NoArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			a, err := g.app()
			if err != nil {
				return err
			}
			setup, err := a.MCPInit(app.MCPInitOptions{Client: client, Binary: mcpBinary(), Force: force, DryRun: dryRun})
			if err != nil {
				return appErr(err)
			}
			return g.renderMCP(setup, true)
		}),
	}
	clientFlag(cmd, &client)
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing, different \"kestrel\" entry")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be written, and write nothing")
	return cmd
}

func (g *globals) renderMCP(s *api.MCPSetup, initCmd bool) error {
	if g.json() {
		return output.WriteJSON(g.streams.Out, s)
	}
	w, st := g.streams.Out, g.style
	switch s.Action {
	case api.MCPCreated:
		fmt.Fprintf(w, "%s created %s\n", st.Green("✓"), s.File)
	case api.MCPMerged:
		fmt.Fprintf(w, "%s added the kestrel server to %s (other servers kept)\n", st.Green("✓"), s.File)
	case api.MCPUnchanged:
		fmt.Fprintf(w, "%s %s already has this entry; nothing to do\n", st.Green("✓"), s.File)
		return nil
	case api.MCPPlanned:
		fmt.Fprintf(w, "%s would write %s (dry run, nothing written):\n\n", st.Bold("·"), s.File)
	default:
		if s.File != "" {
			fmt.Fprintf(w, "%s %s\n\n", st.Dim("add to"), s.File)
		}
	}
	if !initCmd || s.Action == api.MCPPlanned {
		fmt.Fprint(w, s.Snippet)
		if s.Command != "" {
			fmt.Fprintf(w, "\n%s\n  %s\n", st.Dim("or, for Claude Code:"), s.Command)
		}
		if s.Note != "" {
			fmt.Fprintf(w, "\n%s\n", st.Dim(s.Note))
		}
	}
	if initCmd && s.Action != api.MCPPlanned {
		fmt.Fprintf(w, "%s restart the client so it picks the server up; it only reads recorded sessions, so keep using `kestrel run` / `kestrel watch`.\n", st.Dim("next:"))
	}
	return nil
}
