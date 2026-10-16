package ui

import (
	"bytes"
	"image"
	"strconv"
	"strings"
	"testing"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/imgproc"
	"rmkavita/internal/store"
)

// ---- footer ---------------------------------------------------------------------------

func (h *harness) onUI(f func()) {
	h.t.Helper()
	ch := make(chan struct{})
	h.app.post(func() { f(); close(ch) })
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		h.t.Fatal("the UI goroutine did not run the probe")
	}
}

func TestFooterIsOutsideThePageAndOpaque(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	h.openFirstChapter()
	h.waitReady(0)
	var vh, pageH, statusH int
	h.onUI(func() {
		vh, statusH = h.app.vh(), h.app.statusH()
		pageH = h.app.rd.ready(0).H
	})
	if statusH < 24 || statusH > 40 {
		t.Errorf("footer %d px high, want about 30 (0.13 inch at 229 dpi)", statusH)
	}
	if vh != 2160-statusH || pageH > vh {
		t.Errorf("view height %d, page height %d: the page must be fitted above the footer", vh, pageH)
	}
	// the strip is its own: black rule on top, white below it, text inside
	if v := h.pixelAt(5, vh); v != inkBlack {
		t.Errorf("rule pixel %d", v)
	}
	dark := 0
	for x := 0; x < 1620; x += 3 {
		if h.pixelAt(x, 2160-10) < 128 {
			dark++
		}
	}
	if dark == 0 {
		t.Error("the footer carries no text")
	}
	// the turn is still ONE update (the footer is part of it)
	h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, 1) }, "page 1")
	before := len(h.disp.all())
	h.tap(300, 1000)
	h.waitFor(func() bool { return h.snapshot().Page == 1 }, "turn")
	time.Sleep(60 * time.Millisecond)
	if n := len(h.disp.all()) - before; n != 1 {
		t.Errorf("%d updates for one turn with the footer on, want 1", n)
	}
	var frac float64
	var cur, total int
	h.onUI(func() { frac, cur, total = h.app.readProgress(h.app.rd) })
	if cur != 1 || total != 12 || frac < 0.16 || frac > 0.18 {
		t.Errorf("progress %.3f cur=%d total=%d, want 2/12", frac, cur, total)
	}
	h.stop()

	// off: the page gets the whole screen back
	h = startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever; s.Footer = false }})
	h.openFirstChapter()
	h.waitReady(0)
	h.onUI(func() { vh, statusH = h.app.vh(), h.app.statusH() })
	if vh != 2160 || statusH != 0 {
		t.Errorf("footer off: vh=%d statusH=%d", vh, statusH)
	}
	h.stop()
}

func TestFooterSettingRefitsTheOpenBook(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	h.openMangaB("row-5") // 900x1350 pages: limited by the height of the area, so the footer matters
	h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page 0")
	var before int
	h.onUI(func() { before = h.app.rd.ready(0).H })
	if before != 2130 {
		t.Fatalf("with the footer the page is %d high, want 2130", before)
	}
	h.tap(810, 1000) // middle column: bars
	h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
	h.tapName("settings")
	h.waitScreen("settings")
	h.settingsScrollTo("footer-off")
	h.tapName("footer-off")
	h.waitFor(func() bool { s, _ := store.LoadSettings(h.setFile, store.DefaultConfig()); return !s.Footer }, "saved")
	h.tapName("back")
	h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page again")
	var pageH int
	h.onUI(func() { pageH = h.app.rd.ready(0).H })
	if pageH != 2160 {
		t.Errorf("page height %d after the footer went off, want 2160", pageH)
	}
	h.stop()
}

// ---- first / last page buttons -----------------------------------------------------------

