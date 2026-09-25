//go:build linux

package agent

import (
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ViitoJooj/synk/internal/focus"
	"github.com/ViitoJooj/synk/internal/proto"
)

// fakeInjector records what the session asked the daemon to do, so the whole
// receive path can be exercised without root or /dev/uinput.
type fakeInjector struct {
	mu   sync.Mutex
	log  []string
	fail error
}

func (f *fakeInjector) record(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, s)
	return f.fail
}

func (f *fakeInjector) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

func (f *fakeInjector) Key(code uint16, pressed bool) error {
	return f.record(action("key", int(code), pressed))
}
func (f *fakeInjector) Button(btn uint8, pressed bool) error {
	return f.record(action("button", int(btn), pressed))
}
func (f *fakeInjector) MoveRel(dx, dy int32) error { return f.record(pair("move", int(dx), int(dy))) }
func (f *fakeInjector) Scroll(dx, dy int32) error  { return f.record(pair("scroll", int(dx), int(dy))) }
func (f *fakeInjector) ReleaseAll() error          { return f.record("releaseall") }

func action(kind string, code int, pressed bool) string {
	state := "up"
	if pressed {
		state = "down"
	}
	return fmt.Sprintf("%s %d %s", kind, code, state)
}

func pair(kind string, a, b int) string { return fmt.Sprintf("%s %d %d", kind, a, b) }

// startSession wires a session to one end of a pipe and runs its read loop.
// The returned conn is the peer's end, to write frames into and read from.
func startSession(t *testing.T, screenW, screenH int, edge proto.Edge) (net.Conn, *fakeInjector, <-chan error) {
	t.Helper()

	local, remote := net.Pipe()
	inj := &fakeInjector{}

	fsm, err := focus.New(screenW, screenH, edge)
	if err != nil {
		t.Fatalf("focus.New: %v", err)
	}
	// The peer owns the keyboard until it hands it over, exactly as a real
	// connection starts.
	fsm.Leave()

	s := &session{
		conn:   &lockedConn{conn: local},
		daemon: inj,
		fsm:    fsm,
		out:    io.Discard,
		w:      screenW,
		h:      screenH,
	}

	done := make(chan error, 1)
	go func() { done <- s.serve() }()

	t.Cleanup(func() {
		remote.Close()
		local.Close()
	})
	return remote, inj, done
}

func write(t *testing.T, conn net.Conn, typ proto.MsgType, payload []byte) {
	t.Helper()
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := proto.WriteFrame(conn, typ, payload); err != nil {
		t.Fatalf("writing %v: %v", typ, err)
	}
}

// The whole point of the receiving half: frames arrive, injection happens.
func TestSessionInjectsWhatThePeerSends(t *testing.T) {
	conn, inj, done := startSession(t, 3840, 1080, proto.EdgeRight)

	write(t, conn, proto.MsgFocusEnter, proto.FocusEnter{Edge: proto.EdgeRight, Ratio: 32768}.Encode())
	write(t, conn, proto.MsgKey, proto.KeyEvent{Code: 42, Pressed: true}.Encode())
	write(t, conn, proto.MsgKey, proto.KeyEvent{Code: 30, Pressed: true}.Encode())
	write(t, conn, proto.MsgKey, proto.KeyEvent{Code: 30, Pressed: false}.Encode())
	write(t, conn, proto.MsgMouseBtn, proto.MouseBtn{Button: proto.BtnLeft, Pressed: true}.Encode())
	write(t, conn, proto.MsgScroll, proto.Scroll{DY: -1}.Encode())
	// Away from the shared border: a push towards it would cross and hand
	// focus back, which is the next test's business.
	write(t, conn, proto.MsgMouseRel, proto.MouseRel{DX: -5, DY: -3}.Encode())

	conn.Close()
	waitFor(t, done)

	want := []string{
		enterMove(540),
		"key 42 down",
		"key 30 down",
		"key 30 up",
		"button 0 down",
		"scroll 0 -1",
		"move -5 -3",
	}
	assertCalls(t, inj.calls(), want)
}

// Crossing back out of the far border is what returns control, and letting go
// of held keys first is what stops one being left down here.
func TestSessionHandsFocusBackWhenTheCursorLeaves(t *testing.T) {
	conn, inj, done := startSession(t, 3840, 1080, proto.EdgeRight)

	write(t, conn, proto.MsgFocusEnter, proto.FocusEnter{Edge: proto.EdgeRight, Ratio: 32768}.Encode())
	// Enter places the cursor two pixels inside the right border, so a small
	// push to the right is enough to leave.
	write(t, conn, proto.MsgMouseRel, proto.MouseRel{DX: 10}.Encode())

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	typ, payload, err := proto.ReadFrame(conn)
	if err != nil {
		t.Fatalf("reading the handback: %v", err)
	}
	if typ != proto.MsgFocusLeave {
		t.Fatalf("got frame type %v, want MsgFocusLeave", typ)
	}

	f, err := proto.DecodeFocusEnter(payload)
	if err != nil {
		t.Fatalf("decoding the handback: %v", err)
	}
	// Leaving through our right side arrives on the peer's left.
	if f.Edge != proto.EdgeLeft {
		t.Errorf("handback edge = %s, want left", f.Edge)
	}

	conn.Close()
	waitFor(t, done)

	// Letting go comes before parking: a key still held while the pointer is
	// flung into the corner would stay held.
	assertCalls(t, inj.calls(), []string{
		enterMove(540),
		"move 10 0",
		"releaseall",
		parkMove(),
	})
}

