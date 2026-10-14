package ui

import (
	"strings"
	"testing"
	"time"

	"rmkavita/internal/display"
	"rmkavita/internal/store"
)

// Input robustness: the first panel session of rc1 ended with an app that was
// alive and idle but answered no touch. The tracker must be unable to get into
// a state where the next touch is ignored, whatever the events were.

func (h *harness) sendID(k display.EventKind, id, x, y int) {
	h.disp.events <- display.Event{Kind: k, ID: id, X: x, Y: y, At: time.Now()}
}

type touchSnap struct {
	ts      touchStats
	pressed bool
	open    int
}

func (h *harness) touchSnap() touchSnap {
	ch := make(chan touchSnap, 1)
	h.app.post(func() {
		a := h.app
		ch <- touchSnap{ts: a.ts, pressed: a.pressed(), open: len(a.open)}
	})
	select {
	case s := <-ch:
		return s
	case <-time.After(3 * time.Second):
		h.t.Fatal("touch snapshot timed out")
		return touchSnap{}
	}
}

// readerHarness opens the first chapter with a long chapter and no periodic clear.
func readerHarness(t *testing.T, palm time.Duration, mut func(*store.Config)) *harness {
	t.Helper()
	h := startOpts(t, hopts{pages: 30,
		mut: func(c *store.Config) {
			c.HoldMs = 400
			if mut != nil {
				mut(c)
			}
		},
		mutSet: func(s *store.Settings) { s.Refresh = store.RefreshNever },
		app:    func(a *App) { a.palmAfter = palm }})
	h.openFirstChapter()
	h.waitReady(0)
	return h
}

func (h *harness) turnForward(page int) {
	h.t.Helper()
	h.waitFor(func() bool { s := h.snapshot(); return s.Screen == "reader" && contains(s.ReadyPages, page) }, "page ready")
	h.tap(300, 1000) // left column = forward (right to left)
	h.waitFor(func() bool { return h.snapshot().Page == page }, "turned")
}

// A release that never comes (AppLoad took it, a lost message) must not make
// the next press, with another finger id, a no-op.
func TestLostUpDoesNotBlockTheNextTap(t *testing.T) {
	h := readerHarness(t, 0, nil)
	h.sendID(display.Down, 7, 300, 1000) // no Up, ever
	time.Sleep(30 * time.Millisecond)
	h.sendID(display.Down, 8, 300, 1000)
	h.sendID(display.Up, 8, 300, 1000)
	h.waitFor(func() bool { return h.snapshot().Page == 1 }, "the next tap turned the page")
	h.sendID(display.Up, 7, 300, 1000) // the stray release arrives late: nothing happens
	time.Sleep(60 * time.Millisecond)
	if p := h.snapshot().Page; p != 1 {
		t.Errorf("a late release turned the page again: %d", p)
	}
	s := h.touchSnap()
	if s.ts.superseded != 1 || s.ts.ignoredUp != 1 || s.pressed {
		t.Errorf("stats %+v pressed=%v", s.ts, s.pressed)
	}
	if lg := h.readLog(); !strings.Contains(lg, "input: press id=7") || !strings.Contains(lg, "force-cancelled") {
		t.Errorf("the forced cancel was not logged:\n%s", tail(lg, 600))
	}
	h.stop()
}

func TestDownDownWithoutUpThenUp(t *testing.T) {
	h := readerHarness(t, 0, nil)
	h.sendID(display.Down, 3, 300, 1000)
	h.sendID(display.Down, 3, 300, 1000) // same id again: a fresh gesture
	h.sendID(display.Up, 3, 300, 1000)
	h.waitFor(func() bool { return h.snapshot().Page == 1 }, "one turn")
	time.Sleep(60 * time.Millisecond)
	if p := h.snapshot().Page; p != 1 {
		t.Errorf("page %d: the two presses made more than one gesture", p)
	}
	h.stop()
}

