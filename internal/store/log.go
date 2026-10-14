package store

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Logger writes short lines to a size-capped file on tmpfs (past the cap the
// file becomes path + ".1", replacing the previous one, so the newest one to
// two caps of history are always there) and, optionally, to stderr. Every line passes the Redact function first, so the API key can never
// reach either. On the tablet stderr is off unless it is a terminal: AppLoad
// owns the pipe behind it, and a pipe nobody drains would eventually block the
// UI thread on a log write (or kill the process with SIGPIPE).
type Logger struct {
	mu     sync.Mutex
	path   string
	max    int64
	size   int64 // bytes in the current file (-1: not looked at yet)
	Redact func(string) string
	Stderr bool
}

// NewLogger returns a logger; path may be "" for stderr only. Stderr output is
// on by default (tests, manual runs); the app turns it off when stderr is not a
// terminal.
func NewLogger(path string, red func(string) string) *Logger {
	return &Logger{path: path, max: 384 << 10, size: -1, Redact: red, Stderr: true}
}

// StderrIsTerminal reports whether stderr is an interactive terminal.
func StderrIsTerminal() bool {
	fi, err := os.Stderr.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Logf formats, redacts and writes one line.
func (l *Logger) Logf(format string, a ...any) {
	if l == nil {
		return
	}
	s := fmt.Sprintf(format, a...)
	if l.Redact != nil {
		s = l.Redact(s)
	}
	line := time.Now().Format("15:04:05.000") + " " + s + "\n"
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.Stderr {
		fmt.Fprint(os.Stderr, line)
	}
	if l.path == "" {
		return
	}
	if l.size < 0 {
		l.size = 0
		if st, err := os.Stat(l.path); err == nil {
			l.size = st.Size()
		}
	}
	if l.size > l.max {
		_ = os.Remove(l.path + ".1")
		if os.Rename(l.path, l.path+".1") != nil {
			_ = os.Truncate(l.path, 0)
		}
		l.size = 0
	}
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	n, _ := f.WriteString(line)
	f.Close()
	l.size += int64(n)
}
