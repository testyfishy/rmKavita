package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/imgproc"
	"rmkavita/internal/store"
)

// pixelAt reads the canvas on the UI goroutine.
func (h *harness) pixelAt(x, y int) uint8 {
	h.t.Helper()
	ch := make(chan uint8, 1)
	h.app.post(func() {
		c := h.app.cv.Gray
		ch <- c.Pix[y*c.Stride+x]
	})
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		h.t.Fatal("canvas read timed out")
		return 0
	}
}

// openMangaB opens the named row of Manga B and its first chapter.
func (h *harness) openMangaB(row string) {
	h.t.Helper()
	h.waitTag("list", 1)
	h.tapName("row-5")
	h.waitTag("list", 2)
	h.tapName("row-1")
	h.waitTag("list", 3)
	h.waitFor(func() bool { return h.snapshot().ListRows >= 9 }, "Manga B rows")
	if n, _ := strconv.Atoi(strings.TrimPrefix(row, "row-")); n >= 10 { // the list shows ten rows: scroll to the next ten
		h.tapName("page-next")
		h.waitTag("scroll", 1)
		row = "row-" + strconv.Itoa(n-10) // rows are named by their place on the screen
	}
	h.tapName(row)
	h.waitTag("list", 4)
	h.tapName("row-0")
	h.waitTag("reader-first", 1)
}

func TestBorderMatchPaintsTheMarginOfABlackPage(t *testing.T) {
	for _, tc := range []struct {
		border string
		want   uint8
	}{{"match", 0}, {"white", 255}} {
		h := startOpts(t, hopts{mut: func(c *store.Config) { c.Border = tc.border; c.Crop = "keep" }})
		h.openMangaB("row-8") // Black Pages: 900x1350 -> 1440x2160, 90 px margins left and right
		h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page 0")
		h.waitFor(func() bool { return h.pixelAt(30, 1000) == tc.want }, "margin colour "+tc.border)
		if v := h.pixelAt(1590, 1000); v != tc.want {
			t.Errorf("border=%s: right margin %d, want %d", tc.border, v, tc.want)
		}
		if v := h.pixelAt(810, 1080); v > 60 && tc.border == "match" {
			t.Errorf("the page itself is not black: %d", v)
		}
		h.stop()
	}
}

func fitted(level uint8, ok bool) *imgproc.Fitted {
	return &imgproc.Fitted{W: 1440, H: 2160, Gray: make([]uint8, 4), Border: level, BorderOK: ok}
}

// A chapter that moves from a black page to a white one flips the border and
// counts the flip toward the next clearing refresh; an ambiguous page keeps it.
func TestBorderFlipCountsTowardTheClear(t *testing.T) {
	a := &App{Cfg: store.DefaultConfig()}
	a.Cfg.Border = "match"
	a.W, a.H = 1620, 2160
	r := &reader{border: paperGray, borderPage: -1}
	step := func(page int, level uint8, ok bool) uint8 {
		r.page = page
		return a.borderFor(r, []piece{{f: fitted(level, ok)}})
	}
	if v := step(0, 0, true); v != 0 || a.sinceFull != 1 {
		t.Errorf("white -> black: border %d, sinceFull %d", v, a.sinceFull)
	}
	if v := step(0, 0, true); v != 0 || a.sinceFull != 1 {
		t.Errorf("same page again counted: %d %d", v, a.sinceFull)
	}
	if v := step(1, 0, false); v != 0 {
		t.Errorf("ambiguous page changed the border to %d", v)
	}
	if v := step(2, 255, true); v != 255 || a.sinceFull != 2 {
		t.Errorf("black -> white: %d %d", v, a.sinceFull)
	}
	if v := step(3, 216, true); v != 216 || a.sinceFull != 2 {
		t.Errorf("white -> toned: %d %d (a 39-level step is not a flip)", v, a.sinceFull)
	}
	a.Cfg.Border = "white"
	if v := step(4, 0, true); v != 255 {
		t.Errorf("border=white gave %d", v)
	}
}

