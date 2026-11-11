# Configuration reference

`kestrel.yaml` is optional. Kestrel works from discovery alone, and a config makes its picture of your stack
more precise. `kestrel init` writes a starting point from discovery.

- **Location:** `kestrel.yaml` or `.kestrel.yaml` at the project root. Having both is an error.
- **Project root:** `--project <dir>` if given. Otherwise the nearest ancestor of the current directory that
  contains a config file. Otherwise the current directory.
- **Validation:** strict. Unknown keys are errors, every problem is reported at once with a line number, and
  `kestrel config validate` exits with code `2` when the file is invalid.
- **Editor support:** point your YAML language server at [`kestrel.schema.json`](kestrel.schema.json), e.g.
  `# yaml-language-server: $schema=./docs/kestrel.schema.json`.

## Full example

```yaml
version: 1
project:
  name: example-app
storage:
  retention_days: 7
  max_total_mb: 500
correlation:
  window: 10s
redaction:
  extra_patterns: ["acme_[a-z0-9]{32}"]
services:
  - name: database
    health: { type: tcp, address: localhost:5432 }
  - name: api
    dir: ./backend
    command: go run .
    env: { DATABASE_URL: "${DATABASE_URL}" }
    depends_on: [database]
    health: { type: http, url: http://localhost:8080/health, interval: 2s, timeout: 1s, startup_grace: 15s }
  - name: frontend
    dir: ./frontend
    command: npm run dev
    depends_on: [api]
    health: { type: http, url: http://localhost:5173 }
verify:
  checks:
    - { type: probe, service: api }
    - { type: command, run: go test ./..., dir: ./backend, timeout: 120s }
patterns:
  - id: U001-shard-down
    description: a database shard is unreachable
    type: dependency_conn_failed
    severity: error
    regex: 'PRISMA: shard (?P<target>\S+) unreachable'
    match: ["PRISMA: shard db-7:5432 unreachable"]
    no_match: ["PRISMA: shard db-7:5432 healthy"]
```

## Fields

### Top level

| Key | Type | Default | Notes |
|---|---|---|---|
| `version` | int | required | Must be `1`. |
| `project.name` | string | | Display name. |
| `storage.retention_days` | int | `7` | Sessions that ended longer ago than this are removed when a session ends and by `kestrel cleanup`. |
| `storage.max_total_mb` | int | `500` | While the recorded total exceeds this, the oldest closed sessions are removed. The active session is never removed. Sizes are estimates that track real disk use closely (a test keeps them within ±15%). |
| `correlation.window` | duration | `10s` | Time window, used only as a weak correlation signal (from Phase 4). |
| `redaction.extra_patterns` | list of regex | `[]` | Extra patterns to redact. Must compile. |
| `services` | list | `[]` | See below. |
| `verify.checks` | list | `[]` | See below. |
| `patterns` | list | `[]` | Project-specific error shapes. See below. |

Durations use Go syntax: `500ms`, `2s`, `1m30s`.

### `services[]`

| Key | Type | Notes |
|---|---|---|
| `name` | string | Required, unique. Letters, digits, `-`, `_`, `.`; up to 63 characters. |
| `dir` | string | Working directory relative to the project root. Must exist. |
| `command` | string | What Kestrel runs (only under `run`/`watch`/`verify`, never during `inspect`/`init`/`config`). |
| `env` | map | Environment for the command. `${VAR}` takes the value from your shell when the command starts. Resolved values are never stored or printed. Kestrel also sets `PYTHONUNBUFFERED=1` for services it starts, because a Python program writing to a pipe otherwise holds its output back and its last lines can be lost when it stops. Set `PYTHONUNBUFFERED` yourself (in `env` or your shell; an empty value turns it off) and yours is used. |
| `depends_on` | list | Names of other services. Unknown names and cycles are errors. |
| `health` | object | Readiness probe. See below. |

Each service needs a `command`, a `health` probe, or both. A service with only a probe (e.g. a database you
run yourself) is observed and never started.

### `services[].health`

| Key | Default | Applies to | Notes |
|---|---|---|---|
| `type` | required | | `http`, `tcp`, or `process` (process liveness; requires `command`). |
| `url` | | http | Absolute `http://` or `https://` URL. |
| `address` | | tcp | `host:port`. |
| `expect_status` | `200-399` | http | A code (`"204"`) or a range (`"200-299"`). |
| `body_contains` | | http | Required substring of the response body. |
| `interval` | `2s` | all | |
| `timeout` | `1s` | all | Must not exceed `interval`. |
| `retries` | `3` | all | Consecutive failures before the service counts as not ready. |
| `startup_grace` | `10s` | all | Failures during this period after start are not counted. |

### `verify.checks[]`

| Key | Notes |
|---|---|
| `type` | `probe` (re-run a service's health probe) or `command`. |
| `service` | probe: a service that has a `health` probe. |
| `run` | command: a shell line, run only with `--allow-exec` or after you confirm it at a prompt (see [verification](verification.md#commands)). |
| `dir` | command: working directory, relative to the project root. Must exist. Default: the project root. |
| `timeout` | command: default `120s`. The command's whole process group is stopped when it runs over. |

Probe checks are added to the ones verify picks by itself (the health probe of each affected service, and each
address the incident's services failed to reach). See [verification.md](verification.md).

### `patterns[]`

Teach Kestrel an error shape that is specific to your project: an internal library's message, a vendor SDK's
wording. Kestrel then classifies those lines like its own patterns (see [events](events.md)), so they count as
anomalies, join incidents, and feed diagnosis.

| Key | Notes |
|---|---|
| `id` | `U001-short-name`: `U`, three digits, lowercase words. Unique in the file. |
| `description` | What the error means. |
| `type` | `log_line`, `dependency_conn_failed`, `port_bind_failed`, or `test_failed`. |
| `severity` | `debug`, `info`, `warn`, `error`, or `fatal`. A line that declares its own level (say `level=warn`) keeps it, except `fatal`, which always raises it. |
| `regex` | A Go ([RE2](https://github.com/google/re2/wiki/Syntax)) regular expression, at most 1000 characters. Named groups `(?P<port>\d+)` and `(?P<target>\S+:\d+)` become the event's port and target. A `dependency_conn_failed` pattern with a `target` is how a service's failure to reach another service gets linked in the dependency graph. |
| `match` | 1–20 lines the pattern must match. |
| `no_match` | 1–20 similar lines it must **not** match. |

Every pattern carries its own evidence, and `kestrel config validate` checks it: your regex must match every `match`
line and none of the `no_match` lines, with errors on the line that is wrong.

**Order.** Your patterns run **after** Kestrel's built-in ones, so they recognize what the built-ins don't and
never take a line a built-in already claims (diagnosis rules depend on the built-in IDs). If one of your `match`
examples is already recognized by a built-in, validation says which one, because your pattern would never run on
it. Between your own patterns, the first in the file wins. There can be at most 50. They apply to `run` and
`watch` in this project only. Sessions recorded earlier are not re-classified.

## Secrets

- Reference secrets with `${VAR}` rather than writing them into the file.
- `kestrel config show` masks literal `env` values (bare `${VAR}` references are shown as written), replaces
  passwords in URLs with `xxxxx`, and masks `NAME=value` assignments in commands when the name looks secret
  (`TOKEN`, `SECRET`, `PASSWORD`, `API_KEY`, …). This masking is best-effort. Don't commit secrets.
