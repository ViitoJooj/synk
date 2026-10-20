//go:build windows

package inputdev

import (
	"syscall"
	"unsafe"
)

// Win32 bindings. Pure Go: user32 is reached through LazyDLL, so the whole
// tool still cross-compiles from Linux with a plain `GOOS=windows go build`.
var (
	user32 = syscall.NewLazyDLL("user32.dll")

	procSetWindowsHookExW    = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx  = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx       = user32.NewProc("CallNextHookEx")
	procGetMessageW          = user32.NewProc("GetMessageW")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procDispatchMessageW     = user32.NewProc("DispatchMessageW")
	procPostThreadMessageW   = user32.NewProc("PostThreadMessageW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procRegisterRawInputDevs = user32.NewProc("RegisterRawInputDevices")
	procGetRawInputData      = user32.NewProc("GetRawInputData")
	procClipCursor           = user32.NewProc("ClipCursor")
	procGetCursorPos         = user32.NewProc("GetCursorPos")
	procSetCursor            = user32.NewProc("SetCursor")
	procShowWindow           = user32.NewProc("ShowWindow")
	procSetLayeredWinAttrs   = user32.NewProc("SetLayeredWindowAttributes")
	procGetSystemMetrics     = user32.NewProc("GetSystemMetrics")
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentThreadId   = kernel32.NewProc("GetCurrentThreadId")
)

// Hook types and window messages, from winuser.h.
const (
	whKeyboardLL = 13
	whMouseLL    = 14

	wmKeyDown     = 0x0100
	wmKeyUp       = 0x0101
	wmSysKeyDown  = 0x0104
	wmSysKeyUp    = 0x0105
	wmMouseMove   = 0x0200
	wmMouseWheel  = 0x020A
	wmMouseHWheel = 0x020E
	wmInput       = 0x00FF
	wmQuit        = 0x0012
	wmClose       = 0x0010

	// llkhfExtended marks the keys that carry an E0 prefix on the wire.
	llkhfExtended = 0x01
	// llkhfInjected marks synthetic input. Forwarding it would feed our own
	// injection back into the link.
	llkhfInjected = 0x10
	llmhfInjected = 0x01

	hwndMessage = ^uintptr(2) // (HWND)-3

	ridevInputSink = 0x00000100
	rimTypeMouse   = 0
	ridInput       = 0x10000003

	mouseMoveRelative = 0x00
	mouseMoveAbsolute = 0x01

	// Button messages and the wheel unit are in winmap.go, which has no
	// build tag so its translation logic stays testable on Linux.

	// Button flags in RAWMOUSE.usButtonFlags.
	riMouseWheel  = 0x0400
	riMouseHWheel = 0x0800

	smCXScreen = 0
	smCYScreen = 1

	// wmStopCapture unwinds the message loop from another goroutine.
	wmStopCapture = 0x0400 + 1 // WM_USER + 1
)

// errClassAlreadyExists is ERROR_CLASS_ALREADY_EXISTS, what RegisterClassExW
// fails with on the second and later call: the class is already there, from
// the capture this process ran before.
const errClassAlreadyExists = syscall.Errno(1410)

type point struct{ X, Y int32 }

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

// kbdllhookstruct is KBDLLHOOKSTRUCT.
type kbdllhookstruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

// msllhookstruct is MSLLHOOKSTRUCT.
type msllhookstruct struct {
	Pt          point
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

// rawInputDevice is RAWINPUTDEVICE.
type rawInputDevice struct {
	UsagePage  uint16
	Usage      uint16
	Flags      uint32
	HwndTarget uintptr
}

// rawMouse is RAWMOUSE. On amd64 it is 24 bytes: usFlags plus two bytes of
// padding, then the button union, then the three ULONG/LONG fields.
type rawMouse struct {
	Flags            uint16
	_                uint16
	ButtonFlags      uint16
	ButtonData       int16
	RawButtons       uint32
	LastX            int32
	LastY            int32
	ExtraInformation uint32
}

// rawInputHeaderSize is sizeof(RAWINPUTHEADER) on amd64: dwType, dwSize,
// hDevice and wParam. The device payload begins immediately after it.
const rawInputHeaderSize = 24

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type rect struct{ Left, Top, Right, Bottom int32 }

func screenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(smCXScreen)
	h, _, _ := procGetSystemMetrics.Call(smCYScreen)
	return int(w), int(h)
}

func cursorPos() (int, int) {
	var p point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return int(p.X), int(p.Y)
}

// clipCursorTo pins the physical cursor to a one-pixel box, so the local
// pointer stays put while the far machine has focus. Passing nil releases it.
func clipCursorTo(r *rect) error {
	var arg uintptr
	if r != nil {
		arg = uintptr(unsafe.Pointer(r))
	}
	ret, _, err := procClipCursor.Call(arg)
	if ret == 0 {
		return err
	}
	return nil
}

// ScreenSize is the primary display's size in pixels, which is what the edge
// test measures against. Multi-monitor on the Windows side is out of scope for
// now: the handoff border is the edge of the primary display.
func ScreenSize() (int, int) { return screenSize() }

// MoveCursorTo places the physical cursor, which is how focus returning to this
// machine avoids landing on the very border it just came through.
//
// It works through ClipCursor rather than SetCursorPos, which is the obvious
// call and is not used here on purpose. Confining the pointer to a rectangle it
// is outside of moves it in, so a one-pixel rectangle at the destination places
// it there — and confinement is the one mechanism already proven to move the
// pointer on this machine, since it is what parks it in the corner. SetCursorPos
// was tried first and did nothing at all; the symptom was a cursor that
// reappeared in the corner it had been parked in instead of at the border it
// crossed, on every single handoff.
//
// The clip is released immediately afterwards, leaving the pointer free.
func MoveCursorTo(x, y int) error {
	r := rect{
		Left:   int32(x),
		Top:    int32(y),
		Right:  int32(x) + 1,
		Bottom: int32(y) + 1,
	}
	if err := clipCursorTo(&r); err != nil {
		return err
	}
	return clipCursorTo(nil)
}

// CursorPos is where the pointer is now, in primary-display pixels.
func CursorPos() (int, int) { return cursorPos() }

// Constants for the window that hides the pointer while the far machine has
// focus. See Capture.showBlanker for what they are doing together.
const (
	wsExLayered    = 0x00080000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080
	wsExNoActivate = 0x08000000
	wsPopup        = 0x80000000

	lwaAlpha = 0x00000002

	swHide           = 0
	swShowNoActivate = 4

	wmSetCursor = 0x0020
)

// hideCursorOver makes the cursor invisible while it is over hwnd's window.
//
// SetCursor(NULL) only affects the window under the pointer, which is why the
// blanking window has to cover the screen: there is no supported way for a
// process to hide the pointer everywhere at once. SetSystemCursor would do it,
// but it changes the cursor for the whole session and a crash would leave the
// machine with no pointer at all.
func hideCursorOver() {
	procSetCursor.Call(0)
}

func showWindow(hwnd uintptr, cmd int) {
	procShowWindow.Call(hwnd, uintptr(cmd))
}

// setWindowAlpha makes a layered window almost, but not quite, invisible.
//
// Fully transparent (alpha 0) windows stop receiving WM_SETCURSOR, so the one
// message this window exists to answer would never arrive. Alpha 1 of 255 is
// imperceptible and keeps it in the hit test.
func setWindowAlpha(hwnd uintptr, alpha byte) error {
	ret, _, err := procSetLayeredWinAttrs.Call(hwnd, 0, uintptr(alpha), lwaAlpha)
	if ret == 0 {
		return err
	}
	return nil
}
