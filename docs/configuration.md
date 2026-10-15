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
```

## Fields

### Top level

| Key | Type | Default | Notes |
|---|---|---|---|
| `version` | int | required | Must be `1`. |
| `project.name` | string | | Display name. |
| `storage.retention_days` | int | `7` | Sessions older than this are removed (from Phase 3). |
| `storage.max_total_mb` | int | `500` | Oldest closed sessions are removed past this size (from Phase 3). |
| `correlation.window` | duration | `10s` | Time window, used only as a weak correlation signal (from Phase 4). |
| `redaction.extra_patterns` | list of regex | `[]` | Extra patterns to redact. Must compile. |
| `services` | list | `[]` | See below. |
| `verify.checks` | list | `[]` | See below. |

Durations use Go syntax: `500ms`, `2s`, `1m30s`.

### `services[]`

| Key | Type | Notes |
|---|---|---|
| `name` | string | Required, unique. Letters, digits, `-`, `_`, `.`; up to 63 characters. |
| `dir` | string | Working directory relative to the project root. Must exist. |
| `command` | string | What Kestrel runs (only under `run`/`watch`/`verify`, never during `inspect`/`init`/`config`). |
| `env` | map | Environment for the command. `${VAR}` takes the value from your shell when the command starts. Resolved values are never stored or printed. |
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
| `run`, `dir`, `timeout` (default `120s`) | command: executed only with `--allow-exec` or interactive confirmation (from Phase 5). |

## Secrets

- Reference secrets with `${VAR}` rather than writing them into the file.
- `kestrel config show` masks literal `env` values (bare `${VAR}` references are shown as written), replaces
  passwords in URLs with `xxxxx`, and masks `NAME=value` assignments in commands when the name looks secret
  (`TOKEN`, `SECRET`, `PASSWORD`, `API_KEY`, …). This masking is best-effort. Don't commit secrets.
