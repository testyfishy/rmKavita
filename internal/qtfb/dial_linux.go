//go:build linux

package qtfb

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

// DefaultSocket is where rm-appload's qtfb server listens.
const DefaultSocket = "/tmp/qtfb.sock"

// DefaultShmDir is where glibc's shm_open("/qtfb_N") lands.
const DefaultShmDir = "/dev/shm"

// Options tune Dial; the zero value is the production setup.
type Options struct {
	Socket string // default DefaultSocket
	ShmDir string // default DefaultShmDir
	Format string // fb_format: auto (default) | rgba | rgb565
}

// Dial connects to the qtfb server, performs the INITIALIZE handshake with the
// key AppLoad passed in QTFB_KEY (RGBA8888 first, RGB565 when the server
// refuses it, unless the format is forced), maps the framebuffer and returns
// the Panel.
func Dial(key int32, model Model, o Options) (*Panel, error) {
	if o.Socket == "" {
		o.Socket = DefaultSocket
	}
	if o.ShmDir == "" {
		o.ShmDir = DefaultShmDir
	}
	connect := func() (PacketConn, error) {
		c, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: o.Socket, Net: "unixpacket"})
		if err != nil {
			return nil, fmt.Errorf("connect %s: %w", o.Socket, err)
		}
		return c, nil
	}
	mapShm := func(shmKey int32, size uint64) ([]byte, func(), error) {
		path := filepath.Join(o.ShmDir, fmt.Sprintf("qtfb_%d", shmKey))
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return nil, nil, fmt.Errorf("open shm: %w", err)
		}
		mem, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
		f.Close() // the mapping stays valid
		if err != nil {
			return nil, nil, fmt.Errorf("mmap shm: %w", err)
		}
		return mem, func() { _ = syscall.Munmap(mem) }, nil
	}
	p, _, err := Negotiate(FormatOrder(o.Format), connect, key, model, mapShm)
	return p, err
}
