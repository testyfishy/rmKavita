package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRelayRewritesLinksAndStreamsFiles(t *testing.T) {
	book := bytes.Repeat([]byte{0x50, 0x4b, 3, 4, 0xff}, 1000) // looks like a zip (epub)
	var gotHost string
	back := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		switch r.URL.Path {
		case "/kobo/tok/v1/library/sync":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"DownloadUrls":[{"Url":"http://localhost:8083/kobo/tok/download/1/kepub"}],`+
				`"Cover":"https:\/\/localhost:8446\/kobo\/tok\/1\/image.jpg","Other":"https://example.org/x"}`)
		case "/go":
			http.Redirect(w, r, "https://LOCALHOST:8446/login", http.StatusFound)
		case "/book.epub":
			w.Header().Set("Content-Type", "application/epub+zip")
			// the book holds a link-like string: it must not be touched
			w.Write(append(append([]byte{}, book...), []byte("http://localhost:1/")...))
		}
	}))
	defer back.Close()
	bu, _ := url.Parse(back.URL)
	target, _ := url.Parse("http://localhost:" + bu.Port())
	front := httptest.NewServer(newRelay(door{listen: "127.0.0.1:8083", target: target}, http.DefaultTransport, t.Logf))
	defer front.Close()

	r, err := http.Get(front.URL + "/kobo/tok/v1/library/sync")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	s := string(b)
	for _, want := range []string{
		`"Url":"http://127.0.0.1:8083/kobo/tok/download/1/kepub"`,
		`"Cover":"http:\/\/127.0.0.1:8083\/kobo\/tok\/1\/image.jpg"`,
		`"Other":"https://example.org/x"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	if gotHost != target.Host {
		t.Errorf("backend saw Host %q, want %q", gotHost, target.Host)
	}

	nc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err = nc.Get(front.URL + "/go")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if loc := r.Header.Get("Location"); loc != "http://127.0.0.1:8083/login" {
		t.Errorf("Location = %q", loc)
	}

	r, err = http.Get(front.URL + "/book.epub")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if !bytes.HasPrefix(b, book) || !bytes.HasSuffix(b, []byte("http://localhost:1/")) {
		t.Errorf("book changed in transit (%d bytes)", len(b))
	}
}

func TestParseRelayConf(t *testing.T) {
	d, err := parseRelayConf("# calibre-web\n127.0.0.1:8083  https://htpc.example.ts.net:8446/  # sync\n\n")
	if err != nil || len(d) != 1 || d[0].listen != "127.0.0.1:8083" || d[0].target.String() != "https://htpc.example.ts.net:8446" {
		t.Fatalf("got %+v %v", d, err)
	}
	for _, bad := range []string{
		"0.0.0.0:8083 https://h:1", // not loopback: would open the door to the Wi-Fi
		"127.0.0.1 https://h:1",    // no port
		"127.0.0.1:8083 ftp://h",   // scheme
		"127.0.0.1:8083",           // no target
	} {
		if _, err := parseRelayConf(bad); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
}

func TestRelayLogHidesToken(t *testing.T) {
	back := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer back.Close()
	target, _ := url.Parse(back.URL)
	var lines []string
	logf := func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	front := httptest.NewServer(newRelay(door{listen: "127.0.0.1:8083", target: target}, http.DefaultTransport, logf))
	defer front.Close()
	r, err := http.Get(front.URL + "/kobo/0123456789abcdef0123456789abcdef/v1/initialization")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if len(lines) != 1 || strings.Contains(lines[0], "0123456789abcdef") ||
		!strings.Contains(lines[0], "GET /kobo/<token>/v1/initialization -> 401") {
		t.Fatalf("log: %q", lines)
	}
}
