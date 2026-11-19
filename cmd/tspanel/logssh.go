package main

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sshOn reads whether Tailscale SSH is on (the RunSSH preference); ok is false
// when it could not be read.
func (s *server) sshOn(ctx context.Context) (on, ok bool) {
	out, err := s.ts(ctx, 5*time.Second, "debug", "prefs")
	if err != nil {
		return false, false
	}
	if i := strings.IndexByte(out, '{'); i > 0 {
		out = out[i:]
	}
	var p struct{ RunSSH bool }
	if json.Unmarshal([]byte(out), &p) != nil {
		return false, false
	}
	return p.RunSSH, true
}

// tailLog returns up to n of the last lines of the log at path, leaving out
// the JSON copies tailscaled writes for its log uploader (the same text again).
func tailLog(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return []string{"(no log: " + err.Error() + ")"}
	}
	defer f.Close()
	const max = 256 << 10
	if fi, err := f.Stat(); err == nil && fi.Size() > max {
		_, _ = f.Seek(fi.Size()-max, io.SeekStart)
	}
	b, _ := io.ReadAll(f)
	var lines []string
	for _, l := range bytes.Split(b, []byte("\n")) {
		t := strings.TrimRight(string(l), "\r")
		if t == "" || strings.HasPrefix(t, `,{"logtail"`) || strings.HasPrefix(t, `{"logtail"`) {
			continue
		}
		lines = append(lines, t)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// copyFile copies src to dst through a temporary file next to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp")
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

var logPage = template.Must(template.New("log").Parse(`<!doctype html>
<html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Tailscale log</title>
<style>
body{font-family:sans-serif;margin:0;padding:24px;background:#fff;color:#000;font-size:30px}
h1{font-size:44px;margin:0 0 20px}
form{display:inline}
button{font-size:32px;padding:18px 34px;margin:0 18px 18px 0;border:4px solid #000;border-radius:12px;background:#fff;color:#000;font-weight:bold}
.note{border-left:8px solid #000;padding:8px 16px;margin-bottom:24px}
pre{font-size:20px;white-space:pre-wrap;word-break:break-all;border-top:2px solid #ccc;padding-top:16px}
</style></head><body>
<h1>Tailscale log</h1>
{{if .Note}}<div class="note">{{.Note}}</div>{{end}}
<div>
<form method="get" action="/"><button>Back</button></form>
<form method="get" action="/log"><button>Refresh</button></form>
<form method="post" action="/savelog"><input type="hidden" name="t" value="{{.Token}}"><button>Save to USB storage</button></form>
</div>
<p style="font-size:24px;color:#555">Newest at the bottom. Kobo sync tokens are hidden.</p>
<pre>{{range .Lines}}{{.}}
{{end}}</pre>
</body></html>`))
