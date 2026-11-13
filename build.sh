#!/bin/sh
# Builds edgehop for every supported platform into dist/.
#
#   VERSION=v1.2.3 REPO_URL=https://github.com/owner/edgehop sh build.sh
#
# Both variables are optional; the release workflow sets them.
set -eu
cd "$(dirname "$0")"
mkdir -p dist
export CGO_ENABLED=0
version=${VERSION:-dev}
version=${version#v}
ldflags="-s -w -X main.version=$version -X main.repoURL=${REPO_URL:-}"
for target in windows/amd64 windows/arm64 linux/amd64 linux/arm64 darwin/arm64 darwin/amd64; do
	os=${target%/*}
	arch=${target#*/}
	ext=""
	if [ "$os" = windows ]; then ext=".exe"; fi
	echo "building dist/edgehop-$os-$arch$ext ($version)"
	GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$ldflags" -o "dist/edgehop-$os-$arch$ext" ./cmd/edgehop
done
