//go:build linux

package kobo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"rmkavita/internal/display"
)

const (
	fbiogetVScreenInfo = 0x4600
	fbioputVScreenInfo = 0x4601
	fbiogetFScreenInfo = 0x4602
	varSize            = 160
)

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	if e != 0 {
		return e
	}
	return nil
}

// Options configures Open.
type Options struct {
	FB       string // framebuffer device (default /dev/fb0)
	Rotate   int    // -1 automatic (portrait), else FB_ROTATE_* to set
	TouchMap string // calibrated transform ("" = identity until calibrated)
	Logf     func(format string, args ...any)
}

// Panel is the Kobo implementation of display.Display.
type Panel struct {
	fd      *os.File
	fb      []byte
	fi      FBInfo
	origVar []byte // the variable screen info before we changed it (nil: unchanged)
	logf    func(string, ...any)

	wmu    sync.Mutex
	armed  display.Mode
	marker uint32
	v1     bool // the driver wants the older update layout
	cnt    display.Counters

	in        *inputSet
	events    chan display.Event
	mmu       sync.Mutex
	tmap      TouchMap
	raw       bool // calibration: events carry normalised raw points scaled to the screen
	icnt      display.InputCounters
	done      chan struct{}
	closeOnce sync.Once
}

func le32(b []byte, off int) int { return int(binary.LittleEndian.Uint32(b[off:])) }

// readVar returns the raw fb_var_screeninfo.
func readVar(fd uintptr) ([]byte, error) {
	b := make([]byte, varSize)
	if err := ioctl(fd, fbiogetVScreenInfo, unsafe.Pointer(&b[0])); err != nil {
		return nil, fmt.Errorf("FBIOGET_VSCREENINFO: %w", err)
	}
	return b, nil
}

func parseInfo(v, f []byte) FBInfo {
	ptr := int(unsafe.Sizeof(uintptr(0)))
	// fb_fix_screeninfo: id[16], smem_start (ulong), smem_len, type, type_aux,
	// visual (u32), xpanstep, ypanstep, ywrapstep (u16), line_length (u32, aligned)
	lineOff := 16 + ptr + 4 + 12 + 6
	lineOff = (lineOff + 3) &^ 3
	fi := FBInfo{
		XRes: le32(v, 0), YRes: le32(v, 4),
		XOff: le32(v, 16), YOff: le32(v, 20),
		BPP: le32(v, 24), Grayscale: le32(v, 28) != 0,
		Stride: le32(f, lineOff),
		Rotate: le32(v, 136),
	}
	// bitfields: red @32, green @44, blue @56, transp @68 (offset, length, msb_right)
	fi.ROff, fi.GOff, fi.BOff, fi.AOff = le32(v, 32)/8, le32(v, 44)/8, le32(v, 56)/8, le32(v, 68)/8
	fi.ALen = le32(v, 72)
	return fi
}

