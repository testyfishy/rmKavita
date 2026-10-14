//go:build !linux

package main

import "context"

// watchParent is only meaningful on the device (Linux).
func watchParent(stop context.CancelFunc) {}
