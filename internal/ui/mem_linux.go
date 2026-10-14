//go:build linux

package ui

import "os"

// memAvailableMB reads MemAvailable from /proc/meminfo (MB), -1 when unknown.
func memAvailableMB() int {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return -1
	}
	return parseMemAvailable(b)
}

// openFDs counts this process's open file descriptors, -1 when unknown.
func openFDs() int {
	d, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(d)
}
