#!/usr/bin/env bash
#
# install.sh — install the `ap` binary from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/ackstorm/agent-profile/main/install.sh | bash
#
# Options, all via environment variables:
#
#   VERSION=v0.1.0   install a specific tag instead of the latest release
#   PREFIX=/usr/local/bin   install somewhere else (default ~/.local/bin)
#
#   curl -fsSL .../install.sh | VERSION=v0.1.0 PREFIX=/usr/local/bin bash
#
# The download is checksum-verified against the release's checksums.txt before
# anything is written to PREFIX. If you would rather read before you run — which
# is the correct instinct for any `curl | bash` — download it first.

set -euo pipefail

REPO="${REPO:-ackstorm/agent-profile}"
PREFIX="${PREFIX:-$HOME/.local/bin}"
VERSION="${VERSION:-latest}"

BIN=ap
PROJECT=agent-profile

die() {
	echo "install.sh: $*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is required but not on PATH"
}

warn_path() {
	case ":${PATH}:" in
	*":$1:"*) return 0 ;;
	esac
	echo "install.sh: NOTE $1 is not on your PATH"
	echo "install.sh:   add this to your shell profile (~/.zshrc, ~/.bashrc):"
	echo "install.sh:     export PATH=\"$1:\$PATH\""
}

need curl
need tar

# --- what are we running on -------------------------------------------------

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
linux | darwin) ;;
*) die "unsupported OS '$os' — ap is built for linux and darwin only" ;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported architecture '$arch' — ap is built for amd64 and arm64 only" ;;
esac

# --- which release ----------------------------------------------------------

if [ "$VERSION" = latest ]; then
	# Resolve the tag without jq: the API is not going to reorder its own JSON,
	# and an empty result is caught right below either way.
	VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
		grep -m1 '"tag_name"' | cut -d'"' -f4 || true)
	[ -n "$VERSION" ] || die "could not resolve the latest release of ${REPO} — set VERSION=vX.Y.Z to pin one"
fi

# goreleaser names archives with the tag minus its leading v.
version_no_v=${VERSION#v}
archive="${PROJECT}_${version_no_v}_${os}_${arch}.tar.gz"
base="https://github.com/${REPO}/releases/download/${VERSION}"

# --- fetch, verify, install -------------------------------------------------

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "install.sh: downloading ${archive} (${VERSION})"
curl -fsSL -o "${tmp}/${archive}" "${base}/${archive}" ||
	die "no such asset: ${base}/${archive}"
curl -fsSL -o "${tmp}/checksums.txt" "${base}/checksums.txt" ||
	die "release ${VERSION} has no checksums.txt — refusing to install unverified"

# sha256sum on Linux, shasum -a 256 on macOS. Verify only our own line: the
# checksums file covers every platform's archive, and -c fails on missing files.
grep " ${archive}\$" "${tmp}/checksums.txt" > "${tmp}/want.txt" ||
	die "${archive} is not listed in checksums.txt — refusing to install unverified"

if command -v sha256sum >/dev/null 2>&1; then
	(cd "$tmp" && sha256sum -c want.txt >/dev/null) || die "checksum mismatch for ${archive}"
elif command -v shasum >/dev/null 2>&1; then
	(cd "$tmp" && shasum -a 256 -c want.txt >/dev/null) || die "checksum mismatch for ${archive}"
else
	die "neither sha256sum nor shasum found — cannot verify the download"
fi
echo "install.sh: checksum OK"

tar -xzf "${tmp}/${archive}" -C "$tmp" "$BIN"

mkdir -p "$PREFIX"
install -m 0755 "${tmp}/${BIN}" "${PREFIX}/${BIN}" 2>/dev/null ||
	die "cannot write to ${PREFIX} — set PREFIX to a directory you own, or re-run with sudo"

echo "install.sh: installed ${PREFIX}/${BIN}"
"${PREFIX}/${BIN}" version

warn_path "$PREFIX"
# Always, whatever PREFIX was: `ap create` writes its wrappers here regardless, so
# installing ap to /usr/local/bin still leaves you unable to run `claude:plan`.
link_dir="${HOME}/.local/bin"
[ "$link_dir" = "$PREFIX" ] || warn_path "$link_dir"
