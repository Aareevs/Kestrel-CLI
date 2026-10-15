// Package integration runs the kestrel CLI in-process against fixture
// projects and compares output with golden files.
//
// Regenerate goldens with: go test ./tests/integration -update
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Aareevs/Kestrel-CLI/internal/cli"
	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

var update = flag.Bool("update", false, "rewrite golden files")

var fixtures = []string{"node-vite", "go-api", "compose-stack", "python-django"}

type result struct {
	code   int
	stdout string
	stderr string
}

// run executes the CLI with args. stdin, if non-nil, is treated as a TTY.
func run(t *testing.T, cwd string, stdin *string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	s := cli.Streams{
		In:     strings.NewReader(""),
		Out:    &out,
		Err:    &errb,
		Getenv: func(k string) string { return "" },
		Getwd:  func() (string, error) { return cwd, nil },
	}
	if stdin != nil {
		s.In = strings.NewReader(*stdin)
		s.InIsTTY = true
	}
	code := cli.Execute(context.Background(), s, args)
	return result{code: code, stdout: out.String(), stderr: errb.String()}
}

func fixtureDir(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "fixtures", "projects", name))
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// normalize replaces machine-specific paths so goldens are portable.
func normalize(s, root string) string {
	s = strings.ReplaceAll(s, root, "<root>")
	if js, err := json.Marshal(root); err == nil {
		s = strings.ReplaceAll(s, strings.Trim(string(js), `"`), "<root>")
	}
	return s
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", path, err)
	}
	if string(want) != got {
		t.Errorf("%s mismatch\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

func TestInspectGolden(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			root := fixtureDir(t, name)
			text := run(t, root, nil, "inspect", "--no-color")
			if text.code != 0 {
				t.Fatalf("exit %d: %s", text.code, text.stderr)
			}
			golden(t, "inspect/"+name+".txt", normalize(text.stdout, root))

			js := run(t, root, nil, "inspect", "--format", "json")
			if js.code != 0 {
				t.Fatalf("exit %d: %s", js.code, js.stderr)
			}
			var res api.InspectResult
			if err := json.Unmarshal([]byte(js.stdout), &res); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if res.Root != root || res.SchemaVersion != api.SchemaVersion || len(res.Candidates) == 0 {
				t.Fatalf("unexpected result: root=%q schema=%d candidates=%d", res.Root, res.SchemaVersion, len(res.Candidates))
			}
			for _, c := range res.Candidates {
				if len(c.Sources) == 0 {
					t.Errorf("%s: candidate without sources", c.Name)
				}
				if c.Command != nil && c.Command.Source.File == "" {
					t.Errorf("%s: command without source", c.Name)
				}
				for _, d := range c.DependsOn {
					if d.Source.File == "" {
						t.Errorf("%s: dependency %s without source", c.Name, d.Name)
					}
				}
			}
			golden(t, "inspect/"+name+".json", normalize(js.stdout, root))
		})
	}
}

func TestInspectFromSubdirectoryUsesCwd(t *testing.T) {
	root := fixtureDir(t, "compose-stack")
	r := run(t, filepath.Join(root, "web"), nil, "inspect", "--format", "json")
	var res api.InspectResult
	if err := json.Unmarshal([]byte(r.stdout), &res); err != nil {
		t.Fatal(err)
	}
	if res.Root != filepath.Join(root, "web") || len(res.Candidates) != 1 || res.Candidates[0].Name != "web" {
		t.Fatalf("got root %q, %d candidates", res.Root, len(res.Candidates))
	}
}

func TestInitPrintGolden(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			root := fixtureDir(t, name)
			r := run(t, root, nil, "init", "--print")
			if r.code != 0 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			golden(t, "init/"+name+".yaml", r.stdout)
			if _, err := os.Stat(filepath.Join(root, "kestrel.yaml")); err == nil {
				t.Fatal("init --print must not write a file")
			}
		})
	}
}

