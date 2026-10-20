package keystate

import (
	"reflect"
	"testing"
)

// Regular (non-modifier) evdev codes used across these tests; the modifier
// codes come from the package itself.
const (
	keyA = 30
	keyB = 48
)

func TestReleaseAllReturnsHeldKey(t *testing.T) {
	tr := New()
	tr.Key(keyA, true)

	got := tr.ReleaseAll()

	if want := []Event{{Kind: KindKey, Code: keyA}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestReleasedKeyIsNotReported(t *testing.T) {
	tr := New()
	tr.Key(keyA, true)
	tr.Key(keyA, false)

	if got := tr.ReleaseAll(); len(got) != 0 {
		t.Errorf("got %+v, want nothing held", got)
	}
}

// The whole point of the module: crossing back with keys down must not leave
// them stuck on the machine that received them.
func TestReleaseAllReturnsEveryHeldKey(t *testing.T) {
	tr := New()
	tr.Key(keyA, true)
	tr.Key(keyB, true)
	tr.Key(keyLeftShift, true)

	got := tr.ReleaseAll()

	if len(got) != 3 {
		t.Fatalf("got %d releases, want 3: %+v", len(got), got)
	}
	seen := map[uint16]bool{}
	for _, e := range got {
		seen[e.Code] = true
	}
	for _, code := range []uint16{keyA, keyB, keyLeftShift} {
		if !seen[code] {
			t.Errorf("code %d missing from %+v", code, got)
		}
	}
}

// Releasing Shift before A would make the receiver emit a stray lowercase
// character, so modifiers must come last.
func TestModifiersAreReleasedAfterRegularKeys(t *testing.T) {
	tr := New()
	tr.Key(keyLeftShift, true)
	tr.Key(keyLeftCtrl, true)
	tr.Key(keyA, true)
	tr.Key(keyB, true)

	got := tr.ReleaseAll()

	if len(got) != 4 {
		t.Fatalf("got %d releases, want 4: %+v", len(got), got)
	}
	for i, e := range got[:2] {
		if isModifier(e.Code) {
			t.Errorf("release[%d] = code %d is a modifier, want regular key first", i, e.Code)
		}
	}
	for i, e := range got[2:] {
		if !isModifier(e.Code) {
			t.Errorf("release[%d] = code %d is a regular key, want modifier last", i+2, e.Code)
		}
	}
}

func TestReleaseAllIsIdempotent(t *testing.T) {
	tr := New()
	tr.Key(keyA, true)

	if got := tr.ReleaseAll(); len(got) != 1 {
		t.Fatalf("first ReleaseAll got %+v, want 1 event", got)
	}
	if got := tr.ReleaseAll(); len(got) != 0 {
		t.Errorf("second ReleaseAll got %+v, want nothing", got)
	}
}

// Autorepeat and duplicated network frames both resend key-down.
func TestRepeatedPressYieldsSingleRelease(t *testing.T) {
	tr := New()
	tr.Key(keyA, true)
	tr.Key(keyA, true)
	tr.Key(keyA, true)

	if got := tr.ReleaseAll(); len(got) != 1 {
		t.Errorf("got %+v, want exactly 1 release", got)
	}
}

// A key-up with no matching key-down happens after a reconnect; it must not
// create phantom state.
func TestReleaseWithoutPressIsIgnored(t *testing.T) {
	tr := New()
	tr.Key(keyA, false)

	if got := tr.ReleaseAll(); len(got) != 0 {
		t.Errorf("got %+v, want nothing held", got)
	}
}

func TestButtonsAreTrackedAlongsideKeys(t *testing.T) {
	tr := New()
	tr.Key(keyA, true)
	tr.Button(0, true) // left
	tr.Button(1, true) // right
	tr.Button(1, false)

	got := tr.ReleaseAll()

	want := map[Event]bool{
		{Kind: KindKey, Code: keyA}: true,
		{Kind: KindButton, Code: 0}: true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %d events", got, len(want))
	}
	for _, e := range got {
		if !want[e] {
			t.Errorf("unexpected release %+v", e)
		}
	}
}

// A dragged selection left mid-cross must not leave the button latched.
func TestHeldButtonSurvivesUntilReleaseAll(t *testing.T) {
	tr := New()
	tr.Button(0, true)

	if n := tr.Held(); n != 1 {
		t.Errorf("Held() = %d, want 1", n)
	}
	tr.ReleaseAll()
	if n := tr.Held(); n != 0 {
		t.Errorf("Held() after ReleaseAll = %d, want 0", n)
	}
}

func TestIsModifierCoversBothSides(t *testing.T) {
	for _, code := range []uint16{keyLeftShift, keyLeftCtrl, keyLeftAlt, keyRightMeta} {
		if !isModifier(code) {
			t.Errorf("isModifier(%d) = false, want true", code)
		}
	}
	for _, code := range []uint16{keyA, keyB} {
		if isModifier(code) {
			t.Errorf("isModifier(%d) = true, want false", code)
		}
	}
}
