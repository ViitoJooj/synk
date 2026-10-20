package proto

// Scancode is a PS/2 Set 1 scancode, the form Windows SendInput accepts with
// KEYEVENTF_SCANCODE. Scancodes are used rather than virtual-key codes because
// they are layout independent and survive games that read input at a low level.
type Scancode struct {
	Code     uint16
	Extended bool // needs the 0xE0 prefix
}

// mainBlockMax is the top of the range where evdev and Set 1 agree exactly.
// evdev's numbering was lifted from Set 1, so KEY_ESC(1)..KEY_KPDOT(83) need
// no translation at all.
const mainBlockMax = 83

// nonExtended covers the few keys above the identity range that still carry a
// bare Set 1 code. 84 and 85 are deliberately absent: they are not keys on any
// keyboard this tool targets.
var nonExtended = map[uint16]uint16{
	86: 0x56, // KEY_102ND — the extra key beside left shift on ISO boards
	87: 0x57, // KEY_F11
	88: 0x58, // KEY_F12
	89: 0x73, // KEY_RO — the "/ ? °" key, present on ABNT2
	95: 0x7e, // KEY_KPJPCOMMA — the keypad comma, present on ABNT2
}

// extended maps the keys that Windows distinguishes only by the 0xE0 prefix.
// KEY_PAUSE is absent on purpose: it is the one key that uses an 0xE1 prefix
// and a two-scancode sequence, and getting it half right is worse than
// leaving it alone.
var extended = map[uint16]uint16{
	96:  0x1c, // KEY_KPENTER
	97:  0x1d, // KEY_RIGHTCTRL
	98:  0x35, // KEY_KPSLASH
	99:  0x37, // KEY_SYSRQ (Print Screen)
	100: 0x38, // KEY_RIGHTALT
	102: 0x47, // KEY_HOME
	103: 0x48, // KEY_UP
	104: 0x49, // KEY_PAGEUP
	105: 0x4b, // KEY_LEFT
	106: 0x4d, // KEY_RIGHT
	107: 0x4f, // KEY_END
	108: 0x50, // KEY_DOWN
	109: 0x51, // KEY_PAGEDOWN
	110: 0x52, // KEY_INSERT
	111: 0x53, // KEY_DELETE
	125: 0x5b, // KEY_LEFTMETA
	126: 0x5c, // KEY_RIGHTMETA
	127: 0x5d, // KEY_COMPOSE (menu)
}

// ToScancode converts a Linux evdev keycode to its PS/2 Set 1 equivalent. It
// reports false for codes with no PS/2 counterpart — media keys, gamepad
// buttons and the rest of the evdev-only space — which the caller should drop
// rather than guess at.
func ToScancode(evdev uint16) (Scancode, bool) {
	if evdev >= 1 && evdev <= mainBlockMax {
		return Scancode{Code: evdev}, true
	}
	if code, ok := nonExtended[evdev]; ok {
		return Scancode{Code: code}, true
	}
	if code, ok := extended[evdev]; ok {
		return Scancode{Code: code, Extended: true}, true
	}
	return Scancode{}, false
}

// fromScancode is derived from the forward tables rather than written out
// again, so the two directions cannot drift apart. TestMappingIsInjective
// guarantees the inversion loses nothing.
var fromScancode = func() map[Scancode]uint16 {
	m := make(map[Scancode]uint16)
	for evdev := uint16(1); evdev < 256; evdev++ {
		if sc, ok := ToScancode(evdev); ok {
			m[sc] = evdev
		}
	}
	return m
}()

// FromScancode is ToScancode's inverse, used on the Windows side to turn what
// its hooks report back into the evdev codes the wire speaks.
func FromScancode(sc Scancode) (uint16, bool) {
	evdev, ok := fromScancode[sc]
	return evdev, ok
}
