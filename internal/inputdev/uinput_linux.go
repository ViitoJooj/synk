//go:build linux

// Package inputdev reads and injects raw input devices.
//
// On Linux it works through evdev and uinput, below the display server. That
// choice is what makes the tool work identically on X11 and Wayland: the
// libei/portal route the Deskflow project uses needs cgo and inherits the
// compositor's experimental input-capture support, while /dev/uinput is a
// stable kernel ABI that Go can drive with nothing but golang.org/x/sys.
package inputdev

import (
	"encoding/binary"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/ViitoJooj/synk/internal/proto"
)

// ioctl request numbers, taken from this machine's <linux/uinput.h> and
// <linux/input.h>. They are _IOW('U', n, T) macros, so Go cannot compute them
// from the headers; uinput_linux_test.go pins them against the real values.
const (
	uiDevCreate  uintptr = 0x00005501
	uiDevDestroy uintptr = 0x00005502
	uiDevSetup   uintptr = 0x405c5503
	uiSetEvBit   uintptr = 0x40045564
	uiSetKeyBit  uintptr = 0x40045565
	uiSetRelBit  uintptr = 0x40045566

	// eviocGrab takes the device exclusively, so events stop reaching the
	// local compositor while the far side has focus.
	eviocGrab uintptr = 0x40044590
)

const (
	evSyn uint16 = 0
	evKey uint16 = 1
	evRel uint16 = 2

	synReport uint16 = 0

	relX      uint16 = 0
	relY      uint16 = 1
	relHWheel uint16 = 6
	relWheel  uint16 = 8
)

// Kernel BTN_* codes for a mouse.
const (
	btnLeft   uint16 = 0x110
	btnRight  uint16 = 0x111
	btnMiddle uint16 = 0x112
	btnSide   uint16 = 0x113
	btnExtra  uint16 = 0x114
)

const (
	eventSize   = 24 // sizeof(struct input_event) on amd64
	setupSize   = 92 // sizeof(struct uinput_setup)
	nameSize    = 80 // UINPUT_MAX_NAME_SIZE
	maxKeyCode  = 255
	busUSB      = 0x03
	vendorLinux = 0x1d6b // Linux Foundation, so the device looks ordinary
)

// Virtual is a uinput device that injects keys, pointer motion and buttons.
// It presents as a combined keyboard and relative pointer, which is exactly
// what an ordinary wireless keyboard-and-mouse receiver looks like, so
// libinput classifies and accelerates it the same way.
type Virtual struct {
	f *os.File
}

// OpenVirtual creates the device. It needs write access to /dev/uinput, which
// on a stock Debian means root — hence the privileged daemon.
func OpenVirtual(name string) (*Virtual, error) {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("inputdev: opening /dev/uinput: %w", err)
	}

	v := &Virtual{f: f}
	if err := v.configure(name); err != nil {
		f.Close()
		return nil, err
	}
	return v, nil
}

func (v *Virtual) configure(name string) error {
	fd := v.f.Fd()

	for _, ev := range []uint16{evKey, evRel, evSyn} {
		if err := ioctl(fd, uiSetEvBit, uintptr(ev)); err != nil {
			return fmt.Errorf("inputdev: enabling event type %d: %w", ev, err)
		}
	}

	// Every key we could be asked to inject must be declared up front; the
	// kernel silently drops events for codes that were not enabled.
	for code := uint16(1); code <= maxKeyCode; code++ {
		if err := ioctl(fd, uiSetKeyBit, uintptr(code)); err != nil {
			return fmt.Errorf("inputdev: enabling key %d: %w", code, err)
		}
	}
	for _, btn := range []uint16{btnLeft, btnRight, btnMiddle, btnSide, btnExtra} {
		if err := ioctl(fd, uiSetKeyBit, uintptr(btn)); err != nil {
			return fmt.Errorf("inputdev: enabling button %#x: %w", btn, err)
		}
	}
	for _, rel := range []uint16{relX, relY, relWheel, relHWheel} {
		if err := ioctl(fd, uiSetRelBit, uintptr(rel)); err != nil {
			return fmt.Errorf("inputdev: enabling rel axis %d: %w", rel, err)
		}
	}

	setup := encodeSetup(name)
	if err := ioctlPtr(fd, uiDevSetup, &setup[0]); err != nil {
		return fmt.Errorf("inputdev: UI_DEV_SETUP: %w", err)
	}
	if err := ioctl(fd, uiDevCreate, 0); err != nil {
		return fmt.Errorf("inputdev: UI_DEV_CREATE: %w", err)
	}
	return nil
}

