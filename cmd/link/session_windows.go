//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"wfkit-link/internal/clipboard"
	"wfkit-link/internal/focus"
	"wfkit-link/internal/inputdev"
	"wfkit-link/internal/proto"
	"wfkit-link/internal/transport"
)

const (
	// captureBuffer is deliberately generous. Dropping an event here means a
	// lost key-up, so there should be a lot of slack before that can happen.
	captureBuffer = 4096

	// Reconnection backs off from this to maxBackoff. The peer being asleep is
	// the normal case, not an error worth hammering.
	minBackoff = 1 * time.Second
	maxBackoff = 30 * time.Second

	clipboardPoll     = 1 * time.Second
	clipboardMaxBytes = 256 * 1024

	// statsEvery paces the --verbose counters. Often enough to watch while
	// moving the mouse, rare enough not to bury the messages that matter.
	statsEvery = 5 * time.Second
)

var sessionVerbose bool

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Capture this machine's keyboard and mouse and drive the peer with them",
	Long: `Run the sending half of wfkit-link.

This machine owns the physical keyboard and mouse. While the pointer is on this
screen nothing changes; when it reaches the border the peer sits beyond, input
is swallowed here and forwarded there instead.

It dials the peer rather than waiting to be dialled, and needs no configuration
file: the peer is found on the tailnet, and the screen layout arrives in its
greeting. Nothing to copy onto this machine but the program itself.

Ctrl+Alt+F12 always returns control to this machine, whatever state the link is
in.`,
	Args: cobra.NoArgs,
	RunE: runSession,
}

func init() {
	sessionCmd.Flags().BoolVar(&sessionVerbose, "verbose", false,
		"report captured-event counters, to tell a dead hook from a quiet link")
	rootCmd.AddCommand(sessionCmd)
}

func runSession(cmd *cobra.Command, _ []string) error {
	capture, err := inputdev.StartCapture(captureBuffer)
	if err != nil {
		return err
	}
	defer capture.Stop()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "wfkit-link is capturing this keyboard and mouse; Ctrl+Alt+F12 always takes them back")

	backoff := minBackoff
	for ctx.Err() == nil {
		err := connectAndServe(ctx, out, capture)
		if ctx.Err() != nil {
			return nil
		}

		fmt.Fprintf(out, "link down: %v; retrying in %s\n", err, backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, maxBackoff)
		// A connection that lasted is not evidence of a persistent fault, so
		// the next outage starts from a short wait again.
		if errors.Is(err, errLinkWasEstablished) {
			backoff = minBackoff
		}
	}
	return nil
}

// errLinkWasEstablished marks an outage that followed a working connection, so
// the backoff can reset rather than punish a link that merely dropped.
var errLinkWasEstablished = errors.New("link had been established")

func connectAndServe(ctx context.Context, out io.Writer, capture *inputdev.Capture) error {
	ts := transport.NewTailscale()
	peer, err := resolvePeer(ts, nil)
	if err != nil {
		return err
	}
	if !peer.Online {
		return fmt.Errorf("%s is offline", peer.Host)
	}

	conn, err := transport.Dial(peer.IP, portFor(nil))
	if err != nil {
		return err
	}
	defer conn.Close()

	// The peer's edge, not ours: it names the border of *its* screen that this
	// machine sits beyond, so our own handoff border is the opposite one.
	peerEdge, err := transport.ClientHello(conn)
	if err != nil {
		return err
	}

	w, h := inputdev.ScreenSize()
	s := &winSession{
		conn:    &lockedConn{conn: conn},
		capture: capture,
		edge:    peerEdge.Opposite(),
		out:     out,
		screenW: w,
		screenH: h,
		peerIP:  peer.IP,
		port:    portFor(nil),
	}

	fmt.Fprintf(out, "connected to %s (%s); this screen is %dx%d, its screen is beyond the %s edge\n",
		peer.Host, peer.IP, w, h, s.edge)

	if err := servePeer(ctx, s, conn); err != nil {
		return fmt.Errorf("%w: %v", errLinkWasEstablished, err)
	}
	return errLinkWasEstablished
}

