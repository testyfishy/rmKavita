package ui

import (
	"fmt"
	"image"
	"strconv"

	"rmkavita/internal/opds"
	"rmkavita/internal/store"
)

// The Settings screen is one scrollable list of big radio rows. Every change
// is applied and saved at once.

type settingsView struct {
	scroll   int
	adv      bool     // the Advanced screen (the A/B kit)
	about    bool     // the About screen
	hot      int      // 0 = not shown, 1 = the Hot corners list, 2 = the chooser of one corner
	hotIdx   int      // the corner the chooser is for (index into cornerKeys)
	kind     opds.Doc // whose reading mode and fit are shown
	fromRd   bool     // opened from a book: the kind is fixed
	contentH int
	state    []rowState // what each row showed when it was last drawn
	rects    []image.Rectangle
}

// rowState is what makes a row look different.
type rowState struct {
	sel, disabled bool
	val           int
}

type srowKind int

const (
	srSection srowKind = iota
	srRadio
	srStepper
	srInfo
	srKinds
	srLink
)

type srow struct {
	typ      srowKind
	name     string
	label    string
	sel      bool
	disabled bool
	fn       func()
	// stepper
	val        int
	unit       string
	minus, inc func()
	noRepeat   bool // the stepper buttons are plain taps (the layout moves with the value)
	plain      bool // a number, not a choice: no radio mark
}

func (a *App) openSettings() {
	a.closePopover()
	if a.screen() == scrSignin || a.screen() == scrSearch {
		return
	}
	sv := &settingsView{kind: opds.DocComic}
	if a.rd != nil {
		sv.kind, sv.fromRd = a.rd.doc, true
		a.ovT.Stop()
		a.rd.overlay = false
	}
	a.sv = sv
	a.drawSettings()
	if a.rd != nil {
		a.clearScreen("settings-open") // leaving a page: wipe it properly
	} else {
		a.present(a.full(), a.uiMode(), false, "settings")
	}
}

func (a *App) closeSettings() {
	a.flushSettings()
	if a.sv != nil && (a.sv.adv || a.sv.about || a.sv.hot > 0) {
		switch {
		case a.sv.about:
			a.sv.about = false
		case a.sv.hot == 2:
			a.sv.hot = 1 // the chooser goes back to the list of corners
		case a.sv.hot == 1:
			a.sv.hot = 0
		default:
			a.sv.adv = false
		}
		a.sv.scroll = 0
		a.drawSettings()
		a.present(a.full(), a.uiMode(), false, "settings")
		return
	}
	a.sv = nil
	a.closePopover()
	if a.rd != nil {
		if a.pagesDirty {
			a.invalidatePages()
		}
		a.pagesDirty = false
		r := a.rd
		_, ready := a.composeReader()
		if ready && !r.first {
			a.clearScreen("settings-close")
		} else {
			a.present(a.full(), a.uiMode(), false, "settings-close")
		}
		return
	}
	a.pagesDirty = false
	a.render("settings-close")
}

// invalidatePages drops every fitted page (the fit, the reading mode or the
// colour setting changed) and fetches them again, keeping the position.
func (a *App) invalidatePages() {
	r := a.rd
	if r == nil {
		return
	}
	pm := a.offPM(r)
	r.savePos(a)
	for _, s := range r.slots {
		if s.cancel != nil {
			s.cancel()
		}
	}
	r.slots = map[int]*slot{}
	r.dropWarm()
	r.gen++
	r.off, r.toEnd, r.startPM = 0, false, pm
	r.first, r.need, r.shown = true, true, -1
	if !a.specFor(r.doc).colour {
		a.col = nil
	}
	a.rdWindow()
}

// offPM is the scroll offset of the current page in thousandths of its extent.
func (a *App) offPM(r *reader) int {
	f := r.ready(r.page)
	if f == nil || r.off <= 0 {
		return 0
	}
	ext, _, _ := a.scrollGeom(f)
	if ext <= 0 {
		return 0
	}
	return r.off * 1000 / ext
}

func (a *App) prefsFor(k opds.Doc) *store.Prefs {
	if k == opds.DocPDF {
		return &a.Set.PDF
	}
	return &a.Set.Comic
}

