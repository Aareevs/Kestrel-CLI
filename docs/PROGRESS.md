# Progress

## Phase 3: Persistence (2026-10-15)

### Built

- `internal/storage`: SQLite via `modernc.org/sqlite` (pure Go) in WAL mode, with embedded transactional
  migrations (`schema_migrations`), a sessions/events/segments schema indexed on `(session_id, seq)`,
  `(session_id, service, time)` and `(session_id, type)`, and a read-only `Inspect` for `doctor` that never
  creates files.
- Raw logs: per-service append-only NDJSON segments, rotated at 4 MiB, gzipped when closed, indexed by seq range
  and byte offset. SQLite keeps messages truncated to 1 KiB; the full redacted event is in the segment.
- `Recorder`: batched transactions (256 events / 100ms). Storage failures are counted on the session as
  `write_errors` and never stop supervision.
- Retention (`retention_days`, `max_total_mb`) on session end and via `kestrel cleanup [--dry-run]`: oldest closed
  sessions first, never active ones. Stale-session recovery (`interrupted`) after a killed Kestrel.
- Commands: `sessions list|show|export|delete`, `cleanup`, global `--session`; `doctor` now checks storage.
  `run`/`watch` print "saved as session …" and warn if storage is unavailable.
- Redacted export: streamed, always-valid JSON, full messages restored from segments, second redaction pass.

### Exit criteria

> A session is persisted, listed, shown, exported (redacted), and deleted; retention test passes.

Met, against the real binary (`tests/integration/sessions_test.go`):

