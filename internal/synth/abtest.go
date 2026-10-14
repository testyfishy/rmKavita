package synth

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"

	"rmkavita/internal/gfx"
)

// ABPage is one page of the A/B test chapter: a name for the checklist and the
// JPEG bytes.
type ABPage struct {
	Name string
	JPEG []byte
}

// ABOptions size the chapter. The real thing is 2560 x 3936 (the
// 10-megapixel comic pages) with a 3975 x 3057 spread; tests use less.
type ABOptions struct {
	W, H             int // page size
	SpreadW, SpreadH int // landscape spread size
	Quality          int // JPEG quality (85 gives ~1.7 MB pages at the real size)
}

// DefaultAB is the real page size of typical comics.
var DefaultAB = ABOptions{W: 2560, H: 3936, SpreadW: 3975, SpreadH: 3057, Quality: 80}

// halftone fills r with a 45 degree dot screen of the given area coverage
// (0..1) and a dot pitch in pixels, anti-aliased like a scan of a screentone.
func halftone(c *gfx.Canvas, r image.Rectangle, coverage, pitch float64) {
	r = r.Intersect(c.Bounds())
	rad := pitch * math.Sqrt(coverage/math.Pi)
	cs, sn := math.Cos(math.Pi/4), math.Sin(math.Pi/4)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := c.Pix[c.PixOffset(r.Min.X, y):]
		for x := 0; x < r.Dx(); x++ {
			fx, fy := float64(r.Min.X+x), float64(y)
			u := fx*cs + fy*sn
			v := -fx*sn + fy*cs
			du := u - pitch*math.Round(u/pitch)
			dv := v - pitch*math.Round(v/pitch)
			d := math.Hypot(du, dv)
			a := (rad-d)/1.6 + 0.5 // a soft edge, like a scan of a screentone (hard 1 px dots JPEG to 6 MB at 10 MP)
			if a < 0 {
				a = 0
			} else if a > 1 {
				a = 1
			}
			row[x] = uint8(255 - 255*a + 0.5)
		}
	}
}

func label(c *gfx.Canvas, fonts *gfx.Fonts, w int, text string) {
	st := gfx.Style{Size: max(20, w/60), Bold: true, Gray: 0}
	tw := fonts.Width(text, st)
	box := image.Rect(w/60, w/60, w/60+tw+w/50, w/60+st.Size+w/60)
	c.Fill(box, 255)
	c.Frame(box, max(2, w/700), 0)
	fonts.Draw(c, box.Min.X+w/100, box.Min.Y+st.Size+w/200, text, st)
}

func encodeGray(g *image.Gray, q int) []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, g, &jpeg.Options{Quality: q})
	return b.Bytes()
}