// Focus arriving from the peer must never be trusted blindly: an edge byte is
// used to index screen geometry.
func TestSessionRejectsAnImpossibleEdge(t *testing.T) {
	conn, _, done := startSession(t, 3840, 1080, proto.EdgeRight)

	write(t, conn, proto.MsgFocusEnter, []byte{99, 0, 0})

	if err := waitFor(t, done); err == nil {
		t.Fatal("an out-of-range edge was accepted")
	}
}

// A peer that loses focus on its own initiative has to leave nothing pressed.
func TestSessionReleasesEverythingWhenFocusLeaves(t *testing.T) {
	conn, inj, done := startSession(t, 3840, 1080, proto.EdgeRight)

	write(t, conn, proto.MsgFocusEnter, proto.FocusEnter{Edge: proto.EdgeRight, Ratio: 0}.Encode())
	write(t, conn, proto.MsgKey, proto.KeyEvent{Code: 42, Pressed: true}.Encode())
	write(t, conn, proto.MsgFocusLeave, nil)

	conn.Close()
	waitFor(t, done)

	assertCalls(t, inj.calls(), []string{
		enterMove(0),
		"key 42 down",
		"releaseall",
		parkMove(),
	})
}

func TestSessionAnswersPing(t *testing.T) {
	conn, _, done := startSession(t, 3840, 1080, proto.EdgeRight)

	write(t, conn, proto.MsgPing, nil)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	typ, _, err := proto.ReadFrame(conn)
	if err != nil {
		t.Fatalf("reading the pong: %v", err)
	}
	if typ != proto.MsgPong {
		t.Errorf("got %v, want MsgPong", typ)
	}

	conn.Close()
	waitFor(t, done)
}

// A peer on a newer build will send types this one has never heard of, and the
// link has to survive that rather than drop.
func TestSessionIgnoresUnknownFrameTypes(t *testing.T) {
	conn, inj, done := startSession(t, 3840, 1080, proto.EdgeRight)

	write(t, conn, proto.MsgType(200), []byte("from the future"))
	write(t, conn, proto.MsgFocusEnter, proto.FocusEnter{Edge: proto.EdgeRight}.Encode())
	write(t, conn, proto.MsgKey, proto.KeyEvent{Code: 1, Pressed: true}.Encode())

	conn.Close()
	waitFor(t, done)

	assertCalls(t, inj.calls(), []string{enterMove(0), "key 1 down"})
}

// The moves expected on a 3840x1080 desktop whose peer sits beyond the right
// border, written out rather than computed so the test states the numbers
// independently of the code that produces them.
//
// Parking overshoots the bottom-right corner and is clamped to (3839, 1079).
// Entering is then measured from there to two pixels inside the border, at the
// height the ratio names — and both are ONE move, which is the property that
// matters: two moves arriving together are summed by the compositor and clamp
// as their total, which put the pointer on the far edge of the screen.
func enterMove(y int) string { return pair("move", -2, y-1079) }

func parkMove() string { return pair("move", 7680, 2160) }

func waitFor(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("the session loop never returned")
		return nil
	}
}

func assertCalls(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls = %v, want %v", got, want)
		}
	}
}

// The complaint this fixes: crossing over from the PC put the cursor at the top
// of the laptop screen rather than at the height it crossed at.
//
// The cause was that Enter moved only the model. The pointer stayed wherever it
// had last been left, so the two disagreed from the first frame — and the
// handback then fired at a position that had nothing to do with what was on
// screen.
func TestSessionPlacesTheCursorAtTheHeightItCrossedAt(t *testing.T) {
	tests := []struct {
		ratio uint16
		wantY int
	}{
		{0, 0},        // the very top of the border
		{32768, 540},  // halfway down
		{65535, 1079}, // the very bottom
	}

	for _, tt := range tests {
		conn, inj, done := startSession(t, 3840, 1080, proto.EdgeRight)

		write(t, conn, proto.MsgFocusEnter,
			proto.FocusEnter{Edge: proto.EdgeRight, Ratio: tt.ratio}.Encode())

		conn.Close()
		waitFor(t, done)

		// Two pixels inside the border, at the matching height, in one move.
		assertCalls(t, inj.calls(), []string{enterMove(tt.wantY)})
	}
}
