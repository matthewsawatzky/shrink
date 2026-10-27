#!/usr/bin/env sh
# Extract one dated changelog section. Shared by local release and CI.
set -eu
version=${1:-}
version=${version#v}
[ "$#" -ge 1 ] && [ "$#" -le 2 ] || { echo "usage: $0 vX.Y.Z [changelog]" >&2; exit 1; }
printf '%s\n' "$version" | LC_ALL=C grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || {
 echo "invalid release version: $version" >&2; exit 1;
}
changelog=${2:-CHANGELOG.md}
awk -v version="$version" '
 /^## / {
  active = 0
  prefix = "## [" version "] - "
  if (index($0, prefix) == 1 && substr($0, length(prefix)+1) ~ /^[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]$/) {
   sections++; active = 1
  }
  next
 }
 active {
  notes = notes $0 "\n"
  if ($0 !~ /^[[:space:]]*$/ && $0 !~ /^#/) content++
 }
 END {
  if (sections != 1 || content == 0) {
   print "expected exactly one non-empty dated changelog entry for " version > "/dev/stderr"
   exit 1
  }
  sub(/^[[:space:]]*\n/, "", notes)
  sub(/[[:space:]]*$/, "", notes)
  print notes
 }
' "$changelog"