// copyFixture copies a fixture into a temp dir so tests can write to it.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src := fixtureDir(t, name)
	dst, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestInitWritesValidConfigAndNeverOverwrites(t *testing.T) {
	root := copyFixture(t, "compose-stack")
	r := run(t, root, nil, "init", "--yes", "--no-color")
	if r.code != 0 {
		t.Fatalf("init exit %d: %s", r.code, r.stderr)
	}
	path := filepath.Join(root, "kestrel.yaml")
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if v := run(t, root, nil, "config", "validate"); v.code != 0 {
		t.Fatalf("generated config invalid (exit %d): %s%s", v.code, v.stdout, v.stderr)
	}

	r = run(t, root, nil, "init", "--yes")
	if r.code != cli.ExitUsage || !strings.Contains(r.stderr, "never overwrites") {
		t.Fatalf("second init: exit %d, stderr %q", r.code, r.stderr)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(written, after) {
		t.Fatal("existing kestrel.yaml was modified")
	}

	// An existing .kestrel.yaml also blocks init.
	root2 := copyFixture(t, "go-api")
	os.WriteFile(filepath.Join(root2, ".kestrel.yaml"), []byte("version: 1\n"), 0o644)
	if r := run(t, root2, nil, "init", "--yes"); r.code != cli.ExitUsage {
		t.Fatalf("init with .kestrel.yaml: exit %d", r.code)
	}
	if _, err := os.Stat(filepath.Join(root2, "kestrel.yaml")); err == nil {
		t.Fatal("init wrote kestrel.yaml next to .kestrel.yaml")
	}
}

func TestInitConfirmation(t *testing.T) {
	root := copyFixture(t, "go-api")
	path := filepath.Join(root, "kestrel.yaml")

	r := run(t, root, nil, "init")
	if r.code != cli.ExitUsage || !strings.Contains(r.stderr, "--yes") {
		t.Fatalf("non-interactive init: exit %d, stderr %q", r.code, r.stderr)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("wrote without confirmation")
	}

	no := "n\n"
	if r := run(t, root, &no, "init"); r.code != 0 || !strings.Contains(r.stdout, "Nothing written") {
		t.Fatalf("declined init: exit %d, %q", r.code, r.stdout)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("wrote after the user declined")
	}

	yes := "y\n"
	if r := run(t, root, &yes, "init"); r.code != 0 {
		t.Fatalf("confirmed init: exit %d: %s", r.code, r.stderr)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("did not write after confirmation")
	}
}

func TestConfigCommands(t *testing.T) {
	// Kestrel reports the project root with symlinks resolved (/var -> /private/var on macOS).
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(root, "backend"), 0o755)

	if r := run(t, root, nil, "config", "show"); r.code != cli.ExitUsage || !strings.Contains(r.stderr, "kestrel init") {
		t.Fatalf("show without config: exit %d %q", r.code, r.stderr)
	}
	if r := run(t, root, nil, "config", "path"); r.code != 0 || !strings.Contains(r.stdout, "zero-config") {
		t.Fatalf("path without config: exit %d %q", r.code, r.stdout)
	}

	cfg := `version: 1
services:
  - name: api
    dir: ./backend
    command: API_TOKEN=tok_abc123 go run .
    env:
      DATABASE_URL: "${DATABASE_URL}"
      SESSION_SECRET: "literal-secret-value"
    health:
      type: http
      url: http://admin:hunter2@localhost:8080/health
`
	os.WriteFile(filepath.Join(root, "kestrel.yaml"), []byte(cfg), 0o644)

	for _, format := range []string{"text", "json"} {
		r := run(t, root, nil, "config", "show", "--format", format)
		if r.code != 0 {
			t.Fatalf("show %s: exit %d %s", format, r.code, r.stderr)
		}
		for _, secret := range []string{"tok_abc123", "literal-secret-value", "hunter2"} {
			if strings.Contains(r.stdout, secret) {
				t.Errorf("config show --format %s leaked %q:\n%s", format, secret, r.stdout)
			}
		}
		if !strings.Contains(r.stdout, "${DATABASE_URL}") || !strings.Contains(r.stdout, "startup_grace") {
			t.Errorf("show %s should keep references and show defaults:\n%s", format, r.stdout)
		}
	}

	if r := run(t, root, nil, "config", "validate", "--no-color"); r.code != 0 || !strings.Contains(r.stdout, "is valid (1 service)") {
		t.Fatalf("validate: exit %d %q", r.code, r.stdout)
	}
	if r := run(t, root, nil, "config", "path"); strings.TrimSpace(r.stdout) != filepath.Join(root, "kestrel.yaml") {
		t.Fatalf("path: %q", r.stdout)
	}

	bad := "version: 1\nservices:\n  - name: api\n    command: x\n    depends_on: [db]\n    helth: {}\n"
	os.WriteFile(filepath.Join(root, "kestrel.yaml"), []byte(bad), 0o644)
	r := run(t, root, nil, "config", "validate", "--no-color")
	if r.code != cli.ExitUsage || !strings.Contains(r.stdout, `kestrel.yaml:6  unknown field "helth"`) {
		t.Fatalf("invalid: exit %d\n%s", r.code, r.stdout)
	}
	r = run(t, root, nil, "config", "validate", "--format", "json")
	var v api.ConfigValidation
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil || v.Valid || len(v.Issues) == 0 || v.Issues[0].Line != 6 {
		t.Fatalf("json validation: %v %+v", err, v)
	}
	if r := run(t, root, nil, "config", "show"); r.code != cli.ExitUsage {
		t.Fatalf("show invalid config: exit %d", r.code)
	}

	// The project root is found from a subdirectory.
	os.WriteFile(filepath.Join(root, "kestrel.yaml"), []byte(cfg), 0o644)
	if r := run(t, filepath.Join(root, "backend"), nil, "config", "path"); strings.TrimSpace(r.stdout) != filepath.Join(root, "kestrel.yaml") {
		t.Fatalf("path from subdir: %q", r.stdout)
	}
}

