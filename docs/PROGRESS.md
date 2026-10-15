# Progress

## Phase 1: Foundation (2026-10-15)

### Built

- Go module (`go 1.26.2`), cobra CLI with global flags `--project`, `--format text|json`, `--verbose`,
  `--quiet`, `--no-color` (plus `NO_COLOR`, `TERM=dumb`, and TTY detection). Exit codes 0/2/4 are wired up; 1
  and 3 are reserved for later phases.
- Commands: `inspect`, `init` (`--yes`, `--print`, interactive confirmation, never overwrites, `O_EXCL`),
  `config show|validate|path`, `doctor`, `version`.
- `internal/config`: strict decoding, validation that reports every issue with a line number (unknown fields
  with valid alternatives, "did you mean" for service names, cycles, probe shapes, durations, regexes, `${VAR}`
  syntax), defaults, `${VAR}` expansion that never writes values back, masking for display.
  `docs/kestrel.schema.json` is kept in sync by a test.
- `internal/discovery`: bounded read-only walk (depth/files/size/time limits, skip list, root and nested
  `.gitignore`, symlink-escape refusal). Analyzers for Node, Go, Python, Java (Maven/Gradle), PHP, Ruby, Rust,
  Docker Compose, Dockerfile, and `.env.example` (names only). A post-pass merges compose services with their
  native projects and resolves dependencies only when unambiguous. Every finding carries a source and a
  confidence.
- `pkg/api`: JSON types for inspect, config validate/path, doctor, version.
- Fixtures: `node-vite`, `go-api`, `compose-stack`, `python-django`.
- Makefile (`check`, `lint`, `golden`, `cross`, …), GitHub Actions (ubuntu + macOS), README, docs
  (architecture, configuration + JSON Schema, security, testing), logo, VHS tape.

### Exit criterion

> `kestrel inspect` produces correct output on ≥3 sample projects (Node, Go, compose-based) with golden-file tests.

Met for four fixtures. `TestInspectGolden` compares text and JSON output, and `TestInitPrintGolden` compares
the generated config. I reviewed every golden against the fixture files by hand: commands, ports, line numbers,
dependency edges. The generated configs pass `config validate` (`TestInitWritesValidConfigAndNeverOverwrites`).

### Quality checks (run on darwin/arm64, go1.26.2)

| Check | Result |
|---|---|
| `gofmt -l .` | no output |
| `go vet ./...` | pass |
| `go test ./...` | pass (appdir, config, discovery, integration) |
| `go test -race ./...` | pass |
| `go build ./cmd/kestrel` | pass |
| cross-builds linux/amd64, linux/arm64, darwin/amd64, windows/amd64 | pass |
| `staticcheck@2025.1.1` (via `go run`) | pass. It flagged two issues, both fixed: an unused variable and a dead assignment |
| Coverage (`-coverpkg=./internal/...,./pkg/...`) | 87.1% of statements |

The GitHub Actions workflow is written but hasn't run yet: there's no remote.

### Known gaps and limitations

- `internal/app`, `internal/cli`, and `internal/output` have no package-local tests. The in-process integration
  tests cover them (73–81% of functions).
- Colored output isn't covered by tests. Golden tests use `--no-color`, and the test harness never presents a
  TTY on stdout.
- The `.gitignore` matcher is a documented subset of git's rules.
- Ports detected from code (Go/Rust listen addresses) are literal strings. Ports set from env vars at runtime
  aren't detected.
- Compose: only the first published port per service is used. Override files (`compose.override.yaml`) and
  profiles are ignored.
- No license chosen yet.
- `docs/assets/demo.gif` hasn't been generated (VHS isn't installed). Only the `.tape` is committed.
