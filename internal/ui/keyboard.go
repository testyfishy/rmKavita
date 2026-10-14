package ui

import (
	"image"
	"strings"
)

// The on-screen keyboard is shared by Sign in and Search. It has a letters
// layer (with a one-shot shift and a caps lock) and a symbols layer, ten
// columns wide, big keys.

type kbKind int

const (
	kkText kbKind = iota
	kkShift
	kkBackspace
	kkLayer
	kkEnter
	kkSpace
)

// kbd is the keyboard's own state (layer and shift), not the typed text.
type kbd struct {
	layer int // 0 letters, 1 symbols
	shift int // 0 off, 1 next letter, 2 caps lock
}

type kbKeyDef struct {
	label string
	text  string
	units float64
	kind  kbKind
}

type kbKeyRect struct {
	kbKeyDef
	r image.Rectangle
}

func textKeys(s string) []kbKeyDef {
	var out []kbKeyDef
	for _, r := range s {
		out = append(out, kbKeyDef{label: string(r), text: string(r), units: 1, kind: kkText})
	}
	return out
}

func kbRowsFor(layer int, enter string) [][]kbKeyDef {
	if layer == 1 {
		return [][]kbKeyDef{
			textKeys("1234567890"),
			textKeys("!@#$%^&*()"),
			textKeys(`-_=+[]{}\|`),
			textKeys(`;:'",./?<>`),
			{
				{label: "abc", units: 1.5, kind: kkLayer},
				{label: "~", text: "~", units: 1, kind: kkText},
				{label: "`", text: "`", units: 1, kind: kkText},
				{label: "space", text: " ", units: 2.5, kind: kkSpace},
				{label: "<-", units: 1.5, kind: kkBackspace},
				{label: enter, units: 2.5, kind: kkEnter},
			},
		}
	}
	r4 := []kbKeyDef{{label: "shift", units: 1.5, kind: kkShift}}
	r4 = append(r4, textKeys("zxcvbnm")...)
	r4 = append(r4, kbKeyDef{label: "<-", units: 1.5, kind: kkBackspace})
	return [][]kbKeyDef{
		textKeys("1234567890"),
		textKeys("qwertyuiop"),
		textKeys("asdfghjkl'"),
		r4,
		{
			{label: "?123", units: 1.5, kind: kkLayer},
			{label: "@", text: "@", units: 1, kind: kkText},
			{label: ".", text: ".", units: 1, kind: kkText},
			{label: "space", text: " ", units: 3, kind: kkSpace},
			{label: "-", text: "-", units: 1, kind: kkText},
			{label: enter, units: 2.5, kind: kkEnter},
		},
	}
}

// kbLayout places the keys: the keyboard fills the width, its five rows end at
// bottom and are kh tall.
func (a *App) kbLayout(k *kbd, bottom, kh int, enter string) []kbKeyRect {
	m := a.margin()
	gap := a.u(14)
	uw := float64(a.W-2*m-9*gap) / 10
	rows := kbRowsFor(k.layer, enter)
	total := 5*kh + 4*gap
	y := bottom - total
	var out []kbKeyRect
	for _, row := range rows {
		x := float64(m)
		for _, kd := range row {
			w := kd.units*uw + (kd.units-1)*float64(gap)
			r := image.Rect(int(x+0.5), y, int(x+w+0.5), y+kh)
			out = append(out, kbKeyRect{kd, r})
			x += w + float64(gap)
		}
		y += kh + gap
	}
	return out
}

// kbTop is the y of the keyboard's first row.
func (a *App) kbTop(bottom, kh int) int { return bottom - (5*kh + 4*a.u(14)) }

// drawKeyboard paints the keys and registers their hits. onKey receives the
// character (already shifted), "\b" or "\n".
func (a *App) drawKeyboard(k *kbd, bottom, kh int, enter string, onKey func(string)) {
	c := a.cv
	ks := a.st(54, true, inkBlack)
	small := a.st(40, true, inkBlack)
	for _, kr := range a.kbLayout(k, bottom, kh, enter) {
		kr := kr
		r := kr.r
		label := kr.label
		style := ks
		filled := false
		switch kr.kind {
		case kkShift:
			style = small
			label = "shift"
			filled = k.shift != 0
			if k.shift == 2 {
				label = "SHIFT"
			}
		case kkLayer, kkSpace, kkEnter:
			style = small
		case kkBackspace:
			style = small
		case kkText:
			if k.shift != 0 && k.layer == 0 {
				label = strings.ToUpper(label)
			}
		}
		if filled {
			c.Fill(r, inkBlack)
			style.Gray = paperGray
		} else {
			c.Frame(r, a.u(4), inkBlack)
		}
		if kr.kind == kkBackspace {
			a.drawBackspace(r, filled)
		} else {
			lw := a.Fonts.Width(label, style)
			a.Fonts.Draw(c, r.Min.X+(r.Dx()-lw)/2, a.textBaseline(r.Min.Y, r.Dy(), style.Size), label, style)
		}
		name := "key-" + kr.label
		switch kr.kind {
		case kkText:
			txt := kr.text
			a.addNamedHit(name, r, func() {
				t := txt
				if k.shift != 0 && k.layer == 0 {
					t = strings.ToUpper(t)
				}
				onKey(t)
			})
		case kkSpace:
			a.addNamedHit("key-space", r, func() { onKey(" ") })
		case kkEnter:
			a.addNamedHit("key-enter", r, func() { onKey("\n") })
		case kkBackspace:
			a.addNamedRepeatHit("key-bs", r, func() { onKey("\b") })
		case kkShift:
			a.addNamedHit("key-shift", r, func() { onKey("\x01shift") })
		case kkLayer:
			a.addNamedHit("key-layer", r, func() { onKey("\x01layer") })
		}
	}
}

func (a *App) drawBackspace(r image.Rectangle, filled bool) {
	ink := uint8(inkBlack)
	if filled {
		ink = paperGray
	}
	cx, cy := r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2
	w := a.u(52)
	a.cv.Chevron(cx-w/4, cy, a.u(48), -1, a.u(7), ink)
	a.cv.Line(cx-w/4, cy, cx+w/2, cy, a.u(7), ink)
}

// kbApply handles the keyboard-internal pseudo keys. It reports whether the
// key was consumed (and the keyboard must be redrawn).
func (k *kbd) apply(key string) bool {
	switch key {
	case "\x01shift":
		k.shift = (k.shift + 1) % 3
		return true
	case "\x01layer":
		k.layer = 1 - k.layer
		if k.layer == 1 {
			k.shift = 0
		}
		return true
	}
	return false
}

// afterChar relaxes a one-shot shift once a character was typed; it reports a
// change that needs the keys redrawn.
func (k *kbd) afterChar() bool {
	if k.shift == 1 {
		k.shift = 0
		return true
	}
	return false
}
