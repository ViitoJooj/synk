// Package pointer reports how KWin is accelerating the virtual pointer.
//
// It matters more than it sounds. The session places the cursor by injecting a
// relative delta, and that only lands where intended if one injected unit moves
// one pixel. Under libinput's default adaptive profile it does not: measured on
// this machine, an intended 500 pixels travelled 934. The cursor then arrives
// somewhere other than where it crossed, and the model the session keeps of
// where the pointer is drifts from the pointer itself.
//
// The fix is a flat profile for this one device. The installer writes it to
// kcminputrc, but that alone is not enough: KWin reads the file when the
// session starts and does not look again when a new device appears, and the
// injector creates a new device every time it starts. Measured after the
// rename to synk: the entry was in the file and the device was still adaptive.
// So the agent also sets it live, over KWin's DBus, with MakeFlat.
package pointer

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrDeviceNotFound reports that KWin knows of no such input device, which is
// the normal state of affairs when the daemon is not running.
var ErrDeviceNotFound = errors.New("pointer: KWin has no such input device")

// Runner executes a command, injected so tests need no compositor. Mirrors
// screen.Runner and internal/service.Runner.
type Runner interface {
	Run(name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

type Reader struct{ runner Runner }

func New() *Reader { return &Reader{runner: execRunner{}} }

func NewWithRunner(r Runner) *Reader { return &Reader{runner: r} }

// maxDevices bounds the scan. KWin numbers its input devices from event0 and a
// laptop has a handful; scanning far past them would just be slow.
const maxDevices = 32

// Flat reports whether the named device is on a flat acceleration profile.
func (r *Reader) Flat(deviceName string) (bool, error) {
	path, err := r.find(deviceName)
	if err != nil {
		return false, err
	}
	out, err := r.property(path, "pointerAccelerationProfileFlat")
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "true"), nil
}

// MakeFlat puts the named device on a flat profile with no acceleration,
// effective immediately. KWin also writes it back to kcminputrc itself.
func (r *Reader) MakeFlat(deviceName string) error {
	path, err := r.find(deviceName)
	if err != nil {
		return err
	}
	if err := r.set(path, "pointerAccelerationProfileFlat", "<true>"); err != nil {
		return err
	}
	return r.set(path, "pointerAcceleration", "<0.0>")
}

// find locates the DBus object for a device by name. KWin exposes no lookup by
// name, so the objects are read in turn until one matches.
func (r *Reader) find(deviceName string) (string, error) {
	for i := range maxDevices {
		path := fmt.Sprintf("/org/kde/KWin/InputDevice/event%d", i)
		out, err := r.property(path, "name")
		if err != nil {
			continue // no device at this number; the numbering has gaps
		}
		if strings.Contains(out, `'`+deviceName+`'`) {
			return path, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrDeviceNotFound, deviceName)
}

func (r *Reader) property(path, name string) (string, error) {
	out, err := r.runner.Run("gdbus", "call", "--session",
		"--dest", "org.kde.KWin",
		"--object-path", path,
		"--method", "org.freedesktop.DBus.Properties.Get",
		"org.kde.KWin.InputDevice", name)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (r *Reader) set(path, name, value string) error {
	out, err := r.runner.Run("gdbus", "call", "--session",
		"--dest", "org.kde.KWin",
		"--object-path", path,
		"--method", "org.freedesktop.DBus.Properties.Set",
		"org.kde.KWin.InputDevice", name, value)
	if err != nil {
		return fmt.Errorf("pointer: setting %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
