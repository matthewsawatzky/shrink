#!/usr/bin/env sh
# Releases are checked and built locally; GitHub only stores the published assets.
# ./release.sh --next major|minor|patch       preview a version
# ./release.sh major|minor|patch|vX.Y.Z       check, build, tag, push and publish
# ./release.sh --resume vX.Y.Z               retry an interrupted release
set -eu
cd "$(dirname "$0")"

if [ "${1:-}" = "--next" ] && [ "$#" -eq 2 ]; then
 exec ./scripts/next-version.sh "$2"
fi
resume=0
if [ "${1:-}" = "--resume" ] && [ "$#" -eq 2 ]; then
 resume=1
 shift
fi
[ "$#" -eq 1 ] || {
 echo "usage: ./release.sh [--next] major|minor|patch, ./release.sh vX.Y.Z, or ./release.sh --resume vX.Y.Z" >&2
 exit 1
}
for tool in go git gh tar ffmpeg ffprobe; do
 command -v "$tool" >/dev/null || { echo "error: release requires $tool" >&2; exit 1; }
done
if ! command -v sha256sum >/dev/null && ! command -v shasum >/dev/null; then
 echo "error: release requires sha256sum or shasum" >&2; exit 1
fi
gh auth status >/dev/null 2>&1 || { echo "error: run gh auth login before releasing" >&2; exit 1; }
[ -z "$(git status --porcelain)" ] || { echo "error: commit all changes, including CHANGELOG.md, before releasing" >&2; exit 1; }
[ "$(git branch --show-current)" = main ] || { echo "error: release from main" >&2; exit 1; }
git fetch origin --tags

case "$1" in
 major | minor | patch)
  [ "$resume" -eq 0 ] || { echo "error: --resume needs an exact version" >&2; exit 1; }
  VERSION=$(./scripts/next-version.sh "$1") ;;
 *) VERSION="v${1#v}" ;;
esac
notes_dir=$(mktemp -d)
trap 'rm -rf "$notes_dir"' EXIT HUP INT TERM
./scripts/release-notes.sh "$VERSION" > "$notes_dir/notes.md"
if [ "$resume" -eq 1 ]; then
 [ "$(git rev-parse "$VERSION^{commit}")" = "$(git rev-parse HEAD)" ] || {
  echo "error: check out the release commit on main before resuming" >&2; exit 1;
 }
else
 valid=0
 for bump in major minor patch; do
  [ "$VERSION" != "$(./scripts/next-version.sh "$bump")" ] || valid=1
 done
 [ "$valid" -eq 1 ] || { echo "error: $VERSION is not the next major, minor or patch version" >&2; exit 1; }
 if git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null; then
  echo "error: tag $VERSION already exists; use --resume $VERSION" >&2; exit 1
 fi
fi

[ -z "$(gofmt -l .)" ] || { echo "error: run gofmt before releasing" >&2; exit 1; }
go vet ./...
go test -race ./...
./build.sh "$VERSION"

if [ "$resume" -eq 0 ]; then git tag -a "$VERSION" -F "$notes_dir/notes.md"; fi
git push --atomic origin HEAD:main "refs/tags/$VERSION"
./scripts/publish-release.sh "$VERSION" "$notes_dir/notes.md"
