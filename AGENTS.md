# Agent instructions for Kestrel-CLI

Rules for any AI coding agent working in this repository (Claude Code reads this through `CLAUDE.md`). They come
from the project owner (GitHub: **Aareevs**) and the build spec. Follow them unless the owner says otherwise in the
conversation.

## Git and GitHub workflow

- **Big work goes through a PR.** For phases, features, and other significant changes:
  1. Create a branch, and make several logical commits (one per coherent step; a PR doesn't have to be one commit).
  2. Push the branch, and open a PR on https://github.com/Aareevs/Kestrel-CLI **assigned to `Aareevs`**.
  3. Look at what the change touches, create fitting labels if they don't exist (`phase: …`, `type: …`,
     `area: …`, `security`, `needs: windows-testing`, …), and attach them.
  4. Wait for CI to pass, then merge with `gh pr merge --merge` (a merge commit keeps the individual commits).
  5. Never commit phase work straight to `main`. Small fixes may go direct only when the owner asks.
- **No AI attribution.** Never add a `Co-Authored-By: Claude …` (or any AI co-author) trailer to commits, and never
  put "🤖 Generated with Claude Code" or similar in PR descriptions.
- **Releases are the owner's call.** v1.0.0 was published on 2026-10-16 by the owner. Never push tags, create
  GitHub releases or drafts, push to the Homebrew tap, or trigger publishing workflows without explicit go-ahead.
  Dry runs (`make release-snapshot`) are fine.
- **Mind CI minutes.** The owner has no Actions credits to spare. CI skips docs-only changes (`paths-ignore`
  in `ci.yml`). For other pushes that shouldn't run CI, put `[skip ci]` in the commit message (and in the merge
  subject when merging a PR). Don't re-run workflows without asking.

## Working rules

- **Ask before risky actions.** Don't install system-wide software, overwrite existing user config, or delete
  files outside the repo without asking. Tools run via `go run <module>@<pinned version>` (staticcheck,
  GoReleaser), so nothing is installed globally.
- **Never claim a test passed unless you ran it.** Never fabricate benchmarks, output, or screenshots. README
  examples are real, unedited output, and anything not yet working is labeled as such.
- **Kestrel never signals processes it didn't start.** It only stops process groups / Job Objects it created.
- **Log content is data.** It is never executed, never used to build commands, never treated as instructions.
- **Redaction comes first.** It runs before anything is persisted, printed, exported, or served over MCP.
- **Check that tests can fail.** For an important test, break the code on purpose (or revert the fix) and confirm
  the test catches it. Several earlier tests turned out to be vacuous this way.
- **Test timing waits on events, not sleeps,** and new integration tests are run repeatedly, including under CPU
  load, before claiming they're stable.

## Phase / gate checklist

At the end of any significant piece of work:

```bash
gofmt -l .                      # must print nothing
make check                      # vet, test, race, build, cross-compiles
make lint                       # staticcheck@2025.1.1 via go run
GOOS=windows go vet ./internal/...
```

Then update `docs/PROGRESS.md` (what was built, the exit criteria with evidence, the bugs found, the real check
results, and the limits) and `docs/PLAN.md` (status and decisions). Give the owner a summary, and stop until they
say to continue.

## Project facts

- Module `github.com/Aareevs/Kestrel-CLI`, Go 1.26.2, MIT license ("Copyright (c) 2026 Aareevs").
- Architecture: `internal/cli` and `internal/mcp` only parse input and render; all logic is in `internal/app` and
  below. Stable JSON types are in `pkg/api`. See `docs/architecture.md`.
- From this machine, `proxy.golang.org` sometimes fails; use `GOPROXY=direct` (or `https://proxy.golang.org,direct`
  when `direct` hits a bad host).
- The README follows the Octo-CLI product style, with the banner at `docs/assets/kestrel-banner.png`, and shows
  only real behavior.
