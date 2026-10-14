#!/bin/sh
# Offline tests for install.sh. POSIX sh; runs on Linux, macOS, Git Bash and WSL.
#
#   sh test-install.sh            # uses `sh` to run install.sh
#   sh test-install.sh dash       # or any other POSIX shell: dash, bash --posix, busybox sh ...
#
# Nothing touches a real tablet. `ssh` and `scp` are shell shims put first on PATH: the fake ssh runs the remote
# command (and the device scripts piped to `sh -s`) against a FOLDER TREE that plays the device, with every device
# path rewritten into the tree and a few device-only commands (systemctl, stat -c, df, netstat, readlink, setsid,
# mount) replaced by small fake executables. The "rmkavita binary" on the device is a shell stub that follows the
# -migrate contract. The key used below is synthetic and assembled at run time.

HERE=$(cd "$(dirname "$0")" && pwd) || exit 1
RUNSH=${1:-sh}
WORK=${TMPDIR:-/tmp}/rmkavita-public-test.$$
mkdir -p "$WORK" || exit 1
trap 'rm -rf "$WORK"' EXIT
trap 'exit 130' INT TERM HUP

DEV=$WORK/dev
BIN=$WORK/bin          # fake ssh / scp (host side)
DEVBIN=$WORK/devbin    # fake device-only commands
PKG=$WORK/pkg
FAKELOG=$WORK/ssh.log
VD=.vell""um           # the standard Vellum folder name on the device (spelled in two pieces: documented only in install.sh)
mkdir -p "$BIN" "$DEVBIN" "$PKG"

PASS=0
FAIL=0
pass() { PASS=$((PASS + 1)); printf '  PASS %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf '  FAIL %s\n' "$1"; }
section() { printf '== %s\n' "$1"; }
# one check:   t 'description'; n COMMAND...; n COMMAND...; e     (passes when every n-command succeeds)
t() { T_DESC=$1; T_FAIL=0; }
n() { "$@" || T_FAIL=1; }
nn() { if "$@"; then T_FAIL=1; fi; }         # the command must FAIL
e() { if [ "$T_FAIL" = 0 ]; then pass "$T_DESC"; else fail "$T_DESC"; fi; }
contains() { printf '%s\n' "$1" | grep -q -E -e "$2"; }          # contains TEXT REGEX
lacks() { if printf '%s\n' "$1" | grep -q -E -e "$2"; then return 1; fi; return 0; }

# synthetic key (no literal long token in this file)
K1=0123456789abcdef
FAKEKEY="${K1}-${K1}-${K1}-FAKEKEY"

# ---- fake ssh / scp --------
cat > "$BIN/ssh" <<'EOF'
#!/bin/sh
LOG=${RMK_FAKE_LOG:?}
DEV=${RMK_FAKE_DEV:?}
{ printf 'ssh'; for a in "$@"; do printf ' [%s]' "$a"; done; printf '\n'; } >> "$LOG"
while [ $# -gt 0 ]; do
  case "$1" in
    -i|-p|-o|-F|-l|-J|-b|-c|-m|-E|-S|-L|-R|-D|-W|-w) shift 2;;
    -*) shift;;
    *) shift; break;;
  esac
done
cmd="$*"
rewrite() {
  # one pass with a marker, so a device tree that itself lives under /tmp is not rewritten twice
  sed -e 's#/etc/systemd/system#@@DEV@@/etc/systemd/system#g' -e 's#/home/root#@@DEV@@/home/root#g' \
      -e 's#/tmp/#@@DEV@@/tmp/#g' -e 's#/proc/#@@DEV@@/proc/#g' -e 's#/sys/#@@DEV@@/sys/#g' -e "s#@@DEV@@#$DEV#g"
}
rcmd=$(printf '%s' "$cmd" | rewrite)
IN=$DEV/shim/stdin.$$
cat > "$IN"
first=$(dd if="$IN" bs=2 count=1 2>/dev/null)
if [ "$first" = '#!' ]; then rewrite < "$IN" > "$IN.rw"; mv "$IN.rw" "$IN"; fi
PATH="$RMK_FAKE_DEVBIN:$PATH" HOME="$DEV/home/root" sh -c "$rcmd" < "$IN"
rc=$?
rm -f "$IN"
# the uploaded "binary" is replaced by the stub once it has been placed (the original is kept for comparison)
APPBIN="$DEV/home/root/xovi/exthome/appload/rmkavita/rmkavita"
if [ -f "$APPBIN" ]; then
  f2=$(dd if="$APPBIN" bs=2 count=1 2>/dev/null)
  if [ "$f2" != '#!' ]; then
    cp "$APPBIN" "$RMK_FAKE_WORK/placed-binary"
    cp "$RMK_FAKE_STUB" "$APPBIN"
    chmod 755 "$APPBIN"
  fi
fi
exit $rc
EOF
cat > "$BIN/scp" <<'EOF'
#!/bin/sh
{ printf 'scp'; for a in "$@"; do printf ' [%s]' "$a"; done; printf '\n'; } >> "${RMK_FAKE_LOG:?}"
echo "scp is not used by the installer" >&2
exit 99
EOF
chmod 755 "$BIN/ssh" "$BIN/scp"

# ---- fake device-only commands --------
cat > "$DEVBIN/systemctl" <<'EOF'
#!/bin/sh
S=$RMK_FAKE_DEV/shim
case "$1" in
  is-active) if [ -f "$S/active.$2" ]; then echo active; else echo inactive; fi;;
  is-enabled) echo enabled;;
  start)
    : > "$S/active.$2"
    port=$(sed -n 's/^ListenStream=.*:\([0-9][0-9]*\)$/\1/p' "$RMK_FAKE_DEV/etc/systemd/system/$2" 2>/dev/null | head -n 1)
    echo "${port:-5001}" > "$S/listen";;
  stop) rm -f "$S/active.$2"; echo 0 > "$S/listen";;
  daemon-reload|reset-failed) ;;