func TestFirstAndLastPageButtons(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	h.openFirstChapter()
	h.waitReady(0)
	h.tap(810, 1000)
	h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
	first, ok1 := h.hitRect("page-first")
	last, ok2 := h.hitRect("page-last")
	if !ok1 || !ok2 {
		t.Fatalf("buttons: first %v last %v", ok1, ok2)
	}
	// right to left (default): the slider runs backwards, page one is at the right end
	if !(first.Min.X > last.Min.X) {
		t.Errorf("RTL: first at x=%d, last at x=%d: first should be on the right", first.Min.X, last.Min.X)
	}
	if first.Dx() < 100 || first.Dy() < 100 {
		t.Errorf("first button %v is a small target", first)
	}
	h.tapName("page-last")
	h.waitFor(func() bool { s := h.snapshot(); return s.Page == 11 && contains(s.ReadyPages, 11) }, "last page")
	h.tap(810, 1000)
	h.waitFor(func() bool { return h.snapshot().Overlay }, "bars again")
	h.tapName("page-first")
	h.waitFor(func() bool { s := h.snapshot(); return s.Page == 0 && contains(s.ReadyPages, 0) }, "first page")
	h.stop()

	// left to right (PDF-like): first at the left end
	h = startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever; s.RTL = false }})
	h.openFirstChapter()
	h.waitReady(0)
	h.tap(810, 1000)
	h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
	first, _ = h.hitRect("page-first")
	last, _ = h.hitRect("page-last")
	if !(first.Min.X < last.Min.X) {
		t.Errorf("LTR: first at x=%d, last at x=%d", first.Min.X, last.Min.X)
	}
	h.stop()
}

// ---- opaque chrome -----------------------------------------------------------------------

// The bars show nothing of the page: the same bars drawn over an all-black and
// over an all-white page are pixel-identical, and so is the account menu.
func TestBarsAndMenuAreOpaque(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	h.openFirstChapter()
	h.waitReady(0)
	var black, white [][]byte
	var rects []image.Rectangle
	h.onUI(func() {
		a := h.app
		r := a.rd
		orig := r.slots[0].img
		fill := func(v uint8) *imgproc.Fitted {
			g := make([]uint8, orig.W*orig.H)
			for i := range g {
				g[i] = v
			}
			return &imgproc.Fitted{W: orig.W, H: orig.H, Gray: g}
		}
		r.overlay = true
		a.pop = popover{open: true}
		top, bottom := a.overlayRects()
		rects = []image.Rectangle{top, bottom, a.popRect()}
		grab := func() [][]byte {
			a.composeReader()
			var out [][]byte
			for _, rc := range rects {
				var b bytes.Buffer
				for y := rc.Min.Y; y < rc.Max.Y; y++ {
					b.Write(a.cv.Gray.Pix[y*a.cv.Gray.Stride+rc.Min.X : y*a.cv.Gray.Stride+rc.Max.X])
				}
				out = append(out, b.Bytes())
			}
			return out
		}
		r.slots[0].img = fill(0)
		black = grab()
		r.slots[0].img = fill(255)
		white = grab()
		r.slots[0].img = orig
		a.pop = popover{}
		r.overlay = false
	})
	for i, rc := range rects {
		if !bytes.Equal(black[i], white[i]) {
			t.Errorf("%v: bar pixels depend on the page under them", rc)
		}
	}
	h.stop()
}

