#!/bin/sh
# rmKavita installer for the reMarkable Paper Pro - HOST side.
#
# POSIX sh: runs on Linux, macOS, Git Bash and WSL. It talks to the tablet over ssh (key based) and needs
# nothing else on the tablet than the shell and busybox applets it already has. It never asks for, reads, prints
# or stores a Kavita password or API key: you type those on the app's own Sign-in screen.
#
#   ./install.sh            install or upgrade (asks for confirmation; --yes skips the question)
#   ./install.sh -n         dry run: prints the plan, runs only read-only checks, changes nothing
#   ./install.sh -u         uninstall (app folder, config, settings, optional door; keeps reading positions)
#   ./install.sh -u --purge ... and the reading positions too
#   -f / --force            go on although KOReader or rmKavita is running on the tablet
#   -c / --config FILE      configuration file (default: install.conf next to this script)
#
# What an install does, in order (idempotent: run it again any time):
#   1. checks the package on this computer: static AArch64 ELF, manifest names rmkavita, no key-shaped text
#   2. looks at the tablet read-only: model (Ferrari/Chiappa), AppLoad present, free space, KOReader / rmKavita
#      running (refuses unless --force), the config only COUNTED (never read), AppLoad version, fb_format
#   3. uploads the package to a staging folder and verifies the binary's sha256 on the tablet
#   4. places the app folder (built in the staging folder, then swapped in with renames; the old one is put back
#      if the swap fails)
#   5. optional door (DOOR=yes): systemd socket -> `tailscale nc` -> your Kavita server, installed detached
#   6. config: written from the sample ONLY when there is none (never overwrites a signed-in config), then
#      `rmkavita -migrate` runs on the tablet (strips v1-style keys, keeps a stored sign-in)
#   7. final check, counts only: config mode 600, one `server =` line, at most one api_key line (with a username
#      line), nothing else key-shaped. It does NOT restart xochitl/AppLoad: tap AppLoad's reload button.

set -u
umask 077

SELF_DIR=$(cd "$(dirname "$0")" && pwd) || exit 1

# ---- the app's fixed locations on the device --------
CONF=/home/root/.config/rmkavita.conf
SETTINGS=/home/root/.config/rmkavita-settings.json
STATE=/home/root/.config/rmkavita-state.json
APP_LOG=/tmp/rmkavita.log
# legacy prototype names, handed to `rmkavita -migrate` (nothing to migrate on a fresh device)
OLD_CONF=/home/root/.config/kavita-reader.conf
OLD_STATE=/home/root/.config/kavita-reader-state.json
UNIT_DIR=/etc/systemd/system
DOOR_SOCKET=rmkavita-door.socket
DOOR_SERVICE=rmkavita-door@.service
# the standard device layout when the tablet was set up with Vellum (documented in README-install.md)
VELLUM_BIN=/home/root/.vellum/bin

# ---- configuration defaults (install.conf overrides) --------
TABLET_HOST=''
TABLET_USER=root
SSH_PORT=22
SSH_KEY=''
SSH_STRICT=accept-new
SSH_EXTRA=''
APP_DIR=/home/root/xovi/exthome/appload/rmkavita
PACKAGE_DIR=''
STAGE_DIR=/home/root/rmkavita-stage
DOOR=no
KAVITA_HOST=''
KAVITA_PORT=5000
DOOR_LISTEN=127.0.0.1:5001
TAILSCALE_BIN=$VELLUM_BIN/tailscale
ROOTFS_RW_CMD=$VELLUM_BIN/mount-rw
ROOTFS_RO_CMD=$VELLUM_BIN/mount-restore
SSH_BIND_SRC=''

DRY=no
UNINSTALL=no
FORCE=no
YES=no
PURGE=no
CONF_FILE=''

# ---- output --------
say() { printf '%s\n' "$*"; }
warn() { printf '%s\n' "   WARNING: $*"; }
die() { printf '%s\n' "ERROR: $*" >&2; exit 1; }

# anything key-shaped in text that came back from the tablet is hidden before it is printed
redact() {
  sed -E 's#/api/opds/[A-Za-z0-9_-]+#/api/opds/<hidden>#g; s#[A-Za-z0-9_-]{32,}#<hidden>#g'
}

usage() {
  sed -n '2,/^set -u/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
}

# ---- arguments --------
while [ $# -gt 0 ]; do
  case "$1" in
    -n|--dry-run) DRY=yes;;
    -u|--uninstall) UNINSTALL=yes;;
    -f|--force) FORCE=yes;;
    -y|--yes) YES=yes;;
    --purge) PURGE=yes;;
    -c|--config)
      [ $# -ge 2 ] || die "$1 needs a file name"
      CONF_FILE=$2; shift;;
    --config=*) CONF_FILE=${1#--config=};;
    -h|--help) usage; exit 0;;
    *) die "unknown option: $1 (try --help)";;
  esac
  shift
done
if [ "$PURGE" = yes ] && [ "$UNINSTALL" != yes ]; then die "--purge only makes sense with --uninstall"; fi
[ -n "$CONF_FILE" ] || CONF_FILE=$SELF_DIR/install.conf

