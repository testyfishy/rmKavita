package opds

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Kind classifies a failure so the screen can say something useful.
type Kind int

const (
	KindDoor     Kind = iota + 1 // nothing listens on the local door
	KindNetwork                  // connected, but the server did not answer (timeout, reset, EOF)
	KindAuth                     // 401/403: the key was refused
	KindNotFound                 // 404 / 400 for an id that does not exist
	KindServer                   // 5xx and other statuses
	KindData                     // answered, but the body is not what was expected
	KindBadLogin                 // sign-in: the user name or password was refused
	KindLocked                   // sign-in: Kavita locked the account after repeated failures
)

// Error is a request failure with every secret removed.
type Error struct {
	Kind   Kind
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

// Short is the one line shown on the error screen.
func (e *Error) Short() string {
	switch e.Kind {
	case KindDoor:
		return "Cannot reach the local door"
	case KindNetwork:
		return "Kavita is not answering"
	case KindAuth:
		return "Kavita refused the key"
	case KindNotFound:
		return "Not found"
	case KindServer:
		if e.Status > 0 {
			return fmt.Sprintf("Kavita error %d", e.Status)
		}
		return "Kavita error"
	case KindData:
		return "Unreadable answer"
	case KindBadLogin:
		return "Wrong username or password"
	case KindLocked:
		return "Account locked for a while"
	}
	return "Error"
}

var (
	reOpdsKey = regexp.MustCompile(`(?i)(/api/opds/)[^/\s?"'<>&]+`)
	reAPIKey  = regexp.MustCompile(`(?i)(api[_-]?key\s*[=:]\s*)[^&\s"'<>]+`)
	// Kavita keys are 32 characters; any such run is treated as a secret in a log.
	reLongToken = regexp.MustCompile(`[A-Za-z0-9_-]{32,}`)
)

// Redactor removes secrets from any text that might be logged or drawn: the
// API key, any secret registered with it (a password being typed), key-shaped
// path/query pieces and any 32+ character token.
type Redactor struct {
	Key     string
	Secrets []string
}

// String returns s with the secrets replaced.
func (r Redactor) String(s string) string {
	if r.Key != "" {
		s = strings.ReplaceAll(s, r.Key, "<key>")
	}
	for _, x := range r.Secrets {
		if x != "" {
			s = strings.ReplaceAll(s, x, "<secret>")
		}
	}
	s = reOpdsKey.ReplaceAllString(s, "${1}<key>")
	s = reAPIKey.ReplaceAllString(s, "${1}<key>")
	s = reLongToken.ReplaceAllString(s, "<key>")
	return s
}

// RootURL is the catalogue root for a server origin and an API key.
func RootURL(base *url.URL, key string) string {
	return strings.TrimRight(base.String(), "/") + "/api/opds/" + key
}

type session struct {
	base *url.URL
	key  string
}

// Client fetches from exactly one configured origin. Its session (origin + key)
// can be replaced at any time, safely, while requests are in flight: sign-in and
// logout swap it, nothing else touches it.
type Client struct {
	cur     atomic.Pointer[session]
	mu      sync.Mutex
	secrets []string

	HTTP    *http.Client
	UA      string
	MaxFeed int64
	MaxImg  int64
	login   *http.Client
}

func newTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   3,
		MaxConnsPerHost:       3,
		IdleConnTimeout:       45 * time.Second,
		ResponseHeaderTimeout: 25 * time.Second,
		DisableCompression:    false,
		ForceAttemptHTTP2:     false,
	}
}

