package pointer

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeRunner answers gdbus calls from a table keyed by object path and
// property, so a test needs neither KWin nor a real device.
type fakeRunner struct {
	props map[string]string // "event12/name" -> gdbus output
	calls int
}

func (f *fakeRunner) Run(_ string, args ...string) ([]byte, error) {
	var path, prop string
	for i, a := range args {
		if a == "--object-path" && i+1 < len(args) {
			path = args[i+1]
		}
	}
	prop = args[len(args)-1]
	f.calls++

	key := path[strings.LastIndex(path, "/")+1:] + "/" + prop
	out, ok := f.props[key]
	if !ok {
		// What gdbus does for an object that is not there.
		return nil, errors.New("Error: object path not found")
	}
	return []byte(out), nil
}

func devices(flat bool) *fakeRunner {
	return &fakeRunner{props: map[string]string{
		"event0/name":                            "(<'Power Button'>,)\n",
		"event12/name":                           "(<'conn'>,)\n",
		"event12/pointerAccelerationProfileFlat": fmt.Sprintf("(<%t>,)\n", flat),
	}}
}

func TestFlatReportsAFlatProfile(t *testing.T) {
	got, err := NewWithRunner(devices(true)).Flat("conn")
	if err != nil {
		t.Fatalf("Flat: %v", err)
	}
	if !got {
		t.Error("Flat() = false for a device on the flat profile")
	}
}

// The default profile is adaptive, and that is precisely the configuration that
// makes an injected delta land nearly twice as far as intended.
func TestFlatReportsTheDefaultAdaptiveProfile(t *testing.T) {
	got, err := NewWithRunner(devices(false)).Flat("conn")
	if err != nil {
		t.Fatalf("Flat: %v", err)
	}
	if got {
		t.Error("Flat() = true for a device on the adaptive profile")
	}
}

// The device is absent whenever the daemon is not running, which is a state to
// report plainly rather than one to confuse with a bad profile.
func TestFlatSaysWhenTheDeviceIsAbsent(t *testing.T) {
	r := devices(true)
	delete(r.props, "event12/name")

	_, err := NewWithRunner(r).Flat("conn")
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("err = %v, want ErrDeviceNotFound", err)
	}
}

// KWin's device numbering has gaps, so a missing object in the middle must not
// stop the scan before it reaches the device.
func TestFlatScansPastGapsInTheNumbering(t *testing.T) {
	r := devices(true)
	delete(r.props, "event0/name")

	got, err := NewWithRunner(r).Flat("conn")
	if err != nil {
		t.Fatalf("Flat: %v", err)
	}
	if !got {
		t.Error("Flat() = false; the scan stopped at the gap")
	}
}

// A name that merely contains ours is a different device.
func TestFlatDoesNotMatchOnASubstring(t *testing.T) {
	r := devices(true)
	r.props["event12/name"] = "(<'conn-other'>,)\n"

	if _, err := NewWithRunner(r).Flat("conn"); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("err = %v, want ErrDeviceNotFound", err)
	}
}
