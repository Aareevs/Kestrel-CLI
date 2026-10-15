# Progress

## Follow-up: Windows test harness and CI runner (2026-10-15)

Windows supervision (`proc_windows.go`) was implemented with Job Objects and console break events, but was untested because integration test suites carried `//go:build !windows` and CI only ran on Ubuntu and macOS.

### Built

- **Command normalization:** `cmd.exe /S /C` command parser in `proc_windows.go` normalizes leading `./path/...` and `"./path/..."` into `.\path\...`, preventing `cmd.exe` from interpreting `/` as a command switch delimiter.
- **Cross-platform integration test harness:** Extracted process management, event streaming, and binary compilation from `supervise_test.go` into `harness_test.go`, with platform-specific process group controls in `harness_unix_test.go` and `harness_windows_test.go`.
- **Windows process monitoring & memory:** Pure-Go process resident set size accounting implemented via `psapi.dll` (`GetProcessMemoryInfo`), eliminating the Unix `ps` dependency.
- **Windows Job Object tests:** `internal/process/proc_windows_test.go` exercises Job Object creation, process group containment, exit classification, pipe capture, and cleanup.
- **Non-admin file copy fallback:** Integration scenarios copy test binaries instead of relying on `os.Symlink`, which requires elevated privileges or developer mode on Windows.
- **Signal handling in test fixtures:** `tests/fakesvc/main.go` ignores `os.Interrupt` in addition to `syscall.SIGTERM` when `--ignore-term` is passed.
- **CI matrix:** Added `windows-latest` to `.github/workflows/ci.yml` running `gofmt`, `go vet`, `go test ./...`, and `go build ./cmd/kestrel` via Git Bash.

### Quality checks (run on darwin/arm64 host)

| Check | Result |
|---|---|
| `gofmt -l .` | no output |
| `go vet ./...` | pass, plus `GOOS=windows go vet ./...` across all internal and test packages |
| `go test ./...` | pass (macOS) |
| `go test -race ./...` | pass (macOS) |
| Windows compilation: `GOOS=windows go test -c` for `internal/process` and `tests/integration` | pass |
| `staticcheck@2025.1.1` | pass |

### Exit criteria & verification

> All integration test packages compile for `GOOS=windows`; local tests pass on Unix without regression; GitHub Actions `windows-latest` job added to validate against real Windows kernel.

- Local quality gates (`make check`, `make lint`) pass 100% on darwin/arm64.
- Cross-compilation of test suites for `GOOS=windows` passes without build tag exclusion.
- Real execution verification on Windows kernel deferred to the CI run triggered by opening this PR.

### Known gaps and limitations

- **No `-race` on Windows CI:** The Go race detector on Windows requires MinGW (gcc) and CGO. Since Kestrel compiles with `CGO_ENABLED=0` (pure-Go SQLite via modernc.org), the race detector step in CI is constrained to `ubuntu-latest` and `macos-latest`.
- **`install.sh` on Windows:** The shell installer requires POSIX `sh` and `curl`. On Windows, users download the standalone `.zip` archive from GitHub releases or run via Go.

## Follow-up: Homebrew formula instead of a cask (2026-10-15). Not released.

- **What changed:** the tap now gets a standard **formula** (`Formula/kestrel.rb`), which works on macOS and
  Linux and has the same shape as `Octo-sh/homebrew-tap`. GoReleaser no longer handles Homebrew. Its formula
  option (`brews`) is deprecated and makes `goreleaser check` fail.
- **How it's built:** `scripts/homebrew-formula.sh` renders the formula from a release's `checksums.txt`.
- **When it's pushed:** `.github/workflows/homebrew.yml` pushes it to `Aareevs/homebrew-tap` only when a release is
  **published**, so the formula never points at downloads that don't exist yet.
