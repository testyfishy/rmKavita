package ui

import (
	"context"
	"image"
	"strings"
	"time"

	"rmkavita/internal/opds"
)

type keyboard struct {
	text string
	busy bool
	err  *opds.Error
	k    kbd
}

func (a *App) openSearch() {
	if a.searchURL == "" || a.screen() != scrList {
		return
	}
	a.closePopover()
	a.kb = &keyboard{}
	a.render("search")
}

// searchGeom: the query box and the keyboard.
func (a *App) searchGeom() (box image.Rectangle, kbBottom, kh int) {
	m := a.margin()
	qy := a.barRect().Max.Y + a.u(50)
	box = image.Rect(m, qy, a.W-m, qy+a.u(140))
	kbBottom = a.H - a.u(100)
	kh = clampInt((kbBottom-box.Max.Y-a.u(120)-4*a.u(14))/5, a.u(88), a.u(150))
	return
}

func (a *App) drawSearch() {
	c := a.cv
	k := a.kb
	a.layerActive = false
	a.opaque = a.opaque[:0]
	c.Fill(a.full(), paperGray)
	a.hits = a.hits[:0]
	a.drawHeader(hdr{title: "Search", back: true, home: true, homeActive: true, gear: true, account: true})

	box, kbBottom, kh := a.searchGeom()
	m := a.margin()
	c.Frame(box, a.u(5), inkBlack)
	qs := a.st(58, false, inkBlack)
	text := k.text
	if k.busy {
		text = "Searching"
		qs.Gray = inkDim
	}
	text = a.Fonts.Ellipsize(text, box.Dx()-a.u(60), qs)
	w := a.Fonts.Draw(c, box.Min.X+a.u(30), a.textBaseline(box.Min.Y, box.Dy(), qs.Size), text, qs)
	if !k.busy {
		cx := box.Min.X + a.u(30) + w + a.u(6)
		c.Fill(image.Rect(cx, box.Min.Y+a.u(30), cx+a.u(5), box.Max.Y-a.u(30)), inkBlack)
	}
	if k.err != nil {
		es := a.st(34, false, inkDim)
		a.Fonts.Draw(c, m, box.Max.Y+a.u(60), k.err.Short(), es)
	}
	a.drawKeyboard(&k.k, kbBottom, kh, "Search", a.typeText)
	a.drawPopover()
}

func (a *App) searchKey(t string) {
	k := a.kb
	if k == nil || k.busy || t == "" {
		return
	}
	box, kbBottom, kh := a.searchGeom()
	kbRect := image.Rect(0, a.kbTop(kbBottom, kh), a.W, kbBottom)
	if k.k.apply(t) {
		a.drawSearch()
		a.present(kbRect, a.uiMode(), false, "search-keys")
		return
	}
	switch t {
	case "\b":
		rs := []rune(k.text)
		if len(rs) > 0 {
			k.text = string(rs[:len(rs)-1])
		}
	case "\n":
		a.kbSubmit()
		return
	default:
		if len([]rune(k.text)) < 60 {
			k.text += t
		}
	}
	k.err = nil
	shifted := k.k.afterChar()
	a.drawSearch()
	r := box.Inset(-a.u(10)).Union(image.Rect(0, box.Max.Y, a.W, box.Max.Y+a.u(100)))
	if shifted {
		r = r.Union(kbRect)
	}
	a.present(r, a.uiMode(), false, "search-key")
}

func (a *App) kbSubmit() {
	k := a.kb
	if k == nil || k.busy {
		return
	}
	q := strings.TrimSpace(k.text)
	if q == "" {
		return
	}
	k.busy = true
	a.render("searching")
	desc := a.searchURL
	sctx := a.sctx
	a.async(func() any {
		ctx, cancel := context.WithTimeout(sctx, 30*time.Second)
		defer cancel()
		b, _, err := a.Cl.Get(ctx, desc, 128<<10)
		if err != nil {
			return searchMsg{q: q, err: errOf(err)}
		}
		tmpl, perr := opds.SearchURL(b, q)
		if perr != nil {
			return searchMsg{q: q, err: &opds.Error{Kind: opds.KindData, Msg: perr.Error()}}
		}
		return searchMsg{q: q, url: a.resolve(tmpl, desc), title: "Search: " + q}
	})
}

func (a *App) onSearch(m searchMsg) {
	k := a.kb
	if k == nil {
		return
	}
	k.busy = false
	if m.err != nil {
		if m.err.Kind == opds.KindAuth {
			a.confirmAuth()
		}
		k.err = m.err
		a.render("search-error")
		return
	}
	a.kb = nil
	nv := &listView{title: m.title, url: m.url, loading: true, search: true}
	a.pushList(nv)
	a.loadList(nv, false)
	a.render("search-results")
}
