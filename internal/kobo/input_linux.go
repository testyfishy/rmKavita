//go:build linux

package kobo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"unsafe"
)

// evdev ioctls (all read-direction, 'E' type).
func eviocgbit(ev, n int) uintptr { return ior(n, 0x20+ev) }
func eviocgabs(code int) uintptr  { return ior(24, 0x40+code) }
func eviocgname(n int) uintptr    { return ior(n, 0x06) }

// ior is _IOC(_IOC_READ, 'E', nr, size).
func ior(size, nr int) uintptr {
	return uintptr(uint32(2)<<30 | uint32(size)<<16 | uint32('E')<<8 | uint32(nr))
}

const eviocgrab = 1<<30 | 4<<16 | 'E'<<8 | 0x90

func bit(b []byte, n int) bool { return n/8 < len(b) && b[n/8]&(1<<(n%8)) != 0 }

type inputDev struct {
	f     *os.File
	name  string
	touch bool
}

type inputSet struct {
	devs   []*inputDev
	rx, ry AbsRange
	wg     sync.WaitGroup
	logf   func(string, ...any)
}

// openInputs finds the touch screen and the key devices (power, page turn),
// and grabs them so nothing else reads them while the app runs.
func openInputs(logf func(string, ...any)) (*inputSet, error) {
	s := &inputSet{logf: logf}
	for i := 0; i < 16; i++ {
		path := fmt.Sprintf("/dev/input/event%d", i)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		fd := f.Fd()
		name := make([]byte, 128)
		_ = ioctl(fd, eviocgname(len(name)), unsafe.Pointer(&name[0]))
		nm := string(name[:clen(name)])
		evb := make([]byte, 4)
		_ = ioctl(fd, eviocgbit(0, len(evb)), unsafe.Pointer(&evb[0]))
		absb := make([]byte, 8)
		keyb := make([]byte, 96)
		if bit(evb, evAbs) {
			_ = ioctl(fd, eviocgbit(evAbs, len(absb)), unsafe.Pointer(&absb[0]))
		}
		if bit(evb, evKey) {
			_ = ioctl(fd, eviocgbit(evKey, len(keyb)), unsafe.Pointer(&keyb[0]))
		}
		mt := bit(absb, absMTPosX) && bit(absb, absMTPosY)
		// a single-touch panel reports BTN_TOUCH; an accelerometer (the Libra 2's
		// kx122) has ABS_X / ABS_Y too, but no touch key, and must not be taken
		// for the screen (its +-2^31 range would squash every tap to the centre)
		st := bit(absb, absX) && bit(absb, absY) && bit(keyb, btnTouch) && !strings.Contains(strings.ToLower(nm), "accel")
		if s.touchFound() && (mt || st) {
			logf("kobo: %s %q: a second touch device, ignored", path, nm)
			f.Close()
			continue
		}
		keys := false
		for _, k := range []int{keyF23, keyF24, keyPageUp, keyPageDown, keyPower} {
			if bit(keyb, k) {
				keys = true
			}
		}
		d := &inputDev{f: f, name: nm}
		switch {
		case mt || st:
			cx, cy := absMTPosX, absMTPosY
			if !mt {
				cx, cy = absX, absY
			}
			s.rx, s.ry = absRange(fd, cx), absRange(fd, cy)
			d.touch = true
			logf("kobo: %s %q: touch (mt=%v) x %d..%d y %d..%d", path, nm, mt, s.rx.Min, s.rx.Max, s.ry.Min, s.ry.Max)
		case keys:
			logf("kobo: %s %q: keys", path, nm)
		default:
			f.Close()
			continue
		}
		one := int32(1)
		if err := ioctl(fd, eviocgrab, unsafe.Pointer(&one)); err != nil {
			logf("kobo: grab %s: %v", path, err)
		}
		s.devs = append(s.devs, d)
	}
	haveTouch := false
	for _, d := range s.devs {
		haveTouch = haveTouch || d.touch
	}
	if !haveTouch {
		s.close()
		return nil, errors.New("no touch screen found")
	}
	return s, nil
}

func (s *inputSet) touchFound() bool {
	for _, d := range s.devs {
		if d.touch {
			return true
		}
	}
	return false
}

// RawRanges are the raw ranges of the touch screen's axes.
func (p *Panel) RawRanges() (AbsRange, AbsRange) { return p.in.rx, p.in.ry }

func clen(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return len(b)
}

func absRange(fd uintptr, code int) AbsRange {
	b := make([]byte, 24) // value, minimum, maximum, fuzz, flat, resolution
	if err := ioctl(fd, eviocgabs(code), unsafe.Pointer(&b[0])); err != nil {
		return AbsRange{0, 4095}
	}
	return AbsRange{Min: int(int32(binary.LittleEndian.Uint32(b[4:]))), Max: int(int32(binary.LittleEndian.Uint32(b[8:])))}
}

// run reads every device until done; contacts and key presses go to the
// callbacks (from the reading goroutines).
func (s *inputSet) run(onContact func(Contact), onKey func(uint16), done <-chan struct{}) {
	ptr := int(unsafe.Sizeof(uintptr(0)))
	evSize := 2*ptr + 8 // struct timeval + type, code, value
	for _, d := range s.devs {
		d := d
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			var tp TouchParser
			buf := make([]byte, evSize*64)
			for {
				n, err := d.f.Read(buf)
				if err != nil {
					select {
					case <-done:
					default:
						s.logf("kobo: input %q: %v", d.name, err)
					}
					return
				}
				for off := 0; off+evSize <= n; off += evSize {
					e := buf[off+2*ptr:]
					ev := RawEvent{Type: binary.LittleEndian.Uint16(e[0:]), Code: binary.LittleEndian.Uint16(e[2:]),
						Value: int32(binary.LittleEndian.Uint32(e[4:]))}
					if d.touch {
						tp.Feed(ev, onContact)
						continue
					}
					if ev.Type == evKey && ev.Value == 1 {
						onKey(ev.Code)
					}
				}
			}
		}()
	}
}

func (s *inputSet) close() {
	for _, d := range s.devs {
		zero := int32(0)
		_ = ioctl(d.f.Fd(), eviocgrab, unsafe.Pointer(&zero))
		_ = d.f.Close()
	}
}
