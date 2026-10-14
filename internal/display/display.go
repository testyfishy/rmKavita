// Package display is the seam between the UI and the panel. The UI draws into
// a gray canvas (and, for colour pages, a colour layer) and hands it to a
// Display together with the refresh it wants; the qtfb backend converts that to
// RGB565 shared memory for AppLoad, the PNG backend writes it to files so every
// screen can be inspected off the device.
package display

import (
	"image"
	"time"
)

// Mode is the refresh waveform a frame should use.
type Mode int

const (
	ModeFast    Mode = iota // FAST: quickest, no flash, loses colour on colour panels
	ModeUI                  // UI: partial updates for menus; keeps colour
	ModeContent             // CONTENT: highest quality, slow (the clearing flash)
	ModeAnimate             // ANIMATE
	// ModeKeep leaves whatever refresh mode is armed (no SET_REFRESH_MODE is
	// sent): used for a frame that must not cause a mode flip.
	ModeKeep
)

func (m Mode) String() string {
	switch m {
	case ModeFast:
		return "fast"
	case ModeUI:
		return "ui"
	case ModeContent:
		return "content"
	case ModeAnimate:
		return "animate"
	case ModeKeep:
		return "keep"
	}
	return "?"
}

// ParseMode maps a config word to a Mode (unknown -> ModeUI).
func ParseMode(s string) Mode {
	switch s {
	case "fast":
		return ModeFast
	case "content":
		return ModeContent
	case "animate":
		return ModeAnimate
	}
	return ModeUI
}

// EventKind is a touch phase (or a typed key, from test backends).
type EventKind int

const (
	Down EventKind = iota
	Move
	Up
	// Key carries typed text (Event.Text). The panel backend never produces it;
	// the PNG test backend does, so sign-in can be scripted without computing
	// key coordinates.
	Key
)

// Event is one touch (or pen) sample in framebuffer pixels.
type Event struct {
	Kind EventKind
	X, Y int
	ID   int // finger id
	Text string
	At   time.Time
}

// Layer is a full-screen RGB565 picture (row-major, W*H values). A frame that
// carries one takes its colour from it.
type Layer struct {
	Pix  []uint16
	W, H int
}

// Frame is one update request.
type Frame struct {
	Gray *image.Gray     // the whole canvas
	Rect image.Rectangle // region that changed
	Mode Mode
	Full bool   // also request the full (clearing) refresh
	Tag  string // free text for logs and file names

	// Color, when set, supplies the pixels of Rect except inside Opaque, where
	// Gray (the UI drawn over the page) wins.
	Color  *Layer
	Opaque []image.Rectangle
}

// Display is a panel backend.
type Display interface {
	// Size is the framebuffer size in pixels.
	Size() (w, h int)
	// Show pushes frame to the panel.
	Show(f Frame) error
	// Events delivers touches; it is closed when the connection is gone.
	Events() <-chan Event
	// Close terminates the connection politely. Safe to call twice.
	Close() error
}

// Idler is implemented by backends that want to know when the UI has gone
// quiet (the scripted PNG backend paces its input on it).
type Idler interface {
	// Idle is called with the number of input events consumed so far.
	Idle(consumed int)
}

// Counters are the messages a backend has sent so far.
type Counters struct {
	Updates     int // update rectangles
	ModeChanges int // refresh-mode switches
	FullReqs    int // full-refresh requests (the flashing ones)
	Pixels      int // total pixels in the update rectangles
}

// Counted is implemented by backends that count what they send (the qtfb
// panel does); the app logs the counters so a flash can be matched to a message.
type Counted interface {
	Counters() Counters
}

// Armer is implemented by backends that can set the refresh mode without
// drawing (the qtfb panel). The app arms the waveform of the activity it is in
// right after a clearing refresh, so the server's stall after SET_REFRESH_MODE
// overlaps the panel's own flash instead of delaying the next page turn.
type Armer interface {
	// Arm makes mode the refresh mode of the following updates; it is a no-op
	// when that mode is already armed.
	Arm(mode Mode) error
}

// Formatted reports the pixel format a backend negotiated ("rgba8888",
// "rgb565", "png").
type Formatted interface {
	PixelFormat() string
}

// InputCounters are the touch messages a backend has received since it
// started: what arrived, and what it had to drop (a full queue sheds the oldest
// move sample first). They go into the log so that "no events arrived" and
// "events arrived and were ignored" can be told apart after the app got stuck.
type InputCounters struct {
	Down, Up, Move int64
	Coalesced      int64 // move samples merged into the previous one
	Dropped        int64 // events shed because the queue was full
}

// InputCounted is implemented by backends that count their input.
type InputCounted interface {
	InputCounters() InputCounters
}
