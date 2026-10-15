package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validYAML = `version: 1
project:
  name: example-app
storage:
  retention_days: 7
  max_total_mb: 500
correlation:
  window: 10s
redaction:
  extra_patterns: ["acme_[a-z0-9]{32}"]
services:
  - name: database
    health: { type: tcp, address: localhost:5432 }
  - name: api
    dir: ./backend
    command: go run .
    env: { DATABASE_URL: "${DATABASE_URL}", LOG_LEVEL: debug }
    depends_on: [database]
    health: { type: http, url: http://localhost:8080/health, interval: 2s, timeout: 1s, startup_grace: 15s }
  - name: frontend
    dir: ./frontend
    command: npm run dev
    depends_on: [api]
    health: { type: http, url: http://localhost:5173 }
verify:
  checks:
    - { type: probe, service: api }
    - { type: command, run: go test ./..., dir: ./backend, timeout: 120s }
`

func projectWithDirs(t *testing.T, dirs ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestParseValid(t *testing.T) {
	root := projectWithDirs(t, "backend", "frontend")
	cfg, err := Parse("kestrel.yaml", []byte(validYAML), root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Services) != 3 {
		t.Fatalf("services = %d", len(cfg.Services))
	}
	api := cfg.Service("api")
	if api.Health.StartupGrace.D() != 15*time.Second || api.Health.Retries != DefaultHealthRetries || api.Health.ExpectStatus != "200-399" {
		t.Fatalf("health defaults not applied: %+v", api.Health)
	}
	fe := cfg.Service("frontend")
	if fe.Health.Interval.D() != DefaultHealthInterval || fe.Health.Timeout.D() != DefaultHealthTimeout {
		t.Fatalf("frontend health defaults: %+v", fe.Health)
	}
	if cfg.Correlation.Window.D() != 10*time.Second {
		t.Fatalf("window = %v", cfg.Correlation.Window)
	}
}

func TestDefaultsWhenOmitted(t *testing.T) {
	cfg, err := Parse("k.yaml", []byte("version: 1\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.RetentionDays != 7 || cfg.Storage.MaxTotalMB != 500 || cfg.Correlation.Window.D() != 10*time.Second {
		t.Fatalf("defaults: %+v", cfg)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string // substrings of "line N: field: message" rendered issues
	}{
		{"empty", "", []string{"file is empty"}},
		{"not a mapping", "- a\n- b\n", []string{"line 1: top level must be a mapping"}},
		{"syntax", "version: 1\nservices: [\n", []string{"line 2: did not find expected node content"}},
		{"missing version", "services: []\n", []string{"version: required"}},
		{"bad version", "version: 2\n", []string{"line 1: version: unsupported version 2"}},
		{"unknown top-level field", "version: 1\nservise: []\n", []string{`line 2: unknown field "servise" (valid here: correlation, project, redaction, services, storage, verify, version)`}},
		{"unknown service field", "version: 1\nservices:\n  - name: a\n    command: x\n    port: 80\n", []string{`line 5: unknown field "port"`}},
		{"wrong type", "version: 1\nstorage:\n  retention_days: lots\n", []string{"line 3:", "cannot unmarshal"}},
		{"bad duration", "version: 1\ncorrelation:\n  window: soon\n", []string{`line 3: invalid duration "soon"`}},
		{"duplicate key", "version: 1\nversion: 1\n", []string{"line 2:", "already defined"}},
		{"missing name", "version: 1\nservices:\n  - command: x\n", []string{"line 3: services[0].name: required"}},
		{"bad name", "version: 1\nservices:\n  - name: 'my service'\n    command: x\n", []string{"line 3: services[0].name:", "not a valid name"}},
		{"duplicate name", "version: 1\nservices:\n  - {name: a, command: x}\n  - {name: a, command: y}\n", []string{"line 4: services.a.name: duplicate service name"}},
		{"nothing to do", "version: 1\nservices:\n  - name: a\n", []string{`services.a: needs a "command"`}},
		{"unknown dependency", "version: 1\nservices:\n  - {name: api, command: x, depends_on: [databse]}\n  - {name: database, command: y}\n", []string{`unknown service "databse" (did you mean "database"?)`}},
		{"self dependency", "version: 1\nservices:\n  - {name: a, command: x, depends_on: [a]}\n", []string{"cannot depend on itself"}},
		{"cycle", "version: 1\nservices:\n  - {name: a, command: x, depends_on: [b]}\n  - {name: b, command: x, depends_on: [c]}\n  - {name: c, command: x, depends_on: [a]}\n", []string{"dependency cycle: a → b → c → a"}},
		{"http needs url", "version: 1\nservices:\n  - name: a\n    health: {type: http}\n", []string{"services.a.health.url: required"}},
		{"bad url", "version: 1\nservices:\n  - name: a\n    health: {type: http, url: 'localhost:80'}\n", []string{"must be an absolute http"}},
		{"bad tcp address", "version: 1\nservices:\n  - name: a\n    health: {type: tcp, address: localhost}\n", []string{"must be host:port"}},
		{"bad port", "version: 1\nservices:\n  - name: a\n    health: {type: tcp, address: 'localhost:99999'}\n", []string{"must be host:port"}},
		{"unknown probe", "version: 1\nservices:\n  - name: a\n    health: {type: grpc, address: 'x:1'}\n", []string{`unknown probe type "grpc"`}},
		{"process needs command", "version: 1\nservices:\n  - name: a\n    health: {type: process}\n", []string{"process probes need the service to have a command"}},
		{"timeout over interval", "version: 1\nservices:\n  - name: a\n    health: {type: tcp, address: 'x:1', interval: 1s, timeout: 5s}\n", []string{"timeout (5s) must not exceed interval (1s)"}},
		{"bad expect_status", "version: 1\nservices:\n  - name: a\n    health: {type: http, url: 'http://x', expect_status: '399-200'}\n", []string{"expect_status"}},
		{"env bad ref", "version: 1\nservices:\n  - name: a\n    command: x\n    env: {A: '${OOPS'}\n", []string{"unclosed"}},
		{"env bad key", "version: 1\nservices:\n  - name: a\n    command: x\n    env: {'1A': b}\n", []string{"not a valid environment variable name"}},
		{"bad regex", "version: 1\nredaction:\n  extra_patterns: ['(']\n", []string{"line 3: redaction.extra_patterns[0]: invalid regular expression"}},
		{"verify unknown service", "version: 1\nservices:\n  - {name: a, command: x}\nverify:\n  checks:\n    - {type: probe, service: b}\n", []string{`unknown service "b"`}},
		{"verify no health", "version: 1\nservices:\n  - {name: a, command: x}\nverify:\n  checks:\n    - {type: probe, service: a}\n", []string{"has no health probe"}},
		{"verify command needs run", "version: 1\nverify:\n  checks:\n    - {type: command}\n", []string{"verify.checks[0].run: required"}},
		{"verify bad type", "version: 1\nverify:\n  checks:\n    - {type: shell, run: x}\n", []string{`unknown check type "shell"`}},
		{"negative retention", "version: 1\nstorage: {retention_days: -1}\n", []string{"storage.retention_days: must be at least 1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("kestrel.yaml", []byte(tt.yaml), "")
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want *ValidationError, got %v", err)
			}
			var all []string
			for _, is := range ve.Issues {
				all = append(all, is.String())
			}
			joined := strings.Join(all, "\n")
			for _, w := range tt.want {
				if !strings.Contains(joined, w) {
					t.Errorf("issues do not contain %q:\n%s", w, joined)
				}
			}
		})
	}
}

func TestMissingServiceDir(t *testing.T) {
	root := t.TempDir()
	_, err := Parse("k.yaml", []byte("version: 1\nservices:\n  - name: a\n    dir: ./nope\n    command: x\n"), root)
	if err == nil || !strings.Contains(err.Error(), `line 4: services.a.dir: directory "./nope" does not exist`) {
		t.Fatalf("got %v", err)
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	if p, err := Find(dir); p != "" || err != nil {
		t.Fatalf("empty dir: %q %v", p, err)
	}
	os.WriteFile(filepath.Join(dir, ".kestrel.yaml"), []byte("version: 1\n"), 0o644)
	if p, _ := Find(dir); filepath.Base(p) != ".kestrel.yaml" {
		t.Fatalf("got %q", p)
	}
	os.WriteFile(filepath.Join(dir, "kestrel.yaml"), []byte("version: 1\n"), 0o644)
	if _, err := Find(dir); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("want ErrAmbiguous, got %v", err)
	}
}

func TestFindProjectRoot(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	os.MkdirAll(deep, 0o755)
	if got, found := FindProjectRoot(deep); found || got != deep {
		t.Fatalf("no config: got %q found=%v", got, found)
	}
	os.WriteFile(filepath.Join(root, "kestrel.yaml"), []byte("version: 1\n"), 0o644)
	if got, found := FindProjectRoot(deep); !found || got != root {
		t.Fatalf("got %q found=%v, want %q", got, found, root)
	}
}

func TestLoadTooLarge(t *testing.T) {
	p := filepath.Join(t.TempDir(), "kestrel.yaml")
	os.WriteFile(p, []byte("version: 1\n#"+strings.Repeat("x", MaxFileSize)), 0o644)
	if _, err := Load(p, ""); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("got %v", err)
	}
}

