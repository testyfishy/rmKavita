package gfx

import (
	"image"
	"image/color"
	"os"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Style selects size and weight; Gray is the ink value (0 = black).
type Style struct {
	Size int
	Bold bool
	Gray uint8
}

// FallbackPaths are system fonts tried, lazily and in order, for runes the
// embedded font lacks (CJK titles). They are read from the device, never
// embedded, so the binary stays small.
var FallbackPaths = []string{
	"/usr/share/fonts/ttf/noto/NotoSansJP-VariableFont_wght.ttf",
	"/usr/share/fonts/ttf/noto/NotoSansSC-VariableFont_wght.ttf",
	"/usr/share/fonts/ttf/noto/NotoSansKR-VariableFont_wght.ttf",
	"/usr/share/fonts/ttf/noto/NotoSans-VariableFont_wdth,wght.ttf",
}

type faceKey struct {
	font int // 0 regular, 1 bold, 2.. fallbacks
	size int
}

// Fonts owns the parsed fonts and a cache of sized faces.
type Fonts struct {
	mu        sync.Mutex
	reg, bold *sfnt.Font
	fb        []*sfnt.Font
	fbTried   []bool
	faces     map[faceKey]font.Face
	buf       sfnt.Buffer
	paths     []string
}

// NewFonts parses the embedded Go Regular and Go Bold (BSD-style licence,
// Bigelow & Holmes, see the module's font/gofont/ttfs/README).
func NewFonts() (*Fonts, error) {
	r, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	b, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	f := &Fonts{reg: r, bold: b, faces: map[faceKey]font.Face{}, paths: FallbackPaths}
	f.fb = make([]*sfnt.Font, len(f.paths))
	f.fbTried = make([]bool, len(f.paths))
	return f, nil
}

// SetFallbackPaths replaces the fallback list (tests).
func (f *Fonts) SetFallbackPaths(p []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = p
	f.fb = make([]*sfnt.Font, len(p))
	f.fbTried = make([]bool, len(p))
	f.faces = map[faceKey]font.Face{}
}

func (f *Fonts) fontFor(idx int, bold bool) *sfnt.Font {
	switch {
	case idx == 0 && !bold:
		return f.reg
	case idx == 0 && bold:
		return f.bold
	}
	i := idx - 2
	if i < 0 || i >= len(f.fb) {
		return nil
	}
	if !f.fbTried[i] {
		f.fbTried[i] = true
		if fh, err := os.Open(f.paths[i]); err == nil {
			// The file handle stays open for the life of the font: tables are read on demand.
			if ft, err := opentype.ParseReaderAt(fh); err == nil {
				f.fb[i] = ft
			} else {
				fh.Close()
			}
		}
	}
	return f.fb[i]
}

func (f *Fonts) face(idx int, bold bool, size int) font.Face {
	k := faceKey{idx, size}
	if idx == 0 && bold {
		k.font = 1
	} else if idx != 0 {
		k.font = idx
	}
	if fc, ok := f.faces[k]; ok {
		return fc
	}
	ft := f.fontFor(idx, bold)
	if ft == nil {
		return nil
	}
	fc, err := opentype.NewFace(ft, &opentype.FaceOptions{Size: float64(size), DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil
	}
	f.faces[k] = fc
	return fc
}

// pick returns the font index able to draw r (0 = primary) or -1 for none.
func (f *Fonts) pick(r rune, bold bool) int {
	prim := f.fontFor(0, bold)
	if r == ' ' || r == '\t' || unicode.IsControl(r) {
		return 0
	}
	if gi, err := prim.GlyphIndex(&f.buf, r); err == nil && gi != 0 {
		return 0
	}
	for i := range f.paths {
		ft := f.fontFor(i+2, false)
		if ft == nil {
			continue
		}
		if gi, err := ft.GlyphIndex(&f.buf, r); err == nil && gi != 0 {
			return i + 2
		}
	}
	return -1
}

type run struct {
	idx int
	s   string
}

func (f *Fonts) runs(s string, bold bool) []run {
	var out []run
	var cur strings.Builder
	curIdx := 0
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, run{curIdx, cur.String()})
			cur.Reset()
		}
	}
	for _, r := range s {
		if r == '\n' || r == '\r' {
			r = ' '
		}
		idx := f.pick(r, bold)
		if idx < 0 {
			r, idx = '?', 0
		}
		if idx != curIdx {
			flush()
			curIdx = idx
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}

// Width returns the advance width of s in pixels.
func (f *Fonts) Width(s string, st Style) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := fixed.Int26_6(0)
	for _, r := range f.runs(s, st.Bold) {
		if fc := f.face(r.idx, st.Bold, st.Size); fc != nil {
			w += font.MeasureString(fc, r.s)
		}
	}
	return w.Ceil()
}

// Metrics returns ascent and descent in pixels for the primary face.
func (f *Fonts) Metrics(st Style) (ascent, descent int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fc := f.face(0, st.Bold, st.Size)
	m := fc.Metrics()
	return m.Ascent.Ceil(), m.Descent.Ceil()
}

// Draw paints s with its baseline at y, starting at x, and returns the advance.
func (f *Fonts) Draw(c *Canvas, x, y int, s string, st Style) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	src := image.NewUniform(color.Gray{Y: st.Gray})
	d := &font.Drawer{Dst: c.Gray, Src: src, Dot: fixed.P(x, y)}
	for _, r := range f.runs(s, st.Bold) {
		fc := f.face(r.idx, st.Bold, st.Size)
		if fc == nil {
			continue
		}
		d.Face = fc
		d.DrawString(r.s)
	}
	return d.Dot.X.Ceil() - x
}

// Ellipsize shortens s with "..." so it fits maxW.
func (f *Fonts) Ellipsize(s string, maxW int, st Style) string {
	if f.Width(s, st) <= maxW {
		return s
	}
	rs := []rune(s)
	lo, hi := 0, len(rs)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if f.Width(string(rs[:mid])+"...", st) <= maxW {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return strings.TrimRight(string(rs[:lo]), " ") + "..."
}

// Wrap splits s into at most maxLines lines no wider than maxW; the last line
// is ellipsized when the text does not fit.
func (f *Fonts) Wrap(s string, maxW int, st Style, maxLines int) []string {
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for i := 0; i < len(words); i++ {
		w := words[i]
		try := w
		if cur != "" {
			try = cur + " " + w
		}
		if f.Width(try, st) <= maxW {
			cur = try
			continue
		}
		if cur == "" {
			// a single word wider than the line: hard-cut it
			cur = f.Ellipsize(w, maxW, st)
			lines = append(lines, cur)
			cur = ""
			continue
		}
		lines = append(lines, cur)
		cur = ""
		i-- // retry this word on the next line
		if len(lines) == maxLines {
			break
		}
	}
	if cur != "" && len(lines) < maxLines {
		lines = append(lines, cur)
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	// If words remain beyond the allowed lines, mark the last line.
	joined := strings.Join(lines, " ")
	if len(lines) == maxLines && len(strings.Fields(joined)) < len(words) {
		last := lines[len(lines)-1]
		lines[len(lines)-1] = f.Ellipsize(last+" ...", maxW, st)
	}
	return lines
}
