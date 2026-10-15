package discovery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kestrel-cli/kestrel/pkg/api"
)

// tree writes files (slash paths → content) under a new temp dir.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func scan(t *testing.T, root string, mod ...func(*Options)) *api.InspectResult {
	t.Helper()
	o := Options{Root: root}
	for _, m := range mod {
		m(&o)
	}
	res, err := Scan(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func names(res *api.InspectResult) string {
	var n []string
	for _, c := range res.Candidates {
		n = append(n, c.Name+"@"+c.Dir)
	}
	return strings.Join(n, ",")
}

func find(t *testing.T, res *api.InspectResult, name string) api.ServiceCandidate {
	t.Helper()
	for _, c := range res.Candidates {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no candidate %q in [%s]", name, names(res))
	return api.ServiceCandidate{}
}

const devPkg = `{"name":"%s","scripts":{"dev":"vite"},"devDependencies":{"vite":"5"}}`

func pkg(name string) string { return strings.Replace(devPkg, "%s", name, 1) }

func TestSkipsVendoredAndIgnoredDirs(t *testing.T) {
	root := tree(t, map[string]string{
		"package.json":                    pkg("app"),
		"node_modules/dep/package.json":   pkg("dep"),
		"vendor/x/package.json":           pkg("vendored"),
		"dist/package.json":               pkg("dist"),
		".git/package.json":               pkg("git"),
		".venv/lib/package.json":          pkg("venv"),
		"generated/package.json":          pkg("generated"),
		"tools/a.local/package.json":      pkg("local"),
		"tools/keep.local/package.json":   pkg("kept"),
		"nested/.gitignore":               "inner/\n",
		"nested/inner/package.json":       pkg("inner"),
		"nested/other/package.json":       pkg("other"),
		"docs/anchored/package.json":      pkg("anchored"),
		"deep/docs/anchored/package.json": pkg("notanchored"),
		".gitignore":                      "# comment\ngenerated/\n*.local\n!keep.local\n/docs/anchored\n",
	})
	res := scan(t, root)
	got := names(res)
	for _, want := range []string{"app@.", "kept@./tools/keep.local", "other@./nested/other", "notanchored@./deep/docs/anchored"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	for _, bad := range []string{"dep", "vendored", "dist@", "git@", "venv", "generated", "local@", "inner", "anchored@./docs"} {
		if strings.Contains(got, bad) {
			t.Errorf("%s should have been skipped: %s", bad, got)
		}
	}
}

func TestGitignoreMatch(t *testing.T) {
	gi := parseGitignore("*.log\n!important.log\nbuild/\n/root-only\na/**/z\n**/cache\ndocs/*.md\nfoo?\n[ab]x\n\\#hash\n")
	tests := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"x.log", false, true},
		{"sub/x.log", false, true},
		{"important.log", false, false},
		{"build", true, true},
		{"build", false, false},
		{"sub/build", true, true},
		{"root-only", false, true},
		{"sub/root-only", false, false},
		{"a/z", false, true},
		{"a/b/c/z", false, true},
		{"cache", true, true},
		{"x/y/cache", true, true},
		{"docs/a.md", false, true},
		{"docs/sub/a.md", false, false},
		{"food", false, true},
		{"foo", false, false},
		{"ax", false, true},
		{"cx", false, false},
		{"#hash", false, true},
	}
	for _, tt := range tests {
		m, neg := gi.match(tt.path, tt.isDir)
		if got := m && !neg; got != tt.want {
			t.Errorf("match(%q, dir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
		}
	}
}

func TestSymlinksOutsideRootAreNotRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	outside := tree(t, map[string]string{"package.json": pkg("outside"), "pkg/package.json": pkg("outsidedir")})
	root := tree(t, map[string]string{"readme.md": "hi"})
	if err := os.Symlink(filepath.Join(outside, "package.json"), filepath.Join(root, "package.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "pkg"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	res := scan(t, root)
	if len(res.Candidates) != 0 {
		t.Fatalf("read through symlink: %s", names(res))
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "symlink points outside the project root") {
		t.Fatalf("expected a warning, got %v", res.Warnings)
	}
}

func TestSymlinkInsideRootIsRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := tree(t, map[string]string{"real/package.json": pkg("real")})
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real", "package.json"), filepath.Join(root, "app", "package.json")); err != nil {
		t.Fatal(err)
	}
	res := scan(t, root)
	if got := names(res); !strings.Contains(got, "real@./app") {
		t.Fatalf("got %s", got)
	}
}

func TestDepthLimit(t *testing.T) {
	root := tree(t, map[string]string{
		"a/b/c/package.json":   pkg("depth3"),
		"a/b/c/d/package.json": pkg("depth4"),
	})
	got := names(scan(t, root))
	if !strings.Contains(got, "depth3") || strings.Contains(got, "depth4") {
		t.Fatalf("default depth: %s", got)
	}
	got = names(scan(t, root, func(o *Options) { o.MaxDepth = 4 }))
	if !strings.Contains(got, "depth4") {
		t.Fatalf("depth 4: %s", got)
	}
}

func TestFileLimit(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 20; i++ {
		files[filepath.ToSlash(filepath.Join("f", string(rune('a'+i))+".txt"))] = "x"
	}
	res := scan(t, tree(t, files), func(o *Options) { o.MaxFiles = 5 })
	if !res.Scan.Truncated || res.Scan.FilesSeen != 5 || !strings.Contains(res.Scan.TruncatedReason, "file limit") {
		t.Fatalf("scan stats: %+v", res.Scan)
	}
}

func TestTimeBudget(t *testing.T) {
	root := tree(t, map[string]string{"a/x": "", "b/y": "", "c/z": ""})
	now := time.Unix(0, 0)
	res := scan(t, root, func(o *Options) {
		o.Budget = time.Second
		o.Now = func() time.Time { now = now.Add(400 * time.Millisecond); return now }
	})
	if !res.Scan.Truncated || !strings.Contains(res.Scan.TruncatedReason, "time budget") {
		t.Fatalf("scan stats: %+v", res.Scan)
	}
}

func TestCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := Scan(ctx, Options{Root: tree(t, map[string]string{"package.json": pkg("x")})})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Scan.Truncated || res.Scan.TruncatedReason != "cancelled" {
		t.Fatalf("scan stats: %+v", res.Scan)
	}
}

