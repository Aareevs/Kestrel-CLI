# Testing

```sh
make check     # gofmt, vet, tests, race detector, build, cross-compiles (what CI runs)
make lint      # staticcheck (via go run; nothing installed globally)
```

## Layout

- Unit tests sit next to the code (`internal/*/..._test.go`).
- `tests/fixtures/projects/<name>/`: sample projects that discovery scans. Kestrel only reads them. Go fixtures
  have their own `go.mod`, so `go build ./...` and `go vet ./...` in this repo skip them.
- `tests/integration/`: runs the CLI in-process (`cli.Execute` with injected streams, cwd, and env) and compares
  output with goldens in `tests/integration/testdata/`.

## Golden files

Golden output replaces the absolute fixture path with `<root>`. After an intentional output change:

```sh
make golden          # go test ./tests/integration -update
git diff tests/integration/testdata
```

Review the diff line by line. A golden only proves output is stable. It doesn't prove the output is right.

## Adding a discovery fixture

1. Create `tests/fixtures/projects/<name>/` with the smallest realistic set of manifests.
2. Add `<name>` to `fixtures` in `tests/integration/cli_test.go`.
3. Run `make golden` and check every command, port, line number, and dependency in the new goldens against the
   fixture files.

For edge cases (limits, symlinks, ignore rules) use temp-dir unit tests in `internal/discovery`, not fixtures.
Committed fixtures can't contain `node_modules`, gitignored paths, or symlinks reliably.

## Scenario tests

Scenarios A–J (see the build spec) need the `tests/fakesvc` binary, which arrives with supervision in Phase 2.