# ---- configuration file: read line by line, never executed --------
# trim VALUE -> TRIMMED (no subshell: process creation is slow on Windows)
trim() {
  TRIMMED=$1
  TRIMMED=${TRIMMED#"${TRIMMED%%[![:space:]]*}"}
  TRIMMED=${TRIMMED%"${TRIMMED##*[![:space:]]}"}
}

load_conf() {
  [ -f "$1" ] || die "configuration file not found: $1 (copy install.conf.sample to install.conf and edit it)"
  tr -d '\r' < "$1" > "$TMP/install.conf.lf" || die "cannot read $1"
  lc_n=0
  while IFS= read -r lc_line || [ -n "$lc_line" ]; do
    lc_n=$((lc_n + 1))
    trim "$lc_line"; lc_t=$TRIMMED
    case "$lc_t" in ''|'#'*) continue;; esac
    case "$lc_t" in *=*) ;; *) die "$1 line $lc_n: expected KEY=VALUE";; esac
    trim "${lc_t%%=*}"; lc_k=$TRIMMED
    trim "${lc_t#*=}"; lc_v=$TRIMMED
    case "$lc_v" in
      \"*\") lc_v=${lc_v#\"}; lc_v=${lc_v%\"};;
      \'*\') lc_v=${lc_v#\'}; lc_v=${lc_v%\'};;
    esac
    case "$lc_k" in
      TABLET_HOST) TABLET_HOST=$lc_v;;
      TABLET_USER) TABLET_USER=$lc_v;;
      SSH_PORT) SSH_PORT=$lc_v;;
      SSH_KEY) SSH_KEY=$lc_v;;
      SSH_STRICT) SSH_STRICT=$lc_v;;
      SSH_EXTRA) SSH_EXTRA=$lc_v;;
      APP_DIR) APP_DIR=$lc_v;;
      PACKAGE_DIR) PACKAGE_DIR=$lc_v;;
      STAGE_DIR) STAGE_DIR=$lc_v;;
      DOOR) DOOR=$lc_v;;
      KAVITA_HOST) KAVITA_HOST=$lc_v;;
      KAVITA_PORT) KAVITA_PORT=$lc_v;;
      DOOR_LISTEN) DOOR_LISTEN=$lc_v;;
      TAILSCALE_BIN) TAILSCALE_BIN=$lc_v;;
      ROOTFS_RW_CMD) ROOTFS_RW_CMD=$lc_v;;
      ROOTFS_RO_CMD) ROOTFS_RO_CMD=$lc_v;;
      SSH_BIND_SRC) SSH_BIND_SRC=$lc_v;;
      *) die "$1 line $lc_n: unknown setting '$lc_k'";;
    esac
  done < "$TMP/install.conf.lf"
}

# ---- value checks (values end up inside remote shell commands, systemd units and rm -rf) --------
bad_value() { die "$1: '$2' is not acceptable ($3)"; }
check_word()  { case "$2" in ''|*[!A-Za-z0-9._-]*) bad_value "$1" "$2" 'letters, digits, . _ - only';; esac; }
check_port()  { case "$2" in ''|*[!0-9]*) bad_value "$1" "$2" 'a port number';; esac
                if [ "$2" -lt 1 ] || [ "$2" -gt 65535 ]; then bad_value "$1" "$2" 'a port number 1-65535'; fi; }
check_path()  { case "$2" in /*) ;; *) bad_value "$1" "$2" 'an absolute path';; esac
                case "$2" in *[!A-Za-z0-9._/-]*) bad_value "$1" "$2" 'letters, digits, . _ - / only';; esac
                case "$2" in */../*|*/..|*//*|/) bad_value "$1" "$2" 'no .., no empty components';; esac; }
check_listen() { case "$2" in *[!0-9.:]*|'') bad_value "$1" "$2" 'IPv4:port, e.g. 127.0.0.1:5001';; esac
                 lis_ip=${2%:*}; lis_port=${2##*:}
                 case "$lis_ip" in *[!0-9.]*|''|*..*|.*|*.) bad_value "$1" "$2" 'IPv4:port, e.g. 127.0.0.1:5001';; esac
                 lis_dots=$(printf '%s' "$lis_ip" | tr -cd '.' | wc -c | tr -d ' ')
                 [ "$lis_dots" = 3 ] || bad_value "$1" "$2" 'IPv4:port, e.g. 127.0.0.1:5001'
                 check_port "$1" "$lis_port"; }

validate_conf() {
  [ -n "$TABLET_HOST" ] || die "TABLET_HOST is not set in $CONF_FILE"
  case "$TABLET_HOST" in *[!A-Za-z0-9._:-]*) bad_value TABLET_HOST "$TABLET_HOST" 'a host name or IP address';; esac
  case "$TABLET_USER" in ''|*[!a-z0-9_-]*|[0-9-]*) bad_value TABLET_USER "$TABLET_USER" 'a user name';; esac
  check_port SSH_PORT "$SSH_PORT"
  case "$SSH_STRICT" in accept-new|ask|yes|no|off) ;; *) bad_value SSH_STRICT "$SSH_STRICT" 'accept-new, ask, yes or no';; esac
  if [ -n "$SSH_KEY" ] && [ ! -f "$SSH_KEY" ]; then die "SSH_KEY: file not found: $SSH_KEY"; fi
  check_path APP_DIR "$APP_DIR"
  case "${APP_DIR##*/}" in rmkavita) ;; *) bad_value APP_DIR "$APP_DIR" 'the last path component must be rmkavita';; esac
  check_path STAGE_DIR "$STAGE_DIR"
  case "$DOOR" in yes|no) ;; *) bad_value DOOR "$DOOR" 'yes or no';; esac
  if [ "$DOOR" = yes ]; then
    [ -n "$KAVITA_HOST" ] || die "DOOR=yes needs KAVITA_HOST in $CONF_FILE"
    check_word KAVITA_HOST "$KAVITA_HOST"
    check_port KAVITA_PORT "$KAVITA_PORT"
    check_listen DOOR_LISTEN "$DOOR_LISTEN"
    check_path TAILSCALE_BIN "$TAILSCALE_BIN"
    if [ -n "$ROOTFS_RW_CMD" ]; then check_path ROOTFS_RW_CMD "$ROOTFS_RW_CMD"; fi
    if [ -n "$ROOTFS_RO_CMD" ]; then check_path ROOTFS_RO_CMD "$ROOTFS_RO_CMD"; fi
    if [ -n "$SSH_BIND_SRC" ]; then check_path SSH_BIND_SRC "$SSH_BIND_SRC"; fi
  else
    if [ -n "$KAVITA_HOST" ]; then check_word KAVITA_HOST "$KAVITA_HOST"; check_port KAVITA_PORT "$KAVITA_PORT"; fi
    check_listen DOOR_LISTEN "$DOOR_LISTEN"
  fi
  [ -n "$PACKAGE_DIR" ] || PACKAGE_DIR=$SELF_DIR/../package
}

