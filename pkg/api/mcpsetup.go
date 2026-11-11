package api

// MCP client names accepted by `kestrel mcp config` and `kestrel mcp init`.
const (
	MCPClaudeCode    = "claude-code"
	MCPCursor        = "cursor"
	MCPVSCode        = "vscode"
	MCPClaudeDesktop = "claude-desktop"
	MCPGeneric       = "generic"
)

// MCP init actions.
const (
	MCPCreated   = "created"
	MCPMerged    = "merged"
	MCPUnchanged = "unchanged"
	MCPPlanned   = "planned" // --dry-run: nothing was written
)

// MCPSetup is the output of `kestrel mcp config` and `kestrel mcp init`: how to
// point an MCP client at this project's Kestrel server.
type MCPSetup struct {
	SchemaVersion int    `json:"schema_version"`
	Client        string `json:"client"`
	// File is the config file the snippet belongs in (relative to the project
	// when it is inside it); empty when Kestrel has no single place to put it.
	File string `json:"file,omitempty"`
	// Snippet is the complete config file for File (or what to merge into it).
	Snippet string `json:"snippet"`
	// Command is an equivalent one-liner, for clients that have one.
	Command string `json:"command,omitempty"`
	// Action is set by `init`: what was (or, with --dry-run, would be) done.
	Action string `json:"action,omitempty"`
	Note   string `json:"note,omitempty"`
}
