package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/imgproc"
	"rmkavita/internal/store"
)

// ---- footer: the matrix ---------------------------------------------------------------------

// checkFooter looks at the strip the simulated panel shows below the page: it
// must have a background and text whose contrast is at least 4.5:1, and the
// background must be what the border setting says.
func (h *harness) checkFooter(t *testing.T, what string, wantDark *bool, border string) {
	t.Helper()
	var vh, H int
	h.onUI(func() { vh, H = h.app.vh(), h.app.H })
	time.Sleep(60 * time.Millisecond)
	// below the rule and the progress line: where the text is
	bg, lo, hi := h.stripStats(0, vh+10, 1620, H)
	ink := lo
	if bg < 128 {
		ink = hi
	}
	if c := contrastRatio(bg, ink); c < 4.5 {
		t.Errorf("%s: no readable footer: background %d, text %d (contrast %.2f, need 4.5)", what, bg, ink, c)
	}
	if border == "white" && bg != 255 {
		t.Errorf("%s: border=white but the strip background is %d", what, bg)
	}
	if wantDark != nil {
		if *wantDark && bg > 60 {
			t.Errorf("%s: a black page with border=match should have a dark strip, got %d", what, bg)
		}
		if !*wantDark && bg < 100 {
			t.Errorf("%s: a light page should not have a dark strip, got %d", what, bg)
		}
	}
}

func TestFooterContrastIsAtLeastFourPointFiveForEveryBackground(t *testing.T) {
	worst := 100.0
	for v := 0; v < 256; v++ {
		bg := uint8(v)
		if c := contrastRatio(bg, inkOn(bg)); c < worst {
			worst = c
		}
	}
	if worst < 4.5 {
		t.Errorf("the worst background gets %.2f:1", worst)
	}
	if inkOn(0) != 255 || inkOn(255) != 0 {
		t.Error("black needs white text and white needs black")
	}
}

func TestFooterMatrixStaysVisible(t *testing.T) {
	if testing.Short() {
		t.Skip("24 combinations")
	}
	type pg struct {
		name   string
		colour bool
		dark   *bool // what a border=match strip should be: nil = no assertion
		open   func(h *harness)
	}
	yes, no := true, false
	byRow := func(row string) func(h *harness) { return func(h *harness) { h.openMangaB(row) } }
	pages := []pg{
		{"white page", false, &no, func(h *harness) { h.openFirstChapter() }},
		{"black page", false, &yes, byRow("row-8")},
		{"grey page", false, nil, byRow("row-12")},
		{"white colour page", true, &no, byRow("row-0")},
		{"black colour page", true, &yes, byRow("row-13")},
		{"grey colour page", true, nil, byRow("row-14")},
	}
	for _, border := range []string{"match", "white"} {
		for _, crop := range []string{"keep", "trim"} {
			for _, p := range pages {
				name := fmt.Sprintf("%s/%s/%s", border, crop, p.name)
				border, crop, p := border, crop, p
				t.Run(name, func(t *testing.T) {
					h := startOpts(t, hopts{
						mut: func(c *store.Config) { c.Border = border; c.Crop = crop; c.HoldMs = 400 },
						mutSet: func(s *store.Settings) {
							s.Colour = p.colour
							s.Refresh = store.RefreshNever
							s.ColourRefresh = store.ColourNever
						}})
					p.open(h)
					h.waitFor(func() bool {
						s := h.snapshot()
						return s.Screen == "reader" && contains(s.ReadyPages, 0) && s.Tag != "reader-loading"
					}, "page")
					wantDark := p.dark
					if border == "white" {
						wantDark = nil
					}
					h.checkFooter(t, name+" first view", wantDark, border)
					if p.colour {
						var layer bool
						h.onUI(func() { layer = h.app.layerActive })
						if !layer {
							t.Fatalf("%s: a colour page without the colour layer: the test does not test what it should", name)
						}
					}

					// the page is turned: still there, in one update
					h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, 1) }, "page 1 ready")
					before := len(h.disp.all())
					h.tap(300, 1000)
					h.waitFor(func() bool { return h.snapshot().Page == 1 }, "turn")
					time.Sleep(60 * time.Millisecond)
					if n := len(h.disp.all()) - before; n != 1 {
						t.Errorf("%s: %d updates for one turn, want 1 (the footer is part of the page's update)", name, n)
					}
					h.checkFooter(t, name+" after a turn", wantDark, border)

					// the bars open and close
					h.tap(810, 1000)
					h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
					h.tap(810, 1000)
					h.waitFor(func() bool { return !h.snapshot().Overlay }, "bars closed")
					h.checkFooter(t, name+" after the bars closed", wantDark, border)

					// a settings round trip
					h.tap(810, 1000)
					h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
					h.tapName("settings")
					h.waitScreen("settings")
					h.tapName("back")
					h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, s.Page) }, "reader again")
					h.checkFooter(t, name+" after a settings round trip", wantDark, border)

					// a clearing refresh (the top-right corner is Clear by default)
					n := h.disp.count("clear")
					h.hold(1560, 60, 700*time.Millisecond)
					h.waitFor(func() bool { return h.disp.count("clear") > n }, "clear")
					h.checkFooter(t, name+" after a clear", wantDark, border)
					h.stop()
				})
			}
		}
	}
}

