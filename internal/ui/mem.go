package ui

import (
	"bytes"
	"strconv"
)

// parseMemAvailable reads MemAvailable (MB) from the text of /proc/meminfo,
// -1 when it is missing or unreadable.
func parseMemAvailable(b []byte) int {
	for _, l := range bytes.Split(b, []byte("\n")) {
		if !bytes.HasPrefix(l, []byte("MemAvailable:")) {
			continue
		}
		f := bytes.Fields(l)
		if len(f) < 2 {
			return -1
		}
		kb, err := strconv.Atoi(string(f[1]))
		if err != nil {
			return -1
		}
		return kb / 1024
	}
	return -1
}
