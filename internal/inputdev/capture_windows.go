//go:build windows

package inputdev

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// EventKind tags what a captured Event carries.
type EventKind uint8

const (
	KindKey EventKind = iota
	KindButton
	KindScroll
	// KindMoveRel is a raw pointer delta, taken from Raw Input. It is what
	// drives the far cursor while the far machine has focus.
	KindMoveRel
	// KindCursorAt is the real on-screen cursor position, taken from the
	// low-level hook. Raw deltas are pre-acceleration and would drift from
	// where the pointer actually is, so edge detection uses this instead.
	KindCursorAt
	// KindEscape reports the Ctrl+Alt+F12 hatch.
	KindEscape
)

// Event is one captured input event in the neutral wire representation.
type Event struct {
	Kind    EventKind
	Code    uint16 // evdev keycode, or a proto.Btn* button
	Pressed bool
	X, Y    int32 // KindCursorAt
	DX, DY  int16 // KindMoveRel, KindScroll
}

// Capture is the Windows input source.
//
// Windows is the server in this setup: it owns the physical keyboard and mouse
// and forwards them whenever focus is on the Linux machine.
type Capture struct {
	events   chan Event
	suppress atomic.Bool
	dropped  atomic.Uint64
	threadID atomic.Uint32
	ready    chan error
	stopped  chan struct{}

	// Touched only from the hook thread.
	wheel   WheelAccumulator
	hWheel  WheelAccumulator
	escape  escapeDetector
	kbHook  uintptr
	msHook  uintptr
	hwnd    uintptr
	blanker uintptr // covers the screen to hide the pointer; 0 if unavailable
	screenW int32
}

// captureClassName is shared by the message-only window and the blanking
// window: they want the same procedure and the same NULL class cursor.
const captureClassName = "conn-capture"

// Only one Capture can exist per process: the Win32 hook callbacks are plain C
// function pointers with nowhere to carry a receiver.
var active atomic.Pointer[Capture]

// StartCapture installs the hooks and begins delivering events.
func StartCapture(buffer int) (*Capture, error) {
	if !active.CompareAndSwap(nil, new(Capture)) {
		return nil, fmt.Errorf("inputdev: a capture is already running")
	}

	c := &Capture{
		events:  make(chan Event, buffer),
		ready:   make(chan error, 1),
		stopped: make(chan struct{}),
	}
	active.Store(c)

	go c.run()

	if err := <-c.ready; err != nil {
		active.Store(nil)
		return nil, err
	}
	return c, nil
}

func (c *Capture) Events() <-chan Event { return c.events }

// Dropped counts events discarded because the consumer fell behind. Any
// non-zero value is a problem: a lost key-up leaves a key stuck on the far
// machine until the next RELEASE_ALL.
func (c *Capture) Dropped() uint64 { return c.dropped.Load() }

// SetSuppress swallows local input while the far machine has focus, so nothing
// leaks into whatever is on screen here, and takes the pointer out of sight.
//
// Out of sight means two things, deliberately layered. The pointer is parked in
// the bottom-right corner and pinned there, which needs nothing but ClipCursor
// and always works; and a screen-sized transparent window is raised over
// everything, which actually hides it. If that window could not be created the
// pointer is still in the corner rather than sitting in the middle of the
// screen, so the feature degrades instead of failing.
func (c *Capture) SetSuppress(on bool) {
	c.suppress.Store(on)

	if !on {
		_ = clipCursorTo(nil)
		if c.blanker != 0 {
			showWindow(c.blanker, swHide)
		}
		return
	}

	if c.blanker != 0 {
		showWindow(c.blanker, swShowNoActivate)
	}

	// One call parks and pins: confining the pointer to a rectangle it is
	// outside of moves it in, so a one-pixel rectangle in the corner both puts
	// it there and holds it there.
	w, h := screenSize()
	r := rect{Left: int32(w) - 1, Top: int32(h) - 1, Right: int32(w), Bottom: int32(h)}
	_ = clipCursorTo(&r)
}

func (c *Capture) Suppressing() bool { return c.suppress.Load() }

// Stop unwinds the message loop and releases the hooks.
func (c *Capture) Stop() {
	if id := c.threadID.Load(); id != 0 {
		procPostThreadMessageW.Call(uintptr(id), wmStopCapture, 0, 0)
	}
	<-c.stopped
	active.Store(nil)
}