func TestUsageErrors(t *testing.T) {
	cwd := t.TempDir()
	for _, args := range [][]string{
		{"nope"},
		{"inspect", "--bogus"},
		{"inspect", "extra-arg"},
		{"inspect", "--format", "xml"},
		{"inspect", "--verbose", "--quiet"},
		{"inspect", "--project", filepath.Join(cwd, "missing")},
	} {
		if r := run(t, cwd, nil, args...); r.code != cli.ExitUsage {
			t.Errorf("%v: exit %d, want %d (stderr %q)", args, r.code, cli.ExitUsage, r.stderr)
		}
	}
}

func TestVersionJSON(t *testing.T) {
	r := run(t, t.TempDir(), nil, "version", "--format", "json")
	var v api.VersionInfo
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil || v.Version == "" || v.GoVersion == "" || v.Platform == "" {
		t.Fatalf("version: %v %+v", err, v)
	}
}

func TestDoctor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KESTREL_HOME", home)
	r := run(t, t.TempDir(), nil, "doctor", "--format", "json")
	var rep api.DoctorReport
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if r.code != 0 || !rep.OK {
		t.Fatalf("doctor: exit %d %+v", r.code, rep)
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Fatalf("doctor left files behind: %v", entries)
	}

	// Data dir path is a file → fail, exit 4.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	os.WriteFile(file, nil, 0o644)
	t.Setenv("KESTREL_HOME", file)
	if r := run(t, t.TempDir(), nil, "doctor"); r.code != cli.ExitInternal || !strings.Contains(r.stdout, "not a directory") {
		t.Fatalf("doctor with file as data dir: exit %d\n%s", r.code, r.stdout)
	}
}

func TestDoctorPermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced here")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	t.Setenv("KESTREL_HOME", dir)
	r := run(t, t.TempDir(), nil, "doctor", "--no-color")
	if r.code != cli.ExitInternal || !strings.Contains(r.stdout, "is not writable") {
		t.Fatalf("exit %d\n%s", r.code, r.stdout)
	}
}