// changed saves the settings and redraws; pages is true when the change alters
// how pages are fitted.
func (a *App) settingsChanged(pages bool) { a.settingsApply(pages, false) }

// settingsApply saves (not while a finger is repeating a stepper: the file is
// written when it lifts) and redraws. Only the rows that changed are sent to
// the panel, so holding a stepper does not flash the whole screen; full asks
// for the whole screen (the layout moved).
func (a *App) settingsApply(pages, full bool) {
	if pages {
		a.pagesDirty = true
	}
	a.Set.Sanitize()
	a.setDirty = true
	if !a.touch.repFired {
		a.flushSettings()
	}
	a.redrawSettings(full)
}

// redrawSettings repaints the screen and sends only the rows that changed (the
// whole screen when the layout moved).
func (a *App) redrawSettings(full bool) {
	sv := a.sv
	old := append([]rowState(nil), sv.state...)
	a.drawSettings()
	if full || len(old) != len(sv.state) {
		a.present(a.full(), a.uiMode(), false, "settings")
		return
	}
	for i := range old {
		if old[i] != sv.state[i] {
			a.present(sv.rects[i], a.uiMode(), false, "settings-row")
		}
	}
}

// advSet changes one key of the A/B kit: in memory at once, in rmkavita.conf
// (where the key lives; the line is replaced in place) so it is there after the
// next launch. pages is true when it changes how pages are prepared.
func (a *App) advSet(key, val string, pages bool) {
	switch key {
	case "page_mode":
		a.Cfg.PageMode = val
		if a.rd != nil {
			a.rd.wave = a.baseWave()
		}
	case "ui_mode":
		a.Cfg.UIMode = val
	case "clear_style":
		a.Cfg.ClearStyle = val
	case "fb_format":
		a.Cfg.FBFormat = val
	case "border":
		a.Cfg.Border = val
	case "crop":
		a.Cfg.Crop = val
	case "bars_clean":
		a.Cfg.BarsClean = val
	case "clear_idle_ms":
		n, _ := strconv.Atoi(val)
		a.Cfg.ClearIdleMS = n
		if n == 0 {
			a.clrT.Stop()
		}
	case "render", "speed_levels", "speed_dither":
		switch key {
		case "render":
			a.Cfg.Render = val
		case "speed_levels":
			n, _ := strconv.Atoi(val)
			a.Cfg.SpeedLevels = n
		default:
			a.Cfg.SpeedDither = val
		}
		if a.rd != nil {
			a.rd.wave = a.baseWave() // the turn waveform follows the render mode at once
		}
	case "read_ahead", "keep_behind", "cache_mb", "zone_middle_pct":
		n, _ := strconv.Atoi(val)
		switch key {
		case "read_ahead":
			a.Cfg.ReadAhead = n
		case "keep_behind":
			a.Cfg.KeepBehind = n
		case "cache_mb":
			a.Cfg.CacheMB = n
		default:
			a.Cfg.ZoneMiddle = n
		}
		if a.rd != nil && key != "zone_middle_pct" {
			a.rdWindow() // the window changes at once: fetch more, or let go of pages
		}
	}
	if pages {
		a.pagesDirty = true
	}
	if a.confPath != "" {
		if err := store.UpdateConf(a.confPath, map[string]string{key: val}, nil); err != nil {
			a.logf("could not save %s: %v", key, err)
		}
	}
	a.redrawSettings(false)
}

// advStepper is a number of the A/B kit as a stepper row.
func (a *App) advStepper(key, label, unit string, cur, lo, hi, step int) srow {
	set := func(v int) { a.advSet(key, strconv.Itoa(clampInt(v, lo, hi)), false) }
	return srow{typ: srStepper, name: "adv-" + key, label: label, unit: unit, sel: true, plain: true, val: cur, noRepeat: true,
		fn:    func() {},
		minus: func() { set(cur - step) },
		inc:   func() { set(cur + step) }}
}

