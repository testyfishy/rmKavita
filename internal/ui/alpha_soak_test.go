package ui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// A soak with a 1000-series library: open it (the fill, 50 requests), jump
// through the whole alphabet, scroll, open a series and come back, go home and
// open it again (the session cache and its staleness check). After a warm-up lap
// goroutines come back to the baseline and the heap does not keep growing; the
// session keeps at most listCacheMax lists. RMK_SOAK=1 runs more laps.
func TestSoakBigLibrary(t *testing.T) {
	laps := 3
	if os.Getenv("RMK_SOAK") != "" {
		laps = 12
	}
	h := startOpts(t, hopts{big: 1000})
	h.waitTag("list", 1)
	step := 0
	lap := func() {
		h.onUI(func() {
			if len(h.app.stack) > 1 {
				h.app.goHome()
			}
		})
		h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "list" && s.ListRows > 0 }, "home")
		h.tapName("row-5")
		h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "All Libraries" && s.ListRows >= 4 }, "libraries")
		h.tapName("row-3")
		h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "Big Library" && s.ListRows > 0 }, "big library")
		h.waitBigLoaded(1000)
		s := h.listState()
		for l := 0; l < alphaLetters; l++ {
			name := letterName(l)
			if _, ok := s.first[name]; !ok {
				continue
			}
			h.tapName("alpha-" + name)
			step++
			time.Sleep(2 * time.Millisecond)
		}
		for i := 0; i < 6; i++ {
			h.swipe(600, 1500, 600, 700)
			time.Sleep(2 * time.Millisecond)
		}
		h.tapName("row-1")
		h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle != "Big Library" && s.ListRows > 0 }, "series")
		h.tapName("back")
		h.waitFor(func() bool { return h.snapshot().ListTitle == "Big Library" }, "back")
		time.Sleep(30 * time.Millisecond)
	}
	lap() // warm-up
	time.Sleep(300 * time.Millisecond)
	base := h.soakSample(step)
	var samples []soakSample
	for i := 0; i < laps; i++ {
		lap()
		samples = append(samples, h.soakSample(step))
	}
	time.Sleep(300 * time.Millisecond)
	end := h.soakSample(step)
	var rows, cache, cached int
	h.onUI(func() {
		rows = len(h.app.top().rows)
		cache = len(h.app.listCache)
		for _, e := range h.app.listCache {
			cached += len(e.rows)
		}
	})
	var b strings.Builder
	fmt.Fprintf(&b, "step  heap(MB) sys(MB) goroutines\n")
	for _, s := range append(append([]soakSample{base}, samples...), end) {
		fmt.Fprintf(&b, "%4d  %7.1f %6.1f %10d\n", s.step, float64(s.heap)/1048576, float64(s.sys)/1048576, s.goroutines)
	}
	t.Logf("big-library soak: %d laps, list of %d rows, %d cached lists (%d rows)\n%s", laps, rows, cache, cached, b.String())
	if p := os.Getenv("RMK_SOAK_OUT"); p != "" {
		_ = os.WriteFile(p+".big", []byte(b.String()), 0o644)
	}
	if end.goroutines > base.goroutines+3 {
		t.Errorf("goroutines %d -> %d", base.goroutines, end.goroutines)
	}
	if float64(end.heap) > float64(base.heap)*1.05+8<<20 {
		t.Errorf("heap in use %.1f -> %.1f MB", float64(base.heap)/1048576, float64(end.heap)/1048576)
	}
	if cache > listCacheMax {
		t.Errorf("%d lists cached, cap %d", cache, listCacheMax)
	}
	h.stop()
}
