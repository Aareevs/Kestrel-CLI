#!/bin/sh
# Kestrel installer: downloads a release archive, verifies its SHA-256
# checksum, and installs the binary. It never uses sudo and never edits your
# shell profile.
#
#   curl -fsSL https://raw.githubusercontent.com/Aareevs/Kestrel-CLI/main/scripts/install.sh | sh
#
# Environment:
#   KESTREL_VERSION        release tag to install, e.g. v0.1.0 (default: latest)
#   KESTREL_INSTALL_DIR    where to put the binary (default: $HOME/.local/bin)
#   KESTREL_DOWNLOAD_BASE  where archives live (default: the GitHub release for
#                          the version); used for mirrors and tests
set -eu

REPO="Aareevs/Kestrel-CLI"

say() { printf 'kestrel-install: %s\n' "$*" >&2; }
die() { say "error: $*"; exit 1; }

need() { command -v "$1" >/dev/null 2>&1; }

fetch() { # fetch URL OUTFILE
	if need curl; then
		curl -fsSL --proto '=https,file' -o "$2" "$1"
	elif need wget; then
		wget -q -O "$2" "$1"
	else
		die "need curl or wget"
	fi
}

sha256() {
	if need sha256sum; then
		sha256sum "$1" | cut -d' ' -f1
	elif need shasum; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "need sha256sum or shasum to verify the download"
	fi
}

os=$(uname -s)
case "$os" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	MINGW* | MSYS* | CYGWIN*) die "on Windows, download the .zip from https://github.com/$REPO/releases" ;;
	*) die "unsupported OS: $os" ;;
esac
arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) die "unsupported architecture: $arch" ;;
esac

version=${KESTREL_VERSION:-}
if [ -z "$version" ]; then
	tmpjson=$(mktemp)
	fetch "https://api.github.com/repos/$REPO/releases/latest" "$tmpjson" ||
		die "could not find the latest release (none published yet?); set KESTREL_VERSION"
	version=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$tmpjson" | head -n 1)
	rm -f "$tmpjson"
	[ -n "$version" ] || die "could not read the latest release tag; set KESTREL_VERSION"
fi
case "$version" in
	v*) ;;
	*) version="v$version" ;;
esac
plain=${version#v}

base=${KESTREL_DOWNLOAD_BASE:-"https://github.com/$REPO/releases/download/$version"}
archive="kestrel_${plain}_${os}_${arch}.tar.gz"
dest=${KESTREL_INSTALL_DIR:-"$HOME/.local/bin"}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "downloading $archive ($version)"
fetch "$base/$archive" "$tmp/$archive" || die "download failed: $base/$archive"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "download failed: $base/checksums.txt"

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || die "$archive is not listed in checksums.txt"
got=$(sha256 "$tmp/$archive")
[ "$want" = "$got" ] || die "checksum mismatch for $archive (expected $want, got $got); nothing was installed"
say "checksum verified"

mkdir -p "$tmp/x"
tar -xzf "$tmp/$archive" -C "$tmp/x" kestrel || die "archive does not contain kestrel"
mkdir -p "$dest"
# Write next to the target, then rename: a running kestrel is never left
# half-overwritten.
cp "$tmp/x/kestrel" "$dest/.kestrel.new"
chmod 755 "$dest/.kestrel.new"
mv -f "$dest/.kestrel.new" "$dest/kestrel"
say "installed $dest/kestrel"

case ":$PATH:" in
	*":$dest:"*) ;;
	*) say "note: $dest is not on your PATH; add it in your shell profile, e.g. export PATH=\"$dest:\$PATH\"" ;;
esac
"$dest/kestrel" version || true