// servePeer runs one connection to completion. However it ends — cleanly, by
// error, or by the process being told to stop — suppression is lifted, so a
// failure can never leave this machine's own keyboard swallowed.
func servePeer(ctx context.Context, s *winSession, conn net.Conn) error {
	defer s.capture.SetSuppress(false)

	loopCtx, cancel := context.WithCancel(ctx)

	incoming := make(chan frame, 64)
	var wg sync.WaitGroup

	// One defer with an explicit order, not several relying on LIFO.
	//
	// Getting this backwards deadlocks: waiting on the goroutines before
	// cancelling their context means the clipboard watcher never wakes, the
	// wait never returns, and the deferred SetSuppress above never runs — which
	// leaves this machine's own mouse pinned by ClipCursor with no way out.
	defer func() {
		cancel()                         // wake the clipboard watcher
		conn.SetReadDeadline(time.Now()) // unblock the frame reader
		drain(incoming)                  // let a blocked send finish
		wg.Wait()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(incoming)
		readFrames(conn, incoming)
	}()

	if clipboardEnabled(nil) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.watchClipboard(loopCtx)
		}()
	}

	// Audio rides its own UDP socket, dialled from this side so the peer
	// learns where to send and no firewall rule is needed here.
	wg.Add(1)
	go func() {
		defer wg.Done()
		runPlayback(loopCtx, printTo(s.out), s.peerIP, s.port)
	}()

	return s.run(loopCtx, incoming)
}

type frame struct {
	typ     proto.MsgType
	payload []byte
}

// drain empties the frame channel so a reader blocked on a full buffer can
// finish and return, rather than holding the shutdown open.
func drain(ch <-chan frame) {
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

func readFrames(conn net.Conn, out chan<- frame) {
	for {
		typ, payload, err := proto.ReadFrame(conn)
		if err != nil {
			return
		}
		out <- frame{typ, payload}
	}
}

// lockedConn serialises writes: the event loop and the clipboard watcher both
// send, and interleaved frames would desynchronise the peer.
type lockedConn struct {
	mu   sync.Mutex
	conn net.Conn
}

func (c *lockedConn) send(typ proto.MsgType, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return proto.WriteFrame(c.conn, typ, payload)
}

type winSession struct {
	conn    *lockedConn
	capture *inputdev.Capture
	out     io.Writer

	// edge is the border of THIS screen the peer sits beyond, derived from what
	// the peer told us about its own layout. Deriving it rather than reading it
	// from a second config file is what makes the two sides impossible to
	// disagree with each other.
	edge proto.Edge

	screenW, screenH int
	remoteHasFocus   bool

	// Where the peer is, so the audio socket can be dialled to the same place
	// the control connection went.
	peerIP string
	port   int

	captured uint64

	clip     *clipboard.Win32
	clipSync *clipboard.Sync
}

// run is the single owner of focus state: every change flows through this one
// loop, so there is no lock to get wrong.
func (s *winSession) run(ctx context.Context, incoming <-chan frame) error {
	events := s.capture.Events()

	stats := newTicker(sessionVerbose, statsEvery)
	defer stats.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-stats.C:
			s.reportStats()

		case f, ok := <-incoming:
			if !ok {
				return errors.New("peer closed the connection")
			}
			s.handleFrame(f)

		case e := <-events:
			s.captured++
			if err := s.handleEvent(e); err != nil {
				return err
			}
		}
	}
}

// reportStats separates the two ways "nothing happens" presents: a counter that
// climbs while the pointer moves means capture works and the fault is further
// on, while one stuck at zero means the hooks are seeing nothing at all.
func (s *winSession) reportStats() {
	dropped := s.capture.Dropped()
	fmt.Fprintf(s.out, "captured %d events, dropped %d, focus %s\n",
		s.captured, dropped, focusWord(s.remoteHasFocus))
	if dropped > 0 {
		// Any drop at all is a problem: the lost event may have been a key-up,
		// which leaves a key held down on the far machine.
		fmt.Fprintln(s.out, "  dropped events mean the link cannot keep up; a key may stick")
	}
}

func focusWord(remote bool) string {
	if remote {
		return "on the peer"
	}
	return "here"
}

func (s *winSession) handleFrame(f frame) {
	switch f.typ {
	case proto.MsgFocusLeave:
		// The peer's cursor came back out of its far side. The payload says
		// where along our border it should reappear.
		edge, ratio := s.edge, uint16(0)
		if e, err := proto.DecodeFocusEnter(f.payload); err == nil && e.Edge.Valid() {
			edge, ratio = e.Edge, e.Ratio
		} else {
			ratio = s.currentRatio(edge)
		}
		s.takeFocusBack("peer handed focus back", edge, ratio)

	case proto.MsgPing:
		_ = s.conn.send(proto.MsgPong, nil)

	case proto.MsgClipData:
		s.applyClipboard(string(f.payload))
	}
}

