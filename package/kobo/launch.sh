#!/bin/sh
# rmKavita launcher for Kobo e-readers, started by NickelMenu:
#
#   menu_item:main:rmKavita:cmd_spawn:quiet:exec /mnt/onboard/.adds/rmkavita/launch.sh
#
# Nickel (Kobo's reader) and its helpers are stopped so the app owns the screen
# and the input, the app runs, and Nickel is started again the way it was
# (this script inherits Nickel's environment from NickelMenu). If Nickel does
# not come back, the device reboots: nothing is lost either way.
DIR=/mnt/onboard/.adds/rmkavita
LOG="$DIR/launcher.log"

# keep the launcher log small
if [ -f "$LOG" ] && [ "$(wc -c <"$LOG")" -gt 65536 ]; then
	mv -f "$LOG" "$LOG.1"
fi
exec >>"$LOG" 2>&1
echo "=== $(date '+%F %T') launch (PRODUCT=$PRODUCT PLATFORM=$PLATFORM)"

if pidof rmkavita >/dev/null 2>&1; then
	echo "already running"
	exit 0
fi

# Nickel owns Wi-Fi: it connects after it starts (and after a restart, such as
# leaving rmKavita). Stopping it while it is still connecting leaves the Kobo
# offline, so wait (up to 20 s) for an address when Wi-Fi is on.
net_ip() { ip -4 -o addr show 2>/dev/null | awk '$2 != "lo" {print $2, $4; exit}'; }
if [ -n "$(ls /sys/class/net 2>/dev/null | grep -v '^lo$')" ]; then
	i=0
	while [ -z "$(net_ip)" ] && [ $i -lt 20 ]; do
		sleep 1
		i=$((i + 1))
	done
fi
echo "network: $(net_ip || true) ${i:+(waited ${i}s)}"
if pidof tailscaled >/dev/null 2>&1; then
	echo "tailscale: running, $(/mnt/onboard/.adds/tailscale/tailscale --socket=/tmp/tailscaled.sock status 2>&1 | head -n 1)"
else
	echo "tailscale: not running"
fi

# Nickel writes its database and settings on the way out
sync
killall -q -TERM nickel hindenburg sickel fickel strickel fontickel adobehost foxitpdf iink fmon nanoclock.lua 2>/dev/null
i=0
while pidof nickel >/dev/null 2>&1 && [ $i -lt 50 ]; do
	usleep 100000 2>/dev/null || sleep 1
	i=$((i + 1))
done
if pidof nickel >/dev/null 2>&1; then
	echo "nickel did not stop; killing it"
	killall -q -KILL nickel 2>/dev/null
	sleep 1
fi

# run from the user partition; the binary needs nothing but itself
cd "$DIR" || exit 1
"$DIR/rmkavita" -backend kobo
echo "rmkavita exited with $?"

# --- bring Nickel back ------------------------------------------------------------------
sync
export LD_LIBRARY_PATH="/usr/local/Kobo"
[ -e /tmp/nickel-hardware-status ] || mkfifo /tmp/nickel-hardware-status
cd /
if [ -x /usr/local/Kobo/hindenburg ]; then
	(/usr/local/Kobo/hindenburg >/dev/null 2>&1 &)
fi
(LIBC_FATAL_STDERR_=1 /usr/local/Kobo/nickel -platform kobo -skipFontLoad >/dev/null 2>&1 &)
if [ -x /usr/local/Kobo/sickel ]; then
	(/usr/local/Kobo/sickel -platform kobo:noscreen >/dev/null 2>&1 &)
fi
if [ -x /usr/local/Kobo/fickel ]; then
	(/usr/local/Kobo/fickel >/dev/null 2>&1 &)
fi
# Nickel rescans the onboard storage when the "USB" state comes back
(udevadm trigger >/dev/null 2>&1 &)

i=0
while ! pidof nickel >/dev/null 2>&1 && [ $i -lt 20 ]; do
	sleep 1
	i=$((i + 1))
done
if pidof nickel >/dev/null 2>&1; then
	echo "nickel restarted"
	exit 0
fi
echo "nickel did not come back: rebooting"
sync
reboot
