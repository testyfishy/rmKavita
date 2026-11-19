//go:build linux

package main

import (
	"os/exec"
	"syscall"
)

// detach starts c in its own session, so it outlives a closed page.
func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
