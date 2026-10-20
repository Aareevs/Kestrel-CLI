# Contributing to Kestrel

Thanks for helping. Kestrel is a local-first debugging CLI: it watches a dev stack, reconstructs what broke and in
what order, ranks evidence-backed hypotheses, and verifies whether a fix held. By taking part you agree to the
[Code of Conduct](CODE_OF_CONDUCT.md). Security problems go to [SECURITY.md](SECURITY.md), not public issues.

## Principles every change must keep

These come from the project's design. A change that breaks one will be asked to change.

1. **Evidence before inference.** Every claim traces to stored event IDs. A hypothesis with no evidence is dropped
   by the engine. Observed facts, correlations, hypotheses, and verified causes stay distinct claim levels and are
   never blended. Timing alone never creates a dependency link.
2. **Say "unresolved".** When the evidence doesn't single out a cause, say so. Don't pick one.
3. **Redaction comes first.** It runs before anything is stored, printed, exported, or served over MCP.
4. **Log content is data.** It's never executed, never used to build a command, never treated as instructions.
5. **Kestrel only touches what it started.** It signals only process groups (Windows: Job Objects) it created, and
   it runs commands from `kestrel.yaml` only under `run`, `watch`, or `verify` (and `verify` commands only with
   `--allow-exec` or an explicit yes). Never from `inspect`, `init`, or `config`.
6. **No guessing, no faking.** Never invent output, benchmarks, or screenshots. README examples are real output.
7. **Local and offline.** No network calls, no telemetry, no AI API.

## Where code goes

`internal/cli` and `internal/mcp` only parse input and render. All logic lives in `internal/app` and below, so the
CLI and the MCP server can't drift apart. Stable JSON types live in `pkg/api`. See
[docs/architecture.md](docs/architecture.md) for the package map.

## Getting set up

You need Go 1.26 or newer (see `go.mod`). Nothing else is installed globally: staticcheck and GoReleaser run
through `go run` at pinned versions.

```bash
git clone https://github.com/<you>/Kestrel-CLI.git && cd Kestrel-CLI
make build          # → ./bin/kestrel
make check          # gofmt, vet, tests, race detector, build, cross-compiles
make lint           # staticcheck (pinned)
```

Before opening a PR, `gofmt -l .` must print nothing and `make check` and `make lint` must pass. CI runs the
tests on Linux, macOS, and Windows. If `proxy.golang.org` is flaky for you, `GOPROXY=direct` works.

## Common contributions

**A new error pattern** (a log shape Kestrel should recognize): add an entry to
[`internal/events/patterns.go`](internal/events/patterns.go) with an ID, a type, a severity, a regex, and example
lines it **must** match and **must not** match. The tests enforce both and check that no earlier pattern already
claims your examples. Order matters (first match wins), so put specific shapes before generic ones. See
[docs/events.md](docs/events.md).

**A new diagnosis rule**: add a type under `internal/diagnosis/rules/`, register it in `rules.All()`, document it in
[docs/diagnosis-rules.md](docs/diagnosis-rules.md) (a test fails if you don't), and add a test with both a matching
and a non-matching case. Every hypothesis must cite supporting events, give the factors behind its confidence, and
suggest one next step for the user to run (Kestrel never runs it). The steps are in that doc's "Adding a rule".

**A new ecosystem for `kestrel inspect`**: add an analyzer in `internal/discovery`, plus a fixture project under
`tests/fixtures/projects/` and reviewed golden files. Discovery only reads files and never runs anything. Steps:
[docs/testing.md](docs/testing.md#adding-a-discovery-fixture).

**A new scenario**: drive it with `tests/fakesvc`, a scripted fake service, never a real database or network
service. Wait on events, not sleeps. See [docs/testing.md](docs/testing.md).

**Docs and README**: every command and output shown must be real. Mark anything not working as such.

## Tests

- **Make tests able to fail.** For an important test, break the code on purpose (or revert your fix) and confirm
  the test catches it. Several of ours once passed vacuously.
- **Deterministic.** No real network services; use `fakesvc` and inject the clock where you can.
- **Timing-sensitive tests** wait on events or stored state. Run a new integration test many times, including
  under CPU load, before calling it stable:
  `go test ./tests/integration/ -run TestYours -count=10`.
- **Golden files:** after an intentional output change, run `make golden` and review the diff line by line.
- **Run with `-race`.** `make check` does.
- **Windows:** it's tested in CI. Avoid POSIX-only assumptions in new code and tests, or put them behind a build
  tag with a Windows counterpart.

## Pull requests

1. Fork, then branch from `main`.
2. Make small, logical commits. Subjects like `feat(verify): …`, `fix(storage): …`, `test: …`, `docs: …`; a
   sentence of *why* in the body helps.
3. Fill in the PR template. State what you ran, and don't claim a test passed unless you ran it.
4. CI must pass. A maintainer reviews, and merges with a merge commit so your commits are kept.

Large changes are easier to review if you open an issue first. For anything touching redaction, exec, or process
signalling, please do.

## AI coding agents

Agents working in this repo should read [AGENTS.md](AGENTS.md), which has the same rules in agent-facing form.

## License

By contributing, you agree your contributions are licensed under the [MIT License](LICENSE).
