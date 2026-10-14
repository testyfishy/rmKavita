package ui

import (
	"image"
)

// The chrome is one slim bar: Back and Home on the left, the title, then
// Search, Settings and the Account icon on the right. Logout and Exit live in
// the Account popover. While reading, the same bar is part of the overlay.

// hdr says what the bar shows.
type hdr struct {
	title      string
	back       bool
	home       bool
	homeActive bool
	search     bool
	gear       bool
	account    bool
	close      bool // sign-in: a plain Exit cross instead of the account icon
}

func (a *App) headerTop() int { return a.u(100) } // the top strip stays free for AppLoad's own edge swipe
func (a *App) headerH() int   { return a.u(130) }
func (a *App) margin() int    { return a.u(40) }

// barRect is the bar's rectangle.
func (a *App) barRect() image.Rectangle {
	return image.Rect(0, a.headerTop(), a.W, a.headerTop()+a.headerH())
}

// drawHeader paints the bar and registers its buttons.
func (a *App) drawHeader(h hdr) {
	c := a.cv
	bar := a.barRect()
	top, bh := bar.Min.Y, bar.Dy()
	c.Fill(image.Rect(0, 0, a.W, bar.Max.Y), paperGray) // the strip above the bar included
	c.Fill(image.Rect(0, bar.Max.Y-a.u(3), a.W, bar.Max.Y), inkBlack)
	btn := bh
	cy := top + bh/2
	x := 0
	if h.back {
		c.Chevron(x+btn/2, cy, a.u(52), -1, a.u(8), inkBlack)
		a.addNamedHit("back", image.Rect(x, top, x+btn, top+bh), a.back)
		x += btn
	}
	if h.home {
		ink := uint8(inkBlack)
		if !h.homeActive {
			ink = inkRule
		}
		c.House(x+btn/2, cy, a.u(62), a.u(7), ink)
		if h.homeActive {
			a.addNamedHit("home", image.Rect(x, top, x+btn, top+bh), a.goHome)
		}
		x += btn
	}
	right := a.W
	if h.close {
		c.Cross(right-btn/2, cy, a.u(44), a.u(8), inkBlack)
		a.addNamedHit("close", image.Rect(right-btn, top, right, top+bh), func() { a.quit("close button") })
		right -= btn
	}
	if h.account {
		c.User(right-btn/2, cy, a.u(62), a.u(7), inkBlack)
		a.addNamedHit("account", image.Rect(right-btn, top, right, top+bh), a.togglePopover)
		right -= btn
	}
	if h.gear {
		c.Gear(right-btn/2, cy, a.u(66), a.u(6), inkBlack)
		a.addNamedHit("settings", image.Rect(right-btn, top, right, top+bh), a.openSettings)
		right -= btn
	}
	if h.search {
		c.Magnifier(right-btn/2, cy, a.u(60), a.u(7), inkBlack)
		a.addNamedHit("search", image.Rect(right-btn, top, right, top+bh), a.openSearch)
		right -= btn
	}
	if x == 0 {
		x = a.margin()
	}
	s := a.st(48, true, inkBlack)
	t := a.Fonts.Ellipsize(h.title, right-x-a.margin(), s)
	a.Fonts.Draw(c, x, a.textBaseline(top, bh, s.Size), t, s)
}

func (a *App) textBaseline(top, h, size int) int {
	return top + h/2 + size*35/100
}

// ---- account popover ------------------------------------------------------------

type popover struct {
	open    bool
	confirm bool // the Logout row is armed: the next tap on it signs out
}

func (a *App) popRect() image.Rectangle {
	w := a.u(560)
	rowH := a.u(130)
	x1 := a.W - a.margin()
	y0 := a.barRect().Max.Y + a.u(6)
	return image.Rect(x1-w, y0, x1, y0+2*rowH)
}

// drawPopover paints the popover over whatever is on the canvas and adds its
// hits (a full-screen dismiss first, so the rows win inside their rectangle).
func (a *App) drawPopover() {
	if !a.pop.open {
		return
	}
	c := a.cv
	r := a.popRect()
	rowH := r.Dy() / 2
	a.addNamedHit("pop-dismiss", image.Rect(0, 0, a.W, a.H), func() {
		a.pop = popover{}
		a.afterChromeChange()
	})
	c.Fill(r, paperGray)
	c.Frame(r, a.u(4), inkBlack)
	rows := []struct {
		name, label string
		fn          func()
	}{
		{"logout", "Logout", a.onLogoutTap},
		{"exit", "Exit", func() { a.quit("exit") }},
	}
	if a.pop.confirm {
		rows[0].label = "Tap again to log out"
	}
	s := a.st(46, true, inkBlack)
	for i, row := range rows {
		rr := image.Rect(r.Min.X, r.Min.Y+i*rowH, r.Max.X, r.Min.Y+(i+1)*rowH)
		if i == 0 && a.pop.confirm {
			c.Fill(rr.Inset(a.u(4)), inkBlack)
			s2 := s
			s2.Gray = paperGray
			a.Fonts.Draw(c, rr.Min.X+a.u(36), a.textBaseline(rr.Min.Y, rowH, s.Size), a.Fonts.Ellipsize(row.label, rr.Dx()-a.u(72), s2), s2)
		} else {
			a.Fonts.Draw(c, rr.Min.X+a.u(36), a.textBaseline(rr.Min.Y, rowH, s.Size), row.label, s)
		}
		if i == 0 {
			c.Fill(image.Rect(rr.Min.X, rr.Max.Y-a.u(2), rr.Max.X, rr.Max.Y), inkRule)
		}
		a.addNamedHit(row.name, rr, row.fn)
	}
}

func (a *App) togglePopover() {
	a.pop = popover{open: !a.pop.open}
	a.afterChromeChange()
}

func (a *App) onLogoutTap() {
	if !a.pop.confirm {
		a.pop.confirm = true
		a.afterChromeChange()
		return
	}
	a.pop = popover{}
	a.logout("Signed out")
}

// closePopover shuts the popover without redrawing (the caller redraws).
func (a *App) closePopover() { a.pop = popover{} }
