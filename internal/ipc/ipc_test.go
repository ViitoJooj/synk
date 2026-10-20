package ipc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ViitoJooj/synk/internal/proto"
)

// fakeInjector records what the daemon was asked to inject.
type fakeInjector struct {
	mu      sync.Mutex
	keys    []keyCall
	buttons []buttonCall
	moves   [][2]int32
	scrolls [][2]int32
}

type keyCall struct {
	code    uint16
	pressed bool
}
type buttonCall struct {
	btn     uint8
	pressed bool
}

func (f *fakeInjector) Key(code uint16, pressed bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, keyCall{code, pressed})
	return nil
}

func (f *fakeInjector) Button(btn uint8, pressed bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buttons = append(f.buttons, buttonCall{btn, pressed})
	return nil
}

func (f *fakeInjector) MoveRel(dx, dy int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.moves = append(f.moves, [2]int32{dx, dy})
	return nil
}

func (f *fakeInjector) Scroll(dx, dy int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scrolls = append(f.scrolls, [2]int32{dx, dy})
	return nil
}

func (f *fakeInjector) snapshot() ([]keyCall, []buttonCall, [][2]int32, [][2]int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]keyCall(nil), f.keys...),
		append([]buttonCall(nil), f.buttons...),
		append([][2]int32(nil), f.moves...),
		append([][2]int32(nil), f.scrolls...)
}

// startServer brings up a server on a temp socket and returns its path.
func startServer(t *testing.T, inj Injector) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "daemon.sock")

	srv, err := NewServer(path, inj)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		srv.Close()
		<-done
	})
	return path
}

// waitFor polls until cond holds, so tests do not race the server goroutine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestKeyReachesTheInjector(t *testing.T) {
	inj := &fakeInjector{}
	c, err := Dial(startServer(t, inj))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if err := c.Key(30, true); err != nil {
		t.Fatalf("Key: %v", err)
	}

	waitFor(t, "the key to arrive", func() bool {
		keys, _, _, _ := inj.snapshot()
		return len(keys) == 1 && keys[0] == keyCall{30, true}
	})
}

func TestAllCommandKindsReachTheInjector(t *testing.T) {
	inj := &fakeInjector{}
	c, err := Dial(startServer(t, inj))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if err := c.MoveRel(7, -3); err != nil {
		t.Fatalf("MoveRel: %v", err)
	}
	if err := c.Button(proto.BtnRight, true); err != nil {
		t.Fatalf("Button: %v", err)
	}
	if err := c.Scroll(0, -1); err != nil {
		t.Fatalf("Scroll: %v", err)
	}

	waitFor(t, "every command", func() bool {
		_, buttons, moves, scrolls := inj.snapshot()
		return len(moves) == 1 && moves[0] == [2]int32{7, -3} &&
			len(buttons) == 1 && buttons[0] == buttonCall{proto.BtnRight, true} &&
			len(scrolls) == 1 && scrolls[0] == [2]int32{0, -1}
	})
}

// The dead-man switch. If the session process dies while the far machine is
// holding Shift, the daemon must let go — otherwise the laptop is left with a
// stuck modifier and no process to blame.
func TestDisconnectReleasesHeldKeys(t *testing.T) {
	inj := &fakeInjector{}
	c, err := Dial(startServer(t, inj))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	if err := c.Key(42, true); err != nil { // KEY_LEFTSHIFT down
		t.Fatalf("Key: %v", err)
	}
	if err := c.Key(30, true); err != nil { // KEY_A down
		t.Fatalf("Key: %v", err)
	}
	if err := c.Button(proto.BtnLeft, true); err != nil {
		t.Fatalf("Button: %v", err)
	}
	waitFor(t, "the presses to land", func() bool {
		keys, buttons, _, _ := inj.snapshot()
		return len(keys) == 2 && len(buttons) == 1
	})

	// Drop the connection the way a killed process would.
	c.Close()

	waitFor(t, "the daemon to release everything", func() bool {
		keys, buttons, _, _ := inj.snapshot()
		var heldKeys, heldButtons int
		for _, k := range keys {
			if k.pressed {
				heldKeys++
			} else {
				heldKeys--
			}
		}
		for _, b := range buttons {
			if b.pressed {
				heldButtons++
			} else {
				heldButtons--
			}
		}
		return heldKeys == 0 && heldButtons == 0
	})
}