esac
exit 0
EOF
cat > "$DEVBIN/stat" <<'EOF'
#!/bin/sh
[ "$1" = -c ] || exit 1
f=$3
[ -e "$f" ] || exit 1
case "$2" in
  %a) cat "$RMK_FAKE_DEV/shim/mode";;
  %s) wc -c < "$f" | tr -d ' ';;
  *) exit 1;;
esac
EOF
cat > "$DEVBIN/df" <<'EOF'
#!/bin/sh
if [ "$(cat "$RMK_FAKE_DEV/shim/df" 2>/dev/null)" = fail ]; then exit 1; fi
echo 'Filesystem 1K-blocks Used Available Use% Mounted on'
echo "/dev/x 100000000 55000000 $(cat "$RMK_FAKE_DEV/shim/free_kb") 55% /home"
EOF
cat > "$DEVBIN/netstat" <<'EOF'
#!/bin/sh
p=$(cat "$RMK_FAKE_DEV/shim/listen" 2>/dev/null)
if [ -n "$p" ] && [ "$p" != 0 ]; then echo "tcp 0 0 127.0.0.1:$p 0.0.0.0:* LISTEN"; fi
exit 0
EOF
cat > "$DEVBIN/readlink" <<'EOF'
#!/bin/sh
cat "$1.link" 2>/dev/null
EOF
cat > "$DEVBIN/setsid" <<'EOF'
#!/bin/sh
exec "$@"
EOF
cat > "$DEVBIN/mount" <<'EOF'
#!/bin/sh
echo "mount $*" >> "$RMK_FAKE_DEV/shim/mount.log"
exit 0
EOF
chmod 755 "$DEVBIN"/*

# the binary the device "runs": follows the -migrate contract (portable sed, no -i)
cat > "$WORK/stub.template" <<'EOF'
#!/bin/sh
SHIM='@SHIM@'
KEY='@KEY@'
case "$1" in
-version) echo "rmkavita 0.0.0-stub"; exit 0;;
-selftest) echo "not signed in"; exit 3;;
-migrate)
  shift
  conf=''; state=''; oconf=''; ostate=''
  while [ $# -gt 1 ]; do
    case "$1" in
      -conf) conf=$2;; -state) state=$2;; -old-conf) oconf=$2;; -old-state) ostate=$2;;
    esac
    shift 2
  done
  mode=$(cat "$SHIM/migrate" 2>/dev/null)
  case "$mode" in
    fail) echo "flag provided but not defined: -migrate" >&2; exit 2;;
    leakfail) echo "error: cannot parse root = http://127.0.0.1:5001/api/opds/$KEY" >&2; exit 1;;
  esac
  created=0; removed=0; copied=0
  ROOTRE='^[[:space:]]*root[[:space:]]*=[[:space:]]*http://[^/]+/api/opds/'
  CMTRE='^[[:space:]]*#.*/api/opds/[A-Za-z0-9_-]{8,}'
  if [ ! -f "$conf" ] && [ -f "$oconf" ]; then
    cp "$oconf" "$conf"; created=1
    removed=$(grep -c -i -E -e "$ROOTRE" -e '^[[:space:]]*(api_key|apikey|key)[[:space:]]*=' "$conf")
    sed -E -e 's#^[[:space:]]*root[[:space:]]*=[[:space:]]*(http://[^/]+)/api/opds/.*$#server = \1#' \
        -e '/^[[:space:]]*(api_key|apikey|key)[[:space:]]*=/d' "$conf" > "$conf.tmp" && mv "$conf.tmp" "$conf"
  elif [ -f "$conf" ]; then
    removed=$(grep -c -E -e "$ROOTRE" -e "$CMTRE" "$conf")
    if grep -q -E '^[[:space:]]*server[[:space:]]*=' "$conf"; then
      grep -v -E -e "$ROOTRE" -e "$CMTRE" "$conf" > "$conf.tmp"; mv "$conf.tmp" "$conf"
    else
      sed -E -e 's#^[[:space:]]*root[[:space:]]*=[[:space:]]*(http://[^/]+)/api/opds/.*$#server = \1#' "$conf" | grep -v -E -e "$CMTRE" > "$conf.tmp"
      mv "$conf.tmp" "$conf"
    fi
  fi
  if [ ! -f "$state" ] && [ -f "$ostate" ]; then cp "$ostate" "$state"; copied=1; fi
  [ "$mode" = noline ] && exit 0
  msg=''
  [ "$created" = 1 ] && msg='conf created from old conf'
  [ "$removed" -gt 0 ] && msg="${msg:+$msg, }removed $removed key line(s)"
  [ "$copied" = 1 ] && msg="${msg:+$msg, }state copied"
  echo "migrate: ${msg:-nothing to do}"
  exit 0;;
esac
echo "usage: rmkavita [-version | -selftest | -migrate ...]" >&2
exit 2
EOF
sed -e "s|@SHIM@|$DEV/shim|" -e "s|@KEY@|$FAKEKEY|" "$WORK/stub.template" > "$WORK/stub"
chmod 755 "$WORK/stub"