// Key injects a key press or release by evdev code.
func (v *Virtual) Key(code uint16, pressed bool) error {
	return v.emit(
		event{evKey, code, boolValue(pressed)},
	)
}

// MoveRel injects relative pointer motion.
func (v *Virtual) MoveRel(dx, dy int32) error {
	var evs []event
	// Zero deltas are dropped rather than emitted: a stream of no-op REL
	// events wakes the compositor for nothing.
	if dx != 0 {
		evs = append(evs, event{evRel, relX, dx})
	}
	if dy != 0 {
		evs = append(evs, event{evRel, relY, dy})
	}
	if len(evs) == 0 {
		return nil
	}
	return v.emit(evs...)
}

// Button injects a mouse button press or release.
func (v *Virtual) Button(btn uint8, pressed bool) error {
	code, ok := btnCode(btn)
	if !ok {
		return fmt.Errorf("inputdev: unknown button %d", btn)
	}
	return v.emit(event{evKey, code, boolValue(pressed)})
}

// Scroll injects wheel motion; dy is vertical, dx horizontal.
func (v *Virtual) Scroll(dx, dy int32) error {
	var evs []event
	if dy != 0 {
		evs = append(evs, event{evRel, relWheel, dy})
	}
	if dx != 0 {
		evs = append(evs, event{evRel, relHWheel, dx})
	}
	if len(evs) == 0 {
		return nil
	}
	return v.emit(evs...)
}

func (v *Virtual) Close() error {
	// Destroying the device releases anything the kernel still thinks is held.
	_ = ioctl(v.f.Fd(), uiDevDestroy, 0)
	return v.f.Close()
}

type event struct {
	typ, code uint16
	value     int32
}

// emit writes events plus the SYN_REPORT that makes the kernel dispatch them.
// Without the terminating SYN the events sit in the kernel unreported, which
// looks exactly like the device being broken.
func (v *Virtual) emit(evs ...event) error {
	buf := make([]byte, 0, (len(evs)+1)*eventSize)
	for _, e := range evs {
		buf = append(buf, encodeEvent(e.typ, e.code, e.value)...)
	}
	buf = append(buf, encodeEvent(evSyn, synReport, 0)...)

	if _, err := v.f.Write(buf); err != nil {
		return fmt.Errorf("inputdev: writing events: %w", err)
	}
	return nil
}

// encodeEvent lays out struct input_event. The leading timeval is left zero:
// the kernel timestamps input events itself and ignores what we supply.
func encodeEvent(typ, code uint16, value int32) []byte {
	b := make([]byte, eventSize)
	binary.LittleEndian.PutUint16(b[16:], typ)
	binary.LittleEndian.PutUint16(b[18:], code)
	binary.LittleEndian.PutUint32(b[20:], uint32(value))
	return b
}

// encodeSetup lays out struct uinput_setup: input_id, name, ff_effects_max.
func encodeSetup(name string) []byte {
	b := make([]byte, setupSize)
	binary.LittleEndian.PutUint16(b[0:], busUSB)
	binary.LittleEndian.PutUint16(b[2:], vendorLinux)
	binary.LittleEndian.PutUint16(b[4:], 0x0001) // product
	binary.LittleEndian.PutUint16(b[6:], 0x0001) // version

	// Leave the final byte NUL so an over-long name stays a valid C string
	// and cannot run into ff_effects_max.
	copy(b[8:8+nameSize-1], name)
	return b
}

func btnCode(b uint8) (uint16, bool) {
	switch b {
	case proto.BtnLeft:
		return btnLeft, true
	case proto.BtnRight:
		return btnRight, true
	case proto.BtnMiddle:
		return btnMiddle, true
	case proto.BtnSide:
		return btnSide, true
	case proto.BtnExtra:
		return btnExtra, true
	}
	return 0, false
}

func boolValue(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func ioctl(fd, req, arg uintptr) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, req, arg); errno != 0 {
		return errno
	}
	return nil
}

// ioctlPtr passes a struct to the kernel. p must point into a slice that stays
// alive across the call, which it does: the caller holds the backing array.
func ioctlPtr(fd, req uintptr, p *byte) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(p))); errno != 0 {
		return errno
	}
	return nil
}
