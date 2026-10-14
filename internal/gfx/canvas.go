// Package gfx is the small drawing toolkit the UI uses: an 8-bit gray canvas,
// a few vector primitives and text. Everything draws into one *image.Gray; the
// display backends turn that into whatever the panel wants.
package gfx

import (
	"image"
	"math"
)

// Canvas is an 8-bit grayscale drawing surface (0 = black, 255 = white).
type Canvas struct {
	*image.Gray
}

// NewCanvas returns a white canvas.
func NewCanvas(w, h int) *Canvas {
	c := &Canvas{Gray: image.NewGray(image.Rect(0, 0, w, h))}
	c.Fill(c.Bounds(), 255)
	return c
}

// Fill paints r (clipped to the canvas) with value v.
func (c *Canvas) Fill(r image.Rectangle, v uint8) {
	r = r.Intersect(c.Bounds())
	if r.Empty() {
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := c.Pix[c.PixOffset(r.Min.X, y):c.PixOffset(r.Max.X, y)]
		for i := range row {
			row[i] = v
		}
	}
}

// Frame draws a rectangle outline of the given thickness inside r.
func (c *Canvas) Frame(r image.Rectangle, thick int, v uint8) {
	if thick <= 0 {
		return
	}
	c.Fill(image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+thick), v)
	c.Fill(image.Rect(r.Min.X, r.Max.Y-thick, r.Max.X, r.Max.Y), v)
	c.Fill(image.Rect(r.Min.X, r.Min.Y, r.Min.X+thick, r.Max.Y), v)
	c.Fill(image.Rect(r.Max.X-thick, r.Min.Y, r.Max.X, r.Max.Y), v)
}

// Set writes one pixel if it is inside the canvas.
func (c *Canvas) Set(x, y int, v uint8) {
	if image.Pt(x, y).In(c.Bounds()) {
		c.Pix[c.PixOffset(x, y)] = v
	}
}

// Line draws a thick line (square brush, which is plenty for icons).
func (c *Canvas) Line(x0, y0, x1, y1, thick int, v uint8) {
	dx := x1 - x0
	dy := y1 - y0
	steps := int(math.Max(math.Abs(float64(dx)), math.Abs(float64(dy))))
	h := thick / 2
	if steps == 0 {
		c.Fill(image.Rect(x0-h, y0-h, x0-h+thick, y0-h+thick), v)
		return
	}
	for i := 0; i <= steps; i++ {
		x := x0 + dx*i/steps
		y := y0 + dy*i/steps
		c.Fill(image.Rect(x-h, y-h, x-h+thick, y-h+thick), v)
	}
}

// Disc fills a circle.
func (c *Canvas) Disc(cx, cy, r int, v uint8) {
	for y := -r; y <= r; y++ {
		w := int(math.Sqrt(float64(r*r - y*y)))
		c.Fill(image.Rect(cx-w, cy+y, cx+w+1, cy+y+1), v)
	}
}

// Ring draws a circle outline.
func (c *Canvas) Ring(cx, cy, r, thick int, v uint8) {
	ri := r - thick
	for y := -r; y <= r; y++ {
		wo := int(math.Sqrt(float64(r*r - y*y)))
		if abs(y) >= ri {
			c.Fill(image.Rect(cx-wo, cy+y, cx+wo+1, cy+y+1), v)
			continue
		}
		wi := int(math.Sqrt(float64(ri*ri - y*y)))
		c.Fill(image.Rect(cx-wo, cy+y, cx-wi, cy+y+1), v)
		c.Fill(image.Rect(cx+wi+1, cy+y, cx+wo+1, cy+y+1), v)
	}
}

// Pie fills the clockwise fraction frac (0..1) of a disc starting at 12 o'clock.
func (c *Canvas) Pie(cx, cy, r int, frac float64, v uint8) {
	if frac <= 0 {
		return
	}
	if frac >= 1 {
		c.Disc(cx, cy, r, v)
		return
	}
	limit := frac * 2 * math.Pi
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			if x*x+y*y > r*r {
				continue
			}
			a := math.Atan2(float64(x), float64(-y)) // 0 at 12 o'clock, clockwise
			if a < 0 {
				a += 2 * math.Pi
			}
			if a <= limit {
				c.Set(cx+x, cy+y, v)
			}
		}
	}
}

