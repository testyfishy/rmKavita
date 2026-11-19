package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// noEcho turns off console echo for stdin; the returned func restores it.
func noEcho() func() {
	h := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return func() {}
	}
	_ = windows.SetConsoleMode(h, mode&^windows.ENABLE_ECHO_INPUT)
	return func() { _ = windows.SetConsoleMode(h, mode) }
}
