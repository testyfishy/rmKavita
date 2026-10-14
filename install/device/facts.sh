#!/bin/sh
# rmKavita installer, tablet side: a READ-ONLY look at the device.
# Prints key=value lines. Never changes anything; never prints config content (only counts and a few words
# reduced to a safe alphabet). POSIX sh + busybox applets only (grep -c/-E/-v, sed, tr, cut, head -n, awk).
# A fact that cannot be determined prints a number or "unknown", never an error.
#
# usage: sh -s -- KEY=VALUE ...
#   APP_DIR CONF STATE SETTINGS OLD_CONF OLD_STATE UNIT_DIR DOOR_SOCKET DOOR_SERVICE TAILSCALE_BIN DOOR_PORT

APP_DIR=/home/root/xovi/exthome/appload/rmkavita
CONF=/home/root/.config/rmkavita.conf
STATE=/home/root/.config/rmkavita-state.json
SETTINGS=/home/root/.config/rmkavita-settings.json
OLD_CONF=/home/root/.config/kavita-reader.conf
OLD_STATE=/home/root/.config/kavita-reader-state.json
UNIT_DIR=/etc/systemd/system
DOOR_SOCKET=rmkavita-door.socket
DOOR_SERVICE=rmkavita-door@.service
TAILSCALE_BIN=''
DOOR_PORT=5001
for kv in "$@"; do
  case "$kv" in
    APP_DIR=*) APP_DIR=${kv#APP_DIR=};;
    CONF=*) CONF=${kv#CONF=};;
    STATE=*) STATE=${kv#STATE=};;
    SETTINGS=*) SETTINGS=${kv#SETTINGS=};;
    OLD_CONF=*) OLD_CONF=${kv#OLD_CONF=};;
    OLD_STATE=*) OLD_STATE=${kv#OLD_STATE=};;
    UNIT_DIR=*) UNIT_DIR=${kv#UNIT_DIR=};;
    DOOR_SOCKET=*) DOOR_SOCKET=${kv#DOOR_SOCKET=};;
    DOOR_SERVICE=*) DOOR_SERVICE=${kv#DOOR_SERVICE=};;
    TAILSCALE_BIN=*) TAILSCALE_BIN=${kv#TAILSCALE_BIN=};;
    DOOR_PORT=*) DOOR_PORT=${kv#DOOR_PORT=};;
  esac
done

# file mode / size with fallbacks (a stat without -c still answers; "unknown" instead of an empty value)
fmode() {
  m=$(stat -c %a "$1" 2>/dev/null)
  if [ -z "$m" ]; then
    case $(ls -ld "$1" 2>/dev/null | cut -c1-10) in
      -rw-------) m=600;;
      -rw-r--r--) m=644;;
      -rw-rw-rw-) m=666;;
      -r--------) m=400;;
      -*) m=other;;
      *) m=unknown;;
    esac
  fi
  echo "$m"
}

P_KEYLINE='^[[:space:]]*(api_key|apikey|key)[[:space:]]*='
P_OPDS='/api/opds/[A-Za-z0-9_-]{8,}'
P_RUN='[A-Za-z0-9]{32,}'
P_API='^[[:space:]]*api_key[[:space:]]*='
P_USER='^[[:space:]]*username[[:space:]]*='

echo "machine=$(cat /sys/devices/soc0/machine 2>/dev/null)"
APPLOAD_DIR=$(dirname "$APP_DIR")
echo "appload=$([ -d "$APPLOAD_DIR" ] && echo yes || echo no)"
echo "appdir=$([ -d "$APP_DIR" ] && echo yes || echo no)"
echo "binary=$([ -x "$APP_DIR/rmkavita" ] && echo yes || echo no)"
echo "conf=$([ -f "$CONF" ] && fmode "$CONF" || echo none)"
KL=0; AL=0; UL=0; VL=0; UN=''
if [ -f "$CONF" ]; then
  KL=$(grep -v -E "$P_API" "$CONF" 2>/dev/null | grep -c -i -E -e "$P_KEYLINE" -e "$P_OPDS" -e "$P_RUN")
  VL=$(grep -c -E "$P_OPDS" "$CONF" 2>/dev/null)
  AL=$(grep -c -E "$P_API" "$CONF" 2>/dev/null)
  UL=$(grep -c -E "$P_USER" "$CONF" 2>/dev/null)
  UN=$(sed -n 's/^[[:space:]]*username[[:space:]]*=[[:space:]]*//p' "$CONF" 2>/dev/null | head -n 1 | tr -d '\r' | cut -c1-32)
  case "$UN" in *[!A-Za-z0-9._-]*) UN='?';; esac
