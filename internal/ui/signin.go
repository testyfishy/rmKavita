package ui

import (
	"context"
	"image"
	"net/url"
	"strings"
	"time"

	"rmkavita/internal/opds"
	"rmkavita/internal/store"
)

// signin is the Sign-in screen: server, user name and password typed on the
// on-screen keyboard. The password lives in a rune buffer that is wiped as
// soon as the request has been built; only the key Kavita returns is kept.
type signin struct {
	server, user, pass []rune
	focus              int // 0 server, 1 user name, 2 password
	show               bool
	busy               bool
	short, detail      string // the last failure
	notice             string // why this screen is shown ("Signed out")
	k                  kbd
	started            time.Time
}

const (
	maxServerLen = 160
	maxUserLen   = 80
	maxPassLen   = 160
)

func (a *App) openSignin(notice string) {
	s := &signin{notice: notice}
	s.server = []rune(a.Cfg.Server)
	s.user = []rune(a.Cfg.Username)
	switch {
	case len(s.server) == 0:
		s.focus = 0
	case len(s.user) == 0:
		s.focus = 1
	default:
		s.focus = 2
	}
	a.si = s
	a.pop = popover{}
}

// signinGeom is the screen's layout; the tests use it to find keys.
type signinGeom struct {
	fields   [3]image.Rectangle
	toggle   image.Rectangle
	msgY     int
	button   image.Rectangle
	kbBottom int
	kh       int
}

func (a *App) signinGeometry() signinGeom {
	var g signinGeom
	m := a.margin()
	top := a.barRect().Max.Y + a.u(24)
	fh, gap := a.u(116), a.u(20)
	y := top
	for i := 0; i < 3; i++ {
		g.fields[i] = image.Rect(m, y, a.W-m, y+fh)
		y += fh + gap
	}
	g.msgY = y + a.u(40)
	y += a.u(64)
	g.button = image.Rect(m, y, a.W-m, y+a.u(124))
	g.kbBottom = a.H - a.u(60)
	avail := g.kbBottom - (g.button.Max.Y + a.u(30)) - 4*a.u(14)
	g.kh = clampInt(avail/5, a.u(88), a.u(150))
	pb := g.fields[2]
	tw := a.u(200)
	g.toggle = image.Rect(pb.Max.X-tw, pb.Min.Y, pb.Max.X, pb.Max.Y)
	return g
}

func (s *signin) field(i int) *[]rune {
	switch i {
	case 0:
		return &s.server
	case 1:
		return &s.user
	}
	return &s.pass
}

