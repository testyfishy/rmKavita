# Credits

rmKavita is **unofficial**. It is not affiliated with, endorsed by or sponsored by Kavita, reMarkable, KOReader,
Plato, Mihon, the OPDS-PSE project, AppLoad, xovi or any of the others named here. Their names are used only to
say what the program works with or learned from.

The licence of rmKavita is the [PolyForm Noncommercial License 1.0.0](LICENSE). The full texts of the licences of
everything that is compiled into the program are in [NOTICE](NOTICE).

## Third-party code that is in the program

| Component | Licence | What for |
|---|---|---|
| The Go standard library and runtime, (c) The Go Authors | BSD-3-Clause | everything |
| [golang.org/x/image](https://pkg.go.dev/golang.org/x/image), (c) The Go Authors | BSD-3-Clause | font parsing and rendering, WebP decoding, fixed-point maths |
| [golang.org/x/text](https://pkg.go.dev/golang.org/x/text), (c) The Go Authors | BSD-3-Clause | character set tables used by the font parser |
| The Go fonts "Go Regular" and "Go Bold", (c) 2016 Bigelow & Holmes Inc. | BSD-style (see NOTICE) | the user interface text |
| `golang.org/x/sys`, (c) The Go Authors | BSD-3-Clause | only in builds for x86-64 (tests and tools), not in the tablet binary |

Test data: the decoder test images under `internal/imgproc/testdata` come from `golang.org/x/image/webp` (BSD-3-Clause)
and show the Go gopher by Renee French (CC BY 3.0). The OPDS answers under `internal/opds/testdata` were recorded
from a Kavita test server with synthetic content; they are data, not Kavita source code. Everything else (mock
server, generated comic pages) is synthetic.

Nothing else is vendored or copied. In particular **no source code of Kavita, KOReader, Plato, Mihon, AppLoad or
xovi is included**.

## What it works with

- **[Kavita](https://www.kavitareader.com)** (GPL-3.0) is the server rmKavita is a client of. rmKavita talks to it
  over its documented OPDS and OPDS-PSE (page streaming) HTTP interfaces and links none of its code.
- **The OPDS-PSE page streaming extension** (specified by the
  [anansi-project](https://github.com/anansi-project/opds-pse)) is what makes page-by-page reading possible.
- **AppLoad, xovi and qtfb** by asivery: rmKavita runs as an AppLoad "external" application and draws through the
  qtfb shared-framebuffer protocol. The protocol's message and mode constants were re-implemented from its
  documented behaviour; no AppLoad code is included.
- **reMarkable** makes the hardware (the Paper Pro and its Gallery 3 colour e-ink panel); "reMarkable" is their
  trademark.

## Inspiration only (ideas, no code)

These projects taught the author how things are done; their code is not used here and their licences do not
apply to rmKavita:

- **KOReader** (AGPL-3.0): the meaning of the panel's refresh modes and how often to do a clearing refresh,
  automatic screen density, and the tap-zone habits of an e-ink reader.
- **Plato** (AGPL-3.0): how an e-ink reader chooses update modes and dithering.
- **Mihon / Tachiyomi** (Apache-2.0): the idea of an automatic page background (the border colour that follows
  the page's edge).
- **KCC (Kindle Comic Converter)**: pre-quantising pages to the panel's levels so that no work is left at read time.

If you recognise your work here and would like the credit changed, please open an issue.
