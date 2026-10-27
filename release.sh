#!/usr/bin/env sh
# Prepare releases in CHANGELOG.md first. GitHub Actions is the sole publisher.
# ./release.sh --next major|minor|patch       print the next version
# ./release.sh major|minor|patch|vX.Y.Z       validate, tag and push
set -eu
cd "$(dirname "$0")"

if [ "${1:-}" = "--next" ] && [ "$#" -eq 2 ]; then
 exec ./scripts/next-version.sh "$2"
fi
[ "$#" -eq 1 ] || {
 echo "usage: ./release.sh [--next] major|minor|patch, or ./release.sh vX.Y.Z" >&2
 exit 1
}
case "$1" in
 major | minor | patch) VERSION=$(./scripts/next-version.sh "$1") ;;
 *) VERSION="v${1#v}" ;;
esac

# An explicit version must also be one of the three valid next versions.
valid=0
for bump in major minor patch; do
 [ "$VERSION" != "$(./scripts/next-version.sh "$bump")" ] || valid=1
done
[ "$valid" -eq 1 ] || { echo "error: $VERSION is not the next major, minor or patch version" >&2; exit 1; }
notes=$(./scripts/release-notes.sh "$VERSION")

[ -z "$(git status --porcelain)" ] || { echo "error: commit all changes, including CHANGELOG.md, before releasing" >&2; exit 1; }
[ "$(git branch --show-current)" = main ] || { echo "error: release from main" >&2; exit 1; }
git remote get-url origin >/dev/null
if git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null; then
 echo "error: tag $VERSION already exists" >&2; exit 1
fi
[ -z "$(gofmt -l .)" ] || { echo "error: run gofmt before releasing" >&2; exit 1; }
go vet ./...
go test -race ./...

# Keep the exact changelog text in the annotated tag as well. Workflow notes
# come from the committed changelog, so every public release has a local record.
git tag -a "$VERSION" -m "$notes"
git push --atomic origin HEAD:main "refs/tags/$VERSION"
echo "queued $VERSION - GitHub Actions verifies, builds and publishes the changelog"
echo "https://github.com/matthewsawatzky/shrink/actions/workflows/release.yml"
