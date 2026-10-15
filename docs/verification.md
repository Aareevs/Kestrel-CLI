# Verification

`kestrel verify` answers one question: did the fix work? "The error went away" is not the same as "the problem
is fixed", so verify samples the affected services across a **stability window** and, when it can, checks that the
incident's errors have stopped.

Verify never starts, restarts, or signals your services. It observes them. The only thing it ever runs is a
command you configured in `verify.checks`, and only when you allow it (see [Commands](#commands)).

## What it verifies against

By default, the latest incident in the project's latest session. `kestrel verify <session>` picks another session
(full ID, unique prefix, or `last`), and `--incident N` picks another incident in it.

- **With an incident,** verify reports what was failing (**Before**: the leading hypothesis's evidence, or the
  incident's anomalies when no rule matched) and any recovery the session recorded (**Recovery**).
- **With a session but no incident,** it checks that every service in the session is healthy.
- **With no session at all,** it checks the services in `kestrel.yaml`. The result is not saved, because there is
  no session to keep it with.

## The checks

Verify chooses its checks from the evidence. It does not guess:

| Kind | When | Passes when |
|---|---|---|
| `probe` | `verify.checks` probe entries, plus the health probe of every affected service | the probe succeeds (HTTP status / body, TCP connect) |
| `tcp` | an affected service without a probe that announced a listening port; and every address the incident's services **failed to connect to** (the "previously failing requests") | the address accepts a connection |
| `liveness` | the session is still running (`kestrel watch` in another terminal) | the supervised process is running |
| `command` | `verify.checks` command entries, **only when authorized** | the command exits 0 within its timeout |

"Affected services" means the incident's members, or every service when there is no incident. A probe of type
`process` is covered by `liveness`.

## The stability window

Every probe, tcp, and liveness check is sampled once at the start and then once per `--interval` (default 2s)
until `--window` (default 30s) has passed. With the defaults that is 16 samples. The window always runs to the
end: a first sample that passes ends nothing. A window too short for 3 samples is rejected.

Each check ends up:

- `passed`: every sample passed,
- `flaky`: some samples passed and some failed (a fix that landed mid-window looks like this, and so does a relapse),
- `failed`: no sample passed.

Commands run once, after the window.

## Before and after

When the session being verified is still running, verify also reads the events it records during the window. Any
anomaly from the affected services (a crash, a failed probe, a refused connection, an error line) is listed under
**During the window: still occurring**, with repeats collapsed and a count. If errors are still happening, the
problem isn't fixed, however healthy the probes look.

When the session has ended, there is nothing new to compare, and verify says so in its notes. Verifying while
`kestrel watch` is running gives the strongest answer.

An error that repeats unchanged is recorded as one summary event at least every 10 seconds (see
[events](events.md)). A window shorter than that may not see it recur. Verify notes this when the window is
shorter than 10s.

## Verdicts

| Verdict | When | Claim | Exit |
|---|---|---|---|
| `resolved` | the window completed, every check that ran passed on every sample, and nothing recurred | `verified` | 0 |
| `partially_resolved` | some checks pass, while others fail or flap, or errors keep recurring | `observed` | 1 |
| `still_failing` | nothing that ran passed | `observed` | 1 |
| `inconclusive` | the window was interrupted, or nothing could be checked | `unresolved` | 3 |

Two rules keep the verdict honest:

- A passing `liveness` check doesn't count as progress for a service that also has a probe: a process that is up
  but answers 503 is still failing.
- With a live session but nothing to probe, "no new errors" alone is `inconclusive`, not `resolved`.

Every verdict comes with its reasons (which checks passed, which failed, what was not run), and the evidence cites
event IDs from the session. A skipped command is listed under "not run". It doesn't block `resolved`, but you can
see that it didn't run.

## Commands

A `command` check runs only when one of these is true:

- `--allow-exec` was passed, or
- verify is attached to a terminal, showed you the **exact command** (secrets in it masked), **the directory it
  will run in**, and **its timeout**, and you answered `y`.

Without a terminal (a script, CI, `< /dev/null`) there is no prompt, and the command is skipped with the reason
"pass --allow-exec". Any answer other than `y`/`yes`, including an empty one, declines. You are asked about each
command before the window starts, so nobody is prompted halfway through.

An authorized command runs through the platform shell in its own process group, with your environment, in `dir`
(relative to the project root; validated to exist). When it exceeds its timeout (default 120s), or you press
Ctrl-C, the whole group is stopped: it is a group Kestrel started. Only the last 20 lines of its output are kept,
redacted, and they are shown when it fails.

Kestrel never builds a command from log content. The only commands it runs are the ones you wrote in
`kestrel.yaml`.

## Where results go

Each verification of a recorded session is saved with that session (and removed with it). `kestrel timeline` lists
them under **Verification**. `--format json` gives the full report, including every sample count and evidence ID.
The report's schema is `VerificationReport` in [`pkg/api`](../pkg/api/verification.go).

## Examples

```bash
kestrel verify                       # latest incident, 30s window, commands only if you confirm
kestrel verify --allow-exec          # also run verify.checks commands without asking
kestrel verify --window 2m --interval 5s
kestrel verify 01J9 --incident 2     # a specific session and incident
kestrel verify --format json         # full report for scripts; exit code carries the verdict
```