// advancedRows is the A/B kit as radio rows and steppers (one screen).
func (a *App) advancedRows() []srow {
	var rows []srow
	group := func(title, key string, cur string, pages bool, opts [][2]string) {
		rows = append(rows, srow{typ: srSection, label: title})
		for _, o := range opts {
			val := o[0]
			rows = append(rows, srow{typ: srRadio, name: "adv-" + key + "-" + val, label: o[1], sel: cur == val,
				fn: func() { a.advSet(key, val, pages) }})
		}
	}
	c := a.Cfg
	pageMode := c.PageMode
	if pageMode != "fast" && pageMode != "content" && pageMode != "ui" && pageMode != "animate" {
		pageMode = "auto"
	}
	group("Page turns", "page_mode", pageMode, false, [][2]string{
		{"auto", "Auto (UI)"}, {"fast", "Fast"}, {"ui", "UI"}, {"content", "Content"}, {"animate", "Animate (test)"}})
	group("Lists and menus", "ui_mode", c.UIMode, false, [][2]string{
		{"ui", "UI"}, {"fast", "Fast"}, {"content", "Content"}})
	group("Clearing refresh", "clear_style", c.ClearStyle, false, [][2]string{
		{"ui_full", "UI + full refresh (no mode change)"}, {"content_full", "Content + full refresh"}})
	idle := "600"
	if c.ClearIdleMS == 0 {
		idle = "0"
	} else if c.ClearIdleMS != 600 {
		idle = strconv.Itoa(c.ClearIdleMS)
	}
	idleOpts := [][2]string{{"600", "When the reader pauses (0.6 s)"}, {"0", "Inside the page turn"}}
	if idle != "600" && idle != "0" {
		idleOpts = append(idleOpts, [2]string{idle, "When the reader pauses (" + idle + " ms)"})
	}
	group("Clearing refresh timing", "clear_idle_ms", idle, false, idleOpts)
	group("Bars repaint", "bars_clean", c.BarsClean, false, [][2]string{
		{"never", "Never (two updates)"}, {"auto", "Auto (adds a white pre-fill step)"}, {"always", "Always (pre-fill step + CONTENT)"}})
	group("Framebuffer (next launch)", "fb_format", c.FBFormat, false, [][2]string{
		{"auto", "Auto (RGBA8888)"}, {"rgba", "RGBA8888"}, {"rgb565", "RGB565"}})
	group("Speed levels", "speed_levels", strconv.Itoa(c.SpeedLevels), true, [][2]string{
		{"2", "2 (black and white)"}, {"4", "4 grey levels"}, {"16", "16 grey levels"}})
	group("Speed dither", "speed_dither", c.SpeedDither, true, [][2]string{
		{"diffuse", "Error diffusion"}, {"ordered", "Ordered"}, {"none", "None"}})
	group("Page border", "border", c.Border, true, [][2]string{{"match", "Match page"}, {"white", "White"}})
	group("Margins", "crop", c.Crop, true, [][2]string{{"trim", "Trim"}, {"keep", "Keep"}})
	rows = append(rows, srow{typ: srSection, label: "Pages in memory"},
		a.advStepper("read_ahead", "Read ahead", "pages", c.ReadAhead, 1, 5, 1),
		a.advStepper("keep_behind", "Keep behind", "pages", c.KeepBehind, 0, 4, 1),
		a.advStepper("cache_mb", "Budget", "MB", c.CacheMB, 32, 192, 16))
	rows = append(rows, srow{typ: srSection, label: "Tap columns"},
		a.advStepper("zone_middle_pct", "Middle column", "%", c.ZoneMiddle, 20, 60, 5))
	return rows
}

// flushSettings writes the settings file if something changed.
func (a *App) flushSettings() {
	if !a.setDirty {
		return
	}
	if a.setPath == "" {
		a.setDirty = false
		return
	}
	if err := store.SaveSettings(a.setPath, a.Set); err != nil {
		a.logf("could not save the settings: %v", err) // stays dirty: the next flush tries again
		return
	}
	a.setDirty = false
}

