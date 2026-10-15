#!/usr/bin/env sh
# uninstall.sh - remove shrink from this machine.
#
# usage: curl -fsSL https://raw.githubusercontent.com/matthewsawatzky/shrink/main/uninstall.sh | sh
#
# Removes the standalone binary from every place install.sh puts one,
# the Homebrew package if shrink was installed that way, and the shell
# completions the README suggests. ffmpeg is left alone - many other
# tools use it.
set -eu

say() { printf 'shrink: %s\n' "$*"; }
removed=0

# remove_binary deletes the binary from one directory, if present.
remove_binary() {
	[ -n "${1:-}" ] || return 0
	target="$1/shrink"
	[ -e "$target" ] || return 0
	if rm -f "$target" 2>/dev/null; then
		say "removed $target"
		removed=1
	else
		say "could not remove $target - try: sudo rm $target"
	fi
}

# Homebrew-managed installs go through brew so the Cellar is cleaned too
if command -v brew >/dev/null 2>&1 && brew list --formula shrink >/dev/null 2>&1; then
	say "removing the Homebrew package (brew uninstall shrink)"
	brew uninstall shrink
	say "the tap can go too, if you want: brew untap matthewsawatzky/tap"
	removed=1
fi

# standalone binaries: everywhere install.sh or "go install" put one
remove_binary "${SHRINK_INSTALL_DIR:-}"
remove_binary /usr/local/bin
remove_binary "$HOME/.local/bin"
if command -v go >/dev/null 2>&1; then
	remove_binary "$(go env GOPATH)/bin"
fi
if command -v brew >/dev/null 2>&1; then
	remove_binary "$(brew --prefix)/bin"
fi

# shell completions the README suggests installing
for comp in "$HOME/.zfunc/_shrink" /usr/local/etc/bash_completion.d/shrink; do
	if [ -f "$comp" ]; then
		if rm -f "$comp" 2>/dev/null; then
			say "removed completion $comp"
			removed=1
		else
			say "could not remove completion $comp"
		fi
	fi
done

if command -v shrink >/dev/null 2>&1; then
	say "note: shrink is still on PATH at $(command -v shrink) - remove it manually"
elif [ "$removed" -gt 0 ]; then
	say "shrink is uninstalled"
else
	say "shrink was not found - nothing to remove"
fi

say "ffmpeg was left alone (other tools use it); remove it with your package manager if you want"