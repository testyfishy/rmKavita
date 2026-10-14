#!/bin/sh
# Builds the static linux/arm64 binary into package/rmkavita. Reproducible: -trimpath, no VCS stamp, no cgo.
# release.sh builds it twice, compares and writes the tarball and SHA256SUMS.
set -eu
cd "$(dirname "$0")"
VERSION="${VERSION:-$(sed -n 's/^var version = "\(.*\)"$/\1/p' cmd/rmkavita/main.go)}"
mkdir -p package
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -buildvcs=false \
  -ldflags "-s -w -X main.version=$VERSION" -o package/rmkavita ./cmd/rmkavita
echo "built package/rmkavita ($VERSION)"