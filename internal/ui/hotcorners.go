package ui

import (
	"rmkavita/internal/opds"
	"rmkavita/internal/store"
)

// Hot corners: holding a screen corner for hold_ms does what the corner is set
// to (Settings -> Hot corners). The defaults are the v0.3 behaviour: top-right
// clears the screen, bottom-left quits, the other two do nothing.

const hotNone = store.HotNone

// hotLabels are the chooser's rows: the config word and what the user reads.
var hotLabels = map[string]string{
	store.HotNone:        "Nothing",
	store.HotQuit:        "Quit the app",
	store.HotClear:       "Clear the screen",
	store.HotHome:        "Home (libraries)",
	store.HotSettings:    "Settings",
	store.HotControls:    "Show / hide the controls",
	store.HotFirst:       "First page",
	store.HotLast:        "Last page",
	store.HotNextChapter: "Next chapter",
	store.HotPrevChapter: "Previous chapter",
	store.HotColour:      "Colour / black and white",
	store.HotDirection:   "Reading direction",
	store.HotFit:         "Fit: width / height / both",
	store.HotFooter:      "Footer on / off",
}

// cornerKeys are the config keys and row names of the four corners, in the
// order the screen lists them.
var cornerKeys = []struct {
	c     corner
	key   string
	label string
}{
	{cornerTL, "hot_tl", "Top left"},
	{cornerTR, "hot_tr", "Top right"},
	{cornerBL, "hot_bl", "Bottom left"},
	{cornerBR, "hot_br", "Bottom right"},
}

// hotAction is what holding corner c does.
func (a *App) hotAction(c corner) string {
	switch c {
	case cornerTL:
		return a.Cfg.HotTL
	case cornerTR:
		return a.Cfg.HotTR
	case cornerBL:
		return a.Cfg.HotBL
	case cornerBR:
		return a.Cfg.HotBR
	}
	return hotNone
}

func (a *App) setHotAction(c corner, act string) {
	switch c {
	case cornerTL:
		a.Cfg.HotTL = act
	case cornerTR:
		a.Cfg.HotTR = act
	case cornerBL:
		a.Cfg.HotBL = act
	case cornerBR:
		a.Cfg.HotBR = act
	}
}

// holdWanted reports whether a press in corner c should arm the hold timer: the
// corner has a function, and the screen is not one where the keyboard sits in
// the corners (Sign in, Search) or that has its own way out.
func (a *App) holdWanted(c corner) bool {
	if a.hotAction(c) == hotNone {
		return false
	}
	switch a.screen() {
	case scrSignin, scrSearch:
		return false
	}
	return true
}

// runHot performs a hot-corner action.
func (a *App) runHot(act, how string) {
	a.logf("hot corner (%s): %s", how, act)
	r := a.rd
	inReader := a.screen() == scrReader && r != nil
	switch act {
	case store.HotQuit:
		a.quit(how)
	case store.HotClear:
		a.logf("deliberate clear (corner hold)")
		a.render("clear")
		a.clearScreen("clear")
	case store.HotHome:
		a.goHome()
	case store.HotSettings:
		if a.screen() != scrSettings {
			a.openSettings()
		}
	case store.HotControls:
		if !inReader {
			return
		}
		if r.overlay {
			a.hideOverlay()
		} else {
			a.showOverlay()
		}
	case store.HotFirst:
		if inReader && !r.unknown {
			a.readerJump(0)
		}
	case store.HotLast:
		if inReader && !r.unknown {
			a.readerJump(r.count - 1)
		}
	case store.HotNextChapter:
		if inReader {
			a.nextChapter()
		}
	case store.HotPrevChapter:
		if inReader {
			a.prevChapter()
		}
	case store.HotColour:
		if a.speedMode() {
			// Speed is black and white: the Colour setting is left as it is, so
			// that it is still the user's choice when Speed is left
			a.logf("hot corner: colour is off in Speed mode (setting unchanged)")
			return
		}
		a.Set.Colour = !a.Set.Colour
		a.settingChangedOutside(true)
	case store.HotDirection:
		if r != nil && r.doc == opds.DocPDF {
			return // a PDF has no direction
		}
		a.Set.RTL = !a.Set.RTL
		a.settingChangedOutside(false)
	case store.HotFit:
		doc := opds.DocComic
		if r != nil {
			doc = r.doc
		}
		p := a.prefsFor(doc)
		switch p.Fit {
		case store.FitWidth:
			p.Fit = store.FitHeight
		case store.FitHeight:
			p.Fit = store.FitBoth
		default:
			p.Fit = store.FitWidth
		}
		a.settingChangedOutside(a.affects(doc))
	case store.HotFooter:
		a.Set.Footer = !a.Set.Footer
		a.settingChangedOutside(true)
	}
}

// settingChangedOutside applies a setting that was changed by a hot corner (the
// Settings screen is not necessarily open): save, refit the open book when
// pages is true, and redraw what is on screen.
func (a *App) settingChangedOutside(pages bool) {
	a.Set.Sanitize()
	a.setDirty = true
	a.flushSettings()
	switch {
	case a.screen() == scrReader && a.rd != nil:
		if pages {
			a.invalidatePages()
		}
		a.composeReader()
		a.present(a.full(), a.uiMode(), false, "hot")
	case a.screen() == scrSettings:
		a.redrawSettings(false)
	default:
		a.render("hot")
	}
}

// hotSet stores a corner's function in memory and in rmkavita.conf.
func (a *App) hotSet(c corner, key, act string) {
	a.setHotAction(c, act)
	if a.confPath != "" {
		if err := store.UpdateConf(a.confPath, map[string]string{key: act}, nil); err != nil {
			a.logf("could not save %s: %v", key, err)
		}
	}
}
