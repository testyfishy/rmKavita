package ui

import (
	"strings"
	"testing"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/store"
)

// koboHarness is a Kobo Libra 2 sized screen (1264x1680, 300 dpi) with the
// Kobo defaults: comics fit to width in the continuous strip, page keys.
func koboHarness(t *testing.T, pages int, mutSet func(*store.Settings)) *harness {
	t.Helper()
	h := startOpts(t, hopts{w: 1264, h: 1680, dpi: 300, pages: pages, pw: 600, ph: 900,
		mutSet: func(s *store.Settings) {
			s.Refresh = store.RefreshNever
			s.Comic = store.Prefs{Fit: store.FitWidth, Scroll: store.ScrollContinuous}
			if mutSet != nil {
				mutSet(s)
			}
		},
		app: func(a *App) {
			a.Cfg.Platform = store.PlatformKobo
			a.hasButtons = true
		}})
	// The shorter screen shows five home rows: the side key pages the list down
	// to All Libraries (row 5), then the usual walk to Series 01 - Volume 1.
	h.waitTag("list", 1)
	h.tapName("page-next")
	h.waitFor(func() bool { return h.snapshot().ListScroll == 5 }, "the home list paged down")
	for _, row := range []string{"row-0", "row-0", "row-0"} {
		title := h.snapshot().ListTitle
		h.tapName(row)
		h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle != title && !s.ListLoading }, "the next list")
	}
	h.tapName("row-0")
	h.waitTag("reader-first", 1)
	h.waitReady(0)
	return h
}

func (h *harness) button(name string) {
	h.disp.events <- display.Event{Kind: display.Button, Text: name, At: time.Now()}
}

// pos is the reader's (page, offset) read on the UI goroutine.
func (h *harness) pos() (page, off int) {
	page, off = -1, -1
	h.onUI(func() {
		if r := h.app.rd; r != nil {
			page, off = r.page, r.off
		}
	})
	return
}

func (h *harness) waitPos(page, off int, what string) {
	h.t.Helper()
	h.waitFor(func() bool { p, o := h.pos(); return p == page && o == off }, what)
}

func TestKoboDefaultsAreFitWidthContinuous(t *testing.T) {
	s := store.DefaultSettingsFor(store.PlatformKobo)
	if s.Comic.Fit != store.FitWidth || !s.Comic.Continuous() {
		t.Fatalf("kobo comic prefs = %+v, want fit width, continuous", s.Comic)
	}
	if r := store.DefaultSettings(); r.Comic.Continuous() || r.Comic.Fit != store.FitBoth {
		t.Fatalf("reMarkable defaults changed: %+v", r.Comic)
	}
}

// One press of a side key moves the strip by exactly one screen (less the
// overlap), across the page boundary, and back.
func TestContinuousButtonsMoveOneScreen(t *testing.T) {
	h := koboHarness(t, 12, nil)
	var step, h0, gap, w int
	h.onUI(func() {
		step = stepFor(h.app.vh())
		h0 = h.app.rd.ready(0).H
		w = h.app.rd.ready(0).W
		gap = h.app.contGap()
	})
	if w != 1264 {
		t.Fatalf("page fitted %d px wide, want the screen width 1264", w)
	}
	if h0 <= h.app.vh() {
		t.Fatalf("test page %d px high: it must be taller than the screen", h0)
	}
	h.button(display.BtnNext)
	h.waitPos(0, step, "one screen down page 0")
	h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, 1) }, "page 1 ready")
	h.button(display.BtnNext)
	want := 2*step - (h0 + gap)
	if want < 0 {
		h.waitPos(0, 2*step, "two screens down")
	} else {
		h.waitPos(1, want, "into page 1")
	}
	h.button(display.BtnPrev)
	h.waitPos(0, step, "back one screen")
	h.button(display.BtnPrev)
	h.waitPos(0, 0, "back at the top")
	h.button(display.BtnPrev) // at the start: nothing
	time.Sleep(80 * time.Millisecond)
	if p, o := h.pos(); p != 0 || o != 0 {
		t.Fatalf("moved before the start: %d/%d", p, o)
	}
}

// Every key press is one screen update: no animation, no extra frames.
func TestContinuousButtonIsOneUpdate(t *testing.T) {
	h := koboHarness(t, 12, nil)
	h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, 1) }, "page 1 ready")
	time.Sleep(50 * time.Millisecond)
	before := h.disp.count("page")
	n0 := len(h.disp.all())
	h.button(display.BtnNext)
	h.waitTag("page", before+1)
	time.Sleep(80 * time.Millisecond)
	if n := len(h.disp.all()) - n0; n != 1 {
		t.Fatalf("a key press sent %d updates, want 1: %v", n, h.disp.tags()[n0:])
	}
}

