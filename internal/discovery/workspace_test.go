package discovery

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Aareevs/Kestrel-CLI/pkg/api"
)

func TestGoWorkUses(t *testing.T) {
	tests := map[string]struct {
		in   string
		want []string
	}{
		"block":        {"go 1.22\n\nuse (\n\t./api\n\t./worker // the worker\n\t\"./quoted dir\"\n)\n", []string{"./api", "./worker", "./quoted dir"}},
		"single line":  {"go 1.22\nuse ./svc\n", []string{"./svc"}},
		"mixed":        {"use ./a\nuse (\n  ./b\n)\nuse ./c\n", []string{"./b", "./a", "./c"}},
		"none":         {"go 1.22\nreplace x => ../x\n", nil},
		"comment only": {"use (\n\t// ./nope\n)\n", nil},
	}
	for name, tt := range tests {
		got := goWorkUses([]byte(tt.in))
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got %q, want %q", name, got, tt.want)
		}
	}
}

func TestCargoWorkspace(t *testing.T) {
	members, excludes := cargoWorkspace([]byte(`[package]
name = "root"
members = ["not-a-workspace"]

[workspace]
resolver = "2"
members = [
    "crates/*",   # the libraries
    'services/api',
    # "commented/out",
]
exclude = ["crates/legacy"]

[workspace.dependencies]
serde = "1"
`))
	if !reflect.DeepEqual(members, []string{"crates/*", "services/api"}) || !reflect.DeepEqual(excludes, []string{"crates/legacy"}) {
		t.Fatalf("members %q excludes %q", members, excludes)
	}
	if m, _ := cargoWorkspace([]byte("[package]\nname = \"x\"\n\n[workspace.dependencies]\nx = \"1\"\n")); m != nil {
		t.Fatalf("[workspace.dependencies] is not a workspace: %q", m)
	}
}