// ABChapter returns the pages of the A/B test chapter, in reading order. Each
// stresses one thing the panel decisions depend on: screentone at three
// densities (moire / FAST binarisation), a black page with white bubbles (the
// border and ghosting), small text (waveform sharpness), a 16-step grey ramp
// (grey-level depth, RGB565 tint), colour bars (UI waveform), a mostly white
// page with a thin frame (clearing / ghosting), a landscape spread (fit whole)
// and a typical manga page.
func ABChapter(fonts *gfx.Fonts, o ABOptions) []ABPage {
	W, H := o.W, o.H
	var out []ABPage
	add := func(name string, g *image.Gray) { out = append(out, ABPage{name, encodeGray(g, o.Quality)}) }
	pitch := math.Max(4, float64(W)/285) // ~9 px at 2560: a 60 lpi screen at 600 dpi

	// 1. title and index
	{
		c := gfx.NewCanvas(W, H)
		big := gfx.Style{Size: W / 14, Bold: true, Gray: 0}
		sm := gfx.Style{Size: W / 36, Gray: 0}
		fonts.Draw(c, W/12, H/6, "rmKavita A/B test", big)
		lines := []string{"1  this page", "2  screentone 20 %", "3  screentone 40 %", "4  screentone 60 %", "5  black page, white bubbles",
			"6  small text", "7  16-step grey ramp", "8  colour bars", "9  white page, thin frame", "10 landscape spread", "11 typical page", "12 mid-grey patch"}
		for i, l := range lines {
			fonts.Draw(c, W/12, H/4+i*int(float64(sm.Size)*1.6), l, sm)
		}
		add("title", c.Gray)
	}
	// 2-4. screentone
	for i, cov := range []float64{0.20, 0.40, 0.60} {
		c := gfx.NewCanvas(W, H)
		halftone(c, image.Rect(W/20, H/20, W-W/20, H-H/20), cov, pitch)
		soften(c.Gray)
		c.Frame(image.Rect(W/20, H/20, W-W/20, H-H/20), max(4, W/300), 0)
		label(c, fonts, W, fmt.Sprintf("AB %02d  screentone %d %%", i+2, int(cov*100)))
		add(fmt.Sprintf("screentone-%d", int(cov*100)), c.Gray)
	}
	// 5. black page with white speech bubbles that touch the side edges
	{
		c := gfx.NewCanvas(W, H)
		c.Fill(c.Bounds(), 8)
		bs := gfx.Style{Size: W / 36, Gray: 0}
		for i := 0; i < 5; i++ {
			y := H/12 + i*H/6
			left := image.Rect(0, y, W*2/5, y+H/32)
			right := image.Rect(W*3/5, y+H/12, W, y+H/12+H/32)
			for _, b := range []image.Rectangle{left, right} {
				c.Fill(b, 250)
				c.Frame(b, max(3, W/500), 0)
				fonts.Draw(c, b.Min.X+W/60, b.Min.Y+b.Dy()*3/4, "The quick brown fox", bs)
			}
		}
		label(c, fonts, W, "AB 05  black page")
		add("black-bubbles", c.Gray)
	}
	// 6. small text at 12 sizes (source pixels: 18 .. 80)
	{
		c := gfx.NewCanvas(W, H)
		y := H / 14
		for i, size := range []int{18, 22, 26, 30, 34, 38, 44, 50, 58, 66, 74, 80} {
			st := gfx.Style{Size: size, Gray: 0}
			fonts.Draw(c, W/20, y, fmt.Sprintf("%2dpx  The quick brown fox jumps over the lazy dog 0123456789", size), st)
			y += int(float64(size) * 1.9)
			if i == 5 {
				y += H / 40
				bold := gfx.Style{Size: 30, Bold: true, Gray: 0}
				fonts.Draw(c, W/20, y, "bold 30px  The quick brown fox jumps over the lazy dog", bold)
				y += int(30 * 1.9)
			}
		}
		label(c, fonts, W, "AB 06  small text")
		add("small-text", c.Gray)
	}
	// 7. 16-step grey ramp (each plateau the 4-bit level * 17) + a 32-step ramp below
	{
		c := gfx.NewCanvas(W, H)
		bandH := H / 8
		for i := 0; i < 16; i++ {
			x0, x1 := W/20+i*(W-W/10)/16, W/20+(i+1)*(W-W/10)/16
			c.Fill(image.Rect(x0, H/6, x1, H/6+bandH), uint8(i*17))
		}
		for i := 0; i < 32; i++ {
			x0, x1 := W/20+i*(W-W/10)/32, W/20+(i+1)*(W-W/10)/32
			c.Fill(image.Rect(x0, H/6+bandH+H/20, x1, H/6+2*bandH+H/20), uint8(i*255/31))
		}
		// a smooth ramp and a mid-grey text patch
		for x := W / 20; x < W-W/20; x++ {
			v := uint8((x - W/20) * 255 / (W - W/10))
			c.Fill(image.Rect(x, H/6+2*bandH+H/10, x+1, H/6+3*bandH+H/10), v)
		}
		c.Fill(image.Rect(W/20, H/2+H/10, W-W/20, H/2+H/4), 128)
		st := gfx.Style{Size: W / 40, Gray: 0}
		fonts.Draw(c, W/16, H/2+H/6, "text on a mid-grey patch (128)", st)
		st.Gray = 255
		fonts.Draw(c, W/16, H/2+H/6+W/30, "white text on mid-grey", st)
		label(c, fonts, W, "AB 07  grey ramp")
		add("grey-ramp", c.Gray)
	}
	// 8. colour bars (JPEG 4:2:0)
	{
		img := image.NewRGBA(image.Rect(0, 0, W, H))
		bars := []color.RGBA{{255, 255, 255, 255}, {255, 255, 0, 255}, {0, 255, 255, 255}, {0, 255, 0, 255}, {255, 0, 255, 255}, {255, 0, 0, 255}, {0, 0, 255, 255}, {0, 0, 0, 255}}
		for y := 0; y < H; y++ {
			for x := 0; x < W; x++ {
				v := bars[x*len(bars)/W]
				if y > H*3/4 { // the lower quarter: the same bars at half level
					v = color.RGBA{v.R / 2, v.G / 2, v.B / 2, 255}
				}
				img.SetRGBA(x, y, v)
			}
		}
		var b bytes.Buffer
		_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: o.Quality})
		out = append(out, ABPage{"colour-bars", b.Bytes()})
	}
	// 9. mostly white page with a thin black frame
	{
		c := gfx.NewCanvas(W, H)
		c.Frame(image.Rect(W/30, H/40, W-W/30, H-H/40), max(3, W/800), 0)
		st := gfx.Style{Size: W / 30, Gray: 0}
		fonts.Draw(c, W/8, H/2, "an almost empty page", st)
		label(c, fonts, W, "AB 09  white page")
		add("white-frame", c.Gray)
	}
	// 10. landscape spread: two screentone panels with a gutter
	{
		sw, sh := o.SpreadW, o.SpreadH
		c := gfx.NewCanvas(sw, sh)
		l := image.Rect(sw/40, sh/30, sw/2-sw/80, sh-sh/30)
		r := image.Rect(sw/2+sw/80, sh/30, sw-sw/40, sh-sh/30)
		halftone(c, l, 0.30, pitch)
		halftone(c, r, 0.50, pitch)
		soften(c.Gray)
		c.Frame(l, max(4, sw/400), 0)
		c.Frame(r, max(4, sw/400), 0)
		label(c, fonts, sw, "AB 10  spread")
		add("spread", c.Gray)
	}
	// 11. a typical manga page
	add("typical", Page(4711, W, H, "AB 11  typical page", fonts))
	// 12. mid-grey patch with a fine hatching: dithering / tint check
	{
		c := gfx.NewCanvas(W, H)
		c.Fill(c.Bounds(), 128)
		step := max(6, W/300)
		for x := -H; x < W; x += step {
			c.Line(x, H, x+H, 0, max(1, step/4), 40)
		}
		label(c, fonts, W, "AB 12  mid-grey patch")
		add("grey-patch", c.Gray)
	}
	return out
}

