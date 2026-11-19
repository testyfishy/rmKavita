package store

import (
	"path/filepath"
	"testing"
)

func TestKoboConfigDefaults(t *testing.T) {
	c := ParseConfigFor("", PlatformKobo)
	if c.Platform != PlatformKobo || c.CacheMB != 48 || c.MemGuardMB != 90 || c.MemFloorMB != 45 || c.FBRotate != -1 {
		t.Fatalf("kobo defaults: %+v", c)
	}
	r := ParseConfig("")
	if r.Platform != PlatformRemarkable || r.CacheMB != 96 {
		t.Fatalf("reMarkable defaults changed: platform=%s cache=%d", r.Platform, r.CacheMB)
	}
	// a hand-set value still wins over the platform default
	if c := ParseConfigFor("cache_mb = 64\n", PlatformKobo); c.CacheMB != 64 {
		t.Fatalf("cache_mb = %d", c.CacheMB)
	}
}

func TestProxyRotateTouchMapKeys(t *testing.T) {
	c := ParseConfigFor("proxy = socks5://127.0.0.1:1055\nfb_rotate = 3\ntouch_map = sx\n", PlatformKobo)
	if c.Proxy != "socks5://127.0.0.1:1055" || c.FBRotate != 3 || c.TouchMap != "sx" || len(c.Unknown) != 0 {
		t.Fatalf("parsed %+v unknown=%v", c, c.Unknown)
	}
	bad := ParseConfigFor("proxy = ftp://x\nfb_rotate = 7\ntouch_map = q\n", PlatformKobo)
	if bad.Proxy != "" || bad.FBRotate != -1 || bad.TouchMap != "" || len(bad.Unknown) != 3 {
		t.Fatalf("bad values accepted: %+v %v", bad, bad.Unknown)
	}
}

func TestScrollAndButtonsSurviveSaveLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	s := DefaultSettingsFor(PlatformKobo)
	s.Buttons = ButtonsSwapped
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadSettings(p, Config{Platform: PlatformKobo})
	if !ok || !got.Comic.Continuous() || got.Comic.Fit != FitWidth || got.Buttons != ButtonsSwapped {
		t.Fatalf("loaded %+v", got)
	}
	// a value that is not a scroll mode falls back to the default
	s.Comic.Scroll = "sideways"
	s.Sanitize()
	if s.Comic.Scroll != ScrollPage {
		t.Fatalf("scroll %q", s.Comic.Scroll)
	}
}