// ---- crop = trim is the default ------------------------------------------------------------------

func TestTrimIsTheDefaultAndAnExplicitKeepStays(t *testing.T) {
	d := store.DefaultConfig()
	if d.Crop != "trim" {
		t.Errorf("default crop = %q, want trim", d.Crop)
	}
	if c := store.ParseConfig("server = http://x\n"); c.Crop != "trim" {
		t.Errorf("an absent crop key gives %q", c.Crop)
	}
	if c := store.ParseConfig("crop = keep\n"); c.Crop != "keep" {
		t.Errorf("an explicit keep gives %q", c.Crop)
	}
	if c := store.ParseConfig("crop_margins = no\n"); c.Crop != "keep" {
		t.Errorf("the v2 key crop_margins = no gives %q (an explicit choice)", c.Crop)
	}
}

// ---- the top bar is solid from y = 0 ------------------------------------------------------------

// The 100 px above the bar belong to AppLoad (no button lives there) but the
// pixels are ours: with the bars open they are the bar's colour, not a strip of
// the page. And every pixel of both bars on the panel is the app's own bar
// pixel: no page pixel survives inside the bar rectangles.
func TestBarsAreSolidFromTheTopAndLeaveNoPagePixel(t *testing.T) {
	pages := []struct {
		name   string
		colour bool
		open   func(h *harness)
	}{
		{"black page", false, func(h *harness) { h.openMangaB("row-8") }},
		{"busy comic page", false, func(h *harness) { h.openFirstChapter() }},
		{"grey page", false, func(h *harness) { h.openMangaB("row-12") }},
		{"black colour page", true, func(h *harness) { h.openMangaB("row-13") }},
		{"busy colour page", true, func(h *harness) { h.openMangaB("row-0") }},
	}
	for _, bars := range []string{"auto", "always", "never"} {
		for _, p := range pages {
			p, bars := p, bars
			t.Run(bars+"/"+p.name, func(t *testing.T) {
				h := startOpts(t, hopts{mut: func(c *store.Config) { c.BarsClean = bars },
					mutSet: func(s *store.Settings) {
						s.Colour = p.colour
						s.Refresh = store.RefreshNever
						s.ColourRefresh = store.ColourNever
					}})
				p.open(h)
				h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page")
				h.tap(810, 1000)
				h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
				time.Sleep(60 * time.Millisecond)
				var top, bottom image.Rectangle
				var diff, white int
				var cvPix func(x, y int) uint8
				h.onUI(func() {
					a := h.app
					top, bottom = a.overlayRects()
					cvPix = func(x, y int) uint8 { return a.cv.Gray.Pix[y*a.cv.Gray.Stride+x] }
					for _, rc := range []image.Rectangle{top, bottom} {
						for y := rc.Min.Y; y < rc.Max.Y; y++ {
							for x := rc.Min.X; x < rc.Max.X; x++ {
								if h.disp.fb[y*a.W+x] != a.cv.Gray.Pix[y*a.cv.Gray.Stride+x] {
									diff++
								}
							}
						}
					}
					for y := 0; y < 100; y++ {
						for x := 0; x < a.W; x += 7 {
							if h.disp.fb[y*a.W+x] == 255 {
								white++
							}
						}
					}
				})
				if top.Min.Y != 0 || bottom.Max.Y != 2160 {
					t.Fatalf("bars cover y %d..%d and %d..%d: they must reach the top and the bottom of the screen", top.Min.Y, top.Max.Y, bottom.Min.Y, bottom.Max.Y)
				}
				if diff != 0 {
					t.Errorf("%d pixels of the bars on the panel differ from the bars the app drew: the page shows through", diff)
				}
				if want := 100 * ((1620 + 6) / 7); white != want {
					t.Errorf("only %d of %d sampled pixels of the strip above the bar are white", white, want)
				}
				if cvPix(810, 2159) != 255 || cvPix(5, 2159) != 255 {
					t.Error("the bottom row of the screen is not the bar's colour")
				}
				h.stop()
			})
		}
	}
}

