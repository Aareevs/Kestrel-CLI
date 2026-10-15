<p align="center">
  <img src="docs/assets/kestrel-banner.png" alt="Kestrel: Every failure has a trail." width="100%" />
</p>

<p align="center">
  <strong>Stop guessing why your dev stack broke.</strong><br>
  Local-first debugging intelligence: what failed, in what order, why, and whether your fix actually worked.
</p>

<p align="center">
  <a href="https://github.com/Aareevs/Kestrel-CLI/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/Aareevs/Kestrel-CLI/ci.yml?branch=main&style=flat-square&logo=githubactions&logoColor=white&label=CI&labelColor=18181b" alt="CI" /></a>
  <a href="https://github.com/Aareevs/Kestrel-CLI/releases"><img src="https://img.shields.io/github/v/release/Aareevs/Kestrel-CLI?style=flat-square&logo=github&logoColor=white&label=Release&labelColor=18181b&color=2563eb&include_prereleases" alt="Release" /></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white&labelColor=18181b" alt="Go 1.26" /></a>
  <a href="#ai-agent-companion-kestrel-mcp"><img src="https://img.shields.io/badge/MCP-planned-52525b?style=flat-square&logo=anthropic&logoColor=white&labelColor=18181b" alt="MCP planned" /></a>
  <img src="https://img.shields.io/badge/macOS-27272a?style=flat-square&logo=apple&logoColor=white" alt="macOS" />
  <img src="https://img.shields.io/badge/Linux-27272a?style=flat-square&logo=linux&logoColor=white" alt="Linux" />
  <img src="https://img.shields.io/badge/Windows-27272a?style=flat-square&logo=windows&logoColor=white" alt="Windows" />
  <img src="https://img.shields.io/badge/License-TBD-52525b?style=flat-square&labelColor=18181b" alt="License: TBD" />
</p>

<p align="center">
  <a href="#quick-install">Quick Install</a>
  <span>&nbsp;&nbsp;•&nbsp;&nbsp;</span>
  <a href="#30-second-quickstart">30s Quickstart</a>
  <span>&nbsp;&nbsp;•&nbsp;&nbsp;</span>
  <a href="#what-it-looks-like">Example</a>
  <span>&nbsp;&nbsp;•&nbsp;&nbsp;</span>
  <a href="#why-kestrel">Why Kestrel?</a>
  <span>&nbsp;&nbsp;•&nbsp;&nbsp;</span>
  <a href="#supported-ecosystems">Ecosystems</a>
  <span>&nbsp;&nbsp;•&nbsp;&nbsp;</span>
  <a href="#documentation">Documentation</a>
</p>

---

Kestrel watches the services in your local stack, reconstructs what broke and in what order, ranks root-cause
hypotheses backed by the exact events that support them, and verifies whether your fix held. It runs offline,
needs no config to start, never uploads anything, and never runs a command you didn't ask for.

> [!NOTE]
> **Kestrel is in early development.** Discovery (`inspect`, `init`), config tooling, and supervision (`run`,
> `watch`, `status`) work today. Timelines, diagnosis, verification, session history, and the MCP server ship in
> phases. Anything not yet built is marked **planned** below. Every output in this README comes from a real run.

---

## Quick Install

### 1. Go Install (any platform with Go 1.26+)
```bash
go install github.com/Aareevs/Kestrel-CLI/cmd/kestrel@latest
```

### 2. From Source
```bash
git clone https://github.com/Aareevs/Kestrel-CLI
cd Kestrel-CLI
make build        # → ./bin/kestrel
```