// Open takes over the framebuffer and the input devices.
func Open(o Options) (*Panel, error) {
	if o.FB == "" {
		o.FB = "/dev/fb0"
	}
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	f, err := os.OpenFile(o.FB, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	fd := f.Fd()
	v, err := readVar(fd)
	if err != nil {
		f.Close()
		return nil, err
	}
	p := &Panel{fd: f, logf: logf, armed: display.ModeUI, done: make(chan struct{}), events: make(chan display.Event, 256)}
	xres, yres, rot := le32(v, 0), le32(v, 4), le32(v, 136)
	logf("kobo: fb %dx%d bpp=%d rotate=%d grayscale=%d", xres, yres, le32(v, 24), rot, le32(v, 28))
	want := o.Rotate
	if want < 0 && xres > yres {
		// a landscape buffer: turn it a quarter so the app gets a portrait screen
		want = (rot + 1) % 4
	}
	if want >= 0 && want != rot {
		p.origVar = append([]byte(nil), v...)
		nv := append([]byte(nil), v...)
		binary.LittleEndian.PutUint32(nv[136:], uint32(want))
		if (want-rot)%2 != 0 {
			// a quarter turn swaps the visible and the virtual sizes
			binary.LittleEndian.PutUint32(nv[0:], uint32(yres))
			binary.LittleEndian.PutUint32(nv[4:], uint32(xres))
			binary.LittleEndian.PutUint32(nv[8:], uint32(le32(v, 12)))
			binary.LittleEndian.PutUint32(nv[12:], uint32(le32(v, 8)))
		}
		binary.LittleEndian.PutUint32(nv[84:], 0) // activate = FB_ACTIVATE_NOW
		if err := ioctl(fd, fbioputVScreenInfo, unsafe.Pointer(&nv[0])); err != nil {
			logf("kobo: could not set rotate=%d: %v (keeping %d)", want, err, rot)
			p.origVar = nil
		}
		if v, err = readVar(fd); err != nil {
			f.Close()
			return nil, err
		}
		logf("kobo: fb now %dx%d rotate=%d", le32(v, 0), le32(v, 4), le32(v, 136))
	}
	fix := make([]byte, 128)
	if err := ioctl(fd, fbiogetFScreenInfo, unsafe.Pointer(&fix[0])); err != nil {
		f.Close()
		return nil, fmt.Errorf("FBIOGET_FSCREENINFO: %w", err)
	}
	p.fi = parseInfo(v, fix)
	ptr := int(unsafe.Sizeof(uintptr(0)))
	smemLen := le32(fix, 16+ptr)
	if p.fi.BPP != 8 && p.fi.BPP != 16 && p.fi.BPP != 32 {
		f.Close()
		return nil, fmt.Errorf("framebuffer depth %d is not supported", p.fi.BPP)
	}
	need := p.fi.base() + p.fi.YRes*p.fi.Stride
	if smemLen < need || p.fi.Stride < p.fi.XRes*p.fi.BPP/8 {
		f.Close()
		return nil, fmt.Errorf("framebuffer layout not understood (smem=%d stride=%d need=%d)", smemLen, p.fi.Stride, need)
	}
	p.fb, err = syscall.Mmap(int(fd), 0, smemLen, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("mmap: %w", err)
	}
	logf("kobo: screen %dx%d stride=%d bpp=%d offset=%d,%d rgba-bytes=%d,%d,%d,%d", p.fi.XRes, p.fi.YRes, p.fi.Stride, p.fi.BPP,
		p.fi.XOff, p.fi.YOff, p.fi.ROff, p.fi.GOff, p.fi.BOff, p.fi.AOff)

	p.in, err = openInputs(logf)
	if err != nil {
		p.Close()
		return nil, err
	}
	p.tmap = TouchMap{RX: p.in.rx, RY: p.in.ry, W: p.fi.XRes, H: p.fi.YRes}
	p.tmap.Swap, p.tmap.MirX, p.tmap.MirY = ParseMap(o.TouchMap)
	go p.in.run(p.onContact, p.onKey, p.done)
	return p, nil
}

// Info is the framebuffer layout in use.
func (p *Panel) Info() FBInfo { return p.fi }

// Size implements display.Display.
func (p *Panel) Size() (int, int) { return p.fi.XRes, p.fi.YRes }

// Events implements display.Display.
func (p *Panel) Events() <-chan display.Event { return p.events }

// PixelFormat implements display.Formatted.
func (p *Panel) PixelFormat() string { return p.fi.PixelFormat() }

// Counters implements display.Counted.
func (p *Panel) Counters() display.Counters {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	return p.cnt
}

// InputCounters implements display.InputCounted.
func (p *Panel) InputCounters() display.InputCounters {
	p.mmu.Lock()
	defer p.mmu.Unlock()
	return p.icnt
}

// Arm implements display.Armer. The EPDC takes the waveform per update, so
// arming only remembers what ModeKeep means.
func (p *Panel) Arm(m display.Mode) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if m != display.ModeKeep {
		p.armed = m
	}
	return nil
}

// SetTouchMap replaces the touch transform (after a calibration).
func (p *Panel) SetTouchMap(swap, mx, my bool) {
	p.mmu.Lock()
	p.tmap.Swap, p.tmap.MirX, p.tmap.MirY = swap, mx, my
	p.mmu.Unlock()
}

// SetRaw switches calibration mode: events carry the raw point normalised over
// the raw ranges and scaled to the screen, without swapping or mirroring.
func (p *Panel) SetRaw(on bool) {
	p.mmu.Lock()
	p.raw = on
	p.mmu.Unlock()
}

