# Security model

Kestrel runs on a developer machine, next to source code and often next to secrets. This page covers what it
promises, and where the current build stops.

## Threat model

| Threat | Mitigation (current build) |
|---|---|
| Discovery executes hostile project code (e.g. an npm `postinstall`) | Discovery only reads files. It never runs scripts, package managers, or build tools. Tests assert it. |
| Discovery reads outside the project via symlinks | The root is symlink-resolved. Every file read re-resolves its path and is refused if it lands outside the root. Directory symlinks are never followed. Tested. |
| Huge or adversarial files exhaust memory or time | Files over 1 MiB are skipped with a warning. The walk stops at depth 3, 5000 files, or 3 seconds and reports the truncation. |
| Secrets in `.env` leak into output | `.env` is never opened. From `.env.example` / compose `environment` only variable names are kept. Tested. |
| Secrets in `kestrel.yaml` are shown on screen | `config show` masks literal env values, URL passwords, and secret-looking `NAME=value` assignments in commands. |
| `${VAR}` values leak | Resolution happens only when a command starts (from Phase 2). The resolved environment is never written back into the config. Error messages name missing variables, never values. |
| `init` overwrites user files | `init` refuses if either config name exists, writes with `O_EXCL`, and asks first unless `--yes` is given. |
| Kestrel touches processes it didn't start | Nothing in this build starts or signals processes. From Phase 2, signals go only to process groups Kestrel created. |

## Not yet in place

- **Log redaction** (`internal/privacy`) arrives with persistence (Phase 3), and runs before anything is
  stored or printed. Nothing is persisted before then.
- **Exec policy for `verify`** (`--allow-exec`, confirmation showing command, directory, and timeout) arrives
  in Phase 5.

## Limits

- Masking and (later) redaction are pattern-based and best-effort. They lower the risk of accidental exposure;
  they don't guarantee it can't happen.
- The `.gitignore` matcher covers the common syntax (negation, anchoring, `**`, character classes) but not
  every corner of git's behavior. A mismatch makes Kestrel read a few extra manifests. It never causes writes
  or execution.
- Log content will always be treated as inert data: never executed, never used to build shell commands, never
  read as instructions.
