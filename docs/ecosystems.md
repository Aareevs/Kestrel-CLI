# Ecosystem support

Kestrel's support for a stack has three separate parts. They are independent: a stack can have good detection and no
dedicated failure patterns, or the reverse.

1. **Detection** (`kestrel inspect`, `kestrel init`): reads manifests and config files to propose services, commands,
   ports, health endpoints and dependencies. It only reads files and never runs anything.
2. **Failure recognition**: patterns that classify a process's output while `run` or `watch` records it (a port
   already in use, a database refusing connections, a missing module, a build failing). They are listed in
   [events.md](events.md#pattern-library), and work whatever Kestrel detected, because they read output.
3. **Diagnosis advice**: what `kestrel diagnose` suggests, by runtime, for the failures above.

Anything is supervised by `kestrel run -- <command>` or a hand-written `kestrel.yaml`, with or without detection.

## Matrix

| Stack | Detection reads | Failure recognition (pattern IDs) | Next-step advice |
|---|---|---|---|
| **Node.js** (Vite, Next.js, Nuxt, Astro, SvelteKit, Remix, Angular, CRA, NestJS, Express, Fastify, Koa, Hono, Expo, React Native) | `package.json` scripts and dependencies, lockfiles, framework config files, dev-server proxies | `P010` uncaught exception / unhandled rejection, `P042` Node's error-location line, `P013` module not found, `P014` Jest, shared `P001`-`P008`, `P012` | missing module: install dependencies; port in use: `lsof` |
| **Bun** (Elysia, `Bun.serve`) | `bun.lock(b)`, `bunfig.toml`, entry files | as Node | as Node |
| **Deno** (Fresh, Oak, Hono, `Deno.serve`) | `deno.json(c)` tasks, `Deno.serve` entry files | shared patterns only | generic |
| **Go** (gin, echo, fiber, chi, `net/http`) | `go.mod`, `main.go`, health routes in code | `P009` panic / fatal error, `P013` missing package, `P014` `go test`, `P006` `dial tcp`, `P007`, `P008` | missing package: `go mod download` |
| **Python** (Django, FastAPI, Flask) | `manage.py`, `pyproject.toml`, `requirements.txt`, lockfiles | `P011` traceback, `P013` `ModuleNotFoundError`, `P014` pytest, `P003`-`P005` database drivers, `P012` `MemoryError` | missing module: install requirements |
| **Java / Kotlin** (Spring Boot; Maven, Gradle) | `pom.xml`, `build.gradle(.kts)`, `application.properties` / `.yml` | `P015` main thread died, `P016` Spring port in use, `P017` startup failed, `P018` JDBC, `P019` class version, `P020` Maven/Gradle build, `P013` missing class | build first (`mvnw package` / `gradlew build`), check classpath |
| **Rust** (axum, actix-web, rocket, warp, poem) | `Cargo.toml`, `src/main.rs` | `P021` main panic, `P022` thread panic, `P023` cargo build | shared |
| **.NET** (ASP.NET Core, workers, console) | `*.csproj` / `*.fsproj`, `launchSettings.json`, `appsettings.json` | `P024` unhandled exception, `P025` Kestrel bind failed, `P026` Npgsql / SqlClient, `P027` MSBuild | `dotnet restore` / `dotnet build`, check the runtime |
| **Ruby** (Rails, Sinatra) | `Gemfile` | `P028` uncaught, `P029` Rails database, `P030` stale server PID, `P031` missing secret key | `bundle install`, check the require path |
| **PHP** (Laravel) | `composer.json`, `artisan` | `P032` fatal / parse error, `P033` PDO, `P031` Laravel key | `composer install` and `dump-autoload` |
| **Elixir** (Phoenix, Plug with Bandit or Cowboy) | `mix.exs`, `config/dev.exs`, `config/runtime.exs` | shared patterns only | generic |
| **Docker / Compose** | compose files, `Dockerfile` `EXPOSE`, published ports, `depends_on`, build contexts | `P034` daemon unavailable, `P035` port allocated, `P036` image pull failed, `P037` build failed, `P038` compose file invalid, `P039` name conflict, `P040` dependency failed, `P041` container exited | `docker ps --filter "publish=N"` for a taken port |
| **Monorepos** | `pnpm-workspace.yaml`, `package.json` `workspaces` (npm, yarn, Bun), `go.work`, Cargo `[workspace]` | n/a | n/a |

"Shared patterns" are the language-neutral ones: `P001` port in use, `P002` bind permission, `P003`-`P005`
database connections, `P006` connection refused, `P007` host not found, `P008` timeouts, `P012` out of memory, and
`L001` "listening on" (which tells Kestrel which service owns which port). They fire for any runtime whose message
matches. "Generic" advice means: read the service's startup output (`kestrel timeline --service <name>`).

## What this does not mean

- **Detection is a proposal.** Each finding carries its source `file:line` and a confidence; `kestrel init` writes
  a config you review. A framework Kestrel doesn't know is still found as a plain candidate if it has a runnable
  entry point, and is otherwise missed.
- **Pattern coverage is the shapes we have seen,** recorded with example lines. A failure that matches no pattern
  is still recorded and shown; since 1.1, when a process dies with output no pattern matched, the diagnosis quotes
  its last lines instead of saying nothing. Add your own shapes in `kestrel.yaml`: see
  [extending.md](extending.md).
- **Windows** is built and vetted but has not been run on a Windows machine by the maintainers; several patterns
  include Windows wording (for example `P001`, `P034`).

## Adding an ecosystem

See [extending.md](extending.md): an analyzer for detection, patterns for output, a fixture with reviewed goldens.