func TestSwappedButtonsReverse(t *testing.T) {
	h := koboHarness(t, 12, func(s *store.Settings) { s.Buttons = store.ButtonsSwapped })
	var step int
	h.onUI(func() { step = stepFor(h.app.vh()) })
	h.button(display.BtnPrev) // swapped: the back key goes forward
	h.waitPos(0, step, "forward on the back key")
}

// A vertical swipe moves the strip by the swipe's own length.
func TestContinuousSwipeFollowsTheFinger(t *testing.T) {
	h := koboHarness(t, 12, nil)
	h.swipe(632, 1300, 632, 700) // 600 px up
	h.waitPos(0, 600, "scrolled by the swipe")
	h.swipe(632, 700, 632, 1000) // 300 px down
	h.waitPos(0, 300, "back by the swipe")
}

// Going forward from the bottom of the last page opens the next chapter.
func TestContinuousEndOpensNextChapter(t *testing.T) {
	h := koboHarness(t, 3, nil)
	for i := 0; i < 40 && h.disp.count("reader-first") < 2; i++ {
		ready := false
		h.onUI(func() { ready = h.app.rd != nil && h.app.viewportReady(h.app.rd) })
		if ready {
			h.button(display.BtnNext)
		}
		time.Sleep(40 * time.Millisecond)
	}
	if h.disp.count("reader-first") < 2 {
		t.Fatal("the next chapter did not open after the end of the strip")
	}
}

// The window never runs past the last page: its bottom stops at the bottom of
// the screen.
func TestContinuousClampsAtTheEnd(t *testing.T) {
	h := koboHarness(t, 2, nil)
	h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, 1) }, "page 1 ready")
	var atEnd bool
	for i := 0; i < 10 && !atEnd; i++ {
		h.onUI(func() { atEnd = h.app.contAtEnd(h.app.rd) })
		if !atEnd {
			h.button(display.BtnNext)
			time.Sleep(60 * time.Millisecond)
		}
	}
	var page, off, h1, vh int
	h.onUI(func() {
		page, off, vh = h.app.rd.page, h.app.rd.off, h.app.vh()
		h1 = h.app.rd.ready(1).H
	})
	if page != 1 || off != h1-vh {
		t.Fatalf("at the end: page %d off %d, want page 1 off %d", page, off, h1-vh)
	}
}

// Page-by-page mode keeps the old behaviour: a key turns the page (or the
// next segment of a page taller than the screen).
func TestButtonsInPageMode(t *testing.T) {
	h := koboHarness(t, 12, func(s *store.Settings) { s.Comic = store.Prefs{Fit: store.FitBoth, Scroll: store.ScrollPage} })
	h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, 1) }, "page 1 ready")
	h.button(display.BtnNext)
	h.waitPos(1, 0, "next page")
	h.button(display.BtnPrev)
	h.waitPos(0, 0, "previous page")
}

func TestPowerButtonQuits(t *testing.T) {
	h := koboHarness(t, 12, nil)
	h.button(display.BtnPower)
	select {
	case r := <-h.done:
		if !strings.Contains(r, "power") {
			t.Fatalf("exit reason %q", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the power key did not end the app")
	}
}

// The position inside a tall page survives closing and reopening the chapter.
func TestContinuousPositionIsRestored(t *testing.T) {
	h := koboHarness(t, 12, nil)
	h.swipe(632, 1300, 632, 500) // 800 px into page 0
	h.waitPos(0, 800, "scrolled")
	var h0 int
	h.onUI(func() { h0 = h.app.rd.ready(0).H; h.app.closeReader() })
	h.waitScreen("list")
	h.tapName("row-0")
	h.waitFor(func() bool { p, o := h.pos(); return p == 0 && o > 0 }, "reopened at the offset")
	_, o := h.pos()
	if want := (800 * 1000 / h0) * h0 / 1000; o != want {
		t.Fatalf("reopened at %d, want %d", o, want)
	}
}

func TestSettingsShowScrollAndButtons(t *testing.T) {
	h := koboHarness(t, 12, nil)
	h.onUI(func() { h.app.closeReader() })
	h.waitScreen("list")
	h.tapName("settings")
	h.waitScreen("settings")
	have := h.settingsHits()
	for _, n := range []string{"scroll-page", "scroll-continuous", "buttons-normal", "buttons-swapped"} {
		if !have[n] {
			t.Errorf("Settings has no %q row", n)
		}
	}
	h.settingsScrollTop()
	h.settingsScrollTo("scroll-page")
	h.tapName("scroll-page")
	h.waitFor(func() bool {
		var s string
		h.onUI(func() { s = h.app.Set.Comic.Scroll })
		return s == store.ScrollPage
	}, "scroll set to page")
}
