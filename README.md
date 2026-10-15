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
  <a href="#ai-agent-companion-kestrel-mcp"><img src="https://img.shields.io/badge/MCP-read--only-52525b?style=flat-square&logo=anthropic&logoColor=white&labelColor=18181b" alt="MCP read-only" /></a>
  <img src="https://img.shields.io/badge/macOS-27272a?style=flat-square&logo=apple&logoColor=white" alt="macOS" />
  <img src="https://img.shields.io/badge/Linux-27272a?style=flat-square&logo=linux&logoColor=white" alt="Linux" />
  <img src="https://img.shields.io/badge/Windows-27272a?style=flat-square&logo=windows&logoColor=white" alt="Windows" />
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-10b981?style=flat-square&labelColor=18181b" alt="License: MIT" /></a>
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
> **Kestrel is pre-release.** Everything described here works and is tested. No release has been published yet,
> so for now install with `go install` or from source. The one-line installer and Homebrew start working with the
> first release. Every output in this README comes from a real run.

---

## Quick Install

### Today: Go install (any platform with Go 1.26+)
```bash
go install github.com/Aareevs/Kestrel-CLI/cmd/kestrel@latest
```

### Today: from source
```bash
git clone https://github.com/Aareevs/Kestrel-CLI
cd Kestrel-CLI
make build        # → ./bin/kestrel
```

### From the first release: one-line install (macOS, Linux)
```bash
curl -fsSL https://raw.githubusercontent.com/Aareevs/Kestrel-CLI/main/scripts/install.sh | sh
```
Downloads the archive for your platform, **verifies its SHA-256 checksum** (and installs nothing if it doesn't
match), and puts `kestrel` in `~/.local/bin`. No `sudo`, no shell-profile edits. Pin a version with
`KESTREL_VERSION=v0.1.0`, or choose a directory with `KESTREL_INSTALL_DIR`.

### From the first release: Homebrew
```bash
brew install aareevs/tap/kestrel
```

