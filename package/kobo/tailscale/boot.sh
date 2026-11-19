#!/bin/sh
# Run by udev (at boot, and when the Wi-Fi interface appears): start Tailscale
# in the background, detached, with a clean environment, so udev does not wait
# for it or kill it.
env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin setsid /usr/local/tailscale/ts.sh boot >/dev/null 2>&1 </dev/null &
exit 0
