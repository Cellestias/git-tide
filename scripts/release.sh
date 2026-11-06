#!/bin/sh
# Build release archives for every supported platform into ./dist.
#
#   scripts/release.sh v1.2.3
#
# Needs: go, tar, zip, and shasum or sha256sum. Pure Go (CGO_ENABLED=0), so it
# cross-compiles from any host.
set -eu

version=${1:-}
case "$version" in
v[0-9]*) ;;
*) echo "usage: $0 vMAJOR.MINOR.PATCH[-suffix]" >&2; exit 2 ;;
esac

module=github.com/cellestias/git-tide
targets="darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64"

root=$(cd "$(dirname "$0")/.." && pwd)
dist=$root/dist
rm -rf "$dist"
mkdir -p "$dist"
cd "$root"

if command -v sha256sum >/dev/null 2>&1; then
	sha() { sha256sum "$@"; }
else
	sha() { shasum -a 256 "$@"; }
fi

for target in $targets; do
	os=${target%/*}
	arch=${target#*/}
	name=gtide_${version}_${os}_${arch}
	stage=$dist/$name
	exe=gtide
	alias=tide
	if [ "$os" = windows ]; then exe=gtide.exe; alias=tide.exe; fi

	echo "building $name"
	mkdir -p "$stage"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
		-ldflags "-s -w -X $module/internal/cli.Version=$version" \
		-o "$stage/$exe" ./cmd/gtide
	cp "$stage/$exe" "$stage/$alias"   # optional short name; a copy so it also works on Windows
	cp README.md "$stage/README.md"

	if [ "$os" = windows ]; then
		(cd "$dist" && zip -q -r "$name.zip" "$name")
	else
		tar -C "$dist" -czf "$dist/$name.tar.gz" "$name"
	fi
	rm -rf "$stage"
done

(cd "$dist" && sha gtide_* > checksums.txt)
echo "done:"
ls -1 "$dist"