func (a *App) drawSignin() {
	c := a.cv
	s := a.si
	g := a.signinGeometry()
	a.layerActive = false
	a.opaque = a.opaque[:0]
	c.Fill(a.full(), paperGray)
	a.hits = a.hits[:0]
	a.drawHeader(hdr{title: "Sign in", close: true})

	labels := [3]string{"Server", "Username", "Password"}
	names := [3]string{"field-server", "field-user", "field-pass"}
	ls := a.st(34, false, inkDim)
	ts := a.st(48, false, inkBlack)
	for i := 0; i < 3; i++ {
		i := i
		r := g.fields[i]
		th := a.u(3)
		if s.focus == i {
			th = a.u(8)
		}
		c.Frame(r, th, inkBlack)
		a.Fonts.Draw(c, r.Min.X+a.u(24), a.textBaseline(r.Min.Y, r.Dy(), ls.Size)-a.u(2), labels[i], ls)
		x := r.Min.X + a.u(190)
		right := r.Max.X - a.u(24)
		if i == 2 {
			right = g.toggle.Min.X - a.u(10)
		}
		text := string(*s.field(i))
		if i == 2 && !s.show {
			text = strings.Repeat("•", len(*s.field(i)))
		}
		var w int
		if text == "" && i == 0 && s.focus != 0 {
			ph := a.st(44, false, inkRule)
			a.Fonts.Draw(c, x, a.textBaseline(r.Min.Y, r.Dy(), ph.Size), "http://host:port", ph)
		} else {
			// keep the end of long text visible
			for a.Fonts.Width(text, ts) > right-x-a.u(12) && len(text) > 1 {
				text = text[1:]
				for len(text) > 0 && text[0]&0xC0 == 0x80 { // do not cut inside a UTF-8 sequence
					text = text[1:]
				}
			}
			w = a.Fonts.Draw(c, x, a.textBaseline(r.Min.Y, r.Dy(), ts.Size), text, ts)
		}
		if s.focus == i && !s.busy {
			cx := x + w + a.u(4)
			c.Fill(image.Rect(cx, r.Min.Y+a.u(26), cx+a.u(5), r.Max.Y-a.u(26)), inkBlack)
		}
		a.addNamedHit(names[i], r, func() { a.signinFocus(i) })
	}
	// show / hide toggle inside the password box
	{
		r := g.toggle
		c.Fill(image.Rect(r.Min.X, r.Min.Y+a.u(8), r.Min.X+a.u(3), r.Max.Y-a.u(8)), inkRule)
		lab := "Show"
		if s.show {
			lab = "Hide"
		}
		bs := a.st(40, true, inkBlack)
		w := a.Fonts.Width(lab, bs)
		a.Fonts.Draw(c, r.Min.X+(r.Dx()-w)/2, a.textBaseline(r.Min.Y, r.Dy(), bs.Size), lab, bs)
		a.addNamedHit("pass-toggle", r, func() {
			s.show = !s.show
			a.signinRedraw(g.fields[2])
		})
	}
	// message line
	{
		ms := a.st(38, true, inkBlack)
		ds := a.st(32, false, inkDim)
		line, detail := s.short, s.detail
		if line == "" {
			line = s.notice
			ms = a.st(38, false, inkDim)
		}
		if line != "" {
			t := a.Fonts.Ellipsize(line, a.W-2*a.margin(), ms)
			a.Fonts.Draw(c, a.margin(), g.msgY, t, ms)
			if detail != "" {
				t := a.Fonts.Ellipsize(a.Cl.Redact(detail), a.W-2*a.margin(), ds)
				a.Fonts.Draw(c, a.margin(), g.msgY+a.u(40), t, ds)
			}
		}
	}
	// the Sign in button
	{
		r := g.button
		label := "Sign in"
		if s.busy {
			label = "Signing in"
		}
		c.Frame(r, a.u(6), inkBlack)
		bs := a.st(52, true, inkBlack)
		w := a.Fonts.Width(label, bs)
		a.Fonts.Draw(c, r.Min.X+(r.Dx()-w)/2, a.textBaseline(r.Min.Y, r.Dy(), bs.Size), label, bs)
		a.addNamedHit("signin", r, a.signinSubmit)
	}
	enter := "Next"
	if s.focus == 2 {
		enter = "Sign in"
	}
	a.drawKeyboard(&s.k, g.kbBottom, g.kh, enter, a.typeText)
	a.drawPopover()
}

// signinRedraw redraws the screen and updates only the given region.
func (a *App) signinRedraw(r image.Rectangle) {
	a.drawSignin()
	a.present(r, a.uiMode(), false, "signin")
}

func (a *App) signinFocus(i int) {
	s := a.si
	if s == nil || s.busy || s.focus == i {
		return
	}
	s.focus = i
	a.drawSignin()
	g := a.signinGeometry()
	// the focus frames and the Enter key's label changed
	r := g.fields[0].Union(g.fields[2]).Union(image.Rect(0, a.kbTop(g.kbBottom, g.kh), a.W, g.kbBottom))
	a.present(r, a.uiMode(), false, "signin-focus")
}

// typeText routes a typed key (from the on-screen keyboard or a test backend)
// to the screen that takes text.
func (a *App) typeText(t string) {
	switch a.screen() {
	case scrSignin:
		a.signinKey(t)
	case scrSearch:
		a.searchKey(t)
	}
}

