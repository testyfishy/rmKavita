package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/store"
)

// ---- 0.3.0: the bars are two updates --------------------------------------------------------

// openMangaBRig walks home -> All Libraries -> Manga B -> the named series row
// -> its first chapter, on the protocol rig.
func (r *rig) openMangaB(row string) {
	r.t.Helper()
	r.wait(func(s Snapshot) bool { return s.Screen == "list" && s.ListRows > 0 }, "home")
	r.tapName("row-5")
	r.wait(func(s Snapshot) bool { return s.ListTitle == "All Libraries" && s.ListRows >= 2 }, "libraries")
	r.tapName("row-1")
	r.wait(func(s Snapshot) bool { return s.ListTitle == "Manga B" && s.ListRows >= 9 }, "Manga B")
	r.tapName(row)
	r.wait(func(s Snapshot) bool { return s.Screen == "list" && s.ListRows >= 1 && s.ListTitle != "Manga B" }, "chapters")
	r.tapName("row-0")
	r.wait(func(s Snapshot) bool { return s.Screen == "reader" && s.Tag == "reader-first" }, "first page")
}

// With the defaults, opening the bars is exactly two updates (the top bar, the
// bottom bar) and closing them is two, and no refresh-mode message goes out
// either way: on a white page, a black page, a colour page, and in every
// render mode. Earlier builds sent four to open them.
func TestBarsOpenAndCloseAreTwoUpdatesAndNoModeMessage(t *testing.T) {
	cases := []struct {
		name   string
		open   func(r *rig)
		render string
		colour bool
	}{
		{"white page, balanced", func(r *rig) { r.openFirst() }, "balanced", false},
		{"black page, balanced", func(r *rig) { r.openMangaB("row-8") }, "balanced", false},
		{"colour page, balanced", func(r *rig) { r.openMangaB("row-0") }, "balanced", true},
		{"black page, quality", func(r *rig) { r.openMangaB("row-8") }, "quality", false},
		{"black page, speed", func(r *rig) { r.openMangaB("row-8") }, "speed", false},
		{"colour page, speed", func(r *rig) { r.openMangaB("row-0") }, "speed", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, rigOpts{
				pages: 12,
				set:   func(s *store.Settings) { s.Colour = tc.colour; s.Refresh = store.RefreshNever },
				cfg:   func(c *store.Config) { c.Render = tc.render; c.ClearIdleMS = 0 }})
			tc.open(r)
			r.wait(func(s Snapshot) bool { return contains(s.ReadyPages, 0) && s.Tag == "reader-first" }, "page")
			time.Sleep(60 * time.Millisecond)
			for round := 0; round < 2; round++ {
				before := len(r.conn.all())
				r.touch(810, 1000)
				r.wait(func(s Snapshot) bool { return s.Overlay }, "bars open")
				time.Sleep(25 * time.Millisecond)
				if got := kinds(r.conn.all()[before:]); got != "update,update" {
					t.Fatalf("round %d: opening the bars sent %q, want update,update", round, got)
				}
				before = len(r.conn.all())
				r.touch(810, 1000)
				r.wait(func(s Snapshot) bool { return !s.Overlay }, "bars closed")
				time.Sleep(25 * time.Millisecond)
				if got := kinds(r.conn.all()[before:]); got != "update,update" {
					t.Fatalf("round %d: closing the bars sent %q, want update,update", round, got)
				}
			}
			r.stop()
			lg, _ := os.ReadFile(filepath.Join(r.dir, "app.log"))
			if strings.Contains(string(lg), "tag=overlay-prefill") {
				t.Error("a white pre-fill went out with the default bars_clean")
			}
			// ready_ms of the first update of a touch: finger up -> handed to the display
			// (the bottom bar goes out right after it and is not touch-timed)
			re := regexp.MustCompile(`tag=overlay-top ready_ms=(-?\d+)`)
			ms := re.FindAllStringSubmatch(string(lg), -1)
			if len(ms) < 2 {
				t.Fatalf("no overlay-top telemetry lines:\n%s", tail(string(lg), 600))
			}
			if v, _ := strconv.Atoi(ms[0][1]); v < 0 || v > 500 {
				t.Errorf("ready_ms of the bar update = %d", v)
			}
		})
	}
}

