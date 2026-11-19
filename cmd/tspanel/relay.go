package main

// tspanel relay: plain-http doors on the Kobo's loopback to servers on the
// tailnet, for programs that cannot use Tailscale's SOCKS proxy themselves
// (Nickel's Kobo sync, the browser).
//
//	tspanel relay -conf /mnt/onboard/.adds/tailscale/relay.conf [-proxy socks5://127.0.0.1:1055]
//
// relay.conf, one door per line ("#" starts a comment):
//
//	127.0.0.1:8083  https://myserver.tailnet-name.ts.net:8446
//
// Requests to http://127.0.0.1:8083/... go to the target over the tailnet
// (https when the target is). Absolute links to the target's host in
// answers (Location headers, JSON, HTML, XML: Calibre-Web's Kobo sync hands
// out download and cover links) are rewritten to the door, so the device keeps
// coming back through it. Everything else (book files, images) streams through
// untouched.

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	// root certificates for https targets on a device that ships none
	_ "golang.org/x/crypto/x509roots/fallback"
)

type door struct {
	listen string   // 127.0.0.1:8083
	target *url.URL // https://host:port
}

func parseRelayConf(data string) ([]door, error) {
	var out []door
	sc := bufio.NewScanner(strings.NewReader(data))
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("line %d: want \"listen target\"", n)
		}
		host, port, err := net.SplitHostPort(f[0])
		if err != nil || port == "" {
			return nil, fmt.Errorf("line %d: listen address %q: want host:port", n, f[0])
		}
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("line %d: listen address must be on the loopback (127.0.0.1)", n)
		}
		t, err := url.Parse(f[1])
		if err != nil || (t.Scheme != "http" && t.Scheme != "https") || t.Host == "" {
			return nil, fmt.Errorf("line %d: target %q: want http(s)://host:port", n, f[1])
		}
		t.Path, t.RawQuery, t.Fragment = "", "", ""
		out = append(out, door{listen: f[0], target: t})
	}
	return out, sc.Err()
}

// rewriter turns absolute links to the target host (any scheme, any port,
// with or without "//": Calibre-Web builds some from its own external port)
// into links to the door.
type rewriter struct {
	re   *regexp.Regexp
	door string // http://127.0.0.1:8083
}

func newRewriter(target *url.URL, listen string) *rewriter {
	h := regexp.QuoteMeta(target.Hostname())
	return &rewriter{
		re:   regexp.MustCompile(`(?i)https?:(\\?/){2}` + h + `(:\d+)?`),
		door: "http://" + listen,
	}
}

func (w *rewriter) apply(b []byte) []byte {
	return w.re.ReplaceAllFunc(b, func(m []byte) []byte {
		if bytes.Contains(m, []byte(`\/`)) { // inside JSON with escaped slashes
			return []byte(strings.ReplaceAll(w.door, "/", `\/`))
		}
		return []byte(w.door)
	})
}

func textual(ct string) bool {
	ct = strings.ToLower(ct)
	for _, s := range []string{"json", "html", "xml", "text/", "javascript"} {
		if strings.Contains(ct, s) {
			return true
		}
	}
	return false
}

func newRelay(d door, tr http.RoundTripper, logf func(string, ...any)) http.Handler {
	rw := newRewriter(d.target, d.listen)
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(d.target)
			pr.Out.Host = d.target.Host
			// answers are rewritten, so ask for them uncompressed
			pr.Out.Header.Del("Accept-Encoding")
			pr.Out.Header.Del("X-Forwarded-For")
		},
		Transport:     tr,
		FlushInterval: 200 * time.Millisecond,
		ModifyResponse: func(r *http.Response) error {
			if loc := r.Header.Get("Location"); loc != "" {
				r.Header.Set("Location", string(rw.apply([]byte(loc))))
			}
			if !textual(r.Header.Get("Content-Type")) || r.Header.Get("Content-Encoding") != "" {
				return nil
			}
			const max = 32 << 20
			b, err := io.ReadAll(io.LimitReader(r.Body, max+1))
			r.Body.Close()
			if err != nil {
				return err
			}
			if len(b) > max {
				return fmt.Errorf("answer too large to rewrite")
			}
			b = rw.apply(b)
			r.Body = io.NopCloser(bytes.NewReader(b))
			r.ContentLength = int64(len(b))
			r.Header.Set("Content-Length", strconv.Itoa(len(b)))
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logf("relay %s -> %s %s: %v", d.listen, d.target.Host, r.URL.Path, err)
			http.Error(w, "relay: "+err.Error(), http.StatusBadGateway)
		},
	}
	return logRequests(rp, d.listen, logf)
}

// tokenInPath is Calibre-Web's Kobo sync token (/kobo/<token>/...): a secret,
// so it is never written to the log.
var tokenInPath = regexp.MustCompile(`^/kobo/[^/]+`)

type statusWriter struct {
	http.ResponseWriter
	code int
	n    int64
}

func (w *statusWriter) WriteHeader(c int) { w.code = c; w.ResponseWriter.WriteHeader(c) }
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.n += int64(n)
	return n, err
}
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logRequests writes one line per request: enough to see what Kobo sync or
// the browser asked for and what came back.
func logRequests(h http.Handler, listen string, logf func(string, ...any)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		h.ServeHTTP(sw, r)
		logf("relay %s %s %s -> %d %dB %dms", listen, r.Method,
			tokenInPath.ReplaceAllString(r.URL.Path, "/kobo/<token>"), sw.code, sw.n, time.Since(t).Milliseconds())
	})
}

func relayTransport(proxy string) (*http.Transport, error) {
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	if proxy != "" {
		pu, err := url.Parse(proxy)
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(pu)
	}
	return tr, nil
}

func relayMain(args []string) int {
	fs := flag.NewFlagSet("relay", flag.ContinueOnError)
	conf := fs.String("conf", "/mnt/onboard/.adds/tailscale/relay.conf", "doors, one \"listen target\" per line")
	proxy := fs.String("proxy", "socks5://127.0.0.1:1055", "how to reach the tailnet (\"\" = directly)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	logf := log.New(os.Stderr, "", log.LstdFlags).Printf
	data, err := os.ReadFile(*conf)
	if err != nil {
		logf("relay: %v", err)
		return 1
	}
	doors, err := parseRelayConf(string(data))
	if err != nil {
		logf("relay: %s: %v", *conf, err)
		return 1
	}
	if len(doors) == 0 {
		logf("relay: %s has no doors", *conf)
		return 0
	}
	tr, err := relayTransport(*proxy)
	if err != nil {
		logf("relay: proxy: %v", err)
		return 1
	}
	errc := make(chan error, len(doors))
	for _, d := range doors {
		ln, err := net.Listen("tcp", d.listen)
		if err != nil {
			logf("relay: %v", err)
			return 1
		}
		logf("relay: http://%s -> %s", d.listen, d.target)
		srv := &http.Server{Handler: newRelay(d, tr, logf), ReadHeaderTimeout: 20 * time.Second}
		go func() { errc <- srv.Serve(ln) }()
	}
	logf("relay: %v", <-errc)
	return 1
}
