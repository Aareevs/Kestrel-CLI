# Testing

```sh
make check     # gofmt, vet, tests, race detector, build, cross-compiles (what CI runs)
make lint      # staticcheck (via go run; nothing installed globally)
```

## Layout

- Unit tests sit next to the code (`internal/*/..._test.go`).
- `tests/fixtures/projects/<name>/`: sample projects that discovery scans. Kestrel only reads them. Go fixtures
  have their own `go.mod`, so `go build ./...` and `go vet ./...` in this repo skip them.
- `tests/integration/`: runs the CLI in-process (`cli.Execute` with injected streams, cwd, and env) and compares
  output with goldens in `tests/integration/testdata/`.

## Golden files

Golden output replaces the absolute fixture path with `<root>`. After an intentional output change:

```sh
make golden          # go test ./tests/integration -update
git diff tests/integration/testdata
```

Review the diff line by line. A golden only proves output is stable. It doesn't prove the output is right.

## Adding a discovery fixture

1. Create `tests/fixtures/projects/<name>/` with the smallest realistic set of manifests.
2. Add `<name>` to `fixtures` in `tests/integration/cli_test.go`.
3. Run `make golden` and check every command, port, line number, and dependency in the new goldens against the
   fixture files.

For edge cases (limits, symlinks, ignore rules) use temp-dir unit tests in `internal/discovery`, not fixtures.
Committed fixtures can't contain `node_modules`, gitignored paths, or symlinks reliably.

## fakesvc

`tests/fakesvc` is a small service whose behavior is set entirely by flags. Tests build it with `go build`.

```sh
go run ./tests/fakesvc --help
fakesvc --listen 127.0.0.1:8080                 # serve /health
fakesvc --connect 127.0.0.1:5432 --style node    # log ECONNREFUSED in Node's format
fakesvc --exit-after 2s --exit-code 1 --exit-message "FATAL: …"
fakesvc --flood-bytes-per-min 52428800            # 50 MB/min of info lines
fakesvc --children 2 --ignore-term --pidfile p    # process tree that resists SIGTERM
fakesvc --secrets / --hostile                     # redaction and inert-data checks
fakesvc --recover-file f                          # /health returns 503 until f exists (and again if f is removed)
fakesvc --sleep                                    # do nothing, forever (used by --children)
```

## Supervision tests

- `internal/process`: process groups (grandchildren, SIGKILL escalation, exit classification) and the
  supervisor (dependency order, readiness waits, crashes, auto-probe, external services, no orphans).