### Coming with the first release
Homebrew tap, a `curl … | sh` installer with SHA-256 verification, and prebuilt binaries for macOS (Apple
Silicon and Intel), Linux (x86_64 and ARM64), and Windows (x64) on the
[Releases page](https://github.com/Aareevs/Kestrel-CLI/releases).

---

## 30-Second Quickstart

```bash
cd your-project

# 1. Inspect: what's here, how it probably runs, and how sure Kestrel is
kestrel inspect

# 2. Run anything, no config: output captured, classified, redacted; announced ports probed
kestrel run -- npm run dev

# 3. Or supervise the whole stack from a kestrel.yaml (kestrel init writes one for you)
kestrel init && kestrel watch
```

### Commands

| Command | Description | Status |
| :--- | :--- | :---: |
| `kestrel inspect` | Detected technologies and service candidates, each with its source file:line and confidence | ✅ |
| `kestrel init` | Propose a `kestrel.yaml` from discovery (`--yes`, `--print`); never overwrites | ✅ |
| `kestrel config show` / `validate` / `path` | Effective config with secrets masked, strict validation, location | ✅ |
| `kestrel doctor` | Self-check: platform, data directory, config, tools | ✅ |
| `kestrel version` | Version and build info | ✅ |
| `kestrel run -- <cmd>` | Wrap any command (or several with `--svc name="cmd"`), no config needed | ✅ |
| `kestrel watch [svc...]` | Supervise configured services in dependency order with live, classified output | ✅ |
| `kestrel status` | Liveness vs. readiness per service, for a live session or straight from `kestrel.yaml` | ✅ |
| `kestrel timeline` | Incident timeline: last healthy state → first anomaly → downstream symptoms | planned |
| `kestrel diagnose` | Ranked, evidence-backed hypotheses with a concrete next step | planned |
| `kestrel verify` | Post-fix checks over a stability window | planned |
| `kestrel sessions` / `cleanup` | Session history, redacted export, retention | planned |
| `kestrel mcp` | Read-only MCP server for AI agents | planned |

**Global flags** (on every command):
- `--format json`: stable, versioned JSON on every read command (schema in [`pkg/api`](pkg/api/)); `run` and
  `watch` stream [events](docs/events.md) as NDJSON.
- `--project <dir>`: project root (default: nearest ancestor with `kestrel.yaml`, else the current directory).
- `-v` / `--verbose`: diagnostic logging to stderr. `-q` / `--quiet`: essential output only.
- `--no-color`: plain output (`NO_COLOR` and non-TTY output are detected automatically).

**Exit codes:** `0` ok · `1` failure detected / verification failed · `2` invalid usage or config ·
`3` inconclusive investigation · `4` internal error.

---

## What It Looks Like

### Supervision

`kestrel watch` on a two-service config whose database crashes two seconds in. The services are
[`fakesvc`](tests/fakesvc), the scripted stand-in Kestrel's tests use, so this is reproducible. Real output,
including the redaction:

```text
kestrel session 01M4ZDNFN5XYA55F5C1HG58ZTT · db, api · Ctrl-C to stop
14:48:05 db      ▶ started (pid 87897): ./fakesvc --name db --listen 127.0.0.1:15432 --exit-after 2s --exit-code 1 --exit-message "FATAL:  could not open file global/pg_control: Permission denied"
14:48:05 db      │ db starting (pid 87897)
14:48:05 db      │ db listening on 127.0.0.1:15432
14:48:05 db      ✓ ready: tcp localhost:15432 (connected in 1ms)
14:48:05 api     ▶ started (pid 87900): ./fakesvc --name api --listen 127.0.0.1:18080 --connect 127.0.0.1:15432 --connect-every 250ms --secrets
14:48:05 api     │ api starting (pid 87900)
14:48:05 api     │ using api_key=[REDACTED:stripe-key] for billing
14:48:05 api     │ connecting with DATABASE_URL=postgres://app:[REDACTED:url-credentials]@localhost:5432/app
14:48:05 api     │ Authorization: [REDACTED:header]
14:48:05 api     │ aws key [REDACTED:aws-access-key] loaded
14:48:05 api     │ api listening on 127.0.0.1:18080
14:48:05 api     │ connected to 127.0.0.1:15432
14:48:05 api     ✓ ready: http http://localhost:18080/health (status 200 in 2ms)
14:48:07 db      │ FATAL:  could not open file global/pg_control: Permission denied
14:48:07 db      ✗ crashed (exit code 1)
14:48:07 api     │ dial tcp 127.0.0.1:15432: connect: connection refused  ‹dependency_conn_failed 127.0.0.1:15432 P006-connection-refused›
14:48:07 db      ✗ not ready: tcp localhost:15432 failed 2 times (dial [::1]:15432: connect: connection refused)
kestrel: stopping services (press Ctrl-C again to force quit)…
14:48:09 api     │ api received terminated, shutting down
14:48:09 api     │ ↻ ×9 more over 2.3s: dial tcp 127.0.0.1:15432: connect: connection refused
14:48:09 api     ■ stopped by Kestrel (exit code 0)

kestrel session 01M4ZDNFN5XYA55F5C1HG58ZTT ended
  SERVICE  LIVENESS     READINESS  DETAIL
  db       crashed      not_ready  tcp localhost:15432 · exit code 1
  api      exited       unknown    http http://localhost:18080/health · stopped by Kestrel
```

`api` started only after `db` answered its probe. The connection refusals are tagged with the pattern that
recognized them, and the retry loop is folded into one line. Secrets never reach the screen. Ctrl-C stopped
every process Kestrel started, including children, and nothing else. *Planned:* `kestrel diagnose` turning this
trail into "db failed first; api's errors are a downstream symptom", with event IDs as evidence.

### Discovery

`kestrel inspect` on [a compose project](tests/fixtures/projects/compose-stack) with Postgres, Redis, a Go API,
and a Next.js frontend. This is real output, taken from the golden test:

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

Every claim points to the file and line it came from. `api` and `web` appear once even though they're defined
both in compose and as their own Go/Node projects. When a dependency is only *inferred* (say, `pgx` in `go.mod`
implies Postgres) and two Postgres services exist, Kestrel says it can't tell rather than guessing.

---

## AI Agent Companion (`kestrel mcp`)

> **Planned.** This server isn't built yet.

AI coding agents debugging through raw shell commands burn context grepping logs and guessing at causes.
`kestrel mcp` will expose Kestrel's evidence over a **read-only** stdio MCP server, so an agent can ask for the
diagnosis instead of reconstructing it. Planned tools: `inspect_project`, `list_services`, `get_service_status`,
`get_recent_errors`, `get_failure_timeline`, `get_diagnosis`, `get_session_summary`, `get_verification_results`.
Output is redacted, and every result is labeled with its claim level (`observed` → `correlated` → `hypothesis` →
`verified`). There are no exec tools.

Setup will look like `claude mcp add kestrel -- kestrel mcp`. It won't work until the server ships.

---

## Why Kestrel?

| | Kestrel | Scrolling logs | `docker compose logs` | LLM reading logs |
| :--- | :---: | :---: | :---: | :---: |
| **Zero configuration to start** | ✓ | ✓ | — (needs compose) | ✓ |
| **Every finding cites file:line or event ID** | ✓ | — | — | — |
| **Separates observed facts from guesses** | ✓ | — | — | — |
| **Secrets redacted before anything is shown** | ✓ | — | — | — |
| **Ctrl-C stops the whole process tree, nothing else** | ✓ | — (orphans) | ✓ (containers) | — |
| **Bounded memory under log floods, drops reported** | ✓ | n/a | n/a | — (context overflow) |
| **Works offline, nothing uploaded** | ✓ | ✓ | ✓ | — |
| **Never executes anything it finds** | ✓ | ✓ | ✓ | — |
| **Ordered incident timeline across services** | planned | — | Partial (interleaved) | — |
| **Verifies the fix over a stability window** | planned | — | — | — |

---

## Supported Ecosystems

<p align="center">
  <img src="https://skillicons.dev/icons?i=nodejs,nextjs,vite,express,go,python,django,fastapi,flask,java,spring,ruby,rails,php,laravel,rust,docker&theme=dark" alt="Supported technologies" />
</p>

| Stack | Detected from | Support |
| :--- | :--- | :---: |
| **Node.js**: Vite, Next.js, Nuxt, Astro, SvelteKit, Angular, CRA, NestJS, Express… | `package.json` scripts, lockfile → npm/pnpm/yarn/bun, `vite.config.*`, dev-server proxies | full |
| **Go**: gin, echo, fiber, chi, net/http | `go.mod`, `main.go`, `cmd/*/main.go`, listen addresses and health routes in code | full |
| **Docker Compose** | services, published ports, `depends_on`, build contexts | full |
| **Python**: Django, FastAPI, Flask (pip, uv, Poetry, Pipenv) | `manage.py`, `pyproject.toml`, `requirements.txt`, `Pipfile` | partial |
| **Java**: Spring Boot (Maven, Gradle) | `pom.xml`, `build.gradle(.kts)`, `application.properties/yml` | partial |
| **Ruby**: Rails, Sinatra | `Gemfile` | partial |
| **PHP**: Laravel | `composer.json`, `artisan` | partial |
| **Rust**: axum, actix-web, rocket, warp | `Cargo.toml`, `src/main.rs` | partial |
| **Dockerfile only** | `EXPOSE` | partial |

"Full" means the command, port, and dependencies are read from your files. "Partial" means some of them, ports
especially, fall back to framework defaults.

- **Dependency inference**: client libraries (`pgx`, `ioredis`, `psycopg2`, …), `.env.example` names, and
  dev-server proxies are linked to detected services, but only when the match is unambiguous.
- **Bounded and safe**: depth, file-count, file-size, and time limits; honors `.gitignore`; refuses symlinks
  that escape the project; never opens `.env`.

---

## How It Works

```text
 discover ──▶ collect ──▶ normalize ──▶ correlate ──▶ diagnose ──▶ verify
 ✅ files      ✅ output,    ✅ typed,       planned       planned      planned
               probes,       redacted,       (incidents,   (ranked,     (stability
               lifecycle     classified      dep graph)    evidence)    window)
```

- **Discover** reads, never runs, your project files to work out what the services are.
- **Collect** supervises processes in their own process groups, reads stdout/stderr through a bounded
  buffer, and probes readiness (HTTP, TCP, process).
- **Normalize** turns every line into a typed [event](docs/events.md): redacted, severity-tagged, matched
  against a pattern library (port conflicts, refused connections, database errors, panics, tracebacks, OOMs,
  test failures), with repeats folded.

See the [architecture doc](docs/architecture.md).

---

## Configuration

Optional. With `kestrel.yaml` (or `.kestrel.yaml`) at the project root:

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

Validation is strict: unknown keys are errors, every problem comes with a line number, typos get "did you
mean", and dependency cycles are rejected. A [JSON Schema](docs/kestrel.schema.json) enables editor completion.
Full reference: [docs/configuration.md](docs/configuration.md).

---

## Privacy & Storage

- **Local only.** No network calls, no uploads, no telemetry.
- **Secrets stay out.** `.env` is never read. Only variable *names* come from `.env.example`. Every line of
  process output is [redacted](docs/security.md) (keys, tokens, JWTs, URL credentials, auth headers, secret
  `key=value` pairs) before it's printed. `config show` masks literal secrets.
- **Nothing you didn't ask for.** Kestrel only signals processes it started. Log text is data: it's never
  executed, and terminal escape sequences are stripped.
- **Out of your repo.** Kestrel's data lives in `~/Library/Application Support/Kestrel` (macOS),
  `$XDG_DATA_HOME/kestrel` (Linux), or `%LOCALAPPDATA%\Kestrel` (Windows). Override with `KESTREL_HOME`.

Threat model and limits: [docs/security.md](docs/security.md).

---

## Documentation

- **[Architecture](docs/architecture.md)**: package map, discovery and supervision pipelines, design rules.
- **[Events](docs/events.md)**: event schema, types, severity, backpressure, and the pattern library.
- **[Configuration Reference](docs/configuration.md)**: every field, default, and validation rule.
- **[Security Model](docs/security.md)**: threat model, masking/redaction limits, exec policy.
- **[Testing Guide](docs/testing.md)**: golden files, fixtures, adding a detector.
- **[Plan](docs/PLAN.md)** and **[Progress](docs/PROGRESS.md)**: phased roadmap and what's verified so far.

---

## Testing & Verification

```bash
make check    # gofmt, vet, tests, race detector, build, cross-compiles (what CI runs)
make lint     # staticcheck
make golden   # regenerate golden files after an intentional output change, then review the diff
```

### Manual Smoke Test
- [ ] **Build:** `make build`
- [ ] **Inspect a fixture:** `./bin/kestrel inspect --project tests/fixtures/projects/compose-stack`.
  *Verify:* 4 candidates, `api`/`web` merged with their Go/Node projects, every line cites a source.
- [ ] **JSON:** `./bin/kestrel inspect --project tests/fixtures/projects/go-api --format json`.
  *Verify:* valid JSON with `schema_version: 1`.
- [ ] **Init never overwrites:** in a scratch copy of a fixture, run `kestrel init --yes` twice.
  *Verify:* the second run exits `2` and the file is unchanged.
- [ ] **Validation:** add a typo like `helth:` to the generated `kestrel.yaml`, then run `kestrel config validate`.
  *Verify:* it points at the line and exits `2`.
- [ ] **Doctor:** `./bin/kestrel doctor`. *Verify:* all checks pass or explain themselves.
- [ ] **Zero-config run:** `go build -o /tmp/fakesvc ./tests/fakesvc && ./bin/kestrel run -- /tmp/fakesvc --listen 127.0.0.1:18080 --secrets`.
  *Verify:* the port is auto-probed (`✓ ready`), secrets show as `[REDACTED:…]`, and Ctrl-C prints the summary.
- [ ] **No orphans:** run `kestrel watch` on a config whose services spawn children, press Ctrl-C, then
  `ps -ef | grep fakesvc`. *Verify:* nothing left.
- [ ] **Status:** while `watch` runs, run `kestrel status` in a second terminal. *Verify:* it shows liveness and
  readiness live.

---

## Contributing

To add a detector, start with [docs/architecture.md](docs/architecture.md#discovery) and
[docs/testing.md](docs/testing.md). Each detector needs unit tests, and a fixture with reviewed goldens when it
adds a new ecosystem. Run `make check` before opening a PR.

---

## License

Not yet chosen.
