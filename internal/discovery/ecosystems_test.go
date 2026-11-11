package discovery

import (
	"strings"
	"testing"
)

type wantCand struct {
	name, dir string // dir "" = don't check
	cmd       string // prefix of the command
	port      int
	fw        string
	pm        string
	depends   string
}

func checkCand(t *testing.T, got []candidateView, want wantCand) {
	t.Helper()
	for _, c := range got {
		if c.name != want.name {
			continue
		}
		if want.dir != "" && c.dir != want.dir {
			t.Errorf("%s: dir = %q, want %q", c.name, c.dir, want.dir)
		}
		if !strings.HasPrefix(c.cmd, want.cmd) {
			t.Errorf("%s: command = %q, want prefix %q", c.name, c.cmd, want.cmd)
		}
		if c.port != want.port {
			t.Errorf("%s: port = %d, want %d", c.name, c.port, want.port)
		}
		if c.fw != want.fw || c.pm != want.pm {
			t.Errorf("%s: framework/package manager = %q/%q, want %q/%q", c.name, c.fw, c.pm, want.fw, want.pm)
		}
		if c.depends != want.depends {
			t.Errorf("%s: depends = %q, want %q", c.name, c.depends, want.depends)
		}
		return
	}
	t.Fatalf("no candidate %q in %v", want.name, got)
}

type candidateView struct {
	name, dir, cmd, fw, pm, depends string
	port                            int
}

func view(t *testing.T, files map[string]string) ([]candidateView, []string) {
	t.Helper()
	res := scan(t, tree(t, files))
	var out []candidateView
	for _, c := range res.Candidates {
		v := candidateView{name: c.Name, dir: c.Dir, fw: c.Framework, pm: c.PackageManager}
		if c.Command != nil {
			v.cmd = c.Command.Value
		}
		if c.Port != nil {
			v.port = c.Port.Value
		}
		var deps []string
		for _, d := range c.DependsOn {
			deps = append(deps, d.Name)
		}
		v.depends = strings.Join(deps, ",")
		out = append(out, v)
	}
	return out, res.Technologies
}

func TestDenoProjects(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  wantCand
	}{
		{"task with a port flag and Fresh",
			map[string]string{"deno.json": `{"name":"@acme/web","tasks":{"dev":"deno run --allow-net --port 9001 main.ts"},"imports":{"fresh":"jsr:@fresh/core@^2"}}`},
			wantCand{name: "web", cmd: "deno task dev", port: 9001, fw: "fresh", pm: "deno"}},
		{"deno.jsonc with comments and a trailing comma, entry file with a port",
			map[string]string{
				"deno.jsonc": "{\n  // the project\n  \"name\": \"api\", /* name */\n  \"imports\": {\"postgres\": \"https://deno.land/x/postgres@v0.19.0/mod.ts\",},\n}\n",
				"main.ts":    "Deno.serve({ port: 8080 }, () => new Response('hi'));\n",
			},
			wantCand{name: "api", cmd: "deno run --allow-net main.ts", port: 8080, pm: "deno", depends: "postgres"}},
		{"Deno.serve with the default port",
			map[string]string{"deno.json": `{"name":"edge"}`, "server.ts": "Deno.serve((_req) => new Response('ok'));\n"},
			wantCand{name: "edge", cmd: "deno run --allow-net server.ts", port: 8000, pm: "deno"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, techs := view(t, tt.files)
			if len(got) != 1 {
				t.Fatalf("candidates: %v", got)
			}
			checkCand(t, got, tt.want)
			if !contains(techs, "deno") {
				t.Errorf("technologies = %v", techs)
			}
		})
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestDenoThatIsNotAService(t *testing.T) {
	// A library: no task, no Deno.serve.
	got, techs := view(t, map[string]string{"deno.json": `{"name":"@acme/lib","exports":"./mod.ts"}`, "mod.ts": "export const x = 1;\n"})
	if len(got) != 0 || !contains(techs, "deno") {
		t.Fatalf("library: %v %v", got, techs)
	}
	// A directory with a package.json is the Node analyzer's: no duplicate.
	got, _ = view(t, map[string]string{"deno.json": `{"tasks":{"dev":"deno run -A main.ts"}}`, "package.json": `{"name":"both","scripts":{"dev":"vite"},"devDependencies":{"vite":"5"}}`})
	if len(got) != 1 || got[0].fw != "vite" {
		t.Fatalf("deno + package.json: %v", got)
	}
	// An unparseable config is skipped with a warning, not a crash.
	res := scan(t, tree(t, map[string]string{"deno.json": "{ not json"}))
	if len(res.Candidates) != 0 || len(res.Warnings) == 0 {
		t.Fatalf("bad config: %v %v", res.Candidates, res.Warnings)
	}
}

func TestStripJSONCKeepsStrings(t *testing.T) {
	in := `{"url":"https://x.dev//a", "note":"/* not a comment */", // real
	/* real */ "list":[1,2,],}`
	got := string(stripJSONC([]byte(in)))
	for _, want := range []string{`"https://x.dev//a"`, `"/* not a comment */"`} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %s in %q", want, got)
		}
	}
	if strings.Contains(got, "real") || strings.Contains(got, ",}") || strings.Contains(got, ",]") {
		t.Errorf("comments or trailing commas remain: %q", got)
	}
}