// Blit copies src onto the canvas with src's top-left at pt, clipped.
func (c *Canvas) Blit(src *image.Gray, pt image.Point) {
	dst := image.Rectangle{Min: pt, Max: pt.Add(src.Bounds().Size())}.Intersect(c.Bounds())
	if dst.Empty() {
		return
	}
	sx := dst.Min.X - pt.X + src.Bounds().Min.X
	sy := dst.Min.Y - pt.Y + src.Bounds().Min.Y
	for y := 0; y < dst.Dy(); y++ {
		so := src.PixOffset(sx, sy+y)
		do := c.PixOffset(dst.Min.X, dst.Min.Y+y)
		copy(c.Pix[do:do+dst.Dx()], src.Pix[so:so+dst.Dx()])
	}
}

// Chevron draws a "<" (dir<0) or ">" (dir>0) centred at (cx, cy).
func (c *Canvas) Chevron(cx, cy, size, dir, thick int, v uint8) {
	h := size / 2
	if dir < 0 {
		c.Line(cx+h/2, cy-h, cx-h/2, cy, thick, v)
		c.Line(cx-h/2, cy, cx+h/2, cy+h, thick, v)
	} else {
		c.Line(cx-h/2, cy-h, cx+h/2, cy, thick, v)
		c.Line(cx+h/2, cy, cx-h/2, cy+h, thick, v)
	}
}

// Cross draws an "X" centred at (cx, cy).
func (c *Canvas) Cross(cx, cy, size, thick int, v uint8) {
	h := size / 2
	c.Line(cx-h, cy-h, cx+h, cy+h, thick, v)
	c.Line(cx-h, cy+h, cx+h, cy-h, thick, v)
}

// Magnifier draws a search glass centred at (cx, cy).
func (c *Canvas) Magnifier(cx, cy, size, thick int, v uint8) {
	r := size / 3
	c.Ring(cx-r/3, cy-r/3, r, thick, v)
	c.Line(cx-r/3+r*7/10, cy-r/3+r*7/10, cx+size/2-thick, cy+size/2-thick, thick+2, v)
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// House draws a home icon centred at (cx, cy), about size pixels wide.
func (c *Canvas) House(cx, cy, size, thick int, v uint8) {
	h := size / 2
	eaveY := cy - h/5
	c.Line(cx-h, eaveY, cx, cy-h, thick, v)
	c.Line(cx, cy-h, cx+h, eaveY, thick, v)
	wx := h * 7 / 10
	bottom := cy + h*8/10
	c.Line(cx-wx, eaveY+thick, cx-wx, bottom, thick, v)
	c.Line(cx+wx, eaveY+thick, cx+wx, bottom, thick, v)
	c.Line(cx-wx, bottom, cx+wx, bottom, thick, v)
	dx := h * 22 / 100
	c.Line(cx-dx, bottom, cx-dx, cy+h/6, thick, v)
	c.Line(cx+dx, bottom, cx+dx, cy+h/6, thick, v)
	c.Line(cx-dx, cy+h/6, cx+dx, cy+h/6, thick, v)
}

// Gear draws a settings cog centred at (cx, cy), about size pixels across: a
// solid wheel with eight teeth and a hole.
func (c *Canvas) Gear(cx, cy, size, thick int, v uint8) {
	r := size * 30 / 100
	c.Disc(cx, cy, r, v)
	outer := size * 46 / 100
	tooth := size * 17 / 100
	for i := 0; i < 8; i++ {
		a := float64(i) * math.Pi / 4
		x0 := cx + int(math.Round(math.Cos(a)*float64(r)*0.8))
		y0 := cy + int(math.Round(math.Sin(a)*float64(r)*0.8))
		x1 := cx + int(math.Round(math.Cos(a)*float64(outer)))
		y1 := cy + int(math.Round(math.Sin(a)*float64(outer)))
		c.Line(x0, y0, x1, y1, tooth, v)
	}
	c.Disc(cx, cy, size*13/100, 255)
}

// User draws an account icon (head and shoulders) centred at (cx, cy).
func (c *Canvas) User(cx, cy, size, thick int, v uint8) {
	hr := size * 17 / 100
	c.Disc(cx, cy-size*17/100, hr, v)
	r := size * 36 / 100
	by := cy + size*38/100
	for y := -r; y <= 0; y++ {
		w := int(math.Sqrt(float64(r*r - y*y)))
		c.Fill(image.Rect(cx-w, by+y, cx+w+1, by+y+1), v)
	}
}

// Plus draws a "+" centred at (cx, cy).
func (c *Canvas) Plus(cx, cy, size, thick int, v uint8) {
	h := size / 2
	c.Line(cx-h, cy, cx+h, cy, thick, v)
	c.Line(cx, cy-h, cx, cy+h, thick, v)
}

// Minus draws a "-" centred at (cx, cy).
func (c *Canvas) Minus(cx, cy, size, thick int, v uint8) {
	h := size / 2
	c.Line(cx-h, cy, cx+h, cy, thick, v)
}
