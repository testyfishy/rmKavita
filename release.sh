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
#        rmkavita-VERSION-kobo-armv7           the Kobo binary (linux/arm, ARMv7, built twice the same way)
#        rmkavita-VERSION-kobo.zip             extract onto the Kobo's drive: .adds/rmkavita/ (binary, launcher,
#                                              config sample, README), .adds/nm/rmkavita (the NickelMenu entry) and
#                                              the optional Tailscale kit in .adds/tailscale/ (tspanel, built twice the
#                                              same way, ts.sh, boot.sh, the udev rule, NickelMenu entries, the relay
#                                              sample, and KoboRoot.tgz: ts.sh, boot.sh and the rule for the system
#                                              partition). Tailscale's own binaries are not included.
#        SHA256SUMS                            for all of them, in the format `sha256sum -c` reads (run it inside dist/)
#
# The tarball is deterministic where GNU tar is available (sorted names, fixed owner and times, gzip without a name
# or time stamp): the same commit gives the same tarball. Needs: go, tar, gzip, sha256sum (or shasum), a POSIX sh;
# for the Kobo zip, zip or bsdtar (BSDTAR=/path/to/bsdtar to name one).
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

# --- the Kobo build --------------------------------------------------------------------------------------------------
KNAME="rmkavita-$VERSION-kobo-armv7"
KZIP="rmkavita-$VERSION-kobo.zip"
kbuild() {
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -buildvcs=false \
		-ldflags "-s -w -X main.version=$VERSION" -o "$1" "${2:-./cmd/rmkavita}"
}
kbuild dist/.kbuild1
kbuild dist/.kbuild2
K1=$(sha dist/.kbuild1)
K2=$(sha dist/.kbuild2)
if [ "$K1" != "$K2" ]; then
	echo "release.sh: the two Kobo builds differ ($K1 / $K2): not reproducible, stopping" >&2
	exit 1
fi
mv dist/.kbuild1 "dist/$KNAME"
rm -f dist/.kbuild2
echo "binary $KNAME  sha256 $K1"

# tspanel, the Tailscale status page and relay for the Kobo (same target, same check)
kbuild dist/.tbuild1 ./cmd/tspanel
kbuild dist/.tbuild2 ./cmd/tspanel
T1=$(sha dist/.tbuild1)
T2=$(sha dist/.tbuild2)
if [ "$T1" != "$T2" ]; then
	echo "release.sh: the two tspanel builds differ ($T1 / $T2): not reproducible, stopping" >&2
	exit 1
fi
rm -f dist/.tbuild2
echo "binary tspanel (kobo-armv7)  sha256 $T1"

KSTAGE="dist/.kstage"
rm -rf "$KSTAGE"
mkdir -p "$KSTAGE/.adds/rmkavita" "$KSTAGE/.adds/nm" "$KSTAGE/.adds/tailscale"
cp "dist/$KNAME" "$KSTAGE/.adds/rmkavita/rmkavita"
cp package/kobo/launch.sh package/kobo/rmkavita.conf.sample "$KSTAGE/.adds/rmkavita/"
cp package/kobo/README-kobo.md "$KSTAGE/.adds/rmkavita/README.md"
cp LICENSE NOTICE "$KSTAGE/.adds/rmkavita/"
cp package/kobo/nm-rmkavita "$KSTAGE/.adds/nm/rmkavita"
# the optional Tailscale kit; nothing of it goes into .adds/nm, so it adds no menu item until it is set up
mv dist/.tbuild1 "$KSTAGE/.adds/tailscale/tspanel"
for f in ts.sh boot.sh 98-tailscale.rules nm-tailscale nm-calibre relay.conf.sample README-tailscale.md; do
	cp "package/kobo/tailscale/$f" "$KSTAGE/.adds/tailscale/"
done
# KoboRoot.tgz: the system-partition part (copied to .kobo/ by the user, Nickel unpacks it into / and reboots)
RSTAGE="dist/.rstage"
rm -rf "$RSTAGE"
# (state/ is an empty directory entry: tailscaled keeps its sign-in there, and unpacking over an existing one leaves
# its contents alone, so re-installing does not sign the Kobo out)
mkdir -p "$RSTAGE/etc/udev/rules.d" "$RSTAGE/usr/local/tailscale/state"
cp package/kobo/tailscale/98-tailscale.rules "$RSTAGE/etc/udev/rules.d/"
cp package/kobo/tailscale/ts.sh package/kobo/tailscale/boot.sh "$RSTAGE/usr/local/tailscale/"
chmod 0644 "$RSTAGE/etc/udev/rules.d/98-tailscale.rules"
chmod 0755 "$RSTAGE/usr/local/tailscale/ts.sh" "$RSTAGE/usr/local/tailscale/boot.sh"
chmod 0700 "$RSTAGE/usr/local/tailscale/state"
(
	cd "$RSTAGE"
	if tar --version 2>/dev/null | grep -q 'GNU tar'; then
		# every entry named, with its mode set here (file modes on the build host, e.g. Windows, are not reliable)
		R="--owner=0 --group=0 --numeric-owner --mtime=@$EPOCH --no-recursion"
		# shellcheck disable=SC2086
		tar $R --mode=0755 -cf ../.rtar etc etc/udev etc/udev/rules.d usr usr/local usr/local/tailscale
		# shellcheck disable=SC2086
		tar $R --mode=0700 -rf ../.rtar usr/local/tailscale/state
		# shellcheck disable=SC2086
		tar $R --mode=0644 -rf ../.rtar etc/udev/rules.d/98-tailscale.rules
		# shellcheck disable=SC2086
		tar $R --mode=0755 -rf ../.rtar usr/local/tailscale/boot.sh usr/local/tailscale/ts.sh
	else
		tar -cf ../.rtar etc usr
	fi
)
gzip -n -9 -c dist/.rtar > "$KSTAGE/.adds/tailscale/KoboRoot.tgz"
rm -rf "$RSTAGE" dist/.rtar
chmod 0755 "$KSTAGE/.adds/rmkavita/rmkavita" "$KSTAGE/.adds/rmkavita/launch.sh" \
	"$KSTAGE/.adds/tailscale/tspanel" "$KSTAGE/.adds/tailscale/ts.sh" "$KSTAGE/.adds/tailscale/boot.sh"
(
	cd "$KSTAGE"
	if command -v zip >/dev/null 2>&1; then
		zip -X -q -r "../$KZIP" .adds
	elif [ -n "${BSDTAR:-}" ] || command -v bsdtar >/dev/null 2>&1; then
		"${BSDTAR:-bsdtar}" --format zip -cf "../$KZIP" .adds
	else
		echo "release.sh: neither zip nor bsdtar found: no Kobo zip" >&2
		exit 1
	fi
)
rm -rf "$KSTAGE"
echo "kobo zip $KZIP  sha256 $(sha "dist/$KZIP")"

# --- the checksums ---------------------------------------------------------------------------------------------------
(
	cd dist
	{
		echo "$(sha "$NAME")  $NAME"
		echo "$(sha "$NAME.tar.gz")  $NAME.tar.gz"
		echo "$(sha "$KNAME")  $KNAME"
		echo "$(sha "$KZIP")  $KZIP"
	} > SHA256SUMS
)
echo "tarball $NAME.tar.gz  sha256 $(sha "dist/$NAME.tar.gz")"
echo "dist/SHA256SUMS:"
cat dist/SHA256SUMS
echo "contents:"
tar tzf "dist/$NAME.tar.gz"
