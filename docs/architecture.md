# Architecture

## Package map (as built)

```text
cmd/kestrel/            main: signal-aware context → internal/cli.Execute → os.Exit
internal/cli/           cobra commands, global flags, exit-code mapping, rendering choice
internal/app/           use cases shared by the CLI and (later) MCP: Inspect, ProposeConfig,
                        WriteConfig, LoadConfig, ValidateConfig, Doctor, Watch, Diagnose, Verify
internal/config/        kestrel.yaml schema, strict decoding, validation with line numbers,
                        defaults, ${ENV} references, masking for display
internal/discovery/     bounded, read-only project scan → service candidates
internal/output/        TTY/NO_COLOR detection, ANSI styles, text renderers, JSON writer
internal/events/        event identity (ULID, seq, fingerprint), level detection, pattern library
internal/privacy/       redaction applied before anything is printed, stored, or exported
internal/storage/       SQLite sessions + events, log segments, migrations, retention, export
internal/graph/         dependency graph: declared depends_on + port evidence (never timing)
internal/correlation/   anomalies → incidents, initiating failure, timelines
internal/diagnosis/     rule engine; rules/ holds K001–K006
internal/verification/  stability-window sampling, verdicts, authorized command checks
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

Packages listed in the build spec but not here yet (verification, mcp) are created in the phase that first has
code for them.

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
  `…[truncated N bytes]` marker. ANSI escapes and other control characters are removed, `
` redraws keep only
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
  `kestrel status` in another terminal reads it. It stays alongside session storage because it answers a
  different question, "what is running right now", cheaply and without opening the database.

## Storage

```text
 collector ─▶ Recorder ──(batch: 256 events / 100ms, one transaction)──▶ kestrel.db  (WAL)
                 │                                                       sessions · events · segments
                 └─▶ per-service segment files  sessions/<session>/<service>-NNN.log
                      append-only NDJSON, rotated at 4 MiB, gzipped when closed (.log.gz)
```

- **What goes where.** SQLite holds sessions, an indexed `events` table, and a `segments` index. Event
  messages in SQLite are cut to 1 KiB; the *full* redacted event is a JSON line in its service's segment, and
  the event row records the segment ID and byte offset. Unbounded raw text never lives in a SQLite row.
- **Indexes.** `events` is keyed by `(session_id, seq)`, with indexes on `(session_id, service, time)`,
  `(session_id, type)` and `id`; segments are indexed by `(session, service, first_seq)`.
- **Migrations.** `internal/storage/migrations/NNN_name.sql`, embedded. Each runs in one transaction together
  with its `schema_migrations` row, so a failed migration leaves nothing behind and is retried next time. A
  database written by a newer Kestrel is refused rather than guessed at.
- **Backpressure.** `Recorder.Emit` blocks when the disk can't keep up. That fills the collector buffer, which
  drops only debug/info lines and reports it. The recorder never drops an event itself: failures (disk full,
  permissions) are counted on the session as `write_errors`, shown as "incomplete", and supervision continues.
- **Closing.** On a clean end the recorder flushes, gzips the open segments, and records final counters. After a
  crash, the next command that opens the store marks the session `interrupted` (its Kestrel PID is gone) and
  seals its segments. A torn last line in a segment is ignored when reading.
- **Retention** (on session end and `kestrel cleanup`): remove closed sessions that ended more than
  `retention_days` ago, then, while the total exceeds `max_total_mb`, the oldest remaining. Active sessions and
  the session that just ended are never removed. Rows are deleted before files, so an index entry never points at
  deleted data; leftover directories are swept. Freed pages return to the OS via incremental vacuum.
- **Size accounting is an estimate**: compressed segment bytes plus ~330 B per event row. Measured against real
  disk usage (database + WAL + segments) after a 50 MB/min flood, reported size was 0.98× actual, and a test
  fails if it drifts outside ±15%. It first read 0.70× with a naive constant, which is why it was calibrated.
- **Export** streams one JSON document (always valid, even with unreadable data, which becomes `warnings`),
  restores full messages from segments, and redacts every text field a second time.
- **Project identity.** Sessions are keyed by the project root with symlinks resolved, so `/var/…` and
  `/private/var/…` can't split one project's history.

## Correlation and diagnosis

```text
 stored session ──▶ topology + diagnostic events ──▶ graph ──▶ incidents ──▶ rules ──▶ ranked hypotheses
                    (names, depends_on, probe ports)          (strong/weak     (K001–K006,   (evidence IDs,
                                                               links, roots)    pure funcs)   factors, next step)
