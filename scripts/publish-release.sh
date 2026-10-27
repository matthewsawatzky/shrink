#!/usr/bin/env sh
# Shared by local releases and the optional manual Actions fallback.
# Upload to a draft first so users never see a release with partial assets.
set -eu
[ "$#" -eq 2 ] || { echo "usage: $0 vX.Y.Z notes-file" >&2; exit 1; }
VERSION=$1
notes_file=$2
repo=${GITHUB_REPOSITORY:-matthewsawatzky/shrink}
cd "$(dirname "$0")/.."
printf '%s\n' "$VERSION" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || {
 echo "invalid release version: $VERSION" >&2; exit 1;
}
[ -s "$notes_file" ] || { echo "error: release notes are missing" >&2; exit 1; }
set --
for platform in darwin-arm64 darwin-amd64 linux-arm64 linux-amd64 windows-arm64 windows-amd64; do
 asset="dist/shrink-$VERSION-$platform.tar.gz"
 [ -s "$asset" ] || { echo "error: missing release asset $asset" >&2; exit 1; }
 set -- "$@" "$asset"
done
checksums="dist/shrink-$VERSION-checksums.txt"
[ -s "$checksums" ] || { echo "error: missing checksums" >&2; exit 1; }
for asset do
 name=${asset#dist/}
 [ "$(awk -v name="$name" '$2 == name { n++ } END {print n+0}' "$checksums")" -eq 1 ] || {
  echo "error: missing or duplicate checksum for $name" >&2; exit 1;
 }
done
(
 cd dist
 if command -v sha256sum >/dev/null 2>&1; then sha256sum -c "${checksums#dist/}"
 else shasum -a 256 -c "${checksums#dist/}"; fi
)
set -- "$@" "$checksums"
if state=$(gh release view "$VERSION" --repo "$repo" --json isDraft --jq .isDraft 2>/dev/null); then
 [ "$state" = true ] || { echo "error: $VERSION is already published; it will not be modified" >&2; exit 1; }
 gh release upload "$VERSION" "$@" --repo "$repo" --clobber
else
 gh release create "$VERSION" "$@" --draft --verify-tag --repo "$repo" --title "shrink $VERSION" --notes-file "$notes_file"
fi
gh release edit "$VERSION" --repo "$repo" --draft=false --latest --verify-tag --notes-file "$notes_file"
gh release view "$VERSION" --repo "$repo" --json url --jq .url