func TestBunProjects(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  wantCand
	}{
		{"Elysia with no start script",
			map[string]string{"package.json": `{"name":"api","module":"index.ts","dependencies":{"elysia":"^1.0"}}`, "bun.lock": "", "index.ts": "import { Elysia } from 'elysia';\nnew Elysia().get('/', () => 'hi').listen(3000);\n"},
			wantCand{name: "api", cmd: "bun run index.ts", port: 3000, fw: "elysia", pm: "bun"}},
		{"Elysia with a different port in listen()",
			map[string]string{"package.json": `{"name":"api","dependencies":{"elysia":"^1.0"}}`, "bun.lock": "", "index.ts": "new Elysia()\n  .get('/', () => 'hi')\n  .listen(4040);\n"},
			wantCand{name: "api", cmd: "bun run index.ts", port: 4040, fw: "elysia", pm: "bun"}},
		{"Bun.serve with a port, recognized by bunfig.toml alone",
			map[string]string{"package.json": `{"name":"edge"}`, "bunfig.toml": "[install]\n", "index.ts": "Bun.serve({\n  port: 4321,\n  fetch() { return new Response('ok'); },\n});\n"},
			wantCand{name: "edge", cmd: "bun run index.ts", port: 4321, pm: "bun"}},
		{"Bun.serve with the default port",
			map[string]string{"package.json": `{"name":"plain"}`, "bun.lockb": "", "src/index.ts": "Bun.serve({ fetch() { return new Response('ok'); } });\n"},
			wantCand{name: "plain", cmd: "bun run src/index.ts", port: 3000, pm: "bun"}},
		{"a start script still wins",
			map[string]string{"package.json": `{"name":"scripted","scripts":{"dev":"bun --hot index.ts"}}`, "bun.lock": "", "index.ts": "Bun.serve({ port: 1234, fetch() {} });\n"},
			wantCand{name: "scripted", cmd: "bun run dev", pm: "bun"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := view(t, tt.files)
			if len(got) != 1 {
				t.Fatalf("candidates: %v", got)
			}
			checkCand(t, got, tt.want)
		})
	}
	// Without Bun, an index.ts that calls Bun.serve is not guessed at.
	if got, _ := view(t, map[string]string{"package.json": `{"name":"lib"}`, "package-lock.json": "{}", "index.ts": "Bun.serve({})\n"}); len(got) != 0 {
		t.Fatalf("npm project: %v", got)
	}
}

const webCsproj = `<Project Sdk="Microsoft.NET.Sdk.Web">
  <PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Npgsql" Version="8.0.2" />
    <PackageReference Include="StackExchange.Redis" Version="2.7.33" />
    <PackageReference Include="Swashbuckle.AspNetCore" Version="6.5.0" />
  </ItemGroup>
</Project>
`

