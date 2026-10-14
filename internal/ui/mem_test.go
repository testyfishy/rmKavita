package ui

import "testing"

func TestParseMemAvailable(t *testing.T) {
	if got := parseMemAvailable([]byte("MemTotal: 2000000 kB\nMemFree: 100 kB\nMemAvailable:   81920 kB\nBuffers: 1 kB\n")); got != 80 {
		t.Errorf("MemAvailable = %d MB, want 80", got)
	}
	if got := parseMemAvailable([]byte("MemTotal: 1 kB\n")); got != -1 {
		t.Errorf("missing line = %d", got)
	}
	if got := parseMemAvailable([]byte("MemAvailable: lots kB\n")); got != -1 {
		t.Errorf("garbage = %d", got)
	}
}