// bars_clean: auto = a white pre-fill in the page waveform when the page under the
// bars is not white; never = the page waveform only; always = the pre-fill and
// CONTENT. The old chrome_clean key still reads.
func TestBarsCleanModes(t *testing.T) {
	type res struct {
		prefill bool
		mode    display.Mode
	}
	run := func(bars string, open func(h *harness)) res {
		h := startOpts(t, hopts{mut: func(c *store.Config) { c.BarsClean = bars },
			mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
		defer h.stop()
		open(h)
		h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page")
		h.tap(810, 1000)
		h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
		var out res
		for _, f := range h.disp.all() {
			if f.tag == "overlay-prefill" {
				out.prefill = true
			}
			if f.tag == "overlay-top" {
				out.mode = f.mode
			}
		}
		return out
	}
	dark := func(h *harness) { h.openMangaB("row-8") }
	busy := func(h *harness) { h.openFirstChapter() }
	for _, tc := range []struct {
		name string
		bars string
		open func(h *harness)
		want res
	}{
		{"auto over black", "auto", dark, res{true, display.ModeUI}},
		{"auto over a busy page", "auto", busy, res{true, display.ModeUI}},
		{"never over black", "never", dark, res{false, display.ModeUI}},
		{"always over black", "always", dark, res{true, display.ModeContent}},
		{"always over a busy page", "always", busy, res{true, display.ModeContent}},
	} {
		if got := run(tc.bars, tc.open); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
	for _, tc := range []struct{ line, want string }{
		{"bars_clean = always", "always"}, {"bars_clean = NEVER", "never"}, {"chrome_clean = no", "never"},
		{"chrome_clean = yes", "auto"}, {"", "never"},
	} {
		if got := store.ParseConfig(tc.line + "\n").BarsClean; got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.line, got, tc.want)
		}
	}
	if c := store.ParseConfig("bars_clean = sometimes\n"); c.BarsClean != "never" || len(c.Unknown) != 1 {
		t.Errorf("a bad value: %+v", c)
	}
}

// ---- render modes -------------------------------------------------------------------------------

// Over 50 turns with a clear every 20: what each render mode sends to the
// panel. quality: CONTENT is armed once, a clear is the update and the full
// request; balanced: UI turns, a clear is CONTENT + update + full + UI again;
// speed: turns in the page_mode waveform (UI by default), no mode message on any
// of them, a clear as the style says. The clear style and timing of each row is
// the old one (content_full inside the turn) unless the row says ui_full: the
// new default is covered by TestUIFullClearSequence and the deferral tests.
func TestRenderModeMessageSequences(t *testing.T) {
	for _, tc := range []struct {
		name      string
		render    string
		pageMode  string
		style     string
		turn      string
		clear     string
		turnState string
	}{
		{"balanced", "balanced", "", "content_full", "update", "mode3,update,full,mode4", "ui"},
		{"quality", "quality", "", "content_full", "update", "update,full", "content"},
		{"speed", "speed", "", "content_full", "update", "mode3,update,full,mode4", "ui"},
		{"speed-fast", "speed", "fast", "content_full", "update", "mode3,update,full,mode1", "fast"},
		{"speed-animate", "speed", "animate", "content_full", "update", "mode3,update,full,mode2", "animate"},
		{"balanced-ui_full", "balanced", "", "ui_full", "update", "update,full", "ui"},
		{"speed-ui_full", "speed", "", "ui_full", "update", "update,full", "ui"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, rigOpts{pages: 60, cfg: func(c *store.Config) {
				c.Render = tc.render
				c.ClearStyle = tc.style
				if tc.pageMode != "" {
					c.PageMode = tc.pageMode
				}
			}})
			r.openFirst()
			flips := 0
			for turn := 1; turn <= 50; turn++ {
				r.wait(func(s Snapshot) bool {
					return s.Screen == "reader" && s.Page == turn-1 && contains(s.ReadyPages, turn)
				}, fmt.Sprintf("page %d ready", turn))
				before := len(r.conn.all())
				r.touch(300, 1000)
				r.wait(func(s Snapshot) bool {
					return s.Screen == "reader" && s.Page == turn && (s.Tag == "page" || s.Tag == "page-full")
				}, fmt.Sprintf("turn %d shown", turn))
				time.Sleep(12 * time.Millisecond)
				got := kinds(r.conn.all()[before:])
				want := tc.turn
				if turn%20 == 0 {
					want = tc.clear
				}
				if got != want {
					t.Fatalf("%s turn %d sent %q, want %q", tc.render, turn, got, want)
				}
				if got == tc.turn && strings.Contains(got, "mode") {
					flips++
				}
			}
			if flips != 0 {
				t.Errorf("%d turns changed the refresh mode", flips)
			}
			r.stop()
			lg, _ := os.ReadFile(filepath.Join(r.dir, "app.log"))
			if !strings.Contains(string(lg), "kind=turn mode="+tc.turnState+" ") {
				t.Errorf("%s: no turn update in mode %s in the log:\n%s", tc.render, tc.turnState, tail(string(lg), 500))
			}
		})
	}
}

