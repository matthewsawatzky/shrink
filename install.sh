#!/usr/bin/env sh
# install.sh - install shrink from its GitHub releases.
#
# usage: curl -fsSL https://raw.githubusercontent.com/matthewsawatzky/shrink/main/install.sh | sh
#    or: ./install.sh [version]     e.g. ./install.sh v0.1.0 (default: latest)
#    env: SHRINK_INSTALL_DIR=/some/bin  override the install directory
set -eu

REPO="matthewsawatzky/shrink"
VERSION="${1:-}"

say() { printf 'shrink: %s\n' "$*"; }
die() { printf 'shrink: error: %s\n' "$*" >&2; exit 1; }

# --- detect platform ---
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
darwin | linux) ;;
*) die "unsupported OS: $os - releases cover macOS and Linux only" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported architecture: $(uname -m)" ;;
esac

# --- pick the release ---
if [ -z "$VERSION" ]; then
	say "looking up the latest release..."
	VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
	[ -n "$VERSION" ] || die "could not find a release for $REPO"
fi
say "installing shrink $VERSION ($os/$arch)"

asset="shrink-$VERSION-$os-$arch.tar.gz"
url="https://github.com/$REPO/releases/download/$VERSION/$asset"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT

# --- download ---
say "downloading $asset"
curl -fsSL -o "$tmp/$asset" "$url" ||
	die "download failed - $VERSION may not have a $os/$arch build"

# --- verify against the release checksums ---
checksums=$(curl -fsSL "https://github.com/$REPO/releases/download/$VERSION/shrink-$VERSION-checksums.txt" 2>/dev/null || true)
if [ -n "$checksums" ]; then
	want=$(printf '%s\n' "$checksums" | grep "$asset" | cut -d' ' -f1)
	if command -v sha256sum >/dev/null 2>&1; then
		got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
	elif command -v shasum >/dev/null 2>&1; then
		got=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)
	else
		got=""
	fi
	if [ -n "$want" ] && [ -n "$got" ] && [ "$want" != "$got" ]; then
		die "checksum mismatch - the download may be corrupted, try again"
	fi
fi

tar -xzf "$tmp/$asset" -C "$tmp"

# --- install ---
if [ -n "${SHRINK_INSTALL_DIR:-}" ]; then
	bindir=$SHRINK_INSTALL_DIR
	mkdir -p "$bindir"
elif [ -w /usr/local/bin ]; then
	bindir=/usr/local/bin
else
	bindir=$HOME/.local/bin
	mkdir -p "$bindir"
fi

mv -f "$tmp/shrink" "$bindir/shrink"
chmod +x "$bindir/shrink"
say "installed $bindir/shrink"

# --- PATH hint ---
case ":$PATH:" in
*":$bindir:"*) ;;
*)
	printf 'shrink: note: %s is not on your PATH - add it with:
  export PATH="%s:$PATH"
' "$bindir" "$bindir"
	;;
esac

# --- shrink needs ffmpeg/ffprobe to work ---
for dep in ffmpeg ffprobe; do
	command -v "$dep" >/dev/null 2>&1 ||
		printf 'shrink: warning: %s not found - shrink needs it (brew install ffmpeg)\n' "$dep"
done

"$bindir/shrink" -V