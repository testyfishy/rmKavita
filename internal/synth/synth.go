// Package synth draws deterministic, manga-like test pages: black-framed
// panels, halftone screentones, speed lines, speech bubbles and a big page
// label. They exist so the reader can be tested without any real manga, and
// they are deliberately hard on a scaler (fine dots, hairlines).
package synth

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand"

	"rmkavita/internal/gfx"
)

// Page draws one w x h page. label is printed large (e.g. "ALPHA v1 p3").
func Page(seed int64, w, h int, label string, fonts *gfx.Fonts) *image.Gray {
	rng := rand.New(rand.NewSource(seed))
	c := gfx.NewCanvas(w, h)
	m := w / 25
	area := image.Rect(m, m, w-m, h-m)
	panels := split(rng, area, 3+rng.Intn(3), w/40)
	for i, p := range panels {
		sub := gfx.NewCanvas(p.Dx(), p.Dy())
		fill(sub, rng, sub.Bounds(), i)
		c.Blit(sub.Gray, p.Min)
		c.Frame(p, max(3, w/200), 0)
	}
	// big label in the first panel, in a white box
	if fonts != nil {
		st := gfx.Style{Size: w / 14, Bold: true, Gray: 0}
		tw := fonts.Width(label, st)
		p := panels[0]
		bx := p.Min.X + w/40
		by := p.Min.Y + w/40
		box := image.Rect(bx, by, bx+tw+w/30, by+st.Size+w/30)
		c.Fill(box, 255)
		c.Frame(box, max(2, w/300), 0)
		fonts.Draw(c, bx+w/60, by+st.Size, label, st)
		// a bubble with small text in the last panel
		p = panels[len(panels)-1]
		bs := gfx.Style{Size: w / 30, Gray: 0}
		txt := "The quick brown fox jumps over the lazy dog"
		bw := min(p.Dx()*3/4, fonts.Width(txt, bs)+w/20)
		bh := bs.Size*3 + w/30
		x := p.Min.X + (p.Dx()-bw)/2
		y := p.Max.Y - bh - w/40
		if y > p.Min.Y {
			bubble := image.Rect(x, y, x+bw, y+bh)
			c.Fill(bubble, 255)
			c.Frame(bubble, max(2, w/300), 0)
			lines := fonts.Wrap(txt, bw-w/30, bs, 3)
			for i, l := range lines {
				fonts.Draw(c, x+w/60, y+w/60+bs.Size*(i+1), l, bs)
			}
		}
	}
	return c.Gray
}

func split(rng *rand.Rand, r image.Rectangle, n, gap int) []image.Rectangle {
	out := []image.Rectangle{r}
	for len(out) < n {
		// split the largest
		bi := 0
		for i, p := range out {
			if p.Dx()*p.Dy() > out[bi].Dx()*out[bi].Dy() {
				bi = i
			}
		}
		p := out[bi]
		out = append(out[:bi], out[bi+1:]...)
		t := 0.35 + 0.3*rng.Float64()
		if p.Dx() > p.Dy() {
			x := p.Min.X + int(float64(p.Dx())*t)
			out = append(out, image.Rect(p.Min.X, p.Min.Y, x-gap/2, p.Max.Y), image.Rect(x+gap/2, p.Min.Y, p.Max.X, p.Max.Y))
		} else {
			y := p.Min.Y + int(float64(p.Dy())*t)
			out = append(out, image.Rect(p.Min.X, p.Min.Y, p.Max.X, y-gap/2), image.Rect(p.Min.X, y+gap/2, p.Max.X, p.Max.Y))
		}
	}
	return out
}

func fill(c *gfx.Canvas, rng *rand.Rand, r image.Rectangle, i int) {
	switch (i + rng.Intn(3)) % 4 {
	case 0: // halftone screentone, 45 degrees, density varies across the panel
		pitch := 6 + rng.Intn(6)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				u := float64(x+y) / math.Sqrt2
				v := float64(x-y) / math.Sqrt2
				fu := math.Mod(u, float64(pitch)) - float64(pitch)/2
				fv := math.Mod(v, float64(pitch)) - float64(pitch)/2
				d := math.Hypot(fu, fv)
				dens := 0.25 + 0.5*float64(x-r.Min.X)/float64(r.Dx())
				if d < dens*float64(pitch)*0.6 {
					c.Set(x, y, 0)
				}
			}
		}
	case 1: // speed lines from a point
		cx := r.Min.X + r.Dx()/2
		cy := r.Min.Y + r.Dy()/2
		n := 90
		for k := 0; k < n; k++ {
			a := 2 * math.Pi * float64(k) / float64(n)
			r0 := float64(min(r.Dx(), r.Dy())) * (0.15 + 0.1*rng.Float64())
			r1 := float64(max(r.Dx(), r.Dy()))
			c.Line(cx+int(r0*math.Cos(a)), cy+int(r0*math.Sin(a)), cx+int(r1*math.Cos(a)), cy+int(r1*math.Sin(a)), 1+rng.Intn(3), 0)
		}
	case 2: // solid black with a white disc
		c.Fill(r, 0)
		c.Disc(r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2, min(r.Dx(), r.Dy())/4, 255)
	default: // hairline hatching
		step := 5 + rng.Intn(4)
		for x := r.Min.X - r.Dy(); x < r.Max.X; x += step {
			c.Line(x, r.Max.Y, x+r.Dy(), r.Min.Y, 1, 0)
		}
	}
}

