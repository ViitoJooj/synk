package inputdev

import (
	"path/filepath"
	"testing"
	"time"
)

// setBits builds a kernel capability bitmap with the given bits set.
func setBits(size int, bits ...int) []byte {
	b := make([]byte, size)
	for _, n := range bits {
		b[n/8] |= 1 << (n % 8)
	}
	return b
}

func TestHasBitReadsTheKernelBitmapLayout(t *testing.T) {
	// The kernel packs capabilities little-endian by byte, LSB first.
	bits := setBits(16, 0, 7, 8, 63)

	for _, n := range []int{0, 7, 8, 63} {
		if !hasBit(bits, n) {
			t.Errorf("hasBit(%d) = false, want true", n)
		}
	}
	for _, n := range []int{1, 6, 9, 62, 64, 127} {
		if hasBit(bits, n) {
			t.Errorf("hasBit(%d) = true, want false", n)
		}
	}
}

// Reading past the end of a short bitmap must be false, not a panic — the
// kernel returns fewer bytes than requested for simple devices.
func TestHasBitOutOfRangeIsFalseNotPanic(t *testing.T) {
	if hasBit([]byte{0xff}, 100) {
		t.Error("hasBit past the end = true, want false")
	}
	if hasBit(nil, 0) {
		t.Error("hasBit(nil) = true, want false")
	}
}

func TestClassifyKeyboard(t *testing.T) {
	// A keyboard reports EV_KEY plus the letter block.
	ev := setBits(4, int(evKey))
	keys := setBits(96, keyQ, keyA, keyZ, 57 /* space */)

	got := classify(ev, keys, nil)

	if !got.Keyboard {
		t.Error("Keyboard = false, want true")
	}
	if got.Pointer {
		t.Error("Pointer = true, want false for a plain keyboard")
	}
}

func TestClassifyMouse(t *testing.T) {
	// A mouse reports EV_REL with X and Y, plus BTN_LEFT.
	ev := setBits(4, int(evKey), int(evRel))
	keys := setBits(96, int(btnLeft), int(btnRight))
	rel := setBits(4, int(relX), int(relY))

	got := classify(ev, keys, rel)

	if !got.Pointer {
		t.Error("Pointer = false, want true")
	}
	if got.Keyboard {
		t.Error("Keyboard = true, want false — a mouse has no letter keys")
	}
}

// Power buttons and lid switches expose EV_KEY too. Grabbing one would be
// pointless at best and would swallow the power button at worst.
func TestClassifyIgnoresDeviceWithOnlyAFewKeys(t *testing.T) {
	ev := setBits(4, int(evKey))
	keys := setBits(96, 116 /* KEY_POWER */)

	got := classify(ev, keys, nil)

	if got.Keyboard || got.Pointer {
		t.Errorf("got %+v, want neither for a power-button device", got)
	}
}

// A combo receiver reports both, and must be usable as both.
func TestClassifyKeyboardAndPointerCombo(t *testing.T) {
	ev := setBits(4, int(evKey), int(evRel))
	keys := setBits(96, keyQ, keyA, keyZ, int(btnLeft))
	rel := setBits(4, int(relX), int(relY))

	got := classify(ev, keys, rel)

	if !got.Keyboard || !got.Pointer {
		t.Errorf("got %+v, want both Keyboard and Pointer", got)
	}
}

// A pointer needs both axes; a device with only REL_X is something else,
// like a volume dial.
func TestClassifyRejectsSingleAxisDevice(t *testing.T) {
	ev := setBits(4, int(evKey), int(evRel))
	keys := setBits(96, int(btnLeft))
	rel := setBits(4, int(relX))

	if got := classify(ev, keys, rel); got.Pointer {
		t.Error("Pointer = true, want false for a single-axis device")
	}
}

// This laptop's touchpad (BLTP7853, EV=0x1b) reports SYN+KEY+ABS+MSC and no
// EV_REL at all: it is an absolute multitouch device. Requiring EV_REL made
// ListDevices miss it entirely.
func TestClassifyTouchpad(t *testing.T) {
	ev := setBits(4, int(evKey), int(evAbs))
	keys := setBits(96, btnToolFinger, btnTouch, int(btnLeft))

	got := classify(ev, keys, nil)

	if !got.Touchpad {
		t.Error("Touchpad = false, want true")
	}
	if got.Pointer {
		t.Error("Pointer = true, want false — a touchpad has no relative axes")
	}
	if !got.Any() {
		t.Error("Any() = false; a touchpad must be worth grabbing")
	}
}

// Touchscreens also report EV_ABS and BTN_TOUCH, but no finger tool, and must
// not be mistaken for a touchpad.
func TestClassifyIgnoresTouchscreen(t *testing.T) {
	ev := setBits(4, int(evKey), int(evAbs))
	keys := setBits(96, btnTouch)

	if got := classify(ev, keys, nil); got.Touchpad {
		t.Error("Touchpad = true for a touchscreen, want false")
	}
}

