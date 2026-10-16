// Package qtfb speaks the qtfb framebuffer protocol that rm-appload exposes to
// AppLoad apps (github.com/asivery/rm-appload, GPL-3.0; the protocol's own
// header is backends/qtfb-clients/cpp/common.h). Message numbers and field
// offsets are interface facts, re-implemented here in Go; no AppLoad code is
// included. The layout was first read from KOReader's FFI declaration of the
// same protocol and confirmed on the device with ffi.sizeof/offsetof:
// ClientMessage is 24 bytes (union at 4), ServerMessage is 32 bytes (union at
// 8, because of its size_t).
package qtfb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// Client message types.
const (
	MsgInitialize         = 0
	MsgUpdate             = 1
	MsgCustomInitialize   = 2
	MsgTerminate          = 3
	MsgUserInput          = 4 // also the server -> client input message
	MsgSetRefreshMode     = 5
	MsgRequestFullRefresh = 6
)

// Refresh modes (qtfb.lua). UFAST (0) is deliberately never sent: the
// reference client documents it as causing excessive ghosting.
const (
	RefreshUFast   = 0
	RefreshFast    = 1
	RefreshAnimate = 2
	RefreshContent = 3
	RefreshUI      = 4
)

// Framebuffer formats (rm-appload common.h: FBFMT_RM2FB 0, RMPP_RGB888 1,
// RMPP_RGBA8888 2, RMPP_RGB565 3; the Move (RMPPM) row repeats the pattern
// 4, 5, 6 - the RGBA8888 value for the Move is inferred, the 565 fallback
// covers a wrong guess).
const (
	FBFmtRM2FB         = 0
	FBFmtRMPPRGBA8888  = 2
	FBFmtRMPPRGB565    = 3
	FBFmtRMPPMRGBA8888 = 5
	FBFmtRMPPMRGB565   = 6
)

// PixFmt is the pixel layout of the shared framebuffer.
type PixFmt int

const (
	FmtRGB565 PixFmt = iota
	FmtRGBA8888
)

// BytesPerPixel is the size of one framebuffer pixel.
func (f PixFmt) BytesPerPixel() int {
	if f == FmtRGBA8888 {
		return 4
	}
	return 2
}

func (f PixFmt) String() string {
	if f == FmtRGBA8888 {
		return "rgba8888"
	}
	return "rgb565"
}

// UpdatePartial is the update type KOReader always sends.
const UpdatePartial = 1

// Message sizes on the wire.
const (
	ClientMsgSize = 24
	ServerMsgSize = 32
)

// Input types of a server UserInput message.
const (
	InTouchPress   = 0x10
	InTouchRelease = 0x11
	InTouchUpdate  = 0x12
	InPenPress     = 0x20
	InPenRelease   = 0x21
	InPenUpdate    = 0x22
	InBtnPress     = 0x30
	InBtnRelease   = 0x31
)

var le = binary.LittleEndian

// EncodeInit builds MESSAGE_INITIALIZE.
func EncodeInit(key int32, fbType uint8) []byte {
	b := make([]byte, ClientMsgSize)
	b[0] = MsgInitialize
	le.PutUint32(b[4:], uint32(key))
	b[8] = fbType
	return b
}

// EncodeUpdate builds MESSAGE_UPDATE for a rectangle.
func EncodeUpdate(x, y, w, h int) []byte {
	b := make([]byte, ClientMsgSize)
	b[0] = MsgUpdate
	le.PutUint32(b[4:], UpdatePartial)
	le.PutUint32(b[8:], uint32(int32(x)))
	le.PutUint32(b[12:], uint32(int32(y)))
	le.PutUint32(b[16:], uint32(int32(w)))
	le.PutUint32(b[20:], uint32(int32(h)))
	return b
}

// EncodeSetMode builds MESSAGE_SET_REFRESH_MODE.
func EncodeSetMode(mode int) []byte {
	b := make([]byte, ClientMsgSize)
	b[0] = MsgSetRefreshMode
	le.PutUint32(b[4:], uint32(int32(mode)))
	return b
}

