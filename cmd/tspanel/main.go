// Command tspanel is a small Tailscale control page for a Kobo, opened in
// Nickel's built-in browser from one NickelMenu item. It shows whether
// Tailscale is on and connected, this Kobo's tailnet name and address and the
// other devices, and offers On / Off, Sign in (the link, and a QR code to scan
// with a phone), Sign out, Tailscale SSH on / off (remote access for
// debugging), and a page with the end of tailscaled's log, which can be saved
// to the user partition so it can be read over USB.
//
// It listens on the loopback only, answers only requests addressed to the
// loopback (a tailnet peer reaching the Kobo through userspace networking
// cannot use it), needs a per-run token for every action, and exits after a
// few idle minutes. It drives the Tailscale CLI and the ts.sh control script;
// it holds no secret.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"rsc.io/qr"
)

var (
	listen   = flag.String("listen", "127.0.0.1:1057", "address to serve on (loopback)")
	cli      = flag.String("tailscale", "/mnt/onboard/.adds/tailscale/tailscale", "the tailscale CLI")
	sock     = flag.String("socket", "/tmp/tailscaled.sock", "tailscaled's socket")
	ctl      = flag.String("ctl", "/usr/local/tailscale/ts.sh", "the control script (start, stop)")
	hostname = flag.String("hostname", "kobo", "the name this device asks for on the tailnet")
	idle     = flag.Duration("idle", 10*time.Minute, "exit after this long without a request")
	logFile  = flag.String("log", "/tmp/tailscaled.log", "tailscaled's log (shown on /log)")
	saveTo   = flag.String("save-log", "/mnt/onboard/.adds/tailscale/tailscaled.log", "where Save log copies it")
)

// tsStatus is the part of `tailscale status --json` the page shows.
type tsStatus struct {
	BackendState   string
	AuthURL        string
	MagicDNSSuffix string
	Self           *peer
	Peer           map[string]*peer
	CurrentTailnet *struct{ Name string }
}

type peer struct {
	HostName     string
	DNSName      string
	OS           string
	TailscaleIPs []string
	Online       bool
}

func (p *peer) Name() string {
	if n := strings.TrimSuffix(p.DNSName, "."); n != "" {
		if i := strings.IndexByte(n, '.'); i > 0 {
			return n[:i]
		}
		return n
	}
	return p.HostName
}

func (p *peer) IP4() string {
	for _, ip := range p.TailscaleIPs {
		if strings.Count(ip, ".") == 3 {
			return ip
		}
	}
	return ""
}

type server struct {
	token string
	mu    sync.Mutex
	upCmd *exec.Cmd // a running `tailscale up` waiting for the sign-in
	note  string    // one-line result of the last action
	last  time.Time
}