fi
echo "conf_keylines=${KL:-?}"
echo "conf_v1keys=${VL:-?}"
echo "conf_apilines=${AL:-?}"
echo "conf_userlines=${UL:-?}"
echo "conf_username=$UN"
echo "settings=$([ -f "$SETTINGS" ] && echo yes || echo no)"
echo "state=$([ -f "$STATE" ] && echo yes || echo no)"
echo "old_conf=$([ -f "$OLD_CONF" ] && fmode "$OLD_CONF" || echo none)"
echo "old_state=$([ -f "$OLD_STATE" ] && echo yes || echo no)"
echo "units=$([ -f "$UNIT_DIR/$DOOR_SOCKET" ] && [ -f "$UNIT_DIR/$DOOR_SERVICE" ] && echo yes || echo no)"
echo "socket=$(systemctl is-active "$DOOR_SOCKET" 2>/dev/null)"
echo "door_listen=$( (netstat -ltn 2>/dev/null) | grep -c ":$DOOR_PORT ")"
if [ -n "$TAILSCALE_BIN" ]; then
  echo "tailscale=$([ -x "$TAILSCALE_BIN" ] && echo yes || echo no)"
else
  echo "tailscale=unknown"
fi
FK=$(df -k /home/root 2>/dev/null | tail -n 1 | awk '{print $(NF-2)}')
echo "free_kb=${FK:-unknown}"
A=reader; B=.lua; ME=$$
for d in /proc/[0-9]*; do
  p=${d#/proc/}
  [ "$p" = "$ME" ] && continue
  c=$(tr '\0' ' ' < "$d/cmdline" 2>/dev/null) || continue
  case "$c" in *"$A$B"*) echo "koreader_pid=$p";; esac
  e=$(readlink "$d/exe" 2>/dev/null)
  if [ "$e" = "$APP_DIR/rmkavita" ]; then echo "app_pid=$p"; fi
done

# --- best-effort facts come LAST (a failure in them cannot lose the ones above) --------
APF=''
for f in /home/root/xovi/extensions.d/appload*.so* /home/root/xovi/appload*.so* /home/root/xovi/*/appload*.so* /home/root/xovi/*/*/appload*.so*; do
  if [ -f "$f" ]; then APF="$f"; break; fi
done
if [ -z "$APF" ]; then
  APF=$(find /home/root/xovi -maxdepth 3 -type f -name 'appload*.so*' 2>/dev/null | head -n 1)
fi
AVS=unknown
if [ -n "$APF" ]; then
  AVV=$(tr -c '[:print:]' '\n' < "$APF" 2>/dev/null | grep -i appload | grep -E '[0-9]+\.[0-9]+\.[0-9]+' | sed -n 's/^[^0-9]*\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*$/\1/p' | head -n 1)
  if [ -n "$AVV" ]; then AVS="$AVV"; fi
  if [ "$AVS" = unknown ]; then
    AVV=$(printf '%s\n' "${APF##*/}" | sed -n 's/^[^0-9]*\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*$/\1/p' | head -n 1)
    if [ -n "$AVV" ]; then AVS="$AVV"; fi
  fi
fi
echo "appload_version=$AVS"
FBV=''
if [ -f "$CONF" ]; then
  FBN=$(grep -c -E '^[[:space:]]*fb_format[[:space:]]*=' "$CONF" 2>/dev/null)
  if [ "${FBN:-0}" != 0 ]; then
    FBV=$(sed -n 's/^[[:space:]]*fb_format[[:space:]]*=[[:space:]]*\([^[:space:]]*\)[[:space:]]*$/\1/p' "$CONF" 2>/dev/null | head -n 1 | tr 'A-Z' 'a-z' | cut -c1-32)
    case "$FBV" in ''|*[!a-z0-9._-]*) FBV='?';; esac
  fi
fi
echo "fb_format=${FBV:-auto}"
true
