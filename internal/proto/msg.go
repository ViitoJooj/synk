package proto

import (
	"encoding/binary"
	"fmt"
)

// Edge identifies which border of a screen a handoff crosses.
type Edge uint8

const (
	EdgeLeft Edge = iota
	EdgeRight
	EdgeTop
	EdgeBottom
)

// Valid reports whether an edge came off the wire intact. An edge is used to
// index screen geometry, so a peer's byte is checked before it is trusted.
func (e Edge) Valid() bool { return e <= EdgeBottom }

func (e Edge) String() string {
	switch e {
	case EdgeLeft:
		return "left"
	case EdgeRight:
		return "right"
	case EdgeTop:
		return "top"
	case EdgeBottom:
		return "bottom"
	}
	return fmt.Sprintf("edge(%d)", uint8(e))
}

// Opposite converts the edge a pointer leaves by into the edge it arrives at:
// leaving through one screen's right side arrives on the other's left.
//
// It lives here rather than in either session because both machines need it and
// they must agree; two copies is exactly how the two sides come to disagree.
func (e Edge) Opposite() Edge {
	switch e {
	case EdgeLeft:
		return EdgeRight
	case EdgeRight:
		return EdgeLeft
	case EdgeTop:
		return EdgeBottom
	default:
		return EdgeTop
	}
}

// Mouse buttons, numbered independently of any platform's constants so the
// wire format does not inherit either side's quirks.
const (
	BtnLeft uint8 = iota
	BtnRight
	BtnMiddle
	BtnSide
	BtnExtra
)

type KeyEvent struct {
	Code    uint16 // Linux evdev keycode, the neutral form on the wire
	Pressed bool
}

type MouseRel struct{ DX, DY int16 }

type MouseBtn struct {
	Button  uint8
	Pressed bool
}

type Scroll struct{ DX, DY int16 }

type FocusEnter struct {
	Edge  Edge
	Ratio uint16 // position along the edge, 0..65535 of the screen's extent
}

// want checks a decoder's input length, turning a would-be index panic on
// attacker-controlled bytes into an ordinary error.
func want(b []byte, n int, what string) error {
	if len(b) < n {
		return fmt.Errorf("%w: %s needs %d bytes, got %d", ErrBadFrame, what, n, len(b))
	}
	return nil
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

func (k KeyEvent) Encode() []byte {
	b := make([]byte, 3)
	binary.BigEndian.PutUint16(b, k.Code)
	b[2] = boolByte(k.Pressed)
	return b
}

func DecodeKey(b []byte) (KeyEvent, error) {
	if err := want(b, 3, "key"); err != nil {
		return KeyEvent{}, err
	}
	return KeyEvent{Code: binary.BigEndian.Uint16(b), Pressed: b[2] != 0}, nil
}

func (m MouseRel) Encode() []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint16(b, uint16(m.DX))
	binary.BigEndian.PutUint16(b[2:], uint16(m.DY))
	return b
}

func DecodeMouseRel(b []byte) (MouseRel, error) {
	if err := want(b, 4, "mouserel"); err != nil {
		return MouseRel{}, err
	}
	return MouseRel{
		DX: int16(binary.BigEndian.Uint16(b)),
		DY: int16(binary.BigEndian.Uint16(b[2:])),
	}, nil
}

func (m MouseBtn) Encode() []byte {
	return []byte{m.Button, boolByte(m.Pressed)}
}

func DecodeMouseBtn(b []byte) (MouseBtn, error) {
	if err := want(b, 2, "mousebtn"); err != nil {
		return MouseBtn{}, err
	}
	return MouseBtn{Button: b[0], Pressed: b[1] != 0}, nil
}

func (s Scroll) Encode() []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint16(b, uint16(s.DX))
	binary.BigEndian.PutUint16(b[2:], uint16(s.DY))
	return b
}

func DecodeScroll(b []byte) (Scroll, error) {
	if err := want(b, 4, "scroll"); err != nil {
		return Scroll{}, err
	}
	return Scroll{
		DX: int16(binary.BigEndian.Uint16(b)),
		DY: int16(binary.BigEndian.Uint16(b[2:])),
	}, nil
}

func (f FocusEnter) Encode() []byte {
	b := make([]byte, 3)
	b[0] = byte(f.Edge)
	binary.BigEndian.PutUint16(b[1:], f.Ratio)
	return b
}

func DecodeFocusEnter(b []byte) (FocusEnter, error) {
	if err := want(b, 3, "focusenter"); err != nil {
		return FocusEnter{}, err
	}
	return FocusEnter{Edge: Edge(b[0]), Ratio: binary.BigEndian.Uint16(b[1:])}, nil
}