# ---- tablet access --------
# rr 'remote command'   (stdin passes through to the remote command)
rr() {
  if [ -n "$SSH_KEY" ]; then
    ssh -i "$SSH_KEY" -p "$SSH_PORT" -o BatchMode=yes -o IdentitiesOnly=yes -o ConnectTimeout=12 \
        -o ServerAliveInterval=15 -o "StrictHostKeyChecking=$SSH_STRICT" $SSH_EXTRA "$TABLET_USER@$TABLET_HOST" "$1"
  else
    ssh -p "$SSH_PORT" -o BatchMode=yes -o ConnectTimeout=12 \
        -o ServerAliveInterval=15 -o "StrictHostKeyChecking=$SSH_STRICT" $SSH_EXTRA "$TABLET_USER@$TABLET_HOST" "$1"
  fi
}

# shq VALUE  -> VALUE as one single-quoted shell word
shq() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }

# dev NAME KEY=VALUE...  -> runs device/NAME.sh on the tablet with those arguments (script on stdin)
dev() {
  dv_name=$1; shift
  dv_args=''
  for dv_kv in "$@"; do dv_args="$dv_args $(shq "$dv_kv")"; done
  rr "sh -s --$dv_args" < "$SELF_DIR/device/$dv_name.sh"
}

# put LOCALFILE REMOTEPATH  (through ssh stdin: works with dropbear, no sftp-server needed)
put() { rr "cat > $(shq "$2") && echo WROTE" < "$1"; }

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1
  elif command -v openssl >/dev/null 2>&1; then openssl dgst -sha256 -r "$1" | cut -d' ' -f1
  else echo unavailable; fi
}

# ---- package checks (on this computer) --------
ELF_WHY=''
# od prints the bytes as decimal numbers, awk reads the little-endian fields: ELF magic, 64-bit LE, e_machine 183
# (AArch64), then the program headers: a PT_INTERP entry (type 3) means a dynamically linked binary.
elf_check() {
  ec_f=$1
  [ -f "$ec_f" ] || { ELF_WHY='file not found'; return 1; }
  ec_size=$(wc -c < "$ec_f" | tr -d ' ')
  [ "$ec_size" -ge 1048576 ] || { ELF_WHY='smaller than 1 MB'; return 1; }
  ec_r=$(od -An -v -tu1 -N 64 "$ec_f" | tr -s ' \n' '  ' | awk '{
      for (i = 1; i <= NF; i++) b[i - 1] = $i + 0
      if (b[0] != 127 || b[1] != 69 || b[2] != 76 || b[3] != 70) { print "not an ELF file"; exit }
      if (b[4] != 2 || b[5] != 1) { print "not a 64-bit little-endian ELF"; exit }
      if (b[18] + 256 * b[19] != 183) { print "not an AArch64 binary"; exit }
      phoff = 0
      for (i = 7; i >= 0; i--) phoff = phoff * 256 + b[32 + i]
      phes = b[54] + 256 * b[55]; phn = b[56] + 256 * b[57]
      if (phn > 64 || phes < 32 || phes > 256) { print "implausible program header table"; exit }
      printf "ok %d %d %d\n", phoff, phes, phn }')
  case "$ec_r" in
    'ok '*) ;;
    *) ELF_WHY=$ec_r; return 1;;
  esac
  ec_off=$(printf '%s\n' "$ec_r" | cut -d' ' -f2)
  ec_es=$(printf '%s\n' "$ec_r" | cut -d' ' -f3)
  ec_n=$(printf '%s\n' "$ec_r" | cut -d' ' -f4)
  if [ "$ec_n" -gt 0 ]; then
    ec_t=$(od -An -v -tu1 -j "$ec_off" -N $((ec_es * ec_n)) "$ec_f" | tr -s ' \n' '  ' | awk -v es="$ec_es" -v n="$ec_n" '{
        for (i = 1; i <= NF; i++) b[i - 1] = $i + 0
        r = "static"
        for (k = 0; k < n; k++) { o = k * es; if (b[o] + 256 * b[o + 1] + 65536 * b[o + 2] + 16777216 * b[o + 3] == 3) r = "interp" }
        print r }')
    if [ "$ec_t" = interp ]; then ELF_WHY='dynamically linked (PT_INTERP); a static binary is required'; return 1; fi
  fi
  return 0
}

# text files that go to the tablet must contain nothing key-shaped
scan_text() {
  if grep -q -E '/api/opds/[A-Za-z0-9_-]{8,}' "$1"; then die "$2 seems to contain an API key (/api/opds/<key>); refusing to continue"; fi
  if grep -q -E '[A-Za-z0-9]{32,}' "$1"; then die "$2 contains a run of 32 or more letters/digits that looks like a key; refusing to continue"; fi
  if grep -q -E '[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}' "$1"; then
    die "$2 contains a GUID-shaped token (an API key looks like that); refusing to continue"
  fi
}

# ---- facts --------
isnum() { case "$1" in ''|*[!0-9]*) return 1;; *) return 0;; esac; }
count_text() { if isnum "$1"; then printf '%s' "$1"; else printf 'unknown'; fi; }

