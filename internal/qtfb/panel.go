package qtfb

import (
	"encoding/binary"
	"errors"
	"image"
	"sync"
	"time"

	"rmkavita/internal/display"
)

// PacketConn is the part of a SOCK_SEQPACKET connection the panel needs: each
// Read returns exactly one message, and a closed peer reads as an error.
type PacketConn interface {
	Read(b []byte) (int, error)
	Write(b []byte) (int, error)
	Close() error
}

// Panel is the qtfb implementation of display.Display.
type Panel struct {
	model   Model
	format  PixFmt
	conn    PacketConn
	shm     []byte
	unmap   func()
	events  chan display.Event
	lut565  [256]uint16
	lutRGB  [256]uint32 // gray level -> R,G,B,A bytes (v, v, v, 255)
	from565 []uint32    // 565 value -> RGBA bytes, built on the first colour frame

	wmu     sync.Mutex
	curMode int
	cnt     display.Counters

	// Input: the read loop never waits for the UI. Events go into a bounded
	// queue (a burst of move samples is coalesced, a full queue sheds the oldest
	// move first) and a pump goroutine hands them to the UI in order.
	qmu     sync.Mutex
	queue   []display.Event
	qclosed bool
	wake    chan struct{}
	in      display.InputCounters

	closeOnce sync.Once
	done      chan struct{}
}

// NewPanel wraps an initialised connection and its mapped framebuffer. shm
// must hold at least W*H*bytes-per-pixel of format. unmap, if not nil, runs on
// Close.
func NewPanel(model Model, format PixFmt, conn PacketConn, shm []byte, unmap func()) (*Panel, error) {
	need := model.W * model.H * format.BytesPerPixel()
	if len(shm) < need {
		return nil, errors.New("framebuffer smaller than the screen")
	}
	p := &Panel{model: model, format: format, conn: conn, shm: shm, unmap: unmap,
		events: make(chan display.Event, 64), curMode: -1, done: make(chan struct{}), wake: make(chan struct{}, 1)}
	p.lut565 = Gray565LUT()
	for g := range p.lutRGB {
		v := uint32(g)
		p.lutRGB[g] = v | v<<8 | v<<16 | 0xFF<<24 // little-endian bytes: R, G, B, A
	}
	// Start white, as KOReader does, so a half-initialised first frame is not black.
	for i := 0; i < need; i++ {
		shm[i] = 0xFF
	}
	go p.pump()
	go p.readLoop()
	return p, nil
}

// Size implements display.Display.
func (p *Panel) Size() (int, int) { return p.model.W, p.model.H }

// Events implements display.Display.
func (p *Panel) Events() <-chan display.Event { return p.events }

// PixelFormat implements display.Formatted.
func (p *Panel) PixelFormat() string { return p.format.String() }

// Format is the negotiated framebuffer layout.
func (p *Panel) Format() PixFmt { return p.format }

func (p *Panel) write(b []byte) error {
	_, err := p.conn.Write(b)
	return err
}

func modeValue(m display.Mode) int {
	switch m {
	case display.ModeFast:
		return RefreshFast
	case display.ModeContent:
		return RefreshContent
	case display.ModeAnimate:
		return RefreshAnimate
	}
	return RefreshUI
}

// setMode sends SET_REFRESH_MODE only when the mode really changes (the server
// keeps it). Caller holds wmu.
func (p *Panel) setMode(m display.Mode) error {
	if m == display.ModeKeep {
		return nil
	}
	mode := modeValue(m)
	if p.curMode == mode {
		return nil
	}
	if err := p.write(EncodeSetMode(mode)); err != nil {
		return err
	}
	p.curMode = mode
	p.cnt.ModeChanges++
	return nil
}

// Arm implements display.Armer.
func (p *Panel) Arm(m display.Mode) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if p.shm == nil {
		return errors.New("display closed")
	}
	return p.setMode(m)
}

