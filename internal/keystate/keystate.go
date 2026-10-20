// Package keystate tracks which keys and buttons the local machine is holding
// down on behalf of a remote sender, so every handoff, disconnect and shutdown
// can release exactly those.
//
// Without it, crossing back while holding Shift leaves Shift latched on the
// machine you just left — the classic failure of this class of tool.
package keystate

import "sort"

type Kind uint8

const (
	KindKey Kind = iota
	KindButton
)

// Event is one synthetic release to inject.
type Event struct {
	Kind Kind
	Code uint16
}

// evdev modifier keycodes, from linux/input-event-codes.h.
const (
	keyLeftCtrl   = 29
	keyLeftShift  = 42
	keyRightShift = 54
	keyLeftAlt    = 56
	keyRightCtrl  = 97
	keyRightAlt   = 100
	keyLeftMeta   = 125
	keyRightMeta  = 126
)

var modifiers = map[uint16]bool{
	keyLeftCtrl: true, keyLeftShift: true, keyRightShift: true, keyLeftAlt: true,
	keyRightCtrl: true, keyRightAlt: true, keyLeftMeta: true, keyRightMeta: true,
}

func isModifier(code uint16) bool { return modifiers[code] }

// Tracker is not safe for concurrent use; callers own the serialisation.
type Tracker struct {
	keys    map[uint16]bool
	buttons map[uint8]bool
}

func New() *Tracker {
	return &Tracker{
		keys:    make(map[uint16]bool),
		buttons: make(map[uint8]bool),
	}
}

func (t *Tracker) Key(code uint16, pressed bool) {
	if pressed {
		t.keys[code] = true
		return
	}
	delete(t.keys, code)
}

func (t *Tracker) Button(btn uint8, pressed bool) {
	if pressed {
		t.buttons[btn] = true
		return
	}
	delete(t.buttons, btn)
}

// Held reports how many keys and buttons are currently down.
func (t *Tracker) Held() int { return len(t.keys) + len(t.buttons) }

// ReleaseAll returns the events needed to undo all held state and clears the
// tracker, so a second call after a successful release is a no-op.
//
// Ordering is deliberate: buttons, then regular keys, then modifiers. Releasing
// Shift while a letter is still down makes the receiver emit a stray lowercase
// character, so every modifier goes last.
func (t *Tracker) ReleaseAll() []Event {
	if t.Held() == 0 {
		return nil
	}

	var buttons, regular, mods []Event
	for btn := range t.buttons {
		buttons = append(buttons, Event{Kind: KindButton, Code: uint16(btn)})
	}
	for code := range t.keys {
		e := Event{Kind: KindKey, Code: code}
		if isModifier(code) {
			mods = append(mods, e)
		} else {
			regular = append(regular, e)
		}
	}

	// Map iteration is random; sorting keeps the injected sequence
	// reproducible so a failure is the same failure every time.
	for _, group := range [][]Event{buttons, regular, mods} {
		sort.Slice(group, func(i, j int) bool { return group[i].Code < group[j].Code })
	}

	out := make([]Event, 0, len(buttons)+len(regular)+len(mods))
	out = append(out, buttons...)
	out = append(out, regular...)
	out = append(out, mods...)

	clear(t.keys)
	clear(t.buttons)
	return out
}
