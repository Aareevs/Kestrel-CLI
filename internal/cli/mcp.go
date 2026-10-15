package cli

import (
	"github.com/spf13/cobra"

	"github.com/Aareevs/Kestrel-CLI/internal/buildinfo"
	"github.com/Aareevs/Kestrel-CLI/internal/mcp"
)

func newMCPCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Read-only MCP server over stdio, for AI agents",
		Long: "Serves Kestrel's evidence to an MCP client (Claude Code, Cursor, …) over stdin/stdout.\n" +
			"Tools: inspect_project, list_services, get_service_status, get_recent_errors,\n" +
			"get_failure_timeline, get_diagnosis, get_session_summary, get_verification_results.\n" +
			"All tools are read-only: none starts, stops, signals, or executes anything. Every result is redacted.\n\n" +
			"Setup (Claude Code):  claude mcp add kestrel -- kestrel mcp --project /path/to/project",
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
}