// run owns the hook thread. Win32 delivers low-level hook callbacks on the
// thread that installed them, and only while that thread pumps messages, so
// the thread is locked for the lifetime of the capture.
func (c *Capture) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(c.stopped)

	tid, _, _ := procGetCurrentThreadId.Call()
	c.threadID.Store(uint32(tid))

	w, _ := screenSize()
	c.screenW = int32(w)

	if err := c.install(); err != nil {
		c.ready <- err
		return
	}
	defer c.uninstall()
	c.ready <- nil

	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		// GetMessage returns 0 on WM_QUIT and -1 on error.
		if int32(ret) <= 0 {
			return
		}
		if m.Message == wmStopCapture {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (c *Capture) install() error {
	hInst, _, _ := procGetModuleHandleW.Call(0)

	if err := c.createMessageWindow(hInst); err != nil {
		return err
	}
	if err := c.registerRawMouse(); err != nil {
		return err
	}
	// Not fatal: without it the pointer is merely parked rather than hidden.
	c.blanker = c.createBlanker(hInst)

	var err error
	c.kbHook, _, err = procSetWindowsHookExW.Call(whKeyboardLL, syscall.NewCallback(keyboardProc), hInst, 0)
	if c.kbHook == 0 {
		return fmt.Errorf("inputdev: installing the keyboard hook: %w", err)
	}
	c.msHook, _, err = procSetWindowsHookExW.Call(whMouseLL, syscall.NewCallback(mouseProc), hInst, 0)
	if c.msHook == 0 {
		return fmt.Errorf("inputdev: installing the mouse hook: %w", err)
	}
	return nil
}

func (c *Capture) uninstall() {
	_ = clipCursorTo(nil)
	if c.blanker != 0 {
		procDestroyWindow.Call(c.blanker)
	}
	if c.msHook != 0 {
		procUnhookWindowsHookEx.Call(c.msHook)
	}
	if c.kbHook != 0 {
		procUnhookWindowsHookEx.Call(c.kbHook)
	}
	if c.hwnd != 0 {
		procDestroyWindow.Call(c.hwnd)
	}
}

// createMessageWindow makes a message-only window. Raw Input has to be
// delivered somewhere, and a message-only window never appears on screen.
func (c *Capture) createMessageWindow(hInst uintptr) error {
	className, err := syscall.UTF16PtrFromString(captureClassName)
	if err != nil {
		return err
	}
	wc := wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   syscall.NewCallback(windowProc),
		Instance:  hInst,
		ClassName: className,
	}
	// ERROR_CLASS_ALREADY_EXISTS is expected, not a failure: the class is
	// process-wide and outlives a Capture, so every reconnect after the first
	// (synk <peer>, or 'connect' from the window) finds it still registered
	// from the one before.
	if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 && err != errClassAlreadyExists {
		return fmt.Errorf("inputdev: registering the window class: %w", err)
	}

	c.hwnd, _, err = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0,
		hwndMessage, 0, hInst, 0,
	)
	if c.hwnd == 0 {
		return fmt.Errorf("inputdev: creating the message window: %w", err)
	}
	return nil
}

// createBlanker makes the window that hides the pointer: screen-sized, topmost,
// layered at an alpha nobody can see, and never activated.
//
// It reuses the capture window's class, whose cursor is NULL, and the shared
// window procedure answers WM_SETCURSOR by hiding the pointer. It is created
// hidden and only shown while the far machine has focus.
//
// Because it belongs to this process it disappears the instant the process
// does, which is the property that makes this approach safe: there is no state
// left behind to repair, however the program ends.
func (c *Capture) createBlanker(hInst uintptr) uintptr {
	className, err := syscall.UTF16PtrFromString(captureClassName)
	if err != nil {
		return 0
	}
	w, h := screenSize()

	hwnd, _, _ := procCreateWindowExW.Call(
		wsExLayered|wsExTopmost|wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		wsPopup,
		0, 0, uintptr(w), uintptr(h),
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return 0
	}
	if err := setWindowAlpha(hwnd, 1); err != nil {
		procDestroyWindow.Call(hwnd)
		return 0
	}
	return hwnd
}

// registerRawMouse asks for raw pointer deltas. RIDEV_INPUTSINK keeps them
// coming while another application is in the foreground, which is exactly the
// case whenever the far machine has focus.
func (c *Capture) registerRawMouse() error {
	dev := rawInputDevice{
		UsagePage:  0x01, // generic desktop
		Usage:      0x02, // mouse
		Flags:      ridevInputSink,
		HwndTarget: c.hwnd,
	}
	ret, _, err := procRegisterRawInputDevs.Call(
		uintptr(unsafe.Pointer(&dev)), 1, unsafe.Sizeof(dev))
	if ret == 0 {
		return fmt.Errorf("inputdev: registering for raw mouse input: %w", err)
	}
	return nil
}

// emit is called from the hook thread and must never block: a blocked hook
// freezes input for the whole desktop.
func (c *Capture) emit(e Event) {
	select {
	case c.events <- e:
	default:
		c.dropped.Add(1)
	}
}

// ---- Win32 callbacks ----

func windowProc(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	switch message {
	case wmInput:
		if c := active.Load(); c != nil {
			c.onRawInput(lparam)
		}

	case wmSetCursor:
		// Only the blanking window is ever under the pointer, and this is the
		// whole reason it exists: answering here, rather than letting
		// DefWindowProc fall through to the class cursor, is what makes the
		// pointer invisible.
		hideCursorOver()
		return 1
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wparam, lparam)
	return ret
}

