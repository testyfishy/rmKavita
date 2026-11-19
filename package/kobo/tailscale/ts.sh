#!/bin/sh
# Tailscale on a Kobo (Nickel), userspace networking: no TUN device is needed;
# programs reach the tailnet through tailscaled's proxies on the loopback:
#   SOCKS5     127.0.0.1:1055   (rmKavita: proxy = socks5://127.0.0.1:1055)
#   HTTP proxy 127.0.0.1:1056
#
#   ts.sh boot     started by udev at boot and when Wi-Fi comes up (via boot.sh)
#   ts.sh start | stop | toggle
#   ts.sh login    start, ask for a sign-in link, print it (NickelMenu shows it)
#   ts.sh status   state, this device's tailnet address, or the pending link
#   ts.sh panel    the status page (NickelMenu > Tailscale)
#
# With .adds/tailscale/relay.conf, start also opens plain-http doors on
# 127.0.0.1 to servers on the tailnet (see relay_start).
#
# The binaries live on the user partition (.adds/tailscale/); tailscaled is
# copied to /tmp and run from there when /tmp has room, so nothing on the user
# partition is held open and USB storage keeps working. Its state is on the
# system partition (/usr/local/tailscale/state), its log in /tmp.
export PATH=/usr/sbin:/usr/bin:/sbin:/bin:$PATH
ONB=/mnt/onboard/.adds/tailscale
SD=/usr/local/tailscale/state
SOCK=/tmp/tailscaled.sock
LOG=/tmp/tailscaled.log
RUNDIR=/tmp/tailscale-bin
URLF=/tmp/tailscale-login.txt
HOSTNAME_TS=kobo

ts() { "$ONB/tailscale" --socket="$SOCK" "$@"; }
running() { pidof tailscaled >/dev/null 2>&1; }
note() { echo "$(date '+%F %T') $*" >>"$LOG"; }

# TLS needs a sane clock; a Kobo that has never synced with Kobo's servers
# starts in 2000. Set it from NTP, or from an HTTP Date header.
fix_clock() {
	y=$(date +%Y)
	[ "$y" -ge 2025 ] && return 0
	note "clock says $y: setting it"
	if command -v ntpd >/dev/null 2>&1 && timeout 8 ntpd -n -q -p pool.ntp.org >/dev/null 2>&1; then
		note "clock set by ntp: $(date)"
		return 0
	fi
	d=$(timeout 8 wget -S -q -O /dev/null http://www.msftconnecttest.com/connecttest.txt 2>&1 | sed -n 's/^ *Date: //p' | head -n 1)
	if [ -n "$d" ]; then
		# "Wed, 19 Nov 2026 03:04:05 GMT" -> busybox date -s "2026-11-19 03:04:05"
		set -- $d
		case "$3" in
		Jan) m=01 ;; Feb) m=02 ;; Mar) m=03 ;; Apr) m=04 ;; May) m=05 ;; Jun) m=06 ;;
		Jul) m=07 ;; Aug) m=08 ;; Sep) m=09 ;; Oct) m=10 ;; Nov) m=11 ;; Dec) m=12 ;;
		esac
		if [ -n "$m" ] && TZ=UTC date -u -s "$4-$m-$2 $5" >/dev/null 2>&1; then
			hwclock -w -u >/dev/null 2>&1
			note "clock set from http: $(date)"
			return 0
		fi
	fi
	note "could not set the clock"
	return 1
}

start() {
	if running; then
		relay_start
		return 0
	fi
	if [ ! -x "$ONB/tailscaled" ] && [ ! -f "$ONB/tailscaled" ]; then
		echo "Tailscale is not installed ($ONB/tailscaled is missing)"
		return 1
	fi
	ip link set lo up 2>/dev/null || ifconfig lo 127.0.0.1 up 2>/dev/null
	mkdir -p "$SD"
	fix_clock
	BIN="$ONB/tailscaled"
	avail=$(df -k /tmp 2>/dev/null | awk 'NR==2 {print $4}')
	if [ -n "$avail" ] && [ "$avail" -gt 60000 ]; then
		mkdir -p "$RUNDIR"
		if cp -f "$ONB/tailscaled" "$RUNDIR/tailscaled" 2>/dev/null; then
			chmod 755 "$RUNDIR/tailscaled"
			BIN="$RUNDIR/tailscaled"
		fi
	fi
	note "starting $BIN (/tmp free ${avail}k; / free $(df -k / 2>/dev/null | awk 'NR==2 {print $4}')k; tun=$([ -e /dev/net/tun ] && echo yes || echo no))"
	setsid "$BIN" --statedir="$SD" --socket="$SOCK" --tun=userspace-networking \
		--socks5-server=127.0.0.1:1055 --outbound-http-proxy-listen=127.0.0.1:1056 \
		>>"$LOG" 2>&1 </dev/null &
	i=0
	while [ ! -S "$SOCK" ] && [ $i -lt 20 ]; do
		sleep 1
		i=$((i + 1))
	done
	[ -S "$SOCK" ] || return 1
	relay_start
	return 0
}