qtfb_rgba() {
  case "$1" in
    [0-9]*.[0-9]*.[0-9]*)
      qr_maj=${1%%.*}; qr_rest=${1#*.}; qr_min=${qr_rest%%.*}
      case "$qr_maj$qr_min" in *[!0-9]*) echo unknown; return;; esac
      if [ "$qr_maj" -gt 0 ] || [ "$qr_min" -ge 5 ]; then echo yes; else echo no; fi;;
    *) echo unknown;;
  esac
}
fb_request_text() {
  case "$1" in
    auto|'') echo 'rgba8888 (565 fallback)';;
    rgba) echo 'rgba8888 (forced by fb_format)';;
    rgb565) echo 'rgb565 (forced by fb_format)';;
    *) echo 'unknown (unrecognised fb_format value; documented: auto, rgba, rgb565)';;
  esac
}

# ---- the run --------
TMP=${TMPDIR:-/tmp}/rmkavita-install.$$
mkdir "$TMP" || die "cannot create $TMP"
trap 'rm -rf "$TMP"' EXIT
trap 'exit 130' INT TERM HUP

load_conf "$CONF_FILE"
validate_conf

MODE_TEXT='INSTALL'
[ "$UNINSTALL" = yes ] && MODE_TEXT='UNINSTALL'
[ "$DRY" = yes ] && MODE_TEXT="$MODE_TEXT (DRY RUN: read-only, nothing will be changed)"
say "== rmKavita $MODE_TEXT -> $TABLET_USER@$TABLET_HOST"

# ---- 1. package (not needed to uninstall) --------
PKG_BIN=$PACKAGE_DIR/rmkavita
SERVER_URL=''
if [ "$UNINSTALL" != yes ]; then
  say '-- 1. package'
  for f in rmkavita external.manifest.json icon.png rmkavita.conf.sample; do
    [ -f "$PACKAGE_DIR/$f" ] || die "the package is incomplete: $PACKAGE_DIR/$f is missing (build or download rmkavita and put it in PACKAGE_DIR)"
  done
  elf_check "$PKG_BIN" || die "$PKG_BIN: $ELF_WHY"
  PKG_SHA=$(sha256_of "$PKG_BIN")
  PKG_SIZE=$(wc -c < "$PKG_BIN" | tr -d ' ')
  say "   rmkavita: $PKG_SIZE bytes, static AArch64 ELF, sha256 $(printf '%s' "$PKG_SHA" | cut -c1-16)"
  [ "$(grep -c -E '"application"[[:space:]]*:[[:space:]]*"rmkavita"' "$PACKAGE_DIR/external.manifest.json")" = 1 ] ||
    die "external.manifest.json does not name the application rmkavita"
  scan_text "$PACKAGE_DIR/external.manifest.json" external.manifest.json
  scan_text "$PACKAGE_DIR/rmkavita.conf.sample" rmkavita.conf.sample
  [ "$(grep -c -E '^[[:space:]]*server[[:space:]]*=' "$PACKAGE_DIR/rmkavita.conf.sample")" = 1 ] ||
    die "rmkavita.conf.sample must have exactly one 'server =' line"
  if [ "$DOOR" = yes ]; then
    for f in "$DOOR_SOCKET" "$DOOR_SERVICE"; do
      [ -f "$SELF_DIR/units/$f" ] || die "the door unit template units/$f is missing"
      scan_text "$SELF_DIR/units/$f" "units/$f"
      sed -e "s|@DOOR_LISTEN@|$DOOR_LISTEN|g" -e "s|@KAVITA_HOST@|$KAVITA_HOST|g" \
          -e "s|@KAVITA_PORT@|$KAVITA_PORT|g" -e "s|@TAILSCALE_BIN@|$TAILSCALE_BIN|g" "$SELF_DIR/units/$f" > "$TMP/$f"
      if grep -q -E '@[A-Z][A-Z0-9_]*@' "$TMP/$f"; then die "internal error: a placeholder in units/$f was not filled"; fi
    done
    SERVER_URL="http://$DOOR_LISTEN"
    say "   door units: $DOOR_LISTEN -> $KAVITA_HOST:$KAVITA_PORT through $TAILSCALE_BIN (filled in from install.conf)"
  elif [ -n "$KAVITA_HOST" ]; then
    SERVER_URL="http://$KAVITA_HOST:$KAVITA_PORT"
  fi
  # the sample, with the server value set when we know it
  if [ -n "$SERVER_URL" ]; then
    sed "s|^[[:space:]]*server[[:space:]]*=.*\$|server = $SERVER_URL|" "$PACKAGE_DIR/rmkavita.conf.sample" > "$TMP/conf.sample"
  else
    cp "$PACKAGE_DIR/rmkavita.conf.sample" "$TMP/conf.sample"
  fi
fi

