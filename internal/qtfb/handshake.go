package qtfb

import (
	"errors"
	"fmt"
	"io"
	"time"
)

// ShmMapper opens and maps the framebuffer the server announced.
type ShmMapper func(shmKey int32, size uint64) (mem []byte, unmap func(), err error)

// ErrRejected means the server refused the requested framebuffer format
// (closed the connection without a reply, or offered less memory than the
// format needs): the caller may try another format.
var ErrRejected = errors.New("framebuffer format rejected by the server")

type deadliner interface{ SetReadDeadline(time.Time) error }

// Handshake performs MESSAGE_INITIALIZE on conn for format f, maps the shared
// memory and returns the Panel. Any failure closes conn.
func Handshake(conn PacketConn, key int32, model Model, f PixFmt, mapShm ShmMapper) (*Panel, error) {
	fail := func(e error) (*Panel, error) { conn.Close(); return nil, e }
	if _, err := conn.Write(EncodeInit(key, model.TypeFor(f))); err != nil {
		return fail(fmt.Errorf("send init: %w", err))
	}
	buf := make([]byte, 64)
	if d, ok := conn.(deadliner); ok {
		d.SetReadDeadline(time.Now().Add(5 * time.Second))
	}
	n, err := conn.Read(buf)
	if d, ok := conn.(deadliner); ok {
		d.SetReadDeadline(time.Time{})
	}
	if err != nil || n == 0 {
		if err == nil || errors.Is(err, io.EOF) {
			return fail(fmt.Errorf("%w (%s): closed without a reply", ErrRejected, f))
		}
		return fail(fmt.Errorf("read init response: %w", err))
	}
	resp, err := DecodeInitResponse(buf[:n])
	if err != nil {
		return fail(err)
	}
	want := uint64(model.W * model.H * f.BytesPerPixel())
	if resp.ShmSize < want {
		return fail(fmt.Errorf("%w (%s): server framebuffer is %d bytes, need %d", ErrRejected, f, resp.ShmSize, want))
	}
	mem, unmap, err := mapShm(resp.ShmKey, resp.ShmSize)
	if err != nil {
		return fail(err)
	}
	p, err := NewPanel(model, f, conn, mem, unmap)
	if err != nil {
		if unmap != nil {
			unmap()
		}
		return fail(err)
	}
	return p, nil
}

// FormatOrder is the formats to try for a configured fb_format value:
// "auto" tries RGBA8888 first and falls back to RGB565 (as KOReader does),
// "rgba" and "rgb565" force one.
func FormatOrder(cfg string) []PixFmt {
	switch cfg {
	case "rgb565", "565":
		return []PixFmt{FmtRGB565}
	case "rgba", "rgba8888":
		return []PixFmt{FmtRGBA8888}
	}
	return []PixFmt{FmtRGBA8888, FmtRGB565}
}

// Negotiate tries the formats in order. connect opens a fresh connection for
// every attempt (the server closes the previous one when it rejects a
// format). Only a rejection moves on to the next format; any other error ends
// the negotiation. It returns the panel and the rejections it saw.
func Negotiate(order []PixFmt, connect func() (PacketConn, error), key int32, model Model, mapShm ShmMapper) (*Panel, []error, error) {
	var rejected []error
	var last error
	for _, f := range order {
		conn, err := connect()
		if err != nil {
			return nil, rejected, err
		}
		p, err := Handshake(conn, key, model, f, mapShm)
		if err == nil {
			return p, rejected, nil
		}
		last = err
		if !errors.Is(err, ErrRejected) {
			return nil, rejected, err
		}
		rejected = append(rejected, err)
	}
	return nil, rejected, last
}
