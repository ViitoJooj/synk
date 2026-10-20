//go:build windows

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/ViitoJooj/synk/internal/clipboard"
	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/focus"
	"github.com/ViitoJooj/synk/internal/inputdev"
	"github.com/ViitoJooj/synk/internal/proto"
	"github.com/ViitoJooj/synk/internal/transport"
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

// role is how this side takes part in the link.
const role = "dialer"

// runLink captures this machine's keyboard and mouse and drives the peer with
// them, reconnecting until ctx is cancelled.
//
// This machine owns the physical keyboard and mouse. While the pointer is on
// this screen nothing changes; when it reaches the border the peer sits beyond,
// input is swallowed here and forwarded there instead.
//
// It dials the peer rather than waiting to be dialled, and needs no
// configuration file: the peer is found on the tailnet, and the screen layout
// arrives in its greeting. Nothing to copy onto this machine but the program
// itself.
//
// Ctrl+Alt+F12 always returns control to this machine, whatever state the link
// is in.
func runLink(ctx context.Context, opts Options, st *state) error {
	capture, err := inputdev.StartCapture(captureBuffer)
	if err != nil {
		return err
	}
	defer capture.Stop()

	out := opts.Out
	fmt.Fprintln(out, "synk is capturing this keyboard and mouse; Ctrl+Alt+F12 always takes them back")

	backoff := minBackoff
	ultimo := ""
	for ctx.Err() == nil {
		err := connectAndServe(ctx, out, capture, opts, st)
		if ctx.Err() != nil {
			return nil
		}

		// Só reporta quando o MOTIVO muda. Rodando pela tarefa agendada isto
		// fica de pé o dia todo: com uma linha por ciclo, um notebook desligado
		// a noite inteira escreveria a mesma frase mais de mil vezes e
		// esconderia a única que importa — a mudança de estado.
		if msg := errText(err); msg != ultimo {
			ultimo = msg
			fmt.Fprintf(out, "link down: %s; retrying, backing off to %s\n", msg, maxBackoff)
		}
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

func errText(err error) string {
	if err == nil {
		return "no reason given"
	}
	return err.Error()
}

func connectAndServe(ctx context.Context, out io.Writer, capture *inputdev.Capture, opts Options, st *state) error {
	ts := transport.NewTailscale()
	peer, err := ResolvePeer(ts, opts.Peer, opts.Config)
	if err != nil {
		return err
	}
	st.waiting(peer.Host)
	if !peer.Online {
		return fmt.Errorf("%s is offline", peer.Host)
	}

	conn, err := transport.Dial(peer.IP, Port(opts.Port, opts.Config))
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
		st:      st,
		conn:    &lockedConn{conn: conn},
		capture: capture,
		edge:    peerEdge.Opposite(),
		out:     out,
		screenW: w,
		screenH: h,
		peerIP:  peer.IP,
		cfg:     opts.Config,
		port:    Port(opts.Port, opts.Config),
		verbose: opts.Verbose,
	}

	fmt.Fprintf(out, "connected to %s (%s); this screen is %dx%d, its screen is beyond the %s edge\n",
		peer.Host, peer.IP, w, h, s.edge)

	st.connected(peer.Host, peer.IP, ClipboardEnabled(opts.Config), "local")
	err = servePeer(ctx, s, conn)
	if ctx.Err() != nil {
		st.disconnected(nil) // stopped on purpose, not a failure
	} else {
		st.disconnected(err)
	}
	if err != nil {
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

	if ClipboardEnabled(s.cfg) {
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
		runPlayback(loopCtx, printTo(s.out), s.peerIP, s.port, s.st)
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
	st      *state
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

	// cfg is nil here in practice: this machine reads no file.
	cfg     *config.Config
	verbose bool

	captured uint64

	// Pointer motion waiting to be sent. A fast mouse reports hundreds of tiny
	// deltas a second; sending each as its own frame floods the tunnel, so they
	// are summed and sent on a short tick, or ahead of any other event so the
	// order between motion and clicks is preserved.
	pendDX, pendDY int

	clip     *clipboard.Win32
	clipSync *clipboard.Sync
}

// run is the single owner of focus state: every change flows through this one
// loop, so there is no lock to get wrong.
func (s *winSession) run(ctx context.Context, incoming <-chan frame) error {
	events := s.capture.Events()

	stats := newTicker(s.verbose, statsEvery)
	defer stats.Stop()

	motion := time.NewTicker(motionEvery)
	defer motion.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-motion.C:
			if err := s.flushMotion(); err != nil {
				return err
			}

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

	if e.Kind == inputdev.KindMoveRel {
		s.pendDX += int(e.DX)
		s.pendDY += int(e.DY)
		return nil
	}
	if err := s.flushMotion(); err != nil {
		return err
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
	}
	return nil
}

// motionEvery paces the pointer updates sent to the peer.
const motionEvery = 4 * time.Millisecond

// flushMotion sends the summed pointer motion, in as many frames as the int16
// wire fields need.
func (s *winSession) flushMotion() error {
	for s.pendDX != 0 || s.pendDY != 0 {
		dx, dy := clampDelta(s.pendDX), clampDelta(s.pendDY)
		s.pendDX -= int(dx)
		s.pendDY -= int(dy)
		if err := s.conn.send(proto.MsgMouseRel, proto.MouseRel{DX: dx, DY: dy}.Encode()); err != nil {
			return err
		}
	}
	return nil
}

func clampDelta(v int) int16 {
	switch {
	case v > 32767:
		return 32767
	case v < -32768:
		return -32768
	}
	return int16(v)
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
	s.st.focus("peer")
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
	// Warp BEFORE releasing the pin, not after.
	//
	// The pin is a 1x1 ClipCursor in the bottom-right corner. ClipCursor
	// REPLACES a previous clip rather than being blocked by it, so clipping to
	// the destination both moves the cursor and keeps it held; no release is
	// needed first.
	//
	// Releasing first left the cursor loose in that corner across the window
	// hide inside SetSuppress, and hiding a window churns foreground. Windows
	// drops a clip when the process loses activation, so the warp did not
	// stick and the cursor stayed parked — reported as "it teleports to the
	// bottom-right corner", which is exactly where the park pin puts it.
	//
	// The old ordering was written for SetCursorPos, which IS confined by an
	// active clip and did need the release. MoveCursorTo stopped using it.
	x, y := focus.EnterPoint(edge, ratio, s.screenW, s.screenH)
	if err := inputdev.MoveCursorTo(x, y); err != nil {
		fmt.Fprintf(s.out, "could not reposition the cursor: %v\n", err)
	}

	s.pendDX, s.pendDY = 0, 0
	s.capture.SetSuppress(false)
	s.remoteHasFocus = false
	s.st.focus("local")

	// Instrumento, não enfeite: este caminho só existe no Windows e não pode
	// ser exercitado daqui. Sem ler de volta, um warp que não pegou é
	// indistinguível de um que pegou.
	//
	// A tolerância não é preguiça. ClipCursor num retângulo de 1x1 deixa o
	// cursor na BORDA do retângulo por onde ele entrou, não no canto de cima
	// à esquerda: vindo do canto direito, um pedido de x=2 pousa em x=3.
	// Medido. Um pixel para DENTRO da tela é exatamente o que o inset quer, e
	// avisar sobre ele em toda travessia esconderia o caso que importa — o
	// cursor que ficou parado no canto, a mil e novecentos pixels de distância.
	const desvioTolerado = 8
	if ax, ay := inputdev.CursorPos(); abs(ax-x) > desvioTolerado || abs(ay-y) > desvioTolerado {
		fmt.Fprintf(s.out, "cursor asked for (%d,%d) but landed at (%d,%d)\n", x, y, ax, ay)
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

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