# ---- 2. the tablet, read-only --------
say '-- 2. tablet (read-only look)'
DOOR_PORT=${DOOR_LISTEN##*:}
FACTS=$(dev facts "APP_DIR=$APP_DIR" "CONF=$CONF" "STATE=$STATE" "SETTINGS=$SETTINGS" "OLD_CONF=$OLD_CONF" "OLD_STATE=$OLD_STATE" \
        "UNIT_DIR=$UNIT_DIR" "DOOR_SOCKET=$DOOR_SOCKET" "DOOR_SERVICE=$DOOR_SERVICE" "TAILSCALE_BIN=$TAILSCALE_BIN" "DOOR_PORT=$DOOR_PORT" 2>&1)
if ! printf '%s\n' "$FACTS" | grep -q '^machine='; then
  printf '%s\n' "$FACTS" | redact | head -n 5 >&2
  die "the tablet is not reachable over ssh, or did not answer as expected (asleep? wrong TABLET_HOST / SSH_KEY?)"
fi

# one pass over the key=value lines (no subshell per fact)
F_MACHINE=''; F_APPLOAD=''; F_APPDIR=''; F_BINARY=''; F_CONF=''; F_OLDCONF=''; F_SETTINGS=''; F_STATE=''
F_UNITS=''; F_SOCKET=''; F_LISTEN=''; F_TAILSCALE=''; F_FREE=''; F_KL=''; F_V1=''; F_AL=''; F_UL=''; F_USER=''
F_ALV=''; F_FB=''; F_KOREADER=''; F_APP=''
while IFS= read -r fl; do
  case "$fl" in *=*) ;; *) continue;; esac
  fk=${fl%%=*}; fv=${fl#*=}
  case "$fk" in
    machine) F_MACHINE=$fv;;
    appload) F_APPLOAD=$fv;;
    appdir) F_APPDIR=$fv;;
    binary) F_BINARY=$fv;;
    conf) F_CONF=$fv;;
    old_conf) F_OLDCONF=$fv;;
    settings) F_SETTINGS=$fv;;
    state) F_STATE=$fv;;
    units) F_UNITS=$fv;;
    socket) F_SOCKET=$fv;;
    door_listen) F_LISTEN=$fv;;
    tailscale) F_TAILSCALE=$fv;;
    free_kb) F_FREE=$fv;;
    conf_keylines) F_KL=$fv;;
    conf_v1keys) F_V1=$fv;;
    conf_apilines) F_AL=$fv;;
    conf_userlines) F_UL=$fv;;
    conf_username) F_USER=$fv;;
    appload_version) F_ALV=$fv;;
    fb_format) F_FB=$fv;;
    koreader_pid) F_KOREADER="${F_KOREADER:+$F_KOREADER,}$fv";;
    app_pid) F_APP="${F_APP:+$F_APP,}$fv";;
  esac
done <<EOF
$FACTS
EOF
# whatever the tablet could not say is "unknown" (never an empty value)
[ -n "$F_MACHINE" ] || F_MACHINE=unknown
[ -n "$F_APPLOAD" ] || F_APPLOAD=unknown
[ -n "$F_APPDIR" ] || F_APPDIR=unknown
[ -n "$F_BINARY" ] || F_BINARY=unknown
[ -n "$F_CONF" ] || F_CONF=unknown
[ -n "$F_OLDCONF" ] || F_OLDCONF=unknown
[ -n "$F_SETTINGS" ] || F_SETTINGS=unknown
[ -n "$F_STATE" ] || F_STATE=unknown
[ -n "$F_UNITS" ] || F_UNITS=unknown
[ -n "$F_SOCKET" ] || F_SOCKET=unknown
[ -n "$F_LISTEN" ] || F_LISTEN=unknown
[ -n "$F_TAILSCALE" ] || F_TAILSCALE=unknown
[ -n "$F_FREE" ] || F_FREE=unknown
[ -n "$F_ALV" ] || F_ALV=unknown
[ -n "$F_FB" ] || F_FB=auto
case "$F_FB" in *[!a-z0-9._-]*) F_FB='?';; esac
F_RGBA=$(qtfb_rgba "$F_ALV")

# sign-in summary (counts only; the user name is not secret and was reduced to a safe alphabet on the device)
SI_TEXT='no config'
if [ "$F_CONF" != none ]; then
  if ! isnum "$F_AL"; then SI_TEXT=unknown
  elif [ "$F_AL" = 0 ]; then SI_TEXT=no
  else SI_TEXT=yes; [ -n "$F_USER" ] && [ "$F_USER" != '?' ] && SI_TEXT="yes (user $F_USER)"; fi
fi
OTHER=unknown
if isnum "$F_KL" && isnum "$F_V1"; then OTHER=$((F_KL - F_V1)); [ "$OTHER" -lt 0 ] && OTHER=0; fi

say "  device            : $F_MACHINE"
say "  AppLoad folder    : $F_APPLOAD"
say "  rmKavita installed: $F_APPDIR   (binary present: $F_BINARY)"
say "  config file       : $F_CONF   (mode, or 'none'; sign-in stored: $SI_TEXT; v1-style key lines: $(count_text "$F_V1"), other key-shaped lines: $OTHER; counted, never shown)"
say "  settings file     : $F_SETTINGS   positions file: $F_STATE"
say "  door units        : $F_UNITS   socket: $F_SOCKET   listening on :$DOOR_PORT : $F_LISTEN   tailscale: $F_TAILSCALE"
say "  free space        : $F_FREE KB"
say "  KOReader running  : $([ -n "$F_KOREADER" ] && echo "YES (pid $F_KOREADER)" || echo no)"
say "  rmKavita running  : $([ -n "$F_APP" ] && echo "YES (pid $F_APP)" || echo no)"
say "  AppLoad version   : $F_ALV   qtfb_rgba=$F_RGBA"
say "  fb_format (conf)  : $F_FB   framebuffer format the app will request: $(fb_request_text "$F_FB")"

# ---- refusals --------
WOULD='REFUSED'; [ "$DRY" = yes ] && WOULD='WOULD REFUSE'
if [ "$F_MACHINE" = unknown ]; then
  if [ "$DRY" = yes ]; then warn 'the device model could not be read; a real run would refuse on an unidentified device.'
  else die 'the device model could not be read (/sys/devices/soc0/machine); refusing to touch an unidentified device'; fi
else
  case "$F_MACHINE" in *Ferrari*|*Chiappa*) ;; *) die "this installer is for the reMarkable Paper Pro (Ferrari / Chiappa); the device says '$F_MACHINE'";; esac
fi
if [ "$F_APPLOAD" = no ]; then die "AppLoad's folder $(dirname "$APP_DIR") does not exist on the tablet: AppLoad is not installed"; fi
if [ "$UNINSTALL" != yes ] && isnum "$F_FREE" && [ "$F_FREE" -lt 60000 ]; then die "less than 60 MB free on /home ($F_FREE KB)"; fi
if [ "$UNINSTALL" != yes ] && ! isnum "$F_FREE"; then say '   NOTE: free space on /home could not be determined (unknown); continuing, the upload is about 8 MB.'; fi
if [ "$DRY" != yes ] && { [ "$F_CONF" = unknown ] || [ "$F_OLDCONF" = unknown ]; }; then
  die 'could not determine whether a config file exists on the tablet; nothing was changed'
