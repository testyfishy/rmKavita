package imgproc

// Pre-quantisation for the "speed" render mode: a fitted grey page is reduced
// to 2, 4 or 16 grey levels once, when it is prepared (in the read-ahead
// pipeline, off the UI goroutine), so that turning to it costs no CPU at all
// and the panel is asked to show a page that has fewer distinct greys to
// drive. Everything here is pure and deterministic.

// Dither selects how the quantisation error is handled.
type Dither int

const (
	// DitherNone is a plain look-up: every grey goes to its nearest level.
	DitherNone Dither = iota
	// DitherOrdered adds a 4x4 Bayer threshold before the look-up: branch-free,
	// no state between pixels, the same output for the same input anywhere.
	DitherOrdered
	// DitherDiffuse is Floyd-Steinberg error diffusion (serpentine): the best
	// tonality, a little more work, and noisy over a regular screentone.
	DitherDiffuse
)

// ParseDither maps a config word to a Dither (unknown -> DitherOrdered).
func ParseDither(s string) Dither {
	switch s {
	case "none":
		return DitherNone
	case "diffuse":
		return DitherDiffuse
	}
	return DitherOrdered
}

func (d Dither) String() string {
	switch d {
	case DitherNone:
		return "none"
	case DitherDiffuse:
		return "diffuse"
	}
	return "ordered"
}

// ValidLevels reports whether n is one of the supported level counts.
func ValidLevels(n int) bool { return n == 2 || n == 4 || n == 16 }

// LevelValue is the grey value of level i of n (0 = black, n-1 = white),
// spread evenly over 0..255.
func LevelValue(i, n int) uint8 { return uint8((i*255 + (n-1)/2) / (n - 1)) }

// QuantLUT maps every grey to the nearest of n evenly spaced levels. It is
// monotonic and its image is exactly the n level values.
func QuantLUT(n int) [256]uint8 {
	var l [256]uint8
	for v := 0; v < 256; v++ {
		i := (v*(n-1) + 127) / 255 // nearest level index
		l[v] = LevelValue(i, n)
	}
	return l
}

// The white and black points: scan grain on paper (a 245 where the paper is
// white) and a 8 where the ink is black would otherwise become stray dots after
// the reduction, and a binary waveform ghosts them. Everything at or above
// ClipHigh is white, at or below ClipLow black, the rest is stretched.
const (
	ClipLow  = 8
	ClipHigh = 245
)

// ClipTable stretches ClipLow..ClipHigh to 0..255 (monotonic, 0 and 255 fixed).
func ClipTable() [256]uint8 {
	var t [256]uint8
	for v := 0; v < 256; v++ {
		switch {
		case v <= ClipLow:
			t[v] = 0
		case v >= ClipHigh:
			t[v] = 255
		default:
			t[v] = uint8(((v-ClipLow)*255 + (ClipHigh-ClipLow)/2) / (ClipHigh - ClipLow))
		}
	}
	return t
}

// SnapLevel is the level (of n) a flat grey v becomes: the border colour of a
// page and the footer have to be one of the levels or they would be a mid-grey
// flat that the binary waveform does not draw cleanly.
func SnapLevel(v uint8, n int) uint8 {
	if !ValidLevels(n) {
		return v
	}
	lut, clip := QuantLUT(n), ClipTable()
	return lut[clip[v]]
}

// bayer4 is the 4x4 Bayer matrix, 0..15.
var bayer4 = [16]int{
	0, 8, 2, 10,
	12, 4, 14, 6,
	3, 11, 1, 9,
	15, 7, 13, 5,
}

// Quantise reduces the w x h grey image g (row-major, stride w) in place to n
// levels.
func Quantise(g []uint8, w, h, n int, d Dither) {
	if !ValidLevels(n) || w <= 0 || h <= 0 {
		return
	}
	lut := QuantLUT(n)
	clip := ClipTable()
	switch d {
	case DitherNone:
		for i, v := range g[:w*h] {
			g[i] = lut[clip[v]]
		}
	case DitherOrdered:
		step := 255 / (n - 1) // the spacing of the levels
		for y := 0; y < h; y++ {
			row := g[y*w : y*w+w]
			by := (y & 3) * 4
			for x, v := range row {
				// threshold offset in (-step/2, +step/2), centred
				off := (bayer4[by+(x&3)]*2+1)*step/32 - step/2
				t := int(clip[v]) + off
				if t < 0 {
					t = 0
				} else if t > 255 {
					t = 255
				}
				row[x] = lut[t]
			}
		}
	case DitherDiffuse:
		diffuse(g, w, h, &lut, &clip)
	}
}

// diffuse is serpentine Floyd-Steinberg with two error rows.
func diffuse(g []uint8, w, h int, lut, clip *[256]uint8) {
	cur := make([]int, w+2)
	next := make([]int, w+2)
	for y := 0; y < h; y++ {
		row := g[y*w : y*w+w]
		for i := range next {
			next[i] = 0
		}
		if y&1 == 0 {
			for x := 0; x < w; x++ {
				v := int(clip[row[x]]) + cur[x+1]/16
				if v < 0 {
					v = 0
				} else if v > 255 {
					v = 255
				}
				q := lut[v]
				row[x] = q
				e := v - int(q)
				cur[x+2] += e * 7
				next[x] += e * 3
				next[x+1] += e * 5
				next[x+2] += e * 1
			}
		} else {
			for x := w - 1; x >= 0; x-- {
				v := int(clip[row[x]]) + cur[x+1]/16
				if v < 0 {
					v = 0
				} else if v > 255 {
					v = 255
				}
				q := lut[v]
				row[x] = q
				e := v - int(q)
				cur[x] += e * 7
				next[x+2] += e * 3
				next[x+1] += e * 5
				next[x] += e * 1
			}
		}
		cur, next = next, cur
	}
}
