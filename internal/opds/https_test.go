package opds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Plain http sent to an https port (what Go, Kestrel and Tailscale Serve answer).
func TestLoginToHTTPSPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte("Client sent an HTTP request to an HTTPS server.\n"))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	_, e := NewUnsigned().Login(context.Background(), u, "reader", "pw")
	if e == nil || e.Kind != KindNeedsHTTPS {
		t.Fatalf("got %+v, want KindNeedsHTTPS", e)
	}
}

// An address typed without a scheme on an https port: Go's own TLS server
// answers plain http with the 400, and LoginAuto asks again over https.
func TestLoginAutoSwitchesToHTTPS(t *testing.T) {
	logins := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/Account/login" {
			logins++
			w.Write([]byte(`{"username":"reader","authKeys":[{"name":"opds","key":"k123456"}]}`))
		}
	}))
	defer srv.Close()
	c := NewUnsigned()
	c.tr.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
	u, _ := url.Parse(srv.URL)
	u.Scheme = "http"
	res, used, e := c.LoginAuto(context.Background(), u, "reader", "pw", true)
	if e != nil || used.Scheme != "https" || res.Key != "k123456" || logins != 1 {
		t.Fatalf("e=%+v used=%v key=%q logins=%d", e, used, res.Key, logins)
	}
	// typed with http:// explicitly: no switch, a clear error
	_, used, e = c.LoginAuto(context.Background(), u, "reader", "pw", false)
	if e == nil || e.Kind != KindNeedsHTTPS || used.Scheme != "http" {
		t.Fatalf("explicit http: e=%+v used=%v", e, used)
	}
}

// Any other 400 keeps the server's text, for the log.
func TestLogin400KeepsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"errors":{"Username":["required"]}}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	_, e := NewUnsigned().Login(context.Background(), u, "reader", "pw")
	if e == nil || e.Kind != KindServer || e.Status != 400 || e.Msg == "HTTP 400" {
		t.Fatalf("got %+v", e)
	}
}
