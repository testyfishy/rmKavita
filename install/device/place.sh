#!/bin/sh
# rmKavita installer, tablet side: put the staged app folder in place.
# The new folder is built completely in the staging directory first and then swapped in with renames; the old
# folder is kept until the new one is in place and put back if the swap fails. Does not restart anything.
#
# usage: sh -s -- APP_DIR=... STAGE=...
#   expects $STAGE/rmkavita, $STAGE/external.manifest.json, $STAGE/icon.png

APP_DIR=''
STAGE=''
for kv in "$@"; do
  case "$kv" in
    APP_DIR=*) APP_DIR=${kv#APP_DIR=};;
    STAGE=*) STAGE=${kv#STAGE=};;
  esac
done
[ -n "$APP_DIR" ] && [ -n "$STAGE" ] || { echo "place_failed: APP_DIR and STAGE are required"; exit 1; }
for f in rmkavita external.manifest.json icon.png; do
  [ -f "$STAGE/$f" ] || { echo "place_failed: $STAGE/$f is missing"; exit 1; }
done

NEW="$STAGE/app.new"
OLD="$STAGE/app.old"
rm -rf "$NEW" "$OLD"
mkdir -p "$NEW" || { echo "place_failed: cannot create $NEW"; exit 1; }
cp -f "$STAGE/rmkavita" "$NEW/rmkavita" &&
cp -f "$STAGE/external.manifest.json" "$NEW/external.manifest.json" &&
cp -f "$STAGE/icon.png" "$NEW/icon.png" &&
chmod 755 "$NEW/rmkavita" &&
chmod 644 "$NEW/external.manifest.json" "$NEW/icon.png" || { echo "place_failed: copying into $NEW"; rm -rf "$NEW"; exit 1; }

mkdir -p "$(dirname "$APP_DIR")" || { echo "place_failed: cannot create the AppLoad folder"; exit 1; }
if [ -d "$APP_DIR" ]; then
  mv "$APP_DIR" "$OLD" || { echo "place_failed: cannot move the old folder away"; exit 1; }
fi
if mv "$NEW" "$APP_DIR"; then
  rm -rf "$OLD"
  echo "placed=$APP_DIR"
else
  if [ -d "$OLD" ]; then mv "$OLD" "$APP_DIR"; fi
  echo "place_failed: the swap failed; the previous folder was put back"
  exit 1
fi