// The account menu under the top bar is one update, not a pre-fill and the menu.
func TestAccountMenuIsOneUpdate(t *testing.T) {
	r := newRig(t, rigOpts{pages: 12, set: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
	r.openMangaB("row-8")
	r.wait(func(s Snapshot) bool { return contains(s.ReadyPages, 0) }, "page")
	r.touch(810, 1000)
	r.wait(func(s Snapshot) bool { return s.Overlay }, "bars")
	time.Sleep(25 * time.Millisecond)
	before := len(r.conn.all())
	r.tapName("account")
	r.wait(func(s Snapshot) bool { return s.Popover }, "menu")
	time.Sleep(25 * time.Millisecond)
	if got := kinds(r.conn.all()[before:]); got != "update" {
		t.Errorf("the account menu sent %q, want one update", got)
	}
	r.stop()
}

// The two old settings still work, and say what they cost: auto and always add
// the white pre-fill step over a page that is not white (auto in the page
// waveform, always in CONTENT).
func TestBarsPrefillIsOptIn(t *testing.T) {
	for _, tc := range []struct {
		clean string
		want  string
	}{{"never", "update,update"}, {"auto", "update,update,update,update"}} {
		r := newRig(t, rigOpts{pages: 12, cfg: func(c *store.Config) { c.BarsClean = tc.clean },
			set: func(s *store.Settings) { s.Refresh = store.RefreshNever }})
		r.openMangaB("row-8")
		r.wait(func(s Snapshot) bool { return contains(s.ReadyPages, 0) }, "page")
		time.Sleep(60 * time.Millisecond)
		before := len(r.conn.all())
		r.touch(810, 1000)
		r.wait(func(s Snapshot) bool { return s.Overlay }, "bars")
		time.Sleep(25 * time.Millisecond)
		if got := kinds(r.conn.all()[before:]); got != tc.want {
			t.Errorf("bars_clean=%s: %q, want %q", tc.clean, got, tc.want)
		}
		r.stop()
	}
}

// ---- 0.3.0: Speed greys out colour -----------------------------------------------------------

func rowNamed(rows []srow, name string) (srow, bool) {
	for _, r := range rows {
		if r.name == name {
			return r, true
		}
	}
	return srow{}, false
}

// Render = Speed: the Colour rows and the colour full-refresh rows are shown
// disabled with Off, nothing in them takes a tap, and the stored choice (in
// memory and in the settings file) is not touched: leaving Speed brings it back
// exactly, and so does a restart while in Speed.
func TestSpeedGreysOutColourAndTheChoiceComesBack(t *testing.T) {
	h := startOpts(t, hopts{
		mutSet: func(s *store.Settings) {
			s.Colour = true
			s.ColourRefresh = store.ColourCustom2
			s.ColourCustom[1] = 7
			s.Refresh = store.RefreshNever
		}})
	h.waitTag("list", 1)
	h.tapName("settings")
	h.waitScreen("settings")
	rows := func() (rs []srow) {
		h.onUI(func() { rs = h.app.settingsRows() })
		return
	}
	// balanced: live rows, colour on
	if r, ok := rowNamed(rows(), "colour-on"); !ok || r.disabled || !r.sel {
		t.Fatalf("balanced: colour-on = %+v ok=%v", r, ok)
	}
	if r, ok := rowNamed(rows(), "crefresh-c2"); !ok || r.disabled || !r.sel || r.val != 7 {
		t.Fatalf("balanced: crefresh-c2 = %+v ok=%v", r, ok)
	}
	// Render -> Speed
	h.settingsScrollTo("render-speed")
	h.tapName("render-speed")
	h.waitFor(func() bool {
		var sp bool
		h.onUI(func() { sp = h.app.speedMode() })
		return sp
	}, "speed on")
	rs := rows()
	for _, n := range []string{"colour-off", "colour-on", "crefresh-off"} {
		r, ok := rowNamed(rs, n)
		if !ok || !r.disabled {
			t.Errorf("speed: %s = %+v ok=%v, want a disabled row", n, r, ok)
		}
	}
	if r, _ := rowNamed(rs, "colour-off"); !r.sel {
		t.Error("speed: Black and white is not the selected row")
	}
	if r, _ := rowNamed(rs, "colour-on"); r.sel {
		t.Error("speed: Colour is selected")
	}
	if r, _ := rowNamed(rs, "crefresh-off"); !r.sel || r.label != "Off" {
		t.Errorf("speed: colour refresh row %+v", r)
	}
	for _, n := range []string{"crefresh-0", "crefresh-3", "crefresh-10", "crefresh-c1", "crefresh-c2", "crefresh-c3", "crefresh-never"} {
		if _, ok := rowNamed(rs, n); ok {
			t.Errorf("speed: the colour interval row %s is still offered", n)
		}
	}
	// nothing in them takes a tap
	hits := h.settingsHits()
	for _, n := range []string{"colour-on", "colour-off", "crefresh-off", "crefresh-c2-plus"} {
		if hits[n] {
			t.Errorf("speed: %s is tappable", n)
		}
	}
	// the stored choice is untouched: memory and file
	s, _ := store.LoadSettings(h.setFile, store.DefaultConfig())
	if !s.Colour || s.ColourRefresh != store.ColourCustom2 || s.ColourCustom[1] != 7 {
		t.Errorf("the settings file changed in Speed: colour=%v refresh=%d custom=%d", s.Colour, s.ColourRefresh, s.ColourCustom[1])
	}
	var mem bool
	h.onUI(func() { mem = h.app.Set.Colour })
	if !mem {
		t.Error("the Colour setting was switched off in memory")
	}
	// restart while in Speed (the render key is in the conf): still greyed, still stored
	h.stop()
	h.launch()
	h.waitTag("list", 1)
	h.tapName("settings")
	h.waitScreen("settings")
	if r, ok := rowNamed(rows(), "colour-on"); !ok || !r.disabled {
		t.Errorf("after a restart in Speed: colour-on = %+v ok=%v", r, ok)
	}
	// leave Speed: everything is back as it was
	h.settingsScrollTo("render-balanced")
	h.tapName("render-balanced")
	h.waitFor(func() bool {
		var sp bool
		h.onUI(func() { sp = h.app.speedMode() })
		return !sp
	}, "speed off")
	rs = rows()
	if r, ok := rowNamed(rs, "colour-on"); !ok || r.disabled || !r.sel {
		t.Errorf("balanced again: colour-on = %+v ok=%v", r, ok)
	}
	if r, ok := rowNamed(rs, "crefresh-c2"); !ok || r.disabled || !r.sel || r.val != 7 {
		t.Errorf("balanced again: crefresh-c2 = %+v ok=%v", r, ok)
	}
	h.stop()
}

// A colour page: grey in Speed (no colour layer), in colour again as soon as
// Speed is left, without opening the book again.
func TestLeavingSpeedBringsTheColourPagesBack(t *testing.T) {
	h := startOpts(t, hopts{
		mut: func(c *store.Config) { c.Render = "speed"; c.Border = "white" },
		mutSet: func(s *store.Settings) {
			s.Colour = true
			s.Refresh = store.RefreshNever
		}})
	h.openMangaB("row-0") // colour pages
	h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, 0) }, "page 0")
	var layer, col bool
	h.onUI(func() { layer = h.app.layerActive; col = h.app.rd.ready(0).Col != nil })
	if layer || col {
		t.Fatalf("speed: layer=%v chroma=%v", layer, col)
	}
	h.tap(810, 1000) // bars
	h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
	h.tapName("settings")
	h.waitScreen("settings")
	h.settingsScrollTo("render-balanced")
	h.tapName("render-balanced")
	h.tapName("back")
	h.waitFor(func() bool { return h.snapshot().Screen == "reader" }, "back in the book")
	h.waitFor(func() bool {
		var ok bool
		h.onUI(func() {
			f := h.app.rd.ready(h.app.rd.page)
			ok = f != nil && f.Col != nil && h.app.layerActive
		})
		return ok
	}, "page 0 decoded in colour again")
	h.stop()
}