fi
if { [ -n "$F_KOREADER" ] || [ -n "$F_APP" ]; } && [ "$FORCE" != yes ]; then
  say "   $WOULD: KOReader or rmKavita is running on the tablet. Close it and run again, or use --force."
  [ "$DRY" = yes ] || exit 2
elif [ -n "$F_KOREADER" ] || [ -n "$F_APP" ]; then
  say '   running apps noticed, continuing because --force was given'
fi
if [ "$UNINSTALL" != yes ] && [ "$DOOR" = yes ] && [ "$F_TAILSCALE" = no ] && [ "$FORCE" != yes ]; then
  die "DOOR=yes but $TAILSCALE_BIN does not exist on the tablet (set TAILSCALE_BIN, or use --force to install the door anyway)"
fi

NEW_CONF=no; [ "$F_CONF" != none ] && NEW_CONF=yes
OLD_CONF_EXISTS=no; [ "$F_OLDCONF" != none ] && OLD_CONF_EXISTS=yes
SEND_SAMPLE=no; [ "$NEW_CONF" = no ] && [ "$OLD_CONF_EXISTS" = no ] && SEND_SAMPLE=yes

# ---- uninstall --------
if [ "$UNINSTALL" = yes ]; then
  say '-- plan'
  say "   remove $APP_DIR ($F_APPDIR)"
  say "   remove $CONF ($([ "$F_CONF" != none ] && echo present || echo 'not present')) - it holds the sign-in"
  say "   remove $SETTINGS ($F_SETTINGS), $APP_LOG and $APP_LOG.1 (rotated log), $STAGE_DIR"
  say "   $([ "$F_UNITS" = yes ] && echo "stop and remove the door units $DOOR_SOCKET / $DOOR_SERVICE (needs the writable root file system for a moment)" || echo 'no door units present')"
  say "   $([ "$PURGE" = yes ] && echo remove || echo LEAVE) $STATE (reading positions)"
  if [ "$DRY" = yes ]; then say ''; say 'DRY RUN done. Nothing was changed.'; exit 0; fi
  if [ "$YES" != yes ]; then
    if [ -t 0 ]; then printf '%s' 'Proceed? [y/N] '; read -r ans; case "$ans" in y|Y|yes|YES) ;; *) die aborted;; esac
    else die 'refusing to change the tablet without confirmation: run interactively or pass --yes'; fi
  fi
  if [ "$F_UNITS" = yes ] || [ "$F_SOCKET" = active ]; then
    say '-- door'
    rr "mkdir -p $(shq "$STAGE_DIR")" < /dev/null
    put "$SELF_DIR/device/door.sh" "$STAGE_DIR/door.sh" < /dev/null >/dev/null || die 'could not stage the door script'
    rr "rm -f $(shq "$STAGE_DIR/door.log"); (setsid sh $(shq "$STAGE_DIR/door.sh") $(shq "ACTION=remove") $(shq "STAGE=$STAGE_DIR") $(shq "UNIT_DIR=$UNIT_DIR") $(shq "SOCKET=$DOOR_SOCKET") $(shq "SERVICE=$DOOR_SERVICE") $(shq "RW_CMD=$ROOTFS_RW_CMD") $(shq "RO_CMD=$ROOTFS_RO_CMD") $(shq "BIND_SRC=$SSH_BIND_SRC") < /dev/null > /dev/null 2>&1 &) ; sleep 1; echo LAUNCHED" < /dev/null | grep -q LAUNCHED || die 'could not launch the door removal'
    n=0; DONE=no
    while [ "$n" -lt 50 ]; do
      sleep 3; n=$((n + 1))
      LOGTXT=$(rr "cat $(shq "$STAGE_DIR/door.log") 2>/dev/null" < /dev/null)
      if printf '%s\n' "$LOGTXT" | grep -q '^DONE'; then DONE=yes; break; fi
      if printf '%s\n' "$LOGTXT" | grep -q '^FAILED'; then printf '%s\n' "$LOGTXT" | redact | tail -n 5; die 'the door removal failed on the tablet'; fi
    done
    [ "$DONE" = yes ] || die 'the door removal did not finish in time; check the tablet'
    say '   door units removed'
  fi
  say '-- app, config, settings'
  UOUT=$(dev uninstall "APP_DIR=$APP_DIR" "CONF=$CONF" "SETTINGS=$SETTINGS" "STATE=$STATE" "LOG=$APP_LOG" "STAGE=$STAGE_DIR" "PURGE=$PURGE" 2>&1)
  printf '%s\n' "$UOUT" | grep -q '^uninstall_done' || { printf '%s\n' "$UOUT" | redact | head -n 5 >&2; die 'the removal did not complete'; }
  say "   $(printf '%s\n' "$UOUT" | grep -E '^(app|conf|positions)=' | tr '\n' ' ')"
  say ''
  say 'DONE. rmKavita is gone from the tablet; AppLoad drops the entry at its next reload.'
  exit 0
fi

# ---- plan --------
FB_PLANNED=$F_FB
if [ "$NEW_CONF" = no ]; then
  FB_PLANNED=$(sed -n 's/^[[:space:]]*fb_format[[:space:]]*=[[:space:]]*\([^[:space:]#]*\).*$/\1/p' "$TMP/conf.sample" | head -n 1 | tr 'A-Z' 'a-z')
  [ -n "$FB_PLANNED" ] || FB_PLANNED=auto
