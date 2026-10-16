# Roadmap

Goal: **be excellent at streaming page images in series from a Kavita server, so reading manga and comics on an
e-ink panel is comfortable.** Everything is judged by that: does it make streaming images in series more comfortable
or more efficient? If not, it is out.

## Principles

- Stream, never store: no page images on disk, bounded RAM, only small state files.
- One page turn is one partial update. A clearing refresh comes every N turns (default 20), when the reader pauses.
- Tight and boring: one static binary (under 9 MB), no background work while idle, no telemetry, no secrets in logs.
- Auto where KOReader is auto: DPI from the device, refresh policy modelled on KOReader's.
- The user types credentials on the device; the installer never handles them.
- Reading comes first: controls are hidden while reading and revealed by a gesture.

## Non-goals

- EPUB or any text / reflow rendering. Entries without a page-image stream are not listed.
- Downloads or offline copies.
- Library management, scanning, metadata editing, scrobbling.
- Acting as a general ebook reader (KOReader does that well).
- A continuous (scrolling) reading mode: e-ink is not built for it. It existed in early builds and was removed.

## Versions

| Version | Status | Scope |
|---|---|---|
| 0.1 | prototype | OPDS browse, page streaming, centre-tap controls, corner holds, position memory |
| 0.2 | done | Sign-in screen, Settings per document kind, account menu, Home, chrome hidden while reading, no flash per page turn, DPI auto, optional colour, PDF vs comic kinds |
| 0.3.0 | **released** | Self-healing input tracker with counters; three tap columns, first / last page buttons, footer in the border colour; solid bars that open and close in two updates and no mode message; hot corners; margin trim and page-border colour match; RGBA8888 framebuffer with RGB565 fallback; one waveform per activity; one-sleep clearing refresh that waits for a pause; Render setting (quality / balanced / speed) with pre-quantised grey pages and Colour greyed out in Speed; read-ahead / keep-behind with a byte budget, a memory guard and floor; per-update telemetry with latencies; self-test with p50 / p95 timings; generic ssh installer, reproducible release script |
| 0.3.1 | **released** | Long libraries: compact rows, the A-Z jump bar with a complete index loaded in the background, the session cache with a staleness check |
| later | ideas | see backlog |

## Backlog

| Idea | Notes |
|---|---|
| JPEG DCT-domain scaled decode for very large sources | Design only (docs/DESIGN.md). Only if on-device timings demand it: decode plus fit of a 10 MP page is 1.3-1.9 s on the tablet's CPU when a page is not ready |
| Double-page spread: rotate / split | Spreads are shown whole today |
| Per-series reading direction and profiles | Today direction is global per document kind |
| Result of the waveform A/B (UI / Fast / Animate) | `page_mode = animate` is selectable for testing; it becomes a default only if it is faster and clean on real panels |
| Text-friendly enhancement for PDF pages | Contrast / sharpening for text pages |
| PDF annotations that sync back to the source | Stretch goal; only the `Annotator` seam exists |
| Bilinear chroma upsampling | Only visible on saturated edges |
| Reading history list on Home | The Continue row shows one chapter |
| Zoom / guided view | |

## Decisions log (append only)

| Decision | Why |
|---|---|
| Credentials are typed on a Sign-in screen; only server, user name and the OPDS key are stored | No secret ever passes through an installer |
| No EPUB support | Hyperfocus on streaming images in series |
| RGBA8888 framebuffer is requested first and RGB565 is the fallback | True greys (no 565 tint); AppLoad 0.5.0 offers it |
| Page border matches the page; margin trim is on (10 % clamp, never invents a margin) | Liked in daily use; both can be switched off |
| Continuous mode removed | E ink is not built for continuous scrolling |
| The footer is an opaque rectangle painted in the border colour, ink chosen by contrast | The colour layer would otherwise hide it |
| Bars are two updates in the page waveform; the white pre-fill is an opt-in step (`bars_clean`) | Every extra update is a visible step on e-ink |
| Render setting: quality / balanced / speed; Speed is black and white and greys out Colour without changing it | Speed gives up grey levels and colour for page-turn time; the colour choice must come back |
| Clearing refresh is `ui_full`, deferred to a pause | AppLoad sleeps one second after every mode message and full-refresh request: a flipped clear is three of them |
| Tap zones: three columns (left and right turn, the middle one opens the bars) | The exact midline of two halves is hard to hit; a column is easy |
| Page turns default to UI | Fastest in daily use; Fast and Content stay selectable |
| The newest press is always the gesture | A lost release or a resting palm must never make the app ignore the next touch |
| Memory is a budget, not a count | The device has 2 GB; the thing to protect is xochitl, so the guard watches MemAvailable |
| Licence: PolyForm Noncommercial 1.0.0 (source-available); commercial use needs a separate licence | The author keeps the right to offer the program commercially |
| Long lists are compact above 40 entries; the A-Z bar needs an alphabetical list | A library of 900 series is 130 screens of 7 rows; a bar on an unordered list ("Recently Added") would lie |
| A jump is one list-area update; the bar follows a drag with throttled small updates | Every extra update is a visible step on e-ink |
| The index is the list itself, loaded in full in the background (cap 5000) | Letters must be real for the whole list; titles and links are a few hundred KB |