func (a *App) signinKey(t string) {
	s := a.si
	if s == nil || s.busy || t == "" {
		return
	}
	g := a.signinGeometry()
	kbRect := image.Rect(0, a.kbTop(g.kbBottom, g.kh), a.W, g.kbBottom)
	if s.k.apply(t) {
		a.drawSignin()
		a.present(kbRect, a.uiMode(), false, "signin-keys")
		return
	}
	switch t {
	case "\b":
		f := s.field(s.focus)
		if n := len(*f); n > 0 {
			(*f)[n-1] = 0
			*f = (*f)[:n-1]
		}
	case "\n":
		if s.focus < 2 {
			a.signinFocus(s.focus + 1)
		} else {
			a.signinSubmit()
		}
		return
	default:
		f := s.field(s.focus)
		lim := []int{maxServerLen, maxUserLen, maxPassLen}[s.focus]
		for _, r := range t {
			if len(*f) < lim {
				*f = append(*f, r)
			}
		}
	}
	s.short, s.detail, s.notice = "", "", ""
	shifted := s.k.afterChar()
	a.drawSignin()
	r := g.fields[s.focus].Union(image.Rect(0, g.msgY-a.u(40), a.W, g.msgY+a.u(60)))
	if shifted {
		r = r.Union(kbRect)
	}
	a.present(r, a.uiMode(), false, "signin-key")
}

// failure texts for the Sign-in screen
func (a *App) loginText(e *opds.Error) (short, detail string) {
	switch e.Kind {
	case opds.KindBadLogin:
		if e.Status == 403 {
			return "This account may not sign in", ""
		}
		return "Wrong username or password", ""
	case opds.KindLocked:
		return "Account locked for a while", e.Msg
	case opds.KindDoor:
		return "Cannot reach the server", "Nothing is listening at that address"
	case opds.KindNetwork:
		return "The server is not answering", e.Msg
	case opds.KindNotFound:
		return "Not a Kavita address", "Check the server address"
	case opds.KindAuth:
		return "Signed in, but the key was refused", ""
	case opds.KindServer:
		return e.Short(), e.Msg
	}
	return "Unexpected answer from the server", e.Msg
}

func (a *App) signinSubmit() {
	s := a.si
	if s == nil || s.busy {
		return
	}
	g := a.signinGeometry()
	fail := func(short string, focus int) {
		s.short, s.detail, s.notice = short, "", ""
		s.focus = focus
		a.drawSignin()
		a.present(a.full(), a.uiMode(), false, "signin-error")
	}
	srv, err := store.NormalizeServer(string(s.server))
	if err != nil {
		fail(err.Error(), 0)
		return
	}
	user := strings.TrimSpace(string(s.user))
	if user == "" {
		fail("Enter your username", 1)
		return
	}
	if len(s.pass) == 0 {
		fail("Enter your password", 2)
		return
	}
	pw := string(s.pass)
	for i := range s.pass { // the buffer is wiped; the request owns the only copy
		s.pass[i] = 0
	}
	s.pass = s.pass[:0]
	s.server = []rune(srv)
	s.short, s.detail, s.notice = "", "", ""
	s.busy = true
	s.show = false
	s.started = time.Now()
	a.drawSignin()
	a.present(g.fields[0].Union(g.button), a.uiMode(), false, "signin-busy")
	base, _ := url.Parse(srv)
	a.Cl.AddSecret(pw)
	ctxBase := a.ctx
	a.async(func() any {
		ctx, cancel := context.WithTimeout(ctxBase, 25*time.Second)
		defer cancel()
		res, e := a.Cl.Login(ctx, base, user, pw)
		if e != nil {
			return loginMsg{si: s, err: e}
		}
		// Confirm the key works before it is saved.
		if e := a.Cl.VerifyKey(ctx, base, res.Key); e != nil {
			return loginMsg{si: s, err: e}
		}
		return loginMsg{si: s, server: srv, user: res.Username, key: res.Key}
	})
}

func (a *App) onLogin(m loginMsg) {
	s := a.si
	if s == nil || s != m.si {
		return
	}
	s.busy = false
	if m.err != nil {
		a.logf("sign-in failed: kind=%d status=%d", m.err.Kind, m.err.Status)
		s.short, s.detail = a.loginText(m.err)
		s.focus = 2
		a.drawSignin()
		a.present(a.full(), a.uiMode(), false, "signin-error")
		return
	}
	if a.confPath != "" {
		if err := store.SaveSignIn(a.confPath, m.server, m.user, m.key); err != nil {
			a.logf("could not save the sign-in: %v", err)
		}
	}
	a.logf("signed in (%d ms)", time.Since(s.started).Milliseconds())
	a.si = nil
	a.startSession(m.server, m.user, m.key)
}
