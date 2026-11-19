# Changelog

## 0.4.0

Kobo e-readers.

- **Runs on a Kobo** (tested on a Libra 2, firmware 4.38.23852): a new display backend draws on the Linux framebuffer and
  refreshes the i.MX EPDC panel with `MXCFB_SEND_UPDATE` (DU / GC16 / AUTO waveforms, the full refresh as GC16 with
  the flash), and reads the touch screen (multi-touch protocol B, with the single-touch protocol as a fallback) and
  the keys from evdev, grabbed while the app runs. 8, 16 and 32 bpp framebuffers; a landscape buffer is turned to
  portrait. The same binary detects the device; `-backend kobo` forces it.
- **Started from NickelMenu.** `launch.sh` stops Nickel and its helpers, runs the app and starts Nickel again (and
  reboots if Nickel does not come back). The release has a zip to extract onto the Kobo's drive: `.adds/rmkavita/`
  and the NickelMenu entry `.adds/nm/rmkavita`. Config, settings, positions and the log live in
  `.adds/rmkavita/`.
- **Touch calibration** on the first launch: two crosses, two taps; the transform is kept as `touch_map`.
- **Side buttons** (page-turn keys 193/194, PageUp/PageDown): one press is one screen in the reader, one page of
  rows in lists and Settings. Settings -> Side buttons: Normal / Swapped. The **power key** leaves the app.
- **Continuous reading** (Settings -> Scroll: Page by page / Continuous, per document kind): the pages of a chapter
  are one vertical strip. A side key or a tap on a side column moves it by exactly one screen (with the usual 8 %
  overlap) in one redraw, never an animation; a vertical swipe moves it by the swipe's length; past the end of the
  last page the next chapter opens. The position inside a page is remembered. On a Kobo, comics open **fit to
  width, continuous** by default; the reMarkable keeps page by page.
- `proxy = socks5://127.0.0.1:1055` (or http/https) sends every request through a proxy: how a userspace Tailscale on
  the Kobo reaches a server on the tailnet.
- Kobo defaults sized for 512 MB of RAM: `cache_mb = 48`, `mem_guard_mb = 90`, `mem_floor_mb = 45`, `keep_behind = 1`,
  a 150 MB soft heap limit. No AppLoad strip at the top of the screen on a Kobo.
- New config keys: `proxy`, `fb_rotate`, `touch_map`.
- **Sign-in:** an address typed without `http://` or `https://` is tried with http first and switched to https when
  the server asks for it (a Tailscale Serve name, for example). A port that expects https, a certificate refused for
  the name typed, an unreachable server (with a hint to check the Tailscale page when a proxy is set), a wrong
  address and a refused key each get their own message. Root certificates are built in, for devices that ship none.
- **Tailscale on the Kobo** (optional, in the Kobo zip under `.adds/tailscale/`; Tailscale's own binaries are not
  included: use the official static arm build). `ts.sh` runs `tailscaled` in userspace mode with SOCKS5 on
  `127.0.0.1:1055`, started at boot and when Wi-Fi comes up, from a copy in `/tmp` so USB storage keeps working; it
  sets a wrong clock before TLS. Installed with a `KoboRoot.tgz`. Tested with Tailscale 1.104.7.