func TestLargeFilesAreSkipped(t *testing.T) {
	big := `{"name":"big","scripts":{"dev":"vite"},"description":"` + strings.Repeat("x", 500) + `"}`
	res := scan(t, tree(t, map[string]string{"package.json": big}), func(o *Options) { o.MaxFileSize = 100 })
	if len(res.Candidates) != 0 || !strings.Contains(strings.Join(res.Warnings, "\n"), "larger than 100 bytes") {
		t.Fatalf("candidates %s, warnings %v", names(res), res.Warnings)
	}
}

func TestEnvValuesAreNeverRead(t *testing.T) {
	root := tree(t, map[string]string{
		"package.json": pkg("app"),
		".env":         "STRIPE_SECRET=sk_live_realsecret\n",
		".env.example": "STRIPE_SECRET=sk_test_examplevalue\nREDIS_URL=redis://localhost\n",
	})
	res := scan(t, root)
	b, _ := json.Marshal(res)
	for _, leaked := range []string{"sk_live_realsecret", "sk_test_examplevalue", "redis://localhost"} {
		if strings.Contains(string(b), leaked) {
			t.Fatalf("output contains %q: %s", leaked, b)
		}
	}
	c := find(t, res, "app")
	if strings.Join(c.EnvKeys, ",") != "REDIS_URL,STRIPE_SECRET" {
		t.Fatalf("env keys = %v", c.EnvKeys)
	}
}

func TestLibraryPackageIsNotAService(t *testing.T) {
	res := scan(t, tree(t, map[string]string{"package.json": `{"name":"lib","dependencies":{"lodash":"4"}}`}))
	if len(res.Candidates) != 0 || strings.Join(res.Technologies, ",") != "node" {
		t.Fatalf("candidates %s, tech %v", names(res), res.Technologies)
	}
}

func TestMalformedManifestWarns(t *testing.T) {
	res := scan(t, tree(t, map[string]string{"package.json": `{"name": `}))
	if len(res.Candidates) != 0 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "could not parse package.json") {
		t.Fatalf("candidates %s, warnings %v", names(res), res.Warnings)
	}
}

func TestNodePackageManagers(t *testing.T) {
	tests := []struct {
		files map[string]string
		want  string
	}{
		{map[string]string{"package.json": `{"scripts":{"start":"node s.js"}}`}, "npm start"},
		{map[string]string{"package.json": `{"scripts":{"dev":"x"}}`, "yarn.lock": ""}, "yarn dev"},
		{map[string]string{"package.json": `{"scripts":{"dev":"x"}}`, "bun.lockb": ""}, "bun run dev"},
		{map[string]string{"package.json": `{"packageManager":"pnpm@9.1.0","scripts":{"serve":"x"}}`}, "pnpm serve"},
		{map[string]string{"pnpm-lock.yaml": "", "apps/web/package.json": `{"name":"web","scripts":{"dev":"x"}}`}, "pnpm dev"},
	}
	for _, tt := range tests {
		res := scan(t, tree(t, tt.files))
		if len(res.Candidates) != 1 || res.Candidates[0].Command.Value != tt.want {
			t.Errorf("%v: got %+v, want %q", tt.files, res.Candidates, tt.want)
		}
	}
}

