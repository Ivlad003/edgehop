#!/bin/sh
# Builds edgehop for every supported platform into dist/.
set -eu
cd "$(dirname "$0")"
mkdir -p dist
export CGO_ENABLED=0
for target in windows/amd64 windows/arm64 linux/amd64 linux/arm64 darwin/arm64 darwin/amd64; do
	os=${target%/*}
	arch=${target#*/}
	ext=""
	if [ "$os" = windows ]; then ext=".exe"; fi
	echo "building dist/edgehop-$os-$arch$ext"
	GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w" -o "dist/edgehop-$os-$arch$ext" ./cmd/edgehop
done
