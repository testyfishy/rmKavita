package main

import "testing"

func TestPercentileAndSpread(t *testing.T) {
	v := []float64{10, 50, 20, 40, 30, 60, 70, 80, 90, 100}
	if got := percentile(v, 50); got != 50 {
		t.Errorf("p50 = %v", got)
	}
	if got := percentile(v, 95); got != 100 {
		t.Errorf("p95 = %v", got)
	}
	if got := percentile(v, 100); got != 100 {
		t.Errorf("max = %v", got)
	}
	if got := percentile(v, 0); got != 10 {
		t.Errorf("min = %v", got)
	}
	if got := percentile(nil, 50); got != 0 {
		t.Errorf("empty = %v", got)
	}
	if v[0] != 10 || v[1] != 50 {
		t.Error("the input was reordered")
	}
	for _, tc := range []struct {
		count, n int
		want     []int
	}{
		{0, 8, nil}, {3, 8, []int{0, 1, 2}}, {8, 8, []int{0, 1, 2, 3, 4, 5, 6, 7}},
		{100, 8, []int{0, 14, 28, 42, 56, 70, 84, 99}}, {100, 1, []int{0}}, {12, 4, []int{0, 3, 7, 11}},
	} {
		got := spread(tc.count, tc.n)
		if len(got) != len(tc.want) {
			t.Errorf("spread(%d,%d) = %v, want %v", tc.count, tc.n, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("spread(%d,%d) = %v, want %v", tc.count, tc.n, got, tc.want)
				break
			}
		}
	}
}
