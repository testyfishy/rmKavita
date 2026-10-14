#!/bin/sh
# rmKavita installer, tablet side: install or remove the OPTIONAL local "door" (a systemd socket on
# DOOR_LISTEN that forwards each connection to the Kavita server through `tailscale nc`).
#
# This script is uploaded to the staging directory and started DETACHED (setsid): making the root file system
# writable can drop the SSH session for a moment. The host polls its log for DONE / FAILED.
#
# usage: sh door.sh ACTION=install|remove STAGE=... UNIT_DIR=... SOCKET=... SERVICE=...
#                   RW_CMD=... RO_CMD=... BIND_SRC=...
#   install: expects $STAGE/$SOCKET and $STAGE/$SERVICE (filled in on the host); copies them only when they
#            differ from the installed ones (no remount when identical), enables the socket and starts it.
#   remove : stops and removes the units.
#   RW_CMD / RO_CMD: commands that make the root file system writable and put it back (the Vellum helpers on
#            the standard device layout); empty or missing = plain `mount -o remount,rw|ro /`.
#   BIND_SRC: if /etc/dropbear was a bind mount before and is gone after the remount, bind this directory back
#            (only when you keep your SSH host keys somewhere else and bind them there); empty = do nothing.

ACTION=''
STAGE=''
UNIT_DIR=/etc/systemd/system
SOCKET=rmkavita-door.socket
SERVICE=rmkavita-door@.service
RW_CMD=''
RO_CMD=''
BIND_SRC=''
for kv in "$@"; do
  case "$kv" in
    ACTION=*) ACTION=${kv#ACTION=};;
    STAGE=*) STAGE=${kv#STAGE=};;
    UNIT_DIR=*) UNIT_DIR=${kv#UNIT_DIR=};;
    SOCKET=*) SOCKET=${kv#SOCKET=};;
    SERVICE=*) SERVICE=${kv#SERVICE=};;
    RW_CMD=*) RW_CMD=${kv#RW_CMD=};;
    RO_CMD=*) RO_CMD=${kv#RO_CMD=};;
    BIND_SRC=*) BIND_SRC=${kv#BIND_SRC=};;
  esac
done
[ -n "$STAGE" ] || exit 1
exec >> "$STAGE/door.log" 2>&1
echo "== $(date -u) door $ACTION"

rw_on() {
  if [ -n "$RW_CMD" ] && [ -x "$RW_CMD" ]; then "$RW_CMD"; else mount -o remount,rw /; fi
}
rw_off() {
  if [ -n "$RO_CMD" ] && [ -x "$RO_CMD" ]; then "$RO_CMD"; else mount -o remount,ro /; fi
}
BOUND=$(grep -c ' /etc/dropbear ' /proc/mounts)
rebind() {
  if [ "${BOUND:-0}" != 0 ] && [ -n "$BIND_SRC" ] && [ -d "$BIND_SRC" ]; then
    grep -q ' /etc/dropbear ' /proc/mounts || { mount --bind "$BIND_SRC" /etc/dropbear && echo 'RESTORED /etc/dropbear'; }
  fi
}

case "$ACTION" in
install)
  need=0
  cmp -s "$STAGE/$SOCKET" "$UNIT_DIR/$SOCKET" || need=1
  cmp -s "$STAGE/$SERVICE" "$UNIT_DIR/$SERVICE" || need=1
  [ -e "$UNIT_DIR/sockets.target.wants/$SOCKET" ] || need=1
  if [ "$need" = 1 ]; then
    rw_on || { echo 'FAILED: could not make the root file system writable'; exit 1; }
    rc=0
    cp "$STAGE/$SOCKET" "$UNIT_DIR/$SOCKET" || rc=1
    cp "$STAGE/$SERVICE" "$UNIT_DIR/$SERVICE" || rc=1
    mkdir -p "$UNIT_DIR/sockets.target.wants" || rc=1
    ln -sf "$UNIT_DIR/$SOCKET" "$UNIT_DIR/sockets.target.wants/$SOCKET" || rc=1
    ls -la "$UNIT_DIR/$SOCKET" "$UNIT_DIR/$SERVICE"
    rw_off
    rebind
    [ "$rc" = 0 ] || { echo 'FAILED: copying the units'; exit 1; }
  else
    echo 'units already installed and identical: no remount'
  fi
  systemctl daemon-reload
  systemctl start "$SOCKET"
  echo "socket: $(systemctl is-active "$SOCKET")"
  echo DONE
  ;;
remove)
  systemctl stop "$SOCKET" 2>/dev/null
  systemctl stop "rmkavita-door@*.service" 2>/dev/null
  if [ -e "$UNIT_DIR/$SOCKET" ] || [ -e "$UNIT_DIR/$SERVICE" ] || [ -L "$UNIT_DIR/sockets.target.wants/$SOCKET" ]; then
    rw_on || { echo 'FAILED: could not make the root file system writable'; exit 1; }
    rm -f "$UNIT_DIR/sockets.target.wants/$SOCKET" "$UNIT_DIR/$SOCKET" "$UNIT_DIR/$SERVICE"
    rw_off
    rebind
  else
    echo 'no door units present: no remount'
  fi
  systemctl daemon-reload
  systemctl reset-failed 'rmkavita-door*' 2>/dev/null
  echo "socket: $(systemctl is-active "$SOCKET" 2>/dev/null)"
  echo DONE
  ;;
*)
  echo "FAILED: unknown ACTION '$ACTION'"
  exit 1
  ;;
esac