func TestCropTrimMakesTheFittedPageNarrower(t *testing.T) {
	size := func(crop string) (int, int) {
		h := startOpts(t, hopts{mut: func(c *store.Config) { c.Crop = crop }})
		defer h.stop()
		h.openMangaB("row-9") // Wide Margins: 15 % left and right
		ch := make(chan [2]int, 1)
		h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page 0")
		h.app.post(func() {
			f := h.app.rd.ready(0)
			ch <- [2]int{f.W, f.H}
		})
		wh := <-ch
		return wh[0], wh[1]
	}
	kw, kh := size("keep")
	tw, th := size("trim")
	if kw != 1420 || kh != 2130 {
		t.Errorf("keep: %dx%d, want 1420x2130 (the screen less the footer)", kw, kh)
	}
	// trimmed: each side loses its margin (up to the 10 % clamp), so the fitted page is narrower
	if tw >= kw-100 || th != 2130 {
		t.Errorf("trim: %dx%d, want a page narrower than %d and 2130 high", tw, th, kw)
	}
}

func TestAdvancedScreenMirrorsTheConfig(t *testing.T) {
	h := startHarness(t, nil, "")
	h.waitTag("list", 1)
	h.tapName("settings")
	h.waitScreen("settings")
	h.settingsScrollTo("adv-open")
	h.tapName("adv-open")
	h.waitFor(func() bool { _, ok := h.hitRect("adv-page_mode-ui"); return ok }, "advanced screen")
	have := h.settingsHits()
	for _, name := range []string{"adv-page_mode-auto", "adv-page_mode-fast", "adv-page_mode-content", "adv-ui_mode-fast", "adv-clear_style-ui_full",
		"adv-bars_clean-never", "adv-fb_format-rgb565", "adv-fb_format-rgba", "adv-border-match", "adv-crop-trim",
		"adv-read_ahead-plus", "adv-read_ahead-minus", "adv-keep_behind-plus", "adv-keep_behind-minus", "adv-cache_mb-plus", "adv-zone_middle_pct-plus"} {
		if !have[name] {
			t.Errorf("Advanced lacks %q", name)
		}
	}
	h.settingsScrollTop()
	h.tapName("adv-page_mode-ui")
	h.waitFor(func() bool { return strings.Contains(h.readConf(), "page_mode = ui") }, "page_mode written to the config")
	conf := h.readConf()
	if !strings.Contains(conf, h.mock.Key()) { // the sign-in is untouched
		t.Errorf("the rewrite lost the sign-in:\n%s", strings.ReplaceAll(conf, h.mock.Key(), "<key>"))
	}
	// a second change of the same key replaces the line, never duplicates it
	h.tapName("adv-page_mode-fast")
	h.waitFor(func() bool { return strings.Contains(h.readConf(), "page_mode = fast") }, "second write")
	if c := strings.Count(h.readConf(), "page_mode"); c != 1 {
		t.Errorf("page_mode appears %d times:\n%s", c, h.readConf())
	}
	// the memory steppers write numbers and apply at once
	h.settingsScrollTo("adv-read_ahead-plus")
	h.tapName("adv-read_ahead-plus")
	h.waitFor(func() bool { return strings.Contains(h.readConf(), "read_ahead = 4") }, "read_ahead 4 written")
	h.tapName("adv-keep_behind-minus")
	h.waitFor(func() bool { return strings.Contains(h.readConf(), "keep_behind = 1") }, "keep_behind 1 written")
	h.stop()
	// the choices are there after a restart
	h.launch()
	h.waitTag("list", 1)
	ch := make(chan [3]string, 1)
	h.app.post(func() {
		ch <- [3]string{h.app.Cfg.PageMode, strconv.Itoa(h.app.Cfg.ReadAhead), strconv.Itoa(h.app.Cfg.KeepBehind)}
	})
	if got := <-ch; got != [3]string{"fast", "4", "1"} {
		t.Errorf("after restart: page_mode %q read_ahead %s keep_behind %s", got[0], got[1], got[2])
	}
	h.stop()
}

