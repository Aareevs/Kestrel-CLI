package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func mcpApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	a, err := New(root, root)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", path, err, b)
	}
	return m
}

func TestMCPConfigShapesPerClient(t *testing.T) {
	a := mcpApp(t)
	for _, c := range []struct{ client, file, key string }{
		{api.MCPClaudeCode, ".mcp.json", "mcpServers"},
		{api.MCPCursor, ".cursor/mcp.json", "mcpServers"},
		{api.MCPVSCode, ".vscode/mcp.json", "servers"},
		{api.MCPClaudeDesktop, "", "mcpServers"},
		{api.MCPGeneric, "", "mcpServers"},
	} {
		s, err := a.MCPConfig(c.client, "kestrel")
		if err != nil {
			t.Fatal(c.client, err)
		}
		var doc map[string]map[string]map[string]any
		if err := json.Unmarshal([]byte(s.Snippet), &doc); err != nil {
			t.Fatalf("%s: %v", c.client, err)
		}
		e := doc[c.key]["kestrel"]
		args, _ := e["args"].([]any)
		if s.File != c.file || e["command"] != "kestrel" || len(args) != 3 || args[0] != "mcp" || args[2] != a.ProjectRoot {
			t.Errorf("%s: file %q entry %v", c.client, s.File, e)
		}
		if (e["type"] == "stdio") != (c.client == api.MCPVSCode) {
			t.Errorf("%s: type = %v", c.client, e["type"])
		}
		if (s.Command != "") != (c.client == api.MCPClaudeCode) {
			t.Errorf("%s: command %q", c.client, s.Command)
		}
	}
	var ue *UsageError
	if _, err := a.MCPConfig("emacs", "kestrel"); !errors.As(err, &ue) || !strings.Contains(ue.Next, "claude-code") {
		t.Errorf("unknown client: %v", err)
	}
}

func TestMCPInitCreatesMergesAndProtects(t *testing.T) {
	a := mcpApp(t)
	path := filepath.Join(a.ProjectRoot, ".cursor", "mcp.json")
	opts := MCPInitOptions{Client: api.MCPCursor, Binary: "kestrel"}

	// Creates the directory and file.
	s, err := a.MCPInit(opts)
	if err != nil || s.Action != api.MCPCreated {
		t.Fatalf("create: %v %+v", err, s)
	}
	// Running it again changes nothing.
	before, _ := os.ReadFile(path)
	if s, err = a.MCPInit(opts); err != nil || s.Action != api.MCPUnchanged {
		t.Fatalf("again: %v %+v", err, s)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("an unchanged init rewrote the file")
	}

	// Other servers and keys are kept when merging.
	os.WriteFile(path, []byte(`{"theme":"x","mcpServers":{"other":{"command":"foo"}}}`), 0o640)
	os.Chmod(path, 0o640)
	if s, err = a.MCPInit(opts); err != nil || s.Action != api.MCPMerged {
		t.Fatalf("merge: %v %+v", err, s)
	}
	doc := readJSON(t, path)
	servers := doc["mcpServers"].(map[string]any)
	if doc["theme"] != "x" || servers["other"] == nil || servers["kestrel"] == nil {
		t.Errorf("merge lost something: %v", doc)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o640 && filepath.Separator == '/' {
		t.Errorf("file mode changed to %v", st.Mode().Perm())
	}

	// A different kestrel entry is not replaced without --force.
	servers["kestrel"] = map[string]any{"command": "mine"}
	b, _ := json.Marshal(doc)
	os.WriteFile(path, b, 0o644)
	var ue *UsageError
	if _, err = a.MCPInit(opts); !errors.As(err, &ue) || !strings.Contains(ue.Next, "--force") {
		t.Fatalf("existing entry: %v", err)
	}
	if got := readJSON(t, path)["mcpServers"].(map[string]any)["kestrel"].(map[string]any)["command"]; got != "mine" {
		t.Errorf("entry was replaced without --force: %v", got)
	}
	opts.Force = true
	if s, err = a.MCPInit(opts); err != nil || s.Action != api.MCPMerged {
		t.Fatalf("force: %v %+v", err, s)
	}
	if got := readJSON(t, path)["mcpServers"].(map[string]any)["kestrel"].(map[string]any)["command"]; got != "kestrel" {
		t.Errorf("--force did not replace: %v", got)
	}
}

func TestMCPInitRefusesWhatItCannotSafelyChange(t *testing.T) {
	a := mcpApp(t)
	path := filepath.Join(a.ProjectRoot, ".mcp.json")
	for name, content := range map[string]string{
		"comments":      "{\n // my servers\n \"mcpServers\": {}\n}",
		"not an object": `{"mcpServers": ["x"]}`,
		"not json":      `servers: yes`,
	} {
		os.WriteFile(path, []byte(content), 0o644)
		_, err := a.MCPInit(MCPInitOptions{Client: api.MCPClaudeCode, Binary: "kestrel", Force: true})
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("%s: want a usage error, got %v", name, err)
		}
		if got, _ := os.ReadFile(path); string(got) != content {
			t.Errorf("%s: the file was changed", name)
		}
	}
	// Clients without a project file are pointed at `config`.
	_, err := a.MCPInit(MCPInitOptions{Client: api.MCPClaudeDesktop, Binary: "kestrel"})
	var ue *UsageError
	if !errors.As(err, &ue) || !strings.Contains(ue.Next, "kestrel mcp config") {
		t.Errorf("desktop: %v", err)
	}
}

func TestMCPInitDryRunWritesNothing(t *testing.T) {
	a := mcpApp(t)
	s, err := a.MCPInit(MCPInitOptions{Client: api.MCPVSCode, Binary: "kestrel", DryRun: true})
	if err != nil || s.Action != api.MCPPlanned || !strings.Contains(s.Snippet, `"type": "stdio"`) {
		t.Fatalf("%v %+v", err, s)
	}
	if entries, _ := os.ReadDir(a.ProjectRoot); len(entries) != 0 {
		t.Errorf("dry run created %v", entries)
	}
}

func TestResolveBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "kestrel")
	os.WriteFile(exe, []byte("x"), 0o755)
	other := filepath.Join(dir, "other")
	os.WriteFile(other, []byte("y"), 0o755)
	look := func(p string) func(string) (string, error) {
		return func(string) (string, error) {
			if p == "" {
				return "", errors.New("not found")
			}
			return p, nil
		}
	}
	if got := ResolveBinary(exe, look(exe)); got != "kestrel" {
		t.Errorf("same file on PATH: %q", got)
	}
	if got := ResolveBinary(exe, look(other)); got != exe {
		t.Errorf("a different kestrel on PATH must not be assumed: %q", got)
	}
	if got := ResolveBinary(exe, look("")); got != exe {
		t.Errorf("not on PATH: %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"kestrel": "kestrel", "/a b/c": `'/a b/c'`, "it's": `'it'\''s'`, "$(x)": `'$(x)'`} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
