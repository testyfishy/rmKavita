package display

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PNG is a Display that writes every shown frame as a PNG and feeds scripted
// touches to the UI. It is how screens are reviewed without the tablet.
type PNG struct {
	W, H int
	Dir  string

	mu       sync.Mutex
	n        int
	events   chan Event
	idle     chan int
	closed   bool
	logf     *os.File
	script   []step
	started  bool
	cnt      Counters
	lastMod  Mode
	haveMod  bool
	fbG      *image.Gray
	lastIdle int
	fbC      *image.RGBA
	// Keep: when >0 only that many most recent frames are kept on disk.
	Keep int
	// Discard: count the updates but draw and write nothing (memory and timing
	// measurements that should not include the test backend itself).
	Discard bool
}

type step struct {
	op   string
	args []int
	text string
}

// NewPNG creates dir and returns the backend. script may be empty.
func NewPNG(w, h int, dir, script string) (*PNG, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := &PNG{W: w, H: h, Dir: dir, events: make(chan Event, 16), idle: make(chan int, 64)}
	f, err := os.Create(filepath.Join(dir, "frames.log"))
	if err == nil {
		p.logf = f
	}
	if script != "" {
		st, err := ParseScript(script)
		if err != nil {
			return nil, err
		}
		p.script = st
	}
	return p, nil
}

// ParseScript parses lines: tap X Y | hold X Y MS | swipe X0 Y0 X1 Y1 | wait MS |
// type TEXT | key bs|enter | quit.
func ParseScript(text string) ([]step, error) {
	var out []step
	sc := bufio.NewScanner(strings.NewReader(text))
	ln := 0
	for sc.Scan() {
		ln++
		raw := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(strings.TrimSpace(raw), "type ") {
			// the text is everything after "type " (spaces kept, no comments)
			i := strings.Index(raw, "type ")
			out = append(out, step{op: "type", text: raw[i+5:]})
			continue
		}
		line := strings.TrimSpace(raw)
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if f[0] == "key" && len(f) == 2 && (f[1] == "bs" || f[1] == "enter") {
			out = append(out, step{op: "key", text: f[1]})
			continue
		}
		want := map[string]int{"tap": 2, "hold": 3, "swipe": 4, "wait": 1, "quit": 0}
		n, ok := want[f[0]]
		if !ok || len(f)-1 != n {
			return nil, fmt.Errorf("script line %d: %q", ln, f[0])
		}
		st := step{op: f[0]}
		for _, a := range f[1:] {
			v, err := strconv.Atoi(a)
			if err != nil {
				return nil, fmt.Errorf("script line %d: %q", ln, f[0])
			}
			st.args = append(st.args, v)
		}
		out = append(out, st)
	}
	return out, nil
}

// Size implements Display.
func (p *PNG) Size() (int, int) { return p.W, p.H }

// Events implements Display; the script starts on the first call.
func (p *PNG) Events() <-chan Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started {
		p.started = true
		go p.run()
	}
	return p.events
}

// Idle implements Idler.
func (p *PNG) Idle(consumed int) {
	p.mu.Lock()
	if consumed > p.lastIdle {
		p.lastIdle = consumed
	}
	p.mu.Unlock()
	select {
	case p.idle <- consumed:
	default:
	}
}

// Counters implements Counted.
func (p *PNG) Counters() Counters {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cnt
}

func (p *PNG) send(e Event) { e.At = time.Now(); p.events <- e }

func (p *PNG) run() {
	sent := 0
	waitIdle := func() {
		p.mu.Lock()
		done := p.lastIdle >= sent
		p.mu.Unlock()
		if done {
			return
		}
		deadline := time.After(20 * time.Second)
		for {
			select {
			case n := <-p.idle:
				if n >= sent {
					return
				}
			case <-deadline:
				return
			}
		}
	}
	waitIdle() // first screen
	for _, st := range p.script {
		switch st.op {
		case "tap":
			p.send(Event{Kind: Down, X: st.args[0], Y: st.args[1]})
			time.Sleep(30 * time.Millisecond)
			p.send(Event{Kind: Up, X: st.args[0], Y: st.args[1]})
			sent += 2
		case "hold":
			p.send(Event{Kind: Down, X: st.args[0], Y: st.args[1]})
			sent++
			time.Sleep(time.Duration(st.args[2]) * time.Millisecond)
			p.send(Event{Kind: Up, X: st.args[0], Y: st.args[1]})
			sent++
		case "swipe":
			p.send(Event{Kind: Down, X: st.args[0], Y: st.args[1]})
			p.send(Event{Kind: Move, X: (st.args[0] + st.args[2]) / 2, Y: (st.args[1] + st.args[3]) / 2})
			p.send(Event{Kind: Up, X: st.args[2], Y: st.args[3]})
			sent += 3
		case "type":
			for _, r := range st.text {
				p.send(Event{Kind: Key, Text: string(r)})
				sent++
				waitIdle()
			}
		case "key":
			t := "\b"
			if st.text == "enter" {
				t = "\n"
			}
			p.send(Event{Kind: Key, Text: t})
			sent++
		case "wait":
			time.Sleep(time.Duration(st.args[0]) * time.Millisecond)
			continue
		case "quit":
			p.finish()
			return
		}
		waitIdle()
	}
	time.Sleep(200 * time.Millisecond)
	p.finish()
}