// Over a dark page the bars are painted in the page waveform (after a white
// pre-fill) too: a CONTENT repaint is a mode message and a second of the panel
// server sleeping, twice; only bars_clean = always still does it.
func TestBarsOverADarkPageStayInThePageWaveform(t *testing.T) {
	check := func(clean bool, wantMode display.Mode) {
		h := startOpts(t, hopts{mut: func(c *store.Config) {
			c.BarsClean = "never"
			if clean {
				c.BarsClean = "auto"
			}
		},
			mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
		h.openMangaB("row-8") // Black Pages
		h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page 0")
		h.tap(810, 1000)
		h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
		var got display.Mode = -1
		for _, f := range h.disp.all() {
			if f.tag == "overlay-top" {
				got = f.mode
			}
		}
		if got != wantMode {
			t.Errorf("chrome_clean=%v: bars painted with %v, want %v", clean, got, wantMode)
		}
		h.stop()
	}
	check(true, display.ModeUI)
	check(false, display.ModeUI)
	// a light page: the page waveform
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	h.openFirstChapter()
	h.waitReady(0)
	h.tap(810, 1000)
	h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
	for _, f := range h.disp.all() {
		if f.tag == "overlay-top" && f.mode != display.ModeUI {
			t.Errorf("bars over a light page: %v, want UI", f.mode)
		}
	}
	h.stop()
}

// ---- colour pages and the layer ------------------------------------------------------------

// A grey page after a colour page is drawn from the grey canvas: the colour
// layer of the page before it must not be sent with it.
func TestGreyPageAfterColourPageDropsTheColourLayer(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Colour = true; s.Refresh = store.RefreshNever }})
	h.openMangaB("row-11") // Mixed Pages: colour, grey, colour, ...
	h.waitFor(func() bool {
		s := h.snapshot()
		return s.Screen == "reader" && contains(s.ReadyPages, 0) && contains(s.ReadyPages, 1)
	}, "pages 0 and 1")
	var first, turn frameRec
	for _, f := range h.disp.all() {
		if f.tag == "reader-first" {
			first = f
		}
	}
	if !first.colour {
		t.Fatal("page 0 (colour) was sent without the colour layer")
	}
	h.tap(300, 1000)
	h.waitTag("page", 1)
	for _, f := range h.disp.all() {
		if f.tag == "page" {
			turn = f
		}
	}
	if turn.colour {
		t.Error("the grey page 1 was sent with the colour layer of page 0 (stale)")
	}
	h.tap(300, 1000)
	h.waitTag("page", 2)
	last := h.disp.all()[len(h.disp.all())-1]
	if !last.colour {
		t.Error("colour page 2 lost its layer")
	}
	h.stop()
}

// Colour pages have their own clearing interval (every 5 by default), black and
// white pages keep every 20; the log says which fired.
func TestColourPagesClearEveryFiveBlackAndWhiteEveryTwenty(t *testing.T) {
	h := startOpts(t, hopts{pages: 30, mut: func(c *store.Config) { c.Border = "white" },
		mutSet: func(s *store.Settings) { s.Colour = true; s.Refresh = store.RefreshDefault }})
	h.openMangaB("row-0") // Color JPEG Scans, 10 pages
	h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page 0")
	for i := 1; i <= 5; i++ {
		h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, i) }, "page ready")
		h.tap(300, 1000)
		h.waitFor(func() bool { return h.snapshot().Page == i }, "turn")
	}
	h.waitTag("page-full", 1)
	if lg := h.readLog(); !strings.Contains(lg, "clear: 5 turns since the last clear >= 5 (colour page)") {
		t.Errorf("the colour threshold was not logged:\n%s", tail(lg, 700))
	}
	h.stop()

	// the same settings, black and white pages: no clear within 6 turns
	h = startOpts(t, hopts{pages: 30, mut: func(c *store.Config) { c.Border = "white" },
		mutSet: func(s *store.Settings) { s.Colour = true; s.Refresh = store.RefreshDefault }})
	h.openFirstChapter()
	h.waitReady(0)
	for i := 1; i <= 6; i++ {
		h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, i) }, "page ready")
		h.tap(300, 1000)
		h.waitFor(func() bool { return h.snapshot().Page == i }, "turn")
	}
	if n := h.disp.count("page-full"); n != 0 {
		t.Errorf("%d clears within 6 black and white turns", n)
	}
	h.stop()

	// a custom colour interval, and Never
	h = startOpts(t, hopts{pages: 30, mut: func(c *store.Config) { c.Border = "white" },
		mutSet: func(s *store.Settings) {
			s.Colour = true
			s.ColourRefresh = store.ColourEvery3
			s.Refresh = store.RefreshDefault
		}})
	h.openMangaB("row-0")
	h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page 0")
	for i := 1; i <= 3; i++ {
		h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, i) }, "page ready")
		h.tap(300, 1000)
		h.waitFor(func() bool { return h.snapshot().Page == i }, "turn")
	}
	h.waitTag("page-full", 1)
	h.stop()
}

