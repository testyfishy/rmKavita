# rmKavita design

A short tour of how the app is built and why. It is written for someone who wants to change it or port it.

## Shape

One static Go binary (linux/arm64, about 8 MB, no cgo). AppLoad starts it; it talks to AppLoad's framebuffer server
over a unix socket and shared memory (the `qtfb` protocol), to Kavita over HTTPS/HTTP (OPDS feeds and page images),
and writes three small files.

```
cmd/rmkavita      the app (flags: -selftest, -migrate, -backend png for renders)
cmd/mkcbz         synthetic test libraries and the A/B test chapter
cmd/mockopds      the mock Kavita as a stand-alone server
cmd/mkicon        icon generator
internal/qtfb     the framebuffer protocol: handshake, format negotiation, panel (RGB565 / RGBA8888), input
internal/display  the Display interface (frames, waveforms, input events), PNG test backend
internal/ui       the app: screens, reader, settings, tap zones, prefetch, warm-up, telemetry
internal/imgproc  decode, planes, fit, resample kernels, crop, border colour, colour/grey decision
internal/gfx      canvas, fonts, widgets
internal/opds     Kavita client: feeds, page streams, sign-in, redaction
internal/store    config (line-preserving rewrite), settings, positions, log
internal/mockkavita, internal/synth   the test server and synthetic pages
```

## Display path

- **Protocol.** Messages are 24 bytes client to server, 32 server to client, little endian: INITIALIZE (key, framebuffer
  type), UPDATE (x, y, w, h), SET_REFRESH_MODE, REQUEST_FULL_REFRESH, TERMINATE; the server answers INITIALIZE with a
  shared-memory key and size. Input arrives as USER_INPUT messages.
- **Format negotiation.** `fb_format = auto` asks for RGBA8888 first. AppLoad closes the connection without a reply
  when it does not know the type; the app then reconnects once with RGB565. The negotiated format is logged at start.
  RGBA8888 matters because the panel is a colour panel: with RGB565 a grey is `R5 G6 B5` and 5/6/5 levels never
  line up, so mid greys take a tint. The app's canvas is 8-bit grey; colour pages are 565 in a separate layer that is
  expanded to 8888 on show, with neutral 565 values expanding to exact greys.
- **Waveforms.** UI(4) for page turns of black and white and colour pages alike (one partial update, no mode
  message, no full refresh); `page_mode = fast|content` selects FAST(1) or CONTENT(3) instead. CONTENT(3) +
  REQUEST_FULL_REFRESH is the clearing refresh every N turns (5 for colour pages, 20 for black and white; or UI +
  full refresh with `clear_style = ui_full`), after which the turn waveform is armed again once. Lists, settings
  and sign-in use `ui_mode`. A SET_REFRESH_MODE message is only sent when the mode changes. UFAST(0) is never
  sent. Bars and menus are painted in the turn waveform: opening them is two updates (the top bar, the bottom
  bar), closing them two, and no mode message. The top bar is painted from y = 0, so no page shows above it.
  `bars_clean` adds steps and is off by default: `auto` sends a white update of the bar rectangles first when
  the page under them is not white, `always` does the same in CONTENT (two mode messages, two seconds).
- **Render modes.** `render = quality` turns with CONTENT, `balanced` with `page_mode` (UI), `speed` with
  `page_mode` too, but every page, colour ones included, is rendered in grey: no chroma decode, no colour layer.
  In speed mode the fitted page is clipped (white point 245, black point 8), reduced to `speed_levels` (2 by
  default; 4, 16) evenly spaced grey levels and dithered (`speed_dither`: serpentine Floyd-Steinberg by default, a
  4x4 ordered mask beats against screentone, or none) at prepare time in the read-ahead worker, so the turn
  itself has no pixel work. The border colour and the footer are made of the levels (no mid-grey flats for a
  binary-class waveform), and the bars use the turn waveform with no mode message. The clearing interval is the
  black-and-white one. Speed is black and white: the Colour setting and the colour clearing interval are shown
  greyed out in Settings and are left as they are, so leaving Speed brings the choice back. Measurements of
  other projects and of the panel's own timings (see the roadmap) indicate that the update time is set by the
  waveform mode, not by the rectangle or the number of levels, so the update is still the whole page.
- **Clearing refresh.** AppLoad sleeps one second after every SET_REFRESH_MODE and every REQUEST_FULL_REFRESH.
  The default `clear_style = ui_full` is one update with the full-refresh request in the waveform already set
  (one sleep); `content_full` is a mode message, the update with the request and a re-arm (three). A clear that
  falls due at a turn runs after `clear_idle_ms` (600) without a touch and is forced at twice the interval.
  Each update line carries `sleeps=` (mode messages + requests it sent).
- **Why this matters.** AppLoad's server sleeps one second after a refresh-mode message (it waits for the panel), so a
  mode switch in front of every update costs a second. The unit test `TestWaveformEconomyWithRMAppLoadTiming` replays
  100 turns, 5 overlay toggles and 5 clears against a virtual-clock model of that behaviour: exactly two mode
  messages per clear, none per turn.
- **Telemetry.** Every display update logs one line: monotonic milliseconds, kind (turn, clear, overlay, list), mode,
  rectangle, whether a full refresh was requested, and the mode messages and full refreshes sent so far. xochitl's
  journal has one `[QTFB] Updated region` line per update, so the two logs line up.

## Page pipeline

