package inputdev

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	"github.com/ViitoJooj/synk/internal/proto"
)

// These values were read out of this machine's own <linux/uinput.h> and
// <linux/input.h> with a C program. A wrong ioctl number fails silently — the
// device simply never appears — so they are pinned here as a regression guard.
func TestIoctlConstantsMatchTheKernelHeaders(t *testing.T) {
	tests := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"UI_DEV_CREATE", uiDevCreate, 0x00005501},
		{"UI_DEV_DESTROY", uiDevDestroy, 0x00005502},
		{"UI_DEV_SETUP", uiDevSetup, 0x405c5503},
		{"UI_SET_EVBIT", uiSetEvBit, 0x40045564},
		{"UI_SET_KEYBIT", uiSetKeyBit, 0x40045565},
		{"UI_SET_RELBIT", uiSetRelBit, 0x40045566},
		{"EVIOCGRAB", eviocGrab, 0x40044590},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %#08x, want %#08x", tt.name, tt.got, tt.want)
		}
	}
}

// struct input_event on amd64: timeval(16) + type(2) + code(2) + value(4).
func TestEncodeEventMatchesKernelLayout(t *testing.T) {
	got := encodeEvent(evKey, 30, 1)

	if len(got) != 24 {
		t.Fatalf("len = %d, want 24 (sizeof struct input_event)", len(got))
	}
	if typ := binary.LittleEndian.Uint16(got[16:]); typ != evKey {
		t.Errorf("type at offset 16 = %d, want %d", typ, evKey)
	}
	if code := binary.LittleEndian.Uint16(got[18:]); code != 30 {
		t.Errorf("code at offset 18 = %d, want 30", code)
	}
	if val := int32(binary.LittleEndian.Uint32(got[20:])); val != 1 {
		t.Errorf("value at offset 20 = %d, want 1", val)
	}
}

func TestEncodeEventCarriesNegativeValues(t *testing.T) {
	got := encodeEvent(evRel, relX, -42)

	if val := int32(binary.LittleEndian.Uint32(got[20:])); val != -42 {
		t.Errorf("value = %d, want -42 — negative deltas move the cursor left", val)
	}
}

// struct uinput_setup: input_id(8) + name[80] + ff_effects_max(4) = 92.
func TestEncodeSetupMatchesKernelLayout(t *testing.T) {
	got := encodeSetup("conn test")

	if len(got) != 92 {
		t.Fatalf("len = %d, want 92 (sizeof struct uinput_setup)", len(got))
	}
	name := got[8:88]
	if !bytes.HasPrefix(name, []byte("conn test\x00")) {
		t.Errorf("name field = %q, want the name NUL-terminated at offset 8", name)
	}
}

// A name longer than the field must be truncated rather than overflow into
// ff_effects_max, and must still end in a NUL the kernel can find.
func TestEncodeSetupTruncatesLongNameAndKeepsNUL(t *testing.T) {
	long := string(bytes.Repeat([]byte("x"), 200))

	got := encodeSetup(long)

	if len(got) != 92 {
		t.Fatalf("len = %d, want 92", len(got))
	}
	if got[87] != 0 {
		t.Error("last byte of the name field is not NUL; the string was not truncated safely")
	}
	if ff := binary.LittleEndian.Uint32(got[88:]); ff != 0 {
		t.Errorf("ff_effects_max = %d, want 0 — the name overflowed its field", ff)
	}
}

func TestButtonCodesMatchTheKernel(t *testing.T) {
	tests := []struct {
		btn  uint8
		want uint16
	}{
		{proto.BtnLeft, 0x110},
		{proto.BtnRight, 0x111},
		{proto.BtnMiddle, 0x112},
		{proto.BtnSide, 0x113},
		{proto.BtnExtra, 0x114},
	}
	for _, tt := range tests {
		got, ok := btnCode(tt.btn)
		if !ok {
			t.Errorf("btnCode(%d) not mapped", tt.btn)
			continue
		}
		if got != tt.want {
			t.Errorf("btnCode(%d) = %#x, want %#x", tt.btn, got, tt.want)
		}
	}
	if _, ok := btnCode(99); ok {
		t.Error("btnCode(99) = ok, want an unknown button to be rejected")
	}
}

// ---- integration: needs root, creates a real virtual device ----

func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root for /dev/uinput; run: sudo -E go test ./internal/link/inputdev/ -run Integration")
	}
}

func TestIntegrationVirtualDeviceAcceptsEvents(t *testing.T) {
	requireRoot(t)

	v, err := OpenVirtual("conn-test")
	if err != nil {
		t.Fatalf("OpenVirtual: %v", err)
	}
	defer v.Close()

	if err := v.Key(30, true); err != nil {
		t.Errorf("Key down: %v", err)
	}
	if err := v.Key(30, false); err != nil {
		t.Errorf("Key up: %v", err)
	}
	if err := v.MoveRel(5, -5); err != nil {
		t.Errorf("MoveRel: %v", err)
	}
	if err := v.Button(proto.BtnLeft, true); err != nil {
		t.Errorf("Button: %v", err)
	}
	if err := v.Button(proto.BtnLeft, false); err != nil {
		t.Errorf("Button up: %v", err)
	}
	if err := v.Scroll(0, 1); err != nil {
		t.Errorf("Scroll: %v", err)
	}
}