func TestColourIntervalSettingsPersist(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Refresh = store.RefreshDefault }})
	h.waitTag("list", 1)
	h.tapName("settings")
	h.waitScreen("settings")
	h.settingsScrollTo("crefresh-10")
	h.tapName("crefresh-10")
	h.waitFor(func() bool {
		s, _ := store.LoadSettings(h.setFile, store.DefaultConfig())
		return s.ColourRefresh == store.ColourEvery10
	}, "every 10 saved")
	h.settingsScrollTo("crefresh-c2-plus")
	h.tapName("crefresh-c2-plus")
	h.waitFor(func() bool {
		s, _ := store.LoadSettings(h.setFile, store.DefaultConfig())
		return s.ColourRefresh == store.ColourCustom2 && s.ColourCustom[1] == 16
	}, "custom colour slot saved")
	h.stop()
	s, _ := store.LoadSettings(h.setFile, store.DefaultConfig())
	if s.ColourEveryN() != 16 || s.RefreshEvery() != 20 || !s.Footer {
		t.Errorf("colour %d, b/w %d, footer %v", s.ColourEveryN(), s.RefreshEvery(), s.Footer)
	}
	d := store.DefaultSettings()
	if d.ColourEveryN() != 5 || d.RefreshEvery() != 20 {
		t.Errorf("defaults: colour every %d, b/w every %d", d.ColourEveryN(), d.RefreshEvery())
	}
}

// ---- hot corners ---------------------------------------------------------------------------

func TestHotCornerDefaultsAndTable(t *testing.T) {
	c := store.DefaultConfig()
	if c.HotTL != "none" || c.HotTR != "clear" || c.HotBL != "quit" || c.HotBR != "none" {
		t.Errorf("defaults %q %q %q %q", c.HotTL, c.HotTR, c.HotBL, c.HotBR)
	}
	want := []string{"none", "quit", "clear", "home", "settings", "controls", "first", "last", "next_chapter", "prev_chapter",
		"colour", "direction", "fit", "footer"}
	if strings.Join(store.HotActions, ",") != strings.Join(want, ",") {
		t.Errorf("actions %v", store.HotActions)
	}
	for _, w := range want {
		if hotLabels[w] == "" {
			t.Errorf("no label for %q", w)
		}
	}
	p := store.ParseConfig("hot_tl = FIRST\nhot_tr = bogus\nhot_br = next_chapter\n")
	if p.HotTL != "first" || p.HotTR != "clear" || p.HotBR != "next_chapter" || len(p.Unknown) != 1 {
		t.Errorf("parsed %+v", p)
	}
}