func TestScriptPortFlag(t *testing.T) {
	res := scan(t, tree(t, map[string]string{"package.json": `{"name":"x","scripts":{"dev":"next dev -p 4000"},"dependencies":{"next":"14"}}`}))
	c := find(t, res, "x")
	if c.Port == nil || c.Port.Value != 4000 || c.Port.Confidence != api.ConfidenceHigh {
		t.Fatalf("port = %+v", c.Port)
	}
}

func TestGoMultipleCommands(t *testing.T) {
	res := scan(t, tree(t, map[string]string{
		"go.mod":             "module example.com/tool/v2\n",
		"cmd/server/main.go": "package main\n\nfunc main() { http.ListenAndServe(\"127.0.0.1:9000\", nil) }\n",
		"cmd/worker/main.go": "package main\n\nfunc main() {}\n",
		"cmd/notmain/x.go":   "package notmain\n",
		"internal/lib.go":    "package internal\n",
	}))
	if got := names(res); got != "server@.,worker@." {
		t.Fatalf("got %s", got)
	}
	s := find(t, res, "server")
	if s.Command.Value != "go run ./cmd/server" || s.Port == nil || s.Port.Value != 9000 {
		t.Fatalf("server: %+v %+v", s.Command, s.Port)
	}
	if s.Health == nil || s.Health.Type != "tcp" {
		// No net/http import string in the file → not known to speak HTTP.
		t.Fatalf("health: %+v", s.Health)
	}
}

func TestGoLibraryIsNotAService(t *testing.T) {
	res := scan(t, tree(t, map[string]string{"go.mod": "module example.com/lib\n", "lib.go": "package lib\n"}))
	if len(res.Candidates) != 0 || strings.Join(res.Technologies, ",") != "go" {
		t.Fatalf("got %s %v", names(res), res.Technologies)
	}
}

func TestOtherStacks(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		cmd     string
		port    int
		fw      string
		depends string
	}{
		{
			name: "rust axum",
			files: map[string]string{
				"Cargo.toml":  "[package]\nname = \"edge_proxy\"\nversion = \"0.1.0\"\n\n[dependencies]\naxum = \"0.7\"\nredis = \"0.25\"\n",
				"src/main.rs": "fn main() { let addr = \"0.0.0.0:7000\"; }\n",
			},
			cmd: "cargo run", port: 7000, fw: "axum", depends: "redis",
		},
		{
			name: "spring maven",
			files: map[string]string{
				"pom.xml": "<project><parent><artifactId>spring-boot-starter-parent</artifactId></parent>" +
					"<artifactId>billing</artifactId><dependencies><dependency><groupId>org.postgresql</groupId></dependency></dependencies>" +
					"<build><plugins><plugin><artifactId>spring-boot-maven-plugin</artifactId></plugin></plugins></build></project>",
				"mvnw": "",
				"src/main/resources/application.properties": "server.port=9090\n",
			},
			cmd: "./mvnw spring-boot:run", port: 9090, fw: "spring-boot", depends: "postgres",
		},
		{
			name: "spring gradle yaml",
			files: map[string]string{
				"build.gradle.kts":                    "plugins { id(\"org.springframework.boot\") version \"3.2.0\" }\n",
				"settings.gradle.kts":                 "rootProject.name = \"inventory\"\n",
				"src/main/resources/application.yml":  "spring:\n  application:\n    name: inv\nserver:\n  port: 8181\n",
				"src/main/resources/application-x.md": "",
			},
			cmd: "gradle bootRun", port: 8181, fw: "spring-boot",
		},
		{
			name:  "laravel",
			files: map[string]string{"composer.json": `{"require":{"laravel/framework":"^11.0","predis/predis":"^2"}}`, "artisan": ""},
			cmd:   "php artisan serve", port: 8000, fw: "laravel", depends: "redis",
		},
		{
			name:  "rails",
			files: map[string]string{"Gemfile": "source 'https://rubygems.org'\ngem \"rails\", \"~> 7.1\"\ngem 'pg'\n", "bin/rails": ""},
			cmd:   "bin/rails server", port: 3000, fw: "rails", depends: "postgres",
		},
		{
			name:  "flask",
			files: map[string]string{"requirements.txt": "Flask==3.0\n", "app.py": "from flask import Flask\napp = Flask(__name__)\napp.run(port=5050)\n"},
			cmd:   "flask --app app run", port: 5050, fw: "flask",
		},
		{
			name:  "fastapi with uv",
			files: map[string]string{"pyproject.toml": "[project]\nname = \"orders\"\ndependencies = [\"fastapi\", \"asyncpg>=0.29\"]\n", "uv.lock": "", "app/main.py": "from fastapi import FastAPI\nserver = FastAPI()\n"},
			cmd:   "uv run uvicorn app.main:server --reload", port: 8000, fw: "fastapi", depends: "postgres",
		},
		{
			name:  "dockerfile only",
			files: map[string]string{"Dockerfile": "FROM nginx\nEXPOSE 80\n"},
			cmd:   "docker build -t ", port: 80,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := scan(t, tree(t, tt.files))
			if len(res.Candidates) != 1 {
				t.Fatalf("candidates: %s (warnings %v)", names(res), res.Warnings)
			}
			c := res.Candidates[0]
			if c.Command == nil || !strings.HasPrefix(c.Command.Value, tt.cmd) {
				t.Errorf("command = %+v, want %q", c.Command, tt.cmd)
			}
			if c.Port == nil || c.Port.Value != tt.port {
				t.Errorf("port = %+v, want %d", c.Port, tt.port)
			}
			if c.Framework != tt.fw {
				t.Errorf("framework = %q, want %q", c.Framework, tt.fw)
			}
			var deps []string
			for _, d := range c.DependsOn {
				deps = append(deps, d.Name)
			}
			if strings.Join(deps, ",") != tt.depends {
				t.Errorf("depends = %v, want %q", deps, tt.depends)
			}
		})
	}
}