// soften applies a [1 2 1] / 4 blur in both directions, the low-pass a scanner
// puts on a screentone (and what keeps a 10 MP halftone page near the size of a
// real scan instead of 6 MB).
func soften(g *image.Gray) {
	b := g.Bounds()
	w, h := b.Dx(), b.Dy()
	tmp := make([]uint8, w)
	for y := 0; y < h; y++ {
		row := g.Pix[y*g.Stride : y*g.Stride+w]
		for x := 0; x < w; x++ {
			l, r := row[max(x-1, 0)], row[min(x+1, w-1)]
			tmp[x] = uint8((int(l) + 2*int(row[x]) + int(r) + 2) / 4)
		}
		copy(row, tmp)
	}
	col := make([]uint8, h)
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			u, d := g.Pix[max(y-1, 0)*g.Stride+x], g.Pix[min(y+1, h-1)*g.Stride+x]
			col[y] = uint8((int(u) + 2*int(g.Pix[y*g.Stride+x]) + int(d) + 2) / 4)
		}
		for y := 0; y < h; y++ {
			g.Pix[y*g.Stride+x] = col[y]
		}
	}
}

// SpeedChapter returns the 6 pages of the speed A/B pair at real size: the same
// six page-turn pages as an all-grey chapter (colour false) or as an all-colour
// chapter (colour true): line art with speed lines, screentone and speech
// bubbles, dense small text, a black panel page, soft shading (skin tones in
// the colour variant), and a busy second line-art page. The reader turns through
// one and then the other and compares the page-turn time.
func SpeedChapter(fonts *gfx.Fonts, o ABOptions, colour bool) []ABPage {
	W, H := o.W, o.H
	pages := make([]*image.Gray, 0, 6)

	// 1. line art with speed lines and a title box
	pages = append(pages, Page(8101, W, H, "SP 1  line art", fonts))
	// 2. screentone panels, a speech bubble with text
	{
		c := gfx.NewCanvas(W, H)
		pitch := math.Max(4, float64(W)/285)
		l := image.Rect(W/14, H/16, W/2-W/40, H*5/8)
		r := image.Rect(W/2+W/40, H/16, W-W/14, H*5/8)
		halftone(c, l, 0.25, pitch)
		halftone(c, r, 0.55, pitch)
		soften(c.Gray)
		c.Frame(l, max(4, W/300), 0)
		c.Frame(r, max(4, W/300), 0)
		bub := image.Rect(W/6, H*5/8+H/20, W*5/6, H*5/8+H/6)
		c.Fill(bub, 255)
		c.Frame(bub, max(4, W/300), 0)
		st := gfx.Style{Size: W / 22, Gray: 0}
		fonts.Draw(c, bub.Min.X+W/40, bub.Min.Y+H/18, "A line of dialogue in a bubble,", st)
		fonts.Draw(c, bub.Min.X+W/40, bub.Min.Y+H/10, "over a screentone page. SP 2", st)
		pages = append(pages, c.Gray)
	}
	// 3. dense small text
	{
		c := gfx.NewCanvas(W, H)
		y := H / 14
		for i := 0; y < H-H/14; i++ {
			size := 26 + (i%3)*6
			st := gfx.Style{Size: size, Gray: 0}
			fonts.Draw(c, W/16, y, "The quick brown fox jumps over the lazy dog, again and again, to read it on the panel. SP 3", st)
			y += int(float64(size) * 1.8)
		}
		pages = append(pages, c.Gray)
	}
	// 4. a black panel page with white bubbles
	pages = append(pages, BlackPage(8104, W, H, "SP 4  black panels", fonts))
	// 5. soft shading: a gradient face-like blob and rim lines
	{
		c := gfx.NewCanvas(W, H)
		cx, cy := W/2, H/2
		rx, ry := W*2/5, H/3
		for y := cy - ry; y < cy+ry; y++ {
			for x := cx - rx; x < cx+rx; x++ {
				dx, dy := float64(x-cx)/float64(rx), float64(y-cy)/float64(ry)
				d := dx*dx + dy*dy
				if d > 1 {
					continue
				}
				v := 235 - 150*d + 30*dx // light from the left
				if v < 40 {
					v = 40
				}
				c.Pix[c.PixOffset(x, y)] = uint8(v)
			}
		}
		c.Frame(image.Rect(cx-rx, cy-ry, cx+rx, cy+ry), max(4, W/300), 0)
		fonts.Draw(c, W/12, H/10, "SP 5  soft shading", gfx.Style{Size: W / 18, Bold: true, Gray: 0})
		pages = append(pages, c.Gray)
	}
	// 6. a busy second line-art page
	pages = append(pages, Page(8106, W, H, "SP 6  line art 2", fonts))

	var out []ABPage
	for i, g := range pages {
		name := fmt.Sprintf("speed-%d", i+1)
		if !colour {
			out = append(out, ABPage{name, encodeGray(g, o.Quality)})
			continue
		}
		out = append(out, ABPage{name, encodeColour(colourise(g, i), o.Quality)})
	}
	return out
}

