//go:build linux

package inputdev

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Letter keys used to tell a real keyboard from a device that merely reports a
// power button or a few media keys.
const (
	keyQ = 16
	keyA = 30
	keyZ = 44
)

// VirtualNamePrefix marks the devices this tool creates. ListDevices skips
// them: reading back our own injected events would feed them straight into the
// sender and loop forever.
//
// It is also the device's name, and KDE keys the pointer profile on that name:
// hacks/install.sh writes the flat profile under it in kcminputrc. Renaming it
// means rewriting that entry, or the cursor lands in the wrong place again.
const VirtualNamePrefix = "synk"

// eviocGName builds EVIOCGNAME(len) — _IOR('E', 0x06, char[len]).
func eviocGName(length int) uintptr {
	return ioc(iocRead, 'E', 0x06, length)
}

// eviocGBit builds EVIOCGBIT(ev, len) — _IOR('E', 0x20+ev, char[len]).
func eviocGBit(ev uint16, length int) uintptr {
	return ioc(iocRead, 'E', 0x20+int(ev), length)
}

// Linux ioctl encoding: dir<<30 | size<<16 | type<<8 | nr.
const iocRead = 2

func ioc(dir, typ, nr, size int) uintptr {
	return uintptr(dir)<<30 | uintptr(size)<<16 | uintptr(typ)<<8 | uintptr(nr)
}

// evdev codes for absolute pointing devices, from <linux/input-event-codes.h>.
const (
	evAbs uint16 = 3

	btnToolFinger = 0x145
	btnTouch      = 0x14a
)

// Caps says what a device can do, which decides whether it is worth grabbing.
type Caps struct {
	Keyboard bool
	Pointer  bool // relative: a mouse or trackpoint
	Touchpad bool // absolute multitouch
}

func (c Caps) Any() bool { return c.Keyboard || c.Pointer || c.Touchpad }

// Device is an opened /dev/input/eventN.
type Device struct {
	f       *os.File
	path    string
	name    string
	caps    Caps
	grabbed bool
}

// OpenDevice opens one event node read-only. It needs root on stock Debian.
func OpenDevice(path string) (*Device, error) {
	// Non-blocking so Go's poller owns the fd and read deadlines work.
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("inputdev: opening %s: %w", path, err)
	}

	d := &Device{f: f, path: path}
	if d.name, err = deviceName(f.Fd()); err != nil {
		f.Close()
		return nil, fmt.Errorf("inputdev: reading name of %s: %w", path, err)
	}
	if d.caps, err = deviceCaps(f.Fd()); err != nil {
		f.Close()
		return nil, fmt.Errorf("inputdev: reading caps of %s: %w", path, err)
	}
	return d, nil
}

func (d *Device) Name() string { return d.name }
func (d *Device) Path() string { return d.path }
func (d *Device) Caps() Caps   { return d.caps }

// Grab takes the device exclusively, so its events stop reaching the local
// compositor and reach only us.
func (d *Device) Grab() error {
	if err := ioctl(d.f.Fd(), eviocGrab, 1); err != nil {
		return fmt.Errorf("inputdev: grabbing %s: %w", d.path, err)
	}
	d.grabbed = true
	return nil
}

// Ungrab hands the device back to the compositor.
func (d *Device) Ungrab() error {
	if err := ioctl(d.f.Fd(), eviocGrab, 0); err != nil {
		return fmt.Errorf("inputdev: ungrabbing %s: %w", d.path, err)
	}
	d.grabbed = false
	return nil
}

func (d *Device) Grabbed() bool { return d.grabbed }

// SetReadDeadline bounds how long ReadEvent blocks, so a read loop can be
// unwound at shutdown instead of sitting in the kernel forever.
func (d *Device) SetReadDeadline(t time.Time) error { return d.f.SetReadDeadline(t) }

// ReadEvent reads one struct input_event.
func (d *Device) ReadEvent() (typ, code uint16, value int32, err error) {
	var buf [eventSize]byte
	if _, err := readFull(d.f, buf[:]); err != nil {
		return 0, 0, 0, err
	}
	return binary.LittleEndian.Uint16(buf[16:]),
		binary.LittleEndian.Uint16(buf[18:]),
		int32(binary.LittleEndian.Uint32(buf[20:])),
		nil
}