func (p *PNG) finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.events)
	}
}

func inRects(x, y int, rs []image.Rectangle) bool {
	pt := image.Pt(x, y)
	for _, r := range rs {
		if pt.In(r) {
			return true
		}
	}
	return false
}

// Show implements Display: one PNG per frame plus a line in frames.log. Like the
// panel's shared framebuffer, the backend keeps the picture between frames and
// updates only the rectangle of each one, so a sequence of partial updates
// (overlay top, overlay bottom, colour page) shows what the panel would show.
func (p *PNG) Show(f Frame) error {
	p.mu.Lock()
	p.n++
	n := p.n
	r := f.Rect.Intersect(image.Rect(0, 0, p.W, p.H))
	p.cnt.Updates++
	p.cnt.Pixels += r.Dx() * r.Dy()
	// as on the panel: ModeKeep sends no message, and a message goes out only
	// when the mode really changes
	if f.Mode != ModeKeep && (!p.haveMod || p.lastMod != f.Mode) {
		p.cnt.ModeChanges++
		p.lastMod, p.haveMod = f.Mode, true
	}
	if f.Full {
		p.cnt.FullReqs++
	}
	if p.Discard {
		p.mu.Unlock()
		return nil
	}
	if p.fbG == nil {
		p.fbG = image.NewGray(image.Rect(0, 0, p.W, p.H))
		for i := range p.fbG.Pix {
			p.fbG.Pix[i] = 255
		}
	}
	// the gray picture always follows the canvas
	for y := r.Min.Y; y < r.Max.Y; y++ {
		copy(p.fbG.Pix[y*p.fbG.Stride+r.Min.X:y*p.fbG.Stride+r.Max.X], f.Gray.Pix[y*f.Gray.Stride+r.Min.X:y*f.Gray.Stride+r.Max.X])
	}
	hasLayer := f.Color != nil && len(f.Color.Pix) >= p.W*p.H && f.Color.W == p.W
	if hasLayer && p.fbC == nil {
		p.fbC = image.NewRGBA(image.Rect(0, 0, p.W, p.H))
		for y := 0; y < p.H; y++ {
			for x := 0; x < p.W; x++ {
				v := p.fbG.Pix[y*p.fbG.Stride+x]
				p.fbC.SetRGBA(x, y, color.RGBA{v, v, v, 255})
			}
		}
	}
	if p.fbC != nil {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				if hasLayer && !inRects(x, y, f.Opaque) {
					c := f.Color.Pix[y*p.W+x]
					rr := uint8(c>>11&31) << 3
					rr |= rr >> 5
					gg := uint8(c>>5&63) << 2
					gg |= gg >> 6
					bb := uint8(c&31) << 3
					bb |= bb >> 5
					p.fbC.SetRGBA(x, y, color.RGBA{rr, gg, bb, 255})
				} else {
					v := f.Gray.Pix[y*f.Gray.Stride+x]
					p.fbC.SetRGBA(x, y, color.RGBA{v, v, v, 255})
				}
			}
		}
	}
	var img image.Image = p.fbG
	if p.fbC != nil {
		img = p.fbC
	}
	p.mu.Unlock()
	tag := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, f.Tag)
	name := fmt.Sprintf("%04d-%s.png", n, tag)
	fh, err := os.Create(filepath.Join(p.Dir, name))
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	err = enc.Encode(fh, img)
	fh.Close()
	if err != nil {
		return err
	}
	if p.logf != nil {
		fmt.Fprintf(p.logf, "%04d tag=%s mode=%s full=%v rect=%v colour=%v\n", n, f.Tag, f.Mode, f.Full, f.Rect, hasLayer)
	}
	if p.Keep > 0 && n > p.Keep {
		old, _ := filepath.Glob(filepath.Join(p.Dir, fmt.Sprintf("%04d-*.png", n-p.Keep)))
		for _, o := range old {
			os.Remove(o)
		}
	}
	return nil
}

// Close implements Display.
func (p *PNG) Close() error {
	p.finish()
	if p.logf != nil {
		p.logf.Close()
		p.logf = nil
	}
	return nil
}
