package ui

import "testing"

func TestStepAndSegments(t *testing.T) {
	if got := stepFor(2160); got != 2160-2160*8/100 {
		t.Errorf("step = %d", got)
	}
	// fits: one segment
	if s := segStarts(2160, 2160); len(s) != 1 || s[0] != 0 {
		t.Errorf("fit = %v", s)
	}
	// a page twice the screen: overlapping segments, the last clamped to the end
	s := segStarts(4320, 2160)
	if len(s) != 3 || s[0] != 0 || s[1] != 1988 || s[2] != 2160 {
		t.Errorf("double page = %v", s)
	}
	for i := 1; i < len(s); i++ {
		if s[i]-s[i-1] > stepFor(2160) {
			t.Errorf("segment %d moves %d px, more than a step", i, s[i]-s[i-1])
		}
	}
	// every pixel of the extent is covered by some segment
	for _, ext := range []int{2161, 2500, 4000, 7777, 20000} {
		st := segStarts(ext, 2160)
		covered := 0
		for _, x := range st {
			if x > covered {
				t.Errorf("ext %d: gap before %d (covered to %d)", ext, x, covered)
			}
			covered = x + 2160
		}
		if covered < ext || st[len(st)-1]+2160 != ext {
			t.Errorf("ext %d: ends at %d", ext, covered)
		}
	}
	if i := segIndexFor([]int{0, 100, 200}, 150); i != 1 {
		t.Errorf("index = %d", i)
	}
}