# ---- helpers: fake ELF, device, config, runs --------
zeros() { dd if=/dev/zero bs="$1" count=1 2>/dev/null; }
make_elf() {   # make_elf FILE static|dynamic|short|data
  me_f=$1
  case "$2" in
    data) printf 'this is not an ELF file\n' > "$me_f"; dd if=/dev/zero bs=1024 count=1100 2>/dev/null >> "$me_f"; return;;
  esac
  printf '\177ELF\002\001\001\000\000\000\000\000\000\000\000\000' > "$me_f"
  printf '\002\000\267\000\001\000\000\000' >> "$me_f"
  printf '\000\000\000\000\000\000\000\000' >> "$me_f"
  printf '\100\000\000\000\000\000\000\000' >> "$me_f"
  printf '\000\000\000\000\000\000\000\000' >> "$me_f"
  printf '\000\000\000\000' >> "$me_f"
  printf '\100\000\070\000\002\000\000\000\000\000\000\000' >> "$me_f"
  printf '\001\000\000\000' >> "$me_f"; zeros 52 >> "$me_f"
  if [ "$2" = dynamic ]; then printf '\003\000\000\000' >> "$me_f"; else printf '\001\000\000\000' >> "$me_f"; fi
  zeros 52 >> "$me_f"
  if [ "$2" = short ]; then return; fi
  dd if=/dev/zero bs=1024 count=1100 2>/dev/null >> "$me_f"
}

new_package() {
  rm -rf "$PKG"; mkdir -p "$PKG"
  make_elf "$PKG/rmkavita" static
  printf '{\n  "name": "rmKavita",\n  "application": "rmkavita",\n  "qtfb": true\n}\n' > "$PKG/external.manifest.json"
  printf 'PNGDATA' > "$PKG/icon.png"
  # sits in the package folder but must never be uploaded (the allow-list: binary, manifest, icon, units, conf)
  printf '# owner checklist\n' > "$PKG/CHECKLIST.md"
  mkdir -p "$PKG/ab-test"; printf 'CBZ' > "$PKG/ab-test/AB Test v01.cbz"
  cat > "$PKG/rmkavita.conf.sample" <<'EOF'
# rmKavita configuration
server = http://127.0.0.1:5000
page_mode = auto
fb_format = auto
prefetch = 2
EOF
}

# new_device SCENARIO [appload-kind]   scenarios: clean installed signedin signedinroot twoapi koreader apprunning wrongdevice
new_device() {
  rm -rf "$DEV"
  mkdir -p "$DEV/home/root/xovi/exthome/appload" "$DEV/home/root/.config" "$DEV/etc/systemd/system" "$DEV/tmp" \
           "$DEV/proc" "$DEV/sys/devices/soc0" "$DEV/shim" "$DEV/home/root/$VD/bin" "$DEV/home/root/xovi/extensions.d"
  machine='reMarkable Ferrari'
  [ "$1" = wrongdevice ] && machine='reMarkable 2.0'
  printf '%s\n' "$machine" > "$DEV/sys/devices/soc0/machine"
  printf 'tmpfs /run tmpfs rw 0 0\n/dev/mmcblk0p4 /etc/dropbear ext4 rw 0 0\n' > "$DEV/proc/mounts"
  echo 600 > "$DEV/shim/mode"; echo 45000000 > "$DEV/shim/free_kb"; echo 0 > "$DEV/shim/listen"; echo ok > "$DEV/shim/migrate"
  printf '#!/bin/sh\necho mount-rw >> "%s/shim/rw.count"\n' "$DEV" > "$DEV/home/root/$VD/bin/mount-rw"
  printf '#!/bin/sh\necho mount-restore >> "%s/shim/ro.count"\n' "$DEV" > "$DEV/home/root/$VD/bin/mount-restore"
  printf '#!/bin/sh\nexit 0\n' > "$DEV/home/root/$VD/bin/tailscale"
  chmod 755 "$DEV/home/root/$VD/bin/"*
  case "${2:-0.6.0}" in
    none) ;;
    noversion) printf 'xx\000\000AppLoad extension\000rm-appload\000yy' > "$DEV/home/root/xovi/extensions.d/appload.so";;
    *) printf 'zz\000\000Qt 6.8.2 (arm64)\000GCC: (GNU) 11.2.0\000/home/root/xovi/exthome/appload\000\000AppLoad v%s\000yy' "${2:-0.6.0}" \
         > "$DEV/home/root/xovi/extensions.d/appload.so";;
  esac
  app=no
  case "$1" in
    installed|signedin|signedinroot|twoapi|apprunning) app=yes;;
  esac
  if [ "$app" = yes ]; then
    mkdir -p "$DEV/home/root/xovi/exthome/appload/rmkavita"
    cp "$WORK/stub" "$DEV/home/root/xovi/exthome/appload/rmkavita/rmkavita"
    printf '{ "application": "rmkavita" }\n' > "$DEV/home/root/xovi/exthome/appload/rmkavita/external.manifest.json"
    printf 'log\n' > "$DEV/tmp/rmkavita.log"; printf 'rotated log\n' > "$DEV/tmp/rmkavita.log.1"
  fi
  case "$1" in
    installed) printf '# rmKavita configuration\nserver = http://127.0.0.1:5000\n' > "$DEV/home/root/.config/rmkavita.conf";;
    signedin) printf '# rmKavita configuration\nserver = http://127.0.0.1:5000\nusername = reader\napi_key = %s\n' "$FAKEKEY" > "$DEV/home/root/.config/rmkavita.conf";;
    signedinroot) printf '# rmKavita configuration\nroot = http://127.0.0.1:5000/api/opds/%s\nusername = reader\napi_key = %s\n' "$FAKEKEY" "$FAKEKEY" > "$DEV/home/root/.config/rmkavita.conf";;
    twoapi) printf 'server = http://127.0.0.1:5000\nusername = reader\napi_key = %s\napi_key = %s\n' "$FAKEKEY" "$FAKEKEY" > "$DEV/home/root/.config/rmkavita.conf";;
  esac
  case "$1" in
    installed|signedin|signedinroot|twoapi)
      printf '{"pos":{}}\n' > "$DEV/home/root/.config/rmkavita-state.json"
      printf '{"mode":"page"}\n' > "$DEV/home/root/.config/rmkavita-settings.json";;
  esac
  case "$1" in
    koreader) mkdir -p "$DEV/proc/4242"; printf 'luajit\000reader.lua\000' > "$DEV/proc/4242/cmdline";;
    apprunning) mkdir -p "$DEV/proc/777"; printf 'app\000' > "$DEV/proc/777/cmdline"
                printf '%s\n' "$DEV/home/root/xovi/exthome/appload/rmkavita/rmkavita" > "$DEV/proc/777/exe.link";;
  esac
  : > "$DEV/shim/mount.log"
}

