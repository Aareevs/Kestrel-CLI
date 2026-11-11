# Events

Everything Kestrel observes becomes a typed **event**. Later stages (timeline, diagnosis, verification) work
only from events, so every claim they make can cite the IDs of the events behind it.

## Schema

`--format json` on `run` and `watch` streams events as NDJSON, one per line. Go type: [`api.Event`](../pkg/api/events.go).

| Field | Meaning |
|---|---|
| `id` | ULID: sortable, unique. |
| `session_id` | The `run`/`watch` session (also a ULID). |
| `seq` | Monotonic per session, assigned when the event enters the collector buffer. **The authoritative order.** Delivery order always equals `seq` order. |
| `time` | Wall-clock capture time. Informative, not authoritative. |
| `service` | Service name. |
| `pid` | Process the event came from, if any. |
| `type` | See below. |
| `severity` | `debug`, `info`, `warn`, `error`, `fatal`. |
| `message` | The line or description. **Already redacted.** |
| `source` | `stdout`, `stderr`, `probe`, `supervisor`, `kestrel`. |
| `attrs` | Typed optional attributes: `port`, `target`, `exit_code`, `signal`, `http_status`, `duration_ms`, `pattern_id`, `repeats`, `dropped`, `truncated`, `requested`. |
| `fingerprint` | Hash of the normalized message (numbers, timestamps, UUIDs, and hex IDs removed). Used for repetition detection. |

## Types

| Type | Emitted when |
|---|---|
| `process_started` | Kestrel started a service's process. |
| `process_exited` | It exited with code 0, or because Kestrel stopped it (`attrs.requested`). |
| `process_crashed` | Non-zero exit, a signal Kestrel didn't send, or a failure to start. |
| `log_line` | A line of output. `attrs.pattern_id` is set if a pattern recognized it. |
| `port_bind_failed` | Output shows the process couldn't bind its port. |
| `dependency_conn_failed` | Output shows a connection to a dependency failed (refused, timed out, DNS, auth). |
| `test_failed` | Output shows test failures. |
| `probe_ready` | First passing probe after start. |
| `probe_failed` | Probe failed `retries` times in a row after having passed. |
| `probe_recovered` | Probe passes again after `probe_failed`. |
| `startup_timeout` | Probe never passed: `retries` failures after `startup_grace`. |
| `collector_overload` | Debug/info lines were dropped under load. `attrs.dropped` counts them. |
| `restart`, `verification_result` | Reserved. `kestrel verify` runs as its own process and stores its results as reports kept with the session (see [verification.md](verification.md)), not as events: only the recording process assigns a session's `seq`. |

## Severity

A line's own declared level wins: logfmt `level=warn`, JSON `"level":"error"`, pino/bunyan numeric levels, a
leading `ERROR:`, or a bracketed token like `[WARN]` in the first 80 bytes. So
`WARN dial tcp …: connection refused, retrying` stays a warning even though it's a dependency failure. The one
exception is a fatal pattern (panic, out of memory), which always raises the severity. Lines with no declared
level default to `info`, or to the matching pattern's severity.

## Repetition

Consecutive lines on one stream with the same fingerprint are folded. The first line is emitted as is. Later
copies are counted and reported as one event with `attrs.repeats` and `attrs.duration_ms`. The repeat summary
is flushed when a different line arrives, when the stream goes quiet for 1s, or after 10s. Text output shows it
as `↻ ×13 more over 1.4s: <the line>`. Each stream folds separately, so the summary names its line.

## Backpressure

The collector buffer is bounded at 10,000 events and about 8 MiB. When it's full:

1. The oldest **debug/info log line** is dropped to make room.
2. If only protected events (warn and above, lifecycle, probe changes) are buffered, an arriving info line is
   dropped instead.
3. A protected event never gets dropped. The reader blocks, which stops draining the child's pipe, so the
   child's own writes block. Backpressure reaches the source instead of data being lost.

Every drop is counted on a `collector_overload` event placed in the stream where the drops began.