// EncodeFullRefresh builds MESSAGE_REQUEST_FULL_REFRESH.
func EncodeFullRefresh() []byte {
	b := make([]byte, ClientMsgSize)
	b[0] = MsgRequestFullRefresh
	return b
}

// EncodeTerminate builds MESSAGE_TERMINATE.
func EncodeTerminate() []byte {
	b := make([]byte, ClientMsgSize)
	b[0] = MsgTerminate
	return b
}

// InitResponse is the server's answer to MESSAGE_INITIALIZE.
type InitResponse struct {
	ShmKey  int32
	ShmSize uint64
}

// DecodeInitResponse reads the init answer (type byte, then union at 8).
func DecodeInitResponse(b []byte) (InitResponse, error) {
	if len(b) < ServerMsgSize {
		return InitResponse{}, fmt.Errorf("short init response: %d bytes", len(b))
	}
	return InitResponse{ShmKey: int32(le.Uint32(b[8:])), ShmSize: le.Uint64(b[16:])}, nil
}

// Input is a decoded server UserInput message.
type Input struct {
	Type    int
	Dev     int
	X, Y, D int
}

// ErrNotInput is returned for server messages that are not user input.
var ErrNotInput = errors.New("not an input message")

// DecodeInput reads a UserInput message.
func DecodeInput(b []byte) (Input, error) {
	if len(b) < ServerMsgSize {
		return Input{}, fmt.Errorf("short server message: %d bytes", len(b))
	}
	if b[0] != MsgUserInput {
		return Input{}, ErrNotInput
	}
	return Input{
		Type: int(int32(le.Uint32(b[8:]))),
		Dev:  int(int32(le.Uint32(b[12:]))),
		X:    int(int32(le.Uint32(b[16:]))),
		Y:    int(int32(le.Uint32(b[20:]))),
		D:    int(int32(le.Uint32(b[24:]))),
	}, nil
}

// EncodeInput builds a server UserInput message (used by the test server).
func EncodeInput(in Input) []byte {
	b := make([]byte, ServerMsgSize)
	b[0] = MsgUserInput
	le.PutUint32(b[8:], uint32(int32(in.Type)))
	le.PutUint32(b[12:], uint32(int32(in.Dev)))
	le.PutUint32(b[16:], uint32(int32(in.X)))
	le.PutUint32(b[20:], uint32(int32(in.Y)))
	le.PutUint32(b[24:], uint32(int32(in.D)))
	return b
}

// EncodeInitResponse builds the server's init answer (test server).
func EncodeInitResponse(r InitResponse) []byte {
	b := make([]byte, ServerMsgSize)
	b[0] = MsgInitialize
	le.PutUint32(b[8:], uint32(r.ShmKey))
	le.PutUint64(b[16:], r.ShmSize)
	return b
}

// Model describes a supported reMarkable.
type Model struct {
	Name     string
	W, H     int
	FBType   uint8 // framebuffer type of the RGB565 layout
	RGBAType uint8 // framebuffer type of the RGBA8888 layout
	DPI      int   // the device's own pixel density (KOReader's display_dpi)
}

// TypeFor is the INITIALIZE framebuffer type of a pixel layout.
func (m Model) TypeFor(f PixFmt) uint8 {
	if f == FmtRGBA8888 {
		return m.RGBAType
	}
	return m.FBType
}

// ModelFromMachine maps /sys/devices/soc0/machine to a framebuffer layout.
func ModelFromMachine(machine string) (Model, error) {
	m := strings.ToUpper(strings.TrimSpace(machine))
	switch {
	case strings.Contains(m, "FERRARI"):
		return Model{Name: "Paper Pro", W: 1620, H: 2160, FBType: FBFmtRMPPRGB565, RGBAType: FBFmtRMPPRGBA8888, DPI: 229}, nil
	case strings.Contains(m, "CHIAPPA"):
		return Model{Name: "Paper Pro Move", W: 954, H: 1696, FBType: FBFmtRMPPMRGB565, RGBAType: FBFmtRMPPMRGBA8888, DPI: 264}, nil
	}
	return Model{}, fmt.Errorf("unsupported device %q (only Paper Pro and Paper Pro Move)", strings.TrimSpace(machine))
}