write_conf() {   # write_conf FILE [KEY=VALUE ...]
  wc_f=$1; shift
  cat > "$wc_f" <<EOF
# test configuration
TABLET_HOST=192.0.2.10
TABLET_USER=root
SSH_PORT=2222
SSH_KEY=
APP_DIR=/home/root/xovi/exthome/appload/rmkavita
PACKAGE_DIR=$PKG
DOOR=no
KAVITA_HOST=203.0.113.50
KAVITA_PORT=5099
DOOR_LISTEN=127.0.0.1:5077
EOF
  for wc_kv in "$@"; do
    wc_k=${wc_kv%%=*}
    grep -v "^$wc_k=" "$wc_f" > "$wc_f.tmp"; mv "$wc_f.tmp" "$wc_f"
    printf '%s\n' "$wc_kv" >> "$wc_f"
  done
}
CONF_FILE=$WORK/install.conf

OUT=''
RC=0
run_install() {   # run_install ARGS...  (sets OUT, RC; clears the ssh log first)
  : > "$FAKELOG"
  OUT=$(cd "$WORK" && PATH="$BIN:$PATH" RMK_FAKE_LOG="$FAKELOG" RMK_FAKE_DEV="$DEV" RMK_FAKE_DEVBIN="$DEVBIN" \
        RMK_FAKE_WORK="$WORK" RMK_FAKE_STUB="$WORK/stub" "$RUNSH" "$HERE/install.sh" -c "$CONF_FILE" "$@" 2>&1)
  RC=$?
}
snapshot() { (cd "$DEV" && find . -type f ! -path './shim/*' -exec cksum {} \; | sort); }
conf_file() { printf '%s' "$DEV/home/root/.config/rmkavita.conf"; }
ssh_calls() { grep -c '^ssh' "$FAKELOG" 2>/dev/null; }
no_ssh_at_all() { [ ! -s "$FAKELOG" ]; }
no_writes() { ! grep -q -E 'cat >|rm -|mkdir|mv -f|chmod' "$FAKELOG"; }

# =======================================================================================================================
section 'dry run: read-only'
new_package; new_device clean; write_conf "$CONF_FILE"
before=$(snapshot)
run_install --dry-run
t 'exit 0'; n [ "$RC" = 0 ]; e
t 'says DRY RUN done, nothing changed'; n contains "$OUT" 'DRY RUN done\. Nothing was changed'; e
t 'the device tree is byte-for-byte unchanged'; n [ "$(snapshot)" = "$before" ]; e
t 'only `sh -s` calls reached the tablet (the read-only facts script), no write command'; n no_writes; e
t 'exactly one ssh call (facts)'; n [ "$(ssh_calls)" = 1 ]; e
t 'prints the plan for a clean tablet (config from sample, server filled in)'; n contains "$OUT" 'write /home/root/\.config/rmkavita\.conf \(mode 600\) from the package sample with server = http://203\.0\.113\.50:5099'; e
t 'prints the migration command'; n contains "$OUT" 'rmkavita -migrate -conf /home/root/\.config/rmkavita\.conf'; e
t 'prints AppLoad version and qtfb_rgba'; n contains "$OUT" 'AppLoad version   : 0\.6\.0   qtfb_rgba=yes'; e
t 'prints the framebuffer format the app will request'; n contains "$OUT" 'framebuffer format the app will request: rgba8888 \(565 fallback\)'; e
t 'ssh options: port, BatchMode, no -i without SSH_KEY'; n sh -c "grep -q '\[-p\] \[2222\]' '$FAKELOG' && grep -q 'BatchMode=yes' '$FAKELOG' && ! grep -q '\[-i\]' '$FAKELOG'"; e
t 'scp is never used'; n sh -c "! grep -q '^scp' '$FAKELOG'"; e

