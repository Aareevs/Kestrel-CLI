# Kestrel — Working Plan

Living document. The authoritative spec is `KESTREL_BUILD.md` (kept outside the repo); this file records how
we are executing it, the decisions made along the way, and the risks we are watching.

## Starting state (2026-10-15)

- Empty directory, no existing code, no git repo. Initialized git and a Go module.
- Toolchain: `go1.26.2 darwin/arm64`. `staticcheck` and `goreleaser` are not installed; `staticcheck` is
  run via `go run honnef.co/go/tools/cmd/staticcheck@<pinned>` so nothing is installed system-wide.
- `proxy.golang.org` returned `unexpected EOF` from this machine; `GOPROXY=direct` works. CI uses the default proxy.

## Phases

| # | Phase | Exit criterion | Status |
|---|---|---|---|
| 1 | Foundation: module, cobra CLI, config load/validate, discovery, `inspect`, `init`, `config`, `doctor`, `version`, Makefile, CI, README | `kestrel inspect` correct on ≥3 fixture projects (Node, Go, compose) with golden tests | done (see PROGRESS.md) |
| 2 | Supervision: process groups, collectors, backpressure, probes, `run`, `watch`, `status`; `tests/fakesvc` | 3 fakesvc under `watch`, Ctrl-C → zero children (tested); 50 MB/min flood → bounded RSS (measured) | planned |
| 3 | Persistence: SQLite, migrations, log segments, sessions, retention, `cleanup`, `sessions`; `privacy` package | session persisted/listed/shown/exported (redacted)/deleted; retention test | planned |
| 4 | Correlation + diagnosis: graph, incidents, timeline, rules, `timeline`, `diagnose` | Scenarios A, B, C, D, I golden; every hypothesis has evidence IDs | planned |
| 5 | Verification: `verify`, stability window, authorized exec, before/after | Scenario E; exec refused without authorization (tested) | planned |
| 6 | MCP server (read-only, reuses `internal/app`) | test client retrieves real diagnosis; redaction verified | planned |
| 7 | Hardening + release: Scenarios F, G, H, J; GoReleaser; install script; tap; final README | `goreleaser release --snapshot --clean` succeeds | planned |

## Phase 1 breakdown

1. `cmd/kestrel` → `internal/cli` (cobra root, global flags, exit-code mapping).
2. `internal/output`: TTY / `NO_COLOR` / `--no-color` detection, small ANSI style helpers, JSON encoder.
3. `pkg/api`: stable JSON types for inspect, config validation, doctor, version.
4. `internal/config`: typed schema, strict decoding (`KnownFields`), defaults, semantic validation with
   line numbers (from the `yaml.Node` tree), dependency cycle detection, `${ENV}` reference expansion,
   masking for display. JSON Schema in `docs/kestrel.schema.json`, kept in sync by a reflection test.
5. `internal/discovery`: bounded walk (depth, file count, time budget, file size), skip list + `.gitignore`
   (root and nested), symlink-escape refusal, never executes anything. Analyzers: Node, Go, Rust, Python,
   Java (Maven/Gradle), PHP/Laravel, Ruby/Rails, Dockerfile, docker-compose, `.env.example` (keys only).
   Post-pass: merge compose services with the native project they build, resolve inferred dependencies
   (e.g. `pgx` → the compose `postgres` service), make names unique.
6. `internal/app`: `Inspect`, `ProposeConfig`, `WriteConfig`, `LoadConfig`, `Doctor`. CLI only renders.
7. Commands: `inspect`, `init`, `config show|validate|path`, `doctor`, `version`.
8. Fixtures in `tests/fixtures/projects/` (node-vite, go-api, compose-stack, python-django) + golden
   text/JSON tests in `tests/integration`. `-update` flag regenerates goldens.
9. Makefile, GitHub Actions (macOS + Linux), README, docs.

## Key decisions

- **Module path** `github.com/Aareevs/Kestrel-CLI`, matching the GitHub repository.
- **Project root resolution**: `--project` if given; otherwise the nearest ancestor of the working directory
  containing `kestrel.yaml` / `.kestrel.yaml`; otherwise the working directory. Both config names present → error.
- **Global flags** in Phase 1: `--project`, `--format`, `--verbose`, `--quiet`, `--no-color`. `--session` is
  added in Phase 3 when sessions exist, rather than shipping a flag that does nothing.
- **Discovery confidence is per finding** (command, port, health, dependency each carry their own source and
  confidence) plus an overall candidate confidence derived from them by a fixed rule.
- **Compose + native merge**: a compose service whose `build` context is a directory that also has a native
  candidate becomes one candidate: compose name, native technology, compose command as primary and the
  native command as an alternative.
- **`.env.example`**: key names only. `.env` is never opened.
- **Masking before redaction exists**: `config show` masks all literal `env` values and URL passwords. The
  general redaction engine (`internal/privacy`) lands with persistence in Phase 3, before anything is stored.
- **`config show --format json`** emits the config document itself (schema = `docs/kestrel.schema.json`).
- **`doctor` is read-mostly**: it probes writability with a temp file and removes it; it does not create the
  data directory.
- **Exit codes** per spec; `doctor` with a failing check exits `4`.

## Risks

- Discovery heuristics are inherently partial; mitigated by explicit per-finding confidence and sources, and
  by never pretending a guess is a fact.
- The hand-written `.gitignore` matcher does not implement every corner of git's semantics (documented).
- Go 1.26 vs. staticcheck release compatibility; CI pins a version and we record what actually ran.
- Windows Job Objects (Phase 2) cannot be exercised on this macOS machine; only cross-compiled.
- No license chosen yet; needs the owner's decision.
