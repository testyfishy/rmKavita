# Tailscale on a Kobo (optional)

This kit puts [Tailscale](https://tailscale.com) on a Kobo, so rmKavita (and, through a small relay, Nickel's own
Kobo sync and browser) can reach servers on your tailnet from any Wi-Fi network. It is optional: rmKavita does not
need it on a home network where it can reach Kavita directly.

Tested on: **Kobo Libra 2, firmware 4.38.23852**, with the official Tailscale static arm build 1.104.7. Other
firmware and other Kobos are untested.

How it runs: `tailscaled` in **userspace mode** (no TUN device, nothing changed in the Kobo's networking). Programs
reach the tailnet through its proxies on the loopback:

- SOCKS5 on `127.0.0.1:1055` (rmKavita uses this),
- an HTTP proxy on `127.0.0.1:1056`.

It starts at boot and when Wi-Fi comes up. The daemon runs from a copy in `/tmp`, so USB storage keeps working while
it runs. Its state (the sign-in) is on the system partition, `/usr/local/tailscale/state`.

## What is in `.adds/tailscale/`

| File | What it is |
| --- | --- |
| `tspanel` | the Tailscale page (NickelMenu > Tailscale) and the relay; built from `cmd/tspanel` |
| `KoboRoot.tgz` | the system-partition part: `ts.sh` and `boot.sh` in `/usr/local/tailscale/`, the udev rule in `/etc/udev/rules.d/` |
| `ts.sh`, `boot.sh`, `98-tailscale.rules` | the same files, to read (the copies that run are the ones from `KoboRoot.tgz`) |
| `nm-tailscale`, `nm-calibre` | NickelMenu entries, not active until you copy them into `.adds/nm/` |
| `relay.conf.sample` | the relay's doors (see below) |

**Tailscale itself is not included.** Download the official static build for **arm** (`tailscale_VERSION_arm.tgz`)
from [pkgs.tailscale.com/stable/#static](https://pkgs.tailscale.com/stable/#static) and take `tailscale` and
`tailscaled` out of it.

## Install

1. Extract the rmKavita Kobo zip onto the Kobo's drive (it adds `.adds/tailscale/`).
2. Put `tailscale` and `tailscaled` from the official arm build into `.adds/tailscale/`.
3. Copy `.adds/tailscale/KoboRoot.tgz` to `.kobo/KoboRoot.tgz` on the Kobo's drive.
4. Copy `.adds/tailscale/nm-tailscale` to `.adds/nm/tailscale` (and `nm-calibre` to `.adds/nm/calibre` if you use
   calibre's content server through the relay).
5. Eject the Kobo. It installs `KoboRoot.tgz` and reboots.
6. NickelMenu > **Tailscale**, then **Sign in**: scan the QR code with your phone (or open the link) and approve
   the Kobo. It joins the tailnet as `kobo`.

To update Tailscale, replace the two binaries in `.adds/tailscale/` and turn Tailscale off and on in the Tailscale
page.

## The Tailscale page

NickelMenu > **Tailscale** opens a page in the Kobo's browser, served by `tspanel` on `127.0.0.1:1057` (loopback
only; each action needs a token of that run; it exits after a few idle minutes):

- whether Tailscale is on and connected, this Kobo's tailnet name and address, your other devices;
- **On / Off**, **Sign in** (link and QR code), **Sign out**;
- **Tailscale SSH on / off**, for remote access while debugging (off unless you turn it on);
- **Log**: the end of `tailscaled`'s log, and **Save log**, which copies it to `.adds/tailscale/tailscaled.log` so you
  can read it over USB.

## rmKavita over Tailscale

Add to `.adds/rmkavita/rmkavita.conf`:

```
proxy = socks5://127.0.0.1:1055
```

and sign in with the server's Tailscale name (`myserver.tailnet-name.ts.net`) or its 100.x address. A server name
behind Tailscale Serve uses https; rmKavita switches from http to https on its own when the server asks for it.

## The relay: Kobo sync and calibre for Nickel

Nickel cannot use a SOCKS proxy. `tspanel relay` opens plain-http **doors on 127.0.0.1** that carry requests over
the tailnet; absolute links in the answers (redirects, JSON, HTML, XML) are rewritten to point back at the door, so
downloads and covers keep coming through it. Book files and images stream through untouched.

Copy `relay.conf.sample` to `.adds/tailscale/relay.conf` and list one door per line, "listen-on-the-Kobo
server-on-the-tailnet":

```
127.0.0.1:8083  https://myserver.tailnet-name.ts.net:8446   # Calibre-Web (Kobo sync)
127.0.0.1:8084  https://myserver.tailnet-name.ts.net:8445   # calibre's content server
```

The relay starts with Tailscale when `relay.conf` exists; after a change, turn Tailscale off and on.

**Calibre-Web Kobo sync.** In `.kobo/Kobo/Kobo eReader.conf`, section `[OneStoreServices]`, set

```
api_endpoint=http://127.0.0.1:8083/kobo/TOKEN
```

with the sync token from your Calibre-Web profile. Tested with a Calibre-Web library of about 2,900 books: the
books show up in Nickel's library as cloud entries and download when you tap them. The sync token is a secret: the
relay writes `/kobo/<token>` in its log instead of the token.

**calibre's content server.** NickelMenu > **Calibre** (from `nm-calibre`) starts Tailscale if needed and opens
`http://127.0.0.1:8084/mobile` in the Kobo's browser.

## If something goes wrong

- `/tmp/tailscaled.log` has the daemon's and the relay's messages: the Tailscale page shows it and **Save log**
  copies it to the drive.
- TLS needs a correct clock. A Kobo that has never synced with Kobo's servers may think it is the year 2000; `ts.sh`
  sets the clock from NTP or an HTTP date before it starts.
- The Kobo joins only 2.4 / 5 GHz WPA2-Personal networks (no Enterprise, no 6 GHz).

## Uninstall

Delete `.adds/tailscale/`, `.adds/nm/tailscale` and `.adds/nm/calibre`. The udev rule and `/usr/local/tailscale/`
stay on the system partition but do nothing without the binaries; a factory reset removes them.