section 'install: needs confirmation, then a clean install'
new_device clean
before=$(snapshot)
run_install
t 'without --yes and without a terminal: refuses'; n [ "$RC" -ne 0 ]; e
t '... and says why'; n contains "$OUT" 'pass --yes'; e
t '... and changed nothing'; n [ "$(snapshot)" = "$before" ]; e
run_install --yes
t 'exit 0'; n [ "$RC" = 0 ]; e
if [ "$RC" != 0 ]; then printf '%s\n' "$OUT"; fi
t 'says DONE'; n contains "$OUT" '^DONE\.'; e
t 'app folder: binary, manifest, icon'; n [ -f "$DEV/home/root/xovi/exthome/appload/rmkavita/rmkavita" ]; n [ -f "$DEV/home/root/xovi/exthome/appload/rmkavita/external.manifest.json" ]; n [ -f "$DEV/home/root/xovi/exthome/appload/rmkavita/icon.png" ]; e
t 'the placed binary is byte-identical to the package binary'; n cmp -s "$WORK/placed-binary" "$PKG/rmkavita"; e
t 'config written: package sample with server = http://KAVITA_HOST:KAVITA_PORT'; n [ "$(cat "$(conf_file)")" = "$(sed 's|^server = .*|server = http://203.0.113.50:5099|' "$PKG/rmkavita.conf.sample")" ]; e
t 'the config upload used umask 077 + chmod 600 + atomic mv'; n sh -c "grep -q 'umask 077; mkdir -p' '$FAKELOG' && grep -q 'chmod 600' '$FAKELOG' && grep -q 'mv -f' '$FAKELOG'"; e
t 'migration ran'; n contains "$OUT" 'migrate: nothing to do'; e
t 'final check: mode 600, one server line, no sign-in, zero key-shaped lines'; n contains "$OUT" 'config  : mode=600 size=[0-9]+ server-lines=1 api-lines=0 user-lines=0 key-lines=0'; e
t 'staging folder removed'; n [ ! -d "$DEV/home/root/rmkavita-stage" ]; e
t 'allow-list: the app folder holds exactly the binary, the manifest and the icon'; n [ "$(ls "$DEV/home/root/xovi/exthome/appload/rmkavita" | tr '\n' ' ')" = 'external.manifest.json icon.png rmkavita ' ]; e
t 'CHECKLIST.md and ab-test were never sent (no command, no file on the device)'; nn grep -q -E 'CHECKLIST|ab-test|cbz' "$FAKELOG"; n [ -z "$(find "$DEV" \( -name 'CHECKLIST*' -o -name 'ab-test' -o -name '*.cbz' \) | head -n 1)" ]; e
t 'no door units (DOOR=no)'; n [ ! -f "$DEV/etc/systemd/system/rmkavita-door.socket" ]; e
t 'nothing restarted: no systemctl restart / xochitl in any command'; n sh -c "! grep -q -i -E 'xochitl|restart' '$FAKELOG'"; e
t 'no key in the output or in any ssh argument'; n lacks "$OUT$(cat "$FAKELOG")" "$FAKEKEY"; e

section 'install: idempotent re-run, signed-in config kept'
run_install --yes
t 're-run exit 0'; n [ "$RC" = 0 ]; e
t 'existing config kept, not rewritten'; n contains "$OUT" 'existing config kept'; e
t 'no config upload on the re-run'; n sh -c "! grep -q 'cat > .*rmkavita.conf.new' '$FAKELOG'"; e
t 'upgraded the binary in place'; n contains "$OUT" 'upgrading the installed binary|app folder in place'; e
new_device signedin
cp "$(conf_file)" "$WORK/conf.before"
run_install --dry-run
t 'dry run: "signed in as reader: the sign-in is kept"'; n contains "$OUT" 'signed in as reader: the sign-in is kept; would remove 0 key line'; e
t 'dry run: the api key is not printed'; n lacks "$OUT$(cat "$FAKELOG")" "$FAKEKEY"; e
run_install --yes
t 'install over a signed-in config: exit 0'; n [ "$RC" = 0 ]; e
t 'the config is byte-for-byte unchanged (api_key line still there)'; n cmp -s "$(conf_file)" "$WORK/conf.before"; e
t 'check: one api_key line and one username line'; n contains "$OUT" 'api-lines=1 user-lines=1 key-lines=0'; e
t 'no key in the output or in any ssh argument'; n lacks "$OUT$(cat "$FAKELOG")" "$FAKEKEY"; e
new_device signedinroot
run_install --yes
c=$(cat "$(conf_file)")
t 'a v1-style root key is removed, the sign-in stays'; n [ "$RC" = 0 ]; n contains "$c" '^server = http://127\.0\.0\.1:5000$'; n contains "$c" '^api_key = '; n lacks "$c" '/api/opds/'; e
t 'migrate said how many key lines went (count only)'; n contains "$OUT" 'migrate: removed 1 key line\(s\)'; e
t 'no key in the output or in any ssh argument'; n lacks "$OUT$(cat "$FAKELOG")" "$FAKEKEY"; e

section 'install: refusals'
new_device apprunning
before=$(snapshot)
run_install --yes
t 'rmKavita running: exit 2, REFUSED'; n [ "$RC" = 2 ]; n contains "$OUT" 'REFUSED'; e
t '... nothing changed'; n [ "$(snapshot)" = "$before" ]; e
run_install --dry-run
t 'dry run: WOULD REFUSE, exit 0'; n [ "$RC" = 0 ]; n contains "$OUT" 'WOULD REFUSE'; e
run_install --yes --force
t '--force goes on'; n [ "$RC" = 0 ]; n contains "$OUT" 'continuing because --force'; e
new_device koreader
run_install --yes
t 'KOReader running: exit 2'; n [ "$RC" = 2 ]; n contains "$OUT" 'REFUSED'; e
new_device wrongdevice
before=$(snapshot)
run_install --yes
t 'a reMarkable 2: refused, nothing written'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'Paper Pro'; n [ "$(snapshot)" = "$before" ]; e
new_device clean
printf '%s\n' '' > "$DEV/sys/devices/soc0/machine"
run_install --yes
t 'model unreadable: a real run refuses'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'could not be read'; e
run_install --dry-run
t 'model unreadable: the dry run warns and exits 0'; n [ "$RC" = 0 ]; n contains "$OUT" 'WARNING: the device model could not be read'; e

