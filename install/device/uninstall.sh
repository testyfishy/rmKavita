#!/bin/sh
# rmKavita installer, tablet side: remove what the installer put on the device.
# Removes the app folder, the config (it holds the sign-in once you are signed in), the settings file, the log and
# the staging directory. The reading-positions file is KEPT unless PURGE=yes. The door units are removed by
# door.sh (they live on the root file system). Does not restart anything.
#
# usage: sh -s -- APP_DIR=... CONF=... SETTINGS=... STATE=... LOG=... STAGE=... PURGE=yes|no

APP_DIR=''
CONF=''
SETTINGS=''
STATE=''
LOG=''
STAGE=''
PURGE=no
for kv in "$@"; do
  case "$kv" in
    APP_DIR=*) APP_DIR=${kv#APP_DIR=};;
    CONF=*) CONF=${kv#CONF=};;
    SETTINGS=*) SETTINGS=${kv#SETTINGS=};;
    STATE=*) STATE=${kv#STATE=};;
    LOG=*) LOG=${kv#LOG=};;
    STAGE=*) STAGE=${kv#STAGE=};;
    PURGE=*) PURGE=${kv#PURGE=};;
  esac
done
[ -n "$APP_DIR" ] && [ -n "$CONF" ] || { echo "uninstall_failed: APP_DIR and CONF are required"; exit 1; }

rm -rf "$APP_DIR" "$APP_DIR.old"
rm -f "$CONF" "$CONF.new" "$CONF.tmp"
if [ -n "$SETTINGS" ]; then rm -f "$SETTINGS" "$SETTINGS.tmp"; fi
if [ -n "$LOG" ]; then rm -f "$LOG" "$LOG.1"; fi
if [ -n "$STAGE" ]; then rm -rf "$STAGE"; fi
if [ "$PURGE" = yes ] && [ -n "$STATE" ]; then rm -f "$STATE" "$STATE.tmp" "$STATE.bad"; fi
echo "app=$([ -e "$APP_DIR" ] && echo present || echo gone)"
echo "conf=$([ -e "$CONF" ] && echo present || echo gone)"
echo "positions=$([ -n "$STATE" ] && [ -f "$STATE" ] && echo kept || echo gone)"
echo "uninstall_done"
