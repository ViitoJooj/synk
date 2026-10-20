package inputdev

import "github.com/ViitoJooj/synk/internal/proto"

// Win32 mouse messages and the wheel unit. They live in this build-tag-free
// file, rather than beside the rest of the Win32 bindings, so the translation
// logic they drive can be tested on any platform.
const (
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	wmMButtonDown = 0x0207
	wmMButtonUp   = 0x0208
	wmXButtonDown = 0x020B
	wmXButtonUp   = 0x020C

	// wheelDelta is WHEEL_DELTA: the units in one detent of a classic wheel.
	wheelDelta = 120

	xButton1 = 0x0001
	xButton2 = 0x0002
)

// evdev codes for the escape hotkey.
const (
	keyLeftCtrl  = 29
	keyLeftAlt   = 56
	keyRightCtrl = 97
	keyRightAlt  = 100
	keyF12       = 88
)

// WheelAccumulator turns raw wheel units into whole notches.
//
// A classic wheel reports 120 per detent, but a high-resolution wheel reports
// as little as 8 at a time. Dividing each report by 120 would round every one
// of those to zero, and the wheel would appear dead — so the remainder carries
// across reports.
type WheelAccumulator struct{ rem int }

// Add folds in one wheel report and returns the whole notches it completed.
func (w *WheelAccumulator) Add(delta int16) int {
	d := int(delta)

	// Reversing direction abandons the partial notch. Keeping it would make
	// the first scroll back cancel out instead of scrolling.
	if w.rem != 0 && (d > 0) != (w.rem > 0) {
		w.rem = 0
	}

	w.rem += d
	notches := w.rem / wheelDelta
	w.rem -= notches * wheelDelta
	return notches
}

// mouseMessageToButton maps a Win32 button message to the neutral wire button.
// data is MSLLHOOKSTRUCT.mouseData, whose high word names which X button fired.
func mouseMessageToButton(message uint32, data uint32) (button uint8, pressed bool, ok bool) {
	switch message {
	case wmLButtonDown:
		return proto.BtnLeft, true, true
	case wmLButtonUp:
		return proto.BtnLeft, false, true
	case wmRButtonDown:
		return proto.BtnRight, true, true
	case wmRButtonUp:
		return proto.BtnRight, false, true
	case wmMButtonDown:
		return proto.BtnMiddle, true, true
	case wmMButtonUp:
		return proto.BtnMiddle, false, true
	case wmXButtonDown, wmXButtonUp:
		btn := proto.BtnSide
		if data>>16 == xButton2 {
			btn = proto.BtnExtra
		}
		return btn, message == wmXButtonDown, true
	}
	return 0, false, false
}

// scanToEvdev converts what the keyboard hook reports into the wire's evdev
// code. Unmappable keys are dropped rather than guessed at.
func scanToEvdev(scan uint32, extended bool) (uint16, bool) {
	return proto.FromScancode(proto.Scancode{Code: uint16(scan), Extended: extended})
}

// escapeDetector recognises Ctrl+Alt+F12, the hatch that returns focus to the
// local machine if the link wedges while the far side holds it.
type escapeDetector struct{ ctrl, alt bool }

// Key feeds one key event and reports whether the hotkey just fired.
func (e *escapeDetector) Key(code uint16, pressed bool) bool {
	switch code {
	case keyLeftCtrl, keyRightCtrl:
		e.ctrl = pressed
	case keyLeftAlt, keyRightAlt:
		e.alt = pressed
	case keyF12:
		return pressed && e.ctrl && e.alt
	}
	return false
}
