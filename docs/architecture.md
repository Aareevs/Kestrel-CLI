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
internal/events/        event identity (ULID, seq, fingerprint), level detection, pattern library
internal/privacy/       redaction applied before anything is printed (later: stored, exported)
internal/collectors/    line framing, repeat folding, bounded buffer with backpressure
internal/health/        http/tcp/process probes, readiness state machine
internal/process/       process groups / Job Objects, graceful-then-forced stop, supervisor
internal/appdir/        per-OS data directory (KESTREL_HOME override)
internal/buildinfo/     version metadata (ldflags, falls back to embedded VCS info)
pkg/api/                stable JSON types for every --format json output
tests/fakesvc/          scripted fake service used by supervision and scenario tests
tests/fixtures/projects sample projects scanned by golden tests
tests/integration/      in-process CLI tests and golden files
```

Packages listed in the build spec but not here yet (graph, correlation, diagnosis, verification, storage, mcp)
are created in the phase that first has code for them.

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

## Supervision and collection

```text
 child stdout ─┐                                         ┌─▶ live renderer (text / NDJSON)
 child stderr ─┤─ ReadLines ─ redact ─ classify ─ fold ─▶ Buffer ─▶ Collector.Run ─┤
 probes ───────┤        (per stream)                  (bounded,   (single,         └─▶ supervisor sink
 supervisor ───┘                                     seq at push)  in order)            (auto-probe)
```

- **Process groups.** Every process starts in a new group (Unix `Setpgid`) or Job Object (Windows). Stop sends
  SIGTERM (CTRL_BREAK) to the group, waits up to the grace period for the *group* to empty, then sends SIGKILL
  (TerminateJobObject). Kestrel only signals groups it created: POSIX doesn't reuse a PID while a group with
  that ID exists. Process exit is tracked separately from pipe EOF, so a grandchild that holds the pipes can't
  hide the main process's exit.
- **Lines.** `ReadLines` uses `bufio.Reader.ReadSlice`. Lines longer than 16 KiB are cut with a
  `…[truncated N bytes]` marker. ANSI escapes and other control characters are removed, `` redraws keep only
  their final state, and invalid UTF-8 is replaced.
- **Order.** Producers run concurrently, so `seq` is assigned inside the buffer lock when an event is
  inserted. Delivery order is therefore always `seq` order. Collector overload is reported as a marker event
  that takes its place in that order. See [events.md](events.md#backpressure).
- **Supervisor.** Starts services in dependency (topological) order. Before starting a service, it waits for
  each dependency's probe, bounded by that dependency's startup grace plus retries and capped at 60s, then
  starts it anyway with a warning, so downstream symptoms of an upstream failure are still observed. Services
  without a `command` are probed only. Shutdown stops probes, stops all groups in parallel, then drains the
  readers.
- **Live status.** A running session keeps a small JSON file at `<data dir>/run/<hash of project root>.json`
  (names, PIDs, liveness, readiness; never commands, environment, or output), rewritten at most every 250ms.
  `kestrel status` in another terminal reads it. SQLite session storage replaces it in Phase 3.

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

No new modules in Phase 2. Windows Job Objects are called through `syscall.NewLazyDLL` rather than adding
`golang.org/x/sys`.

Dev-only, not in `go.mod`: staticcheck, run with `go run honnef.co/go/tools/cmd/staticcheck@2025.1.1`.

## Deviations from the spec

- The `--session` global flag isn't registered until sessions exist (Phase 3), so no flag silently does nothing.
- Event source type is named `api.EventSource` because `api.Source` already names discovery sources.
- One event type was added to the spec's initial set: `probe_ready` (first passing probe), which timelines need
  to anchor "last known healthy".
- The supervisor lives in `internal/process`, as the spec's package map suggests, next to the
  process-group code it drives.
- `config show --format json` emits the config document itself (`internal/config` types with JSON tags). Its
  schema is `docs/kestrel.schema.json`, not a `pkg/api` type.
