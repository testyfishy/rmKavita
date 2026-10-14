#!/bin/sh
# release.sh - builds the release artefacts of rmKavita reproducibly.
#
#   ./release.sh
#
# What it does:
#   1. reads the version from cmd/rmkavita/main.go
#   2. builds the static linux/arm64 binary TWICE (-trimpath, no VCS stamp, no cgo) and stops unless both builds are
#      byte for byte the same
#   3. writes dist/ :
#        rmkavita-VERSION-linux-arm64          the binary
#        rmkavita-VERSION-linux-arm64.tar.gz   package/ (binary, manifest, icon, config sample) + install/ (the ssh
#                                              installer with its door unit templates) + LICENSE, NOTICE, CREDITS.md,
#                                              README.md, CHANGELOG.md
#        SHA256SUMS                            for both, in the format `sha256sum -c` reads (run it inside dist/)
#
# The tarball is deterministic where GNU tar is available (sorted names, fixed owner and times, gzip without a name
# or time stamp): the same commit gives the same tarball. Needs: go, tar, gzip, sha256sum (or shasum), a POSIX sh.
set -eu
cd "$(dirname "$0")"

VERSION=$(sed -n 's/^var version = "\(.*\)"$/\1/p' cmd/rmkavita/main.go)
[ -n "$VERSION" ] || { echo "release.sh: cannot read the version from cmd/rmkavita/main.go" >&2; exit 1; }
NAME="rmkavita-$VERSION-linux-arm64"
TOP="rmkavita-$VERSION"

sha() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}
build() {
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -buildvcs=false \
		-ldflags "-s -w -X main.version=$VERSION" -o "$1" ./cmd/rmkavita
}

rm -rf dist
mkdir -p dist package
build dist/.build1
build dist/.build2
H1=$(sha dist/.build1)
H2=$(sha dist/.build2)
if [ "$H1" != "$H2" ]; then
	echo "release.sh: the two builds differ ($H1 / $H2): not reproducible, stopping" >&2
	exit 1
fi
mv dist/.build1 "dist/$NAME"
rm -f dist/.build2
cp "dist/$NAME" package/rmkavita
echo "binary $NAME  sha256 $H1"

# --- the tarball -----------------------------------------------------------------------------------------------------
STAGE="dist/.stage"
rm -rf "$STAGE"
mkdir -p "$STAGE/$TOP/package" "$STAGE/$TOP/install"
cp package/rmkavita package/external.manifest.json package/icon.png package/rmkavita.conf.sample "$STAGE/$TOP/package/"
cp -R install/. "$STAGE/$TOP/install/"
rm -f "$STAGE/$TOP/install/install.conf"
cp LICENSE NOTICE CREDITS.md README.md CHANGELOG.md "$STAGE/$TOP/"

EPOCH="${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct 2>/dev/null || echo 1767225600)}"
(
	cd "$STAGE"
	find "$TOP" -type f | LC_ALL=C sort > ../.files
	# executables: the binary and the installer's shell scripts; everything else is 0644
	grep -E '/package/rmkavita$|\.sh$' ../.files > ../.exec || true
	grep -v -E '/package/rmkavita$|\.sh$' ../.files > ../.data || true
	if tar --version 2>/dev/null | grep -q 'GNU tar'; then
		TAROPT="--owner=0 --group=0 --numeric-owner --mtime=@$EPOCH"
	else
		TAROPT=""
	fi
	# shellcheck disable=SC2086
	tar $TAROPT --mode='u=rw,go=r' -cf ../.tar -T ../.data
	# shellcheck disable=SC2086
	[ ! -s ../.exec ] || tar $TAROPT --mode='u=rwx,go=rx' -rf ../.tar -T ../.exec
)
gzip -n -9 -c dist/.tar > "dist/$NAME.tar.gz"
rm -rf "$STAGE" dist/.files dist/.exec dist/.data dist/.tar

# --- the checksums ---------------------------------------------------------------------------------------------------
(
	cd dist
	{
		echo "$(sha "$NAME")  $NAME"
		echo "$(sha "$NAME.tar.gz")  $NAME.tar.gz"
	} > SHA256SUMS
)
echo "tarball $NAME.tar.gz  sha256 $(sha "dist/$NAME.tar.gz")"
echo "dist/SHA256SUMS:"
cat dist/SHA256SUMS
echo "contents:"
tar tzf "dist/$NAME.tar.gz"