// JPEG encodes a gray page as a grayscale JPEG.
func JPEG(g *image.Gray, q int) []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, g, &jpeg.Options{Quality: q})
	return b.Bytes()
}

// JPEGColor encodes the page as a 3-component (YCbCr 4:2:0) JPEG with real
// colour: the upper left third of the page is tinted (red/blue), the rest is
// the faint cast of JPEGFaint. It stands for a colour comic page.
func JPEGColor(g *image.Gray, q int) []byte {
	b := g.Bounds()
	rgba := image.NewRGBA(b)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			v := g.Pix[y*g.Stride+x]
			o := 4 * (y*b.Dx() + x)
			if x < b.Dx()/2 && y < b.Dy()/3 {
				rgba.Pix[o], rgba.Pix[o+1], rgba.Pix[o+2], rgba.Pix[o+3] = uint8(min(255, int(v)+70)), v, uint8(max(0, int(v)-70)), 255
			} else {
				rgba.Pix[o], rgba.Pix[o+1], rgba.Pix[o+2], rgba.Pix[o+3] = uint8(min(255, int(v)+4)), v, uint8(max(0, int(v)-6)), 255
			}
		}
	}
	var bb bytes.Buffer
	_ = jpeg.Encode(&bb, rgba, &jpeg.Options{Quality: q})
	return bb.Bytes()
}

// JPEGFaint encodes the page as a 3-component (YCbCr 4:2:0) JPEG with a faint
// colour cast, like a colour scan of a black-and-white page.
func JPEGFaint(g *image.Gray, q int) []byte {
	rgba := image.NewRGBA(g.Bounds())
	for i, v := range g.Pix {
		r := uint8(min(255, int(v)+4))
		rgba.Pix[4*i], rgba.Pix[4*i+1], rgba.Pix[4*i+2], rgba.Pix[4*i+3] = r, v, uint8(max(0, int(v)-6)), 255
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, rgba, &jpeg.Options{Quality: q})
	return b.Bytes()
}

// PNG encodes the page as an 8-bit gray PNG.
func PNG(g *image.Gray) []byte {
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	_ = enc.Encode(&b, g)
	return b.Bytes()
}

// Label formats "NAME p3".
func Label(name string, page int) string { return fmt.Sprintf("%s p%d", name, page+1) }

// BlackPage draws a full-bleed black page with white speech bubbles that touch
// both side edges, plus a little white line art: the page that makes a white
// frame around it jarring.
func BlackPage(seed int64, w, h int, label string, fonts *gfx.Fonts) *image.Gray {
	c := gfx.NewCanvas(w, h)
	c.Fill(c.Bounds(), 8)
	for i := 0; i < 5; i++ {
		y := h/12 + i*h/6
		c.Fill(image.Rect(0, y, w/8, y+h/36), 250)
		c.Fill(image.Rect(w-w/8, y+h/12, w, y+h/12+h/36), 250)
	}
	rng := rand.New(rand.NewSource(seed))
	for k := 0; k < 14; k++ {
		x := w/6 + rng.Intn(w*2/3)
		y := h/10 + rng.Intn(h*4/5)
		c.Line(x, y, x+rng.Intn(w/6)-w/12, y+rng.Intn(h/8), 2+rng.Intn(3), 230)
	}
	if fonts != nil {
		st := gfx.Style{Size: w / 12, Bold: true, Gray: 255}
		fonts.Draw(c, w/6, h/2, label, st)
	}
	return c.Gray
}

// MarginPage draws a white page whose content box leaves unequal margins
// (wide on the left and right, thin at the top and bottom).
func MarginPage(seed int64, w, h int, label string, fonts *gfx.Fonts) *image.Gray {
	c := gfx.NewCanvas(w, h)
	inner := image.Rect(w*15/100, h*3/100, w*85/100, h*97/100)
	c.Blit(Page(seed, inner.Dx(), inner.Dy(), label, fonts), inner.Min)
	return c.Gray
}

// GreyPage draws a page that is a field of mid grey all the way to its edges,
// with a lighter and a darker panel and a line of text: the border colour of
// such a page is grey, neither black nor white.
func GreyPage(seed int64, w, h int, label string, fonts *gfx.Fonts) *image.Gray {
	c := gfx.NewCanvas(w, h)
	c.Fill(c.Bounds(), 128)
	c.Fill(image.Rect(w/10, h/12, w*9/10, h*5/12), 200)
	c.Fill(image.Rect(w/10, h*6/12, w/2-w/40, h*11/12), 60)
	c.Fill(image.Rect(w/2+w/40, h*6/12, w*9/10, h*11/12), 230)
	if fonts != nil {
		st := gfx.Style{Size: w / 14, Bold: true, Gray: 0}
		fonts.Draw(c, w/8, h/4, label, st)
	}
	return c.Gray
}