// NewUnsigned returns a client with no session (the Sign-in screen's state).
func NewUnsigned() *Client {
	tr := newTransport()
	return &Client{
		HTTP:    &http.Client{Transport: tr},
		UA:      "rmKavita/0.3 (reMarkable)",
		MaxFeed: 8 << 20,
		MaxImg:  40 << 20,
		login: &http.Client{
			Transport: tr,
			// A redirect would resend the password somewhere else: never follow one.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// NewClient builds a Client for base with key. Keep-alive is on and the
// connection count is small: every connection through the door spawns a
// forwarded connection.
func NewClient(base *url.URL, key string) *Client {
	c := NewUnsigned()
	c.Configure(base, key)
	return c
}

// Configure sets the session. A nil base signs the client out.
func (c *Client) Configure(base *url.URL, key string) {
	if base == nil || key == "" {
		c.cur.Store(nil)
		return
	}
	cp := *base
	c.cur.Store(&session{base: &cp, key: key})
}

// Clear signs the client out (and forgets any registered secret).
func (c *Client) Clear() {
	c.cur.Store(nil)
	c.mu.Lock()
	c.secrets = nil
	c.mu.Unlock()
}

// Signed reports whether a key is configured.
func (c *Client) Signed() bool { return c.cur.Load() != nil }

// Origin returns the configured origin ("" when signed out).
func (c *Client) Origin() string {
	if s := c.cur.Load(); s != nil {
		return s.base.String()
	}
	return ""
}

// AddSecret registers a value that must never appear in a log or on screen.
func (c *Client) AddSecret(s string) {
	if s == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, x := range c.secrets {
		if x == s {
			return
		}
	}
	c.secrets = append(c.secrets, s)
}

// DropSecret forgets a registered secret.
func (c *Client) DropSecret(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, x := range c.secrets {
		if x == s {
			c.secrets = append(c.secrets[:i], c.secrets[i+1:]...)
			return
		}
	}
}

// Redact removes the current key and every registered secret from s.
func (c *Client) Redact(s string) string {
	r := Redactor{}
	if cur := c.cur.Load(); cur != nil {
		r.Key = cur.key
	}
	c.mu.Lock()
	r.Secrets = append([]string(nil), c.secrets...)
	c.mu.Unlock()
	return r.String(s)
}

// Resolve turns an href from a feed into an absolute URL on the pinned origin.
// A server that emits its own public hostname still reaches us through the door.
func (c *Client) Resolve(ref, from string) (string, error) {
	cur := c.cur.Load()
	if cur == nil {
		return "", errors.New("not signed in")
	}
	base := cur.base
	if from != "" {
		if f, err := url.Parse(from); err == nil && f.IsAbs() {
			base = f
		}
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("bad link: %s", c.Redact(err.Error()))
	}
	abs := base.ResolveReference(r)
	abs.Scheme, abs.Host = cur.base.Scheme, cur.base.Host
	return abs.String(), nil
}

func (c *Client) classify(err error) *Error {
	// net/http wraps everything as `Get "<url>": <cause>`; the URL is noise on
	// the error screen (and carries the key, which Redact would have to hide).
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		err = ue.Err
	}
	msg := c.Redact(err.Error())
	low := strings.ToLower(err.Error())
	var ne net.Error
	switch {
	case strings.Contains(low, "refused"):
		return &Error{Kind: KindDoor, Msg: msg}
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return &Error{Kind: KindNetwork, Msg: "timeout: " + msg}
	}
	return &Error{Kind: KindNetwork, Msg: msg}
}

// Get fetches rawURL (absolute, or relative to the pinned origin) and returns
// the body. It retries once on a transport failure: every request is an
// idempotent GET and Wi-Fi often dropped while the tablet slept.
func (c *Client) Get(ctx context.Context, rawURL string, limit int64) ([]byte, string, error) {
	u, err := c.Resolve(rawURL, "")
	if err != nil {
		if !c.Signed() {
			return nil, "", &Error{Kind: KindAuth, Msg: "not signed in"}
		}
		return nil, "", &Error{Kind: KindData, Msg: err.Error()}
	}
	var lastErr *Error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, "", lastErr
			case <-time.After(400 * time.Millisecond):
			}
		}
		body, ctype, e := c.once(ctx, u, limit)
		if e == nil {
			return body, ctype, nil
		}
		lastErr = e
		if ctx.Err() != nil || e.Kind != KindNetwork {
			break
		}
	}
	return nil, "", lastErr
}

func (c *Client) once(ctx context.Context, u string, limit int64) ([]byte, string, *Error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", &Error{Kind: KindData, Msg: c.Redact(err.Error())}
	}
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Accept", "application/atom+xml, application/xml, image/*, */*;q=0.5")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", c.classify(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, "", &Error{Kind: KindAuth, Status: resp.StatusCode, Msg: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	case resp.StatusCode == 404:
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, "", &Error{Kind: KindNotFound, Status: 404, Msg: "HTTP 404"}
	case resp.StatusCode != 200:
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, "", &Error{Kind: KindServer, Status: resp.StatusCode,
			Msg: fmt.Sprintf("HTTP %d %s", resp.StatusCode, c.Redact(strings.TrimSpace(string(snippet))))}
	}
	if limit <= 0 {
		limit = c.MaxFeed
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", c.classify(err)
	}
	if int64(len(b)) > limit {
		return nil, "", &Error{Kind: KindData, Msg: "response too large"}
	}
	return b, resp.Header.Get("Content-Type"), nil
}