func TestHotCornerChooserPersistsAndWorks(t *testing.T) {
	h := startOpts(t, hopts{pages: 30, mut: func(c *store.Config) { c.HoldMs = 400 },
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	h.waitTag("list", 1)
	h.tapName("settings")
	h.waitScreen("settings")
	h.settingsScrollTo("hot-open")
	h.tapName("hot-open")
	h.waitFor(func() bool { _, ok := h.hitRect("hot-hot_tl"); return ok }, "the four corners")
	for _, n := range []string{"hot-hot_tl", "hot-hot_tr", "hot-hot_bl", "hot-hot_br"} {
		if _, ok := h.hitRect(n); !ok {
			t.Errorf("no row %q", n)
		}
	}
	h.tapName("hot-hot_tl")
	h.waitFor(func() bool { _, ok := h.hitRect("hotpick-first"); return ok }, "the chooser")
	h.tapName("hotpick-first")
	h.waitFor(func() bool { return strings.Contains(h.readConf(), "hot_tl = first") }, "hot_tl written")
	h.tapName("back") // chooser -> the corners
	h.waitFor(func() bool { _, ok := h.hitRect("hot-hot_br"); return ok }, "back at the corners")
	h.tapName("hot-hot_br")
	h.waitFor(func() bool { _, ok := h.hitRect("hotpick-last"); return ok }, "chooser for bottom right")
	h.tapName("hotpick-last")
	h.waitFor(func() bool { return strings.Contains(h.readConf(), "hot_br = last") }, "hot_br written")
	if !strings.Contains(h.readConf(), h.mock.Key()) {
		t.Error("the config rewrite lost the sign-in")
	}
	h.tapName("back") // the chooser -> the corners
	h.waitFor(func() bool { _, ok := h.hitRect("hot-hot_tl"); return ok }, "the corners")
	h.tapName("back") // the corners -> settings
	h.waitFor(func() bool { _, ok := h.hitRect("hot-open"); return ok || h.snapshot().Screen == "settings" }, "settings")
	h.tapName("back") // settings -> the list
	h.waitFor(func() bool { return h.snapshot().Screen == "list" }, "list")
	h.stop()

	// persisted, and working in the reader: hold top-left = first page, bottom-right = last
	h.launch()
	h.waitTag("list", 1)
	h.openFirstChapter()
	h.waitReady(0)
	h.hold(120, 120, 600*time.Millisecond) // top-left, 400 ms hold
	time.Sleep(100 * time.Millisecond)
	h.hold(1500, 2050, 600*time.Millisecond) // bottom-right
	h.waitFor(func() bool { return h.snapshot().Page == 29 }, "bottom-right corner = last page")
	h.hold(120, 120, 600*time.Millisecond)
	h.waitFor(func() bool { return h.snapshot().Page == 0 }, "top-left corner = first page")
	h.stop()
}

func TestHotCornerActions(t *testing.T) {
	run := func(act string, setup func(h *harness), check func(h *harness)) {
		t.Helper()
		h := startOpts(t, hopts{pages: 20, mut: func(c *store.Config) { c.HoldMs = 400; c.HotTL = act },
			mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
		h.openFirstChapter()
		h.waitReady(0)
		if setup != nil {
			setup(h)
		}
		h.hold(120, 120, 600*time.Millisecond)
		check(h)
		h.stop()
	}
	run("controls", nil, func(h *harness) {
		h.waitFor(func() bool { return h.snapshot().Overlay }, "controls shown")
	})
	run("home", nil, func(h *harness) {
		h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "list" }, "home")
	})
	run("settings", nil, func(h *harness) {
		h.waitFor(func() bool { return h.snapshot().Screen == "settings" }, "settings")
	})
	run("colour", nil, func(h *harness) {
		h.waitFor(func() bool { s, _ := store.LoadSettings(h.setFile, store.DefaultConfig()); return s.Colour }, "colour on")
	})
	run("direction", nil, func(h *harness) {
		h.waitFor(func() bool { s, _ := store.LoadSettings(h.setFile, store.DefaultConfig()); return !s.RTL }, "left to right")
	})
	run("fit", nil, func(h *harness) {
		h.waitFor(func() bool {
			s, _ := store.LoadSettings(h.setFile, store.DefaultConfig())
			return s.Comic.Fit == store.FitWidth
		}, "fit width")
	})
	run("footer", nil, func(h *harness) {
		h.waitFor(func() bool { s, _ := store.LoadSettings(h.setFile, store.DefaultConfig()); return !s.Footer }, "footer off")
	})
	run("last", nil, func(h *harness) {
		h.waitFor(func() bool { return h.snapshot().Page == 19 }, "last page")
	})
	run("first", func(h *harness) { h.turnForward(1) }, func(h *harness) {
		h.waitFor(func() bool { return h.snapshot().Page == 0 }, "first page")
	})
	run("next_chapter", nil, func(h *harness) {
		h.waitFor(func() bool { return h.disp.count("reader-first") >= 2 }, "next chapter opened")
	})
	run("clear", nil, func(h *harness) {
		h.waitFor(func() bool { return h.disp.count("clear") >= 1 }, "clear")
	})
	// a corner set to nothing does nothing
	h := startOpts(t, hopts{mut: func(c *store.Config) { c.HoldMs = 400; c.HotTL = "none" }})
	h.openFirstChapter()
	h.waitReady(0)
	h.hold(120, 120, 600*time.Millisecond) // a hold in a corner with no function is just a (long) tap
	time.Sleep(100 * time.Millisecond)
	if h.snapshot().Screen != "reader" || strings.Contains(h.readLog(), "hot corner (") {
		t.Error("a corner set to nothing did something")
	}
	h.stop()
}

func TestPreviousChapterHotCorner(t *testing.T) {
	h := startOpts(t, hopts{mut: func(c *store.Config) { c.HoldMs = 400; c.HotTL = "prev_chapter"; c.HotTR = "next_chapter" },
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	h.openFirstChapter() // Series 01 - Volume 1
	h.waitReady(0)
	h.hold(1500, 120, 600*time.Millisecond) // next chapter
	h.waitFor(func() bool { return h.disp.count("reader-first") >= 2 }, "volume 2")
	h.hold(120, 120, 600*time.Millisecond) // and back
	h.waitFor(func() bool { return h.disp.count("reader-first") >= 3 }, "volume 1 again")
	if lg := h.readLog(); strings.Count(lg, "open chapter") != 3 {
		t.Errorf("chapters opened: %s", tail(lg, 500))
	}
	h.stop()
}

// ---- covers and list repaints --------------------------------------------------------------

func TestLibraryCoversAreNotRequested(t *testing.T) {
	h := startOpts(t, hopts{mut: func(c *store.Config) { c.Covers = true }})
	h.waitTag("list", 1)
	h.tapName("row-5") // All Libraries
	h.waitTag("list", 2)
	time.Sleep(300 * time.Millisecond)
	for _, r := range h.mock.RequestLog() {
		if strings.HasPrefix(r, "cover library") {
			t.Errorf("a library cover was requested: %s", r)
		}
	}
	if lg := h.readLog(); strings.Contains(lg, "cover fetch failed") {
		t.Errorf("cover errors in the log:\n%s", tail(lg, 400))
	}
	h.stop()
	time.Sleep(300 * time.Millisecond)
}

// Scrolling a list repaints the body, not the header; a cover that arrives
// repaints its own row.
func TestListScrollAndCoversRepaintOnlyWhatChanged(t *testing.T) {
	h := startOpts(t, hopts{mut: func(c *store.Config) { c.Covers = true }})
	h.waitTag("list", 1)
	h.tapName("row-5")
	h.waitTag("list", 2)
	h.tapName("row-0") // Manga A: 45 series, covers
	h.waitTag("list", 3)
	h.waitFor(func() bool { return h.snapshot().ListRows >= 10 }, "rows")
	h.waitFor(func() bool { return h.disp.count("covers") >= 1 }, "a cover arrived")
	full := image.Rect(0, 0, 1620, 2160)
	for _, f := range h.disp.all() {
		if f.tag == "covers" && (f.rect == full || f.rect.Dy() > 300) {
			t.Errorf("a cover repainted %v", f.rect)
		}
	}
	h.tapName("page-next")
	h.waitTag("scroll", 1)
	for _, f := range h.disp.all() {
		if f.tag == "scroll" {
			if f.rect == full || f.rect.Min.Y < 200 {
				t.Errorf("a scroll repainted %v: the header did not change", f.rect)
			}
		}
	}
	h.stop()
	time.Sleep(300 * time.Millisecond) // late cover goroutines finish before the temp folder goes
}

// ---- memory: the window, the budget, the line --------------------------------------------------

func TestWindowKeepsBehindAndAheadAndFreesTheFarthest(t *testing.T) {
	h := startOpts(t, hopts{pages: 30, mut: func(c *store.Config) { c.Warmup = false },
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	h.openFirstChapter()
	h.waitReady(0)
	for i := 1; i <= 8; i++ {
		h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, i) }, "page ready")
		h.tap(300, 1000)
		h.waitFor(func() bool { return h.snapshot().Page == i }, "turn")
	}
	// page 8: 6 and 7 behind (keep_behind 2), 9, 10, 11 ahead (read_ahead 3), nothing else
	h.waitFor(func() bool {
		s := h.snapshot()
		return len(s.ReadyPages) == 6 && contains(s.ReadyPages, 6) && contains(s.ReadyPages, 7) && contains(s.ReadyPages, 8) &&
			contains(s.ReadyPages, 9) && contains(s.ReadyPages, 10) && contains(s.ReadyPages, 11)
	}, "the window 6..11")
	h.stop()
}

func TestByteBudgetShrinksTheWindowAheadFirstAndNeverTheCurrentPage(t *testing.T) {
	// colour pages are 2 bytes a pixel: 7 MB each at 1620x2130; 24 MB holds three
	h := startOpts(t, hopts{pages: 30, pw: 1000, ph: 1500, mut: func(c *store.Config) { c.Warmup = false; c.CacheMB = 32; c.Border = "white" },
		mutSet: func(s *store.Settings) {
			s.Refresh = store.RefreshNever
			s.Colour = true
			s.ColourRefresh = store.ColourNever
		}})
	h.openMangaB("row-0") // colour pages
	h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page 0")
	for i := 1; i <= 4; i++ {
		h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, i) }, "page ready")
		h.tap(300, 1000)
		h.waitFor(func() bool { return h.snapshot().Page == i }, "turn")
	}
	time.Sleep(200 * time.Millisecond)
	s := h.snapshot()
	if s.HeldBytes > 32<<20 {
		t.Errorf("held %d bytes, budget 32 MB", s.HeldBytes)
	}
	if !contains(s.ReadyPages, 4) {
		t.Errorf("the current page is not held: %v", s.ReadyPages)
	}
	// priority: current, next, the page behind, ... : with room for four of them the far-ahead page goes first
	if contains(s.ReadyPages, 7) {
		t.Errorf("far-ahead pages are held under a tight budget: %v", s.ReadyPages)
	}
	h.stop()
}

