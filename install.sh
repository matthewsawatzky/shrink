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

# --- shrink needs ffmpeg/ffprobe to work; offer to install if missing ---

# ffmpeg_install_cmd prints the command that installs ffmpeg on this
# platform, or nothing when no known package manager is present.
ffmpeg_install_cmd() {
	case "$os" in
	darwin)
		if command -v brew >/dev/null 2>&1; then
			echo "brew install ffmpeg"
		fi
		;;
	linux)
		if command -v apt-get >/dev/null 2>&1; then
			echo "sudo apt-get install -y ffmpeg"
		elif command -v dnf >/dev/null 2>&1; then
			echo "sudo dnf install -y ffmpeg"
		elif command -v pacman >/dev/null 2>&1; then
			echo "sudo pacman -S --noconfirm ffmpeg"
		elif command -v apk >/dev/null 2>&1; then
			echo "sudo apk add ffmpeg"
		elif command -v zypper >/dev/null 2>&1; then
			echo "sudo zypper install -y ffmpeg"
		fi
		;;
	esac
}

# ensure_ffmpeg asks whether to install ffmpeg when it is missing and
# runs the platform's package manager on yes. The prompt reads /dev/tty
# so it still works when the script itself is piped in (curl | sh);
# without a terminal it falls back to printing the hint.
ensure_ffmpeg() {
	if command -v ffmpeg >/dev/null 2>&1; then
		if ! command -v ffprobe >/dev/null 2>&1; then
			say "warning: ffprobe not found - complete ffmpeg installs ship both; reinstall ffmpeg"
		fi
		return
	fi

	cmd=$(ffmpeg_install_cmd)
	if [ -z "$cmd" ]; then
		say "warning: ffmpeg not found - shrink needs it (ffmpeg installs ship ffprobe too)"
		say "  install it from https://ffmpeg.org/download.html"
		if [ "$os" = "darwin" ]; then
			say "  on macOS: install Homebrew (https://brew.sh), then: brew install ffmpeg"
		fi
		return
	fi

	answer=""
	if [ -c /dev/tty ]; then
		printf 'shrink: ffmpeg is not installed. Install it now with "%s"? [y/N] ' "$cmd"
		read answer </dev/tty || answer=""
	fi
	case "$answer" in
	y | Y | yes | Yes | YES)
		say "running: $cmd"
		if $cmd; then
			if command -v ffmpeg >/dev/null 2>&1; then
				say "ffmpeg installed - shrink is ready"
			else
				say "warning: still no ffmpeg on PATH - check the output above"
			fi
		else
			say "warning: the install command failed - check the output above"
		fi
		;;
	*)
		say "note: shrink needs ffmpeg - install it yourself with: $cmd"
		;;
	esac
}

ensure_ffmpeg

"$bindir/shrink" -V