func TestEnvRefs(t *testing.T) {
	names, err := Refs("postgres://${DB_USER}:${DB_PASS}@localhost/$literal")
	if err != nil || strings.Join(names, ",") != "DB_USER,DB_PASS" {
		t.Fatalf("Refs = %v, %v", names, err)
	}
	for _, bad := range []string{"${", "${A", "${1A}", "${}"} {
		if _, err := Refs(bad); err == nil {
			t.Errorf("Refs(%q) should fail", bad)
		}
	}
	lookup := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	got, err := Expand("a=${A} b=${B}", lookup(map[string]string{"A": "1", "B": "2"}))
	if err != nil || got != "a=1 b=2" {
		t.Fatalf("Expand = %q, %v", got, err)
	}
	_, err = Expand("${A}${SECRET}", lookup(map[string]string{"SECRET": "hunter2"}))
	if err == nil || !strings.Contains(err.Error(), "A") || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("missing-var error must name the variable and never leak values: %v", err)
	}
}

func TestExpandEnvDoesNotMutateConfig(t *testing.T) {
	cfg := &Config{Services: []Service{{Name: "api", Env: map[string]string{"TOKEN": "${TOKEN}"}}}}
	env, err := cfg.ExpandEnv(func(string) (string, bool) { return "s3cret", true })
	if err != nil || env["api"]["TOKEN"] != "s3cret" {
		t.Fatalf("got %v %v", env, err)
	}
	if cfg.Services[0].Env["TOKEN"] != "${TOKEN}" {
		t.Fatal("ExpandEnv must not write resolved values into the config")
	}
}