// onRawInput reads one RAWINPUT and emits the pointer delta it carries.
func (c *Capture) onRawInput(lparam uintptr) {
	var buf [64]byte
	size := uint32(len(buf))
	ret, _, _ := procGetRawInputData.Call(
		lparam, ridInput,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		rawInputHeaderSize,
	)
	if int32(ret) < 0 {
		return
	}
	if kind := *(*uint32)(unsafe.Pointer(&buf[0])); kind != rimTypeMouse {
		return
	}

	m := (*rawMouse)(unsafe.Pointer(&buf[rawInputHeaderSize]))
	// Absolute reports come from tablets and remote-desktop sessions; there
	// is no meaningful delta to forward, so they are ignored.
	if m.Flags&mouseMoveAbsolute != 0 {
		return
	}
	if m.LastX == 0 && m.LastY == 0 {
		return
	}
	c.emit(Event{Kind: KindMoveRel, DX: clampInt16(m.LastX), DY: clampInt16(m.LastY)})
}

func keyboardProc(code int32, wparam, lparam uintptr) uintptr {
	c := active.Load()
	if code < 0 || c == nil {
		return callNext(code, wparam, lparam)
	}

	k := osStruct[kbdllhookstruct](lparam)

	// Never re-capture our own synthetic input, or the link feeds itself.
	if k.Flags&llkhfInjected != 0 {
		return callNext(code, wparam, lparam)
	}

	pressed := wparam == wmKeyDown || wparam == wmSysKeyDown
	evdev, ok := scanToEvdev(k.ScanCode, k.Flags&llkhfExtended != 0)
	if !ok {
		return callNext(code, wparam, lparam)
	}

	// The escape hatch, and it releases suppression right here rather than
	// emitting an event and trusting the session loop to act on it.
	//
	// A wedged or dead session loop is precisely the situation this hotkey
	// exists for; routing the rescue through the thing that is broken would
	// make it useless exactly when it is needed. The event is still emitted so
	// the session can update its own view, but the mouse and keyboard are
	// already free by then.
	if c.escape.Key(evdev, pressed) {
		c.SetSuppress(false)
		c.emit(Event{Kind: KindEscape})
		return callNext(code, wparam, lparam)
	}

	c.emit(Event{Kind: KindKey, Code: evdev, Pressed: pressed})

	if c.suppress.Load() {
		return 1 // swallow: this keystroke belongs to the far machine
	}
	return callNext(code, wparam, lparam)
}

func mouseProc(code int32, wparam, lparam uintptr) uintptr {
	c := active.Load()
	if code < 0 || c == nil {
		return callNext(code, wparam, lparam)
	}

	m := osStruct[msllhookstruct](lparam)
	if m.Flags&llmhfInjected != 0 {
		return callNext(code, wparam, lparam)
	}

	message := uint32(wparam)
	switch {
	case message == wmMouseMove:
		// Position, not delta: this is the real post-acceleration cursor, and
		// it is what edge detection needs. Deltas arrive via Raw Input.
		// Only worth reporting while the pointer is local: once the far machine
		// has focus the cursor is pinned and the session ignores it.
		if !c.suppress.Load() {
			c.emit(Event{Kind: KindCursorAt, X: m.Pt.X, Y: m.Pt.Y})
		}

	case message == wmMouseWheel:
		if n := c.wheel.Add(int16(m.MouseData >> 16)); n != 0 {
			c.emit(Event{Kind: KindScroll, DY: int16(n)})
		}

	case message == wmMouseHWheel:
		if n := c.hWheel.Add(int16(m.MouseData >> 16)); n != 0 {
			c.emit(Event{Kind: KindScroll, DX: int16(n)})
		}

	default:
		if btn, pressed, ok := mouseMessageToButton(message, m.MouseData); ok {
			c.emit(Event{Kind: KindButton, Code: uint16(btn), Pressed: pressed})
		}
	}

	if c.suppress.Load() {
		return 1
	}
	return callNext(code, wparam, lparam)
}

// osStruct reinterprets a pointer Windows handed us in an LPARAM.
//
// go vet's unsafeptr check exists because a uintptr that came from a Go
// pointer can be invalidated by the collector between the two conversions.
// This one never came from Go: Windows owns the memory, keeps it alive for the
// duration of the callback, and the collector has no idea it exists. The
// conversion is confined to this one function so the reasoning lives in one
// place.
func osStruct[T any](lparam uintptr) *T {
	return (*T)(unsafe.Pointer(lparam))
}

func callNext(code int32, wparam, lparam uintptr) uintptr {
	ret, _, _ := procCallNextHookEx.Call(0, uintptr(code), wparam, lparam)
	return ret
}

// clampInt16 keeps an implausibly large delta from wrapping when it is packed
// onto the wire.
func clampInt16(v int32) int16 {
	switch {
	case v > 32767:
		return 32767
	case v < -32768:
		return -32768
	}
	return int16(v)
}
