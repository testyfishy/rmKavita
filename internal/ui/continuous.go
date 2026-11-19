package ui

import "rmkavita/internal/display"

// Continuous reading: the pages of a chapter are stacked into one vertical
// strip (each fitted as the Fit setting says, centred, a small gap between
// them) and the screen is a window onto it. r.page is the page at the top of
// the window and r.off how far down that page the window starts.
//
// The strip never animates: a tap, a side button or a horizontal swipe moves
// it by exactly one screen (less the usual overlap, so the eye keeps its
// place), a vertical swipe moves it by the swipe's length, and either way the
// screen is redrawn once. Pages that the window needs and that are not decoded
// yet are fetched first (the read-ahead keeps them coming).

// continuousOn reports whether the reader shows the strip.
func (a *App) continuousOn(r *reader) bool { return r != nil && a.prefsFor(r.doc).Continuous() }

// contGap is the space between two pages of the strip.
func (a *App) contGap() int { return a.u(14) }

// contH is the height a page takes in the strip (with the gap after it), or
// -1 while it is not decoded.
func (a *App) contH(r *reader, n int) int {
	f := r.ready(n)
	if f == nil {
		return -1
	}
	return f.H + a.contGap()
}

// contPages lists the pages the window shows, from r.page down, and whether
// all of them are decoded (missing reports the first one that is not).
func (a *App) contPages(r *reader) (pages []int, missing int) {
	y := -r.off
	for n := r.page; n < r.count && y < a.vh(); n++ {
		pages = append(pages, n)
		h := a.contH(r, n)
		if h < 0 {
			return pages, n
		}
		y += h
	}
	return pages, -1
}

// contPieces lays out the window, or nil while a page in it is missing.
func (a *App) contPieces(r *reader) []piece {
	pages, missing := a.contPages(r)
	if missing >= 0 {
		return nil
	}
	var ps []piece
	y := -r.off
	for _, n := range pages {
		f := r.ready(n)
		top, bot := max(y, 0), min(y+f.H, a.vh())
		if bot > top {
			p := piece{f: f, w: min(f.W, a.W), h: bot - top, sy: top - y, dy: top}
			if f.W > a.W {
				p.sx = (f.W - a.W) / 2
			}
			p.dx = (a.W - p.w) / 2
			ps = append(ps, p)
		}
		y += f.H + a.contGap()
	}
	return ps
}

// contNormalize carries the offset across page boundaries while the pages it
// passes are decoded, and keeps the window from running past the end of the
// chapter (the last page's bottom stops at the bottom of the screen) or before
// its start.
func (a *App) contNormalize(r *reader) {
	for r.off < 0 && r.page > 0 {
		h := a.contH(r, r.page-1)
		if h < 0 {
			break // settled once the page above arrives (r.backRem)
		}
		r.page--
		r.off += h
	}
	if r.page == 0 && r.off < 0 {
		r.off = 0
	}
	for {
		h := a.contH(r, r.page)
		if h < 0 || r.off < h || r.page+1 >= r.count {
			break
		}
		r.off -= h
		r.page++
	}
	// the end: how much strip is left below the window's top?
	left := -r.off
	for n := r.page; n < r.count; n++ {
		h := a.contH(r, n)
		if h < 0 {
			return // not all known: no clamp yet
		}
		left += h
	}
	left -= a.contGap() // no gap after the last page
	if short := a.vh() - left; short > 0 {
		r.off -= short
		for r.off < 0 && r.page > 0 {
			h := a.contH(r, r.page-1)
			if h < 0 {
				r.off = 0
				return
			}
			r.page--
			r.off += h
		}
		if r.off < 0 {
			r.off = 0
		}
	}
}

// contAtEnd reports whether the window shows the bottom of the last page.
func (a *App) contAtEnd(r *reader) bool {
	pages, missing := a.contPages(r)
	if missing >= 0 || len(pages) == 0 || pages[len(pages)-1] != r.count-1 {
		return false
	}
	y := -r.off
	for _, n := range pages {
		y += a.contH(r, n)
	}
	return y-a.contGap() <= a.vh()
}

// contScroll moves the window by d pixels (positive = forward). Moving
// forward from the end of the chapter opens the next one; moving back from
// its start does nothing.
func (a *App) contScroll(d int) {
	r := a.rd
	if r == nil || d == 0 {
		return
	}
	if d > 0 && a.contAtEnd(r) {
		a.nextChapter()
		return
	}
	if d < 0 && r.page == 0 && r.off <= 0 {
		return
	}
	r.off += d
	if r.off < 0 && r.page > 0 && a.contH(r, r.page-1) < 0 {
		// the page above is not decoded: land at its end minus what is left
		r.backRem = -r.off
		r.page--
		r.off = 0
	}
	a.contNormalize(r)
	r.startPM, r.toEnd = -1, false
	a.afterMove()
}

// contStep is one screen forward or back.
func (a *App) contStep(dir int) { a.contScroll(dir * stepFor(a.vh())) }

// contSettle applies what was pending for a page that has just been decoded:
// a restored position (per mille of the page's height), a step back into a
// page above that was not there yet, or the end of the page.
func (a *App) contSettle(r *reader) {
	f := r.ready(r.page)
	if f == nil {
		return
	}
	switch {
	case r.backRem > 0:
		r.off = f.H + a.contGap() - r.backRem
		r.backRem = 0
	case r.toEnd:
		r.off = max(0, f.H-a.vh())
	case r.startPM > 0:
		r.off = r.startPM * f.H / 1000
	}
	r.startPM, r.toEnd = -1, false
	a.contNormalize(r)
}

// contPM is the window's offset inside its top page in thousandths of the
// page's height (the saved position).
func (a *App) contPM(r *reader) int {
	f := r.ready(r.page)
	if f == nil || r.off <= 0 || f.H <= 0 {
		return 0
	}
	return min(999, r.off*1000/f.H)
}

// buttonDir is the direction of a side key under the Buttons setting.
func (a *App) buttonDir(name string) int {
	dir := 1
	if name == display.BtnPrev {
		dir = -1
	}
	if a.Set.Buttons == "swapped" {
		dir = -dir
	}
	return dir
}

// onButton handles a hardware key: the side keys move one screen in the
// reader (in either scroll mode: one segment or one page) and one page of rows
// in lists and in Settings; the power key leaves the app.
func (a *App) onButton(name string) {
	if name == display.BtnPower {
		a.quit("power button")
		return
	}
	if name != display.BtnNext && name != display.BtnPrev {
		return
	}
	dir := a.buttonDir(name)
	if a.pop.open {
		a.pop = popover{}
		a.afterChromeChange()
		return
	}
	switch a.screen() {
	case scrReader:
		r := a.rd
		if r.overlay {
			a.hideOverlay()
		}
		if a.errPage(r) >= 0 {
			return
		}
		a.readerStep(dir)
	case scrSettings:
		a.settingsScroll(-dir)
	case scrList:
		a.listPage(dir)
	}
}