```

- **Topology** is recorded with each session (migration 002), so any stored session can be diagnosed later.
  Diagnosis reads only the diagnostic events: non-log events, warn+ lines, and listen announcements.
- **Graph.** Declared edges come from `depends_on`. Inferred edges come only from connection errors aimed at a port
  exactly one service owns. A contested port has no owner. Two services failing together never creates an edge.
- **Incidents.** Anomalies join the current incident while they keep arriving within `correlation.window`. Each
  join is strong (same service, dependency path, port) or weak (time only), and the incident reports its
  correlation strength separately from any hypothesis's confidence.
- **Initiating failure.** A member is explained when it depends on a member that failed earlier. One unexplained
  member is the initiating failure. With several, the incident says so and is diagnosed `unresolved`.
- **Rules** are pure functions of a context (incident, graph, events, the output before each failure). They
  never touch storage or the system, which keeps them deterministic and unit-testable. The engine drops any
  hypothesis without evidence and ranks the rest. See [diagnosis-rules.md](diagnosis-rules.md).
- **Causes precede crashes.** When a process exits, the supervisor lets its remaining output drain (up to 2s)
  before recording the exit, so the line that explains a crash always has an earlier `seq` than the crash.
- **Counting.** A repeat summary stands for exactly `attrs.repeats` folded lines (`Event.Lines()`). Timelines
  merge it into the run it continues.

## Verification

```text
 session + incident ──▶ choose checks ──▶ authorize commands ──▶ sample window ──▶ run commands ──▶ verdict
 (before, recovery)     (probes, refused     (--allow-exec or       (every interval;    (own process     (saved with
                         addresses, liveness)  a y at a prompt)      new live events)     group, timeout)  the session)
```

- `internal/verification` holds the pure parts: `Run` samples probes across the window (sleep and polling are
  injectable), `Decide` turns observations into a verdict with reasons, and `RunCommand` runs a command, but only
  when handed an `Authorization`. It never decides authorization itself. The CLI does that.
- `internal/app.Verify` picks the session and incident, chooses checks from the evidence, and, for a live session,
  reads the events recorded after its starting point. The running `watch` process is the only writer; verify only
  reads the database (WAL) and the live status file.
- Reports are stored in `verifications` (migration 003, `ON DELETE CASCADE` with the session) and listed by
  `timeline`.

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

Phase 3 adds `modernc.org/sqlite` (spec-mandated; pure Go, so cross-compilation needs no cgo) and its
transitive modules (libc, memory, mathutil, …). No new modules in Phase 2. Windows Job Objects are called through `syscall.NewLazyDLL` rather than adding
`golang.org/x/sys`.

Dev-only, not in `go.mod`: staticcheck, run with `go run honnef.co/go/tools/cmd/staticcheck@2025.1.1`.

## Deviations from the spec

- The `--session` global flag arrived with sessions (Phase 3), so no flag ever silently did nothing.
- Event source type is named `api.EventSource` because `api.Source` already names discovery sources.
- One event type was added to the spec's initial set: `probe_ready` (first passing probe), which timelines need
  to anchor "last known healthy".
- Rule `K004-startup-failure` is separate from the spec's `K003-crash-after-start`, so "never became healthy" and
  "crashed after being healthy" stay distinct claims. `K005` (dependency not running) and `K006` (missing module)
  were added because they are common, cheap to detect with evidence, and come with an obvious next step.
- The supervisor lives in `internal/process`, as the spec's package map suggests, next to the
  process-group code it drives.
- `config show --format json` emits the config document itself (`internal/config` types with JSON tags). Its
  schema is `docs/kestrel.schema.json`, not a `pkg/api` type.
