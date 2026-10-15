# Diagnosis rules

`kestrel diagnose` runs every rule below against each incident. A rule proposes **hypotheses**, and each one
carries:

- the **supporting** event IDs,
- any **contradicting** event IDs,
- a confidence level (`high` / `medium` / `low`) with the **factors** that produced it (never a percentage),
- one concrete **next step**, which is a command for you to run. Kestrel never runs it.

The engine drops any hypothesis that cites no events, so nothing appears without evidence. Rule IDs are stable.

## How incidents are formed

An **anomaly** is a crash, startup timeout, probe failure, bind failure, connection failure, test failure, or a log
line at `error`/`fatal`. Warnings, overload notices, and stops Kestrel requested are not anomalies, so a noisy but
healthy service has no incident.

Anomalies join the current incident while they keep arriving within `correlation.window` (default 10s) of its
latest anomaly. Each join is recorded as one of two kinds:

- **strong**: same service, a dependency path in the graph, or a connection failure aimed at a port the other
  service owns;
- **weak**: time only.

An incident's `correlation_strength` is `weak` if any join was weak. That is a separate field from any
hypothesis's causal confidence.

**Dependency graph.** Edges come only from `depends_on` (declared) or from connection failures that name a port
exactly one other service owns (inferred, carrying the event IDs). Port ownership comes from probe targets and a
service's own "listening on …" output. A port claimed by two services has no owner. Failing at the same moment
never creates an edge.

**Initiating failure.** A member is *explained* when it depends on another member that failed before it. If
exactly one member is unexplained, its first anomaly is the initiating event. Otherwise the incident says it can't
be determined, nothing is labeled a downstream symptom, and the diagnosis is `unresolved`.

## Ranking and status

Hypotheses are ranked by confidence. Then a hypothesis about the initiating service ranks higher, then one that
covers more services, and one with contradicting evidence ranks lower. Ties go to the earlier evidence.

The incident's status is `hypothesis` when the initiating failure is known and the top hypothesis is at least
`medium`. Otherwise it is `unresolved`, and the diagnosis leads with the most informative next check.

Exit codes for `kestrel diagnose`: `0` no incidents, `1` an incident with a hypothesis (a project failure was
detected), `3` an unresolved incident.

## Rules

### `K001-port-conflict`

**Detects:** a service couldn't bind its port (`EADDRINUSE`, "address already in use", "Port N is in use", the
Windows equivalent), or wasn't permitted to (`EACCES`, `bind: permission denied`).

**Evidence:** the bind-failure line (patterns `P001`/`P002`), and the service's exit right after, if any.

**Confidence:** `high` with the exit, `medium` without it. A later readiness success for the service is
contradicting evidence (it did get a port) and lowers the level one step.

**Next step:** `lsof -nP -iTCP:<port> -sTCP:LISTEN` (`netstat -ano | findstr :<port>` on Windows) to see who holds
the port. Kestrel never signals that process. For permission failures, use a port above 1024.

### `K002-dependency-down`

**Detects:** a service failed (crash, startup timeout, probe failure, or bind failure) and services depending on
it failed afterwards.

**Requires a real link:** a direct dependent either declares `depends_on` on the failed service, or its connection
errors target that service's port. Services further down the chain are included through those dependents
("frontend via api"). Two services failing in sequence with no link produce no K002 hypothesis.

**Evidence:** the upstream failure and the line that preceded it, the dependents' connection errors (repeats
collapsed with a count), and their later readiness or lifecycle failures.

**Confidence:** starts `high`. It is lowered when a dependent was already failing *before* the upstream failure
(contradicting evidence). It drops to `low` when the failed service itself depends on a member that failed even
earlier, because then it's a symptom, not the start.

**Next step:** `kestrel timeline --service <failed service>`. The dependents are likely to recover once it does.

### `K003-crash-after-start`

**Detects:** a service that had passed its readiness probe later crashed.

**Evidence:** the readiness success (it was healthy), the most telling error line just before the exit, and the
exit itself.

**Confidence:** `high` if a fatal line (panic, out of memory, uncaught exception, …) came right before the exit,
`medium` for an error line, and `low` if the output shows nothing. In the `low` case the title says the cause isn't
visible.

**Facts vs. possibilities:** the explanation states what was observed ("was ready at …, exited with code … at …")
separately from the hypothesis ("caused by what it logged just before exiting").

**Next step:** `kestrel timeline --service <service>` to read what it printed before it exited.

### `K004-startup-failure`

**Detects:** a service that crashed before ever becoming ready, or never passed its readiness probe
(`startup_timeout`). It defers to `K001` when a bind failure explains the start-up failure.

**Evidence:** the most telling error line from its startup output, and the crash or timeout.

**Confidence:**

| Situation | Level |
|---|---|
| Crashed, with an error logged first | `high` |
| Crashed, with no error in its output | `medium` |
| Timed out, with errors logged while starting | `medium` |
| Timed out, with no explanation in its output | `low` |
| It was trying to reach a member that had already failed, or depends on one | drops to `low`, and those events are cited as contradicting: this may be a downstream symptom |

**Next step:** `kestrel timeline --service <service>`.

### `K005-dependency-not-running`

**Detects:** repeated connection failures to a port that no supervised service provides. Either nothing Kestrel
knows listens there, or a probe-only (external) service owns it.

**Evidence:** the connection errors, collapsed with a count. For an external service, also its own failing probe.

**Confidence:** `medium`. It is `high` for an external service whose own probe also fails. It stays silent when the
connection target is unknown, and when a supervised service owns the port (`K002` covers that).

**Next step:** start whatever should listen there, or check the service's connection settings (for example
`DATABASE_URL`). The suggested command, `lsof -nP -iTCP:<port> -sTCP:LISTEN`, shows whether anything listens.

### `K006-missing-module`

**Detects:** "Cannot find module", `ModuleNotFoundError`, a missing Go package (pattern `P013`).

**Evidence:** the missing-module lines, and the exit after them, if any.

**Confidence:** `high` with the exit, `medium` without it.

**Next step:** install the project's dependencies (`npm install`, `pip install -r requirements.txt`,
`go mod download`) and check the import path.

## Adding a rule

1. Add a type in `internal/diagnosis/rules/` with an `ID()` of the form `K0NN-short-name` and an `Evaluate`.
   It reads only the `Context` (incident, graph, events, tail lines), never storage or the system.
2. Every hypothesis must cite supporting events, give confidence factors, and suggest a next step.
3. Register it in `rules.All()`, document it here (a test fails otherwise), and add a test in
   `internal/diagnosis/diagnosis_test.go` with both a matching and a non-matching case.