// Show converts the changed region to the framebuffer format and asks for the
// update. The order is the reference client's: pixels, refresh mode (only when
// it changes, the server keeps it), update rectangle, then the optional full
// refresh.
func (p *Panel) Show(f display.Frame) error {
	r := f.Rect.Intersect(image.Rect(0, 0, p.model.W, p.model.H))
	if r.Empty() {
		return nil
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if p.shm == nil {
		return errors.New("display closed")
	}
	layer := f.Color != nil && f.Color.W == p.model.W && len(f.Color.Pix) >= p.model.W*p.model.H
	switch {
	case p.format == FmtRGBA8888 && layer:
		p.convertLayerRGBA(f.Color, f.Gray, f.Opaque, r)
	case p.format == FmtRGBA8888:
		ConvertRGBA(p.shm, p.model.W*4, f.Gray, r, &p.lutRGB)
	case layer:
		ConvertLayer565(p.shm, p.model.W*2, f.Color, f.Gray, f.Opaque, r, &p.lut565)
	default:
		Convert565(p.shm, p.model.W*2, f.Gray, r, &p.lut565)
	}
	if err := p.setMode(f.Mode); err != nil {
		return err
	}
	if err := p.write(EncodeUpdate(r.Min.X, r.Min.Y, r.Dx(), r.Dy())); err != nil {
		return err
	}
	p.cnt.Updates++
	p.cnt.Pixels += r.Dx() * r.Dy()
	if f.Full {
		p.cnt.FullReqs++
		return p.write(EncodeFullRefresh())
	}
	return nil
}

// Counters implements display.Counted: what has been sent to the server.
func (p *Panel) Counters() display.Counters {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	return p.cnt
}

// ConvertLayer565 writes region r of the colour layer into the framebuffer,
// except inside the opaque rectangles, where the gray canvas (the UI drawn
// over the page) is converted instead.
func ConvertLayer565(dst []byte, stride int, col *display.Layer, gray *image.Gray, opaque []image.Rectangle, r image.Rectangle, lut *[256]uint16) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		src := col.Pix[y*col.W+r.Min.X : y*col.W+r.Max.X]
		d := dst[y*stride+r.Min.X*2 : y*stride+r.Max.X*2]
		for i, v := range src {
			d[2*i] = byte(v)
			d[2*i+1] = byte(v >> 8)
		}
	}
	for _, o := range opaque {
		o = o.Intersect(r)
		if !o.Empty() {
			Convert565(dst, stride, gray, o, lut)
		}
	}
}

// expand565 is the RGBA bytes of one RGB565 value. A value that is exactly a
// neutral gray of the page cache (the 5-bit level replicated into the 6-bit
// green) expands to a true gray (r = g = b), not to Qt's tinted expansion.
func expand565(v uint16) uint32 {
	r5, g6, b5 := uint32(v>>11)&31, uint32(v>>5)&63, uint32(v)&31
	if r5 == b5 && g6 == r5<<1|r5>>4 {
		g := r5<<3 | r5>>2
		return g | g<<8 | g<<16 | 0xFF<<24
	}
	r := r5<<3 | r5>>2
	g := g6<<2 | g6>>4
	b := b5<<3 | b5>>2
	return r | g<<8 | b<<16 | 0xFF<<24
}

func (p *Panel) convertLayerRGBA(col *display.Layer, gray *image.Gray, opaque []image.Rectangle, r image.Rectangle) {
	if p.from565 == nil {
		t := make([]uint32, 65536)
		for i := range t {
			t[i] = expand565(uint16(i))
		}
		p.from565 = t
	}
	stride := p.model.W * 4
	for y := r.Min.Y; y < r.Max.Y; y++ {
		src := col.Pix[y*col.W+r.Min.X : y*col.W+r.Max.X]
		d := p.shm[y*stride+r.Min.X*4 : y*stride+r.Max.X*4]
		for i, v := range src {
			binary.LittleEndian.PutUint32(d[4*i:], p.from565[v])
		}
	}
	for _, o := range opaque {
		o = o.Intersect(r)
		if !o.Empty() {
			ConvertRGBA(p.shm, stride, gray, o, &p.lutRGB)
		}
	}
}

// ConvertRGBA writes the gray region r of src into the RGBA8888 framebuffer
// dst (stride in bytes) as (v, v, v, 255): truly neutral, alpha opaque.
func ConvertRGBA(dst []byte, stride int, src *image.Gray, r image.Rectangle, lut *[256]uint32) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		s := src.Pix[src.PixOffset(r.Min.X, y) : src.PixOffset(r.Min.X, y)+r.Dx()]
		d := dst[y*stride+r.Min.X*4 : y*stride+r.Max.X*4]
		for i, g := range s {
			binary.LittleEndian.PutUint32(d[4*i:], lut[g])
		}
	}
}

// maxQueued bounds the input queue. A touch screen produces a few hundred
// samples a second at most; the UI normally keeps up, so the queue only fills
// while it is busy decoding a page.
const maxQueued = 256

