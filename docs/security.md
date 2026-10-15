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
| `${VAR}` values leak | Resolution happens only when `watch` starts a command. The resolved environment is never written back into the config. Error messages name missing variables, never values. |
| `init` overwrites user files | `init` refuses if either config name exists, writes with `O_EXCL`, and asks first unless `--yes` is given. |
| Kestrel touches processes it didn't start | Signals go only to process groups (Windows: Job Objects) Kestrel created. Services without a `command` are probed, never started or signalled. `status` reports leftover PIDs from a crashed session but never signals them. |
| Supervised processes outlive Kestrel | Ctrl-C stops every group (SIGTERM, grace, SIGKILL) and tests assert zero survivors. On Linux the group leader also gets `Pdeathsig`, so it dies if Kestrel is killed with `kill -9`. On macOS a `kill -9` of Kestrel can leave services running. Windows Job Objects use KILL_ON_JOB_CLOSE. |
| Secrets in process output reach the terminal | Every line is redacted before it's printed or emitted as JSON: private keys (including multi-line blocks), JWTs, URL credentials, provider key prefixes, bearer/basic auth, auth/cookie headers, secret-named `key=value` pairs, plus `redaction.extra_patterns`. Tested with `fakesvc --secrets`. |
| Hostile log content (`rm -rf ~`, prompt injection, terminal escapes) | Log text is only ever data: it's never executed, never used to build commands, and never read as instructions. ANSI/OSC sequences and other control characters are stripped before printing, so output can't drive your terminal. Tested with `fakesvc --hostile`. |
| Log floods exhaust memory | The collector buffer is bounded (10,000 events / ~8 MiB). Only debug/info lines are dropped, and every drop is reported. Measured: a 50 MB/min flood with a slow reader plateaus at ~47 MiB RSS. |

| Secrets reach disk | Redaction runs before an event is stored, so the SQLite database, its WAL, and the gzipped segments contain only redacted text. An integration test scans every stored byte, decompressed, for the fake secrets. Export redacts again. |
| A corrupt or newer database is "fixed" destructively | It never is. Corruption is reported (`doctor`, warnings) and the file is left byte-for-byte untouched, so you can inspect or move it. Supervision keeps working without storage. |
| Stored paths escape the data directory | Segment paths read from the database are validated before use; `..` and absolute paths are refused. |
| Exports overwrite files or leak via permissions | `--out` creates the file `0600`, refuses to overwrite, and removes a half-written bundle on failure. The data directory and files are created `0700`/`0600`. |
| `verify` runs a command you didn't approve | Commands run only with `--allow-exec` or an explicit `y` at a prompt showing the command, directory, and timeout. No terminal means no prompt and no exec. See [Exec policy](#exec-policy-verify). |
| History grows without bound | Retention (default 7 days / 500 MB) runs when each session ends and via `kestrel cleanup`. Active sessions are never removed. |

## Exec policy (`verify`)

`kestrel verify` runs a `command` check from `verify.checks` only with `--allow-exec`, or after showing the exact
command (secrets masked), the directory, and the timeout in a terminal and getting an explicit `y`. Without a
terminal there is no prompt and nothing runs. An empty or any other answer declines. Each command runs in its own
process group, is stopped (the whole group) at its timeout or on Ctrl-C, and only a redacted 20-line tail of its
output is kept. Verify never starts, restarts, or signals services, and never builds a command from log content.
Tests: `TestVerifyRefusesExecWithoutAuthorization`, `TestVerifyPromptInProcess`. Details:
[verification.md](verification.md#commands).

Terminal detection uses a real TTY check: `/dev/null` is a character device, but it is not a terminal and is
never prompted.

## Limits

- Masking and redaction are pattern-based and best-effort. They lower the risk of accidental exposure; they
  don't guarantee it can't happen. A secret in a format no pattern recognizes will be stored and exported as is,
  so review exports before sharing them. Stored data isn't encrypted: it's protected by file permissions only.
- Segments are flushed to the OS on every batch but not `fsync`ed, so a power loss (not a Kestrel crash) can
  lose the last moments of a session. SQLite itself runs with WAL and `synchronous=NORMAL`.
- Stale-session detection uses the recorded Kestrel PID. If the OS reuses that PID for an unrelated process,
  the session stays `active` (and so is never removed) until that process exits.
- The `.gitignore` matcher covers the common syntax (negation, anchoring, `**`, character classes) but not
  every corner of git's behavior. A mismatch makes Kestrel read a few extra manifests. It never causes writes
  or execution.
- On Windows, a process the child spawns in the instant before it is assigned to its Job Object isn't contained.
  That window is a few milliseconds after start.
- `run` executes the argv you give it directly. `watch` runs each configured `command` through `/bin/sh -c`
  (`cmd.exe /C` on Windows), because that's what config commands are: shell lines you wrote.