// The hot-corner "colour" function does nothing in Speed (and says so in the
// log): it must not change the stored choice behind the greyed rows.
func TestHotCornerColourIsANoOpInSpeed(t *testing.T) {
	h := startOpts(t, hopts{
		mut:    func(c *store.Config) { c.Render = "speed" },
		mutSet: func(s *store.Settings) { s.Colour = false; s.Refresh = store.RefreshNever }})
	h.openFirstChapter()
	h.waitReady(0)
	n := len(h.disp.all())
	h.onUI(func() { h.app.runHot(store.HotColour, "test") })
	time.Sleep(40 * time.Millisecond)
	var colour bool
	h.onUI(func() { colour = h.app.Set.Colour })
	if colour {
		t.Error("the hot corner switched Colour on in Speed")
	}
	if got := len(h.disp.all()) - n; got != 0 {
		t.Errorf("the hot corner repainted the screen %d times", got)
	}
	if !strings.Contains(h.readLog(), "colour is off in Speed") {
		t.Error("no log line for the ignored corner")
	}
	h.stop()
	// outside Speed it still toggles
	h = startOpts(t, hopts{mutSet: func(s *store.Settings) { s.Colour = false; s.Refresh = store.RefreshNever }})
	h.openFirstChapter()
	h.waitReady(0)
	h.onUI(func() { h.app.runHot(store.HotColour, "test") })
	h.onUI(func() { colour = h.app.Set.Colour })
	if !colour {
		t.Error("the hot corner no longer toggles colour in balanced mode")
	}
	h.stop()
}