// Show implements display.Display: the region is converted into the
// framebuffer and one update request is sent for it.
func (p *Panel) Show(f display.Frame) error {
	r := f.Rect.Intersect(image.Rect(0, 0, p.fi.XRes, p.fi.YRes))
	if r.Empty() {
		return nil
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if p.fb == nil {
		return errors.New("display closed")
	}
	Convert(p.fb, p.fi, f, r)
	mode := f.Mode
	if mode == display.ModeKeep {
		mode = p.armed
	} else {
		if mode != p.armed {
			p.cnt.ModeChanges++
		}
		p.armed = mode
	}
	if f.Full {
		r = image.Rect(0, 0, p.fi.XRes, p.fi.YRes) // the flash covers the whole panel
		p.cnt.FullReqs++
	}
	wf, um := waveFor(mode, f.Full)
	p.marker++
	if p.marker == 0 {
		p.marker = 1
	}
	if err := p.send(r, wf, um, p.marker); err != nil {
		return err
	}
	p.cnt.Updates++
	p.cnt.Pixels += r.Dx() * r.Dy()
	return nil
}

// send issues MXCFB_SEND_UPDATE in the layout the driver accepts (the newer
// one first; the older one is remembered once it is the one that works).
func (p *Panel) send(r image.Rectangle, wf, mode, marker uint32) error {
	fd := p.fd.Fd()
	if !p.v1 {
		b := encodeUpdate(r, wf, mode, marker, true)
		err := ioctl(fd, ioSendV2, unsafe.Pointer(&b[0]))
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.ENOTTY) && !errors.Is(err, syscall.EINVAL) {
			return fmt.Errorf("MXCFB_SEND_UPDATE: %w", err)
		}
		p.logf("kobo: update v2 refused (%v), trying the older layout", err)
		p.v1 = true
	}
	b := encodeUpdate(r, wf, mode, marker, false)
	if err := ioctl(fd, ioSendV1NTX, unsafe.Pointer(&b[0])); err != nil {
		return fmt.Errorf("MXCFB_SEND_UPDATE (v1): %w", err)
	}
	return nil
}

// WaitIdle waits (up to timeout) for the last update to reach the panel: the
// launcher restarts Nickel only after the app's last frame is on screen.
func (p *Panel) WaitIdle(timeout time.Duration) {
	p.wmu.Lock()
	m := p.marker
	fb := p.fb
	p.wmu.Unlock()
	if fb == nil || m == 0 {
		return
	}
	done := make(chan struct{})
	go func() {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint32(b, m)
		_ = ioctl(p.fd.Fd(), ioWaitV3, unsafe.Pointer(&b[0]))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func (p *Panel) emit(ev display.Event) {
	select {
	case p.events <- ev:
	default:
		p.mmu.Lock()
		p.icnt.Dropped++ // the UI is far behind: shed this sample
		p.mmu.Unlock()
	}
}

func (p *Panel) onContact(c Contact) {
	p.mmu.Lock()
	var x, y int
	if p.raw {
		nx, ny := p.tmap.Normalized(c.X, c.Y)
		x, y = int(nx*float64(p.fi.XRes-1)+0.5), int(ny*float64(p.fi.YRes-1)+0.5)
	} else {
		x, y = p.tmap.Apply(c.X, c.Y)
	}
	switch c.Kind {
	case display.Down:
		p.icnt.Down++
	case display.Up:
		p.icnt.Up++
	case display.Move:
		p.icnt.Move++
	}
	p.mmu.Unlock()
	p.emit(display.Event{Kind: c.Kind, X: x, Y: y, ID: c.ID, At: time.Now()})
}

func (p *Panel) onKey(code uint16) {
	if b := ButtonFor(code); b != "" {
		p.logf("kobo: key %d -> %s", code, b)
		p.emit(display.Event{Kind: display.Button, Text: b, At: time.Now()})
	}
}

// Close releases the input devices, restores the framebuffer rotation and
// unmaps it. Safe to call twice.
func (p *Panel) Close() error {
	p.closeOnce.Do(func() {
		close(p.done)
		if p.in != nil {
			p.in.close()
		}
		p.wmu.Lock()
		defer p.wmu.Unlock()
		if p.origVar != nil {
			_ = ioctl(p.fd.Fd(), fbioputVScreenInfo, unsafe.Pointer(&p.origVar[0]))
		}
		if p.fb != nil {
			_ = syscall.Munmap(p.fb)
			p.fb = nil
		}
		_ = p.fd.Close()
	})
	return nil
}