- **tspanel**, the Tailscale page (NickelMenu > Tailscale, in the Kobo's browser): on/off, sign in with a link and a
  QR code, sign out, Tailscale SSH on/off, the devices on the tailnet, the end of the log with Save to the Kobo's
  drive. Loopback only, a per-run token for every action, exits when idle.
- **tspanel relay**: plain-http doors on `127.0.0.1` to https servers on the tailnet, for Nickel, which cannot use a
  proxy; absolute links in answers are rewritten to the door. Verified with Calibre-Web's Kobo sync
  (`api_endpoint=http://127.0.0.1:8083/kobo/<TOKEN>`) on a library of about 2,900 books, which arrive as cloud
  entries and download on tap. A second door for calibre's content server in the browser (NickelMenu > Calibre) is in
  the sample. Sync tokens are written as `<token>` in the log.
- **kavcheck**, a command-line tool for a computer: runs rmKavita's sign-in against a server and prints each step
  (login, OPDS key, visible libraries), the password read without echo and the key removed.
- The release has the Kobo binary and `rmkavita-VERSION-kobo.zip` next to the reMarkable build; all binaries,
  `tspanel` included, are built twice and checked to be the same.
- NOTICE lists the new third-party code: `golang.org/x/crypto/x509roots/fallback` (with the Mozilla root store, MPL-2.0)
  and `rsc.io/qr`.

### Documentation (after 0.3.1)

- The README is shorter; settings, hot corners, files and troubleshooting moved into
  [docs/USER-GUIDE.md](docs/USER-GUIDE.md).
- [CREDITS.md](CREDITS.md) now names every project that was read for ideas, with its licence and what it taught, and
  says plainly that no code was copied.
- The comment on the qtfb message layout now cites AppLoad's protocol header instead of KOReader's FFI declaration.
- Issue form, pull-request checklist and a link to private vulnerability reporting.
- README and CREDITS say that rmKavita was written with Claude (Claude Code).

## 0.3.1

Long libraries.

- Compact list view: one-line rows of 64 px at 229 dpi, about 27 to a screen instead of 7, no covers. Settings ->
  Lists: Auto (compact when a list has more than 40 entries), Compact, Comfortable. Search results, the home menu
  and the list of libraries keep the old rows.
- A-Z jump bar on the right edge (27 targets, # and A-Z, 7 mm wide): tap a letter to put its first entry on top;
  dragging along the bar outlines the letter under the finger and the jump happens on release. Letters with no
  entries are dim. Settings -> Alphabet bar: Auto (long lists that are in alphabetical order, with at least three
  letters), On, Off. A press on the bar is never a page swipe and never a hot-corner hold.
- The whole list is loaded in the background, one catalogue page after the other (up to 5000 entries), with
  "Loading list 3/45" in the footer. The index is the list itself: titles, links, cover links and progress marks
  only. A list that was loaded completely is shown at once the next time it is opened in the same session, after
  one request for its first page to see whether anything changed.
- Letters: the first letter or digit of the title; accents are folded, leading punctuation is skipped, digits,
  symbols and non-Latin scripts are under #. If the server sorts by a name without a leading "The", "A" or "An" the
  bar follows that.
- One update per action: a jump or a scroll repaints the list area once (rows, bar and footer together); the
  outline that follows a dragging finger is at most one small update per 300 ms; the background loading repaints
  the footer and the bar at most every 2 seconds. No refresh-mode message is sent for any of it.
- The mock server and `cmd/mockopds` can generate a library of any size (`-big 1000`) for testing.

## 0.3.0

The first public release. It collects the work of the 0.3 release candidates.

**Reading**
- Page turns are one partial update (UI waveform by default); a clearing refresh comes every N pages (20 for black
  and white, 5 for colour pages) and, by default, waits until the reader has paused for 0.6 s. It is one
  full-refresh request in the waveform that is already set: AppLoad's server sleeps one second after every mode
  message and every full-refresh request, so the older flip + request + flip back cost three.
- Three tap columns (left and right turn pages, the middle one - 40 % by default - opens and closes the bars),
  first-page and last-page buttons at the ends of the slider.
- Footer: page n/N, percent, pages left and a progress line, in the same screen update as the page. It is
  painted in the colour of the page border with ink chosen by contrast (at least 4.5:1) and registered as an
  opaque rectangle, so the colour layer can no longer cover it.
- Margin trim is on by default (10 % clamp); the page border matches the page's own edge colour by default.
- Bars are solid from the top edge of the screen and open and close in two updates with no refresh-mode
  message. `bars_clean = auto|always` adds a white pre-fill step; the default is `never`.
- Hot corners: `hot_tl`, `hot_tr`, `hot_bl`, `hot_br` and Settings -> Hot corners (none, quit, clear, home,
  settings, controls, first, last, next chapter, previous chapter, colour, direction, fit, footer).
- Colour pages have their own clearing interval (default every 5).
- The continuous reading mode of earlier builds is gone; a PDF in fit-width scrolls inside the page and then turns.

**Render**
- `render = quality | balanced | speed` (Settings -> Render). Speed: pages rendered in grey (no chroma decode, no
  colour layer), clipped (white point 245, black point 8) and reduced to `speed_levels` (2 by default; 4, 16) grey
  levels with `speed_dither` (error diffusion by default) while they are prepared, so a turn costs no pixel
  work; the border and the footer are made of the levels. Colour is greyed out in Settings while Speed is on and
  the stored choice comes back when Speed is left.
- `page_mode = animate` is accepted, for trying qtfb's fastest-settling waveform.

**System**
- Framebuffer: RGBA8888 first (`fb_format = auto`), RGB565 fallback when the system does not offer it.
- Input: the gesture tracker cannot be left unable to answer. A new press always starts a fresh gesture, a
  stationary contact with no release is classed as a resting palm after 1.2 s, a release or move that matches
  no press is ignored, and the panel's read loop never waits for the UI. Input counters go into every update
  line and the exit totals.
- Memory is a budget: read-ahead 3 and keep-behind 2 pages (`read_ahead`, `keep_behind`), a byte budget for the
  cache (`cache_mb`, 96), a guard that shrinks the window below `mem_guard_mb` (200) and stops decoding below
  `mem_floor_mb` (100), freed memory returned to the system when idle.
- Telemetry: one log line per display update with the waveform, rectangle, `ready_ms` (finger up to picture handed
  to the panel), `cpu_ms`, `net_ms`, `show_ms`, `bytes` and `sleeps` (mode messages and full-refresh requests).
  The log never contains a URL, key or password.
- `-selftest` prints p50 / p95 / max per metric over up to 8 pages per document kind, and peak memory.
- Settings -> About: version, licence, where the credits are.
- Public ssh installer (`install/`), reproducible release script (`release.sh`), `cmd/mkcbz` writes test
  chapters (`-ab`, `-speed`).

**Licence**
- PolyForm Noncommercial License 1.0.0 (source-available). Third-party notices are in `NOTICE`, credits in
  `CREDITS.md`.

## 0.2.0

Sign-in screen, Settings per document kind, account menu with Logout and Exit, Home, chrome hidden while reading,
no flash per page turn, DPI from the device, optional colour, PDF vs comic kinds, entries without a page stream
are hidden, positions per server and user, renamed to rmKavita.

## 0.1.0 (prototype)

OPDS browse, page streaming, centre-tap controls, corner holds, position memory.
