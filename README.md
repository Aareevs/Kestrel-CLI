<div align="center">

<img src="docs/assets/logo.svg" width="96" alt="Kestrel logo">

# Kestrel

**Every failure has a trail.**

[![ci](https://github.com/kestrel-cli/kestrel/actions/workflows/ci.yml/badge.svg)](https://github.com/kestrel-cli/kestrel/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go)
![License](https://img.shields.io/badge/license-TBD-lightgrey)
![Release](https://img.shields.io/badge/release-unreleased-lightgrey)

</div>

Kestrel is a local-first debugging CLI for your dev stack. It watches the services you run, reconstructs what
broke and in what order, ranks root-cause hypotheses backed by the exact events that support them, and checks
whether your fix actually worked. It runs offline, needs no config to start, and never executes anything you
didn't ask it to.

> **Status: early development.** Project discovery (`inspect`, `init`) and config tooling work today.
> Supervision, timelines, diagnosis, and verification are being built in phases; they're marked *planned*
> below. This README only shows what works now.

<!-- Demo: docs/assets/demo.tape renders docs/assets/demo.gif with VHS. Not yet generated. -->

## Install

Kestrel has no tagged release yet. For now, build it from source (Go 1.26+):

```sh
git clone https://github.com/kestrel-cli/kestrel
cd kestrel
make build          # → ./bin/kestrel
```

*Planned:* a one-line install script, a Homebrew tap, and `go install …@latest` once releases are published.

## Quick start

```sh
cd your-project
kestrel inspect     # what's here, how it probably runs, and how sure Kestrel is
kestrel init        # propose a kestrel.yaml from that; asks before writing
```

## Features

| | Status |
|---|---|
| **Zero-config discovery**: detects services, start commands, ports, health checks, and dependencies from the files already in your repo, each with its source file/line and a confidence level ([details](docs/architecture.md#discovery)) | available |
| **Config you can trust**: strict `kestrel.yaml` validation with line numbers, typo suggestions, cycle detection, and a [JSON Schema](docs/kestrel.schema.json) for editor completion ([reference](docs/configuration.md)) | available |
| **Secrets stay masked**: `.env` is never read; `config show` masks literal secrets ([security](docs/security.md)) | available |
| Process supervision with clean shutdown and bounded log capture (`run`, `watch`, `status`) | planned |
| Incident timeline and evidence-backed diagnosis (`timeline`, `diagnose`) | planned |
| Fix verification over a stability window (`verify`) | planned |
| Session storage, retention, redacted export (`sessions`, `cleanup`) | planned |
| Read-only MCP server for AI assistants (`mcp`) | planned |

## Example

`kestrel inspect` on [a compose project](tests/fixtures/projects/compose-stack) with a Postgres database, a
Redis cache, a Go API, and a Next.js frontend. This is real output, taken from the golden test:

```text
Project  ~/src/compose-stack
Stack    docker, docker-compose, go, node, postgres, redis

Service candidates (4)

  cache  redis · .                                       high
    command  docker compose up cache                     compose.yaml:8
    port     6379 (high)                                 compose.yaml:11 · published port
    health   tcp localhost:6379                          TCP connect to the redis port

  db  postgres · .                                       high
    command  docker compose up db                        compose.yaml:2
    port     5432 (high)                                 compose.yaml:5 · published port
    health   tcp localhost:5432                          TCP connect to the postgres port
    env      POSTGRES_PASSWORD

  api  go · ./api                                        high
    command  docker compose -f ../compose.yaml up api    compose.yaml:12
             alt: go run .                               api/main.go:1
    port     8080 (high)                                 compose.yaml:15 · published port
    health   http http://localhost:8080/health           health route "/health" found in api/main.go:10
    needs    db (declared)                               compose.yaml:17 · depends_on
             cache (declared)                            compose.yaml:18 · depends_on
    env      DATABASE_URL, REDIS_URL

  web  node (next) · ./web                               high
    command  docker compose -f ../compose.yaml up web    compose.yaml:22
             alt: npm run dev                            web/package.json:5
    port     3000 (high)                                 compose.yaml:26 · published port
    health   http http://localhost:3000/                 no health endpoint detected; probes the root path
    needs    api (declared)                              compose.yaml:29 · depends_on

Scanned 7 files in 3 directories (depth ≤ 3). Nothing was executed.
Next: kestrel init   # propose a kestrel.yaml from these candidates
```

Here the compose services `api` and `web` are merged with the Go and Node projects they build, so they appear
once each. A dependency Kestrel only *inferred* (say, `pgx` in `go.mod` pointing to Postgres) is labeled
`inferred`. If it can't tell which service is meant, it says so and doesn't guess.

The database → API → frontend failure walkthrough will go here once diagnosis ships.

## Supported stacks

Detection support today. "Full" means command, port, and dependency detection all work. "Partial" means some
of them are conventions rather than read from your files.

| Stack | Detected from | Support |
|---|---|---|
| Node.js: Vite, Next.js, Nuxt, Astro, SvelteKit, Angular, CRA, NestJS, Express… | `package.json` scripts, lockfile → npm/pnpm/yarn/bun, `vite.config.*` etc. | full |
| Go: gin, echo, fiber, chi, net/http | `go.mod`, `main.go`, `cmd/*/main.go`, listen addresses and health routes in code | full |
| Docker Compose | `compose.yaml` / `docker-compose.yml`: services, published ports, `depends_on`, build contexts | full |
| Python: Django, FastAPI, Flask (pip, uv, Poetry, Pipenv) | `manage.py`, `pyproject.toml`, `requirements.txt`, `Pipfile` | partial (ports are framework defaults unless set in code) |
| Java: Spring Boot (Maven, Gradle) | `pom.xml`, `build.gradle(.kts)`, `application.properties/yml` | partial |
| Ruby on Rails, Sinatra | `Gemfile` | partial |
| PHP: Laravel | `composer.json`, `artisan` | partial |
| Rust: axum, actix-web, rocket, warp | `Cargo.toml`, `src/main.rs` | partial |
| Dockerfile only | `EXPOSE` | partial |

## Commands

| Command | What it does | Status |
|---|---|---|
| `kestrel inspect` | Detected technologies and service candidates, with sources and confidence | available |
| `kestrel init` | Propose a `kestrel.yaml`; confirms before writing (`--yes`, `--print`); never overwrites | available |
| `kestrel config show\|validate\|path` | Effective config (secrets masked), validation, location | available |
| `kestrel doctor` | Self-check: platform, data directory, config, tools | available |
| `kestrel version` | Version and build info | available |
| `kestrel run -- <cmd>` | Wrap one command and record a session, no config needed | planned |
| `kestrel watch` / `status` | Supervise services; liveness and readiness | planned |
| `kestrel timeline` / `diagnose` | Incident timeline; ranked hypotheses with evidence | planned |
| `kestrel verify` | Post-fix checks over a stability window | planned |
| `kestrel sessions` / `cleanup` | Session history, export, retention | planned |
| `kestrel mcp` | Read-only MCP server (stdio) | planned |

**Global flags:** `--project <dir>`, `--format text|json` (every read command), `--verbose`, `--quiet`,
`--no-color` (also honors `NO_COLOR`).

**Exit codes:**

| Code | Meaning |
|---|---|
| `0` | OK |
| `1` | Project failure detected / verification failed |
| `2` | Invalid usage or invalid config |
| `3` | Incomplete or inconclusive investigation |
| `4` | Internal error (including a failed `doctor` check) |

## How it works

```text
 collect ──▶ normalize ──▶ correlate ──▶ diagnose ──▶ verify
 (process     (typed events,  (incidents,    (ranked,        (stability
  output,      redaction)      dependency     evidence-       window,
  probes)                      graph)         backed)         before/after)
```

Today only the step before all of these exists: **discovery**. It reads (never runs) your project files to
work out what the services are. See [docs/architecture.md](docs/architecture.md).

## Configuration

Optional. Without a config, Kestrel works from discovery alone. With `kestrel.yaml` (or `.kestrel.yaml`) at the
project root:

```yaml
version: 1
services:
  - name: database
    health: { type: tcp, address: localhost:5432 }
  - name: api
    dir: ./backend
    command: go run .
    env: { DATABASE_URL: "${DATABASE_URL}" }   # resolved from your shell; never stored or shown
    depends_on: [database]
    health: { type: http, url: http://localhost:8080/health }
```

Full reference: [docs/configuration.md](docs/configuration.md). Kestrel finds the project root by looking for
the config in the current directory and its parents; `--project` overrides that.

## Privacy & storage

- Discovery reads files only, stays inside the project root (symlinks that escape it are refused), skips
  `node_modules`, `vendor`, `.git`, build output, and anything in `.gitignore`, and never opens `.env`.
  From `.env.example` it takes variable *names* only.
- Nothing is uploaded. Kestrel makes no network calls.
- Kestrel's own data directory is outside your repo: `~/Library/Application Support/Kestrel` (macOS),
  `$XDG_DATA_HOME/kestrel` or `~/.local/share/kestrel` (Linux), `%LOCALAPPDATA%\Kestrel` (Windows).
  Override with `KESTREL_HOME`. No sessions are stored yet.

More in [docs/security.md](docs/security.md).

## MCP setup

*Planned:* `kestrel mcp` will expose read-only tools (diagnosis, timeline, recent errors) to Claude Code and
other MCP clients.

## Troubleshooting

- **`kestrel inspect` found nothing:** check `--max-depth` (default 3) and whether the directory is gitignored.
  Warnings at the end of the output name any file Kestrel couldn't parse or chose not to read.
- **`init` refuses to write:** Kestrel never overwrites an existing `kestrel.yaml` or `.kestrel.yaml`. When stdin
  isn't a terminal, pass `--yes`, or use `--print` and redirect the output.
- **Something else:** `kestrel doctor` checks the platform, data directory, and config.

## Platform support

macOS (arm64, amd64) and Linux (arm64, amd64) are developed and tested in CI. Windows (amd64) cross-compiles;
process supervision there (Job Objects) will be documented with its limits when it lands.

## Contributing

`make check` runs everything CI runs (gofmt, vet, tests, race detector, cross-builds); `make lint` runs
staticcheck. If you change output on purpose, regenerate the golden files with `make golden` and review the
diff. See [docs/testing.md](docs/testing.md).

## License

Not yet chosen.