// colourise turns a grey page into a colour one: the mid greys (shading,
// screentone, soft gradients) take a warm skin tone, a sky blue or a leaf green
// by region; pure ink and pure paper stay as they are.
func colourise(g *image.Gray, variant int) *image.RGBA {
	b := g.Bounds()
	out := image.NewRGBA(b)
	w, h := b.Dx(), b.Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := int(g.Pix[y*g.Stride+x])
			r, gg, bb := v, v, v
			if v > 25 && v < 235 {
				k := (x*3/w + y*2/h + variant) % 3
				f := 1 - math.Abs(float64(v)-130)/130 // strongest at the middle greys
				switch k {
				case 0: // skin
					r, gg, bb = clamp255(v+int(55*f)), clamp255(v+int(15*f)), clamp255(v-int(60*f))
				case 1: // sky
					r, gg, bb = clamp255(v-int(60*f)), clamp255(v-int(5*f)), clamp255(v+int(55*f))
				default: // leaf
					r, gg, bb = clamp255(v-int(50*f)), clamp255(v+int(35*f)), clamp255(v-int(50*f))
				}
			}
			if v >= 235 && y*3/h == variant%3 {
				// a coloured ground behind a band of the page (a pastel wash)
				switch variant % 3 {
				case 0:
					r, gg, bb = 255, 233, 205
				case 1:
					r, gg, bb = 214, 233, 255
				default:
					r, gg, bb = 221, 255, 214
				}
			}
			o := out.PixOffset(x, y)
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = uint8(r), uint8(gg), uint8(bb), 255
		}
	}
	return out
}

func clamp255(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func encodeColour(img *image.RGBA, q int) []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: q})
	return b.Bytes()
}
