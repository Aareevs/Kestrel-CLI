# Releasing

Kestrel is released with [GoReleaser](https://goreleaser.com) v2 (pinned to v2.18.2). v1.0.0 was published on
2026-10-16. You can dry-run everything below locally as a snapshot, which builds every artifact and publishes
nothing.

## What a release produces

| Artifact | Details |
|---|---|
| Binaries | darwin/arm64, darwin/amd64, linux/arm64, linux/amd64, windows/amd64. Static (`CGO_ENABLED=0`; SQLite is pure Go), `-trimpath`, version/commit/date set via `-ldflags`. |
| Archives | `kestrel_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows), each with `kestrel`, `LICENSE`, and `README.md`. The install script depends on this name. |
| `checksums.txt` | SHA-256 of every archive. |
| Homebrew formula | `Formula/kestrel.rb`, rendered by [`scripts/homebrew-formula.sh`](../scripts/homebrew-formula.sh) from `checksums.txt` and pushed to the tap **when the release is published** ([`homebrew.yml`](../.github/workflows/homebrew.yml)). |
| GitHub release | Created as a **draft**, with a changelog from commit subjects (docs/test commits and merges left out). |

Config: [`.goreleaser.yaml`](../.goreleaser.yaml).

## Dry run (any time)

```bash
make release-check      # validate .goreleaser.yaml
make release-snapshot   # build everything into dist/, publish nothing
```

Both use `go run`, so GoReleaser lands in the Go module cache and is not installed globally. GoReleaser v2.18.2
itself needs Go ≥ 1.27.1, so `go run` fetches that toolchain into the module cache too, and local snapshot
binaries are built with it. The release workflow builds with the Go version in `go.mod`.

Try the installer against the snapshot (it needs a directory path without spaces, because it becomes a `file://`
URL):

```bash
mkdir -p /tmp/kestrel-rel && cp dist/*.tar.gz dist/checksums.txt /tmp/kestrel-rel/
KESTREL_VERSION=<snapshot version from dist/> KESTREL_DOWNLOAD_BASE=file:///tmp/kestrel-rel \
  KESTREL_INSTALL_DIR=/tmp/kestrel-bin sh scripts/install.sh
```

## Before the first release (one-time setup)

1. ✅ **Create the tap repository** `github.com/Aareevs/homebrew-tap` (public). Done 2026-10-15.
2. ✅ **Create a token** that can push to it: a fine-grained PAT with *Contents: read and write* on `homebrew-tap`
   only, saved in this repository's Actions secrets as `HOMEBREW_TAP_TOKEN`. Done 2026-10-15. Whether it can
   push is first exercised when a release is published. If the `homebrew` workflow fails then, create a new
   token, update the secret, and re-run the workflow. Renew the token before it expires (it was created with a
   one-year expiry), or the `homebrew` workflow will fail on the next release.
3. **Check the README** install section: it says the one-liner and Homebrew start working with the first release.

## Cutting a release

1. Make sure `main` is green and `docs/PROGRESS.md` is current.
2. Tag and push: `git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0`.
3. [`.github/workflows/release.yml`](../.github/workflows/release.yml) runs: `go test -race ./...`, then
   GoReleaser. It builds the artifacts and creates a **draft** release.
4. Review the draft on GitHub (assets, changelog), then **publish** it. Publishing triggers
   [`.github/workflows/homebrew.yml`](../.github/workflows/homebrew.yml), which renders `Formula/kestrel.rb` from
   the release's `checksums.txt` and pushes it to the tap. The formula is pushed only after publishing, so it
   never points at downloads that don't exist yet. The install script's "latest" lookup also works only once the
   release is published.

Nothing happens unless a `v*` tag is pushed. Pushing a tag only produces a draft.

## The install script

[`scripts/install.sh`](../scripts/install.sh) (POSIX `sh`):

1. Detects the OS (macOS/Linux) and architecture (amd64/arm64).
2. Picks `KESTREL_VERSION`, or the latest published release.
3. Downloads the archive and `checksums.txt`.
4. Verifies the SHA-256 and **installs nothing on a mismatch**.
5. Installs to `KESTREL_INSTALL_DIR` (default `~/.local/bin`).

It never uses `sudo` and never edits shell profiles; it only tells you when the directory isn't on your `PATH`.
`KESTREL_DOWNLOAD_BASE` points it at a mirror or a local directory. `TestInstallScript` covers the happy path, a
tampered archive, and a missing version.

## Notes

- **Not notarized.** The binaries aren't signed or notarized for macOS. Homebrew formula installs and the install
  script's `curl` downloads aren't quarantined, so Gatekeeper doesn't block them. A browser download from the
  Releases page is quarantined (remove it with `xattr -d com.apple.quarantine kestrel`). Notarization would need an
  Apple Developer account.
- **A formula, not a cask.** Formulas are the standard way to ship a CLI on both macOS and Linux (the same
  approach as `Octo-sh/homebrew-tap`). GoReleaser v2 generates only casks without deprecation warnings, and its
  deprecated `brews` option fails `goreleaser check`. So the formula is rendered by
  `scripts/homebrew-formula.sh` (tested by `TestHomebrewFormula`, including that a missing archive fails rather
  than yielding an empty checksum). Install: `brew install aareevs/tap/kestrel`.
