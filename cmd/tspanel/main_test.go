package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every state of the page renders; with TSPANEL_PREVIEW=<dir> the pages are
// written there for a look in a browser.
func TestPageStates(t *testing.T) {
	self := &peer{HostName: "kobo", DNSName: "kobo.tail1234.ts.net.", OS: "linux", TailscaleIPs: []string{"100.101.102.103", "fd7a::1"}, Online: true}
	peers := []*peer{
		{HostName: "htpc", DNSName: "htpc.tail1234.ts.net.", OS: "windows", TailscaleIPs: []string{"100.64.0.2"}, Online: true},
		{HostName: "phone", DNSName: "phone.tail1234.ts.net.", OS: "android", TailscaleIPs: []string{"100.64.0.3"}},
	}
	views := map[string]view{
		"off":       {State: "off", Headline: "Tailscale is off", Detail: "Turn it on to reach your tailnet.", CanOn: true},
		"signin":    {State: "needslogin", Headline: "Not signed in", Detail: "Scan the code with your phone (or open the link) and approve this Kobo.", AuthURL: "https://login.tailscale.com/a/1a2b3c4d5e6f", Waiting: true, CanOff: true},
		"connected": {State: "running", Headline: "Connected", Detail: "This Kobo is on your tailnet.", Self: self, Tailnet: "example@", Peers: peers, CanOff: true, CanLogout: true, Note: "Tailscale is on.", SSHKnown: true, SSH: true},
	}
	dir := os.Getenv("TSPANEL_PREVIEW")
	for name, v := range views {
		v.Token, v.Now = "tok", "12:34:56"
		var b bytes.Buffer
		if err := page.Execute(&b, v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(b.String(), v.Headline) {
			t.Errorf("%s: headline missing", name)
		}
		if dir != "" {
			_ = os.WriteFile(filepath.Join(dir, name+".html"), b.Bytes(), 0o644)
		}
	}
	if self.Name() != "kobo" || self.IP4() != "100.101.102.103" {
		t.Errorf("name/ip: %s %s", self.Name(), self.IP4())
	}
}

func TestOnlyLoopbackHosts(t *testing.T) {
	for host, ok := range map[string]bool{"127.0.0.1:1057": true, "localhost:1057": true, "100.101.102.103:1057": false, "kobo:1057": false} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = host
		if localOnly(r) != ok {
			t.Errorf("%s: localOnly = %v", host, !ok)
		}
	}
}

func TestLogPage(t *testing.T) {
	dir := t.TempDir()
	lf := filepath.Join(dir, "tailscaled.log")
	_ = os.WriteFile(lf, []byte("2026/11/19 05:05:08 relay: http://127.0.0.1:8083 -> https://h:8446\n"+
		`,{"logtail":{"client_time":"x"},"text":"same again"}`+"\n"+
		"2026/11/19 05:17:50 relay 127.0.0.1:8083 GET /kobo/<token>/download/3261/epub -> 404 1231B 765ms\n"), 0o644)
	lines := tailLog(lf, 150)
	if len(lines) != 2 || !strings.Contains(lines[1], "-> 404") {
		t.Fatalf("lines: %q", lines)
	}
	if got := tailLog(lf, 1); len(got) != 1 || got[0] != lines[1] {
		t.Errorf("last 1: %q", got)
	}
	var b bytes.Buffer
	if err := logPage.Execute(&b, map[string]any{"Token": "tok", "Note": "Saved.", "Lines": lines}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "/kobo/&lt;token&gt;/download") || !strings.Contains(b.String(), "Save to USB storage") {
		t.Errorf("page: %s", b.String())
	}
	if d := os.Getenv("TSPANEL_PREVIEW"); d != "" {
		_ = os.WriteFile(filepath.Join(d, "log.html"), b.Bytes(), 0o644)
	}
	dst := filepath.Join(dir, "saved.log")
	if err := copyFile(lf, dst); err != nil {
		t.Fatal(err)
	}
	if a, _ := os.ReadFile(lf); string(a) != func() string { c, _ := os.ReadFile(dst); return string(c) }() {
		t.Error("copy differs")
	}
}
