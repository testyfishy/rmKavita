package ui

import (
	"strings"
	"testing"
	"time"
)

// What the long-list features send to the panel, counted in protocol messages:
// a jump is ONE update, a scroll is ONE update, and neither the background fill
// nor any of it costs a refresh-mode message or a full-refresh request.
func TestLongListMessagesOnTheProtocol(t *testing.T) {
	r := newRig(t, rigOpts{pages: 6, big: 300})
	r.wait(func(s Snapshot) bool { return s.Screen == "list" && s.ListRows > 0 }, "home")
	r.tapName("row-5")
	r.wait(func(s Snapshot) bool { return s.ListTitle == "All Libraries" && s.ListRows >= 4 }, "libraries")
	r.tapName("row-3")
	r.wait(func(s Snapshot) bool { return s.ListTitle == "Big Library" && s.ListRows > 0 }, "the big library")
	before := len(r.conn.all())
	r.wait(func(s Snapshot) bool { return s.ListRows >= 300 && !s.ListLoading }, "the whole list")
	time.Sleep(60 * time.Millisecond)
	for _, m := range strings.Split(kinds(r.conn.all()[before:]), ",") {
		if m != "update" && m != "" {
			t.Fatalf("the background fill sent %q", m)
		}
	}

	// a jump
	before = len(r.conn.all())
	r.tapName("alpha-M")
	r.wait(func(s Snapshot) bool { return s.Tag == "jump" }, "the jump")
	time.Sleep(40 * time.Millisecond)
	if got := kinds(r.conn.all()[before:]); got != "update" {
		t.Errorf("a jump sent %q, want one update", got)
	}
	// a page scroll (swipe up), and one back
	for _, dir := range []struct {
		name         string
		y0, y1       int
		wantScrolled bool
	}{{"up", 1500, 700, true}, {"down", 700, 1500, true}} {
		before = len(r.conn.all())
		r.swipe(600, dir.y0, 600, dir.y1)
		r.wait(func(s Snapshot) bool { return s.Tag == "scroll" }, "a scroll "+dir.name)
		time.Sleep(40 * time.Millisecond)
		if got := kinds(r.conn.all()[before:]); got != "update" {
			t.Errorf("a scroll %s sent %q, want one update", dir.name, got)
		}
	}
	// a drag over letters and a release
	before = len(r.conn.all())
	snap := r.get()
	var hitY, hitX int
	for _, h := range snap.Hits {
		if h.Name == "alpha-C" {
			hitX, hitY = (h.Rect.Min.X+h.Rect.Max.X)/2, (h.Rect.Min.Y+h.Rect.Max.Y)/2
		}
	}
	if hitX == 0 {
		t.Fatal("no alpha-C target")
	}
	r.swipe(hitX, hitY, hitX, hitY+10*64)
	time.Sleep(60 * time.Millisecond)
	for _, m := range strings.Split(kinds(r.conn.all()[before:]), ",") {
		if m != "update" {
			t.Errorf("a drag on the bar sent %q", m)
		}
	}
	r.stop()
}