## Pattern library

Patterns are data in [`internal/events/patterns.go`](../internal/events/patterns.go). Each has an ID, a type, a
severity, a regex with named groups (`port*`, `target*`) that become attributes, and lines it must and must not
match. Tests enforce both, and also check that no example is claimed by an earlier pattern. IDs are stable
because diagnosis rules reference them.

**Adding a pattern.** Call `events.MustRegister(events.Pattern{…})` from an `init()` (the built-ins do the same).
`Register` checks the pattern's own evidence: an ID like `P015-short-name`, a description, a type and severity, named
groups limited to `port*` and `target*`, and that every `Match` line matches and no `NoMatch` line does. A
pattern that fails the check can't be registered, so the program (and its tests) won't start.

**Project patterns.** A project can add its own patterns in [`kestrel.yaml`](configuration.md#patterns), without
touching the code. They apply to that project's `run` and `watch` only, run after the built-ins, and carry the same
evidence (`match` / `no_match` lines) that `kestrel config validate` checks.

**Order.** The first match wins, so patterns are sorted by `Priority` (lower first). The built-ins are spaced 100
apart (100, 200, …), so a new pattern can slot between two of them by choosing a priority in between. A pattern with
no priority runs after all of them. Patterns with equal priority keep registration order, which follows file names
within the package, so give a distinct priority when order matters. The test suite also fails if an earlier
pattern claims one of your `Match` examples.

| ID | Type | Recognizes |
|---|---|---|
| `P025-dotnet-bind-failed` | `port_bind_failed` | ASP.NET Core Kestrel's "Failed to bind to address http://…:5000" (before `P001`, so the port is captured) |
| `P001-port-in-use` | `port_bind_failed` | `address already in use`, `EADDRINUSE`, "Port N is in use", Windows "Only one usage of each socket address" |
| `P002-bind-permission` | `port_bind_failed` | `listen EACCES`, `bind: permission denied` |
| `P003-postgres-unavailable` | `dependency_conn_failed` | libpq/psql and Ruby `PG::ConnectionBad` connection failures, "database system is starting up", missing database/role, auth failures |
| `P004-mysql-unavailable` | `dependency_conn_failed` | "Can't connect to MySQL server", `ER_ACCESS_DENIED_ERROR`, `ER_BAD_DB_ERROR` |
| `P005-redis-unavailable` | `dependency_conn_failed` | "Could not connect to Redis", `redis.exceptions.ConnectionError`, ioredis errors |
| `P006-connection-refused` | `dependency_conn_failed` | Go `dial tcp …: connection refused`, Node `ECONNREFUSED`, Ruby `Connection refused - connect(2) for … port N`, dev-server proxy errors |
| `P007-host-not-found` | `dependency_conn_failed` | `ENOTFOUND`, `no such host`, "Name or service not known", "could not translate host name" |
| `P008-connection-timeout` | `dependency_conn_failed` | `i/o timeout`, `ETIMEDOUT` |
| `P012-out-of-memory` | `log_line` (fatal) | V8 heap OOM, Go runtime OOM, Python `MemoryError`, Java `OutOfMemoryError`, PHP "Allowed memory size … exhausted" |
| `P009-go-panic` | `log_line` (fatal) | `panic:` / `fatal error:` at line start |
| `P010-node-uncaught` | `log_line` (fatal) | uncaught exceptions, unhandled promise rejections |
| `P011-python-traceback` | `log_line` (error) | `Traceback (most recent call last):` |
| `P013-module-not-found` | `log_line` (error) | missing Node/Python modules, missing Go packages, missing Java classes (`ClassNotFoundException`, `NoClassDefFoundError`, "Could not find or load main class", "Unable to access jarfile"), .NET assemblies and runtimes, Ruby gems and `LoadError`, PHP `vendor/autoload.php` and missing classes |
| `P014-test-failed` | `test_failed` | `go test`, Jest, pytest, Maven Surefire (`Tests run: …, Failures: 1`) and Gradle (`3 tests completed, 1 failed`, `> Task :test FAILED`), `dotnet test` (`Failed!  - Failed: 1`), RSpec and Minitest, and PHPUnit (`FAILURES!`) failure summaries |
| `P016-spring-port-in-use` | `port_bind_failed` | Spring Boot's "Web server failed to start. Port 8080 was already in use." |
| `P017-spring-startup-failed` | `log_line` (error) | `APPLICATION FAILED TO START`, "Application run failed", `BeanCreationException`, `UnsatisfiedDependencyException` |
| `P018-jdbc-unavailable` | `dependency_conn_failed` | pgjdbc "Connection to host:port refused" (with its target), MySQL "Communications link failure", `CannotGetJdbcConnectionException`, Hikari pool initialization failures |
| `P019-jvm-class-version` | `log_line` (error) | `UnsupportedClassVersionError`: the JVM is older than the code's target |
| `P020-jvm-build-failed` | `log_line` (error) | Maven and Gradle build failures (`BUILD FAILED`, `COMPILATION ERROR`, "Failed to execute goal", "Execution failed for task") |
| `P015-jvm-main-uncaught` | `log_line` (fatal) | `Exception in thread "main" …`: the JVM's main thread died |
| `P021-rust-main-panic` | `log_line` (fatal) | `thread 'main' panicked at …` |
| `P022-rust-thread-panic` | `log_line` (error) | a Rust thread other than main panicked (the process may keep running) |
| `P023-cargo-build-failed` | `log_line` (error) | `error: could not compile`, `error[E0425]`, linker failures |
| `P026-dotnet-db-unavailable` | `dependency_conn_failed` | Npgsql "Failed to connect to host:port" (with its target), SqlClient "A network-related or instance-specific error" |
| `P024-dotnet-unhandled` | `log_line` (fatal) | `Unhandled exception. …`: a .NET process died |
| `P027-dotnet-build-failed` | `log_line` (error) | `Build FAILED.`, `error CS…`, `NETSDK…`, `MSB…`, `NU…` |
| `P029-rails-db-setup` | `log_line` (error) | `ActiveRecord::NoDatabaseError`, pending migrations |
| `P030-rails-server-running` | `log_line` (error) | "A server is already running" (stale PID file) |
| `P031-app-secret-missing` | `log_line` (error) | Rails `secret_key_base` / master key, Laravel "No application encryption key" |
| `P028-ruby-uncaught` | `log_line` (fatal) | `app.rb:3:in …: message (ErrorClass)`: a Ruby program died |
| `P033-php-db-unavailable` | `dependency_conn_failed` | PDO/Laravel `SQLSTATE[…] [2002/1045/1049/2006]`, "could not find driver" |
| `P032-php-fatal` | `log_line` (fatal) | `PHP Fatal error:` and `PHP Parse error:` |
| `L001-listening` | `log_line` (info) | announced listen addresses (`Listening on :8080`, Vite `Local:`, Uvicorn, Django, Flask, Next.js, Spring's `Tomcat started on port(s): 8080`, Ktor's `Responding at …`, Rocket's `has launched from`, PHP's built-in server). Used by `run` to auto-probe the port |

The table is in match order: `P012` is checked before `P009` so `fatal error: runtime: out of memory` counts as
out of memory, not as a generic fatal error. The JVM patterns are specific shapes, so they go before `P015`: a
JVM error often arrives as `Exception in thread "main" …`, and `P015` would otherwise claim it. (Java's
`OutOfMemoryError` is still claimed by `P012` first, and a missing class by `P013`.) Java and Kotlin share the JVM
and its messages, so these cover both. The same rule orders the other ecosystems: `P025` runs before everything so
Kestrel's port is captured, `P026` before `P024`, the Rails shapes (`P029`–`P031`) before the generic Ruby
exception (`P028`), and `P033` before `P032`, because the more specific error is also printed as the generic one.
