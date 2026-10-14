package opds

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const tkey = "0123456789abcdef" + "0123456789abcdef"

const stray = "ABCDEFGHIJKLMNOP" + "QRSTUVWXYZ012345"

func loginServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *url.URL) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return srv, u
}

func TestLoginSuccessPrefersOpdsKey(t *testing.T) {
	var gotBody string
	var calls int32
	_, base := loginServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method != "POST" || r.URL.Path != "/api/Account/login" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		json.NewEncoder(w).Encode(map[string]any{
			"username": "Reader", "apiKey": "legacy-key-not-this-one",
			"authKeys": []map[string]string{{"name": "image-only", "key": "imagekey"}, {"name": "opds", "key": tkey}},
		})
	})
	c := NewUnsigned()
	res, err := c.Login(context.Background(), base, "reader", "pass word")
	if err != nil {
		t.Fatal(err)
	}
	if res.Key != tkey || res.Username != "Reader" {
		t.Errorf("result %+v", res)
	}
	if !strings.Contains(gotBody, `"username":"reader"`) || !strings.Contains(gotBody, `"password":"pass word"`) {
		t.Errorf("body %q", gotBody)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("%d requests", calls)
	}
	// the password is not remembered by the client
	if got := c.Redact("typed pass word here"); !strings.Contains(got, "pass word") {
		t.Errorf("secret should be dropped after the call, got %q", got)
	}
}

func TestLoginFallsBackToApiKey(t *testing.T) {
	_, base := loginServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"username":"u","apiKey":"` + tkey + `"}`))
	})
	res, err := NewUnsigned().Login(context.Background(), base, "u", "p")
	if err != nil || res.Key != tkey {
		t.Errorf("%+v %v", res, err)
	}
}

func TestLoginFailureKinds(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		kind   Kind
		short  string
	}{
		{"wrong", 401, "Your credentials are not correct", KindBadLogin, "Wrong username or password"},
		{"locked", 401, "You've been locked out from too many authorization attempts. Please wait 10 minutes.", KindLocked, "Account locked for a while"},
		{"not kavita", 404, "", KindNotFound, "Not found"},
		{"method", 405, "", KindNotFound, "Not found"},
		{"server", 500, "boom", KindServer, "Kavita error 500"},
		{"redirect", 302, "", KindServer, "Kavita error 302"},
		{"garbage", 200, "<html>nope</html>", KindData, "Unreadable answer"},
		{"no key", 200, `{"username":"u"}`, KindData, "Unreadable answer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			_, base := loginServer(t, func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				if tc.status == 302 {
					http.Redirect(w, r, "http://elsewhere.invalid/api/Account/login", 302)
					return
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			_, err := NewUnsigned().Login(context.Background(), base, "u", "p")
			if err == nil || err.Kind != tc.kind {
				t.Fatalf("err = %+v, want kind %d", err, tc.kind)
			}
			if tc.kind != KindNotFound && err.Short() != tc.short {
				t.Errorf("short = %q, want %q", err.Short(), tc.short)
			}
			if n := atomic.LoadInt32(&calls); n != 1 {
				t.Errorf("%d requests: a login must never be retried or followed", n)
			}
		})
	}
}

func TestLoginTransportFailureIsNotRetried(t *testing.T) {
	var calls int32
	srv, base := loginServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close() // drop the connection without an answer
	})
	_ = srv
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := NewUnsigned().Login(ctx, base, "u", "p")
	if err == nil || err.Kind != KindNetwork {
		t.Fatalf("err = %+v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("transport failure was retried (%d requests)", n)
	}
}

func TestRedactorCoversPasswordKeyAndLongTokens(t *testing.T) {
	c := NewUnsigned()
	base, _ := url.Parse("http://h:1")
	c.Configure(base, tkey)
	c.AddSecret("hunter two")
	in := "login for hunter two with " + tkey + " at /api/opds/" + tkey + "/x?apiKey=" + tkey +
		" and a stray token " + stray + " plus short abc123"
	out := c.Redact(in)
	for _, bad := range []string{"hunter two", tkey, stray} {
		if strings.Contains(out, bad) {
			t.Errorf("%q survived: %s", bad, out)
		}
	}
	if !strings.Contains(out, "abc123") {
		t.Errorf("a short harmless word was removed: %s", out)
	}
	c.DropSecret("hunter two")
	if !strings.Contains(c.Redact("hunter two"), "hunter two") {
		t.Error("DropSecret did not drop")
	}
}

func TestClientSessionSwapAndSignedOutErrors(t *testing.T) {
	c := NewUnsigned()
	if c.Signed() {
		t.Fatal("signed")
	}
	_, err := c.GetFeed(context.Background(), "/api/opds/x")
	if e, ok := err.(*Error); !ok || e.Kind != KindAuth {
		t.Errorf("unsigned request: %v", err)
	}
	var hits int32
	srv, base := loginServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if !strings.Contains(r.URL.Path, tkey) {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`<feed xmlns="http://www.w3.org/2005/Atom"><title>ok</title></feed>`))
	})
	_ = srv
	c.Configure(base, tkey)
	if f, err := c.GetFeed(context.Background(), RootURL(base, tkey)); err != nil || f.Title != "ok" {
		t.Fatalf("%v %v", f, err)
	}
	// VerifyKey does not disturb the live session
	if e := c.VerifyKey(context.Background(), base, "wrongkey"); e == nil || e.Kind != KindAuth {
		t.Errorf("verify wrong key: %v", e)
	}
	if !c.Signed() || c.Origin() != base.String() {
		t.Error("session changed by VerifyKey")
	}
	if e := c.Recheck(context.Background()); e != nil {
		t.Errorf("recheck: %v", e)
	}
	c.Clear()
	if c.Signed() || c.Origin() != "" {
		t.Error("Clear did not sign out")
	}
}

func TestDocKindAndFormatHint(t *testing.T) {
	f, err := ParseFeed([]byte(`<feed xmlns="http://www.w3.org/2005/Atom" xmlns:p5="http://vaemendis.net/opds-pse/ns">