func TestMemoryLineIsLogged(t *testing.T) {
	h := startOpts(t, hopts{pages: 40, mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	h.openFirstChapter()
	h.waitReady(0)
	for i := 1; i <= 24; i++ {
		h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, i) }, "page ready")
		h.tap(300, 1000)
		h.waitFor(func() bool { return h.snapshot().Page == i }, "turn")
	}
	h.stop()
	lg := h.readLog()
	i := strings.Index(lg, " mem heap=")
	if i < 0 {
		t.Fatalf("no memory line in the log:\n%s", tail(lg, 600))
	}
	line := lg[i:]
	if j := strings.Index(line, "\n"); j >= 0 {
		line = line[:j]
	}
	for _, want := range []string{"sys=", "goroutines=", "cache=", "ahead=", "behind=", "avail="} {
		if !strings.Contains(line, want) {
			t.Errorf("memory line %q lacks %q", line, want)
		}
	}
	_ = strconv.Itoa
}

// TestListUpdateCensus walks a typical browse (the same kinds of steps as the
// first panel session) and reports how many list-screen updates were
// full-screen and how many were partial. It asserts only that the walk has some
// partial ones; the numbers go into NOTES.md.
func TestListUpdateCensus(t *testing.T) {
	h := startOpts(t, hopts{mut: func(c *store.Config) { c.Covers = true }})
	h.waitTag("list", 1)
	h.tapName("row-5")
	h.waitTag("list", 2)
	h.tapName("row-0")
	h.waitTag("list", 3)
	h.waitFor(func() bool { return h.snapshot().ListRows >= 10 }, "rows")
	for i := 1; i <= 2; i++ {
		h.tapName("page-next")
		h.waitTag("scroll", i)
	}
	h.tapName("back")
	h.waitFor(func() bool { return h.snapshot().Screen == "list" && h.disp.count("back") >= 1 }, "back")
	h.tapName("settings")
	h.waitScreen("settings")
	for i := 0; i < 3; i++ {
		h.settingsScrollOnce()
	}
	time.Sleep(700 * time.Millisecond) // late covers
	full, part := map[string]int{}, map[string]int{}
	whole := image.Rect(0, 0, 1620, 2160)
	for _, f := range h.disp.all() {
		switch f.tag {
		case "start", "reader-first", "reader-close", "settings-open", "settings-close", "home", "clear", "page", "page-full", "reader-loading":
			continue
		}
		if f.rect == whole {
			full[f.tag]++
		} else {
			part[f.tag]++
		}
	}
	t.Logf("list-screen updates, full-screen: %v", full)
	t.Logf("list-screen updates, partial:     %v", part)
	nPart := 0
	for _, n := range part {
		nPart += n
	}
	if nPart == 0 {
		t.Error("no partial list updates at all")
	}
	h.stop()
	time.Sleep(300 * time.Millisecond)
}