// Touch issues a GET and drops the body once the headers have arrived. Kavita
// saves reading progress before it answers a page request, so this is enough to
// move the server's progress marker without paying for the image again.
func (c *Client) Touch(ctx context.Context, rawURL string) error {
	u, err := c.Resolve(rawURL, "")
	if err != nil {
		return &Error{Kind: KindData, Msg: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return &Error{Kind: KindData, Msg: c.Redact(err.Error())}
	}
	req.Header.Set("User-Agent", c.UA)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return c.classify(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return &Error{Kind: KindAuth, Status: resp.StatusCode, Msg: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	if resp.StatusCode != 200 {
		return &Error{Kind: KindServer, Status: resp.StatusCode, Msg: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return nil
}

// GetFeed fetches and parses a catalogue page.
func (c *Client) GetFeed(ctx context.Context, rawURL string) (*Feed, error) {
	b, _, err := c.Get(ctx, rawURL, c.MaxFeed)
	if err != nil {
		return nil, err
	}
	f, perr := ParseFeed(b)
	if perr != nil {
		return nil, &Error{Kind: KindData, Msg: c.Redact(perr.Error())}
	}
	return f, nil
}

// GetImage fetches an image body.
func (c *Client) GetImage(ctx context.Context, rawURL string) ([]byte, error) {
	b, _, err := c.Get(ctx, rawURL, c.MaxImg)
	return b, err
}

// ---- sign-in -----------------------------------------------------------------

// LoginResult is what a successful sign-in yields. The password is not part of
// it and is never kept.
type LoginResult struct {
	Key      string
	Username string
}

type loginBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
	APIKey   string `json:"apiKey"`
}

// loginAnswer decodes only what is needed from Kavita's user DTO.
type loginAnswer struct {
	Username string `json:"username"`
	APIKey   string `json:"apiKey"`
	AuthKeys []struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"authKeys"`
	IdentityProvider int `json:"identityProvider"`
}

// Login signs in with a user name and password against server (an origin) and
// returns the key Kavita's OPDS endpoints accept. It is a single POST: a retry
// of a login is not idempotent (every failed attempt counts towards Kavita's
// lockout), so nothing is repeated, redirects are not followed and neither the
// request nor the answer is logged. The password is registered with the
// redactor for the duration of the call.
func (c *Client) Login(ctx context.Context, server *url.URL, username, password string) (LoginResult, *Error) {
	c.AddSecret(password)
	defer c.DropSecret(password)
	body, _ := json.Marshal(loginBody{Username: username, Password: password})
	ep := strings.TrimRight(server.String(), "/") + "/api/Account/login"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep, bytes.NewReader(body))
	if err != nil {
		return LoginResult{}, &Error{Kind: KindData, Msg: "bad server address"}
	}
	defer func() { // the request body shares this slice: wipe it once the call is over
		for i := range body {
			body[i] = 0
		}
	}()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UA)
	resp, err := c.login.Do(req)
	if err != nil {
		return LoginResult{}, c.classify(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 200:
	case resp.StatusCode == 401:
		txt, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		s := strings.TrimSpace(c.Redact(string(txt)))
		if strings.Contains(strings.ToLower(s), "locked") {
			return LoginResult{}, &Error{Kind: KindLocked, Status: 401, Msg: oneLine(s)}
		}
		return LoginResult{}, &Error{Kind: KindBadLogin, Status: 401, Msg: "HTTP 401"}
	case resp.StatusCode == 403:
		return LoginResult{}, &Error{Kind: KindBadLogin, Status: 403, Msg: "This account may not sign in (HTTP 403)"}
	case resp.StatusCode == 404 || resp.StatusCode == 405:
		return LoginResult{}, &Error{Kind: KindNotFound, Status: resp.StatusCode, Msg: "Not a Kavita address"}
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return LoginResult{}, &Error{Kind: KindServer, Status: resp.StatusCode, Msg: "The server redirected the sign-in; use its final address"}
	default:
		return LoginResult{}, &Error{Kind: KindServer, Status: resp.StatusCode, Msg: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return LoginResult{}, c.classify(err)
	}
	var ans loginAnswer
	if err := json.Unmarshal(raw, &ans); err != nil {
		return LoginResult{}, &Error{Kind: KindData, Msg: "Kavita's answer was not understood"}
	}
	key := ""
	for _, k := range ans.AuthKeys {
		if k.Name == "opds" && k.Key != "" {
			key = k.Key
			break
		}
	}
	if key == "" {
		key = ans.APIKey
	}
	if key == "" {
		return LoginResult{}, &Error{Kind: KindData, Msg: "Kavita did not return a key"}
	}
	if ans.Username == "" {
		ans.Username = username
	}
	return LoginResult{Key: key, Username: ans.Username}, nil
}

// VerifyKey confirms that key works against server by fetching the root feed
// (without touching this client's own session).
func (c *Client) VerifyKey(ctx context.Context, server *url.URL, key string) *Error {
	t := &Client{HTTP: c.HTTP, UA: c.UA, MaxFeed: c.MaxFeed, MaxImg: c.MaxImg}
	t.Configure(server, key)
	_, err := t.GetFeed(ctx, RootURL(server, key))
	if err == nil {
		return nil
	}
	e, ok := err.(*Error)
	if !ok {
		return &Error{Kind: KindData, Msg: t.Redact(err.Error())}
	}
	return e
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// Recheck asks Kavita once more, with a fresh request, whether this client's
// own key is still accepted (the root feed). It is how a foreground 401 is
// confirmed before the user is signed out.
func (c *Client) Recheck(ctx context.Context) *Error {
	cur := c.cur.Load()
	if cur == nil {
		return &Error{Kind: KindAuth, Msg: "not signed in"}
	}
	return c.VerifyKey(ctx, cur.base, cur.key)
}
