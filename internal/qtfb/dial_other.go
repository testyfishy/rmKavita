//go:build !linux

package qtfb

import "errors"

// Options mirrors the Linux type so callers compile everywhere.
type Options struct {
	Socket string
	ShmDir string
	Format string
}

// Dial is only implemented on Linux (AF_UNIX SOCK_SEQPACKET + shm).
func Dial(key int32, model Model, o Options) (*Panel, error) {
	return nil, errors.New("qtfb is only available on Linux")
}