func TestMemoryGuardShrinksTheWindowAndTheFloorStopsDecoding(t *testing.T) {
	var avail atomic.Int64
	avail.Store(150) // below the guard (200), above the floor (100)
	h := startOpts(t, hopts{
		mut:    func(c *store.Config) { c.ReadAhead = 3; c.KeepBehind = 2; c.Warmup = false },
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever },
		app:    func(a *App) { a.memAvail = func() int { return int(avail.Load()) } },
	})
	h.openFirstChapter()
	h.waitReady(0)
	time.Sleep(150 * time.Millisecond)
	if n := len(h.imageRequests()); n != 2 {
		t.Fatalf("%d page requests under the guard, want the current page and one ahead", n)
	}
	h.tap(300, 1000)
	h.waitReady(1)
	h.waitFor(func() bool { return len(h.imageRequests()) == 3 }, "one ahead again")
	avail.Store(400) // memory is back: the full read-ahead resumes
	h.tap(300, 1000)
	h.waitReady(2)
	h.waitFor(func() bool { return len(h.imageRequests()) >= 6 }, "read-ahead resumes")
	lg := h.readLog()
	if !strings.Contains(lg, "memory guard: 150 MB available") || !strings.Contains(lg, "memory guard: 400 MB available again") {
		t.Errorf("the guard transitions were not logged once each:\n%s", tail(lg, 800))
	}
	h.stop()

	// below the floor nothing new is decoded, not even the page on screen: the
	// error state with Retry shows instead, and Retry works once memory is back
	avail.Store(60)
	h2 := startOpts(t, hopts{
		mut:    func(c *store.Config) { c.Warmup = false },
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever },
		app:    func(a *App) { a.memAvail = func() int { return int(avail.Load()) } },
	})
	h2.waitTag("list", 1)
	h2.tapName("row-5")
	h2.waitTag("list", 2)
	h2.tapName("row-0")
	h2.waitTag("list", 3)
	h2.tapName("row-0")
	h2.waitTag("list", 4)
	h2.tapName("row-0")
	h2.waitFor(func() bool { _, ok := h2.hitRect("retry"); return ok }, "the error state with Retry")
	if n := len(h2.imageRequests()); n != 0 {
		t.Errorf("%d page requests below the floor, want none", n)
	}
	avail.Store(500)
	h2.tapName("retry")
	h2.waitFor(func() bool { s := h2.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page 0 after Retry")
	if lg := h2.readLog(); !strings.Contains(lg, "memory floor: 60 MB available") {
		t.Errorf("the floor was not logged:\n%s", tail(lg, 800))
	}
	h2.stop()
}

func TestNextChapterWarmUpIsRequestedOnceAndUsed(t *testing.T) {
	h := startOpts(t, hopts{
		mut:    func(c *store.Config) { c.ReadAhead = 2; c.KeepBehind = 0; c.SyncProgress = false },
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever },
	})
	h.openFirstChapter()
	next := func() []string {
		var out []string
		for _, r := range h.imageRequests() {
			if strings.Contains(r, "chapter=2 ") {
				out = append(out, r)
			}
		}
		return out
	}
	// pages 0..8: no request for chapter 2 yet (the warm-up starts at count-3 = page 9)
	for i := 1; i <= 8; i++ {
		h.tap(300, 1000)
		h.waitReady(i)
	}
	if n := len(next()); n != 0 {
		t.Fatalf("chapter 2 requested too early: %v", next())
	}
	h.tap(300, 1000)
	h.waitReady(9)
	h.waitFor(func() bool { return len(next()) == 1 }, "the warm-up request")
	for i := 10; i <= 11; i++ {
		h.tap(300, 1000)
		h.waitReady(i)
	}
	h.tap(300, 1000) // past the end: chapter 2 opens
	h.waitTag("reader-first", 2)
	h.waitReady(0)
	time.Sleep(100 * time.Millisecond)
	zero := 0
	for _, r := range next() {
		if strings.Contains(r, "page=0 ") {
			zero++
		}
	}
	if zero != 1 {
		t.Errorf("page 0 of chapter 2 was requested %d times: %v", zero, next())
	}
	if !strings.Contains(h.readLog(), "warm page used for chapter 2") {
		t.Errorf("the warm page was not used:\n%s", h.readLog())
	}
	h.stop()
}