fi
V1N=$(count_text "$F_V1")
say '-- plan'
say "   3. upload the package to $STAGE_DIR (sha256 verified on the tablet)"
say "   4. place the app folder $APP_DIR$([ "$F_APPDIR" = yes ] && echo ' - upgrading the installed binary')"
if [ "$DOOR" = yes ]; then
  say "   5. install the door $DOOR_LISTEN -> $KAVITA_HOST:$KAVITA_PORT ($([ "$F_UNITS" = yes ] && [ "$F_SOCKET" = active ] && echo 'units present and socket active: no remount if identical' || echo 'needs the root file system writable for a moment'))"
else
  say '   5. no door (DOOR=no)'
fi
if [ "$SEND_SAMPLE" = yes ]; then
  say "   6. write $CONF (mode 600) from the package sample$([ -n "$SERVER_URL" ] && echo " with server = $SERVER_URL"); no key in it; the app asks for sign-in on first launch"
elif [ "$NEW_CONF" = yes ]; then
  if [ "$F_AL" = 1 ] && isnum "$F_UL" && [ "$F_UL" -ge 1 ]; then
    say "   6. keep the existing config (mode $F_CONF) and migrate it: signed in$([ "$SI_TEXT" != yes ] && echo " as ${F_USER:-?}"): the sign-in is kept; would remove $V1N key line(s)"
  elif [ "$F_AL" = 0 ]; then
    say "   6. keep the existing config (mode $F_CONF) and migrate it: would remove $V1N key line(s); the app will ask for sign-in on first launch"
  else
    say "   6. keep the existing config (mode $F_CONF) and migrate it: would remove $V1N key line(s)"
    if isnum "$F_AL" && [ "$F_AL" -ge 2 ]; then warn "the config has $F_AL api_key lines, at most 1 is allowed: the final check would FAIL"; fi
    if [ "$F_AL" = 1 ]; then warn 'the config has an api_key line but no username line: the final check would FAIL'; fi
  fi
  if [ "$OTHER" != unknown ] && [ "$OTHER" -gt 0 ]; then warn "$OTHER other key-shaped line(s) outside the api_key line (counted, not shown): the final check would FAIL"; fi
else
  say "   6. build $CONF from the legacy prototype config (key lines are dropped)"
fi
say "      then run on the tablet: $APP_DIR/rmkavita -migrate -conf $CONF -state $STATE -old-conf $OLD_CONF -old-state $OLD_STATE"
say "      framebuffer format the app will request: $(fb_request_text "$FB_PLANNED")   (fb_format = $FB_PLANNED; AppLoad $F_ALV, qtfb_rgba=$F_RGBA)"
if [ "$F_RGBA" = no ]; then
  case "$FB_PLANNED" in auto|rgba) warn 'this AppLoad is older than 0.5.0 and has no RGBA8888 framebuffer: the app falls back to rgb565 (set fb_format = rgb565 to skip the attempt)';; esac
fi
say '   7. check (counts only): config mode 600, one server = line, no key-shaped line except one api_key line with a username line'
say '      The app creates its settings file itself; the positions file is kept. Nothing restarts xochitl or AppLoad.'
if [ "$DRY" = yes ]; then say ''; say 'DRY RUN done. Nothing was changed.'; exit 0; fi

# ---- confirmation --------
if [ "$YES" != yes ]; then
  if [ -t 0 ]; then printf '%s' 'Proceed? [y/N] '; read -r ans; case "$ans" in y|Y|yes|YES) ;; *) die aborted;; esac
  else die 'refusing to change the tablet without confirmation: run interactively or pass --yes'; fi
fi

# ---- 3. upload --------
say '-- 3. upload'
rr "rm -rf $(shq "$STAGE_DIR") && mkdir -p $(shq "$STAGE_DIR") && echo STAGED" < /dev/null | grep -q STAGED || die "cannot create $STAGE_DIR on the tablet"
for f in rmkavita external.manifest.json icon.png; do
  put "$PACKAGE_DIR/$f" "$STAGE_DIR/$f" | grep -q WROTE || die "uploading $f failed"
done
if [ "$DOOR" = yes ]; then
  put "$TMP/$DOOR_SOCKET" "$STAGE_DIR/$DOOR_SOCKET" | grep -q WROTE || die "uploading $DOOR_SOCKET failed"
  put "$TMP/$DOOR_SERVICE" "$STAGE_DIR/$DOOR_SERVICE" | grep -q WROTE || die "uploading $DOOR_SERVICE failed"
  put "$SELF_DIR/device/door.sh" "$STAGE_DIR/door.sh" | grep -q WROTE || die 'uploading door.sh failed'
fi
DEV_SHA=$(rr "sha256sum $(shq "$STAGE_DIR/rmkavita")" < /dev/null | cut -d' ' -f1)
if [ "$PKG_SHA" != unavailable ] && [ "$DEV_SHA" != "$PKG_SHA" ]; then
  die "the uploaded binary does not match the package (device sha256 $(printf '%s' "$DEV_SHA" | cut -c1-16))"
fi
say '   uploaded, sha256 verified on the tablet'

# ---- 4. app folder --------
say '-- 4. app folder'
POUT=$(dev place "APP_DIR=$APP_DIR" "STAGE=$STAGE_DIR" 2>&1)
printf '%s\n' "$POUT" | grep -q '^placed=' || { printf '%s\n' "$POUT" | redact | head -n 5 >&2; die 'placing the app folder failed (the previous one was put back)'; }
say '   app folder in place'

