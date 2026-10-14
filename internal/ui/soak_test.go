package ui

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"rmkavita/internal/store"
)

// A soak of the app against the mock: page turns across chapters, settings
// screens, overlay toggles, chapter open/close cycles and sign-out/sign-in. The
// default run is a scaled-down lap that takes a few seconds; RMK_SOAK=1 runs the
// full counts (300 turns, 50 settings, 100 overlays, 20 chapters, 5 sign-outs).
// After a warm-up lap (the mock caches the pages it generates) goroutines and
// descriptors must come back to the baseline, the page cache must stay inside
// its budget, and the heap must not keep growing.

type soakSample struct {
	step              int
	heap, sys         uint64
	goroutines, fds   int
	cache, thumbs     int
	openContacts, rdr int
}

func (h *harness) soakSample(step int) soakSample {
	runtime.GC()
	debug.FreeOSMemory()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s := soakSample{step: step, heap: ms.HeapInuse, sys: ms.Sys, goroutines: runtime.NumGoroutine(), fds: openFDs()}
	h.onUI(func() {
		a := h.app
		s.thumbs, s.openContacts = len(a.thumbs), len(a.open)
		if a.rd != nil {
			s.cache = a.rd.cacheBytes()
			s.rdr = len(a.rd.slots)
		}
	})
	return s
}