func TestComposeHostPort(t *testing.T) {
	tests := map[string]int{
		`"8080:80"`:                       8080,
		`"127.0.0.1:5433:5432"`:           5433,
		`"3000"`:                          0,
		`"9000-9001:9000-9001"`:           9000,
		`"53:53/udp"`:                     53,
		`{target: 80, published: "8081"}`: 8081,
		`{target: 80}`:                    0,
	}
	for in, want := range tests {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(in), &n); err != nil {
			t.Fatal(err)
		}
		if got := composeHostPort(n.Content[0]); got != want {
			t.Errorf("composeHostPort(%s) = %d, want %d", in, got, want)
		}
	}
}

func TestComposeBuildOutsideRootIgnored(t *testing.T) {
	res := scan(t, tree(t, map[string]string{
		"deploy/compose.yml": "services:\n  api:\n    build: ../../elsewhere\n    ports: ['8080:8080']\n",
	}))
	c := find(t, res, "api")
	if c.Dir != "./deploy" || c.Command.Value != "docker compose up api" {
		t.Fatalf("dir %q command %q", c.Dir, c.Command.Value)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "outside the project root") {
		t.Fatalf("warnings: %v", res.Warnings)
	}
}

func TestInferredDependencyAmbiguityStaysUnresolved(t *testing.T) {
	res := scan(t, tree(t, map[string]string{
		"compose.yaml": "services:\n  db1:\n    image: postgres:16\n  db2:\n    image: postgres:15\n",
		"svc/go.mod":   "module x/svc\n\nrequire github.com/lib/pq v1.10.9\n",
		"svc/main.go":  "package main\n",
	}))
	c := find(t, res, "svc")
	if len(c.DependsOn) != 1 || c.DependsOn[0].Name != "postgres" || c.DependsOn[0].Resolved {
		t.Fatalf("two postgres services must not be guessed between: %+v", c.DependsOn)
	}
}

func TestDuplicateNamesMadeUnique(t *testing.T) {
	res := scan(t, tree(t, map[string]string{
		"a/package.json": pkg("app"),
		"b/package.json": pkg("app"),
	}))
	if got := names(res); got != "app@./a,app-b@./b" {
		t.Fatalf("got %s", got)
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		"@acme/Web UI":          "web-ui",
		"github.com/x/y":        "y",
		"__weird__":             "weird",
		"":                      "",
		strings.Repeat("a", 80): strings.Repeat("a", 63),
	} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNeverExecutes plants scripts that would leave a marker file if any of
// them ran, then checks that discovery left no trace.
func TestNeverExecutes(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "PWNED")
	cmd := "touch " + marker
	files := map[string]string{
		"package.json":     `{"name":"x","scripts":{"dev":"` + cmd + `","preinstall":"` + cmd + `","postinstall":"` + cmd + `"}}`,
		"Makefile":         "all:\n\t" + cmd + "\n",
		"manage.py":        "import os\nos.system('" + cmd + "')\n",
		"requirements.txt": "django\n",
		"gradlew":          "#!/bin/sh\n" + cmd + "\n",
		"build.gradle":     "plugins { id 'org.springframework.boot' }\n",
	}
	for p, c := range files {
		full := filepath.Join(root, p)
		if err := os.WriteFile(full, []byte(c), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	res := scan(t, root)
	if len(res.Candidates) == 0 {
		t.Fatal("expected candidates from the planted manifests")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("discovery executed project code")
	}
}
