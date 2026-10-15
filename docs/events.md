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
| `restart`, `verification_result` | Reserved for later phases. |

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
match. Tests enforce both, and also check that no example is claimed by an earlier pattern. To add a pattern,
add an entry with its evidence. IDs are stable because diagnosis rules will reference them.

| ID | Type | Recognizes |
|---|---|---|
| `P001-port-in-use` | `port_bind_failed` | `address already in use`, `EADDRINUSE`, "Port N is in use", Windows "Only one usage of each socket address" |
| `P002-bind-permission` | `port_bind_failed` | `listen EACCES`, `bind: permission denied` |
| `P003-postgres-unavailable` | `dependency_conn_failed` | libpq/psql connection failures, "database system is starting up", missing database/role, auth failures |
| `P004-mysql-unavailable` | `dependency_conn_failed` | "Can't connect to MySQL server", `ER_ACCESS_DENIED_ERROR`, `ER_BAD_DB_ERROR` |
| `P005-redis-unavailable` | `dependency_conn_failed` | "Could not connect to Redis", `redis.exceptions.ConnectionError`, ioredis errors |
| `P006-connection-refused` | `dependency_conn_failed` | Go `dial tcp …: connection refused`, Node `ECONNREFUSED`, dev-server proxy errors |
| `P007-host-not-found` | `dependency_conn_failed` | `ENOTFOUND`, `no such host`, "Name or service not known" |
| `P008-connection-timeout` | `dependency_conn_failed` | `i/o timeout`, `ETIMEDOUT` |
| `P012-out-of-memory` | `log_line` (fatal) | V8 heap OOM, Go runtime OOM, Python `MemoryError`, Java `OutOfMemoryError` |
| `P009-go-panic` | `log_line` (fatal) | `panic:` / `fatal error:` at line start |
| `P010-node-uncaught` | `log_line` (fatal) | uncaught exceptions, unhandled promise rejections |
| `P011-python-traceback` | `log_line` (error) | `Traceback (most recent call last):` |
| `P013-module-not-found` | `log_line` (error) | missing Node/Python modules, missing Go packages |
| `P014-test-failed` | `test_failed` | `go test`, Jest, pytest failure summaries |
| `L001-listening` | `log_line` (info) | announced listen addresses (`Listening on :8080`, Vite `Local:`, Uvicorn, Django, Flask, Next.js). Used by `run` to auto-probe the port |

The table is in match order: `P012` is checked before `P009` so `fatal error: runtime: out of memory` counts as
out of memory, not as a generic fatal error.