# relay: plain-http doors on 127.0.0.1 to servers on the tailnet, for Nickel
# (Kobo sync with Calibre-Web, the browser), which cannot use the SOCKS proxy.
# The doors are listed in $ONB/relay.conf; no file, no relay. It is tspanel
# run as "tsrelay" (its own name, so it can be told apart from the panel).
relay_start() {
	[ -s "$ONB/relay.conf" ] || return 0
	pidof tsrelay >/dev/null 2>&1 && return 0
	mkdir -p "$RUNDIR"
	R="$RUNDIR/tsrelay"
	cp -f "$ONB/tspanel" "$R" 2>/dev/null && chmod 755 "$R" || R=""
	if [ -z "$R" ]; then
		note "relay: could not copy tspanel to $RUNDIR"
		return 1
	fi
	setsid "$R" relay -conf "$ONB/relay.conf" -proxy socks5://127.0.0.1:1055 >>"$LOG" 2>&1 </dev/null &
	note "relay started"
}

stop() {
	# no "tailscale down": that would be remembered, and the next start would
	# stay disconnected; stopping the daemon keeps the sign-in for next time
	killall -q tsrelay 2>/dev/null
	killall -q tailscaled 2>/dev/null
	i=0
	while running && [ $i -lt 10 ]; do
		sleep 1
		i=$((i + 1))
	done
	killall -q -KILL tailscaled 2>/dev/null
	note "stopped"
}

myip() { ts ip -4 2>/dev/null | head -n 1; }

status() {
	if ! running; then
		echo "Tailscale is off."
		echo "Use \"Tailscale on/off\" to start it."
		return 0
	fi
	ip4=$(myip)
	if [ -n "$ip4" ]; then
		echo "Tailscale is on and connected."
		echo "This Kobo: $ip4"
		echo
		ts status 2>/dev/null | head -n 8 | awk '{print $1, $2}'
		return 0
	fi
	if [ -s "$URLF" ]; then
		echo "Not signed in yet. Open this link on your phone or computer:"
		echo
		grep -o 'https://login.tailscale.com/[^ ]*' "$URLF" | head -n 1
		return 0
	fi
	echo "Tailscale is on but not connected."
	echo "Use \"Tailscale sign in\" to sign this Kobo in."
}

login() {
	if ! start; then
		echo "Tailscale did not start. See /tmp/tailscaled.log."
		return 1
	fi
	if [ -n "$(myip)" ]; then
		status
		return 0
	fi
	rm -f "$URLF"
	# "up" refuses to run unless every non-default setting is repeated: keep
	# Tailscale SSH as it is (the Tailscale page turns it on and off)
	SSHF=--ssh=false
	ts debug prefs 2>/dev/null | grep -q '"RunSSH": *true' && SSHF=--ssh=true
	# tailscale up waits for the sign-in in the background; the link appears in
	# its output within a few seconds
	setsid "$ONB/tailscale" --socket="$SOCK" up --hostname="$HOSTNAME_TS" --accept-dns=true $SSHF --timeout=10m \
		>"$URLF" 2>&1 </dev/null &
	i=0
	while [ $i -lt 6 ]; do
		sleep 1
		i=$((i + 1))
		grep -q 'https://login.tailscale.com/' "$URLF" 2>/dev/null && break
	done
	if grep -q 'https://login.tailscale.com/' "$URLF" 2>/dev/null; then
		echo "Open this link on your phone or computer and approve this Kobo:"
		echo
		grep -o 'https://login.tailscale.com/[^ ]*' "$URLF" | head -n 1
		echo
		echo "Then check \"Tailscale status\"."
	else
		echo "Asking Tailscale for a sign-in link..."
		echo "Check \"Tailscale status\" in a few seconds to see it."
	fi
}

# panel: start the status page (tspanel, on 127.0.0.1:1057) if it is not
# serving, from a copy in /tmp so the user partition is not held open; returns
# once it answers (NickelMenu then opens it in the browser).
panel() {
	if wget -q -T 2 -O /dev/null http://127.0.0.1:1057/ 2>/dev/null; then
		return 0
	fi
	ip link set lo up 2>/dev/null || ifconfig lo 127.0.0.1 up 2>/dev/null
	mkdir -p "$RUNDIR"
	cp -f "$ONB/tspanel" "$RUNDIR/tspanel" 2>/dev/null && chmod 755 "$RUNDIR/tspanel"
	P="$RUNDIR/tspanel"
	[ -x "$P" ] || P="$ONB/tspanel"
	setsid "$P" -hostname "$HOSTNAME_TS" >>/tmp/tspanel.log 2>&1 </dev/null &
	i=0
	while [ $i -lt 20 ]; do
		wget -q -T 1 -O /dev/null http://127.0.0.1:1057/ 2>/dev/null && return 0
		usleep 200000 2>/dev/null || sleep 1
		i=$((i + 1))
	done
	return 1
}

case "$1" in
panel) panel ;;
boot)
	# wait for the user partition, then start (no sign-in: that is the user's)
	i=0
	while ! grep -q ' /mnt/onboard ' /proc/mounts && [ $i -lt 60 ]; do
		sleep 1
		i=$((i + 1))
	done
	start >/dev/null 2>&1
	;;
start)
	if start; then echo "Tailscale is on."; status; else echo "Tailscale did not start. See /tmp/tailscaled.log."; fi
	;;
stop) stop; echo "Tailscale is off." ;;
toggle)
	if running; then stop; echo "Tailscale is off."; else
		if start; then echo "Tailscale is on."; status; else echo "Tailscale did not start. See /tmp/tailscaled.log."; fi
	fi
	;;
login) login ;;
status) status ;;
*) echo "usage: $0 boot|start|stop|toggle|login|status|panel"; exit 2 ;;
esac
