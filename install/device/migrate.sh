#!/bin/sh
# rmKavita installer, tablet side: run the app's one-shot config/positions migration.
#   rmkavita -migrate -conf C -state S -old-conf OC -old-state OS
# Exit 0 + at least one "migrate:" line = success. Output is redacted here on the device (anything key-shaped
# becomes <hidden>) and only `migrate:` lines plus a few other lines come back. The config is never printed.
#
# usage: sh -s -- APP_DIR=... CONF=... STATE=... OLD_CONF=... OLD_STATE=...

APP_DIR=''
CONF=''
STATE=''
OLD_CONF=''
OLD_STATE=''
for kv in "$@"; do
  case "$kv" in
    APP_DIR=*) APP_DIR=${kv#APP_DIR=};;
    CONF=*) CONF=${kv#CONF=};;
    STATE=*) STATE=${kv#STATE=};;
    OLD_CONF=*) OLD_CONF=${kv#OLD_CONF=};;
    OLD_STATE=*) OLD_STATE=${kv#OLD_STATE=};;
  esac
done

# a config that holds the sign-in must not be readable by others
if [ -f "$CONF" ]; then chmod 600 "$CONF" 2>/dev/null; fi

out=$("$APP_DIR/rmkavita" -migrate -conf "$CONF" -state "$STATE" -old-conf "$OLD_CONF" -old-state "$OLD_STATE" 2>&1 < /dev/null)
rc=$?
echo "migrate_rc=$rc"
safe=$(printf '%s\n' "$out" | sed -E 's#/api/opds/[A-Za-z0-9_-]+#/api/opds/<hidden>#g; s#[A-Za-z0-9_-]{32,}#<hidden>#g' | cut -c1-200)
printf '%s\n' "$safe" | grep '^migrate:' | head -n 4 | sed 's/^/m| /'
printf '%s\n' "$safe" | grep -v -e '^migrate:' -e '^$' | head -n 6 | sed 's/^/o| /'
echo "migrate_end"
exit "$rc"