func TestUpWithoutDownIsIgnored(t *testing.T) {
	h := readerHarness(t, 0, nil)
	before := len(h.disp.all())
	h.sendID(display.Up, 9, 300, 1000)
	h.sendID(display.Move, 9, 310, 1010)
	h.sendID(display.Up, 0, 1400, 1000)
	time.Sleep(80 * time.Millisecond)
	if n := len(h.disp.all()); n != before {
		t.Errorf("%d updates for releases without a press", n-before)
	}
	s := h.touchSnap()
	if s.ts.ignoredUp != 2 || s.ts.ignoredMove != 1 || s.pressed {
		t.Errorf("stats %+v pressed=%v", s.ts, s.pressed)
	}
	h.turnForward(1) // and the app still answers
	h.stop()
}

// A press at the very top (AppLoad's own bar takes the matching release) and
// then silence: after the classification time the tracker is free again, and
// even before it a new press wins.
func TestDownAtTheTopThenSilence(t *testing.T) {
	h := readerHarness(t, 150*time.Millisecond, nil)
	h.sendID(display.Down, 4, 800, 50)
	h.waitFor(func() bool { return h.touchSnap().pressed }, "press registered")
	h.waitFor(func() bool { return !h.touchSnap().pressed }, "the silent press was let go")
	if lg := h.readLog(); !strings.Contains(lg, "classed as a resting palm/thumb") {
		t.Errorf("the classification was not logged:\n%s", tail(lg, 600))
	}
	s := h.touchSnap()
	if s.ts.palm != 1 || s.open != 1 {
		t.Errorf("palm=%d open=%d, want 1 and 1 (the contact stays open until its release)", s.ts.palm, s.open)
	}
	h.turnForward(1)
	h.sendID(display.Up, 4, 800, 50) // its release finally arrives
	time.Sleep(50 * time.Millisecond)
	if s := h.touchSnap(); s.open != 0 || s.pressed {
		t.Errorf("after the release: open=%d pressed=%v", s.open, s.pressed)
	}
	h.stop()
}

// A hold timer armed by a press must not outlive the press that was replaced.
func TestHoldTimerDoesNotSurviveANewDown(t *testing.T) {
	h := readerHarness(t, 0, nil)
	h.sendID(display.Down, 1, 60, 2100) // bottom-left corner: quit after 400 ms
	time.Sleep(100 * time.Millisecond)
	h.sendID(display.Down, 2, 800, 1000) // another finger: the corner press is replaced
	time.Sleep(600 * time.Millisecond)   // longer than the hold time
	select {
	case r := <-h.done:
		t.Fatalf("the app quit (%q) from a corner press that had been replaced", r)
	default:
	}
	h.sendID(display.Up, 2, 800, 1000)
	h.stop()
}

// A stuck press cannot survive the next Down, whatever it is doing.
func TestStuckPressCannotSurviveTheNextDown(t *testing.T) {
	h := readerHarness(t, 0, nil)
	// a press that is mid-"hold" in the clear corner and a second one on a page column
	h.sendID(display.Down, 1, 1560, 80)
	time.Sleep(30 * time.Millisecond)
	for i := 1; i <= 3; i++ { // three taps with fresh ids; the first press is never released
		h.sendID(display.Down, 10+i, 300, 1000)
		h.sendID(display.Up, 10+i, 300, 1000)
		h.waitFor(func() bool { return h.snapshot().Page == i }, "turn")
		h.waitFor(func() bool { s := h.snapshot(); return contains(s.ReadyPages, i+1) }, "next ready")
	}
	if h.touchSnap().pressed {
		t.Error("a press is still believed to be down")
	}
	h.stop()
}

// A thumb resting at the edge for a minute (here: a few seconds with a short
// classification time) while ten taps happen elsewhere: every tap works.
func TestRestingThumbAtTheEdgeDoesNotBlockTaps(t *testing.T) {
	h := readerHarness(t, 200*time.Millisecond, nil)
	h.sendID(display.Down, 99, 20, 1000) // the thumb; it never moves and is never released
	h.waitFor(func() bool { return h.touchSnap().ts.palm == 1 }, "the thumb was classed as a resting palm")
	for i := 1; i <= 10; i++ {
		h.turnForward(i)
	}
	s := h.touchSnap()
	if s.ts.palm != 1 || s.ts.superseded != 0 {
		t.Errorf("palm=%d superseded=%d: the resting contact was classed once and nothing was superseded: %+v", s.ts.palm, s.ts.superseded, s.ts)
	}
	if s.open != 1 {
		t.Errorf("open contacts %d, want only the thumb", s.open)
	}
	h.sendID(display.Up, 99, 20, 1000)
	time.Sleep(50 * time.Millisecond)
	if s := h.touchSnap(); s.open != 0 {
		t.Errorf("open contacts after the thumb lifted: %d", s.open)
	}
	h.stop()
}