// Speed mode: colour pages are shown in grey (no colour layer, no chroma
// decode), every grey on the page is one of the levels, and the page is
// prepared ahead (the turn itself sends the already quantised cache entry).
func TestSpeedModeRendersQuantisedGreyAndDropsColour(t *testing.T) {
	for _, levels := range []int{2, 4, 16} {
		levels := levels
		t.Run(fmt.Sprintf("%d levels", levels), func(t *testing.T) {
			h := startOpts(t, hopts{
				mut: func(c *store.Config) { c.Render = "speed"; c.SpeedLevels = levels; c.Border = "white" },
				mutSet: func(s *store.Settings) {
					s.Colour = true
					s.Refresh = store.RefreshNever
					s.ColourRefresh = store.ColourEvery3
				}})
			h.openMangaB("row-0") // colour pages
			h.waitFor(func() bool {
				s := h.snapshot()
				return s.Screen == "reader" && contains(s.ReadyPages, 0) && contains(s.ReadyPages, 1)
			}, "pages 0 and 1")
			set := map[uint8]bool{}
			for i := 0; i < levels; i++ {
				set[imgproc.LevelValue(i, levels)] = true
			}
			var bad, total int
			var layer bool
			h.onUI(func() {
				a := h.app
				layer = a.layerActive
				for _, n := range []int{0, 1} {
					f := a.rd.ready(n)
					if f.Col != nil {
						bad += 1 << 20
					}
					for _, v := range f.Gray {
						total++
						if !set[v] {
							bad++
						}
					}
				}
			})
			if layer {
				t.Error("a colour layer is active in speed mode")
			}
			if bad != 0 {
				t.Errorf("%d of %d cached grey values are not one of the %d levels (or a page still has chroma)", bad, total, levels)
			}
			h.tap(300, 1000)
			h.waitTag("page", 1)
			for _, f := range h.disp.all() {
				if f.tag == "page" && (f.mode != display.ModeUI || f.colour || f.full) {
					t.Errorf("speed turn %+v: want one partial UI update without colour", f)
				}
			}
			// the colour interval does not apply to grey pages: three turns, no clear
			for i := 2; i <= 4; i++ {
				h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, i) }, "ready")
				h.tap(300, 1000)
				h.waitFor(func() bool { return h.snapshot().Page == i }, "turn")
			}
			if n := h.disp.count("page-full"); n != 0 {
				t.Errorf("%d clears within 4 turns: the colour interval was applied to grey pages", n)
			}
			h.stop()
		})
	}
}