Prebuilt archives for macOS (Apple Silicon, Intel), Linux (x86_64, ARM64), and Windows (x64) will be on the
[Releases page](https://github.com/Aareevs/Kestrel-CLI/releases). The release tooling is built and tested
locally ([docs/releasing.md](docs/releasing.md)).

---

## 30-Second Quickstart

```bash
cd your-project

# 1. Inspect: what's here, how it probably runs, and how sure Kestrel is
kestrel inspect

# 2. Run anything, no config: output captured, classified, redacted; announced ports probed
kestrel run -- npm run dev

# 3. When something breaks: what failed first, why Kestrel thinks so, and what to check next
kestrel diagnose

# 4. After your fix: is it actually fixed? Every check must hold for a 30s window, not just one probe
kestrel verify
```

With a `kestrel.yaml` (`kestrel init` writes one), `kestrel watch` supervises the whole stack in dependency order.

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
| `kestrel timeline` | Incident timeline: last healthy state → first anomaly → downstream symptoms → recovery | ✅ |
| `kestrel diagnose` | Ranked, evidence-backed hypotheses with a concrete next step | ✅ |
| `kestrel verify` | Did the fix work? Checks held across a stability window, plus before/after evidence; runs your test command only if you allow it | ✅ |
| `kestrel sessions list\|show\|export\|delete` | Browse recorded sessions; export a redacted JSON bundle | ✅ |
| `kestrel cleanup [--dry-run]` | Apply retention now (`retention_days`, `max_total_mb`) | ✅ |
| `kestrel mcp` | Read-only MCP server (stdio) so AI agents can ask for the diagnosis | ✅ |

**Global flags** (on every command):
- `--format json`: stable, versioned JSON on every read command (schema in [`pkg/api`](pkg/api/)); `run` and
  `watch` stream [events](docs/events.md) as NDJSON.
- `--project <dir>`: project root (default: nearest ancestor with `kestrel.yaml`, else the current directory).
- `--session <id|prefix|last>`: which recorded session `sessions show/export/delete` act on.
- `-v` / `--verbose`: diagnostic logging to stderr. `-q` / `--quiet`: essential output only.
- `--no-color`: plain output (`NO_COLOR` and non-TTY output are detected automatically).

**Exit codes:** `0` ok · `1` failure detected / verification failed · `2` invalid usage or config ·
`3` inconclusive investigation (e.g. `diagnose` can't single out a cause, or `verify` couldn't check anything) ·
`4` internal error.

---

## What It Looks Like

### Diagnosis: the database → API → frontend failure

A database that fails to initialize, an API that depends on it, and a frontend that proxies to the API. The
services are [`fakesvc`](tests/fakesvc) stand-ins (this is Kestrel's
[Scenario A](tests/integration/scenarios_test.go)), so you can reproduce it. This is the real, unedited output of
`kestrel diagnose` after `kestrel watch`:

```text
$ kestrel diagnose
Session 01M4ZJJPYB0ZCZPJXVCZ8FZYV9 · watch · closed · 16:13:57–16:14:03 · database, api, frontend

Incident #1  ·  3 services affected  ·  started 16:13:57  ·  correlation strong
  Initiating failure: database (evt …D8245V)

  Likely initiating failure  [hypothesis · high]
  database failed first; api, frontend failed because of it
    ✓ database: FATAL:  database files are incompatible with server  evt …D8245V
    ✓ database exited with code 1                               evt …1XKSJ1
    ✓ api: connection refused to 127.0.0.1:15432 ×24            evt …PST2FE
    ✓ api never became ready                                    evt …ZWD39Y
    ✓ frontend: connection refused to 127.0.0.1:8080 ×15        evt …59R16Y
    ✗ no contradicting evidence found
    high because: database failed first (16:13:57); api declares depends_on: [database]; frontend depends on api, which failed after database
    rule K002-dependency-down

  Other hypotheses
  2. [hypothesis · high] database failed during startup: FATAL:  database files are incompatible with server  K004-startup-failure
       ✓ database: FATAL:  database files are incompatible with server  evt …D8245V
       ✓ database exited with code 1                               evt …1XKSJ1
       ✗ no contradicting evidence found
  3. [hypothesis · low] api failed first; frontend failed because of it  K002-dependency-down
       ✓ api never became ready                                    evt …ZWD39Y
       ✓ frontend: connection refused to 127.0.0.1:8080 ×15        evt …59R16Y
       ✗ database: FATAL:  database files are incompatible with server (a dependency of api, failed earlier)  evt …D8245V
  4. [hypothesis · low] api never became ready  K004-startup-failure
       ✓ api never became ready                                    evt …ZWD39Y
       ✗ api: connection refused to 127.0.0.1:15432 (database had already failed) ×24  evt …PST2FE

  Downstream symptoms  [correlated]
    api       connection errors ×24; never became ready
    frontend  connection errors ×15

  Next step
    Check why database failed; the others are likely to recover once it does.
    $ kestrel timeline --service database

Kestrel only suggests commands; it never runs them. Full evidence: --format json
```

Every ✓ and ✗ line that carries an `evt` ID is an event Kestrel recorded; `--format json` gives the full IDs. The database is blamed because
`api` *declares* it depends on it and `api`'s refused connections target the database's port, not because the
failures happened close together. Two services failing at the same moment with no such link produce an
**unresolved** incident with competing hypotheses instead of a guess (exit code `3`). `kestrel timeline` shows the
same incident in order:

```text
$ kestrel timeline
Incident #1  ·  3 services affected  ·  started 16:13:57  ·  correlation strong
  Initiating failure: database (evt …D8245V)
  16:13:57           database  ● started       started (pid 12633): ./bin/fakesvc --name database --exit-after 300ms --exit-code 1 --exit-message …
  16:13:57           database  ✗ first anomaly FATAL:  database files are incompatible with server
  16:13:57           database  ✗ key           crashed (exit code 1)
  16:13:57–16:14:03  api       ↳ symptom       dial tcp 127.0.0.1:15432: connect: connection refused  ×24
  16:13:58           database  ✗ key           never became ready: tcp localhost:15432 (dial [::1]:15432: connect: connection refused)
  16:14:00           api       ↳ symptom       never became ready: http http://localhost:8080/health (dial [::1]:8080: connect: connection refused)
  16:14:00–16:14:03  frontend  ↳ symptom       Error: connect ECONNREFUSED 127.0.0.1:8080  ×15
```

The full rule list, with what each needs as evidence, is in [docs/diagnosis-rules.md](docs/diagnosis-rules.md).

### Verification: did the fix work?

An API that fails its health check. `kestrel verify` runs while `kestrel watch` is still recording, so besides
probing, it compares the session's new events with the incident's. The command check is a stand-in for your
test suite (`go test ./...`, `npm test`). This is real, unedited output. Before the fix, from a script (no
terminal, so the command is not run):

```text
$ kestrel verify --window 10s
Session 01M4ZN0HSHDN9RQ18QA2QMV1DJ · watch · active · 16:56:28–running · api

Incident #1  ·  1 service affected  ·  started 16:56:31  ·  correlation strong
  Initiating failure: api (evt …4BBQD3)

  Before: what was failing
    ✗ api: api never became ready                               evt …4BBQD3

  Checks (every 2s across a 10s window)
    ✓ api  liveness process
         6/6 samples passed · the running session
    ✗ api  probe http://127.0.0.1:18080/health
         0/6 samples passed · last failure: status 503 (want 200-399) · its health probe
    -      command echo 'ok  example.com/api  0.41s' (in .)
         skipped: not authorized: pass --allow-exec to run it (no terminal to ask in) · verify.checks

  During the window no new errors from the affected services

Verdict: still_failing  [observed]
  · failing: api (probe http://127.0.0.1:18080/health)
  · not run: command `echo 'ok  example.com/api  0.41s'`
```

After the fix (exit code 0, after the full default 30s window):

```text
$ kestrel verify --allow-exec
  Before: what was failing
    ✗ api: api never became ready                               evt …4BBQD3

  Recovery recorded
    ✓ api: recovered: http http://127.0.0.1:18080/health (status 200 in 1ms)  evt …KC727H

  Checks (every 2s across a 30s window)
    ✓ api  liveness process
         16/16 samples passed · the running session
    ✓ api  probe http://127.0.0.1:18080/health
         16/16 samples passed · its health probe
    ✓      command echo 'ok  example.com/api  0.41s' (in .)
         passed · exit code 0 · 0.0s · verify.checks

  During the window no new errors from the affected services

Verdict: resolved  [verified]
  · passing on every sample: api (probe http://127.0.0.1:18080/health), command `echo 'ok  example.com/api  0.41s'`
  · no new errors from the affected services during the window
```

(The session and incident header lines are the same as above and are left out here.) A fix that lands halfway
through the window, or a relapse during it, is never `resolved`. In a terminal, verify shows you each command, its
directory, and its timeout, and runs it only if you answer `y`. Verdicts and checks:
[docs/verification.md](docs/verification.md).

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
every process Kestrel started, including children, and nothing else. `kestrel diagnose` then turns that trail into
a ranked explanation, as shown above.

### Sessions

Every `run` and `watch` is recorded locally, so you can come back to it after the terminal scrolls away. Real
output (project path and the command line shortened):

```text
$ kestrel sessions list
Sessions (2)
  ID                          STARTED              DURATION  MODE   STATUS       EVENTS  ERRORS       SIZE  SERVICES
  01M4ZG0QD6XNDEP8XRGMCARVQN  2026-10-15 15:29:11  1s        run    closed           13       4    6.6 KiB  api
  01M4ZG0PZ065JG1C1933VYZH3P  2026-10-15 15:29:10  <1s       run    closed            3       0    1.7 KiB  web

$ kestrel sessions show last
Session  01M4ZG0QD6XNDEP8XRGMCARVQN
  mode      run · closed
  project   ~/src/shop
  time      2026-10-15 15:29:11 → 2026-10-15 15:29:12 (1s)
  recorded  13 events, 4 errors, 0 info lines dropped under load, 6.6 KiB on disk

Final service state
  SERVICE  LIVENESS     READINESS  DETAIL
  api      crashed      not_ready  tcp localhost:18082 · exit code 2

Events by type
  dependency_conn_failed 2 · log_line 8 · probe_ready 1 · process_crashed 1 · process_started 1

Notable events (warnings and above, lifecycle, probes; last 40 at most)
  · 15:29:11 api      process_started        started (pid 4182): fakesvc --name api …
  ✗ 15:29:11 api      dependency_conn_failed dial tcp 127.0.0.1:15440: connect: connection refused
  ✓ 15:29:11 api      probe_ready            ready: tcp localhost:18082 (connected in 5ms)
  ✗ 15:29:11 api      dependency_conn_failed dial tcp 127.0.0.1:15440: connect: connection refused
  ✗ 15:29:12 api      log_line               fatal: cannot reach database
  ✗ 15:29:12 api      process_crashed        crashed (exit code 2)
```

`kestrel sessions export last -o bundle.json` writes the whole session as redacted JSON (never overwriting an
existing file). Old sessions are removed automatically when a session ends, per `storage.retention_days`
(default 7) and `storage.max_total_mb` (default 500); `kestrel cleanup --dry-run` shows what would go. If
Kestrel itself is killed mid-session, the next command marks that session `interrupted`, and everything recorded
up to that point is still readable. If storage is unavailable, `run` and `watch` carry on and tell you the
session isn't being saved.

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

AI coding agents debugging through raw shell commands burn context grepping logs and guessing at causes.
`kestrel mcp` exposes Kestrel's evidence over a **read-only** stdio MCP server, so an agent can ask for the
diagnosis instead of reconstructing it.

```bash
claude mcp add kestrel -- kestrel mcp --project /path/to/your/project
```

| Tool | What the agent gets |
| :--- | :--- |
| `get_diagnosis` | Ranked hypotheses per incident, with supporting/contradicting event IDs, confidence, next step |
| `get_failure_timeline` | Incident timelines (optionally one service's full output), dependency graph, past verifications |
| `get_recent_errors` | The latest error and fatal events |
| `get_session_summary` | A recorded session: final state, counts, notable events |
| `get_verification_results` | Past `kestrel verify` runs and the latest full report |
| `get_service_status` | Liveness and readiness per service |
| `list_services` / `inspect_project` | Configured services (secrets masked) / discovery's findings with sources |

- **Read-only.** No tool starts, stops, signals, or runs anything. Verification and commands stay with you.
- **Honest about uncertainty.** Every result carries its claim level (`observed` → `correlated` → `hypothesis` →
  `verified` / `unresolved`), and the agent is told not to present a hypothesis as fact.
- **Logs are untrusted.** The agent is told that event messages are process output, never instructions, so a
  hostile log line can't steer it.
- **Redacted twice.** Everything was redacted when recorded, and is redacted again on the way out with your
  current patterns.

Other clients (Cursor, Claude Desktop, VS Code) and the full tool reference: [docs/mcp.md](docs/mcp.md).

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
| **Ordered incident timeline across services** | ✓ | — | Partial (interleaved) | — |
| **Names the failure that came first, with evidence** | ✓ | — | — | — (guesses) |
| **Says "unresolved" instead of guessing** | ✓ | n/a | n/a | — |
| **Verifies the fix over a stability window** | ✓ | — | — | — |

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
 discover ──▶ collect ──▶ normalize ──▶ record ──▶ correlate ──▶ diagnose ──▶ verify
 ✅ files      ✅ output,    ✅ typed,      ✅ SQLite    ✅ incidents  ✅ ranked     ✅ stability
               probes,       redacted,      + log        dep graph,    evidence,    window,
               lifecycle     classified     segments     timeline      next step    before/after
```

- **Discover** reads, never runs, your project files to work out what the services are.
- **Collect** supervises processes in their own process groups, reads stdout/stderr through a bounded
  buffer, and probes readiness (HTTP, TCP, process).
- **Normalize** turns every line into a typed [event](docs/events.md): redacted, severity-tagged, matched
  against a pattern library (port conflicts, refused connections, database errors, panics, tracebacks, OOMs,
  test failures), with repeats folded.

- **Correlate** builds a dependency graph from declared `depends_on` and port evidence (never from timing alone),
  groups anomalies into incidents, and finds the failure that came first.
- **Diagnose** runs [rules](docs/diagnosis-rules.md) that each propose hypotheses with supporting and
  contradicting events, a confidence level with its reasons, and one next step. Observed facts, correlations, and
  hypotheses are always labeled as such.
- **Verify** re-checks the affected services across a stability window (every sample must pass), re-tries the
  addresses that refused connections, compares a live session's new events with the incident's, and runs your
  test command only with `--allow-exec` or your explicit `y`. It never restarts anything.
- **Serve** (`kestrel mcp`) gives AI agents the same diagnosis, timelines, and verification results over a
  read-only MCP server, redacted and labeled with claim levels.
- **Record** stores each session in a local SQLite database, with the full redacted text in append-only,
  gzipped log segments, and enforces retention. Nothing is stored that wasn't redacted first.

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
verify:
  checks:
    - { type: command, run: go test ./..., dir: ./backend, timeout: 120s }   # runs only if you allow it
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
- **Nothing you didn't ask for.** Kestrel only signals processes it started. `verify` runs a configured command
  only with `--allow-exec` or after you confirm it. Log text is data: it's never executed, and terminal escape
  sequences are stripped.
- **Out of your repo.** Kestrel's data lives in `~/Library/Application Support/Kestrel` (macOS),
  `$XDG_DATA_HOME/kestrel` (Linux), or `%LOCALAPPDATA%\Kestrel` (Windows). Override with `KESTREL_HOME`.
  Sessions are kept 7 days / 500 MB by default, and `kestrel sessions delete` removes one immediately.

Threat model and limits: [docs/security.md](docs/security.md).

---

## Platform Support

| | macOS | Linux | Windows |
| :--- | :---: | :---: | :---: |
| Discovery, diagnosis, timelines, sessions, MCP | ✅ tested (CI) | ✅ tested (CI) | ✅ tested (CI) |
| Supervision (process groups, clean Ctrl-C) | ✅ tested | ✅ tested | ✅ tested (Job Objects) |
| Services survive `kill -9` of Kestrel? | can (no parent-death signal on macOS) | no (`Pdeathsig`) | no (job closes) |
| `verify` command checks | ✅ tested | ✅ tested | ✅ tested (`cmd.exe /C`) |
| Install script / Homebrew | from the first release | from the first release | download the `.zip` |

Binaries are static (pure-Go SQLite, no cgo). Windows is tested across all packages in CI via native Job Objects
containment and `cmd.exe` process trees (Go's `-race` detector is skipped on Windows runners). They are not notarized for macOS; see
[docs/releasing.md](docs/releasing.md#notes).

---

## Troubleshooting

| Symptom | What to do |
| :--- | :--- |
| `no kestrel.yaml found` from `watch` | Run `kestrel init` to generate one from discovery, or use `kestrel run -- <command>`, which needs no config. |
| `session storage unavailable` | Run `kestrel doctor`. Check permissions on the data directory, or point `KESTREL_HOME` somewhere writable. Supervision keeps working without storage. |
| `diagnose` says **unresolved** (exit 3) | Several services failed with nothing linking them. Add `depends_on` to `kestrel.yaml` if one really depends on another, and follow the suggested next check. |
| `verify` says **inconclusive** | Nothing could be checked. Give the service a `health` probe, or run `verify` while `kestrel watch` is still running. |
| Your test command didn't run during `verify` | Commands need `--allow-exec`, or a `y` at the prompt in a terminal. |
| An MCP client sees no sessions | Start the server with `--project <dir>` pointing at the project you ran `watch`/`run` in, with the same `KESTREL_HOME`. |
| A port is "already in use" | `kestrel diagnose` names the port and suggests `lsof -nP -iTCP:<port> -sTCP:LISTEN`. Kestrel never touches the process holding it. |

---

## Documentation

- **[Architecture](docs/architecture.md)**: package map, discovery and supervision pipelines, design rules.
- **[Events](docs/events.md)**: event schema, types, severity, backpressure, and the pattern library.
- **[Diagnosis Rules](docs/diagnosis-rules.md)**: how incidents form, how hypotheses are ranked, every rule.
- **[Verification](docs/verification.md)**: checks, the stability window, before/after, verdicts, exec policy.
- **[MCP Server](docs/mcp.md)**: setup for Claude Code and other clients, every tool, redaction, limits.
- **[Releasing](docs/releasing.md)**: GoReleaser, the Homebrew tap, the install script.
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
make release-snapshot   # build every release artifact into dist/ (publishes nothing)
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
- [ ] **Sessions:** after a run, `./bin/kestrel sessions list`, `sessions show last`, `sessions export last -o /tmp/s.json`,
  then `grep -c REDACTED /tmp/s.json`. *Verify:* the session is listed, secrets appear only as `[REDACTED:…]`.
- [ ] **Diagnosis:** after a `watch` in which something failed, `./bin/kestrel diagnose` then `kestrel timeline`.
  *Verify:* the incident names what failed first, every ✓/✗ line has an `evt` ID, and the next step is a command.
- [ ] **Verify:** with `watch` running on a failing service, `./bin/kestrel verify --window 10s < /dev/null`.
  *Verify:* `still_failing`, exit 1, and the command check is skipped. Fix the service, run `kestrel verify` in a
  terminal, and answer `n`, then `y`, at the prompt. *Verify:* the command runs only after `y`, and the verdict is
  `resolved` only after the full window.
- [ ] **MCP:** `claude mcp add kestrel -- ./bin/kestrel mcp --project <a project with a failed session>`, then ask
  the agent what failed. *Verify:* it calls `get_diagnosis`, cites event IDs, and calls hypotheses hypotheses.
- [ ] **Retention:** `./bin/kestrel cleanup --dry-run`. *Verify:* it explains what it keeps and why.
- [ ] **Status:** while `watch` runs, run `kestrel status` in a second terminal. *Verify:* it shows liveness and
  readiness live.

---

## Contributing

To add a detector, start with [docs/architecture.md](docs/architecture.md#discovery) and
[docs/testing.md](docs/testing.md). Each detector needs unit tests, and a fixture with reviewed goldens when it
adds a new ecosystem. Run `make check` before opening a PR.

---

## License

MIT License. See [LICENSE](LICENSE).
