package proto

import (
	"errors"
	"testing"
)

func TestKeyEventRoundTrip(t *testing.T) {
	tests := []KeyEvent{
		{Code: 30, Pressed: true},    // KEY_A down
		{Code: 30, Pressed: false},   // KEY_A up
		{Code: 0, Pressed: false},    // KEY_RESERVED, the zero value
		{Code: 65535, Pressed: true}, // upper bound of the evdev code space
	}
	for _, want := range tests {
		got, err := DecodeKey(want.Encode())
		if err != nil {
			t.Fatalf("DecodeKey(%+v): %v", want, err)
		}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}

// Deltas are signed; a sign bug here shows up as the cursor only moving
// right and down, so it gets its own test.
func TestMouseRelRoundTripPreservesSign(t *testing.T) {
	tests := []MouseRel{
		{DX: 1, DY: 1},
		{DX: -1, DY: -1},
		{DX: -128, DY: 127},
		{DX: 32767, DY: -32768},
		{DX: 0, DY: 0},
	}
	for _, want := range tests {
		got, err := DecodeMouseRel(want.Encode())
		if err != nil {
			t.Fatalf("DecodeMouseRel(%+v): %v", want, err)
		}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}

func TestScrollRoundTripPreservesSign(t *testing.T) {
	for _, want := range []Scroll{{DX: 0, DY: 1}, {DX: 0, DY: -1}, {DX: -3, DY: 3}} {
		got, err := DecodeScroll(want.Encode())
		if err != nil {
			t.Fatalf("DecodeScroll(%+v): %v", want, err)
		}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}

func TestMouseBtnRoundTrip(t *testing.T) {
	for _, want := range []MouseBtn{{Button: BtnLeft, Pressed: true}, {Button: BtnRight, Pressed: false}, {Button: BtnMiddle, Pressed: true}} {
		got, err := DecodeMouseBtn(want.Encode())
		if err != nil {
			t.Fatalf("DecodeMouseBtn(%+v): %v", want, err)
		}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}

func TestFocusEnterRoundTrip(t *testing.T) {
	// Ratio is where along the shared edge the cursor crosses, so screens of
	// different heights hand off at the same relative point.
	for _, want := range []FocusEnter{
		{Edge: EdgeLeft, Ratio: 0},
		{Edge: EdgeRight, Ratio: 65535},
		{Edge: EdgeTop, Ratio: 32768},
		{Edge: EdgeBottom, Ratio: 100},
	} {
		got, err := DecodeFocusEnter(want.Encode())
		if err != nil {
			t.Fatalf("DecodeFocusEnter(%+v): %v", want, err)
		}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}

// A short or corrupt payload must be an error, never a panic — this decodes
// bytes straight off a network socket.
func TestDecodersRejectShortPayload(t *testing.T) {
	decoders := map[string]func([]byte) error{
		"key":        func(b []byte) error { _, err := DecodeKey(b); return err },
		"mouserel":   func(b []byte) error { _, err := DecodeMouseRel(b); return err },
		"mousebtn":   func(b []byte) error { _, err := DecodeMouseBtn(b); return err },
		"scroll":     func(b []byte) error { _, err := DecodeScroll(b); return err },
		"focusenter": func(b []byte) error { _, err := DecodeFocusEnter(b); return err },
	}
	for name, dec := range decoders {
		t.Run(name, func(t *testing.T) {
			for _, short := range [][]byte{nil, {}, {0x01}} {
				if err := dec(short); !errors.Is(err, ErrBadFrame) {
					t.Errorf("payload %v: err = %v, want ErrBadFrame", short, err)
				}
			}
		})
	}
}
