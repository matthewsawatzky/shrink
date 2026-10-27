#!/usr/bin/env sh
# Print the next version without changing files or creating a tag.
set -eu

bump=${1:-}
case "$bump" in major | minor | patch) ;; *) echo "usage: $0 major|minor|patch [current-version]" >&2; exit 1 ;; esac
if [ "$#" -gt 2 ]; then echo "too many arguments" >&2; exit 1; fi
current=${2:-}
if [ -z "$current" ]; then
 current=$(git tag --sort=-version:refname | awk '/^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/ {print; exit}')
 current=${current:-v0.0.0}
fi
current=${current#v}
printf '%s\n' "$current" | LC_ALL=C grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || {
 echo "invalid version: $current (expected X.Y.Z without leading zeros)" >&2; exit 1;
}
printf '%s\n' "$current" | awk -F. -v bump="$bump" '{
 if (bump == "major") printf "v%.0f.0.0\n", $1 + 1
 else if (bump == "minor") printf "v%s.%.0f.0\n", $1, $2 + 1
 else printf "v%s.%s.%.0f\n", $1, $2, $3 + 1
}'
