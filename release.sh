#!/usr/bin/env sh
# release.sh - tag, push and publish a shrink release on GitHub.
#
# usage: ./release.sh <version> ["release notes"]
# e.g.:  ./release.sh v0.1.0 "first release - compress, convert, shrink"
#
# requires: a clean committed tree, the gh CLI logged in
# (brew install gh && gh auth login), and build.sh next to this file.
set -eu

REPO="matthewsawatzky/shrink"

VERSION="${1:-}"
NOTES="${2:-}"

[ -n "$VERSION" ] || {
	echo "usage: ./release.sh <version> [notes]   e.g. ./release.sh v0.1.0" >&2
	exit 1
}
case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
[ -n "$NOTES" ] || NOTES="shrink $VERSION"

command -v gh >/dev/null 2>&1 || {
	echo "error: the GitHub CLI is required: brew install gh && gh auth login" >&2
	exit 1
}
gh auth status >/dev/null 2>&1 || {
	echo "error: not logged in to GitHub: run gh auth login" >&2
	exit 1
}
git remote get-url origin >/dev/null 2>&1 || {
	echo "error: no 'origin' remote - set it up first:
  git remote add origin https://github.com/$REPO.git" >&2
	exit 1
}
[ -n "$(git status --porcelain)" ] && {
	echo "error: uncommitted changes - commit before releasing" >&2
	exit 1
}
if git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null; then
	echo "error: tag $VERSION already exists" >&2
	exit 1
fi

./build.sh "$VERSION"

git tag -a "$VERSION" -m "shrink $VERSION"
git push origin HEAD
git push origin "$VERSION"

gh release create "$VERSION" dist/* \
	--repo "$REPO" \
	--title "shrink $VERSION" \
	--notes "$NOTES"

echo
echo "released shrink $VERSION -> https://github.com/$REPO/releases/tag/$VERSION"