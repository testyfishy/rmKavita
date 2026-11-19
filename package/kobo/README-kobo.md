# rmKavita on a Kobo

rmKavita streams manga and comics from a Kavita server, a page at a time, and
keeps nothing on the device but a few small state files. On a Kobo it runs as a
full-screen app started from NickelMenu; Nickel (Kobo's own reader) is stopped
while it runs and comes back when you leave.

Tested on: **Kobo Libra 2** (firmware 4.38.23852). Other Kobos with an i.MX panel
(Clara HD / 2E / BW, Libra H2O, Forma) should work but are untested. The Sage,
Elipsa and Elipsa 2E use a different display driver and do **not** work.

## Install

1. Install [NickelMenu](https://pgaskin.net/NickelMenu/) if you do not have it.
2. Connect the Kobo to the computer and extract `rmkavita-VERSION-kobo.zip`
   into the root of the Kobo's drive. It adds `.adds/rmkavita/` and the
   NickelMenu entry `.adds/nm/rmkavita` (your own NickelMenu config is not
   touched).
3. Eject the Kobo. **rmKavita** is now in the NickelMenu menu.

To update, extract the new zip over the old one: your sign-in, settings and
reading positions (`rmkavita.conf`, `settings.json`, `state.json`) stay.

## First launch

1. Two crosses appear one after the other: tap the centre of each. This
   calibrates the touch screen once (it is saved as `touch_map` in
   `rmkavita.conf`; delete that line to do it again).
2. Sign in with the Kavita server address, your user name and password. The
   password is used once and never stored. Without `http://` or `https://`,
   rmKavita tries http and switches to https by itself when the server asks
   for it; a wrong port, an unreachable server or a refused secure connection
   each get their own message with what to try.

Turn Wi-Fi on in the Kobo before starting rmKavita (Nickel's Wi-Fi stays up
while the app runs).

## Reading

- Comics open **fit to width** in a **continuous** strip.
- **Side buttons:** one press moves exactly one screen (with a small overlap so
  nothing is cut off) in one redraw. Settings -> Side buttons -> Swapped if the
  direction is the wrong way round for how you hold the Kobo.
- **Tap** the left or right third: one screen back or forward. **Swipe** up or
  down: the strip moves by the length of the swipe. **Tap the middle:** the
  bars (page slider, settings, exit).
- Settings -> Scroll -> Page by page brings back one-page-at-a-time reading.
- **Power button:** leave rmKavita (back to Nickel). Holding the bottom-left
  corner does the same.

## Tailscale

The zip also has an optional Tailscale kit in `.adds/tailscale/`, for reaching
Kavita on your tailnet from any network: Tailscale in userspace mode, a status
page (NickelMenu > Tailscale: on/off, sign in with a QR code, SSH on/off, the
log), and a relay that lets Nickel's own Kobo sync (Calibre-Web) and browser
reach servers on the tailnet. Tailscale's own binaries are not included. Setup
and details: `.adds/tailscale/README-tailscale.md`.

With it, add `proxy = socks5://127.0.0.1:1055` to `rmkavita.conf` and sign in
with the server's Tailscale name or 100.x address.

The Kobo joins only 2.4 / 5 GHz WPA2-Personal networks: no WPA2/WPA3-Enterprise
(user name and password) and no 6 GHz. At work or school, use a phone or
laptop hotspot.

## If something goes wrong

- `.adds/rmkavita/rmkavita.log` (the app) and `launcher.log` (stopping and
  restarting Nickel) are on the Kobo's drive: plug it in to read them.
- If Nickel does not come back after rmKavita closes, the launcher reboots the
  Kobo. A hung screen can always be cleared by holding the power button for
  about 10 seconds (a hard reboot; nothing is lost).
- Uninstall: delete `.adds/rmkavita/` and `.adds/nm/rmkavita`.
