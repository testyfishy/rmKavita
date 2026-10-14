#!/bin/sh
# rmKavita installer, tablet side: the final check. COUNTS only; never prints config content. Removes the
# staging directory at the end.
#
# usage: sh -s -- APP_DIR=... CONF=... STAGE=... [DOOR_SOCKET=... DOOR_PORT=...]
#
# config line: mode, size, `server =` lines, `api_key =` lines (the app's own sign-in; at most 1),
#   `username =` lines, and key-shaped lines OUTSIDE the api_key line (must be 0).

APP_DIR=''
CONF=''
STAGE=''
DOOR_SOCKET=''
DOOR_PORT=5001
for kv in "$@"; do
  case "$kv" in
    APP_DIR=*) APP_DIR=${kv#APP_DIR=};;
    CONF=*) CONF=${kv#CONF=};;
    STAGE=*) STAGE=${kv#STAGE=};;
    DOOR_SOCKET=*) DOOR_SOCKET=${kv#DOOR_SOCKET=};;
    DOOR_PORT=*) DOOR_PORT=${kv#DOOR_PORT=};;
  esac
done

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
fsize() {
  s=$(stat -c %s "$1" 2>/dev/null)
  if [ -z "$s" ]; then s=$(wc -c < "$1" 2>/dev/null | tr -d ' '); fi
  echo "${s:-unknown}"
}

P_KEYLINE='^[[:space:]]*(api_key|apikey|key)[[:space:]]*='
P_OPDS='/api/opds/[A-Za-z0-9_-]{8,}'
P_RUN='[A-Za-z0-9]{32,}'
P_API='^[[:space:]]*api_key[[:space:]]*='
P_USER='^[[:space:]]*username[[:space:]]*='
P_SERVER='^[[:space:]]*server[[:space:]]*='

echo "version : $("$APP_DIR/rmkavita" -version 2>&1 < /dev/null | head -n 1)"
echo "app     : $(ls -la "$APP_DIR/rmkavita" 2>/dev/null | awk '{print $1, $5}')"
echo "manifest: $(grep -c -E '"application"[[:space:]]*:[[:space:]]*"rmkavita"' "$APP_DIR/external.manifest.json" 2>/dev/null) match"
echo "config  : mode=$(fmode "$CONF") size=$(fsize "$CONF") server-lines=$(grep -c -E "$P_SERVER" "$CONF" 2>/dev/null) api-lines=$(grep -c -E "$P_API" "$CONF" 2>/dev/null) user-lines=$(grep -c -E "$P_USER" "$CONF" 2>/dev/null) key-lines=$(grep -v -E "$P_API" "$CONF" 2>/dev/null | grep -c -i -E -e "$P_KEYLINE" -e "$P_OPDS" -e "$P_RUN")"
if [ -n "$DOOR_SOCKET" ]; then
  echo "door    : $(systemctl is-active "$DOOR_SOCKET" 2>/dev/null) listening=$( (netstat -ltn 2>/dev/null) | grep -c ":$DOOR_PORT ")"
fi
if [ -n "$STAGE" ]; then rm -rf "$STAGE"; fi
true