# ---- 5. door --------
if [ "$DOOR" = yes ]; then
  say '-- 5. door'
  rr "rm -f $(shq "$STAGE_DIR/door.log"); (setsid sh $(shq "$STAGE_DIR/door.sh") $(shq "ACTION=install") $(shq "STAGE=$STAGE_DIR") $(shq "UNIT_DIR=$UNIT_DIR") $(shq "SOCKET=$DOOR_SOCKET") $(shq "SERVICE=$DOOR_SERVICE") $(shq "RW_CMD=$ROOTFS_RW_CMD") $(shq "RO_CMD=$ROOTFS_RO_CMD") $(shq "BIND_SRC=$SSH_BIND_SRC") < /dev/null > /dev/null 2>&1 &) ; sleep 1; echo LAUNCHED" < /dev/null | grep -q LAUNCHED || die 'could not launch the door installation'
  n=0; DONE=no
  while [ "$n" -lt 50 ]; do
    sleep 3; n=$((n + 1))
    LOGTXT=$(rr "cat $(shq "$STAGE_DIR/door.log") 2>/dev/null" < /dev/null)
    if printf '%s\n' "$LOGTXT" | grep -q '^DONE'; then DONE=yes; break; fi
    if printf '%s\n' "$LOGTXT" | grep -q '^FAILED'; then printf '%s\n' "$LOGTXT" | redact | tail -n 5; die 'the door installation failed on the tablet'; fi
  done
  [ "$DONE" = yes ] || die 'the door installation did not finish in time; check the tablet'
  say '   door installed:'
  printf '%s\n' "$LOGTXT" | redact | tail -n 3 | sed 's/^/     /'
fi

# ---- 6. config and migration --------
say '-- 6. config and migration'
if [ "$SEND_SAMPLE" = yes ]; then
  WOUT=$(rr "umask 077; mkdir -p $(shq "${CONF%/*}") && cat > $(shq "$CONF.new") && chmod 600 $(shq "$CONF.new") && mv -f $(shq "$CONF.new") $(shq "$CONF") && echo WROTE" < "$TMP/conf.sample" 2>&1)
  printf '%s\n' "$WOUT" | grep -q WROTE || die 'writing the config failed'
  say "   config written from the package sample$([ -n "$SERVER_URL" ] && echo " (server = $SERVER_URL)"), mode 600, no key in it"
elif [ "$NEW_CONF" = yes ]; then
  say "   existing config kept (mode $F_CONF)"
else
  say '   no new config written: the migration builds it from the legacy prototype config'
fi
MOUT=$(dev migrate "APP_DIR=$APP_DIR" "CONF=$CONF" "STATE=$STATE" "OLD_CONF=$OLD_CONF" "OLD_STATE=$OLD_STATE" 2>&1)
M_RC=$(printf '%s\n' "$MOUT" | sed -n 's/^migrate_rc=//p' | head -n 1)
M_LINES=$(printf '%s\n' "$MOUT" | sed -n 's/^m| //p')
if [ "$M_RC" != 0 ] || ! printf '%s\n' "$MOUT" | grep -q '^migrate_end' || [ -z "$M_LINES" ]; then
  say '   the config migration failed:'
  printf '%s\n' "$MOUT" | sed -n 's/^o| //p' | redact | sed 's/^/     device said: /'
  die "config migration failed (exit ${M_RC:-?}, or no 'migrate:' line). The app folder is in place; fix the cause and run the installer again (it is idempotent)."
fi
printf '%s\n' "$M_LINES" | redact | sed 's/^/   /'

# ---- 7. final check --------
say '-- 7. check'
if [ "$DOOR" = yes ]; then
  COUT=$(dev check "APP_DIR=$APP_DIR" "CONF=$CONF" "STAGE=$STAGE_DIR" "DOOR_SOCKET=$DOOR_SOCKET" "DOOR_PORT=$DOOR_PORT" 2>&1)
else
  COUT=$(dev check "APP_DIR=$APP_DIR" "CONF=$CONF" "STAGE=$STAGE_DIR" 2>&1)
fi
printf '%s\n' "$COUT" | redact | sed 's/^/   /'
PROBLEMS=''
add_problem() { PROBLEMS="$PROBLEMS
   - $1"; }
CFG=$(printf '%s\n' "$COUT" | sed -n 's/^config  : //p' | head -n 1)
if [ -z "$CFG" ]; then add_problem 'the config line is missing from the check output (config unreadable?)'
else
  c_get() { printf '%s\n' "$CFG" | sed -n "s/.*$1=\\([^ ]*\\).*/\\1/p"; }
  c_mode=$(c_get mode); c_srv=$(c_get server-lines); c_api=$(c_get api-lines); c_usr=$(c_get user-lines); c_key=$(c_get key-lines)
  [ "$c_mode" = 600 ] || add_problem "the config mode is '$c_mode', not 600"
  [ "$c_srv" = 1 ] || add_problem "the config has '$c_srv' 'server =' line(s), expected exactly 1"
  case "$c_api" in
    0) ;;
    1) if ! isnum "$c_usr" || [ "$c_usr" -lt 1 ]; then add_problem 'the config has an api_key line but no username line'; fi;;
    *) add_problem "the config has '$c_api' api_key lines, at most 1 is allowed";;
  esac
  [ "$c_key" = 0 ] || add_problem "the config has '$c_key' key-shaped line(s) outside the single api_key line (counted, not shown)"
fi
printf '%s\n' "$COUT" | grep -q -E '^app     : -[rwx-]{9}' || add_problem 'the app binary is missing'
printf '%s\n' "$COUT" | grep -q -E '^manifest: [1-9][0-9]* match' || add_problem 'the manifest does not name the app rmkavita'
if [ "$DOOR" = yes ]; then
  printf '%s\n' "$COUT" | grep -q -E '^door    : active' || add_problem 'the door socket is not active'
fi
if [ -n "$PROBLEMS" ]; then
  printf '%s\n' "post-install check FAILED:$PROBLEMS" >&2
  exit 1
fi
say "   $(fb_request_text "$FB_PLANNED" | sed 's/^/framebuffer format the app will request: /')   (AppLoad $F_ALV, qtfb_rgba=$F_RGBA)"
say ''
say 'DONE. On the tablet open AppLoad and tap its reload button: "rmKavita" appears; the first launch shows the Sign-in screen.'
exit 0