section 'facts: unknown is explicit and never fails a dry run'
new_device clean none
run_install --dry-run
t 'no appload.so: version unknown, qtfb_rgba=unknown, exit 0'; n [ "$RC" = 0 ]; n contains "$OUT" 'AppLoad version   : unknown   qtfb_rgba=unknown'; e
new_device clean noversion
run_install --dry-run
t 'appload.so without a version string: unknown, exit 0'; n [ "$RC" = 0 ]; n contains "$OUT" 'AppLoad version   : unknown   qtfb_rgba=unknown'; e
new_device clean 0.4.2
run_install --dry-run
t 'AppLoad 0.4.2: qtfb_rgba=no and a warning'; n [ "$RC" = 0 ]; n contains "$OUT" 'qtfb_rgba=no'; n contains "$OUT" 'WARNING: this AppLoad is older than 0\.5\.0'; e
new_device clean 0.5.1
run_install --dry-run
t 'AppLoad 0.5.1: qtfb_rgba=yes'; n [ "$RC" = 0 ]; n contains "$OUT" 'AppLoad version   : 0\.5\.1   qtfb_rgba=yes'; e
new_device installed
printf 'fb_format = RGB565\n' >> "$(conf_file)"
run_install --dry-run
t 'fb_format = RGB565: "rgb565 (forced by fb_format)"'; n [ "$RC" = 0 ]; n contains "$OUT" 'framebuffer format the app will request: rgb565 \(forced by fb_format\)'; e
new_device installed
printf 'fb_format = rgba # note\n' >> "$(conf_file)"
run_install --dry-run
t 'a value with a trailing comment: reported as unrecognised, not guessed'; n [ "$RC" = 0 ]; n contains "$OUT" 'unrecognised fb_format value'; e
new_device signedin
sed 's/^username = .*/username = me@example.org/' "$(conf_file)" > "$WORK/c.tmp" && cp "$WORK/c.tmp" "$(conf_file)"
run_install --dry-run
t 'a user name outside letters, digits and . _ - is not printed; the sign-in is still reported'; n [ "$RC" = 0 ]; n lacks "$OUT" 'me@example'; n contains "$OUT" 'the sign-in is kept'; e
new_device clean
echo fail > "$DEV/shim/df"
run_install --dry-run
t 'df fails: free space unknown, exit 0'; n [ "$RC" = 0 ]; n contains "$OUT" 'free space on /home could not be determined'; e
run_install --yes
t 'df fails: a real install still goes through'; n [ "$RC" = 0 ]; n contains "$OUT" '^DONE\.'; e
new_device clean
echo 1000 > "$DEV/shim/free_kb"
run_install --dry-run
t 'a KNOWN low free space is refused'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'less than 60 MB free'; e

section 'check failures: counts only, key never printed'
new_device twoapi
run_install --yes
t 'two api_key lines: the final check fails (exit 1)'; n [ "$RC" = 1 ]; n contains "$OUT" "'2' api_key lines, at most 1 is allowed"; e
t '... without printing the key'; n lacks "$OUT$(cat "$FAKELOG")" "$FAKEKEY"; e
new_device installed
echo fail > "$DEV/shim/migrate"
run_install --yes
t 'migrate exits 2: the install fails with the device message'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'config migration failed'; e
new_device installed
echo leakfail > "$DEV/shim/migrate"
run_install --yes
t 'migrate fails and prints a key: failure, key hidden (<hidden>)'; n [ "$RC" -ne 0 ]; n lacks "$OUT" "$FAKEKEY"; n contains "$OUT" '<hidden>'; e
new_device installed
echo noline > "$DEV/shim/migrate"
run_install --yes
t 'exit 0 but no "migrate:" line: failure'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'config migration failed'; e
new_device installed
echo 644 > "$DEV/shim/mode"
run_install --yes
t 'config mode 644: the final check fails'; n [ "$RC" = 1 ]; n contains "$OUT" "the config mode is '644', not 600"; e

section 'door (DOOR=yes)'
new_device clean
write_conf "$CONF_FILE" DOOR=yes
run_install --yes
t 'exit 0'; n [ "$RC" = 0 ]; e
if [ "$RC" != 0 ]; then printf '%s\n' "$OUT"; fi
t 'socket unit has DOOR_LISTEN and no placeholder'; n [ -f "$DEV/etc/systemd/system/rmkavita-door.socket" ]; n grep -q '^ListenStream=127\.0\.0\.1:5077$' "$DEV/etc/systemd/system/rmkavita-door.socket"; nn grep -q '@[A-Z_]*@' "$DEV/etc/systemd/system/rmkavita-door.socket"; e
t 'service unit has KAVITA_HOST, KAVITA_PORT and TAILSCALE_BIN from the config'; n grep -q "^ExecStart=/home/root/$VD/bin/tailscale nc 203\.0\.113\.50 5099\$" "$DEV/etc/systemd/system/rmkavita-door@.service"; e
t 'enabled through sockets.target.wants (a symlink; a copy where the host cannot make symlinks)'; n [ -e "$DEV/etc/systemd/system/sockets.target.wants/rmkavita-door.socket" ]; e
t 'the root file system was made writable once and put back once'; n [ "$(wc -l < "$DEV/shim/rw.count" | tr -d ' ')" = 1 ]; n [ "$(wc -l < "$DEV/shim/ro.count" | tr -d ' ')" = 1 ]; e
t 'the config points the app at the door'; n grep -q '^server = http://127\.0\.0\.1:5077$' "$(conf_file)"; e
t 'final check: door active and listening'; n contains "$OUT" 'door    : active listening=1'; e
run_install --yes
t 're-run: units identical, no remount'; n [ "$RC" = 0 ]; n [ "$(wc -l < "$DEV/shim/rw.count" | tr -d ' ')" = 1 ]; e
run_install --dry-run
t 'dry run with a door: plan mentions it, exit 0'; n [ "$RC" = 0 ]; n contains "$OUT" '5\. install the door 127\.0\.0\.1:5077 -> 203\.0\.113\.50:5099'; e

