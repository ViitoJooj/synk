package proto

import "testing"

// evdev codes 1..83 were derived from PS/2 Set 1 and still match it one for
// one. Spot-check the corners rather than restating the whole range.
func TestMainBlockMapsToIdenticalScancode(t *testing.T) {
	tests := []struct {
		name  string
		evdev uint16
		want  uint16
	}{
		{"KEY_ESC", 1, 0x01},
		{"KEY_1", 2, 0x02},
		{"KEY_Q", 16, 0x10},
		{"KEY_A", 30, 0x1e},
		{"KEY_LEFTSHIFT", 42, 0x2a},
		{"KEY_Z", 44, 0x2c},
		{"KEY_SPACE", 57, 0x39},
		{"KEY_F1", 59, 0x3b},
		{"KEY_KPDOT", 83, 0x53},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ToScancode(tt.evdev)
			if !ok {
				t.Fatalf("ToScancode(%d) not mapped", tt.evdev)
			}
			if got.Code != tt.want {
				t.Errorf("Code = %#x, want %#x", got.Code, tt.want)
			}
			if got.Extended {
				t.Error("Extended = true, want false for a main-block key")
			}
		})
	}
}

// These need the E0 prefix. Getting it wrong is why arrow keys type digits.
func TestExtendedKeysCarryTheE0Prefix(t *testing.T) {
	tests := []struct {
		name  string
		evdev uint16
		want  uint16
	}{
		{"KEY_KPENTER", 96, 0x1c},
		{"KEY_RIGHTCTRL", 97, 0x1d},
		{"KEY_KPSLASH", 98, 0x35},
		{"KEY_RIGHTALT", 100, 0x38},
		{"KEY_HOME", 102, 0x47},
		{"KEY_UP", 103, 0x48},
		{"KEY_PAGEUP", 104, 0x49},
		{"KEY_LEFT", 105, 0x4b},
		{"KEY_RIGHT", 106, 0x4d},
		{"KEY_END", 107, 0x4f},
		{"KEY_DOWN", 108, 0x50},
		{"KEY_PAGEDOWN", 109, 0x51},
		{"KEY_INSERT", 110, 0x52},
		{"KEY_DELETE", 111, 0x53},
		{"KEY_LEFTMETA", 125, 0x5b},
		{"KEY_RIGHTMETA", 126, 0x5c},
		{"KEY_COMPOSE", 127, 0x5d},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ToScancode(tt.evdev)
			if !ok {
				t.Fatalf("ToScancode(%d) not mapped", tt.evdev)
			}
			if got.Code != tt.want {
				t.Errorf("Code = %#x, want %#x", got.Code, tt.want)
			}
			if !got.Extended {
				t.Error("Extended = false, want true — E0 prefix required")
			}
		})
	}
}

// KEY_DELETE (111) and KEY_KPDOT (83) both carry scancode 0x53 and are told
// apart only by the prefix. If this collapses, Delete starts typing ".".
func TestDeleteAndKeypadDotShareCodeButNotPrefix(t *testing.T) {
	del, _ := ToScancode(111)
	kpDot, _ := ToScancode(83)

	if del.Code != kpDot.Code {
		t.Fatalf("expected both to be %#x, got %#x and %#x", 0x53, del.Code, kpDot.Code)
	}
	if !del.Extended {
		t.Error("KEY_DELETE must be extended")
	}
	if kpDot.Extended {
		t.Error("KEY_KPDOT must not be extended")
	}
}

func TestUnmappedCodesAreReported(t *testing.T) {
	// 0 is KEY_RESERVED; the high codes are evdev-only (media, buttons)
	// with no PS/2 equivalent.
	for _, code := range []uint16{0, 200, 400, 65535} {
		if got, ok := ToScancode(code); ok {
			t.Errorf("ToScancode(%d) = %+v, true; want not mapped", code, got)
		}
	}
}

// Two different evdev keys mapping to the same (code, extended) pair would
// make one of them unreachable on the far side.
func TestMappingIsInjective(t *testing.T) {
	seen := map[Scancode]uint16{}
	for evdev := uint16(1); evdev < 256; evdev++ {
		sc, ok := ToScancode(evdev)
		if !ok {
			continue
		}
		if prev, dup := seen[sc]; dup {
			t.Errorf("evdev %d and %d both map to %+v", prev, evdev, sc)
		}
		seen[sc] = evdev
	}
}

// The Windows side receives scancodes from its hooks and must turn them back
// into the evdev codes the wire speaks. Any code that survives one direction
// must survive the round trip, or a key silently changes identity in flight.
func TestScancodeRoundTripsForEveryMappedKey(t *testing.T) {
	mapped := 0
	for evdev := uint16(1); evdev < 256; evdev++ {
		sc, ok := ToScancode(evdev)
		if !ok {
			continue
		}
		mapped++

		back, ok := FromScancode(sc)
		if !ok {
			t.Errorf("evdev %d -> %+v -> not mapped back", evdev, sc)
			continue
		}
		if back != evdev {
			t.Errorf("evdev %d -> %+v -> evdev %d; want the original", evdev, sc, back)
		}
	}
	if mapped < 100 {
		t.Fatalf("only %d keys mapped; the table looks truncated", mapped)
	}
	t.Logf("%d keys round-trip cleanly", mapped)
}

func TestFromScancodeDistinguishesTheE0Prefix(t *testing.T) {
	// 0x53 is KEY_KPDOT bare and KEY_DELETE extended.
	bare, ok := FromScancode(Scancode{Code: 0x53})
	if !ok || bare != 83 {
		t.Errorf("bare 0x53 -> %d (%v), want KEY_KPDOT (83)", bare, ok)
	}
	ext, ok := FromScancode(Scancode{Code: 0x53, Extended: true})
	if !ok || ext != 111 {
		t.Errorf("extended 0x53 -> %d (%v), want KEY_DELETE (111)", ext, ok)
	}
}

func TestFromScancodeRejectsUnknownCodes(t *testing.T) {
	for _, sc := range []Scancode{{Code: 0x00}, {Code: 0xff}, {Code: 0x99, Extended: true}} {
		if got, ok := FromScancode(sc); ok {
			t.Errorf("FromScancode(%+v) = %d, true; want not mapped", sc, got)
		}
	}
}