func (s *server) run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(c, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (s *server) ts(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	return s.run(ctx, timeout, *cli, append([]string{"--socket=" + *sock}, args...)...)
}

func daemonUp() bool {
	_, err := os.Stat(*sock)
	if err != nil {
		return false
	}
	c, err := net.DialTimeout("unix", *sock, time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func (s *server) status(ctx context.Context) (*tsStatus, bool) {
	if !daemonUp() {
		return nil, false
	}
	out, err := s.ts(ctx, 5*time.Second, "status", "--json")
	if err != nil && out == "" {
		return nil, true
	}
	var st tsStatus
	if i := strings.IndexByte(out, '{'); i > 0 {
		out = out[i:]
	}
	if json.Unmarshal([]byte(out), &st) != nil {
		return nil, true
	}
	return &st, true
}

type view struct {
	Token     string
	Running   bool // the daemon is up
	State     string
	Headline  string
	Detail    string
	Self      *peer
	Tailnet   string
	Peers     []*peer
	AuthURL   string
	Waiting   bool // a sign-in is pending: the page refreshes itself
	Note      string
	CanOn     bool
	CanOff    bool
	CanLogin  bool
	CanLogout bool
	SSHKnown  bool // the SSH setting could be read
	SSH       bool // Tailscale SSH is on
	Now       string
}

func (s *server) view(ctx context.Context) view {
	v := view{Token: s.token, Now: time.Now().Format("15:04:05")}
	s.mu.Lock()
	v.Note, s.note = s.note, ""
	waitingUp := s.upCmd != nil
	s.mu.Unlock()
	st, running := s.status(ctx)
	v.Running = running
	switch {
	case !running:
		v.State, v.Headline, v.Detail = "off", "Tailscale is off", "Turn it on to reach your tailnet."
		v.CanOn = true
	case st == nil:
		v.State, v.Headline, v.Detail = "starting", "Tailscale is starting", "Just a moment..."
		v.Waiting, v.CanOff = true, true
	default:
		v.State = strings.ToLower(st.BackendState)
		v.Self, v.AuthURL = st.Self, st.AuthURL
		if st.CurrentTailnet != nil {
			v.Tailnet = st.CurrentTailnet.Name
		}
		for _, p := range st.Peer {
			v.Peers = append(v.Peers, p)
		}
		sort.Slice(v.Peers, func(i, j int) bool {
			if v.Peers[i].Online != v.Peers[j].Online {
				return v.Peers[i].Online
			}
			return v.Peers[i].Name() < v.Peers[j].Name()
		})
		v.CanOff = true
		switch st.BackendState {
		case "Running":
			v.Headline = "Connected"
			v.Detail = "This Kobo is on your tailnet."
			v.CanLogout = true
			v.SSH, v.SSHKnown = s.sshOn(ctx)
		case "NeedsLogin", "NoState":
			v.Headline = "Not signed in"
			if v.AuthURL != "" {
				v.Detail = "Scan the code with your phone (or open the link) and approve this Kobo."
				v.Waiting = true
			} else if waitingUp {
				v.Detail = "Asking Tailscale for a sign-in link..."
				v.Waiting = true
			} else {
				v.Detail = "Sign in to add this Kobo to your tailnet."
				v.CanLogin = true
			}
		case "NeedsMachineAuth":
			v.Headline = "Waiting for approval"
			v.Detail = "An admin of your tailnet has to approve this device."
			v.Waiting = true
		case "Stopped":
			v.Headline = "Disconnected"
			v.Detail = "Signed in, but not connected."
			v.CanLogin, v.CanLogout = true, true
		case "Starting":
			v.Headline = "Connecting..."
			v.Waiting = true
		default:
			v.Headline = st.BackendState
		}
	}
	return v
}

var page = template.Must(template.New("p").Parse(`<!doctype html>
<html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
{{if .Waiting}}<meta http-equiv="refresh" content="4">{{end}}
<title>Tailscale</title>
<style>
body{font-family:sans-serif;margin:0;padding:24px;background:#fff;color:#000;font-size:30px}
h1{font-size:44px;margin:0 0 4px}
.sub{color:#444;margin:0 0 28px}
.state{border:4px solid #000;border-radius:14px;padding:20px 24px;margin-bottom:28px}
.state b{font-size:40px;display:block;margin-bottom:6px}
.on{background:#000;color:#fff}
.on .muted{color:#ddd}
.muted{color:#555}
form{display:inline}
button{font-size:32px;padding:18px 34px;margin:0 18px 18px 0;border:4px solid #000;border-radius:12px;background:#fff;color:#000;font-weight:bold}
button.primary{background:#000;color:#fff}
table{border-collapse:collapse;width:100%;margin-top:8px}
td{padding:10px 6px;border-bottom:2px solid #ccc;font-size:28px}
.dot{display:inline-block;width:18px;height:18px;border-radius:9px;border:3px solid #000;margin-right:10px}
.dot.up{background:#000}
.qr{text-align:center;margin:10px 0 24px}
.qr img{width:420px;height:420px;image-rendering:pixelated}
.link{font-family:monospace;font-size:26px;word-break:break-all}
.note{border-left:8px solid #000;padding:8px 16px;margin-bottom:24px}
.row{border-top:2px solid #ccc;padding-top:20px;margin-top:10px}
.row form{display:block;margin-top:14px}
</style></head><body>
<h1>Tailscale</h1>
<p class="sub">{{if .Self}}{{.Self.Name}}{{if .Tailnet}} &middot; {{.Tailnet}}{{end}}{{else}}This Kobo{{end}} <span class="muted">&middot; {{.Now}}</span></p>
{{if .Note}}<div class="note">{{.Note}}</div>{{end}}
<div class="state{{if eq .State "running"}} on{{end}}"><b>{{.Headline}}</b><span class="{{if eq .State "running"}}muted{{end}}">{{.Detail}}</span>
{{if and .Self (eq .State "running")}}<br><span class="muted">Address: {{.Self.IP4}}</span>{{end}}</div>
{{if .AuthURL}}<div class="qr"><img src="/qr.png?t={{.Now}}" alt="QR code"><p class="link">{{.AuthURL}}</p></div>{{end}}
<div>
{{if .CanOn}}<form method="post" action="/on"><input type="hidden" name="t" value="{{.Token}}"><button class="primary">Turn on</button></form>{{end}}
{{if .CanLogin}}<form method="post" action="/login"><input type="hidden" name="t" value="{{.Token}}"><button class="primary">Sign in</button></form>{{end}}
{{if .CanOff}}<form method="post" action="/off"><input type="hidden" name="t" value="{{.Token}}"><button>Turn off</button></form>{{end}}
{{if .CanLogout}}<form method="post" action="/logout"><input type="hidden" name="t" value="{{.Token}}"><button>Sign out</button></form>{{end}}
<form method="get" action="/"><button>Refresh</button></form>
<form method="get" action="/log"><button>Log</button></form>
</div>
{{if .SSHKnown}}<div class="row"><b>Remote access (SSH):</b> {{if .SSH}}on{{else}}off{{end}}
<form method="post" action="/ssh"><input type="hidden" name="t" value="{{.Token}}"><input type="hidden" name="on" value="{{if .SSH}}0{{else}}1{{end}}"><button>{{if .SSH}}Turn SSH off{{else}}Turn SSH on{{end}}</button></form>
<span class="muted">{{if .SSH}}Devices your tailnet policy allows can open a shell on this Kobo.{{else}}Lets you debug this Kobo from a computer on your tailnet.{{end}}</span></div>{{end}}
{{if .Peers}}<h2 style="font-size:34px;margin-top:30px">Your devices</h2>
<table>{{range .Peers}}<tr><td><span class="dot{{if .Online}} up{{end}}"></span>{{.Name}}</td><td class="muted">{{.IP4}}</td><td class="muted">{{.OS}}</td></tr>{{end}}</table>{{end}}
</body></html>`))

// localOnly refuses requests that were not addressed to the loopback: with
// userspace networking, a connection to the Kobo's tailnet address can be
// forwarded to loopback listeners, and then its Host names that address.
func localOnly(r *http.Request) bool {
	h := r.Host
	if i := strings.LastIndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	return h == "127.0.0.1" || h == "localhost"
}

func (s *server) touch() {
	s.mu.Lock()
	s.last = time.Now()
	s.mu.Unlock()
}

func (s *server) action(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context) string) {
	s.actionTo(w, r, "/", fn)
}

// actionTo runs a page action (a POST carrying this run's token), keeps its
// one-line result for the next page, and sends the browser back to back.
func (s *server) actionTo(w http.ResponseWriter, r *http.Request, back string, fn func(ctx context.Context) string) {
	if r.Method != http.MethodPost || r.FormValue("t") != s.token {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	note := fn(r.Context())
	s.mu.Lock()
	s.note = note
	s.mu.Unlock()
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *server) startUp() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upCmd != nil {
		return
	}
	// `up` refuses to run unless every non-default setting is repeated, so
	// the SSH setting goes along as it is
	ssh, _ := s.sshOn(context.Background())
	c := exec.Command(*cli, "--socket="+*sock, "up", "--hostname="+*hostname, "--accept-dns=true",
		fmt.Sprintf("--ssh=%v", ssh), "--timeout=10m")
	detach(c)
	if err := c.Start(); err != nil {
		s.note = "Could not start the sign-in: " + err.Error()
		return
	}
	s.upCmd = c
	go func() {
		_ = c.Wait()
		s.mu.Lock()
		s.upCmd = nil
		s.mu.Unlock()
	}()
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "relay" {
		os.Exit(relayMain(os.Args[2:]))
	}
	flag.Parse()
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	s := &server{token: hex.EncodeToString(b), last: time.Now()}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := page.Execute(w, s.view(r.Context())); err != nil {
			log.Printf("page: %v", err)
		}
	})
	mux.HandleFunc("/qr.png", func(w http.ResponseWriter, r *http.Request) {
		st, _ := s.status(r.Context())
		if st == nil || st.AuthURL == "" {
			http.NotFound(w, r)
			return
		}
		code, err := qr.Encode(st.AuthURL, qr.M)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		code.Scale = 8
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(code.PNG())
	})
	mux.HandleFunc("/on", func(w http.ResponseWriter, r *http.Request) {
		s.action(w, r, func(ctx context.Context) string {
			if out, err := s.run(ctx, 40*time.Second, *ctl, "start"); err != nil {
				return "Could not turn Tailscale on: " + lastLine(out, err)
			}
			return "Tailscale is on."
		})
	})
	mux.HandleFunc("/off", func(w http.ResponseWriter, r *http.Request) {
		s.action(w, r, func(ctx context.Context) string {
			s.mu.Lock()
			if s.upCmd != nil && s.upCmd.Process != nil {
				_ = s.upCmd.Process.Kill()
			}
			s.mu.Unlock()
			if out, err := s.run(ctx, 20*time.Second, *ctl, "stop"); err != nil {
				return "Could not turn Tailscale off: " + lastLine(out, err)
			}
			return "Tailscale is off."
		})
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		s.action(w, r, func(ctx context.Context) string {
			if !daemonUp() {
				if out, err := s.run(ctx, 40*time.Second, *ctl, "start"); err != nil {
					return "Could not turn Tailscale on: " + lastLine(out, err)
				}
			}
			s.startUp()
			time.Sleep(2 * time.Second) // the link usually exists by now
			return ""
		})
	})
	mux.HandleFunc("/ssh", func(w http.ResponseWriter, r *http.Request) {
		s.action(w, r, func(ctx context.Context) string {
			on := r.FormValue("on") == "1"
			if out, err := s.ts(ctx, 15*time.Second, "set", fmt.Sprintf("--ssh=%v", on)); err != nil {
				return "Could not change SSH: " + lastLine(out, err)
			}
			if on {
				return "SSH is on."
			}
			return "SSH is off."
		})
	})
	mux.HandleFunc("/log", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		s.mu.Lock()
		note := s.note
		s.note = ""
		s.mu.Unlock()
		if err := logPage.Execute(w, map[string]any{"Token": s.token, "Note": note, "Lines": tailLog(*logFile, 150)}); err != nil {
			log.Printf("log page: %v", err)
		}
	})
	mux.HandleFunc("/savelog", func(w http.ResponseWriter, r *http.Request) {
		s.actionTo(w, r, "/log", func(ctx context.Context) string {
			if err := copyFile(*logFile, *saveTo); err != nil {
				return "Could not save the log: " + err.Error()
			}
			return "Saved as .adds/tailscale/tailscaled.log (readable over USB)."
		})
	})
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		s.action(w, r, func(ctx context.Context) string {
			if out, err := s.ts(ctx, 15*time.Second, "logout"); err != nil {
				return "Could not sign out: " + lastLine(out, err)
			}
			return "Signed out. This Kobo is no longer on your tailnet."
		})
	})

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localOnly(r) {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		s.touch()
		mux.ServeHTTP(w, r)
	})
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tspanel:", err)
		os.Exit(1)
	}
	go func() {
		for range time.Tick(15 * time.Second) {
			s.mu.Lock()
			quiet := time.Since(s.last) > *idle && s.upCmd == nil
			s.mu.Unlock()
			if quiet {
				os.Exit(0)
			}
		}
	}()
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.Serve(ln))
}

func lastLine(out string, err error) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return err.Error()
	}
	l := strings.Split(out, "\n")
	return l[len(l)-1]
}
