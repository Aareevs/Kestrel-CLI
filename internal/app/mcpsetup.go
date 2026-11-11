package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

// MCPClients lists the client names `kestrel mcp config|init` accept.
var MCPClients = []string{api.MCPClaudeCode, api.MCPCursor, api.MCPVSCode, api.MCPClaudeDesktop, api.MCPGeneric}

// mcpTarget describes where a client keeps its MCP servers.
type mcpTarget struct {
	file     string // project-relative config file, "" when there isn't one Kestrel should write
	key      string // top-level key holding the servers
	stdioTag bool   // entries carry "type": "stdio"
	note     string
}

func mcpTargetFor(client string) (mcpTarget, error) {
	switch client {
	case api.MCPClaudeCode:
		return mcpTarget{file: ".mcp.json", key: "mcpServers"}, nil
	case api.MCPCursor:
		return mcpTarget{file: filepath.Join(".cursor", "mcp.json"), key: "mcpServers"}, nil
	case api.MCPVSCode:
		return mcpTarget{file: filepath.Join(".vscode", "mcp.json"), key: "servers", stdioTag: true}, nil
	case api.MCPClaudeDesktop:
		return mcpTarget{key: "mcpServers", note: "Claude Desktop keeps one config for the whole user (claude_desktop_config.json), so Kestrel prints the entry for you to merge instead of editing it."}, nil
	case api.MCPGeneric:
		return mcpTarget{key: "mcpServers"}, nil
	}
	return mcpTarget{}, &UsageError{Err: fmt.Errorf("unknown MCP client %q", client), Next: "use one of: " + strings.Join(MCPClients, ", ")}
}

// ResolveBinary picks how a client should start Kestrel: the bare name when it
// is on PATH and is this same program (so a Homebrew upgrade doesn't break the
// config), otherwise the absolute path of the running binary.
func ResolveBinary(exe string, lookPath func(string) (string, error)) string {
	if onPath, err := lookPath("kestrel"); err == nil {
		a, e1 := os.Stat(onPath)
		b, e2 := os.Stat(exe)
		if e1 == nil && e2 == nil && os.SameFile(a, b) {
			return "kestrel"
		}
	}
	return exe
}

func (a *App) mcpEntry(t mcpTarget, binary string) map[string]any {
	e := map[string]any{"command": binary, "args": []any{"mcp", "--project", a.ProjectRoot}}
	if t.stdioTag {
		e["type"] = "stdio"
	}
	return e
}

func marshalConfig(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return b.String()
}

// MCPConfig returns the config for a client, without touching any file.
func (a *App) MCPConfig(client, binary string) (*api.MCPSetup, error) {
	t, err := mcpTargetFor(client)
	if err != nil {
		return nil, err
	}
	setup := &api.MCPSetup{
		SchemaVersion: api.SchemaVersion, Client: client, Note: t.note,
		Snippet: marshalConfig(map[string]any{t.key: map[string]any{"kestrel": a.mcpEntry(t, binary)}}),
	}
	if t.file != "" {
		setup.File = filepath.ToSlash(t.file)
	}
	if client == api.MCPClaudeCode {
		setup.Command = fmt.Sprintf("claude mcp add kestrel -- %s mcp --project %s", shellQuote(binary), shellQuote(a.ProjectRoot))
	}
	return setup, nil
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`&|;<>()*?[]{}!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// MCPInitOptions control MCPInit.
type MCPInitOptions struct {
	Client string
	Binary string
	Force  bool // replace an existing, different "kestrel" entry
	DryRun bool
}

// MCPInit adds Kestrel's server to the client's project config file. It
// merges: other servers and keys are kept. It never replaces a different
// existing "kestrel" entry without Force, and refuses a file it cannot parse
// as plain JSON (rather than rewriting something it doesn't understand). The
// write is atomic.
func (a *App) MCPInit(o MCPInitOptions) (*api.MCPSetup, error) {
	setup, err := a.MCPConfig(o.Client, o.Binary)
	if err != nil {
		return nil, err
	}
	t, _ := mcpTargetFor(o.Client)
	if t.file == "" {
		return nil, &UsageError{
			Err:  fmt.Errorf("%s has no project config file for Kestrel to write", o.Client),
			Next: "kestrel mcp config --client " + o.Client + "   (prints the entry to add yourself)",
		}
	}
	path := filepath.Join(a.ProjectRoot, t.file)
	entry := a.mcpEntry(t, o.Binary)

	doc := map[string]any{}
	existed := false
	if raw, err := os.ReadFile(path); err == nil {
		existed = true
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &doc); err != nil {
				return nil, &UsageError{
					Err:  fmt.Errorf("%s is not plain JSON (%v); Kestrel won't rewrite it", setup.File, err),
					Next: "add the entry yourself: kestrel mcp config --client " + o.Client,
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	servers, _ := doc[t.key].(map[string]any)
	if doc[t.key] != nil && servers == nil {
		return nil, &UsageError{Err: fmt.Errorf("%s: %q is not an object; Kestrel won't change it", setup.File, t.key)}
	}
	if servers == nil {
		servers = map[string]any{}
	}
	if old, ok := servers["kestrel"]; ok {
		// Compare as decoded JSON, so formatting and key order don't matter.
		var want any
		_ = json.Unmarshal([]byte(marshalConfig(entry)), &want)
		if reflect.DeepEqual(old, want) {
			setup.Action = api.MCPUnchanged
			return setup, nil
		}
		if !o.Force {
			return nil, &UsageError{
				Err:  fmt.Errorf("%s already has a different \"kestrel\" server entry", setup.File),
				Next: "re-run with --force to replace it, or edit the file by hand",
			}
		}
	}
	servers["kestrel"] = entry
	doc[t.key] = servers
	out := marshalConfig(doc)
	setup.Snippet = out
	switch {
	case o.DryRun:
		setup.Action = api.MCPPlanned
		return setup, nil
	case existed:
		setup.Action = api.MCPMerged
	default:
		setup.Action = api.MCPCreated
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(path, []byte(out)); err != nil {
		return nil, err
	}
	return setup, nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kestrel-mcp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
