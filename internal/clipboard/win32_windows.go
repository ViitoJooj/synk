//go:build windows

package clipboard

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procSetClipboardData = user32.NewProc("SetClipboardData")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalFree   = kernel32.NewProc("GlobalFree")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	// The clipboard is a single global resource; another application can hold
	// it open for a moment while it finishes its own copy.
	openAttempts = 10
	openBackoff  = 10 * time.Millisecond
)

// Win32 reads and writes the Windows clipboard.
type Win32 struct{}

func NewWin32() *Win32 { return &Win32{} }

// open retries: OpenClipboard fails outright if any other process currently
// holds it, and that is routine rather than exceptional.
func open() error {
	var lastErr error
	for range openAttempts {
		ret, _, err := procOpenClipboard.Call(0)
		if ret != 0 {
			return nil
		}
		lastErr = err
		time.Sleep(openBackoff)
	}
	return fmt.Errorf("clipboard: could not open the Windows clipboard: %w", lastErr)
}

// Read returns the clipboard's text. A clipboard holding something that is not
// text is empty text, not an error.
func (w *Win32) Read() (string, error) {
	if err := open(); err != nil {
		return "", err
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", nil // empty, or holding an image or files
	}

	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return "", fmt.Errorf("clipboard: locking the clipboard buffer failed")
	}
	defer procGlobalUnlock.Call(h)

	return syscall.UTF16ToString(utf16Slice(p)), nil
}

// Write replaces the clipboard's contents.
func (w *Win32) Write(text string) error {
	encoded, err := syscall.UTF16FromString(text)
	if err != nil {
		return fmt.Errorf("clipboard: encoding text: %w", err)
	}

	if err := open(); err != nil {
		return err
	}
	defer procCloseClipboard.Call()

	if ret, _, err := procEmptyClipboard.Call(); ret == 0 {
		return fmt.Errorf("clipboard: emptying the clipboard: %w", err)
	}

	size := uintptr(len(encoded) * 2)
	h, _, err := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return fmt.Errorf("clipboard: allocating %d bytes: %w", size, err)
	}

	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("clipboard: locking the new buffer failed")
	}
	copy(osUint16s(p, len(encoded)), encoded)
	procGlobalUnlock.Call(h)

	if ret, _, err := procSetClipboardData.Call(cfUnicodeText, h); ret == 0 {
		// Ownership only transfers on success, so a failure leaves the block
		// ours to free.
		procGlobalFree.Call(h)
		return fmt.Errorf("clipboard: setting clipboard data: %w", err)
	}
	return nil
}

// osUint16s views memory Windows handed back from GlobalLock.
//
// go vet's unsafeptr check guards against a uintptr that came from a Go
// pointer, which the collector may move or free between the two conversions.
// This one came from GlobalLock: the block is owned by Windows, pinned for as
// long as the lock is held, and invisible to Go's collector. The conversion
// lives here alone so the reasoning does not have to be repeated.
func osUint16s(p uintptr, n int) []uint16 {
	return unsafe.Slice((*uint16)(unsafe.Pointer(p)), n)
}

// utf16Slice views a NUL-terminated UTF-16 string the OS handed us. The length
// is not known up front, so it is found by scanning for the terminator; the
// cap is the largest clipboard text this tool will look at.
func utf16Slice(p uintptr) []uint16 {
	const maxRunes = 1 << 20
	all := osUint16s(p, maxRunes)
	for i, v := range all {
		if v == 0 {
			return all[:i]
		}
	}
	return all
}