// The telemetry line carries the latency fields: ready_ms (finger up -> frame
// handed to the display), cpu_ms and net_ms (what a page that had to be waited
// for cost), show_ms and bytes.
func TestTelemetryHasTheLatencyFields(t *testing.T) {
	h := startOpts(t, hopts{
		mut:    func(c *store.Config) { c.ReadAhead = 0; c.KeepBehind = 0; c.Warmup = false },
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	h.openFirstChapter()
	h.waitReady(0)
	h.mock.SetDelay(180 * time.Millisecond)
	h.tap(300, 1000) // the next page is not there: it is fetched, decoded and shown
	h.waitFor(func() bool {
		return h.snapshot().Page == 1 && contains(h.snapshot().ReadyPages, 1) && h.disp.count("page") >= 1
	}, "turn")
	h.mock.SetDelay(0)
	h.stop()
	lg := h.readLog()
	re := regexp.MustCompile(`tel n=\d+ t=\d+ kind=turn mode=\w+ region=(\d+),(\d+),(\d+),(\d+) full=0 flips=\d+ fulls=\d+ tag=page ready_ms=(-?\d+) cpu_ms=(\d+) net_ms=(\d+) show_ms=(\d+) bytes=(\d+)`)
	m := re.FindStringSubmatch(lg)
	if m == nil {
		t.Fatalf("no turn line with the latency fields:\n%s", tail(lg, 900))
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	w, hh := atoi(m[3]), atoi(m[4])
	ready, cpu, net, bytes := atoi(m[5]), atoi(m[6]), atoi(m[7]), atoi(m[9])
	if ready < 150 {
		t.Errorf("ready_ms=%d: the page was waited for (the mock took 180 ms)", ready)
	}
	if net < 150 {
		t.Errorf("net_ms=%d, want >= 150", net)
	}
	if cpu <= 0 || cpu > ready {
		t.Errorf("cpu_ms=%d (ready_ms %d)", cpu, ready)
	}
	if bytes != w*hh*4 {
		t.Errorf("bytes=%d, want %d x %d x 4", bytes, w, hh)
	}
	if strings.Contains(lg, h.mock.Key()) || strings.Contains(lg, "http://") {
		t.Error("the telemetry carries a key or a URL")
	}
}

// With the new default (crop = trim) and border = match, the margin beside a
// black page is the page's own black: no seam, no white strip.
func TestTrimMatchMarginIsThePagesOwnBlack(t *testing.T) {
	h := startOpts(t, hopts{mut: func(c *store.Config) { c.Border = "match"; c.Crop = "trim" }})
	h.openMangaB("row-8") // Black Pages
	h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page 0")
	page := int(h.pixelAt(810, 1000))
	for _, x := range []int{3, 30, 1590, 1617} {
		if v := int(h.pixelAt(x, 1000)); v > page+3 {
			t.Errorf("margin x=%d is %d, the page's black is %d", x, v, page)
		}
	}
	h.stop()
}

// Speed mode: the footer is made of the page's levels only and stays readable,
// the bars open and close in the turn waveform (no mode message, no CONTENT
// repaint, no white pre-fill) and the border is one of the levels.
func TestSpeedModeFooterBarsAndBorderStayInTheSpeedWaveform(t *testing.T) {
	for _, levels := range []int{2, 4} {
		levels := levels
		t.Run(fmt.Sprintf("%d levels", levels), func(t *testing.T) {
			h := startOpts(t, hopts{
				mut: func(c *store.Config) {
					c.Render = "speed"
					c.SpeedLevels = levels
					c.Border = "match"
					c.Crop = "keep"
				},
				mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
			h.openMangaB("row-8") // Black Pages: 90 px margins beside the page
			h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page 0")
			set := map[uint8]bool{}
			for i := 0; i < levels; i++ {
				set[imgproc.LevelValue(i, levels)] = true
			}
			var lo, hi uint8 = 255, 0
			var off, count int
			margin := h.pixelAt(30, 1000)
			h.onUI(func() {
				g := h.app.cv.Gray
				st := h.app.statusRect()
				for y := st.Min.Y; y < st.Max.Y; y++ {
					for x := 0; x < h.app.W; x++ {
						v := g.Pix[y*g.Stride+x]
						count++
						if !set[v] {
							off++
						}
						if v < lo {
							lo = v
						}
						if v > hi {
							hi = v
						}
					}
				}
			})
			if off != 0 {
				t.Errorf("%d of %d footer pixels are not one of the %d levels", off, count, levels)
			}
			if c := contrastRatio(lo, hi); c < 4.5 {
				t.Errorf("footer: darkest %d vs lightest %d = contrast %.2f", lo, hi, c)
			}
			if !set[margin] {
				t.Errorf("the matched border is %d, not one of the levels", margin)
			}
			// the bars open and close without a mode message
			before := len(h.disp.all())
			h.tap(810, 1000) // the middle column
			h.waitFor(func() bool { return h.snapshot().Overlay }, "bars open")
			h.tap(810, 1000)
			h.waitFor(func() bool { return !h.snapshot().Overlay }, "bars closed")
			for _, f := range h.disp.all()[before:] {
				if strings.HasPrefix(f.tag, "overlay") && f.mode != display.ModeUI {
					t.Errorf("%s was sent in mode %v, want the speed waveform", f.tag, f.mode)
				}
			}
			h.stop()
		})
	}
}

// ---- clearing refreshes: ui_full, deferred to a pause, counted in the log --------------------------

func TestClearDefaults(t *testing.T) {
	d := store.DefaultConfig()
	if d.ClearStyle != "ui_full" || d.ClearIdleMS != 600 || d.BarsClean != "never" {
		t.Errorf("defaults: clear_style=%s clear_idle_ms=%d bars_clean=%s", d.ClearStyle, d.ClearIdleMS, d.BarsClean)
	}
	if c := store.ParseConfig("clear_idle_ms = 99999\n"); c.ClearIdleMS != 5000 {
		t.Errorf("clear_idle_ms is not clamped: %d", c.ClearIdleMS)
	}
	if c := store.ParseConfig("clear_idle_ms = soon\n"); c.ClearIdleMS != 600 || len(c.Unknown) != 1 {
		t.Errorf("a bad value: %+v", c)
	}
	if c := store.ParseConfig("page_mode = animate\n"); c.PageMode != "animate" {
		t.Errorf("page_mode = animate parsed as %q", c.PageMode)
	}
}

var sleepsRe = regexp.MustCompile(`tag=(\S+) .*? sleeps=(\d+)`)

// A clearing refresh with ui_full is one full-refresh request (one sleep of the
// panel server); with content_full it is a mode message and the request, and
// the re-arm after it is a third one. The log says so per update.
func TestTelSleepsFieldCountsTheServersSecondsOfSleep(t *testing.T) {
	for _, tc := range []struct {
		style string
		want  int
	}{{"ui_full", 1}, {"content_full", 2}} {
		r := newRig(t, rigOpts{pages: 30, cfg: func(c *store.Config) { c.ClearStyle = tc.style; c.ClearIdleMS = 0 }})
		r.openFirst()
		for turn := 1; turn <= 20; turn++ {
			r.wait(func(s Snapshot) bool {
				return s.Screen == "reader" && s.Page == turn-1 && contains(s.ReadyPages, turn)
			}, fmt.Sprintf("page %d ready", turn))
			r.touch(300, 1000)
			r.wait(func(s Snapshot) bool {
				return s.Screen == "reader" && s.Page == turn && (s.Tag == "page" || s.Tag == "page-full")
			}, fmt.Sprintf("turn %d shown", turn))
			time.Sleep(12 * time.Millisecond)
		}
		r.stop()
		lg, _ := os.ReadFile(filepath.Join(r.dir, "app.log"))
		got := -1
		for _, m := range sleepsRe.FindAllStringSubmatch(string(lg), -1) {
			if m[1] == "page-full" {
				got, _ = strconv.Atoi(m[2])
			}
		}
		if got != tc.want {
			t.Errorf("%s: the clearing update's line says sleeps=%d, want %d", tc.style, got, tc.want)
		}
	}
}

// With clear_idle_ms the turn that makes a clear due is an ordinary page update
// and the clear runs after the reader pauses; turning on cancels it, and at
// twice the interval it happens inside the turn regardless.
func TestClearIsDeferredToAPauseAndForcedAtTwiceTheInterval(t *testing.T) {
	h := startOpts(t, hopts{
		pages:  40,
		mut:    func(c *store.Config) { c.ClearStyle = "ui_full"; c.ClearIdleMS = 400 },
		mutSet: func(s *store.Settings) { s.Refresh, s.Custom[0] = store.RefreshCustom1, 4 }})
	h.openFirstChapter()
	h.waitReady(0)
	turn := func(p int) {
		h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, p) }, "ready")
		h.tap(300, 1000)
		h.waitFor(func() bool { return h.snapshot().Page == p }, "turn")
	}
	base := h.disp.count("page-full")
	for p := 1; p <= 4; p++ { // the 4th turn makes a clear due
		turn(p)
	}
	time.Sleep(60 * time.Millisecond)
	if n := h.disp.count("page-full") - base; n != 0 {
		t.Fatalf("a clear ran inside the turn that made it due (%d)", n)
	}
	h.waitFor(func() bool { return h.disp.count("page-full")-base == 1 }, "the clear after the pause")
	// eight quick turns: the second due point is at 4 again, but the reader keeps
	// turning, so it is put off until twice the interval (8) and forced there
	base = h.disp.count("page-full")
	for p := 5; p <= 12; p++ {
		turn(p)
		time.Sleep(30 * time.Millisecond)
	}
	if n := h.disp.count("page-full") - base; n != 1 {
		t.Errorf("8 quick turns after a clear: %d clears, want 1 (forced at twice the interval)", n)
	}
	h.stop()
}

// A touch while a clear is waiting for the pause cancels the wait; leaving the
// book does too.
func TestDeferredClearIsCancelledByTheNextTouchAndByLeaving(t *testing.T) {
	h := startOpts(t, hopts{
		mut:    func(c *store.Config) { c.ClearStyle = "ui_full"; c.ClearIdleMS = 300 },
		mutSet: func(s *store.Settings) { s.Refresh, s.Custom[0] = store.RefreshCustom1, 2 }})
	h.openFirstChapter()
	h.waitReady(0)
	for p := 1; p <= 2; p++ {
		h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, p) }, "ready")
		h.tap(300, 1000)
		h.waitFor(func() bool { return h.snapshot().Page == p }, "turn")
	}
	base := h.disp.count("page-full")
	h.tap(810, 1000) // the middle column: the bars (a touch, not a turn)
	h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
	time.Sleep(700 * time.Millisecond)
	if n := h.disp.count("page-full") - base; n != 0 {
		t.Errorf("a clear ran %d times after a touch cancelled it", n)
	}
	h.stop()
}

// bars_clean = auto no longer changes the refresh mode: over a page that is not
// white a white pre-fill goes out in the turn waveform first. always keeps the
// CONTENT repaint (and its mode messages).
func TestBarsAutoPrefillsWithoutAModeMessage(t *testing.T) {
	for _, tc := range []struct {
		clean   string
		prefill bool
		content bool
	}{{"auto", true, false}, {"always", true, true}, {"never", false, false}} {
		h := startOpts(t, hopts{mut: func(c *store.Config) { c.BarsClean = tc.clean; c.Border = "match"; c.Crop = "keep" }})
		h.openMangaB("row-8") // Black Pages
		h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page 0")
		before := len(h.disp.all())
		h.tap(810, 1000)
		h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
		var pre, content bool
		for _, f := range h.disp.all()[before:] {
			if f.tag == "overlay-prefill" {
				pre = true
			}
			if strings.HasPrefix(f.tag, "overlay") && f.mode == display.ModeContent {
				content = true
			}
		}
		if pre != tc.prefill || content != tc.content {
			t.Errorf("bars_clean=%s: prefill=%v content=%v, want %v and %v", tc.clean, pre, content, tc.prefill, tc.content)
		}
		h.stop()
	}
}
