#!/usr/bin/env sh
# build.sh - build shrink.
#
# usage: ./build.sh            -> local binary (./shrink, version "dev")
#        ./build.sh <version>  -> cross-compiled release archives in dist/
#                                e.g. ./build.sh v0.1.0
set -eu

VERSION="${1:-}"

# local build: one binary for this machine
if [ -z "$VERSION" ]; then
	go build -trimpath -ldflags "-s -w" -o shrink .
	echo "built ./shrink"
	exit 0
fi

case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac

rm -rf dist
mkdir -p dist

# CGO disabled so every binary is fully static and portable
for platform in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
	os=${platform%/*}
	arch=${platform#*/}
	bin=shrink
	if [ "$os" = "windows" ]; then
		bin=shrink.exe
	fi
	echo "building shrink-$VERSION-$os-$arch"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch \
		go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
		-o "dist/shrink-$VERSION-$os-$arch/$bin" .
	tar -czf "dist/shrink-$VERSION-$os-$arch.tar.gz" \
		-C "dist/shrink-$VERSION-$os-$arch" "$bin"
	rm -rf "dist/shrink-$VERSION-$os-$arch"
done

(
	cd dist
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum shrink-*.tar.gz >"shrink-$VERSION-checksums.txt"
	else
		shasum -a 256 shrink-*.tar.gz >"shrink-$VERSION-checksums.txt"
	fi
)

echo "release artifacts for $VERSION in dist/"