// Close releases the grab before closing. Closing alone would also release it,
// since the kernel drops the grab with the fd, but being explicit means a
// failure here is visible rather than silent.
func (d *Device) Close() error {
	if d.grabbed {
		_ = d.Ungrab()
	}
	return d.f.Close()
}

// ListDevices returns every keyboard or pointer under /dev/input, skipping this
// tool's own virtual devices. Devices that are neither are closed and dropped.
func ListDevices() ([]*Device, error) {
	paths, err := filepath.Glob("/dev/input/event*")
	if err != nil {
		return nil, fmt.Errorf("inputdev: listing /dev/input: %w", err)
	}
	sort.Strings(paths) // glob order is not guaranteed; keep runs reproducible

	var out []*Device
	for _, p := range paths {
		d, err := OpenDevice(p)
		if err != nil {
			// A node we cannot open is not fatal: some are hot-unplugged
			// between the glob and the open.
			continue
		}
		if !d.caps.Any() || strings.HasPrefix(d.name, VirtualNamePrefix) {
			d.Close()
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func deviceName(fd uintptr) (string, error) {
	buf := make([]byte, 256)
	if err := ioctlPtr(fd, eviocGName(len(buf)), &buf[0]); err != nil {
		return "", err
	}
	if i := bytes.IndexByte(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	return string(buf), nil
}

func deviceCaps(fd uintptr) (Caps, error) {
	evBits := make([]byte, (32+7)/8)
	if err := ioctlPtr(fd, eviocGBit(0, len(evBits)), &evBits[0]); err != nil {
		return Caps{}, err
	}
	keyBits := make([]byte, (768+7)/8)
	if err := ioctlPtr(fd, eviocGBit(evKey, len(keyBits)), &keyBits[0]); err != nil {
		return Caps{}, err
	}
	relBits := make([]byte, (16+7)/8)
	if err := ioctlPtr(fd, eviocGBit(evRel, len(relBits)), &relBits[0]); err != nil {
		return Caps{}, err
	}
	return classify(evBits, keyBits, relBits), nil
}

// classify decides what a device is from its capability bitmaps.
//
// The letter-key test matters: lid switches, power buttons and video-bus
// devices all advertise EV_KEY, and grabbing the power button would be a
// genuinely bad outcome.
func classify(evBits, keyBits, relBits []byte) Caps {
	var c Caps

	if hasBit(evBits, int(evKey)) &&
		hasBit(keyBits, keyQ) && hasBit(keyBits, keyA) && hasBit(keyBits, keyZ) {
		c.Keyboard = true
	}

	// Both axes plus a left button: one axis alone is a dial or a scroll-only
	// device, not something that can move a cursor.
	if hasBit(evBits, int(evRel)) &&
		hasBit(relBits, int(relX)) && hasBit(relBits, int(relY)) &&
		hasBit(keyBits, int(btnLeft)) {
		c.Pointer = true
	}

	// Touchpads report absolute multitouch and no relative axes at all. The
	// finger tool is what separates them from touchscreens, which report
	// BTN_TOUCH but never BTN_TOOL_FINGER.
	if hasBit(evBits, int(evAbs)) && hasBit(keyBits, btnToolFinger) {
		c.Touchpad = true
	}

	return c
}

// hasBit reads bit n from a kernel capability bitmap. Out-of-range reads are
// false: the kernel returns a short buffer for simple devices.
func hasBit(bits []byte, n int) bool {
	i := n / 8
	if i < 0 || i >= len(bits) {
		return false
	}
	return bits[i]&(1<<(n%8)) != 0
}

// readFull fills buf, retrying short reads. An event node delivers whole
// structs, but a signal can still cut a read short.
func readFull(f *os.File, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := f.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, fmt.Errorf("inputdev: short read on %s", f.Name())
		}
	}
	return total, nil
}
