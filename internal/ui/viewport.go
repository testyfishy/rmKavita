package ui

import (
	"fmt"
	"image"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/imgproc"
	"rmkavita/internal/opds"
)

// piece is a rectangle of a fitted page and where it lands on the screen.
type piece struct {
	f      *imgproc.Fitted
	sx, sy int
	w, h   int
	dx, dy int
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// errPage returns the page whose load failed among the pages the viewport
// needs, or -1.
func (a *App) errPage(r *reader) int {
	if s := r.slots[r.page]; s != nil && s.state == 3 {
		return r.page
	}
	return -1
}

// viewportPieces lays out what the screen shows, or nil while pages are
// missing. Every offset is a whole pixel: a page always lands on exact pixel
// boundaries.
func (a *App) viewportPieces(r *reader) []piece {
	f := r.ready(r.page)
	if f == nil {
		return nil
	}
	p := piece{f: f, w: min(f.W, a.W), h: min(f.H, a.vh())}
	ext, view, vertical := a.scrollGeom(f)
	switch {
	case ext > view && vertical:
		p.sy = clampInt(r.off, 0, f.H-a.vh())
		if f.W > a.W {
			p.sx = (f.W - a.W) / 2
		}
	case ext > view:
		maxOff := f.W - a.W
		off := clampInt(r.off, 0, maxOff)
		if a.rtlOf(r) {
			p.sx = maxOff - off // reading starts at the right edge
		} else {
			p.sx = off
		}
	}
	p.dx, p.dy = (a.W-p.w)/2, (a.vh()-p.h)/2
	return []piece{p}
}

// paint copies the pieces into the gray canvas, or (colour) into the colour
// layer, converting gray pages with the neutral-gray table.
func (a *App) paint(ps []piece, colour bool) {
	if !colour {
		c := a.cv.Gray
		for _, p := range ps {
			for y := 0; y < p.h; y++ {
				src := p.f.Gray[(p.sy+y)*p.f.W+p.sx : (p.sy+y)*p.f.W+p.sx+p.w]
				copy(c.Pix[(p.dy+y)*c.Stride+p.dx:], src)
			}
		}
		return
	}
	for _, p := range ps {
		for y := 0; y < p.h; y++ {
			d := a.col[(p.dy+y)*a.W+p.dx : (p.dy+y)*a.W+p.dx+p.w]
			if p.f.Col != nil {
				copy(d, p.f.Col[(p.sy+y)*p.f.W+p.sx:(p.sy+y)*p.f.W+p.sx+p.w])
				continue
			}
			src := p.f.Gray[(p.sy+y)*p.f.W+p.sx : (p.sy+y)*p.f.W+p.sx+p.w]
			for i, g := range src {
				d[i] = imgproc.Gray565[g]
			}
		}
	}
}

func (a *App) ensureCol(bg uint8) {
	if a.col == nil {
		a.col = make([]uint16, a.W*a.H)
	}
	v := imgproc.Gray565[bg]
	for i := range a.col {
		a.col[i] = v
	}
}

// borderFor is the colour of the margin around the page (Border: Match page).
// The level was measured when the page was prepared; an ambiguous page keeps
// the previous colour, a chapter starts white, and a black<->white flip counts
// as a turn toward the next clearing refresh (it ghosts more than a plain turn).
func (a *App) borderFor(r *reader, ps []piece) uint8 {
	if a.Cfg.Border != "match" || len(ps) != 1 || r.doc == opds.DocPDF {
		return paperGray
	}
	if f := ps[0].f; r.borderPage != r.page {
		if f.BorderOK {
			prev := r.border
			r.border = f.Border
			if abs(int(prev)-int(r.border)) >= 128 {
				a.sinceFull++
			}
		}
		r.borderPage = r.page
	}
	return r.border
}

// composeReader paints the current page (or its loading/error state) and the
// overlay. colour reports a colour page on screen; ready that a page is shown.
func (a *App) composeReader() (colour, ready bool) {
	r := a.rd
	c := a.cv
	a.hits = a.hits[:0]
	a.opaque = a.opaque[:0]
	var ps []piece
	errN := a.errPage(r)
	if errN < 0 {
		a.settle(r)
		ps = a.viewportPieces(r)
	}
	bg := uint8(paperGray)
	if ps != nil {
		bg = a.borderFor(r, ps)
	}
	c.Fill(a.full(), bg)
	// the colour layer is authoritative wherever it is active, so it is active
	// only while the page on screen has colour (a grey page after a colour one
	// must not be drawn from the old page's layer)
	a.layerActive = false
	switch {
	case errN >= 0:
		s := r.slots[errN]
		a.drawErrorBox(s.err, func() {
			delete(r.slots, errN)
			a.rdWindow()
			a.readerRedraw("ui")
		}, a.closeReader)
	case ps != nil:
		for _, p := range ps {
			if !p.f.IsGray() {
				colour = true
			}
		}
		if colour {
			a.ensureCol(bg)
			a.layerActive = true
		}
		a.paint(ps, colour)
		ready = true
		r.shown = r.page
	default:
		a.drawCenterText(fmt.Sprintf("Loading %d / %d", r.page+1, r.count))
	}
	a.drawStatus(r, bg)
	if a.statusH() > 0 {
		// the footer comes from the grey canvas also when the colour layer is on
		a.opaque = append(a.opaque, a.statusRect())
	}
	if r.overlay {
		a.drawOverlay()
	}
	a.drawPopover()
	return colour, ready
}

func (a *App) drawCenterText(msg string) {
	s := a.st(46, false, inkDim)
	w := a.Fonts.Width(msg, s)
	a.Fonts.Draw(a.cv, (a.W-w)/2, a.vh()/2, msg, s)
}

// readerRedraw recomposes and presents with the refresh each situation needs:
//
//	first   the first page of a chapter: the one clearing refresh of opening it
//	turn    an ordinary page turn: ONE partial update with the turn waveform;
//	        every N turns (the setting) a clearing refresh instead
//	loading a small marker over the old page, no page change
//	ui      anything else (error box, retry): a plain UI update
func (a *App) readerRedraw(kind string) {
	r := a.rd
	if r == nil {
		return
	}
	switch kind {
	case "loading":
		a.drawLoadingMarker()
		return
	case "first":
		colour, ready := a.composeReader()
		if ready {
			r.first = false
			a.turnWave(r, colour)
			a.clearScreen("reader-first") // one clearing refresh, with the first page
		} else {
			// before the first page: no mode switch (the clearing refresh that follows
			// is the chapter's only flip, plus the re-arm after it)
			a.present(a.full(), display.ModeKeep, false, "reader-loading")
		}
		return
	case "turn":
		colour, _ := a.composeReader()
		r.first = false
		a.sinceFull++
		wave := a.turnWave(r, colour)
		if every := a.refreshEveryFor(colour); every > 0 && a.sinceFull >= every {
			cls := "black and white"
			if colour {
				cls = "colour"
			}
			if idle := a.Cfg.ClearIdleMS; idle > 0 && a.sinceFull < 2*every {
				// the turn is an ordinary partial update; the clearing refresh (a
				// second or more of the panel server's sleeping) runs when the
				// reader has paused, and not at all when they keep turning (it is
				// forced at twice the interval)
				a.logf("clear: %d turns since the last clear >= %d (%s page): when the reader pauses for %d ms", a.sinceFull, every, cls, idle)
				a.present(a.full(), wave, false, "page")
				a.clrT.Reset(time.Duration(idle) * time.Millisecond)
				return
			}
			a.logf("clear: %d turns since the last clear >= %d (%s page)", a.sinceFull, every, cls)
			a.clearScreen("page-full")
		} else {
			a.present(a.full(), wave, false, "page")
		}
		return
	}
	a.composeReader()
	a.present(a.full(), a.readerMode(), false, "reader")
}

func (a *App) drawLoadingMarker() {
	r := a.rd
	c := a.cv
	msg := fmt.Sprintf("%d / %d", r.page+1, r.count)
	s := a.st(44, true, inkBlack)
	w := a.Fonts.Width(msg, s)
	bw, bh := w+a.u(80), a.u(100)
	x := (a.W - bw) / 2
	y := a.vh() - a.u(180)
	rc := image.Rect(x, y, x+bw, y+bh)
	c.Fill(rc, paperGray)
	c.Frame(rc, a.u(4), inkBlack)
	a.Fonts.Draw(c, x+a.u(40), a.textBaseline(y, bh, s.Size), msg, s)
	a.presentOpaque(rc, a.readerMode(), "loading")
}

// ---- overlay ----------------------------------------------------------------------

func (a *App) overlayRects() (top, bottom image.Rectangle) {
	// The top bar is painted from the very top of the screen: the 100 px above
	// the bar proper belong to AppLoad (touches there are not ours, so no
	// button lives there), but the pixels are ours and must not show the page.
	top = image.Rect(0, 0, a.W, a.barRect().Max.Y)
	bh := a.u(250)
	bottom = image.Rect(0, a.H-bh, a.W, a.H)
	return
}

// midPage is the page the counter shows.
func (a *App) midPage(r *reader) int { return r.page }

func (a *App) drawOverlay() {
	r := a.rd
	c := a.cv
	t, b := a.overlayRects()
	a.drawHeader(hdr{title: r.title, back: true, home: true, homeActive: true, gear: true, account: true})

	// The bars are solid: white fill, black rule, nothing of the page is
	// blended into them. (b reaches over the footer strip as well.)
	c.Fill(b, paperGray)
	c.Fill(image.Rect(b.Min.X, b.Min.Y, b.Max.X, b.Min.Y+a.u(3)), inkBlack)
	ls := a.st(46, true, inkBlack)
	cur := a.midPage(r)
	label := fmt.Sprintf("%d / %d", cur+1, r.count)
	if r.unknown {
		label = fmt.Sprintf("%d", cur+1)
	}
	lw := a.Fonts.Width(label, ls)
	a.Fonts.Draw(c, (a.W-lw)/2, b.Min.Y+a.u(100), label, ls)

	// First-page and last-page buttons at the two ends of the slider: big
	// targets, so the far ends do not have to be dragged to. The glyph points to
	// the end of the slider it is at, so with right-to-left reading the left
	// button is the LAST page (the slider runs backwards).
	sy := b.Min.Y + a.u(180)
	bsz := a.u(110)
	x0, x1 := a.u(16)+bsz+a.u(44), a.W-a.u(16)-bsz-a.u(44)
	c.Fill(image.Rect(x0, sy-a.u(4), x1, sy+a.u(4)), inkBlack)
	if !r.unknown && r.count > 1 {
		f := float64(cur) / float64(r.count-1)
		if a.rtlOf(r) {
			f = 1 - f
		}
		kx := x0 + int(f*float64(x1-x0))
		c.Disc(kx, sy, a.u(30), inkBlack)
		c.Disc(kx, sy, a.u(18), paperGray)
		a.addNamedHitXY("slider", image.Rect(0, sy-a.u(70), a.W, sy+a.u(70)), func(x, y int) { a.sliderTap(x0, x1, x) })

		left := image.Rect(a.u(16), sy-bsz/2, a.u(16)+bsz, sy+bsz/2)
		right := image.Rect(a.W-a.u(16)-bsz, sy-bsz/2, a.W-a.u(16), sy+bsz/2)
		leftIsFirst := !a.rtlOf(r)
		drawEnd := func(rc image.Rectangle, atLeft bool, name string, target int) {
			c.Frame(rc, a.u(4), inkBlack)
			cy := (rc.Min.Y + rc.Max.Y) / 2
			if atLeft {
				bx := rc.Min.X + a.u(30)
				c.Fill(image.Rect(bx-a.u(4), cy-a.u(28), bx+a.u(4), cy+a.u(28)), inkBlack)
				c.Chevron(bx+a.u(30), cy, a.u(52), -1, a.u(8), inkBlack)
			} else {
				bx := rc.Max.X - a.u(30)
				c.Fill(image.Rect(bx-a.u(4), cy-a.u(28), bx+a.u(4), cy+a.u(28)), inkBlack)
				c.Chevron(bx-a.u(30), cy, a.u(52), 1, a.u(8), inkBlack)
			}
			a.addNamedHit(name, rc.Inset(-a.u(12)).Intersect(b), func() { a.readerJump(target) })
		}
		firstName, lastName := "page-first", "page-last"
		if leftIsFirst {
			drawEnd(left, true, firstName, 0)
			drawEnd(right, false, lastName, r.count-1)
		} else {
			drawEnd(left, true, lastName, r.count-1)
			drawEnd(right, false, firstName, 0)
		}
	}
	a.opaque = append(a.opaque, t, b)
}

// sliderTap converts a tap on the slider into a page jump.
func (a *App) sliderTap(x0, x1, x int) {
	r := a.rd
	if r == nil || r.unknown || r.count < 2 {
		return
	}
	f := float64(x-x0) / float64(x1-x0)
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	if a.rtlOf(r) {
		f = 1 - f
	}
	a.readerJump(int(f*float64(r.count-1) + 0.5))
}

// chromeWave decides how chrome (bars, the account menu) is painted over the
// page area rects, following bars_clean:
//
//	never   the activity's own waveform, nothing else
//	auto    when the page under the rectangles is not white, a white pre-fill
//	        update of them first (in the same waveform, so no mode message and
//	        no second of waiting for it), then the chrome
//	always  as auto, but in CONTENT (a mode message before and a re-arm after,
//	        two seconds of the panel server's sleep; not in speed mode)
//
// dirty says the waveform was changed: the caller re-arms the activity's.
func (a *App) chromeWave(rects ...image.Rectangle) (mode display.Mode, prefill, dirty bool) {
	if a.screen() != scrReader {
		return a.uiMode(), false, false
	}
	m := a.readerMode()
	switch a.Cfg.BarsClean {
	case "never":
		return m, false, false
	case "always":
		if !a.speedMode() {
			return display.ModeContent, !a.whiteUnder(rects...), true
		}
	}
	return m, !a.whiteUnder(rects...), false
}

// whiteUnder reports whether the area under every one of the rectangles is
// white (at most 0.2 % of its sampled pixels darker than 250: a line of text
// or a frame line is enough to make it not white).
func (a *App) whiteUnder(rects ...image.Rectangle) bool {
	for _, rc := range rects {
		rc = rc.Intersect(a.full())
		non, n := 0, 0
		for y := rc.Min.Y; y < rc.Max.Y; y += 6 {
			for x := rc.Min.X; x < rc.Max.X; x += 6 {
				var l int
				if a.layerActive && a.col != nil {
					v := int(a.col[y*a.W+x])
					rr, gg, bb := (v>>11)&31*8, (v>>5)&63*4, v&31*8
					l = (rr*30 + gg*59 + bb*11) / 100
				} else {
					l = int(a.cv.Gray.Pix[y*a.cv.Gray.Stride+x])
				}
				if l < 250 {
					non++
				}
				n++
			}
		}
		if n > 0 && non*500 > n {
			return false
		}
	}
	return true
}

// prefillWhite sends a white update for each rectangle (the page under chrome
// that is not white is driven to white first, so the chrome is painted on a
// clean ground). The canvas is restored by the compose that follows.
func (a *App) prefillWhite(mode display.Mode, tag string, rects ...image.Rectangle) {
	for _, rc := range rects {
		a.cv.Fill(rc, paperGray)
		a.presentOpaque(rc, mode, tag)
	}
}

// barsMeasure reports whether painting chrome over the page has to look at the
// page first (bars_clean auto and always do, to decide on the white pre-fill;
// never, and speed mode, do not, and then the page is composed once, not twice).
func (a *App) barsMeasure() bool {
	return !a.speedMode() && a.Cfg.BarsClean != "never"
}

// showOverlay reveals the controls: two partial updates (the top bar, the bottom
// bar), no mode message, no flash. With bars_clean auto or always a white
// pre-fill of the same two rectangles goes first when the page under them is not
// white.
func (a *App) showOverlay() {
	r := a.rd
	t, b := a.overlayRects()
	mode, prefill, dirty := a.readerMode(), false, false
	if a.barsMeasure() {
		// measure the page the bars will cover (without them)
		r.overlay = false
		a.composeReader()
		mode, prefill, dirty = a.chromeWave(t, b)
		if prefill {
			a.prefillWhite(mode, "overlay-prefill", t, b)
		}
	}
	r.overlay = true
	a.composeReader()
	a.presentOpaque(t, mode, "overlay-top")
	a.presentOpaque(b, mode, "overlay-bottom")
	if dirty {
		a.rearm()
	}
	a.ovT.Reset(a.overlayFor)
}

// hideOverlay puts the page back under the controls (partial updates only).
func (a *App) hideOverlay() {
	r := a.rd
	if r == nil || !r.overlay {
		return
	}
	hadPop := a.pop.open
	pr := a.popRect()
	r.overlay = false
	a.pop = popover{}
	a.ovT.Stop()
	a.composeReader()
	t, b := a.overlayRects()
	m := a.readerMode()
	a.present(t, m, false, "overlay-hide-top")
	a.present(b, m, false, "overlay-hide-bottom")
	if hadPop {
		a.present(pr, m, false, "overlay-hide-popover")
	}
}

func (a *App) onOverlayTimer() {
	if a.rd == nil || !a.rd.overlay || a.screen() != scrReader {
		return
	}
	if a.pop.open { // the user is in the account menu: wait
		a.ovT.Reset(a.overlayFor)
		return
	}
	a.hideOverlay()
}

// afterChromeChange redraws the current screen after the popover opened or
// closed and updates just the popover's rectangle.
func (a *App) afterChromeChange() {
	mode, dirty, prefill := a.uiMode(), false, false
	switch a.screen() {
	case scrReader:
		if a.pop.open && a.barsMeasure() { // what is under the menu (drawn without it)?
			keep := a.pop
			a.pop = popover{}
			a.composeReader()
			mode, prefill, dirty = a.chromeWave(a.popRect())
			a.pop = keep
		} else if a.pop.open {
			mode = a.readerMode()
		}
		a.composeReader()
	case scrSettings:
		a.drawSettings()
	case scrSignin:
		a.drawSignin()
	case scrSearch:
		a.drawSearch()
	default:
		a.drawList(a.top())
	}
	r := a.popRect()
	if a.pop.open {
		if prefill {
			a.prefillWhite(mode, "popover-prefill", r)
			a.composeReader()
		}
		a.presentOpaque(r, mode, "popover")
		if dirty {
			a.rearm()
		}
		if a.rd != nil && a.rd.overlay {
			a.ovT.Reset(a.overlayFor)
		}
	} else {
		a.present(r, a.uiMode(), false, "popover-close")
	}
}

// onClearTimer runs a clearing refresh that was due at a page turn and put off
// until the reader paused. Anything that moved on in the meantime (another
// turn that already cleared, leaving the book, a menu) cancels it.
func (a *App) onClearTimer() {
	r := a.rd
	if r == nil || a.screen() != scrReader || r.unknown || a.sinceFull == 0 {
		return
	}
	if a.touch.active {
		a.clrT.Reset(150 * time.Millisecond) // a finger is down: ask again shortly
		return
	}
	if _, ready := a.composeReader(); !ready {
		return
	}
	a.logf("clear: after %d turns, the reader paused", a.sinceFull)
	a.clearScreen("page-full")
}
