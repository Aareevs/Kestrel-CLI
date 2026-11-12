# Changelog

All notable changes to Kestrel. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
versions follow [Semantic Versioning](https://semver.org/). Details and evidence for each release are in
[docs/releases/](docs/releases) and [docs/PROGRESS.md](docs/PROGRESS.md). Numbers like (#12) are pull requests.

## [1.1.0] - 2026-11-12

[Release notes](docs/releases/v1.1.0.md).

### Changed (behavior you may notice)

- **`kestrel verify` exits 3 (inconclusive) when everything that ran passed but a configured check did not run**,
  such as a `command` check without `--allow-exec`. It used to exit 0 with `resolved`. A failing check still
  fails. (#27)
- **A weak (time-only) incident is at most one correlation window long.** Unrelated errors in a long session no
  longer chain into one ever-growing incident. Strong links (same service, dependency path, a connection to an
  owned port) still carry an incident on. (#27)
- **A steadily repeating error now emits a summary every 2 seconds** instead of one per run (up to 10s), and
  `verify` windows of 2s or more see it recur. (#27)
- **Kestrel sets `PYTHONUNBUFFERED=1` for services it starts**, unless you set it. Python no longer holds back
  output written to Kestrel's pipe. (#25)
- **Every command that prints stored text redacts it again with today's rules**, not only `sessions export` and
  MCP. (#26)
- `kestrel` with no arguments at a terminal shows a welcome screen instead of the command list. Piped, `--json`
  and `--quiet` still print the list. (#21)
- `kestrel watch` at a terminal pins a status table above the log. `--plain` turns it off. (#22)
- Output is tidier on a terminal: width-aware, grouped `--help`, `next:` hints on errors. (#18)

### Added

- **Interactive picker**: `-i` / `--interactive` on `diagnose`, `timeline`, `verify` and `sessions show`. (#20)
- **Shortcuts**: `why` for `diagnose`, `-a`, `-s`, `-w`, `-n`, `-I/--incident N` on `diagnose`, `timeline` and
  `verify`, and a bare `kestrel sessions` that lists. (#19)
- **Custom error patterns** in `kestrel.yaml` (`patterns:`), validated with line numbers. (#12)
- **Error patterns** for Java/Kotlin (#13), Rust, .NET, Ruby/Rails and PHP/Laravel (#14), and Docker and
  Compose (#15).
- **`kestrel inspect`** for Elixir/Phoenix, .NET, Deno and Bun (#16), monorepo and workspace discovery for pnpm,
  npm, yarn, Bun, Go and Cargo (#17), and Expo / React Native (#25).
- **`kestrel mcp init` and `kestrel mcp config`** for Claude Code, Cursor and VS Code. (#23)
- **Three MCP tools**: `list_sessions`, `search_events`, `validate_config`; `incident` on `get_diagnosis` and
  `get_failure_timeline`. (#24)
- **JSON Schemas** for `diagnose`, `timeline` and `verify` (`docs/schemas/`, `kestrel schema`). (#28)
- **CI reports for `verify`**: `--format markdown|junit`, `--markdown FILE`, `--junit FILE`. (#29)
- Registries for patterns, rules and analyzers, so each can be extended in one file. (#11)
- Docs: [extension guide](docs/extending.md) and [ecosystem matrix](docs/ecosystems.md). (#30)

### Fixed

- Node's `node:internal/…:N` location line was classified as a fatal error and picked as the cause; a missing
  module is now explained before the generic "failed during startup", with the right next step. (#25)
- The port-conflict diagnosis claimed an unknown process held the port when another service in the same session
  did. (#25)
- `redis://:password@host` and an AWS secret key written after the word "secret" were not redacted. (#26)
- A dependent's steady refusals after its dependency died formed a bogus second incident. (#27)
- A crashed process whose output matched no pattern was reported as having "no error"; its last lines are now
  quoted. (#27)
- A flaky `verify` test (a 300ms command timeout raced process start). (#27)
- Expo and React Native apps had no framework or port detected. (#25)
- `kestrel verify` no longer waits out its window when there is nothing to sample. (#18)
- Smaller: the "Checks none could be chosen" wording in `verify` (#26).

### Security

- Redaction gaps closed (see Fixed), and stored text is redacted with current rules wherever it is printed. Text
  already on disk from before a fix stays as stored; delete such a session with `kestrel sessions delete` if an old
  rule missed a secret in it. (#26)

## [1.0.0] - 2026-10-16

First release: `inspect`, `init`, `run`, `watch`, `status`, `timeline`, `diagnose`, `verify`, `sessions`, `cleanup`,
`config`, `doctor`, and the read-only MCP server, for macOS, Linux and Windows (Windows untested). See
[docs/PROGRESS.md](docs/PROGRESS.md).