- **Checked:** `TestHomebrewFormula` checks every platform's URL and checksum, valid Ruby (`ruby -c`), and that a
  missing archive fails. That last check found a real bug: inside the template, a failed lookup would have left
  an empty checksum instead of stopping the script. Fixed. The formula rendered from a real snapshot is valid
  Ruby, and its checksums match `checksums.txt`. `goreleaser check` and `release --snapshot --clean` pass.
- **Install command unchanged:** `brew install aareevs/tap/kestrel`. Nothing was tagged, released, or pushed to a
  tap.

## Phase 7: Hardening and release tooling (2026-10-15). Not released.

**No release has been made.** You asked for none yet. Nothing was tagged, no GitHub release or draft was
created, no tap repository was created, and nothing was published. The release tooling is built and tested
locally only.

### Built

- **Scenarios F, G, H, J** (`tests/integration/hardening_test.go`): one end-to-end test each, covering every
  guarantee the spec states for that scenario (details in [testing.md](testing.md#scenario-tests)).
- **GoReleaser** (`.goreleaser.yaml`, v2.18.2 pinned):
  - five static binaries, tar.gz/zip archives, and SHA-256 checksums;
  - a Homebrew cask for `Aareevs/homebrew-tap`;
  - **draft** GitHub releases only.
  - `make release-check` / `make release-snapshot` run it via `go run`. CI validates the config.
- **Release workflow** (`.github/workflows/release.yml`): runs only on a pushed `v*` tag (none exists). It runs
  the race tests, then GoReleaser, which produces a draft.
- **Install script** (`scripts/install.sh`, POSIX sh): detects platform, downloads, verifies SHA-256 (installs
  nothing on a mismatch), installs to `~/.local/bin`, never uses sudo, never edits profiles.
- **Docs and README:** [docs/releasing.md](releasing.md) covers the dry run, the one-time setup before the first
  release, and cutting a release. The README now has install paths labeled "today" and "from the first release",
  plus Platform Support and Troubleshooting. `docs/assets/demo.tape` (VHS) and a demo stack are included; no GIF
  is committed because VHS isn't installed here, and the spec forbids a fake one.

### Exit criteria

> `goreleaser release --snapshot --clean` succeeds; all quality checks green; README reflects only real behavior.

- **Snapshot:** `goreleaser release --snapshot --clean` succeeded. It built the 5 binaries, 5 archives,
  `checksums.txt`, and the cask in `dist/`. Checked: the archive holds `kestrel`, `LICENSE`, and `README.md`; the
  Linux binary is static; `kestrel version` reports the snapshot version; the cask's checksums match. The
  installer, run against those artifacts, installed and ran the binary, and refused a tampered archive.
- **Quality checks:** all green (table below).
- **README:** every command and output shown exists and works. The two things that don't work yet (the one-line
  installer and Homebrew) are labeled "from the first release".

Stability: the full integration suite passed 9 of 9 runs, 3 of them under 8 CPU hogs.

### Bug found while building this (fixed in this PR)

- **Terminal escape sequences in a command line reached the terminal and storage** (Phase 2). Output lines were
  always cleaned, but supervisor messages, such as `started (pid N): <command>`, were only redacted. A command
  containing escape sequences could therefore drive the terminal, and was stored with them. `Collector.Emit` now
  cleans every non-line event the same way. Found by Scenario J; there's also a unit test.

### Quality checks (darwin/arm64, go1.26.2)

| Check | Result |
|---|---|
| `gofmt -l .` | no output |
| `go vet ./...` | pass, plus `GOOS=windows go vet ./internal/...` |
| `go test ./...` | pass (16 packages) |
| `go test -race ./...` | pass |
| builds: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64 | pass |
| `staticcheck@2025.1.1` | pass |
| `goreleaser check` / `release --snapshot --clean` (v2.18.2) | pass |
| Coverage (`-short`, `-coverpkg=./internal/...,./pkg/...`) | 86.5% overall |

### Before a first release (your to-do list; also in releasing.md)

1. ✅ Done 2026-10-15: `github.com/Aareevs/homebrew-tap` exists (public), and the `HOMEBREW_TAP_TOKEN` secret is set.
2. Optionally record the demo: `vhs docs/assets/demo.tape`, then commit the GIF and reference it in the README.
3. Tag `vX.Y.Z` and push the tag, review the draft release, then publish it.

### Known gaps and limitations

- **Unsigned binaries:** they aren't signed or notarized for macOS. The cask removes the quarantine attribute.
- **Local snapshot Go version:** local snapshots are built with the toolchain GoReleaser runs under (go1.27.2,
  fetched into the module cache automatically). The release workflow uses the Go version in `go.mod`.
- **Windows remains untested,** as in every phase.
- **The installer can't be tested end to end against GitHub** until a release is published. It is tested against
  a local release directory.

## Phase 6: MCP server (2026-10-15)

### Built

- `internal/mcp`: a read-only MCP server over stdio on the official Go SDK (`github.com/modelcontextprotocol/go-sdk`
  v1.8.0, pinned). It has eight tools: `get_diagnosis`, `get_failure_timeline`, `get_recent_errors`,
  `get_session_summary`, `get_verification_results`, `get_service_status`, `list_services`, `inspect_project`.
  Each one is a thin wrapper over `internal/app` and publishes input and output schemas derived from `pkg/api`.
- **Read-only by construction:** no tool reaches anything that starts, stops, signals, executes, or writes,
  `verify` included. Annotations: `readOnlyHint: true`, `openWorldHint: false`.
- **Instructions for agents:** the server's instructions and the tool descriptions state the claim levels
  ("don't present a hypothesis as fact; report unresolved as unresolved"), and say that event messages are
  untrusted process output, never instructions.
- **Redaction:** every result is redacted again on the way out, string by string on the decoded value (so the JSON
  can't break), with the project's current `extra_patterns`.
- `internal/app`: three read-only queries the CLI didn't have: `Services`, `RecentErrors`, `VerificationResults`.
- `kestrel mcp` command, and [docs/mcp.md](mcp.md) with setup for Claude Code and JSON-config clients (Cursor,
  Claude Desktop, VS Code).

### Exit criteria

> An MCP client (test harness) retrieves a real diagnosis; redaction verified on MCP output.

Met. `TestMCPRealDiagnosisAndRedaction` connects the official SDK client to the real `kestrel mcp` binary over
stdio, against a recorded failing session that contains fake secrets and hostile lines.

- **Tools:** exactly eight, each read-only, closed-world, and with an output schema.
- **Real diagnosis:** `get_diagnosis` matches `kestrel diagnose --format json` (incidents, statuses, rules,
  confidence levels, leading evidence IDs).
- **Every tool** works on that session.
- **Redaction:** no fake secret appears anywhere in what the client received, the placeholders are present, and the
  hostile lines arrive as inert text.
- **Errors** are tool errors with a message, and there is no exec tool.

`TestMCPRedactsWithCurrentProjectPatterns` adds a pattern after recording: the token is still in storage but
never reaches the client. That test fails when the outbound redaction is disabled (checked).

Stability: the full integration suite passed 9 of 9 runs, 3 of them under 8 CPU hogs.

### Quality checks (darwin/arm64, go1.26.2)

| Check | Result |
|---|---|
| `gofmt -l .` | no output |
| `go vet ./...` | pass, plus `GOOS=windows go vet ./internal/...` |
| `go test ./...` | pass (16 packages) |
| `go test -race ./...` | pass |
| builds: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64 | pass |
| `staticcheck@2025.1.1` | pass |
| Coverage (`-short`, `-coverpkg=./internal/...,./pkg/...`) | 86.5% overall |
| Binary size (darwin/arm64, `-s -w`) | 13.2 MiB, up from 11.4 MiB (the MCP SDK and its JSON Schema library) |

### Known gaps and limitations

- One project per server: add one server per project.
- The server ends when the client closes stdin, and requests still in flight at that moment are dropped (SDK
  behavior; real clients keep stdin open).
- The SDK links `golang.org/x/oauth2` for its HTTP transports. Kestrel uses only stdio and makes no network calls
  through it.
- `get_service_status` without a running session makes one connection attempt per configured probe, as
  `kestrel status` does. It is the only tool that touches anything outside Kestrel's data.
- Telling the agent that log text is untrusted lowers prompt-injection risk but can't eliminate it. The real
  guarantee is that there's nothing to misuse: no tool can act.

## Phase 5: Verification (2026-10-15)

### Built

- `internal/verification`: `Run` samples every check once per interval across the whole stability window (a pass
  on the first sample ends nothing), and each check ends `passed`, `flaky`, or `failed`. `Decide` turns that, plus
  any recurring errors, into `resolved`, `partially_resolved`, `still_failing`, or `inconclusive`, with reasons.
  `RunCommand` runs a configured command in its own process group, with a timeout and a redacted 20-line output
  tail, and only when it is handed an `Authorization`.
- `kestrel verify` (`internal/app/verify.go`, `internal/cli/verify.go`): verifies the latest incident of the
  latest session (or `[session] --incident N`). Checks come from the evidence: the affected services' health
  probes, the exact addresses they failed to connect to, process liveness from a live session, plus
  `verify.checks`. While `kestrel watch` is still recording, the session's new events are compared with the
  incident's (before/after). Exit codes: 0 resolved, 1 partially resolved or still failing, 3 inconclusive.
- **Exec authorization:** a command check runs only with `--allow-exec`, or after a prompt that shows the exact
  command (secrets masked), the directory, and the timeout gets an explicit `y`. No terminal means no prompt and
  no exec.
- Results are stored with their session (migration 003, deleted with it) and listed by `kestrel timeline`.
- Docs: [verification.md](verification.md), plus updates to the README, configuration, security (exec policy),
  architecture, events, and testing docs.

### Exit criteria

> Scenario E passes; `verify` refuses to exec without authorization (tested).

Met.

- **Scenario E** (`TestScenarioERecovery`, real `kestrel watch` and `fakesvc`):
  - **Broken:** `still_failing`, exit 1.
  - **Fix landing mid-window:** the probe is `flaky`, and the verdict is not `resolved`.
  - **Recovery:** recorded as `probe_recovered`.
  - **Stable for the full window:** `resolved`, exit 0, 9/9 samples, never before the 2s window has elapsed, and
    the recovery evidence is that exact event.
  - **Relapse mid-window:** not `resolved`, and the new failure is cited from the live session.
  - **Evidence:** every cited event exists in the session export.
- **Exec refused without authorization:**
  - `TestVerifyRefusesExecWithoutAuthorization`: with no terminal and no flag, a `touch` command does not run;
    with `--allow-exec`, it does. I checked that this test fails against a deliberately broken authorizer.
  - `TestVerifyPromptInProcess`: empty, `n`, `nope`, and EOF answers all decline, `y` runs it, and the prompt
    shows the command, the directory, and the timeout.
  - Timeout and redaction: a timed-out command's process is gone afterwards, and a secret in a failing command's
    output is redacted.

Stability: the full integration suite passed 9 of 9 runs, 3 of them under 8 CPU hogs. The scenarios alone passed
12 of 12, 6 under load.

### Bugs found while building this (fixed in this PR)

- **Two Kestrel processes opening a new data directory at the same moment broke storage for one of them** (Phase
  3). Both ran the migrations, and the loser failed with "table sessions already exists". `watch` then recorded
  *nothing* for its whole session, for example when started alongside `status` on first use. A second, rarer
  failure: SQLite answers `SQLITE_BUSY` at once, without waiting, to a connection made while another process
  switches the new file to WAL. Fixes: each migration takes the write lock first (`BEGIN IMMEDIATE`) and is
  re-checked under it, and Open retries a busy first connection for up to 5s. `TestConcurrentFirstOpen` (8
  concurrent opens × 5 rounds) failed on the old code and passed 150 rounds under `-race` and CPU load.
- **`/dev/null` counted as a terminal** (Phase 1). The check was "is a character device", which `/dev/null` is,
  so a script piping from `/dev/null` was shown a prompt (`sessions delete` too). It failed safe (EOF means no),
  but terminal detection now uses a real TTY check (`go-isatty`, already in the module graph).
- **`fakesvc --sleep` died instantly** ("all goroutines are asleep": `select {}` with nothing else running). Every
  child process in the Phase 2 orphan tests was therefore already dead, so for them "no orphans" proved nothing.
  Fixed, and both orphan tests now first assert that every recorded PID is really running. They use `ps`, since a
  zombie still answers `kill(pid, 0)`. With the old fakesvc, both tests now fail. Shutdown with a SIGTERM-ignoring
  grandchild really takes the grace period plus SIGKILL (about 0.5s).
- **Two Phase 4 scenario flakes**, which showed up in repeated full-suite runs:
  - **A:** the golden assumed an event order that depended on machine speed (the database's probe timeout vs.
    api's first refused connection). The timeout is now 2s, the test waits for it explicitly, and the order
    follows from causality.
  - **B:** the port the test holds was chosen by opening and closing a listener, and another parallel test could
    take it in between. The test now opens its listener on port 0 first.

### Quality checks (darwin/arm64, go1.26.2)

| Check | Result |
|---|---|
| `gofmt -l .` | no output |
| `go vet ./...` | pass, plus `GOOS=windows go vet ./internal/...` |
| `go test ./...` | pass (15 packages) |
| `go test -race ./...` | pass |
| builds: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64 | pass |
| `staticcheck@2025.1.1` | pass |
| Coverage (`-short`, `-coverpkg=./internal/...,./pkg/...`) | 86.8% overall; verification 97.8%, `app/verify.go` 81.4% |

### Known gaps and limitations

- **Repeat folding vs. short windows:** an error that repeats unchanged is recorded as one summary at least every
  10s, so a window shorter than 10s may not see it recur. Verify notes this. The default 30s window always does.
- **Comparing events needs a live session:** after `watch` has stopped, verify can only probe and run commands,
  and it says so.
- **Skipped commands:** a skipped command check is listed under "not run" but doesn't prevent `resolved`.
- **Windows is untested:** command checks there (`cmd.exe /C`, Job Object stop on timeout) are built but not run,
  like the rest of Windows supervision.
- **No `verification_result` events:** verify doesn't write events into the session, because only the recording
  process assigns `seq`. Results are reports kept alongside the session.
- **Liveness depends on the status file:** liveness checks read the running session's status file, which is
  refreshed every 250ms.

## Phase 4: Correlation and diagnosis (2026-10-15)

### Built

- `internal/graph`: the dependency graph. Declared edges come from `depends_on`. Inferred edges come only from
  connection errors aimed at a port exactly one service owns, and carry the event IDs. A contested port has no
  owner, and simultaneous failures never create an edge (tested).
- `internal/correlation`: anomalies grouped into incidents within `correlation.window`. Each link is strong (same
  service, dependency path, port) or weak (time only), and correlation strength is reported separately from causal
  confidence. The initiating failure is the one unexplained member; when there are several, that is said
  explicitly. Timelines carry last known state, first anomaly, key events, downstream symptoms (claim:
  correlated), and recovery, with repeats collapsed.
- `internal/diagnosis`: a rule engine and six documented rules (`K001` port conflict, `K002` dependency down, `K003`
  crash after healthy start, `K004` startup failure, `K005` dependency not running, `K006` missing module). Every
  hypothesis carries supporting and contradicting event IDs, a confidence level plus the factors behind it, and one
  next step (a command for you; never run). Incidents are `unresolved` when no single failure stands out.
- Session topology is recorded with each session (migration 002, an in-place upgrade tested on a v1 database
  with data), so any stored session can be diagnosed later.
- Commands: `kestrel diagnose` (exit 0 / 1 / 3) and `kestrel timeline` (`--service`, `--since`, `-n`). Both work on
  a live session, and both support `--format json` and `--all`.

### Exit criteria

> Scenarios A, B, C, D, I pass with golden outputs; no hypothesis lacks evidence IDs.

Met. `TestScenarios` runs each scenario as a real `fakesvc` stack under `kestrel watch`, then compares the
normalized `diagnose` and `timeline` output with goldens I reviewed line by line, and asserts the key facts in code:

| Scenario | Result |
|---|---|
| A database startup failure | `K002`, high: "database failed first; api, frontend failed because of it". Symptoms: api, frontend. Exit 1 |
| B port conflict | `K001`, high, `lsof` next step; the test's own listener on that port is verified untouched. Exit 1 |
| C crash after healthy start | `K003`, high, from the logged panic; the explanation separates "Observed:" from "Hypothesis:". Exit 1 |
| D noisy but healthy | no incident. Exit 0 |
| I ambiguous simultaneous failures | unresolved, two competing high-confidence hypotheses about different services, weak correlation. Exit 3 |

No hypothesis lacks evidence, by two independent mechanisms. The engine drops any hypothesis that cites no events,
which is tested with a deliberately misbehaving rule. And every scenario checks that each cited event ID (evidence,
initiating event, symptoms, graph edges) exists in the session's export. Stability: 11 consecutive runs, 3 of them
under 8 CPU hogs.

### Bugs found while building this (fixed in this PR)

- **Node connection errors lost their target** (a Phase 2 pattern bug). RE2 takes the leftmost match, so for
  `Error: connect ECONNREFUSED 127.0.0.1:8080` a target-less alternative won. Diagnosis then printed "dependency at
  (blank) isn't running".
- **Repeat counts were one too high** everywhere: a run of 29 lines showed as 30. A repeat summary stands for
  exactly its folded lines. There is now one definition (`Event.Lines()`), pinned to ground truth by a collector
  test.
- **Repeat summaries appeared at the end of timelines** (they're recorded when the run ends). They now merge into
  their run.
- **A crash could be recorded before the line that explained it** (the output pipe and `wait()` race). The
  supervisor now drains output, bounded at 2s, before recording an exit. The test was first written in a way that
  could not fail, which I caught by reverting the fix; it was redesigned to reproduce the race (fails 3/3 without
  the fix, passes with it, including under `-race`).
- **K002 lowered its own confidence for transitive dependents**, and ranked a downstream "api failed first" above
  the real root. Chains are now followed explicitly, and a "root" that itself depends on an earlier failure is
  demoted.
- **A service's first-ever readiness was labeled "recovery"** when it happened to follow a connection error.
- **Test-harness flakiness** from copying an 8 MB binary into five projects at once (macOS scans fresh binaries). It
  is symlinked now, and scenarios stop on events rather than sleeps.

### Quality checks (darwin/arm64, go1.26.2)

| Check | Result |
|---|---|
| `gofmt -l .` | no output |
| `go vet ./...` | pass, plus `GOOS=windows go vet ./internal/...` |
| `go test ./...` | pass (14 packages) |
| `go test -race ./...` | pass |
| builds: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64 | pass |
| `staticcheck@2025.1.1` | pass |
| Coverage (`-short`, `-coverpkg=./internal/...,./pkg/...`) | 86.5% overall; graph 89.7%, correlation 95.2%, diagnosis + rules 92.9% |

### Known gaps and limitations

- Incidents group by time within the window, so in a long session with continuous unrelated errors one large weak
  incident can form. It is labeled `weak`, and no causal claim is made from timing.
- Port ownership comes from probe ports and "listening on" output. A service that listens without saying so and
  without a probe can't be linked by port evidence; declared `depends_on` still works.
- A process that dies while a grandchild holds its pipe has its exit recorded after up to 2s.
- Confidence levels come from fixed, documented rules, not from learned weights. They are deliberately coarse.
- `diagnose` reads only diagnostic events. Plain info lines are available through `timeline --service` but are not
  used as evidence (except as context before a crash).

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
