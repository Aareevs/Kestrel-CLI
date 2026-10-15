# Architecture

## Package map (as built)

```text
cmd/kestrel/            main: signal-aware context → internal/cli.Execute → os.Exit
internal/cli/           cobra commands, global flags, exit-code mapping, rendering choice
internal/app/           use cases shared by the CLI and (later) MCP: Inspect, ProposeConfig,
                        WriteConfig, LoadConfig, ValidateConfig, Doctor
internal/config/        kestrel.yaml schema, strict decoding, validation with line numbers,
                        defaults, ${ENV} references, masking for display
internal/discovery/     bounded, read-only project scan → service candidates
internal/output/        TTY/NO_COLOR detection, ANSI styles, text renderers, JSON writer
internal/appdir/        per-OS data directory (KESTREL_HOME override)
internal/buildinfo/     version metadata (ldflags, falls back to embedded VCS info)
pkg/api/                stable JSON types for every --format json output
tests/fixtures/projects sample projects scanned by golden tests
tests/integration/      in-process CLI tests and golden files
```

Packages listed in the build spec but not here (process, collectors, health, events, graph, correlation,
diagnosis, verification, storage, privacy, mcp) are created in the phase that first has code for them.

## Rules

- `internal/cli` only parses flags, calls `internal/app`, and renders. It holds no business logic.
- Every command that reads data supports `--format json`, and the JSON comes from `pkg/api` types. Fields are
  only ever added. A breaking change bumps `api.SchemaVersion`.
- Commands that can block take the context from `cmd.Context()`; `main` cancels it on SIGINT/SIGTERM.
- Time is injectable (`discovery.Options.Now`, `app.App.Now`) so tests are deterministic.
- Exit codes are mapped in one place (`cli.Execute`). Errors a command returns without an explicit code are
  internal errors (4). Errors raised by cobra's own parsing are usage errors (2).

## Discovery

`discovery.Scan` runs in two stages.

1. **Walk.** `filepath.WalkDir` from the symlink-resolved root. It skips well-known vendored and build
   directories and anything matched by `.gitignore` (root and nested), and stops at `MaxDepth` (3),
   `MaxFiles` (5000), or the time budget (3s). A stop is reported as `scan.truncated` with a reason. The walk
   only records file names; it doesn't open files.
2. **Analyze.** Per directory, each analyzer (Node, Go, Python, JVM, PHP, Ruby, Rust, Compose) reads only the
   manifests it needs, through one guarded reader. That reader resolves symlinks and refuses anything outside
   the root, files over 1 MiB, and non-regular files.

A post-pass then reconciles the results:

- **Compose merge.** A compose service whose `build` context is a directory with exactly one native candidate
  is merged into it. The candidate takes the compose name and command; the native command becomes an
  alternative.
- **Dockerfiles.** `EXPOSE` attaches a low-confidence port to candidates in the same directory. A Dockerfile
  with nothing else next to it becomes its own low-confidence candidate.
- **`.env.example`.** Variable names attach to candidates in the same directory. Some names imply
  infrastructure (`POSTGRES_*`, `REDIS_*`, …). Values are never kept.
- **Name uniqueness.** Compose names win, because `depends_on` refers to them.
- **Dependency resolution.** Each hint is one of three kinds: a declared name (`depends_on`), an
  infrastructure kind (from client libraries such as `pgx`, or env names), or a localhost port (from
  dev-server proxies). A hint resolves to a candidate only when exactly one candidate matches. Otherwise it
  stays unresolved and is shown with a `?`. Kestrel never adds an edge just because two services exist side
  by side.
- **Health suggestion.** Infrastructure gets TCP. HTTP frameworks get HTTP, using a health route found in the
  code if there is one, otherwise `/` at no more than medium confidence. Anything else gets TCP.
- **Confidence.** Every finding (command, port, health, dependency) carries its own source and confidence.
  The candidate's overall confidence is `high` when the command is high and the port is at least medium,
  `medium` when the command is at least medium, and `low` otherwise.

## Config

`config.Parse` decodes the file twice. The `yaml.Node` tree supplies line numbers. A `yaml.Decoder` with
`KnownFields(true)` rejects unknown keys. yaml.v3's type errors are reworded: "field x not found in type
config.Service" becomes `unknown field "x" (valid here: …)`. Semantic validation then collects every problem
in one pass: names, references with "did you mean" suggestions, cycles (DFS), probe shapes, durations, regex
compilation, and `${VAR}` syntax. Defaults are applied only to a valid config. `docs/kestrel.schema.json` is
kept in sync with the structs by `TestJSONSchemaInSync`.

## Dependencies

| Module | Why |
|---|---|
| `github.com/spf13/cobra` | Spec-mandated CLI framework: subcommands, help, persistent flags. |
| `gopkg.in/yaml.v3` | Spec-mandated YAML parser. Gives node line numbers and strict known-field decoding. Also parses compose files. |
| `github.com/spf13/pflag`, `github.com/inconshreveable/mousetrap` | Indirect, via cobra. |

Dev-only, not in `go.mod`: staticcheck, run with `go run honnef.co/go/tools/cmd/staticcheck@2025.1.1`.

## Deviations from the spec

- The `--session` global flag isn't registered until sessions exist (Phase 3), so no flag silently does nothing.
- `config show --format json` emits the config document itself (`internal/config` types with JSON tags). Its
  schema is `docs/kestrel.schema.json`, not a `pkg/api` type.
