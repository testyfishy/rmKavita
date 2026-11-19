//go:build !windows

package main

import (
	"os"
	"os/exec"
)

// noEcho turns off terminal echo with stty; the returned func restores it.
func noEcho() func() {
	off := exec.Command("stty", "-echo")
	off.Stdin = os.Stdin
	if off.Run() != nil {
		return func() {}
	}
	return func() {
		on := exec.Command("stty", "echo")
		on.Stdin = os.Stdin
		_ = on.Run()
	}
}
