# Extending Kestrel

Four extension points, from "no code" to "a new package". Each one is checked by the code that registers it, so a
mistake fails loudly (a panic at startup, or a failing test) instead of quietly misbehaving.

| I want Kestrel to… | Do this | Needs a code change? |
|---|---|---|
| recognize a log line from my own stack | [a project pattern](#a-project-pattern-no-code) in `kestrel.yaml` | no |
| recognize a log line for everyone | [a built-in pattern](#a-built-in-pattern) | yes |
| explain a failure it can't explain yet | [a diagnosis rule](#a-diagnosis-rule) | yes |
| detect a new stack in `inspect` / `init` | [an analyzer](#an-analyzer-for-inspect) | yes |

Principles that every extension keeps (evidence before inference, redaction first, log text is data, Kestrel only
touches what it started) are in [CONTRIBUTING.md](../CONTRIBUTING.md#principles-every-change-must-keep).

## A project pattern (no code)

Add a `patterns:` entry to `kestrel.yaml`. It applies to this project's `run` and `watch`, after the built-ins:

```yaml
patterns:
  - id: U001-shard-down
    description: a database shard is unreachable
    type: dependency_conn_failed          # log_line, dependency_conn_failed, port_bind_failed, test_failed
    severity: error
    regex: 'PRISMA: shard (?P<target>\S+) unreachable'
    match: ["PRISMA: shard db-7:5432 unreachable"]
    no_match: ["PRISMA: shard db-7:5432 healthy"]
```

- `id` is `U` + three digits + a short name, unique in the file.
- `match` and `no_match` are required examples: every `match` line must match the regex and no `no_match` line may.
  `kestrel config validate` checks them, with line numbers, before anything runs.
- Named groups `port*` and `target*` become the event's port and target, which is how a connection failure gets
  linked to the service that owns that port. `target` should look like `host:port`.
- A project pattern can't shadow an earlier one: if a built-in already claims your example line, validation says
  which and you can usually drop your pattern.
- The first match wins, built-ins first, then yours in file order.

Try it: add the pattern, run `kestrel config validate`, then `kestrel run -- sh -c 'echo "PRISMA: shard db-7:5432 unreachable"'`
and `kestrel sessions export last`: the event is `dependency_conn_failed` with `pattern_id: U001-shard-down`, port 5432 and target
`db-7:5432` (while a session is live, `watch` also shows it as an annotation after the line).

Reference: [configuration.md](configuration.md#patterns), [events.md](events.md#pattern-library).

## A built-in pattern

For a shape that belongs to a runtime or tool many people use. Patterns live in `internal/events`, grouped by
ecosystem (`patterns_jvm.go`, `patterns_docker.go`, …). Register from an `init()`:

```go
func init() {
	for _, p := range []Pattern{{
		ID:          "P043-example-db-down",       // P + number + short name; stable once released
		Description: "ExampleDB refused the connection",
		Type:        api.EventDependencyConnFailed,
		Severity:    api.SeverityError,
		Re:          regexp.MustCompile(`ExampleDB: cannot reach (?P<target>\S+)`),
		Priority:    1250,                          // lower runs first; built-ins are 100 apart
		Match:       []string{"ExampleDB: cannot reach db:9042"},
		NoMatch:     []string{"ExampleDB: reached db:9042"},
	}} {
		MustRegister(p)
	}
}
```

- **Order matters** (the first match wins). Put specific shapes before generic ones by choosing a `Priority`
  between two built-ins. A test fails if an earlier pattern already claims one of your `Match` examples.
- `Register` checks the ID format, the description, the type and severity, that named groups are only `port*` and
  `target*`, and your examples. A bad pattern stops the program (and its tests) from starting.
- Document it in [events.md](events.md#pattern-library); a test fails if a registered pattern is missing there.
- If a rule should react to it (a next step for this runtime), see below.

## A diagnosis rule

For a failure Kestrel recognizes but can't yet explain well. Rules read an incident's events and propose
hypotheses; they never touch storage or the system.

1. Add a type in `internal/diagnosis/rules/` with `ID()` (`K0NN-short-name`) and `Evaluate(*Context) []api.Hypothesis`.
2. Every hypothesis must **cite events** (the engine drops one that cites none), give the **factors behind its
   confidence**, and suggest **one next step for the user to run**. Kestrel never runs it.
3. Register it with `rules.MustRegister(MyRule{})` from an `init()`. Rules run in ID order.
4. Document it in [diagnosis-rules.md](diagnosis-rules.md) (a test fails otherwise) and test both a case where it
   fires and one where it does not.

Two things to know before you write one. **Specific beats generic:** the catch-all `K004` (a service failed, cause
unknown) is nudged below a rule that names the cause, so yours should be specific about what it found. **Claims stay
labeled:** a hypothesis is a hypothesis; use "unresolved" rather than guessing when the evidence doesn't single out
a cause. See [diagnosis-rules.md](diagnosis-rules.md#ranking-and-status).

## An analyzer for inspect

For a stack `kestrel inspect` should detect. An analyzer reads files in a directory and returns candidates. It
**only reads**; nothing it does may run a project's code.

```go
// internal/discovery/zig.go
func init() { registerAnalyzer("zig", 110, (*scanner).analyzeZig) }

func (s *scanner) analyzeZig(dir string) []*candidate {
	raw, ok := s.readSeen(joinRel(dir, "build.zig.zon")) // records the file as scanned
	if !ok {
		return nil
	}
	c := newCandidate(dir, s.dirName(dir), "zig", originNative)
	c.setCommand("zig build run", api.ConfidenceMedium, api.Source{File: joinRel(dir, "build.zig.zon"), Line: lineOf(raw, ".name")})
	return []*candidate{c}
}
```

- Every command, port and dependency carries a **source** (`file:line`) and a **confidence**. Ports found only by
  convention are `medium` or `low`.
- Analyzers run in `(order, name)` order. The built-ins use 10-100; take the next free number.
- Add a **fixture** under `tests/fixtures/projects/<name>/` (the smallest realistic set of manifests), add it to
  `fixtures` in `tests/integration/cli_test.go`, run `make golden`, and **check every line number, command and port in
  the goldens against the fixture's files**. Steps: [testing.md](testing.md#adding-a-discovery-fixture).
- Unit-test edge cases in `internal/discovery` with temp directories, not fixtures.
- Add the stack to [ecosystems.md](ecosystems.md) and the README table.

## When you change what Kestrel prints

- **`--json` output** comes from `pkg/api`. After changing a type, run `make schemas` to regenerate
  `docs/schemas/*.schema.json`; a test fails if you forget, and the integration tests validate real output against
  them. Adding a field is compatible within a schema version; changing or removing one is not.
- **Text output** is checked by golden files: `make golden`, then review the diff line by line.
- **MCP tools** are thin wrappers over `internal/app`. A new one is read-only by construction (see
  [mcp.md](mcp.md)); add it to the tool list test and the docs.

## Before you open a PR

`gofmt -l .` prints nothing, then `make check` and `make lint`. For an important test, break the code on purpose and
confirm the test fails. See [CONTRIBUTING.md](../CONTRIBUTING.md).