// settingsRows is the list, in order, for the current state.
func (a *App) settingsRows() []srow {
	sv := a.sv
	if sv.about {
		return a.aboutRows()
	}
	if sv.adv {
		return a.advancedRows()
	}
	if sv.hot > 0 {
		return a.hotRows()
	}
	s := &a.Set
	p := a.prefsFor(sv.kind)
	var rows []srow
	if !sv.fromRd {
		rows = append(rows, srow{typ: srKinds})
	}
	rows = append(rows, srow{typ: srSection, label: "Fit"})
	for _, f := range []struct{ name, label, v string }{
		{"fit-width", "Width", store.FitWidth}, {"fit-height", "Height", store.FitHeight}, {"fit-both", "Both", store.FitBoth}} {
		f := f
		rows = append(rows, srow{typ: srRadio, name: f.name, label: f.label, sel: p.Fit == f.v,
			fn: func() {
				p.Fit = f.v
				a.settingsChanged(a.affects(sv.kind))
			}})
	}
	rows = append(rows, srow{typ: srSection, label: "Footer"},
		srow{typ: srRadio, name: "footer-on", label: "On", sel: s.Footer, fn: func() { s.Footer = true; a.settingsChanged(a.rd != nil) }},
		srow{typ: srRadio, name: "footer-off", label: "Off", sel: !s.Footer, fn: func() { s.Footer = false; a.settingsChanged(a.rd != nil) }})
	rows = append(rows, srow{typ: srSection, label: "Full refresh, black and white"},
		srow{typ: srRadio, name: "refresh-0", label: fmt.Sprintf("Every %d pages (default)", store.DefaultRefresh), sel: s.Refresh == store.RefreshDefault,
			fn: func() { s.Refresh = store.RefreshDefault; a.settingsChanged(false) }})
	for i := 0; i < 3; i++ {
		i := i
		rows = append(rows, srow{typ: srStepper, name: fmt.Sprintf("refresh-%d", i+1), label: "Every", unit: "pages",
			sel: s.Refresh == store.RefreshCustom1+i, val: s.Custom[i],
			fn: func() { s.Refresh = store.RefreshCustom1 + i; a.settingsChanged(false) },
			minus: func() {
				s.Custom[i] = clampInt(s.Custom[i]-1, store.MinRefresh, store.MaxRefresh)
				s.Refresh = store.RefreshCustom1 + i
				a.settingsChanged(false)
			},
			inc: func() {
				s.Custom[i] = clampInt(s.Custom[i]+1, store.MinRefresh, store.MaxRefresh)
				s.Refresh = store.RefreshCustom1 + i
				a.settingsChanged(false)
			}})
	}
	rows = append(rows, srow{typ: srRadio, name: "refresh-4", label: "Never", sel: s.Refresh == store.RefreshNever,
		fn: func() { s.Refresh = store.RefreshNever; a.settingsChanged(false) }})
	// Colour pages ghost more: their own interval, every 5 pages by default.
	colourRefreshAt := len(rows)
	rows = append(rows, srow{typ: srSection, label: "Full refresh, colour"},
		srow{typ: srRadio, name: "crefresh-0", label: fmt.Sprintf("Every %d pages (default)", store.DefaultColourN), sel: s.ColourRefresh == store.ColourEvery5,
			fn: func() { s.ColourRefresh = store.ColourEvery5; a.settingsChanged(false) }},
		srow{typ: srRadio, name: "crefresh-3", label: "Every 3 pages", sel: s.ColourRefresh == store.ColourEvery3,
			fn: func() { s.ColourRefresh = store.ColourEvery3; a.settingsChanged(false) }},
		srow{typ: srRadio, name: "crefresh-10", label: "Every 10 pages", sel: s.ColourRefresh == store.ColourEvery10,
			fn: func() { s.ColourRefresh = store.ColourEvery10; a.settingsChanged(false) }})
	for i := 0; i < 3; i++ {
		i := i
		rows = append(rows, srow{typ: srStepper, name: fmt.Sprintf("crefresh-c%d", i+1), label: "Every", unit: "pages",
			sel: s.ColourRefresh == store.ColourCustom1+i, val: s.ColourCustom[i],
			fn: func() { s.ColourRefresh = store.ColourCustom1 + i; a.settingsChanged(false) },
			minus: func() {
				s.ColourCustom[i] = clampInt(s.ColourCustom[i]-1, store.MinRefresh, store.MaxRefresh)
				s.ColourRefresh = store.ColourCustom1 + i
				a.settingsChanged(false)
			},
			inc: func() {
				s.ColourCustom[i] = clampInt(s.ColourCustom[i]+1, store.MinRefresh, store.MaxRefresh)
				s.ColourRefresh = store.ColourCustom1 + i
				a.settingsChanged(false)
			}})
	}
	rows = append(rows, srow{typ: srRadio, name: "crefresh-never", label: "Never", sel: s.ColourRefresh == store.ColourNever,
		fn: func() { s.ColourRefresh = store.ColourNever; a.settingsChanged(false) }})
	speed := a.speedMode()
	if speed {
		// Speed is black and white: the colour intervals are shown as Off and
		// cannot be changed. The stored choice is not touched, so it is back, as it
		// was, when Speed is left.
		rows = append(rows[:colourRefreshAt],
			srow{typ: srSection, label: "Full refresh, colour"},
			srow{typ: srRadio, name: "crefresh-off", label: "Off", sel: true, disabled: true},
			srow{typ: srInfo, label: "Speed is black and white"})
	}
	if sv.kind == opds.DocComic {
		rows = append(rows, srow{typ: srSection, label: "Reading direction"},
			srow{typ: srRadio, name: "dir-rtl", label: "Right to left", sel: s.RTL, fn: func() { s.RTL = true; a.settingsChanged(false) }},
			srow{typ: srRadio, name: "dir-ltr", label: "Left to right", sel: !s.RTL, fn: func() { s.RTL = false; a.settingsChanged(false) }})
	}
	if speed {
		rows = append(rows, srow{typ: srSection, label: "Colour"},
			srow{typ: srRadio, name: "colour-off", label: "Black and white", sel: true, disabled: true},
			srow{typ: srRadio, name: "colour-on", label: "Colour", sel: false, disabled: true},
			srow{typ: srInfo, label: "Speed is black and white"})
	} else {
		rows = append(rows, srow{typ: srSection, label: "Colour"},
			srow{typ: srRadio, name: "colour-off", label: "Black and white", sel: !s.Colour, fn: func() { s.Colour = false; a.settingsChanged(true) }},
			srow{typ: srRadio, name: "colour-on", label: "Colour", sel: s.Colour, fn: func() { s.Colour = true; a.settingsChanged(true) }},
			srow{typ: srInfo, label: "Colour may flicker and slow page turns"})
	}
	// how a page turn is rendered: quality (CONTENT), balanced (the page_mode,
	// UI by default), speed (FAST, grey, few grey levels, quantised ahead of time)
	renderRow := func(val, name, label string) srow {
		return srow{typ: srRadio, name: "render-" + name, label: label, sel: a.Cfg.Render == val,
			fn: func() { a.advSet("render", val, true) }}
	}
	rows = append(rows, srow{typ: srSection, label: "Render"},
		renderRow("quality", "quality", "Quality"),
		renderRow("balanced", "balanced", "Balanced"),
		renderRow("speed", "speed", "Speed"))
	if a.Cfg.Render == "speed" {
		rows = append(rows, srow{typ: srInfo, label: fmt.Sprintf("Speed: grey, %d levels", a.Cfg.SpeedLevels)})
	}
	// long lists: compact rows and the A-Z bar (see alpha.go)
	rows = append(rows, srow{typ: srSection, label: "Lists"},
		srow{typ: srRadio, name: "lists-auto", label: "Auto (compact over 40)", sel: s.Lists == store.ListsAuto, fn: func() { s.Lists = store.ListsAuto; a.settingsChanged(false) }},
		srow{typ: srRadio, name: "lists-compact", label: "Compact", sel: s.Lists == store.ListsCompact, fn: func() { s.Lists = store.ListsCompact; a.settingsChanged(false) }},
		srow{typ: srRadio, name: "lists-comfortable", label: "Comfortable", sel: s.Lists == store.ListsComfortable, fn: func() { s.Lists = store.ListsComfortable; a.settingsChanged(false) }},
		srow{typ: srSection, label: "Alphabet bar"},
		srow{typ: srRadio, name: "alpha-auto", label: "Auto (long alphabetical lists)", sel: s.Alpha == store.AlphaAuto, fn: func() { s.Alpha = store.AlphaAuto; a.settingsChanged(false) }},
		srow{typ: srRadio, name: "alpha-on", label: "On", sel: s.Alpha == store.AlphaOn, fn: func() { s.Alpha = store.AlphaOn; a.settingsChanged(false) }},
		srow{typ: srRadio, name: "alpha-off", label: "Off", sel: s.Alpha == store.AlphaOff, fn: func() { s.Alpha = store.AlphaOff; a.settingsChanged(false) }})
	dpiVal := s.DPI
	if dpiVal == 0 {
		dpiVal = a.autoDPI()
	}
	rows = append(rows, srow{typ: srSection, label: "Screen density"},
		srow{typ: srRadio, name: "dpi-auto", label: fmt.Sprintf("Auto (%d dpi)", a.autoDPI()), sel: s.DPI == 0,
			fn: func() { s.DPI = 0; a.applyDPI(); a.settingsApply(false, true) }},
		srow{typ: srStepper, name: "dpi-custom", label: "Custom", unit: "dpi", sel: s.DPI != 0, val: dpiVal,
			fn: func() {
				if s.DPI == 0 {
					s.DPI = a.autoDPI()
				}
				a.applyDPI()
				a.settingsApply(false, true)
			},
			noRepeat: true,
			minus: func() {
				s.DPI = clampInt(dpiVal-dpiStep, store.MinDPI, store.MaxDPI)
				a.applyDPI()
				a.settingsApply(false, true)
			},
			inc: func() {
				s.DPI = clampInt(dpiVal+dpiStep, store.MinDPI, store.MaxDPI)
				a.applyDPI()
				a.settingsApply(false, true)
			}},
		srow{typ: srLink, name: "hot-open", label: "Hot corners", fn: func() {
			sv.hot, sv.scroll = 1, 0
			a.drawSettings()
			a.present(a.full(), a.uiMode(), false, "settings")
		}},
		srow{typ: srLink, name: "adv-open", label: "Advanced", fn: func() {
			sv.adv, sv.scroll = true, 0
			a.drawSettings()
			a.present(a.full(), a.uiMode(), false, "settings")
		}},
		srow{typ: srLink, name: "about-open", label: "About", fn: func() {
			sv.about, sv.scroll = true, 0
			a.drawSettings()
			a.present(a.full(), a.uiMode(), false, "settings")
		}})
	return rows
}

