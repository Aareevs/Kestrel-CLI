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
fakesvc --recover-file f                          # /health returns 503 until f exists
```

## Supervision tests

- `internal/process`: process groups (grandchildren, SIGKILL escalation, exit classification) and the
  supervisor (dependency order, readiness waits, crashes, auto-probe, external services, no orphans).
- `tests/integration/supervise_test.go` drives the real `kestrel` binary. It sends a terminal-style Ctrl-C
  (SIGINT to Kestrel's process group) and checks that every PID the services recorded is gone. The flood test
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

Scenarios A–J from the build spec are built on `fakesvc` as their phases arrive: A–D and I with diagnosis
(Phase 4), E with verification (Phase 5), F–H and J in hardening (Phase 7). Phases 2–3 already cover parts of F
(redaction in terminal and JSON output, the database, WAL, segments, and export), G (interrupt, no orphans,
storage flushed), H (bounded memory, overload events, retention) and J (hostile lines are inert).