// ---- 0.3.0: the defaults ----------------------------------------------------------------------

func TestVersion030Defaults(t *testing.T) {
	d := store.DefaultConfig()
	if d.BarsClean != "never" || d.Render != "balanced" || d.ClearStyle != "ui_full" || d.Crop != "trim" ||
		d.SpeedLevels != 2 || d.SpeedDither != "diffuse" || d.PageMode != "auto" {
		t.Errorf("defaults: %+v", d)
	}
	// the key of earlier versions still maps: yes -> auto (the white pre-fill is an opt-in step)
	if got := store.ParseConfig("chrome_clean = yes\n").BarsClean; got != "auto" {
		t.Errorf("chrome_clean = yes -> %q", got)
	}
	_ = fmt.Sprint
	_ = display.ModeUI
}

// ---- 0.3.0: About ------------------------------------------------------------------------------

// Settings -> About says the version (the one the binary was built with), the
// licence and where the credits are; Back goes to Settings, not out of it.
func TestAboutScreen(t *testing.T) {
	h := startOpts(t, hopts{})
	h.waitTag("list", 1)
	h.onUI(func() { h.app.version = "9.8.7" })
	h.tapName("settings")
	h.waitScreen("settings")
	h.settingsScrollTo("about-open")
	h.tapName("about-open")
	h.waitFor(func() bool {
		var in bool
		h.onUI(func() { in = h.app.sv != nil && h.app.sv.about })
		return in
	}, "about")
	var labels []string
	h.onUI(func() {
		for _, r := range h.app.settingsRows() {
			labels = append(labels, r.label)
		}
	})
	all := strings.Join(labels, "\n")
	for _, want := range []string{"Version 9.8.7", "PolyForm Noncommercial 1.0.0", "CREDITS.md"} {
		if !strings.Contains(all, want) {
			t.Errorf("About lacks %q:\n%s", want, all)
		}
	}
	if strings.Contains(strings.ToLower(all), "open source") || strings.Contains(all, "M"+"IT") {
		t.Errorf("About says something the licence does not:\n%s", all)
	}
	h.tapName("back")
	h.waitFor(func() bool {
		var in, s bool
		h.onUI(func() { in = h.app.sv != nil && h.app.sv.about; s = h.app.screen() == scrSettings })
		return !in && s
	}, "back to settings")
	h.stop()
}