// InputCounters implements display.InputCounted.
func (p *Panel) InputCounters() display.InputCounters {
	p.qmu.Lock()
	defer p.qmu.Unlock()
	return p.in
}

// enqueue adds one event without ever blocking the read loop.
func (p *Panel) enqueue(ev display.Event) {
	p.qmu.Lock()
	switch ev.Kind {
	case display.Down:
		p.in.Down++
	case display.Up:
		p.in.Up++
	case display.Move:
		p.in.Move++
	}
	if n := len(p.queue); ev.Kind == display.Move && n > 0 && p.queue[n-1].Kind == display.Move && p.queue[n-1].ID == ev.ID {
		p.queue[n-1] = ev // the finger moved on before the UI looked: keep the newest sample
		p.in.Coalesced++
		p.qmu.Unlock()
		return
	}
	if len(p.queue) >= maxQueued {
		shed := false
		for i, q := range p.queue {
			if q.Kind == display.Move {
				p.queue = append(p.queue[:i], p.queue[i+1:]...)
				shed = true
				break
			}
		}
		if !shed {
			p.queue = p.queue[1:] // only presses and releases: the oldest goes
		}
		p.in.Dropped++
	}
	p.queue = append(p.queue, ev)
	p.qmu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// pump delivers the queued events to the UI, in order, and closes the events
// channel once the read loop has ended and the queue is empty.
func (p *Panel) pump() {
	defer close(p.events)
	for {
		p.qmu.Lock()
		var ev display.Event
		have := len(p.queue) > 0
		if have {
			ev = p.queue[0]
			p.queue = p.queue[1:]
		}
		closed := p.qclosed
		p.qmu.Unlock()
		switch {
		case have:
			select {
			case p.events <- ev:
			case <-p.done:
				return
			}
		case closed:
			return
		default:
			select {
			case <-p.wake:
			case <-p.done:
				return
			}
		}
	}
}

func (p *Panel) readLoop() {
	defer func() {
		p.qmu.Lock()
		p.qclosed = true
		p.qmu.Unlock()
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}()
	buf := make([]byte, 128)
	for {
		n, err := p.conn.Read(buf)
		if err != nil || n == 0 {
			return // peer closed (AppLoad's X, xochitl gone) or socket error: the UI exits
		}
		if n < ServerMsgSize {
			continue
		}
		in, err := DecodeInput(buf[:n])
		if err != nil {
			continue
		}
		var kind display.EventKind
		switch in.Type {
		case InTouchPress, InPenPress:
			kind = display.Down
		case InTouchUpdate, InPenUpdate:
			kind = display.Move
		case InTouchRelease, InPenRelease:
			kind = display.Up
		default:
			continue
		}
		p.enqueue(display.Event{Kind: kind, X: in.X, Y: in.Y, ID: in.Dev, At: time.Now()})
	}
}

// Close sends MESSAGE_TERMINATE, closes the socket and unmaps the framebuffer.
func (p *Panel) Close() error {
	p.closeOnce.Do(func() {
		close(p.done)
		p.wmu.Lock()
		defer p.wmu.Unlock()
		_ = p.write(EncodeTerminate())
		_ = p.conn.Close()
		if p.unmap != nil {
			p.unmap()
		}
		p.shm = nil
	})
	return nil
}

// Gray565LUT maps an 8-bit gray to the RGB565 value of a neutral gray: the
// level is quantised once to 5 bits and replicated into all three channels,
// so independent rounding of R/G/B can never tint it.
func Gray565LUT() [256]uint16 {
	var l [256]uint16
	for g := 0; g < 256; g++ {
		v5 := (g*31 + 127) / 255
		v6 := v5<<1 | v5>>4
		l[g] = uint16(v5<<11 | v6<<5 | v5)
	}
	return l
}

// Convert565 writes the gray region r of src into the little-endian RGB565
// framebuffer dst (stride in bytes).
func Convert565(dst []byte, stride int, src *image.Gray, r image.Rectangle, lut *[256]uint16) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		s := src.Pix[src.PixOffset(r.Min.X, y) : src.PixOffset(r.Min.X, y)+r.Dx()]
		d := dst[y*stride+r.Min.X*2 : y*stride+r.Max.X*2]
		for i, g := range s {
			v := lut[g]
			d[2*i] = byte(v)
			d[2*i+1] = byte(v >> 8)
		}
	}
}