// licenceName and creditsLine are what the About screen says about the program;
// the texts are in LICENSE, NOTICE and CREDITS.md of the repository.
const (
	licenceName = "PolyForm Noncommercial 1.0.0"
	creditsLine = "Credits are in CREDITS.md in the repository"
)

// aboutRows is the About screen: the version, the licence, where the credits
// are. Plain lines, nothing to tap.
func (a *App) aboutRows() []srow {
	v := a.version
	if v == "" {
		v = "development"
	}
	return []srow{
		{typ: srSection, label: "rmKavita"},
		{typ: srInfo, label: "Version " + v},
		{typ: srInfo, label: "Licence: " + licenceName},
		{typ: srInfo, label: "Source-available, for noncommercial use"},
		{typ: srSection, label: "Credits"},
		{typ: srInfo, label: creditsLine},
		{typ: srInfo, label: "Unofficial: not affiliated with Kavita or reMarkable"},
	}
}

// hotRows is the Hot corners screen: the list of the four corners, and, once one
// is chosen, the functions it can be given.
func (a *App) hotRows() []srow {
	sv := a.sv
	var rows []srow
	if sv.hot == 1 {
		for i, ck := range cornerKeys {
			i, ck := i, ck
			rows = append(rows, srow{typ: srLink, name: "hot-" + ck.key, label: ck.label, unit: hotLabels[a.hotAction(ck.c)],
				fn: func() {
					sv.hot, sv.hotIdx, sv.scroll = 2, i, 0
					a.drawSettings()
					a.present(a.full(), a.uiMode(), false, "settings")
				}})
		}
		rows = append(rows, srow{typ: srInfo, label: "Hold a corner; Exit stays in the account menu"})
		return rows
	}
	ck := cornerKeys[sv.hotIdx]
	cur := a.hotAction(ck.c)
	rows = append(rows, srow{typ: srSection, label: ck.label})
	for _, act := range store.HotActions {
		act := act
		rows = append(rows, srow{typ: srRadio, name: "hotpick-" + act, label: hotLabels[act], sel: cur == act,
			fn: func() {
				a.hotSet(ck.c, ck.key, act)
				a.redrawSettings(false)
			}})
	}
	return rows
}