<entry><title>a</title><summary>Format: Pdf</summary><link rel="subsection" type="application/atom+xml;profile=opds-catalog" href="/s/1"/></entry>
<entry><title>b</title><summary>Format: Archive</summary><link rel="subsection" type="application/atom+xml;profile=opds-catalog" href="/s/2"/></entry>
<entry><title>c</title><summary>Format: Unknown</summary><link rel="subsection" type="application/atom+xml;profile=opds-catalog" href="/s/3"/></entry>
<entry><title>d</title><summary>nothing</summary><link rel="subsection" type="application/atom+xml;profile=opds-catalog" href="/s/4"/></entry>
<entry><title>e</title>
 <link rel="http://opds-spec.org/acquisition/open-access" type="application/pdf" href="/d/1"/>
 <link rel="http://vaemendis.net/opds-pse/stream" type="image/jpeg" href="/p?n={pageNumber}" p5:count="3"/></entry>
<entry><title>f</title>
 <link rel="http://opds-spec.org/acquisition/open-access" type="application/x-cbz" href="/d/2"/>
 <link rel="http://vaemendis.net/opds-pse/stream" type="image/jpeg" href="/p?n={pageNumber}" p5:count="3"/></entry>
</feed>`))
	if err != nil {
		t.Fatal(err)
	}
	e := f.Entries
	if e[0].FormatHint() != "pdf" || !e[0].ImageFormat() || !e[1].ImageFormat() || e[2].ImageFormat() || !e[3].ImageFormat() {
		t.Errorf("format hints: %q %q %q %q", e[0].FormatHint(), e[1].FormatHint(), e[2].FormatHint(), e[3].FormatHint())
	}
	if e[4].DocKind() != DocPDF || e[5].DocKind() != DocComic {
		t.Errorf("doc kinds: %v %v", e[4].DocKind(), e[5].DocKind())
	}
	if e[4].Stream() == nil || e[5].Stream() == nil {
		t.Error("streams not found")
	}
}