1. **Fetch.** Kavita's OPDS-PSE page URL returns the page image as stored (JPEG/PNG/WebP) or, for PDFs, a rendered
   page. The size parameters of the URL are ignored by Kavita.
2. **Decode.** Luminance only for black and white reading (Cb/Cr are not touched); Y, Cb, Cr when colour is on. A size
   guard rejects absurd dimensions before decoding.
3. **Colour or grey?** A page counts as colour only if more than 0.2 % of its chroma samples deviate from neutral by more
   than 10 levels, so tinted scans of black and white pages (paper tone) are treated as grey and keep the FAST waveform.
4. **Crop (optional).** `crop = trim` removes empty margins found by a tolerant row/column scan, at most 10 % of the
   page, only when the gain is worth it. `trim` is the default; `keep` never touches the page.
5. **Fit.** Fit width, height or both; never upscaled more than 3x; the result is at most 16 MP; a page whose aspect
   is within 1.5 % of the box is stretched to fill it; spreads are fitted whole.
6. **Resample.** One separable pass. Catmull-Rom for comics (support widened when shrinking, so screentone is averaged,
   not aliased), Lanczos-3 for PDFs (text), `smooth` (non-negative) selectable. Measurements are in the repository's
   tests (`go test -run RealSizePages` with `RMK_BENCH=1`).
7. **Border.** The strip around a page that does not fill the screen is the page's own edge colour by default (`border = match`;
   `white` is the alternative). `match` paints it
   with the median of the page's own edge band when that edge is clearly flat and at least 1.5 % of the screen is
   visible; an ambiguous page keeps the previous border; a change of border by more than 127 levels counts as a flip
   toward the next clearing refresh.
8. **Show.** The page rectangle is composed into the canvas and sent as one partial update.

The pipeline holds at most `max_cached` fitted pages (bounded by bytes), prefetches `prefetch` pages ahead, fetches the
first page of the next chapter three pages before the end (once, cancelled when the reader leaves), and stops
prefetching when MemAvailable falls below `mem_guard_mb`; the current page is always fetched.

## Reader

- **Tap zones.** Three columns over the full height: the left and the right column turn the page (direction
  aware: right-to-left comics go forward on the left column), the middle one (`zone_middle_pct`, 40 %) opens and
  closes the bars. A downward swipe starting below 120 px also opens them (the 100 px above belong to AppLoad).
  Corner holds run the hot-corner function of the corner (defaults: top-right clears, bottom-left quits).
- **Footer.** An opaque strip (about 0.13 inch) under the page: page n/N, percent, pages left and a progress line.
  Pages are fitted into the area above it, and it is part of the page's own screen update. The strip is filled with
  the border colour and its ink is chosen by contrast (at least 4.5:1); it is registered as an opaque rectangle so
  the colour layer cannot cover it.
- **Paging.** Page by page; a page that overflows the screen (PDF fit width) scrolls inside itself first (a pure layout
  model, `ui/layout.go`) and then turns.
- **Positions.** Stored per server and user in a small JSON file, with a per-mille offset inside the page; reported to
  Kavita after a pause.

## Input

- The panel's read loop only appends to a bounded queue (moves are coalesced, a full queue sheds the oldest move
  first); a pump hands events to the UI in order. Nothing waits on the UI.
- The tracker (`ui/touch.go`) keeps one active gesture and a table of open contacts. A Down always starts a fresh
  gesture (the newest press wins) unless the first finger is actively swiping, or the new contact is a palm
  landing at the left or right edge while a fresh press is in progress. A contact that is stationary for 1.2 s
  without a release is classed as a resting palm or thumb and let go of; a corner hold and a held stepper are
  exempt while they do their job. A release or move that matches no press is ignored. Every cancel and
  classification is logged, and every update line carries `in=down/up/move/dropped g=started/completed/cancelled
  pr=<pressed> open=<contacts without a release> palm=<classified>`.

## Memory

- Fitted pages are held at 1 byte a pixel (grey) or 2 (RGB565 colour). The window is the current page, the next
  one, `keep_behind` pages behind, then the rest of `read_ahead`; `cache_mb` bounds the bytes, and when that or
  the memory guard says so the tail of that list goes first. The current page is never dropped.
- MemAvailable below `mem_guard_mb` shrinks the window to the next page and one behind; below `mem_floor_mb` no
  decode buffer is allocated at all and the page on screen shows the error state with Retry.
- A page over 4 MP decodes alone (two tokens), small pages two at a time; the resize scratch image comes from a pool;
  after 1.5 s of quiet the freed memory is returned to the system.

## Security and privacy

- The password lives in a rune buffer wiped when the request is built; only server, user name and the OPDS key are
  stored (`rmkavita.conf`, mode 600, rewritten atomically, line-preserving).
- Every log line and every error passes a redactor that replaces the key, the password and any 32+ character token.
- No telemetry leaves the device; the only network peer is the Kavita server the user typed.

## Testing

- A mock Kavita serves synthetic comics, PDFs, WebP, spreads, black pages and OPDS feeds shaped like a real
  server's; UI tests drive the real app on a fake display and check frames, tags and request counts.
- A fake AppLoad peer with a virtual clock models the server's one-second sleeps and checks message sequences.
- Image tests use generated fixtures with known properties (screentone coverage, ramps, borders).
- The PNG backend renders every screen to files for review.

## Open items

See the roadmap. The most likely next steps are decided by on-device numbers: scaled JPEG decode for large spreads
(only if decode plus fit exceeds roughly 700 ms there), and whether the one-second AppLoad sleeps can be avoided with
a different clearing sequence.