func TestSoakNoLeaksNoBloat(t *testing.T) {
	turns, settings, overlays, chapters, signouts := 60, 8, 20, 4, 1
	if os.Getenv("RMK_SOAK") != "" {
		turns, settings, overlays, chapters, signouts = 300, 50, 100, 20, 5
	}
	h := startOpts(t, hopts{pages: 12, mut: func(c *store.Config) { c.Covers = true },
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshDefault }})
	h.waitTag("list", 1)
	h.waitFor(func() bool { return h.snapshot().ListRows > 0 }, "home")

	step := 0
	var samples []soakSample
	sample := func() {
		samples = append(samples, h.soakSample(step))
	}
	tick := func() {
		step++
		if step%25 == 0 {
			sample()
		}
	}
	openChapter := func() {
		// home, then All Libraries -> Manga A -> Series 01 -> the first chapter, by what the lists say
		h.onUI(func() {
			if h.app.screen() == scrList && len(h.app.stack) > 1 {
				h.app.goHome()
			}
		})
		h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "list" && s.ListRows > 0 && len(s.Hits) > 0 }, "home list")
		idx := -1
		h.onUI(func() {
			for i, r := range h.app.stack[0].rows {
				if r.title == "All Libraries" {
					idx = i
				}
			}
		})
		if idx < 0 {
			t.Fatal("no All Libraries row on the home list")
		}
		h.tapName(fmt.Sprintf("row-%d", idx))
		h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "All Libraries" && s.ListRows >= 2 }, "libraries")
		h.tapName("row-0")
		h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "Manga A" && s.ListRows >= 1 }, "series list")
		h.tapName("row-0")
		h.waitFor(func() bool { s := h.snapshot(); return s.ListTitle == "Series 01" && s.ListRows >= 1 }, "chapters")
		h.tapName("row-0")
		h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, s.Page) }, "reader")
	}
	closeToHome := func() {
		h.tap(810, 1000)
		h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
		h.tapName("home")
		h.waitFor(func() bool { return h.snapshot().Screen == "list" }, "home list")
	}
	turn := func() {
		s := h.snapshot()
		if s.Screen != "reader" {
			openChapter()
			s = h.snapshot()
		}
		want := s.Page + 1
		h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, s.Page) }, "page ready")
		before := h.disp.count("page") + h.disp.count("page-full") + h.disp.count("reader-first")
		h.tap(300, 1000)
		h.waitFor(func() bool {
			s := h.snapshot()
			return h.disp.count("page")+h.disp.count("page-full")+h.disp.count("reader-first") > before || s.Page == want || s.Screen != "reader"
		}, "turn drawn")
		time.Sleep(5 * time.Millisecond)
	}
	settingsCycle := func() {
		if h.snapshot().Screen == "reader" {
			h.tap(810, 1000)
			h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
		}
		h.tapName("settings")
		h.waitScreen("settings")
		h.tapName("back")
		h.waitFor(func() bool { s := h.snapshot(); return s.Screen != "settings" }, "settings closed")
	}
	overlayCycle := func() {
		h.tap(810, 1000)
		h.waitFor(func() bool { return h.snapshot().Overlay }, "bars")
		h.tap(300, 1000) // hides
		h.waitFor(func() bool { return !h.snapshot().Overlay }, "bars hidden")
	}
	signOutIn := func() {
		if h.snapshot().Screen == "reader" {
			closeToHome()
		}
		h.tapName("account")
		h.waitTag("popover", h.disp.count("popover")+1)
		h.tapName("logout")
		h.waitTag("popover", h.disp.count("popover")+1)
		h.tapName("logout")
		h.waitScreen("signin")
		h.typeText(goodPassword + "\n")
		h.waitScreen("list")
		h.waitFor(func() bool { return h.snapshot().ListRows > 0 }, "list after sign-in")
	}

	lap := func() {
		openChapter()
		for i := 0; i < turns; i++ {
			turn()
			tick()
		}
		for i := 0; i < overlays; i++ {
			if h.snapshot().Screen != "reader" {
				openChapter()
			}
			overlayCycle()
			tick()
		}
		for i := 0; i < settings; i++ {
			settingsCycle()
			tick()
		}
		for i := 0; i < chapters; i++ {
			if h.snapshot().Screen == "reader" {
				closeToHome()
			}
			openChapter()
			turn()
			tick()
		}
		for i := 0; i < signouts; i++ {
			signOutIn()
			tick()
		}
		if h.snapshot().Screen == "reader" {
			closeToHome()
		}
	}

	lap() // warm-up: the mock generates and caches its pages, the app's maps and pools fill
	time.Sleep(400 * time.Millisecond)
	base := h.soakSample(step)
	samples = nil
	lap()
	time.Sleep(400 * time.Millisecond)
	end := h.soakSample(step)

	var b strings.Builder
	fmt.Fprintf(&b, "step  heap(MB) sys(MB) goroutines fds cache(MB) thumbs slots contacts\n")
	row := func(s soakSample) {
		fmt.Fprintf(&b, "%4d  %7.1f %6.1f %10d %3d %8.1f %6d %5d %8d\n", s.step, float64(s.heap)/1048576, float64(s.sys)/1048576,
			s.goroutines, s.fds, float64(s.cache)/1048576, s.thumbs, s.rdr, s.openContacts)
	}
	row(base)
	for _, s := range samples {
		row(s)
	}
	row(end)
	t.Logf("soak: turns=%d settings=%d overlays=%d chapters=%d signouts=%d\n%s", turns, settings, overlays, chapters, signouts, b.String())
	if p := os.Getenv("RMK_SOAK_OUT"); p != "" {
		_ = os.WriteFile(p, []byte(b.String()), 0o644)
	}

	if end.goroutines > base.goroutines+3 {
		t.Errorf("goroutines %d -> %d: something is left running", base.goroutines, end.goroutines)
	}
	if base.fds >= 0 && end.fds > base.fds+3 {
		t.Errorf("open files %d -> %d", base.fds, end.fds)
	}
	for _, s := range samples {
		if s.cache > 96<<20 {
			t.Errorf("step %d: page cache %d bytes, budget 96 MB", s.step, s.cache)
		}
		if s.thumbs > 60 {
			t.Errorf("step %d: %d cover thumbnails held, cap 60", s.step, s.thumbs)
		}
		if s.openContacts > maxOpen {
			t.Errorf("step %d: %d open contacts", s.step, s.openContacts)
		}
	}
	// the heap does not keep growing: the end of the second lap is within 5 % (and 8 MB) of its start
	if float64(end.heap) > float64(base.heap)*1.05+8<<20 {
		t.Errorf("heap in use %.1f -> %.1f MB over the second lap", float64(base.heap)/1048576, float64(end.heap)/1048576)
	}
	h.stop()
}
