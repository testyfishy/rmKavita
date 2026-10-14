package ui

import (
	"time"
)

// settingsScrollTo swipes the Settings list up until the named hit target is on
// screen (the list is several screens long).
func (h *harness) settingsScrollTo(name string) {
	h.t.Helper()
	for i := 0; i < 14; i++ {
		if _, ok := h.hitRect(name); ok {
			return
		}
		if !h.settingsScrollOnce() {
			break
		}
	}
	if _, ok := h.hitRect(name); !ok {
		h.t.Fatalf("no %q anywhere in the list", name)
	}
}

// settingsScrollOnce scrolls the list by a swipe; false when it did not move.
func (h *harness) settingsScrollOnce() bool {
	n := h.disp.count("settings-scroll")
	h.swipe(800, 1700, 800, 400)
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		if h.disp.count("settings-scroll") > n {
			time.Sleep(20 * time.Millisecond)
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// settingsHits returns the names of every hit target of the open settings list
// (scrolling through all of it; the list is left scrolled to its end).
func (h *harness) settingsHits() map[string]bool {
	h.t.Helper()
	have := map[string]bool{}
	for i := 0; i < 16; i++ {
		for _, hi := range h.snapshot().Hits {
			have[hi.Name] = true
		}
		if !h.settingsScrollOnce() {
			break
		}
	}
	for _, hi := range h.snapshot().Hits {
		have[hi.Name] = true
	}
	return have
}

// settingsScrollTop swipes the list back to its top.
func (h *harness) settingsScrollTop() {
	h.t.Helper()
	for i := 0; i < 16; i++ {
		n := h.disp.count("settings-scroll")
		h.swipe(800, 400, 800, 1700)
		moved := false
		deadline := time.Now().Add(600 * time.Millisecond)
		for time.Now().Before(deadline) {
			if h.disp.count("settings-scroll") > n {
				moved = true
				time.Sleep(20 * time.Millisecond)
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !moved {
			return
		}
	}
}

// shown is the luminance the simulated panel shows at (x, y).
func (h *harness) shown(x, y int) uint8 { return h.disp.shown(x, y) }

// stripStats summarises a rectangle of the panel: the most common value (the
// background), the darkest and the lightest.
func (h *harness) stripStats(x0, y0, x1, y1 int) (bg, lo, hi uint8) {
	var hist [256]int
	lo, hi = 255, 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x += 2 {
			v := h.shown(x, y)
			hist[v]++
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
	}
	best := 0
	for v, n := range hist {
		if n > hist[best] {
			best = v
		}
	}
	return uint8(best), lo, hi
}
