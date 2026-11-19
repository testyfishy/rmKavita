//go:build !linux

package kobo

import (
	"errors"
	"time"

	"rmkavita/internal/display"
)

// Options configures Open.
type Options struct {
	FB       string
	Rotate   int
	TouchMap string
	Logf     func(format string, args ...any)
}

// Panel is the Kobo display; it exists only on Linux.
type Panel struct{}

// Open fails off the device.
func Open(Options) (*Panel, error) { return nil, errors.New("the Kobo backend runs on the device only") }

func (p *Panel) Info() FBInfo                 { return FBInfo{} }
func (p *Panel) PixelFormat() string          { return "" }
func (p *Panel) RawRanges() (AbsRange, AbsRange) { return AbsRange{}, AbsRange{} }
func (p *Panel) Size() (int, int)             { return 0, 0 }
func (p *Panel) Events() <-chan display.Event { return nil }
func (p *Panel) Show(display.Frame) error     { return errors.New("not on a Kobo") }
func (p *Panel) Close() error                 { return nil }
func (p *Panel) SetTouchMap(bool, bool, bool) {}
func (p *Panel) SetRaw(bool)                  {}
func (p *Panel) WaitIdle(time.Duration)       {}
