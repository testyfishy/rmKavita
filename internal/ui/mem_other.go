//go:build !linux

package ui

// memAvailableMB: the memory guard only exists on the device.
func memAvailableMB() int { return -1 }

// openFDs: only counted on the device.
func openFDs() int { return -1 }
