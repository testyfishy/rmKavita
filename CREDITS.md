# Credits

rmKavita stands on other people's work. This page says what is inside the program, what it talks to, and whose
projects it learned from. If you recognise your work here and would like the credit changed, please open an issue.

rmKavita is **unofficial**. It is not affiliated with, endorsed by or sponsored by Kavita, reMarkable, KOReader,
Plato, Mihon, Komga, the OPDS-PSE project, AppLoad, xovi or any other project named here. Names are used only to say
what the program works with or learned from.

rmKavita's own licence is the [PolyForm Noncommercial License 1.0.0](LICENSE). The full licence texts of everything
that is compiled into the program are in [NOTICE](NOTICE).

## 1. Code that is in the program

| Component | Licence | Used for |
|---|---|---|
| The Go standard library and runtime, (c) The Go Authors | BSD-3-Clause | everything |
| [golang.org/x/image](https://github.com/golang/image) (mirror of [go.googlesource.com/image](https://go.googlesource.com/image)), (c) The Go Authors | BSD-3-Clause | font parsing and drawing, WebP decoding, fixed-point maths |
| [golang.org/x/text](https://pkg.go.dev/golang.org/x/text), (c) The Go Authors | BSD-3-Clause | character set tables used by the font parser |
| The Go fonts "Go Regular" and "Go Bold", (c) Bigelow & Holmes Inc. | BSD-style (see NOTICE) | all text in the user interface |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys), (c) The Go Authors | BSD-3-Clause | only in builds for a PC (tests and tools), not in the tablet binary |

Test data: the decoder test images in `internal/imgproc/testdata` come from `golang.org/x/image/webp` (BSD-3-Clause);
they show the Go gopher by Renee French (CC BY 3.0). The OPDS answers in `internal/opds/testdata` were recorded from
a Kavita 0.9.1.4 test server holding synthetic content: they are data, not Kavita source code. Everything else (the
mock server, the generated comic pages, the screenshots) is synthetic.

Nothing else is vendored or copied. In particular **no source code of Kavita, KOReader, Plato, Mihon, Komga, KCC,
MuPDF, AppLoad or xovi is included.**

## 2. What it works with

- **[Kavita](https://www.kavitareader.com)** ([source](https://github.com/Kareadita/Kavita), GPL-3.0) is the
  server rmKavita is a client of. rmKavita talks to it over its OPDS and OPDS-PSE HTTP interfaces and links none of
  its code. Reading Kavita's open source, especially its OPDS controller, is how the exact behaviour of those
  interfaces (and a few traps in them) was understood.
- **[OPDS-PSE](https://github.com/anansi-project/opds-pse)**, the Page Streaming Extension of OPDS from the Anansi
  Project, is what makes reading page by page possible without downloading the file.
- **[AppLoad](https://github.com/asivery/rm-appload) (GPL-3.0), [xovi](https://github.com/asivery/xovi) (LGPL-3.0)
  and qtfb**, all by asivery. rmKavita runs as an AppLoad "external" application and draws through qtfb, the
  shared-framebuffer protocol AppLoad offers. The message layout and mode numbers are interface facts documented in
  AppLoad's `backends/qtfb-clients/cpp/common.h`; rmKavita re-implements the client side in Go, and no AppLoad code
  is included. The sleeps AppLoad's server adds after a refresh-mode message, which shaped how rmKavita refreshes,
  were found by reading its source.
- **reMarkable** makes the tablet (the Paper Pro and its Gallery 3 colour e-ink panel). "reMarkable" is their
  trademark.

## 3. Inspiration: projects that were read and learned from

The authors of these projects did the hard thinking about e-ink readers, comics and manga. Their source or
documentation was read for ideas, behaviour and facts. **No code was copied or translated from any of them**, and
their licences do not apply to rmKavita. Where something is the same idea, it was written again from scratch.

| Project | Licence | What it taught |
|---|---|---|
| **[KOReader](https://github.com/koreader/koreader)** and [koreader-base](https://github.com/koreader/koreader-base) | AGPL-3.0 | What each e-ink refresh mode is for, how often an e-ink reader should clear the screen, automatic screen density, tap-zone habits, the content of a reader footer, and that colour reMarkables look right with a 32-bit RGBA framebuffer (koreader-base moved to it on 2026-10-03; rmKavita asks for it too). The qtfb field layout was first read from KOReader's FFI declaration of the same protocol and then checked on the device. |
| **[Plato](https://github.com/baskerville/plato)** | AGPL-3.0 | How a native e-ink reader counts page turns between clearing refreshes, picks update modes per kind of page and keeps a small page cache. |
| **[Mihon](https://github.com/mihonapp/mihon)** and [subsampling-scale-image-view](https://github.com/mihonapp/subsampling-scale-image-view) | Apache-2.0 | The "automatic background" idea (the page border follows the page's own edge colour), content-based margin trimming, how spreads and tall pages are treated, and read-ahead windows for manga readers. |
| **[KCC, Kindle Comic Converter](https://github.com/ciromattia/kcc)** | ISC | The reference pipeline for manga on e-ink: margin cropping with a cap, auto-contrast, and reducing pages to the panel's grey levels ahead of time instead of at read time. This is where "Speed" mode's pre-quantised pages come from. |
| **[screentone](https://github.com/ophymx/screentone)** | MIT | A compact Go specification of the same pipeline (trim against the page's own background colour, resize, quantise with error diffusion). |
| **[Komga](https://github.com/gotson/komga)** | MIT | How a comic server's reader offers scale types and background colours. |
| **[Kavita's web reader](https://github.com/Kareadita/Kavita)** | GPL-3.0 | Fit and layout modes, per-page "wide page" information, reading direction, and how many pages a reader should keep ahead. |
| **[UltimateMangaReader](https://github.com/Rain92/UltimateMangaReader)** (Kobo) and **[komga-reader](https://github.com/Gabriel-Graf/komga-reader)** (Boox) | AGPL-3.0 | Designs of streaming manga readers on e-ink: a prefetch window, a memory guard, refresh profiles per kind of screen. |
| **[MuPDF](https://github.com/ArtifexSoftware/mupdf)** | AGPL-3.0 | The family of page-scaling filters that e-ink readers (KOReader among them) use. rmKavita has its own Catmull-Rom scaler. |
| **[rmpp-display-notes](https://github.com/MaximeRivest/rmpp-display-notes)**, [quill](https://github.com/MaximeRivest/quill) and **[oxide](https://github.com/Eeems-Org/oxide)** | MIT | Independent notes and headers on how the Paper Pro's display stack and its e-paper update modes behave. |
| **[troop-reader](https://github.com/raishack/troop-reader)** | GPL-3.0 | Another Kavita client with an e-ink mode. Its README points out that ghosting cannot be judged on an emulator; the same is true here, so rmKavita's display settings were tuned on a real panel. |
| **[awesome-reMarkable](https://github.com/reHackable/awesome-reMarkable)** | CC0-1.0 | The community map of what is possible on these tablets. |

## 4. Fonts and icons

Text in the app is set in the Go fonts (see above). Titles in scripts the Go fonts lack (Japanese, Chinese,
Korean) are drawn with the Noto Sans fonts (SIL Open Font License) that the tablet already carries; rmKavita reads
them from the device at run time and does not ship them. The icons in the app (`internal/gfx`) and the launcher icon
(`cmd/mkicon`) are drawn by the program's own code; no third-party icon set and no Kavita or reMarkable logo is used.

## 5. Thank you

To the maintainers of Kavita, AppLoad, xovi, KOReader and the other projects above, and to the people in the
reMarkable community who documented how the tablet works. If rmKavita saves you time, they are the reason.