func (s *winSession) handleEvent(e inputdev.Event) error {
	// The escape hatch outranks everything, including a wedged link.
	if e.Kind == inputdev.KindEscape {
		if s.remoteHasFocus {
			s.takeFocusBack("Ctrl+Alt+F12 pressed", s.edge, s.currentRatio(s.edge))
			// Tell the peer to let go of whatever it is holding for us.
			_ = s.conn.send(proto.MsgReleaseAll, nil)
		}
		return nil
	}

	if !s.remoteHasFocus {
		// While focus is local the only thing worth watching is whether the
		// pointer has reached the border. Everything else is the user working
		// on this machine and is none of our business.
		if e.Kind == inputdev.KindCursorAt {
			return s.maybeHandOff(int(e.X), int(e.Y))
		}
		return nil
	}

	switch e.Kind {
	case inputdev.KindKey:
		return s.conn.send(proto.MsgKey,
			proto.KeyEvent{Code: e.Code, Pressed: e.Pressed}.Encode())

	case inputdev.KindButton:
		return s.conn.send(proto.MsgMouseBtn,
			proto.MouseBtn{Button: uint8(e.Code), Pressed: e.Pressed}.Encode())

	case inputdev.KindScroll:
		return s.conn.send(proto.MsgScroll,
			proto.Scroll{DX: e.DX, DY: e.DY}.Encode())

	case inputdev.KindMoveRel:
		return s.conn.send(proto.MsgMouseRel,
			proto.MouseRel{DX: e.DX, DY: e.DY}.Encode())
	}
	return nil
}

// maybeHandOff gives the peer control when the pointer reaches the shared edge.
func (s *winSession) maybeHandOff(x, y int) error {
	reached, ratio := focus.AtEdge(x, y, s.screenW, s.screenH, s.edge)
	if !reached {
		return nil
	}

	// Suppress first: any event between the decision and the pin would
	// otherwise land on this machine.
	s.capture.SetSuppress(true)
	s.remoteHasFocus = true
	fmt.Fprintln(s.out, "focus handed to the peer")

	return s.conn.send(proto.MsgFocusEnter, proto.FocusEnter{
		Edge:  s.edge.Opposite(),
		Ratio: ratio,
	}.Encode())
}

// takeFocusBack returns the pointer to this machine and puts it just inside the
// border it came back through.
//
// The reposition is not cosmetic. While the peer had focus the cursor was
// pinned by ClipCursor to a one-pixel box exactly on the border; releasing the
// pin without moving it means the very next mouse movement reports a position
// still on the border, focus.AtEdge fires again, and control bounces straight
// back to the peer. The far side has always avoided this with an inset — this
// is the same inset, on this side.
func (s *winSession) takeFocusBack(why string, edge proto.Edge, ratio uint16) {
	if !s.remoteHasFocus {
		return
	}
	// Order matters: ClipCursor confines SetCursorPos too, so the pin has to go
	// before the warp or the warp does nothing.
	s.capture.SetSuppress(false)
	s.remoteHasFocus = false

	x, y := focus.EnterPoint(edge, ratio, s.screenW, s.screenH)
	if err := inputdev.MoveCursorTo(x, y); err != nil {
		fmt.Fprintf(s.out, "could not reposition the cursor: %v\n", err)
	}
	fmt.Fprintf(s.out, "focus back on this machine: %s\n", why)
}

// currentRatio is where the pointer sits along an edge right now, for the paths
// that have no ratio from the peer to go on.
func (s *winSession) currentRatio(edge proto.Edge) uint16 {
	x, y := inputdev.CursorPos()
	return focus.RatioAlong(x, y, s.screenW, s.screenH, edge)
}

// newTicker returns a ticker that never fires when the feature is off, so the
// select arm needs no special case.
func newTicker(on bool, d time.Duration) *time.Ticker {
	t := time.NewTicker(d)
	if !on {
		t.Stop()
	}
	return t
}

// watchClipboard mirrors the Linux side: poll, and send only genuine changes.
func (s *winSession) watchClipboard(ctx context.Context) {
	s.clip = clipboard.NewWin32()
	s.clipSync = clipboard.NewSync(clipboardMaxBytes)

	if text, err := s.clip.Read(); err == nil {
		s.clipSync.LocalChanged(text) // seed, so existing content is not pushed
	}

	ticker := time.NewTicker(clipboardPoll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		text, err := s.clip.Read()
		if err != nil {
			continue
		}
		if send, _ := s.clipSync.LocalChanged(text); !send {
			continue
		}
		if err := s.conn.send(proto.MsgClipData, []byte(text)); err != nil {
			return
		}
	}
}

func (s *winSession) applyClipboard(text string) {
	if s.clipSync == nil || s.clip == nil {
		return
	}
	if !s.clipSync.RemoteReceived(text) {
		return
	}
	if err := s.clip.Write(text); err != nil {
		fmt.Fprintf(s.out, "clipboard write failed: %v\n", err)
	}
}