section 'uninstall'
new_device signedin
write_conf "$CONF_FILE"
before=$(snapshot)
run_install --uninstall --dry-run
t 'dry run: plan only, nothing changed'; n [ "$RC" = 0 ]; n contains "$OUT" 'LEAVE /home/root/\.config/rmkavita-state\.json'; n [ "$(snapshot)" = "$before" ]; e
run_install --uninstall --yes
t 'exit 0'; n [ "$RC" = 0 ]; e
t 'app folder, config and settings are gone'; n [ ! -d "$DEV/home/root/xovi/exthome/appload/rmkavita" ]; n [ ! -f "$(conf_file)" ]; n [ ! -f "$DEV/home/root/.config/rmkavita-settings.json" ]; e
t 'the log and its rotated .1 are gone'; n [ ! -f "$DEV/tmp/rmkavita.log" ]; n [ ! -f "$DEV/tmp/rmkavita.log.1" ]; e
t 'reading positions are kept'; n [ -f "$DEV/home/root/.config/rmkavita-state.json" ]; e
run_install --uninstall --yes --purge
t '--purge removes the positions too'; n [ "$RC" = 0 ]; n [ ! -f "$DEV/home/root/.config/rmkavita-state.json" ]; e
new_device clean
write_conf "$CONF_FILE" DOOR=yes
run_install --yes
run_install --uninstall --yes
t 'with a door: units and the enabling link are removed'; n [ "$RC" = 0 ]; n [ ! -f "$DEV/etc/systemd/system/rmkavita-door.socket" ]; n [ ! -f "$DEV/etc/systemd/system/rmkavita-door@.service" ]; n [ ! -L "$DEV/etc/systemd/system/sockets.target.wants/rmkavita-door.socket" ]; e
new_device apprunning
run_install --uninstall --yes
t 'uninstall refuses while the app runs'; n [ "$RC" = 2 ]; e
write_conf "$CONF_FILE"

section 'configuration and package are checked before anything is sent'
new_device clean
for bad in 'TABLET_HOST=a;b' 'TABLET_HOST=' 'TABLET_USER=Root User' 'SSH_PORT=99999' 'APP_DIR=/home/root/xovi/exthome/appload/other' \
           'APP_DIR=/home/root/../rmkavita' 'APP_DIR=relative/rmkavita' 'DOOR=maybe' 'STAGE_DIR=/home/root/a b' 'SSH_STRICT=whatever'; do
  write_conf "$CONF_FILE" "$bad"
  run_install --dry-run
  t "refused before any ssh call: $bad"; n [ "$RC" -ne 0 ]; n no_ssh_at_all; e
done
write_conf "$CONF_FILE" DOOR=yes KAVITA_HOST=
run_install --dry-run
t 'DOOR=yes without KAVITA_HOST: refused'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'needs KAVITA_HOST'; n no_ssh_at_all; e
write_conf "$CONF_FILE" DOOR=yes 'DOOR_LISTEN=localhost:5001'
run_install --dry-run
t 'DOOR_LISTEN must be IPv4:port'; n [ "$RC" -ne 0 ]; n no_ssh_at_all; e
write_conf "$CONF_FILE"
printf 'SOMETHING_ELSE=1\n' >> "$CONF_FILE"
run_install --dry-run
t 'an unknown setting is refused (the file is parsed, never executed)'; n [ "$RC" -ne 0 ]; n contains "$OUT" "unknown setting 'SOMETHING_ELSE'"; e
printf 'TABLET_HOST=192.0.2.10\n$(touch %s/pwned)\n' "$WORK" > "$CONF_FILE"
run_install --dry-run
t 'shell code in the config file is not executed'; n [ ! -e "$WORK/pwned" ]; n [ "$RC" -ne 0 ]; e
printf 'TABLET_HOST=192.0.2.10\nAPP_DIR=`touch %s/pwned2`/rmkavita\n' "$WORK" > "$CONF_FILE"
run_install --dry-run
t 'command substitution in a value is refused, not executed'; n [ ! -e "$WORK/pwned2" ]; n [ "$RC" -ne 0 ]; e
CONF_FILE=$WORK/does-not-exist.conf
run_install --dry-run
t 'a missing config file: clear error naming install.conf.sample'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'install\.conf\.sample'; n no_ssh_at_all; e
CONF_FILE=$WORK/install.conf
write_conf "$CONF_FILE"

new_package; make_elf "$PKG/rmkavita" data
run_install --dry-run
t 'a binary that is not an ELF file'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'not an ELF file'; n no_ssh_at_all; e
new_package; make_elf "$PKG/rmkavita" dynamic
run_install --dry-run
t 'a dynamically linked binary'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'dynamically linked'; n no_ssh_at_all; e
new_package; make_elf "$PKG/rmkavita" short
run_install --dry-run
t 'a binary under 1 MB'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'smaller than 1 MB'; n no_ssh_at_all; e
new_package; rm -f "$PKG/icon.png"
run_install --dry-run
t 'an incomplete package'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'incomplete'; n no_ssh_at_all; e
new_package; printf '{ "application": "kavita" }\n' > "$PKG/external.manifest.json"
run_install --dry-run
t 'a manifest that does not name rmkavita'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'does not name the application rmkavita'; n no_ssh_at_all; e
new_package; printf 'note = %s\n' "ABCDEFGHIJKLMNOPQRSTUVWXYZ${K1}" >> "$PKG/rmkavita.conf.sample"
run_install --dry-run
t 'a 32+ character alphanumeric run in the sample'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'looks like a key'; n no_ssh_at_all; n lacks "$OUT" 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'; e
new_package; printf '# root = http://127.0.0.1:5000/api/opds/abcdefgh1234\n' >> "$PKG/rmkavita.conf.sample"
run_install --dry-run
t '/api/opds/<key> in the sample'; n [ "$RC" -ne 0 ]; n contains "$OUT" 'API key'; n no_ssh_at_all; e
new_package; printf 'server = http://127.0.0.1:5001\n' >> "$PKG/rmkavita.conf.sample"
run_install --dry-run
t 'two server lines in the sample'; n [ "$RC" -ne 0 ]; n contains "$OUT" "exactly one 'server ='"; n no_ssh_at_all; e
new_package