func TestEvdevIoctlConstantsMatchTheKernelHeaders(t *testing.T) {
	if got := eviocGName(256); got != 0x81004506 {
		t.Errorf("EVIOCGNAME(256) = %#08x, want 0x81004506", got)
	}
	if got := eviocGBit(0, 32); got != 0x80204520 {
		t.Errorf("EVIOCGBIT(0, 32) = %#08x, want 0x80204520", got)
	}
	if got := eviocGBit(evKey, 96); got != 0x80604521 {
		t.Errorf("EVIOCGBIT(EV_KEY, 96) = %#08x, want 0x80604521", got)
	}
	if got := eviocGBit(evRel, 4); got != 0x80044522 {
		t.Errorf("EVIOCGBIT(EV_REL, 4) = %#08x, want 0x80044522", got)
	}
}

// ---- integration: needs root ----

// The strongest proof the injection layer works: create the virtual device,
// inject through it, then read the events back off its own /dev/input node and
// check they arrived byte for byte.
func TestIntegrationInjectedEventsAreDeliveredByTheKernel(t *testing.T) {
	requireRoot(t)

	const devName = "conn-readback"
	v, err := OpenVirtual(devName)
	if err != nil {
		t.Fatalf("OpenVirtual: %v", err)
	}
	defer v.Close()

	dev := waitForDevice(t, devName, 3*time.Second)
	defer dev.Close()

	if caps := dev.Caps(); !caps.Keyboard || !caps.Pointer {
		t.Errorf("Caps() = %+v, want both — it declares letter keys and both axes", caps)
	}

	go func() {
		v.Key(30, true)  // KEY_A down
		v.Key(30, false) // KEY_A up
		v.MoveRel(7, -3)
	}()

	want := []struct {
		typ   uint16
		code  uint16
		value int32
	}{
		{evKey, 30, 1},
		{evKey, 30, 0},
		{evRel, relX, 7},
		{evRel, relY, -3},
	}

	for _, w := range want {
		typ, code, value, err := readSkippingSyn(dev)
		if err != nil {
			t.Fatalf("reading back: %v", err)
		}
		if typ != w.typ || code != w.code || value != w.value {
			t.Errorf("got (type %d, code %d, value %d), want (%d, %d, %d)",
				typ, code, value, w.typ, w.code, w.value)
		}
	}
}

// Grabbing must succeed on a real device and be reversible; a grab that leaks
// leaves the keyboard dead.
func TestIntegrationGrabAndUngrab(t *testing.T) {
	requireRoot(t)

	v, err := OpenVirtual("conn-grab")
	if err != nil {
		t.Fatalf("OpenVirtual: %v", err)
	}
	defer v.Close()

	dev := waitForDevice(t, "conn-grab", 3*time.Second)
	defer dev.Close()

	if err := dev.Grab(); err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if err := dev.Ungrab(); err != nil {
		t.Fatalf("Ungrab: %v", err)
	}
	// Re-grabbing after an ungrab proves the release really happened.
	if err := dev.Grab(); err != nil {
		t.Errorf("re-Grab after Ungrab: %v", err)
	}
}

func TestIntegrationListDevicesFindsRealHardware(t *testing.T) {
	requireRoot(t)

	devs, err := ListDevices()
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	defer func() {
		for _, d := range devs {
			d.Close()
		}
	}()

	if len(devs) == 0 {
		t.Fatal("found no keyboards or pointers; this machine has both")
	}
	for _, d := range devs {
		t.Logf("%s -> %q %+v", d.Path(), d.Name(), d.Caps())
	}
}

// ---- test helpers ----

// waitForDevice polls /dev/input for the node udev creates for our virtual
// device. ListDevices deliberately hides conn devices, so the scan here
// is deliberately raw.
func waitForDevice(t *testing.T, name string, timeout time.Duration) *Device {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		paths, _ := filepath.Glob("/dev/input/event*")
		for _, p := range paths {
			d, err := OpenDevice(p)
			if err != nil {
				continue
			}
			if d.Name() == name {
				return d
			}
			d.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("virtual device %q never appeared under /dev/input", name)
	return nil
}

// readSkippingSyn drops the SYN_REPORT separators so a test can assert on the
// events it actually cares about. The deadline keeps a failure a failure
// rather than a hang.
func readSkippingSyn(d *Device) (uint16, uint16, int32, error) {
	if err := d.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return 0, 0, 0, err
	}
	for {
		typ, code, value, err := d.ReadEvent()
		if err != nil {
			return 0, 0, 0, err
		}
		if typ == evSyn {
			continue
		}
		return typ, code, value, nil
	}
}
