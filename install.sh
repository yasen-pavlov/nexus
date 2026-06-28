#!/bin/sh
# Install the nexus-cli binary from GitHub Releases.
#
#   curl -fsSL https://raw.githubusercontent.com/yasen-pavlov/nexus/main/install.sh | sh
#
# Environment overrides:
#   NEXUS_CLI_VERSION=v0.9.0      pin a specific release (default: latest)
#   NEXUS_CLI_INSTALL_DIR=/path   install location (default: /usr/local/bin if
#                                 writable, else ~/.local/bin)
set -eu

REPO="yasen-pavlov/nexus"
BIN="nexus-cli"

err() {
	printf 'install: %s\n' "$1" >&2
	exit 1
}

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
linux | darwin) ;;
*) err "unsupported OS '$os' — build from source with 'go install ./cmd/nexus-cli'" ;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) err "unsupported architecture '$arch'" ;;
esac

# Pick a download tool. Pin curl to HTTPS (incl. redirects) so a downgrade
# redirect can never be followed; GitHub serves HTTPS-only, which covers wget.
if command -v curl >/dev/null 2>&1; then
	dl() { curl -fsSL --proto '=https' --proto-redir '=https' "$1"; }
	dlo() { curl -fsSL --proto '=https' --proto-redir '=https' -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	dl() { wget -qO- "$1"; }
	dlo() { wget -qO "$2" "$1"; }
else
	err "need curl or wget"
fi
command -v tar >/dev/null 2>&1 || err "need tar"
command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1 ||
	err "need sha256sum or shasum to verify the download"

# sha256 helper (sha256sum on Linux, shasum on macOS).
sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$@"
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$@"
	else
		return 1
	fi
}

version="${NEXUS_CLI_VERSION:-}"
if [ -z "$version" ]; then
	version=$(dl "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)
	[ -n "$version" ] || err "could not resolve the latest version — set NEXUS_CLI_VERSION"
fi
# Normalize to a leading-'v' tag so both `0.9.0` and `v0.9.0` overrides resolve:
# the GitHub tag carries the 'v', the GoReleaser archive name does not.
version="v${version#v}"

archive="${BIN}_${version#v}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

printf 'install: downloading %s %s (%s/%s)\n' "$BIN" "$version" "$os" "$arch" >&2
dlo "$base/$archive" "$tmp/$archive" || err "download failed: $base/$archive"

# Verify the checksum — mandatory and fail-closed. Every release publishes
# checksums.txt, so a failed download or a missing/mismatched entry is an error,
# never a silent skip of the integrity check.
dlo "$base/checksums.txt" "$tmp/checksums.txt" || err "could not download checksums.txt to verify $archive"
sum_line=$(grep " ${archive}\$" "$tmp/checksums.txt") || err "no checksum entry for $archive"
(cd "$tmp" && printf '%s\n' "$sum_line" | sha256 -c - >/dev/null 2>&1) || err "checksum verification failed for $archive"
printf 'install: checksum OK\n' >&2

tar -xzf "$tmp/$archive" -C "$tmp" "$BIN" || err "could not extract $BIN from $archive"

dir="${NEXUS_CLI_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
	if [ -w /usr/local/bin ] 2>/dev/null; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir" || err "cannot create install dir $dir"
install -m 0755 "$tmp/$BIN" "$dir/$BIN" 2>/dev/null ||
	{ cp "$tmp/$BIN" "$dir/$BIN" && chmod 0755 "$dir/$BIN"; } ||
	err "could not install to $dir (try NEXUS_CLI_INSTALL_DIR or sudo)"

printf 'install: installed %s to %s/%s\n' "$BIN" "$dir" "$BIN" >&2
# The $PATH in the note below is intentionally literal — it is the command we
# tell the user to run, not a value to expand here.
# shellcheck disable=SC2016
case ":$PATH:" in
*":$dir:"*) ;;
*) printf 'install: note: %s is not on your PATH — add: export PATH="%s:$PATH"\n' "$dir" "$dir" >&2 ;;
esac
"$dir/$BIN" --version 2>/dev/null || true