- `TestSessionPersistListShowExportDelete` runs the whole lifecycle. It also scans **every stored byte** (SQLite,
  WAL, and decompressed segments) for the four fake secrets: none found, while the hostile lines are stored
  verbatim as inert text. The export is checked for secrets, ordering, and completeness (`events` equals the
  session's count, no warnings).
- `TestRetentionOnCleanupAndSessionClose`: dry run changes nothing; `cleanup` removes the two sessions past the
  window and keeps the third; `retention_days` from `kestrel.yaml` is honored; the same policy runs
  automatically when a session ends. Exact size-order behavior, the active/protected rules, and "oldest first"
  are in `internal/storage` (`TestRetentionSizeOrder`).

Also tested: migration failure/rollback, a newer database is refused, permission-denied data directory, corrupt
database (supervision unaffected, file untouched, `doctor` explains), `kill -9` recovery, write failures, partial
last line in a segment, path traversal in the index.

### Measurements

- **Size accounting.** A 50 MB/min flood session was reported at 14.2 MiB but occupied 20.3 MiB on disk
  (ratio 0.70). The per-row constant was recalibrated (200 → 330 bytes); re-measured twice at **0.98**. A test now
  fails if the ratio leaves 0.85–1.15.
- **Recorder throughput.** The flood (≈0.8 MB/s of lines) recorded 23k–45k events in 6–8s with a fast reader:
  0 dropped, 0 write errors, 10–20 MiB on disk.
- **Memory with recording on.** The 60s, 50 MB/min, slow-reader test: RSS plateaus at **65 MiB** (max 65.0,
  avg 60.1; trajectory 60 → 61 → 65 → 65 → … → 59 → 61 → 61 MiB, flat from ~15s). Phase 2 measured 47 MiB without
  recording, so persistence costs about 18 MiB (SQLite page cache and the recorder queue). Still bounded; the test
  limit is 100 MiB.

### Bugs found by the new tests (fixed in this PR)

- **Project identity split by symlinks.** `--project /var/...` and a cwd-based lookup (`/private/var/...` on
  macOS) gave one project two roots, so its sessions were invisible from one of them. The root is now
  canonicalized in `app.New`.
- **Sizes undercounted by 30%** (above).
- **ID/prefix lookups ignored project scoping** while `last` honored it, contradicting the documented rule. All
  forms are now scoped; `--all` widens them.
- **A silently skipping test.** One retention test skipped half its body when its fixture was too small to
  matter. The skip was removed and the logic moved to an exact-bytes test.
- **A wrong assertion of mine** (session size "below raw segment bytes") replaced by an exact identity: size
  equals sealed segment bytes plus the row estimate.
- **A timing assumption** in a Phase 2 supervisor test (3s startup grace) failed with the full suite running in
  parallel under load. It now uses 15s and passed 12× under 8 CPU hogs.

### Quality checks (darwin/arm64, go1.26.2)

| Check | Result |
|---|---|
| `gofmt -l .` | no output |
| `go vet ./...` | pass, plus `GOOS=windows` vet of `internal/process` and `internal/storage` |
| `go test ./...` | pass (11 packages) |
| `go test -race ./...` | pass |
| builds: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64 | pass (SQLite is pure Go, no cgo) |
| `staticcheck@2025.1.1` | pass (it caught one unused test helper) |
| Coverage (`-short`, `-coverpkg=./internal/...,./pkg/...`) | 84.9% of statements |

### Known gaps and limitations

- Stored data is **not encrypted**; it's protected by file permissions (`0700`/`0600`). Redaction is best-effort,
  so review exports before sharing.
- Segments are flushed to the OS but not `fsync`ed; a power loss can lose the last moments of a session.
- Stale-session detection uses the recorded PID. If the OS reuses it for an unrelated process, that session stays
  `active` (and is never removed) until that process exits.
- Export holds the full text of long (>1 KiB) messages in memory while it streams; fine at these limits.
- Windows storage and supervision compile and vet but have not been run on Windows.

## Phase 2: Supervision (2026-10-15)

### Built

- `internal/events`: typed events (`pkg/api/events.go`), monotonic ULIDs, message fingerprints, level
  detection (logfmt, JSON incl. pino numeric levels, bracket tokens), and a data-driven pattern library of 15
  patterns. Each pattern carries match/no-match evidence enforced by tests, plus a test that no example is
  shadowed by an earlier pattern and one that `docs/events.md` documents every ID.
- `internal/privacy`: best-effort redaction (private keys incl. multi-line blocks, JWTs, URL credentials,
  provider key prefixes, bearer/basic auth, auth/cookie headers, secret-named pairs, `extra_patterns`). It runs
  on every line before it's printed or emitted.
- `internal/collectors`: long-line-safe framing, ANSI/control-character stripping, repeat folding, and a
  bounded buffer (10,000 events / ~8 MiB) that drops only debug/info lines, never errors or lifecycle events,
  blocks the producer when it must, and reports every drop as a `collector_overload` event.
- `internal/health`: HTTP/TCP/process probes and a readiness state machine (startup grace, retries;
  `probe_ready`, `probe_failed`, `probe_recovered`, `startup_timeout`).
- `internal/process`: process-group isolation (Unix `Setpgid`, Linux `Pdeathsig`, Windows Job Objects),
  SIGTERM → grace → SIGKILL to the whole group, and a supervisor with dependency ordering, bounded readiness
  waits, auto-probing of announced ports, probe-only external services, and parallel shutdown.
- `tests/fakesvc`: scripted service (listen/bind-fail, connect-and-refuse, scheduled lines, exit codes,
  floods, fake secrets, hostile text, children, SIGTERM-ignoring, recovery file).
- Commands: `run` (zero-config, `--svc`, `--name`, `--grace`), `watch [svc...]`, `status`; live text
  renderer and NDJSON event stream; a second Ctrl-C force-quits.

### Exit criteria

> `watch` runs 3 `fakesvc` instances; Ctrl-C leaves zero child processes (tested).

Met. `TestWatchCtrlCLeavesNoOrphans` runs the real binary with three services. Each spawns children, and one
ignores SIGTERM. The test sends SIGINT to Kestrel's process group, as a terminal Ctrl-C does, then checks that
every one of the 6 recorded PIDs is gone and that each service logged exactly one requested exit. Shutdown
took about 510ms: the 500ms grace for the SIGTERM-ignoring service, then SIGKILL.

> A 50 MB/min log flood keeps RSS bounded (measured, number recorded here).

Met. `TestFloodKeepsMemoryBounded` floods at 50 MB/min while the reader of Kestrel's output deliberately reads
slowly (~64 KiB every 200ms), so the drop path is exercised. RSS sampled every 500ms on darwin/arm64:

| Run | RSS first | RSS max | RSS avg | RSS last | Trajectory (MiB, every ~5s) |
|---|---|---|---|---|---|
| 60s (`KESTREL_FLOOD_SECONDS=60`) | 13.5 MiB | **46.8 MiB** | 44.5 MiB | 39.9 MiB | 14 → 44 → 44 → 44 → 47 → 47 → 47 → 47 → 47 → 47 → 47 → 47 |
| 60s, earlier run | 13.5 MiB | 50.5 MiB | 44.3 MiB | 46.3 MiB | — |
| 10s (default in `go test`) | 20.2 MiB | 50.0 MiB | 43.4 MiB | 50.0 MiB | — |

It plateaus after about 20s. `collector_overload` events were observed in every run, and the test requires
them. Its limit is 100 MiB.

### Bugs found by the new tests (fixed before merge)

- **Event ordering.** With concurrent streams, an event stamped first could be buffered second, so `seq` went
  backwards. The flood test caught this intermittently. `seq` is now assigned at insertion, under the buffer
  lock.
- **Pattern shadowing.** `fatal error: runtime: out of memory` was claimed by the Go panic pattern. The
  shadowing test caught it and the pattern order was fixed.
- **`fakesvc --ignore-term`.** `signal.Notify` re-enabled SIGTERM, so the SIGKILL-escalation test passed for
  the wrong reason. Caught by asserting that shutdown takes at least the grace period.
- **Stale readiness.** A crashed service was reported `ready` from a probe taken before it crashed. A service
  Kestrel stopped is now `unknown` ("stopped by Kestrel"). One that crashed or exited on its own is
  `not_ready`.
- **Supervisor races.** These were found in review rather than by a failing test. The auto-probe context was
  read without a lock, a monitor could be added during shutdown, and exit status was read after its lock was
  released. All three are now guarded.

### Quality checks (darwin/arm64, go1.26.2)

| Check | Result |
|---|---|
| `gofmt -l .` | no output |
| `go vet ./...` | pass, plus `GOOS=windows`/`linux` vet of `internal/process` |
| `go test ./...` | pass (9 packages) |
| `go test -race ./...` | pass. The collector suite was also run 8× and health 20× to check for flakiness |
| `go build ./cmd/kestrel` + cross-builds linux/amd64, linux/arm64, darwin/amd64, windows/amd64 | pass |
| `staticcheck@2025.1.1` | pass |
| Coverage (`-short`, `-coverpkg=./internal/...,./pkg/...`) | 86.0% of statements |

### Known gaps and limitations

- Windows supervision (Job Objects, CTRL_BREAK) compiles and vets but has **not been run**: there's no Windows
  machine, and CI is macOS and Linux only.
- On macOS, `kill -9` of Kestrel itself can leave services running. Linux covers the group leader with
  `Pdeathsig`. Normal Ctrl-C/SIGTERM shutdown is tested and clean.
- Ordering across *different* streams of one process (stdout vs. stderr) is arrival order at Kestrel, which
  can differ slightly from the order the process wrote them. That's inherent to two pipes.
- `status` reads a small state file refreshed every 250ms. Session storage (Phase 3) is separate; the state file stays because `status` needs a cheap "what is running now" answer.
- No automatic restarts (`restart` events are reserved).
- Flood test RSS limits assume a non-race build of the binary, which is what the test builds.

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
- (Resolved after Phase 2: MIT license added.)
- `docs/assets/demo.gif` hasn't been generated (VHS isn't installed). Only the `.tape` is committed.