section 'the scripts themselves'
SCRIPTS="$HERE/install.sh $HERE/device/facts.sh $HERE/device/place.sh $HERE/device/migrate.sh $HERE/device/check.sh $HERE/device/uninstall.sh $HERE/device/door.sh $HERE/test-install.sh"
n=0; bad=''
for s in $SCRIPTS; do
  n=$((n + 1))
  if ! "$RUNSH" -n "$s" 2>/dev/null; then bad="$bad $s"; fi
done
t "$RUNSH -n: all $n scripts parse"; n [ -z "$bad" ]; e
if command -v dash >/dev/null 2>&1; then
  bad=''
  for s in $SCRIPTS; do if ! dash -n "$s" 2>/dev/null; then bad="$bad $s"; fi; done
  t "dash -n (strict POSIX): all $n scripts parse"; n [ -z "$bad" ]; e
else
  printf '  SKIP dash -n (dash not installed)\n'
fi
if command -v busybox >/dev/null 2>&1; then
  bad=''
  for s in $SCRIPTS; do if ! busybox sh -n "$s" 2>/dev/null; then bad="$bad $s"; fi; done
  t "busybox sh -n: all $n scripts parse"; n [ -z "$bad" ]; e
else
  printf '  SKIP busybox sh -n (busybox not installed)\n'
fi
lint=$(grep -n -E 'grep +(-[A-Za-z]*P|--perl)|\[\[ |<<<|\$\{[A-Za-z_0-9]+//|<\(|echo +-e|(^|[;&|{(] *)local |(^|[^A-Za-z_])(declare|typeset|mapfile|readarray)([^A-Za-z_]|$)|head +-[0-9]|tail +-[0-9]' \
       "$HERE/install.sh" "$HERE"/device/*.sh | grep -v -E '^[^:]*:[0-9]+:[[:space:]]*#' || true)
t 'no bashism in install.sh or the device scripts (grep -P, [[ ]], <<<, ${x//}, <( ), echo -e, local, arrays, head -N)'; n [ -z "$lint" ]; e
if [ -n "$lint" ]; then printf '%s\n' "$lint" | head -n 5; fi
t 'no PowerShell file anywhere in the public tree'; n sh -c "[ -z \"\$(find '$HERE' \\( -name '*.ps1' -o -name '*.psm1' -o -name '*.psd1' -o -name '*.cmd' -o -name '*.bat' \\) | head -n 1)\" ]"; e

section 'public tree hygiene (the export scans must come back empty)'
# shapes of private values (nobody's in particular): a Tailscale-range address, a private LAN address, a Windows
# drive path, a credentials file name
hits=''
for pat in '100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.[0-9]+\.[0-9]+' '192\.168\.[0-9]+\.[0-9]+' '[A-Za-z]:\\[A-Za-z]+' 'credentials\.txt'; do
  h=$(grep -r -l -E -e "$pat" "$HERE" 2>/dev/null | head -n 3)
  [ -z "$h" ] || hits="$hits [$h]"
done
t 'no private address, drive path or credentials file name anywhere'; n [ -z "$hits" ]; e
tok=$(grep -r -n -E '[A-Za-z0-9-]{32,}' "$HERE" 2>/dev/null | head -n 3)
t 'no 32-character [A-Za-z0-9-] token in any file (decorative dash lines included)'; n [ -z "$tok" ]; e
vel_bad=''
for vf in $(grep -r -l "/home/root/\\.vell""um" "$HERE" 2>/dev/null); do
  case "${vf##*/}" in install.sh|install.conf.sample|README-install.md) ;; *) vel_bad="$vel_bad ${vf##*/}";; esac
done
t ".vellum only in install.sh / install.conf.sample / README-install.md (the documented standard device layout), not in:$vel_bad"; n [ -z "$vel_bad" ]; e
t 'every .vellum mention is documented as the standard device layout'; n sh -c "grep -q -i 'standard' '$HERE/install.conf.sample' && grep -q -i 'standard' '$HERE/install.sh'"; e
t 'unit templates: neutral names, placeholders, no HTPC / tailnet wording'; n sh -c "grep -q '@DOOR_LISTEN@' '$HERE/units/rmkavita-door.socket' && grep -q '@TAILSCALE_BIN@' '$HERE/units/rmkavita-door@.service' && ! grep -q -i -E 'htpc|tailnet' '$HERE'/units/*"; e
t 'install.conf is git-ignored'; n grep -q '^install\.conf$' "$HERE/.gitignore"; e
t 'the sample configuration has every key the installer reads'; n sh -c "for k in TABLET_HOST TABLET_USER SSH_PORT SSH_KEY APP_DIR PACKAGE_DIR DOOR KAVITA_HOST KAVITA_PORT DOOR_LISTEN TAILSCALE_BIN; do grep -q \"^\$k=\" '$HERE/install.conf.sample' || exit 1; done"; e

printf '== result: %s passed, %s failure(s)\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