func TestDotnetProjects(t *testing.T) {
	launch := `{"profiles":{"https":{"applicationUrl":"https://localhost:7114;http://localhost:5114"}}}`
	tests := []struct {
		name  string
		files map[string]string
		want  wantCand
	}{
		{"web API with a launch profile",
			map[string]string{"Orders.csproj": webCsproj, "Properties/launchSettings.json": launch},
			wantCand{name: "orders", cmd: "dotnet run", port: 5114, fw: "aspnet", pm: "nuget", depends: "postgres,redis"}},
		{"Kestrel endpoint in appsettings.json",
			map[string]string{"Api.csproj": webCsproj, "appsettings.json": `{"Kestrel":{"Endpoints":{"Http":{"Url":"http://localhost:6060"}}}}`},
			wantCand{name: "api", cmd: "dotnet run", port: 6060, fw: "aspnet", pm: "nuget", depends: "postgres,redis"}},
		{"ASPNETCORE_URLS in a launch profile",
			map[string]string{"Api.csproj": webCsproj, "Properties/launchSettings.json": `{"profiles":{"dev":{"environmentVariables":{"ASPNETCORE_URLS":"http://+:8088"}}}}`},
			wantCand{name: "api", cmd: "dotnet run", port: 8088, fw: "aspnet", pm: "nuget", depends: "postgres,redis"}},
		{"no settings: the default port, low confidence",
			map[string]string{"Api.csproj": webCsproj},
			wantCand{name: "api", cmd: "dotnet run", port: 5000, fw: "aspnet", pm: "nuget", depends: "postgres,redis"}},
		{"AssemblyName wins over the file name",
			map[string]string{"x.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><AssemblyName>Billing.Api</AssemblyName></PropertyGroup></Project>`},
			wantCand{name: "billing.api", cmd: "dotnet run", port: 5000, fw: "aspnet", pm: "nuget"}},
		{"worker service has no port",
			map[string]string{"Jobs.csproj": `<Project Sdk="Microsoft.NET.Sdk.Worker"><ItemGroup><PackageReference Include="MassTransit.RabbitMQ" Version="8"/></ItemGroup></Project>`},
			wantCand{name: "jobs", cmd: "dotnet run", fw: "worker", pm: "nuget", depends: "rabbitmq"}},
		{"console app",
			map[string]string{"Tool.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><OutputType>Exe</OutputType></PropertyGroup></Project>`},
			wantCand{name: "tool", cmd: "dotnet run", pm: "nuget"}},
		{"F# project",
			map[string]string{"Svc.fsproj": `<Project Sdk="Microsoft.NET.Sdk.Web"></Project>`},
			wantCand{name: "svc", cmd: "dotnet run", port: 5000, fw: "aspnet", pm: "nuget"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, techs := view(t, tt.files)
			if len(got) != 1 {
				t.Fatalf("candidates: %v", got)
			}
			checkCand(t, got, tt.want)
			if !contains(techs, "dotnet") {
				t.Errorf("technologies = %v", techs)
			}
		})
	}
}

func TestDotnetSolutionsAndLibraries(t *testing.T) {
	// A solution: the web project is a service, the class library is not.
	got, techs := view(t, map[string]string{
		"Shop.sln":             "",
		"src/Api/Api.csproj":   webCsproj,
		"src/Core/Core.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`,
		"tests/T/T.csproj":     `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><PackageReference Include="xunit" Version="2"/></ItemGroup></Project>`,
	})
	if len(got) != 1 || got[0].name != "api" || got[0].dir != "./src/Api" || !contains(techs, "dotnet") {
		t.Fatalf("solution: %v %v", got, techs)
	}
	// Two executables in one directory are told apart by --project.
	got, _ = view(t, map[string]string{
		"A.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"></Project>`,
		"B.csproj": `<Project Sdk="Microsoft.NET.Sdk.Worker"></Project>`,
	})
	if len(got) != 2 || got[0].cmd != "dotnet run --project A.csproj" || got[1].cmd != "dotnet run --project B.csproj" {
		t.Fatalf("two projects: %v", got)
	}
}

func TestElixirProjects(t *testing.T) {
	mix := func(deps string) string {
		return "defmodule Shop.MixProject do\n  use Mix.Project\n  def project, do: [app: :shop, version: \"0.1.0\", deps: deps()]\n  defp deps do\n    [\n" + deps + "\n    ]\n  end\nend\n"
	}
	phoenix := "      {:phoenix, \"~> 1.7.10\"},\n      {:postgrex, \">= 0.0.0\"},\n      {:redix, \"~> 1.2\"}"
	tests := []struct {
		name  string
		files map[string]string
		want  wantCand
	}{
		{"Phoenix with a dev port",
			map[string]string{"mix.exs": mix(phoenix), "config/dev.exs": "config :shop, ShopWeb.Endpoint,\n  http: [ip: {127, 0, 0, 1}, port: 4001]\n"},
			wantCand{name: "shop", cmd: "mix phx.server", port: 4001, fw: "phoenix", pm: "mix", depends: "postgres,redis"}},
		{"Phoenix with PORT from the environment",
			map[string]string{"mix.exs": mix(phoenix), "config/runtime.exs": "port = String.to_integer(System.get_env(\"PORT\") || \"4002\")\n"},
			wantCand{name: "shop", cmd: "mix phx.server", port: 4002, fw: "phoenix", pm: "mix", depends: "postgres,redis"}},
		{"Phoenix with no config: the default port",
			map[string]string{"mix.exs": mix(phoenix)},
			wantCand{name: "shop", cmd: "mix phx.server", port: 4000, fw: "phoenix", pm: "mix", depends: "postgres,redis"}},
		{"Plug with Bandit",
			map[string]string{"mix.exs": mix("      {:bandit, \"~> 1.0\"},\n      {:plug, \"~> 1.15\"}")},
			wantCand{name: "shop", cmd: "mix run --no-halt", port: 4000, fw: "plug", pm: "mix"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, techs := view(t, tt.files)
			if len(got) != 1 {
				t.Fatalf("candidates: %v", got)
			}
			checkCand(t, got, tt.want)
			if !contains(techs, "elixir") {
				t.Errorf("technologies = %v", techs)
			}
		})
	}
	// phoenix_html is a different package: a library, not a Phoenix app.
	got, techs := view(t, map[string]string{"mix.exs": mix("      {:phoenix_html, \"~> 4.0\"},\n      {:jason, \"~> 1.4\"}")})
	if len(got) != 0 || !contains(techs, "elixir") {
		t.Fatalf("library: %v %v", got, techs)
	}
}

// The registry puts the new ecosystems after the built-in ones, in order.
func TestNewAnalyzersAreRegisteredAfterTheBuiltIns(t *testing.T) {
	var got []string
	for _, a := range registeredAnalyzers() {
		got = append(got, a.name)
	}
	if strings.Join(got, ",") != "node,go,python,jvm,php,ruby,rust,deno,dotnet,elixir" {
		t.Fatalf("analyzers = %v", got)
	}
}