func TestMasked(t *testing.T) {
	cfg := &Config{
		Services: []Service{{
			Name:    "api",
			Command: "API_KEY=abc123 DEBUG=1 go run .",
			Env:     map[string]string{"REF": "${DATABASE_URL}", "LIT": "hunter2", "MIXED": "postgres://u:${PW}@h"},
			Health:  &Health{Type: HealthHTTP, URL: "http://admin:pa55@localhost:8080/health"},
		}},
		Verify: Verify{Checks: []Check{{Type: CheckCommand, Run: "GITHUB_TOKEN=ghp_x go test ./..."}}},
	}
	m := cfg.Masked()
	s := m.Services[0]
	if s.Env["REF"] != "${DATABASE_URL}" || s.Env["LIT"] != Mask || s.Env["MIXED"] != Mask {
		t.Fatalf("env masking: %v", s.Env)
	}
	if strings.Contains(s.Command, "abc123") || !strings.Contains(s.Command, "DEBUG=1") {
		t.Fatalf("command masking: %q", s.Command)
	}
	if strings.Contains(s.Health.URL, "pa55") {
		t.Fatalf("url masking: %q", s.Health.URL)
	}
	if strings.Contains(m.Verify.Checks[0].Run, "ghp_x") {
		t.Fatalf("verify masking: %q", m.Verify.Checks[0].Run)
	}
	if cfg.Services[0].Env["LIT"] != "hunter2" || cfg.Services[0].Health.URL != "http://admin:pa55@localhost:8080/health" {
		t.Fatal("Masked must not modify the original")
	}
}