// Modifiers must be released after ordinary keys, or the receiving side emits
// a stray character on the way out.
func TestDisconnectReleasesModifiersLast(t *testing.T) {
	inj := &fakeInjector{}
	c, err := Dial(startServer(t, inj))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	c.Key(42, true) // KEY_LEFTSHIFT
	c.Key(30, true) // KEY_A
	waitFor(t, "the presses to land", func() bool {
		keys, _, _, _ := inj.snapshot()
		return len(keys) == 2
	})
	c.Close()

	waitFor(t, "both releases", func() bool {
		keys, _, _, _ := inj.snapshot()
		return len(keys) == 4
	})

	keys, _, _, _ := inj.snapshot()
	releases := keys[2:]
	if releases[0].code != 30 {
		t.Errorf("first release is code %d, want KEY_A (30) before the modifier", releases[0].code)
	}
	if releases[1].code != 42 {
		t.Errorf("second release is code %d, want KEY_LEFTSHIFT (42) last", releases[1].code)
	}
}

// A key the session already released must not be released a second time.
func TestDisconnectDoesNotReleaseKeysAlreadyUp(t *testing.T) {
	inj := &fakeInjector{}
	c, err := Dial(startServer(t, inj))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	c.Key(30, true)
	c.Key(30, false)
	waitFor(t, "press and release", func() bool {
		keys, _, _, _ := inj.snapshot()
		return len(keys) == 2
	})
	c.Close()

	// Give the server a moment to do anything it was going to do.
	time.Sleep(200 * time.Millisecond)
	keys, _, _, _ := inj.snapshot()
	if len(keys) != 2 {
		t.Errorf("got %d key calls, want 2 — nothing was still held", len(keys))
	}
}

// The socket carries the right to inject keystrokes. Anyone outside the
// daemon's group must not be able to open it.
func TestSocketIsNotWorldAccessible(t *testing.T) {
	path := startServer(t, &fakeInjector{})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o007 != 0 {
		t.Errorf("socket mode is %#o, want no permissions for other", perm)
	}
	if perm := info.Mode().Perm(); perm != 0o660 {
		t.Errorf("socket mode is %#o, want 0660", perm)
	}
}

// A stale socket from a crashed daemon must not block the next start.
func TestServerReplacesAStaleSocket(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.sock")
	if err := os.WriteFile(path, nil, 0o660); err != nil {
		t.Fatalf("creating a stale socket: %v", err)
	}

	srv, err := NewServer(path, &fakeInjector{})
	if err != nil {
		t.Fatalf("NewServer over a stale socket: %v", err)
	}
	srv.Close()
}

func TestDialFailsWhenNoDaemonIsListening(t *testing.T) {
	_, err := Dial(filepath.Join(t.TempDir(), "absent.sock"))
	if err == nil {
		t.Fatal("Dial succeeded with no daemon listening")
	}
}

// A corrupt frame must cost the connection, not the daemon.
func TestServerSurvivesAMalformedFrame(t *testing.T) {
	inj := &fakeInjector{}
	path := startServer(t, inj)

	bad, err := dialRaw(path)
	if err != nil {
		t.Fatalf("dialRaw: %v", err)
	}
	// A frame whose type means nothing, then a truncated one.
	proto.WriteFrame(bad, proto.MsgType(200), []byte{1, 2, 3})
	bad.Write([]byte{0xff, 0xff})
	bad.Close()

	// The daemon must still serve the next client.
	good, err := Dial(path)
	if err != nil {
		t.Fatalf("Dial after a malformed client: %v", err)
	}
	defer good.Close()

	if err := good.Key(30, true); err != nil {
		t.Fatalf("Key after a malformed client: %v", err)
	}
	waitFor(t, "the daemon to keep serving", func() bool {
		keys, _, _, _ := inj.snapshot()
		return len(keys) >= 1
	})
}

func TestClientOnAClosedConnectionErrors(t *testing.T) {
	c, err := Dial(startServer(t, &fakeInjector{}))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	c.Close()

	if err := c.Key(30, true); err == nil {
		t.Error("Key on a closed client = nil, want an error")
	} else if errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want a connection error", err)
	}
}