// affects reports whether a reading-mode/fit change for kind alters the pages
// of the book that is open.
func (a *App) affects(k opds.Doc) bool { return a.rd != nil && a.rd.doc == k }

func (a *App) rowHeight(t srowKind) int {
	switch t {
	case srSection:
		return a.u(80)
	case srInfo:
		return a.u(64)
	case srKinds:
		return a.u(130)
	}
	return a.u(120)
}

func (a *App) drawSettings() {
	sv := a.sv
	c := a.cv
	a.layerActive = false
	a.opaque = a.opaque[:0]
	c.Fill(a.full(), paperGray)
	a.hits = a.hits[:0]
	rows := a.settingsRows()
	top := a.barRect().Max.Y
	h := 0
	for _, r := range rows {
		h += a.rowHeight(r.typ)
	}
	h += a.u(30)
	sv.contentH = h
	maxScroll := max(h-(a.H-top), 0)
	sv.scroll = clampInt(sv.scroll, 0, maxScroll)

	m := a.margin()
	y := top + a.u(10) - sv.scroll
	ls := a.st(46, false, inkBlack)
	sv.state = sv.state[:0]
	sv.rects = sv.rects[:0]
	for _, r := range rows {
		rh := a.rowHeight(r.typ)
		row := image.Rect(0, y, a.W, y+rh)
		sv.state = append(sv.state, rowState{sel: r.sel, disabled: r.disabled, val: r.val})
		sv.rects = append(sv.rects, row.Intersect(image.Rect(0, top, a.W, a.H)))
		visible := row.Min.Y >= top && row.Max.Y <= a.H
		switch r.typ {
		case srSection:
			s := a.st(34, true, inkDim)
			a.Fonts.Draw(c, m, y+rh-a.u(20), r.label, s)
			c.Fill(image.Rect(m, y+rh-a.u(6), a.W-m, y+rh-a.u(4)), inkRule)
		case srInfo:
			s := a.st(32, false, inkDim)
			a.Fonts.Draw(c, m+a.u(20), y+rh-a.u(18), a.Fonts.Ellipsize(r.label, a.W-2*m, s), s)
		case srKinds:
			bw := (a.W - 2*m - a.u(20)) / 2
			for i, k := range []struct {
				name, label string
				d           opds.Doc
			}{{"kind-comic", "Comics", opds.DocComic}, {"kind-pdf", "PDF", opds.DocPDF}} {
				k := k
				x := m + i*(bw+a.u(20))
				rc := image.Rect(x, y+a.u(10), x+bw, y+rh-a.u(10))
				bs := a.st(46, true, inkBlack)
				if sv.kind == k.d {
					c.Fill(rc, inkBlack)
					bs.Gray = paperGray
				} else {
					c.Frame(rc, a.u(4), inkBlack)
				}
				w := a.Fonts.Width(k.label, bs)
				a.Fonts.Draw(c, rc.Min.X+(rc.Dx()-w)/2, a.textBaseline(rc.Min.Y, rc.Dy(), bs.Size), k.label, bs)
				if visible {
					a.addNamedHit(k.name, rc, func() {
						sv.kind = k.d
						sv.scroll = 0
						a.settingsChanged(false)
					})
				}
			}
		case srLink:
			s := a.st(46, true, inkBlack)
			a.Fonts.Draw(c, m+a.u(20), a.textBaseline(y, rh, s.Size), r.label, s)
			if r.unit != "" { // the current value, right-aligned before the chevron
				vs := a.st(38, false, inkDim)
				vt := a.Fonts.Ellipsize(r.unit, a.W/2-a.u(60), vs)
				a.Fonts.Draw(c, a.W-m-a.u(90)-a.Fonts.Width(vt, vs), a.textBaseline(y, rh, vs.Size), vt, vs)
			}
			c.Chevron(a.W-m-a.u(20), y+rh/2, a.u(40), 1, a.u(7), inkBlack)
			c.Fill(image.Rect(m, y+rh-a.u(2), a.W-m, y+rh), inkRule)
			if visible {
				a.hits = append([]hit{{r: row, fn: r.fn, name: r.name}}, a.hits...)
			}
		case srRadio, srStepper:
			ink := uint8(inkBlack)
			if r.disabled {
				ink = inkRule
			}
			cx, cy := m+a.u(50), y+rh/2
			lx := m + a.u(110)
			if r.plain {
				lx = m + a.u(20)
			} else {
				c.Ring(cx, cy, a.u(26), a.u(4), ink)
				if r.sel {
					c.Disc(cx, cy, a.u(14), ink)
				}
			}
			ls.Gray = ink
			if r.typ == srRadio {
				a.Fonts.Draw(c, lx, a.textBaseline(y, rh, ls.Size), r.label, ls)
			} else {
				a.Fonts.Draw(c, lx, a.textBaseline(y, rh, ls.Size), r.label, ls)
				x := lx + a.Fonts.Width(r.label, ls) + a.u(36)
				bsz := a.u(96)
				minus := image.Rect(x, cy-bsz/2, x+bsz, cy+bsz/2)
				c.Frame(minus, a.u(4), ink)
				c.Minus(minus.Min.X+bsz/2, cy, a.u(44), a.u(7), ink)
				vs := a.st(48, true, ink)
				vt := fmt.Sprintf("%d", r.val)
				vw := a.u(170)
				vx := minus.Max.X
				a.Fonts.Draw(c, vx+(vw-a.Fonts.Width(vt, vs))/2, a.textBaseline(y, rh, vs.Size), vt, vs)
				plus := image.Rect(vx+vw, cy-bsz/2, vx+vw+bsz, cy+bsz/2)
				c.Frame(plus, a.u(4), ink)
				c.Plus(plus.Min.X+bsz/2, cy, a.u(44), a.u(7), ink)
				us := a.st(40, false, inkDim)
				a.Fonts.Draw(c, plus.Max.X+a.u(24), a.textBaseline(y, rh, us.Size), r.unit, us)
				if visible && !r.disabled {
					if r.noRepeat {
						a.addNamedHit(r.name+"-minus", minus, r.minus)
						a.addNamedHit(r.name+"-plus", plus, r.inc)
					} else {
						a.addNamedRepeatHit(r.name+"-minus", minus, r.minus)
						a.addNamedRepeatHit(r.name+"-plus", plus, r.inc)
					}
				}
			}
			c.Fill(image.Rect(m, y+rh-a.u(2), a.W-m, y+rh), inkRule)
			if visible && !r.disabled {
				// the row itself is registered first, so the stepper buttons win
				a.hits = append([]hit{{r: row, fn: r.fn, name: r.name}}, a.hits...)
			}
		}
		y += rh
	}
	// the bar goes on top of whatever scrolled under it (and the reserved strip above it stays blank)
	c.Fill(image.Rect(0, 0, a.W, a.barRect().Min.Y), paperGray)
	title := "Settings"
	switch {
	case sv.about:
		title = "About"
	case sv.adv:
		title = "Advanced"
	case sv.hot > 0:
		title = "Hot corners"
	}
	a.drawHeader(hdr{title: title, back: true, home: true, homeActive: true, account: true})
	a.drawPopover()
}

// settingsScroll scrolls the list by most of a screen (sy < 0: finger moved
// up, the list moves up).
func (a *App) settingsScroll(sy int) {
	sv := a.sv
	if sv == nil {
		return
	}
	step := (a.H - a.barRect().Max.Y) * 7 / 10
	old := sv.scroll
	sv.scroll -= sy * step
	a.drawSettings() // clamps
	if sv.scroll != old {
		// the bar on top is unchanged: only the list below it is sent
		a.present(image.Rect(0, a.barRect().Max.Y, a.W, a.H), a.uiMode(), false, "settings-scroll")
	}
}

// dpiStep is how far one tap on the DPI stepper moves the value.
const dpiStep = 5
