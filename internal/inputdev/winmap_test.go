package inputdev

import (
	"testing"

	"github.com/ViitoJooj/synk/internal/proto"
)

// These map Win32 mouse messages to the neutral wire representation. The code
// runs only on Windows, but the logic is pure, so it is tested here.
func TestMouseMessageToButton(t *testing.T) {
	tests := []struct {
		name        string
		message     uint32
		mouseData   uint32
		wantButton  uint8
		wantPressed bool
	}{
		{"left down", 0x0201, 0, proto.BtnLeft, true},
		{"left up", 0x0202, 0, proto.BtnLeft, false},
		{"right down", 0x0204, 0, proto.BtnRight, true},
		{"right up", 0x0205, 0, proto.BtnRight, false},
		{"middle down", 0x0207, 0, proto.BtnMiddle, true},
		{"middle up", 0x0208, 0, proto.BtnMiddle, false},
		// XBUTTON1/2 live in the high word of mouseData.
		{"x1 down", 0x020B, 0x0001 << 16, proto.BtnSide, true},
		{"x2 down", 0x020B, 0x0002 << 16, proto.BtnExtra, true},
		{"x1 up", 0x020C, 0x0001 << 16, proto.BtnSide, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			btn, pressed, ok := mouseMessageToButton(tt.message, tt.mouseData)
			if !ok {
				t.Fatalf("message %#x not recognised", tt.message)
			}
			if btn != tt.wantButton {
				t.Errorf("button = %d, want %d", btn, tt.wantButton)
			}
			if pressed != tt.wantPressed {
				t.Errorf("pressed = %v, want %v", pressed, tt.wantPressed)
			}
		})
	}
}

func TestMouseMessageToButtonIgnoresNonButtonMessages(t *testing.T) {
	for _, m := range []uint32{0x0200 /* move */, 0x020A /* wheel */, 0x0100 /* keydown */} {
		if _, _, ok := mouseMessageToButton(m, 0); ok {
			t.Errorf("message %#x reported as a button, want not recognised", m)
		}
	}
}

// One notch is WHEEL_DELTA (120). Dividing naively means a high-resolution
// wheel, which reports as little as 8 per step, scrolls nothing at all.
func TestWheelAccumulatorWholeNotches(t *testing.T) {
	var w WheelAccumulator

	if got := w.Add(120); got != 1 {
		t.Errorf("Add(120) = %d, want 1", got)
	}
	if got := w.Add(-120); got != -1 {
		t.Errorf("Add(-120) = %d, want -1", got)
	}
	if got := w.Add(240); got != 2 {
		t.Errorf("Add(240) = %d, want 2", got)
	}
}

func TestWheelAccumulatorCombinesHighResolutionSteps(t *testing.T) {
	var w WheelAccumulator

	// Fifteen steps of 8 make exactly one notch; none of the first fourteen
	// should scroll on their own.
	for i := range 14 {
		if got := w.Add(8); got != 0 {
			t.Fatalf("step %d: Add(8) = %d, want 0 before a full notch", i, got)
		}
	}
	if got := w.Add(8); got != 1 {
		t.Errorf("fifteenth Add(8) = %d, want 1", got)
	}
}

// Reversing direction must not leave a stale remainder pushing the next
// scroll the wrong way.
func TestWheelAccumulatorResetsOnDirectionChange(t *testing.T) {
	var w WheelAccumulator
	w.Add(60) // half a notch down, nothing emitted

	if got := w.Add(-120); got != -1 {
		t.Errorf("Add(-120) after a partial forward step = %d, want -1", got)
	}
	// The leftover +60 must be gone, not waiting to cancel the next step.
	if got := w.Add(-60); got != 0 {
		t.Errorf("Add(-60) = %d, want 0", got)
	}
	if got := w.Add(-60); got != -1 {
		t.Errorf("second Add(-60) = %d, want -1", got)
	}
}

// The keyboard hook hands us a scancode plus an extended flag, which is
// exactly what the wire format needs.
func TestKeyboardHookScancodeToEvdev(t *testing.T) {
	tests := []struct {
		name     string
		scan     uint32
		extended bool
		want     uint16
	}{
		{"A", 0x1e, false, 30},
		{"left shift", 0x2a, false, 42},
		{"up arrow", 0x48, true, 103},
		{"right ctrl", 0x1d, true, 97},
		{"keypad dot", 0x53, false, 83},
		{"delete", 0x53, true, 111},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := scanToEvdev(tt.scan, tt.extended)
			if !ok {
				t.Fatalf("scancode %#x (ext %v) not mapped", tt.scan, tt.extended)
			}
			if got != tt.want {
				t.Errorf("got evdev %d, want %d", got, tt.want)
			}
		})
	}
}

func TestKeyboardHookRejectsUnmappableScancode(t *testing.T) {
	if got, ok := scanToEvdev(0xff, false); ok {
		t.Errorf("scancode 0xff mapped to %d, want dropped", got)
	}
}

// Ctrl+Alt+F12 is the escape hatch. If the link wedges while the far side has
// focus, this is what gives the user their mouse and keyboard back.
func TestEscapeHotkeyNeedsAllThreeKeys(t *testing.T) {
	tests := []struct {
		name  string
		press []uint16
		want  bool
	}{
		{"all three", []uint16{keyLeftCtrl, keyLeftAlt, keyF12}, true},
		{"right-hand modifiers", []uint16{keyRightCtrl, keyRightAlt, keyF12}, true},
		{"missing alt", []uint16{keyLeftCtrl, keyF12}, false},
		{"missing ctrl", []uint16{keyLeftAlt, keyF12}, false},
		{"modifiers only", []uint16{keyLeftCtrl, keyLeftAlt}, false},
		{"nothing", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var e escapeDetector
			got := false
			for _, k := range tt.press {
				got = e.Key(k, true)
			}
			if got != tt.want {
				t.Errorf("fired = %v, want %v", got, tt.want)
			}
		})
	}
}

// Releasing a modifier must disarm it, or the hotkey stays hot forever.
func TestEscapeHotkeyDisarmsOnRelease(t *testing.T) {
	var e escapeDetector
	e.Key(keyLeftCtrl, true)
	e.Key(keyLeftAlt, true)
	e.Key(keyLeftAlt, false)

	if e.Key(keyF12, true) {
		t.Error("fired with alt released, want false")
	}
}