func TestWarmUpOffAndCancelledWhenTheReaderLeaves(t *testing.T) {
	h := startOpts(t, hopts{
		mut:    func(c *store.Config) { c.Warmup = false },
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever },
	})
	h.openFirstChapter()
	for i := 1; i <= 10; i++ {
		h.tap(300, 1000)
		h.waitReady(i)
	}
	time.Sleep(100 * time.Millisecond)
	for _, r := range h.imageRequests() {
		if strings.Contains(r, "chapter=2 ") {
			t.Fatalf("warm-up requested although it is off: %s", r)
		}
	}
	h.stop()

	// on, with a slow server: leave the reader while the page is in flight
	h = startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	h.openFirstChapter()
	for i := 1; i <= 8; i++ {
		h.tap(300, 1000)
		h.waitReady(i)
	}
	h.mock.SetDelay(400 * time.Millisecond)
	h.tap(300, 1000)
	h.waitReady(9) // the warm-up starts here and takes 400 ms
	h.tap(810, 300)
	h.waitTag("overlay-top", 1)
	h.tapName("home")
	h.waitFor(func() bool { return h.snapshot().Screen == "list" }, "home")
	h.mock.SetDelay(0)
	count := func() int {
		n := 0
		for _, r := range h.imageRequests() {
			if strings.Contains(r, "chapter=2 ") {
				n++
			}
		}
		return n
	}
	n := count()
	time.Sleep(700 * time.Millisecond)
	// the one request that was already on its way may still reach the server; no second one follows
	if m := count(); m > 1 {
		t.Errorf("chapter 2 requests: %d then %d", n, m)
	}
	if s := h.snapshot(); s.Screen != "list" {
		t.Errorf("a late warm page changed the screen: %s", s.Screen)
	}
	h.stop()
}

func TestTelemetryLineKindsAndNoSecrets(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Refresh = store.RefreshCustom1; s.Custom[0] = 3 }})
	h.openFirstChapter()
	for i := 1; i <= 3; i++ {
		h.tap(300, 1000)
		h.waitReady(i)
	}
	h.tap(810, 300)
	h.waitTag("overlay-bottom", 1)
	h.tap(300, 1000)
	h.waitTag("overlay-hide-bottom", 1)
	h.stop()
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(h.conf), "app.log"))
	lg := string(b)
	for _, want := range []string{"kind=list", "kind=turn", "kind=clear", "kind=overlay", "region=0,0,1620,2160", " flips=", " fulls=", " t="} {
		if !strings.Contains(lg, want) {
			t.Errorf("telemetry lacks %q:\n%s", want, tail(lg, 900))
		}
	}
	if strings.Contains(lg, h.mock.Key()) || strings.Contains(lg, "http://") {
		t.Errorf("telemetry holds a key or URL:\n%s", tail(lg, 900))
	}
}

// A scan of a black and white page with a paper tone (a faint cast) is grey: with
// colour on it is sent without the colour layer and turns with UI like every page (page_mode = auto), without colour.
func TestTintedBlackAndWhiteScanIsTreatedAsGrey(t *testing.T) {
	h := startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Colour = true; s.Refresh = store.RefreshNever }})
	h.openMangaB("row-10") // Tinted Scans
	h.waitReady(0)
	for _, f := range h.disp.all() {
		if f.tag == "reader-first" && f.colour {
			t.Errorf("a faint cast was treated as colour: %+v", f)
		}
	}
	h.tap(300, 1000)
	h.waitTag("page", 1)
	for _, f := range h.disp.all() {
		if f.tag == "page" && (f.colour || f.mode != display.ModeUI) {
			t.Errorf("turn on a tinted b/w page = %+v, want a UI update without colour", f)
		}
	}
	h.stop()
}

// border = match applies to comics only: a PDF page (text on paper) keeps the
// white strip.
func TestBorderMatchLeavesPDFWhite(t *testing.T) {
	h := startOpts(t, hopts{mut: func(c *store.Config) { c.Border = "match"; c.Crop = "keep" }})
	h.openMangaB("row-7") // Text PDF
	h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "pdf page 0")
	if v := h.pixelAt(3, 1000); v != 255 {
		t.Errorf("pdf: left edge %d, want white", v)
	}
	h.stop()
}