func TestPackageWorkspaces(t *testing.T) {
	for in, want := range map[string][]string{
		`{"workspaces":["apps/*","packages/*"]}`:                    {"apps/*", "packages/*"},
		`{"workspaces":{"packages":["apps/*"],"nohoist":["**/x"]}}`: {"apps/*"},
		`{"name":"no-workspaces"}`:                                  nil,
		`{"workspaces":"apps/*"}`:                                   nil, // not a valid shape
		`not json`:                                                  nil,
	} {
		if got := packageWorkspaces([]byte(in)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
}

func wsServices(t *testing.T, res *api.InspectResult) string {
	t.Helper()
	var out []string
	for _, c := range res.Candidates {
		out = append(out, c.Name+"@"+c.Dir)
	}
	return strings.Join(out, " ")
}

const turboRoot = `{"name":"acme","private":true,"scripts":{"dev":"turbo run dev"},"devDependencies":{"turbo":"2"}}`

func TestPnpmWorkspaceWithDeepMemberAndOrchestratorRoot(t *testing.T) {
	root := tree(t, map[string]string{
		"package.json":                            turboRoot,
		"pnpm-workspace.yaml":                     "packages:\n  - 'apps/*'\n  - 'packages/*'\n  - 'services/billing/team/deep'\n  - '!packages/legacy'\n",
		"pnpm-lock.yaml":                          "",
		"apps/web/package.json":                   `{"name":"@acme/web","scripts":{"dev":"next dev"},"dependencies":{"next":"14"}}`,
		"apps/api/package.json":                   `{"name":"@acme/api","scripts":{"dev":"tsx watch src/index.ts --port 4000"},"dependencies":{"express":"4"}}`,
		"packages/ui/package.json":                `{"name":"@acme/ui","main":"dist/index.js"}`,
		"packages/legacy/package.json":            `{"name":"@acme/legacy","scripts":{"start":"node x.js"}}`,
		"services/billing/team/deep/package.json": `{"name":"@acme/billing","scripts":{"start":"node server.js"},"dependencies":{"express":"4"}}`,
	})
	res := scan(t, root)
	// The depth limit is 3, but the declared member 4 levels down is read, and
	// the orchestrating root is not a service. The excluded package is still a
	// project the walk finds, but it isn't a member of the workspace.
	if got := wsServices(t, res); got != "api@./apps/api web@./apps/web legacy@./packages/legacy billing@./services/billing/team/deep" {
		t.Fatalf("candidates: %s", got)
	}
	if len(res.Workspaces) != 1 {
		t.Fatalf("workspaces: %+v", res.Workspaces)
	}
	w := res.Workspaces[0]
	if w.Kind != "pnpm" || w.Root != "." || w.File != "pnpm-workspace.yaml" || w.Orchestrator != "pnpm dev: turbo run dev" ||
		!reflect.DeepEqual(w.Members, []string{"apps/api", "apps/web", "packages/ui", "services/billing/team/deep"}) ||
		!reflect.DeepEqual(w.Services, []string{"apps/api", "apps/web", "services/billing/team/deep"}) {
		t.Fatalf("workspace: %+v", w)
	}
	// Members inherit the package manager from the workspace root's lockfile.
	if c := find(t, res, "billing"); c.PackageManager != "pnpm" || c.Command.Value != "pnpm start" {
		t.Fatalf("billing: %+v", c)
	}
}

func TestRootThatIsARealAppIsKept(t *testing.T) {
	root := tree(t, map[string]string{
		"package.json":          `{"name":"site","scripts":{"dev":"vite"},"devDependencies":{"vite":"5"},"workspaces":["apps/*"]}`,
		"apps/api/package.json": `{"name":"api","scripts":{"dev":"node server.js"},"dependencies":{"express":"4"}}`,
	})
	res := scan(t, root)
	if got := wsServices(t, res); got != "site@. api@./apps/api" {
		t.Fatalf("candidates: %s", got)
	}
	if w := res.Workspaces[0]; w.Orchestrator != "" || w.Kind != "npm" {
		t.Fatalf("workspace: %+v", w)
	}
}

func TestOrchestratorRootIsKeptWhenNoMemberIsAService(t *testing.T) {
	// Nothing to replace it with: dropping it would leave nothing runnable.
	root := tree(t, map[string]string{
		"package.json":             `{"name":"libs","scripts":{"dev":"turbo run dev"},"workspaces":["packages/*"]}`,
		"packages/ui/package.json": `{"name":"ui","main":"x.js"}`,
	})
	res := scan(t, root)
	if got := wsServices(t, res); got != "libs@." || res.Workspaces[0].Orchestrator != "" {
		t.Fatalf("candidates %s, workspace %+v", got, res.Workspaces[0])
	}
}

func TestOrchestratorScripts(t *testing.T) {
	for script, want := range map[string]bool{
		"turbo run dev":                        true,
		"nx run-many -t serve":                 true,
		"lerna run dev --parallel":             true,
		"pnpm -r run dev":                      true,
		"pnpm --filter ./apps/* dev":           true,
		"yarn workspaces foreach -p run dev":   true,
		"npm run dev --workspaces":             true,
		"concurrently \"npm:api\" \"npm:web\"": true,
		"run-p dev:*":                          true,
		"vite":                                 false,
		"next dev":                             false,
		"node server.js":                       false,
		"tsx watch src/index.ts --port 3000":   false,
	} {
		if got := orchestratorRe.MatchString(script); got != want {
			t.Errorf("%q: orchestrator = %v, want %v", script, got, want)
		}
	}
}

func TestWorkspaceKindsFollowTheLockfile(t *testing.T) {
	for lock, kind := range map[string]string{"yarn.lock": "yarn", "bun.lock": "bun", "package-lock.json": "npm", "": "npm"} {
		files := map[string]string{
			"package.json":        `{"name":"r","workspaces":["apps/*"]}`,
			"apps/a/package.json": `{"name":"a","scripts":{"dev":"node a.js"}}`,
		}
		if lock != "" {
			files[lock] = ""
		}
		res := scan(t, tree(t, files))
		if len(res.Workspaces) != 1 || res.Workspaces[0].Kind != kind {
			t.Errorf("lockfile %q: workspaces %+v, want kind %s", lock, res.Workspaces, kind)
		}
	}
	// A pnpm workspace file wins over a `workspaces` field in the same directory.
	res := scan(t, tree(t, map[string]string{
		"package.json": `{"name":"r","workspaces":["a/*"]}`, "pnpm-workspace.yaml": "packages: ['apps/*']\n",
		"apps/x/package.json": `{"name":"x","scripts":{"dev":"node x.js"}}`,
	}))
	if len(res.Workspaces) != 1 || res.Workspaces[0].Kind != "pnpm" {
		t.Fatalf("workspaces: %+v", res.Workspaces)
	}
}

func TestGoAndCargoWorkspacesReachDeepMembers(t *testing.T) {
	root := tree(t, map[string]string{
		// Members 4 levels down, with the main package two levels further.
		"go.work":                               "go 1.22\nuse (\n\t./services/payments/internal/api\n\t./missing/one\n)\n",
		"services/payments/internal/api/go.mod": "module example.com/payments\n\ngo 1.22\n",
		"services/payments/internal/api/cmd/server/main.go": "package main\n\nfunc main() {}\n",
		"Cargo.toml":                    "[workspace]\nmembers = [\"crates/*/*\"]\nexclude = [\"crates/tools/skip\"]\n",
		"crates/tools/edge/Cargo.toml":  "[package]\nname = \"edge\"\n\n[dependencies]\naxum = \"0.7\"\n",
		"crates/tools/edge/src/main.rs": "fn main() { let a = \"0.0.0.0:7777\"; }\n",
		"crates/tools/skip/Cargo.toml":  "[package]\nname = \"skip\"\n",
		"crates/tools/skip/src/main.rs": "fn main() {}\n",
	})
	res := scan(t, root, func(o *Options) { o.MaxDepth = 2 })
	if got := wsServices(t, res); got != "edge@./crates/tools/edge server@./services/payments/internal/api" {
		t.Fatalf("candidates: %s (warnings %v)", got, res.Warnings)
	}
	if c := find(t, res, "edge"); c.Port == nil || c.Port.Value != 7777 || c.Framework != "axum" {
		t.Fatalf("edge: %+v", c)
	}
	var warned bool
	for _, w := range res.Warnings {
		warned = warned || strings.Contains(w, `go.work: workspace member "./missing/one" does not exist`)
	}
	if !warned {
		t.Fatalf("a declared member that doesn't exist should be reported: %v", res.Warnings)
	}
	if len(res.Workspaces) != 2 || res.Workspaces[0].Kind != "go" || res.Workspaces[1].Kind != "cargo" {
		t.Fatalf("workspaces: %+v", res.Workspaces)
	}
	for _, m := range res.Workspaces[1].Members {
		if strings.Contains(m, "skip") {
			t.Fatalf("an excluded member was listed: %v", res.Workspaces[1].Members)
		}
	}
}

func TestNoWorkspaceMeansNothingChanges(t *testing.T) {
	res := scan(t, tree(t, map[string]string{"package.json": pkg("solo")}))
	if len(res.Workspaces) != 0 || len(res.Candidates) != 1 {
		t.Fatalf("%+v", res)
	}
}

// Member patterns never leave the project or enter places the scanner won't.
func TestWorkspaceMemberSafety(t *testing.T) {
	outside := t.TempDir()
	os.MkdirAll(filepath.Join(outside, "evil"), 0o755)
	os.WriteFile(filepath.Join(outside, "evil", "package.json"), []byte(`{"name":"evil","scripts":{"dev":"node x.js"}}`), 0o644)
	root := tree(t, map[string]string{
		"package.json":                           `{"name":"r","workspaces":["apps/*","../*","/etc/*","packages/**","vendored/*"]}`,
		"apps/ok/package.json":                   `{"name":"ok","scripts":{"dev":"node ok.js"}}`,
		"packages/node_modules/dep/package.json": `{"name":"dep","scripts":{"dev":"node dep.js"}}`,
		"packages/ignored/package.json":          `{"name":"ignored","scripts":{"dev":"node i.js"}}`,
		".gitignore":                             "packages/ignored/\n",
	})
	if err := os.Symlink(filepath.Join(outside, "evil"), filepath.Join(root, "apps", "link")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	res := scan(t, root)
	if got := wsServices(t, res); got != "ok@./apps/ok" {
		t.Fatalf("candidates: %s", got)
	}
	for _, m := range res.Workspaces[0].Members {
		if strings.Contains(m, "link") || strings.Contains(m, "node_modules") || strings.Contains(m, "ignored") || strings.HasPrefix(m, "..") {
			t.Fatalf("unsafe member listed: %v", res.Workspaces[0].Members)
		}
	}
}

func TestExplicitMemberMayBeNamedLikeABuildDirectory(t *testing.T) {
	root := tree(t, map[string]string{
		"package.json":             `{"name":"r","workspaces":["tools/build","tools/*"]}`,
		"tools/build/package.json": `{"name":"build","scripts":{"dev":"node b.js"}}`,
		"tools/dist/package.json":  `{"name":"dist","scripts":{"dev":"node d.js"}}`,
		"tools/other/package.json": `{"name":"other","scripts":{"dev":"node o.js"}}`,
	})
	res := scan(t, root)
	if got := res.Workspaces[0].Members; !reflect.DeepEqual(got, []string{"tools/build", "tools/other"}) {
		t.Fatalf("members = %v: an explicit `tools/build` is a member, a glob never matches `dist`", got)
	}
}

func TestWorkspacePatternBudget(t *testing.T) {
	files := map[string]string{"package.json": `{"name":"r","workspaces":["**"]}`}
	for i := 0; i < 30; i++ {
		files["d"+string(rune('a'+i%26))+string(rune('a'+i/26))+"/x/package.json"] = `{"name":"x"}`
	}
	s := &scanner{root: tree(t, files), files: map[string][]string{}, dirSeen: map[string]bool{}, ignores: map[string]*gitignore{}}
	budget := 20
	got := s.expandPattern(".", "**", &budget)
	if budget > 0 || len(got) > 25 {
		t.Fatalf("budget %d, %d matches: the expansion must stop at its budget", budget, len(got))
	}
}

func TestExpandPatternGlobs(t *testing.T) {
	root := tree(t, map[string]string{
		"apps/a/x": "", "apps/b/x": "", "apps/b/sub/x": "", "libs/c/x": "", "libs/c/deep/x": "", "apps/file": "",
	})
	s := &scanner{root: root, files: map[string][]string{}, dirSeen: map[string]bool{}, ignores: map[string]*gitignore{}}
	for pat, want := range map[string][]string{
		"apps/*":        {"apps/a", "apps/b"},
		"apps/?":        {"apps/a", "apps/b"},
		"apps/[a]":      {"apps/a"},
		"*/c":           {"libs/c"},
		"libs/**":       {"libs", "libs/c", "libs/c/deep"},
		"apps/b/**/sub": {"apps/b/sub"},
		"libs/c":        {"libs/c"},
		"./libs/c/":     {"libs/c"},
		"nope/*":        nil,
		"apps/file":     nil, // a file is not a member
		".":             nil,
		"..":            nil,
	} {
		budget := 1000
		got := s.expandPattern(".", pat, &budget)
		if !sameSet(got, want) {
			t.Errorf("%q: got %v, want %v", pat, got, want)
		}
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}