- `tests/integration/supervise_test.go` drives the real `kestrel` binary. It sends a terminal-style Ctrl-C
  (SIGINT to Kestrel's process group) and checks that every PID the services recorded is gone. Both orphan tests
  first assert that every recorded PID is really running (via `ps`, since a zombie still answers `kill(pid, 0)`),
  so "no orphans" can't pass vacuously. The flood test
  samples RSS for 10s by default:

  ```sh
  KESTREL_FLOOD_SECONDS=60 go test -run TestFlood -v ./tests/integration
  ```

  `go test -short` skips the flood test.

## Storage tests

- `internal/storage`: migrations (applied once, failure rolls back and is retried, newer database refused),
  round trip, segment rotation + compression, full-text export, delete, retention by age and exact size order,
  stale-session recovery, corruption, write failure, unwritable directory, size-accounting identity.
- `tests/integration/sessions_test.go` (real binary): persist → list → show → export → delete; a byte scan of
  the whole data directory for secrets; retention via `cleanup` and on session close (sessions are back-dated by
  editing the database, the way time would age them); active sessions; `kill -9` recovery; corrupt and
  unwritable storage; flood size accounting.

To check a change to storage accounting, run the flood test verbosely and compare the numbers it logs:

```sh
go test -v -run TestFloodIsPersisted ./tests/integration
```

## Scenario tests

`tests/integration/scenarios_test.go` runs each scenario as a real `fakesvc` stack under `kestrel watch`, stops it
when the scenario's defining event arrives on the NDJSON stream (not after a fixed sleep), and compares
`diagnose`/`timeline` with goldens in `testdata/scenarios/`. The normalizer masks only what varies between runs:
times, IDs, PIDs, ports, durations, and repeat counts. Each scenario also asserts its key facts in code (which rule
leads, its confidence, the status, the exit code), and checks that every cited event ID exists in the session.

| Scenario | Expectation |
|---|---|
| A database startup failure | database is the high-confidence initiating failure; api and frontend are downstream symptoms |
| B port conflict | `K001` with an `lsof` next step; the process holding the port is untouched |
| C crash after healthy start | `K003`, with observed facts and the hypothesis stated separately |
| D noisy but healthy | no incident, exit 0 |
| I ambiguous simultaneous failures | unresolved, competing hypotheses, exit 3 |

`fakesvc` is symlinked, not copied, into each scenario project: macOS scans every fresh binary on first exec, which
made parallel runs flaky.

**Scenario E (recovery)** is `TestScenarioERecovery` in `verify_test.go`. Under a real `kestrel watch`, an api
fails its startup probe, then the test:

1. verifies while it's broken (`still_failing`, exit 1, compared live);
2. fixes it a third of the way into a window (the probe is `flaky`, and the verdict is not `resolved`);
3. waits for the recorded `probe_recovered` event;
4. verifies across a 2s window (`resolved`, exit 0, 9/9 samples, not before 2s have passed, recovery evidence is
   that exact event);
5. breaks it again mid-window (not `resolved`; the relapse is cited from the live session);
6. checks that every cited event exists in the session, and that `timeline` lists all four verifications.

The exec policy is tested from both sides: with no terminal and no flag, a command that would create a file
doesn't run (the test was checked against a deliberately broken authorizer, and it catches it); with `--allow-exec`,
or a `y` at the prompt, it does.

**Scenarios F, G, H and J** are in `hardening_test.go`. Each one checks every guarantee the spec states for it,
end to end:

| Scenario | What the test proves |
|---|---|
| F secrets | No fake secret in: the text terminal, the JSON stream, every stored byte (SQLite, WAL, decompressed segments), `sessions show/export`, `diagnose`, `timeline`, `verify` (text and JSON), or any MCP tool. Placeholders are present in each, so the secrets did reach every one. |
| G interrupt | Ctrl-C exits 0. Every PID (children included, checked live first) is gone, and each service's stop is recorded. The session is closed with no write errors, **every event that was streamed is in storage**, and every segment is sealed. |
| H log flood | 50 MB/min with a slow reader: RSS stays under 100 MiB, `collector_overload` is emitted, and with `max_total_mb: 2` retention removes both older sessions while keeping the one being closed. |
| J hostile content | Canary lines (`$(touch …)`, backticks, `; touch …`) never create their files, through recording, `diagnose`, `timeline`, `verify --allow-exec`, or MCP. The diagnosis and exit code are identical to the same run without hostile lines. The lines are stored verbatim as data, with secrets redacted and terminal escapes stripped everywhere. |

`TestInstallScript` builds a release directory the way GoReleaser lays it out and runs `scripts/install.sh`
against it via `file://`. It checks the happy path, that a tampered archive is refused (and nothing installed),
and that a missing version fails cleanly.

## MCP tests

`tests/integration/mcp_test.go` connects the official Go SDK client to the real `kestrel mcp` binary over stdio
(`TestMCPRealDiagnosisAndRedaction`, the Phase 6 exit criterion). It checks:

- **Tools:** exactly the eight documented tools exist, each read-only, closed-world, and with an output schema.
- **Real diagnosis:** `get_diagnosis` returns the same incidents, rules, confidences, and leading evidence IDs as
  `kestrel diagnose --format json`.
- **Every tool works** on a recorded session that contains fake secrets and hostile lines.
- **Redaction:** no fake secret appears anywhere in what the client received, and the hostile lines arrive as
  inert text.
- **Errors:** they come back as tool errors, and an unknown tool name is refused.

`TestMCPRedactsWithCurrentProjectPatterns` adds a redaction pattern after the session was recorded, and asserts
the token is still stored but never reaches the client. Disabling the server's outbound redaction makes it fail.
`TestMCPInProcess` serves the CLI's `mcp` command over pipes, for coverage.