// Two contacts at once: the newest press is the gesture, the older one's
// release is ignored.
func TestTwoContactsNewestWins(t *testing.T) {
	h := readerHarness(t, 0, nil)
	h.turnForward(1)
	h.waitFor(func() bool { return contains(h.snapshot().ReadyPages, 2) }, "page 2 ready")
	h.sendID(display.Down, 1, 300, 1000) // left column (forward) ...
	time.Sleep(20 * time.Millisecond)
	h.sendID(display.Down, 2, 1400, 1000) // ... then, still down, the right column (back)
	h.sendID(display.Up, 1, 300, 1000)
	h.sendID(display.Up, 2, 1400, 1000)
	h.waitFor(func() bool { return h.snapshot().Page == 0 }, "the newest contact (right column: back) decided")
	time.Sleep(60 * time.Millisecond)
	if p := h.snapshot().Page; p != 0 {
		t.Errorf("page %d: both contacts acted", p)
	}
	h.stop()
}

// A finger that is swiping is not interrupted by a second contact.
func TestSecondContactDuringASwipeIsIgnored(t *testing.T) {
	h := readerHarness(t, 0, nil)
	b := h.touchSnap().ts
	h.sendID(display.Down, 1, 800, 400)
	h.sendID(display.Move, 1, 800, 900)
	h.sendID(display.Down, 2, 1400, 1500) // a palm lands while the finger is moving
	h.sendID(display.Move, 1, 800, 1500)
	h.sendID(display.Up, 1, 800, 1500) // a downward swipe from y=400: a scroll back, not a reveal
	h.sendID(display.Up, 2, 1400, 1500)
	time.Sleep(80 * time.Millisecond)
	s := h.touchSnap()
	if s.ts.ignoredDown-b.ignoredDown != 1 || s.ts.completed-b.completed != 1 || s.ts.superseded != b.superseded {
		t.Errorf("stats before %+v after %+v", b, s.ts)
	}
	h.stop()
}

// The counters appear in the telemetry lines and in the exit totals.
func TestInputCountersAreLogged(t *testing.T) {
	h := readerHarness(t, 0, nil)
	h.turnForward(1)
	h.sendID(display.Down, 5, 10, 10)
	h.sendID(display.Down, 6, 10, 10) // supersedes
	h.sendID(display.Up, 6, 10, 10)
	time.Sleep(50 * time.Millisecond)
	h.stop()
	lg := h.readLog()
	if !strings.Contains(lg, " in=") || !strings.Contains(lg, " g=") || !strings.Contains(lg, " pr=0") || !strings.Contains(lg, " open=") {
		t.Errorf("telemetry lines lack the input counters:\n%s", tail(lg, 900))
	}
	if !strings.Contains(lg, "input totals: ") || !strings.Contains(lg, "superseded=1") {
		t.Errorf("exit totals missing or wrong:\n%s", tail(lg, 900))
	}
}

// A tap that lands right after a thumb (before it could be classified) still
// wins: the thumb is replaced and its release is ignored when it comes.
func TestTapRightAfterAThumbWins(t *testing.T) {
	h := readerHarness(t, 0, nil)
	h.sendID(display.Down, 99, 20, 1000)
	h.turnForward(1)
	h.turnForward(2)
	s := h.touchSnap()
	if s.ts.superseded != 1 {
		t.Errorf("stats %+v", s.ts)
	}
	h.sendID(display.Up, 99, 20, 1000)
	time.Sleep(40 * time.Millisecond)
	if p := h.snapshot().Page; p != 2 {
		t.Errorf("page %d: the thumb's late release acted", p)
	}
	h.stop()
}
