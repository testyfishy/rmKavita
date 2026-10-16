package ui

import (
	"context"
	"image"

	"rmkavita/internal/display"
	"rmkavita/internal/gfx"
)

// ShowMessage draws a one-screen notice (configuration problems, unsupported
// device) with a Close button and waits for it, for the corner hold, for the
// display to go away or for ctx to end. It is used before the app proper can
// start, so the user sees a reason instead of a window that vanishes.
func ShowMessage(ctx context.Context, d display.Display, fonts *gfx.Fonts, lines ...string) {
	w, h := d.Size()
	k := float64(w) / 1620
	u := func(v int) int { return int(float64(v)*k + 0.5) }
	c := gfx.NewCanvas(w, h)
	big := gfx.Style{Size: u(54), Bold: true, Gray: 0}
	small := gfx.Style{Size: u(38), Gray: 70}
	y := h/2 - u(120)
	for i, l := range lines {
		s := small
		if i == 0 {
			s = big
		}
		t := fonts.Ellipsize(l, w-u(120), s)
		tw := fonts.Width(t, s)
		fonts.Draw(c, (w-tw)/2, y, t, s)
		y += int(float64(s.Size) * 1.7)
	}
	bw, bh := u(420), u(140)
	bx, by := (w-bw)/2, y+u(60)
	btn := image.Rect(bx, by, bx+bw, by+bh)
	c.Frame(btn, u(5), 0)
	bs := gfx.Style{Size: u(46), Bold: true}
	lw := fonts.Width("Close", bs)
	fonts.Draw(c, bx+(bw-lw)/2, by+bh/2+bs.Size*35/100, "Close", bs)
	if err := d.Show(display.Frame{Gray: c.Gray, Rect: c.Bounds(), Mode: display.ModeContent, Full: true, Tag: "message"}); err != nil {
		return
	}
	if id, ok := d.(display.Idler); ok {
		id.Idle(0)
	}
	ev := d.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ev:
			if !ok {
				return
			}
			if e.Kind == display.Up && image.Pt(e.X, e.Y).In(btn.Inset(-u(30))) {
				return
			}
		}
	}
}
