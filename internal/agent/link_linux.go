//go:build linux

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
	"github.com/ViitoJooj/synk/internal/ipc"
	"github.com/ViitoJooj/synk/internal/pointer"
	"github.com/ViitoJooj/synk/internal/proto"
	"github.com/ViitoJooj/synk/internal/screen"
	"github.com/ViitoJooj/synk/internal/transport"
)

const (
	// clipboardPoll is how often the local clipboard is read. wl-paste --watch
	// would be instant, but it means babysitting a long-lived subprocess; on a
	// laptop, one cheap poll a second is the better trade.
	clipboardPoll = 1 * time.Second

	clipboardMaxBytes = 256 * 1024
)

// role is how this side takes part in the link.
const role = "listener"

// runLink accepts the peer's connection and applies its input locally, until
// ctx is cancelled.
//
// This process owns the network connection, the clipboard and the screen
// geometry. It holds no privilege of its own: to type, it asks the injector
// over a unix socket.
//
// This machine listens and the peer dials in. That direction is deliberate: an
// inbound listener on Windows needs a firewall rule and an administrator to
// approve it, while an outbound connection needs neither — and this is the side
// whose logs are easy to read when something goes wrong.
//
// Only the peer's own tailnet address is accepted. Nothing else on the tailnet,
// and nothing off it, can connect.
func runLink(ctx context.Context, opts Options, st *state) error {
	cfg := opts.Config
	if cfg == nil {
		return errors.New("this machine receives input, so it needs a config naming the peer and its edge")
	}

	ts := transport.NewTailscale()

	out := opts.Out
	aviso := func(m string) { fmt.Fprintln(out, m) }

	// Espera o tailscale em vez de sair com erro. Sair fazia o systemd
	// reiniciar de 5 em 5 segundos enquanto o backend subia — 38
	// inicializações em 7 dias, medidas no journal, cada uma acordando a CPU
	// para descobrir que nada mudou.
	self, err := ts.WaitLocal(ctx, aviso)
	if err != nil {
		// SIGTERM durante a espera é parada limpa, não falha: sem isto o
		// `systemctl stop` marcaria a unit como failed toda vez.
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	st.waiting(cfg.Peer)

	// Só faz sentido anunciar link quando há com quem falar. O peer continua
	// listado no tailnet com a máquina desligada, só que com Online falso.
	if _, err := ts.WaitPeer(ctx, cfg.Peer, aviso); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	// Binds the tailnet address only, and refuses anything else. If Tailscale
	// is down the right outcome is to fail, not to expose keystroke injection
	// to the local network.
	l, err := transport.Listen(self, cfg.Port)
	if err != nil {
		return err
	}
	defer l.Close()

	go func() {
		<-ctx.Done()
		l.Close()
	}()

	fmt.Fprintf(out, "synk listening on %s:%d for %s, peer screen on the %s\n",
		self, cfg.Port, cfg.Peer, cfg.EdgeName)

	for ctx.Err() == nil {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(out, "accept failed: %v\n", err)
			continue
		}
		servePeer(ctx, out, conn, ts, cfg, self, st)
	}
	return nil
}

// servePeer runs one connection to completion. Every exit path closes the
// daemon socket, which is what makes the daemon release anything still held —
// so a dropped link can never leave a key down on this machine.
func servePeer(ctx context.Context, out io.Writer, conn net.Conn, ts *transport.Tailscale, cfg *config.Config, self string, st *state) {
	defer conn.Close()
	transport.NoDelay(conn)

	remote := conn.RemoteAddr()

	// Resolved per connection rather than once at startup: a peer that
	// reconnects after a Tailscale restart can come back on a different
	// address, and a cached one would then reject the real machine.
	peer, err := ts.Peer(cfg.Peer)
	if err != nil {
		fmt.Fprintf(out, "refused %s: %v\n", remote, err)
		return
	}
	if err := transport.AuthorizePeer(conn, peer.IP); err != nil {
		fmt.Fprintf(out, "refused %s: %v\n", remote, err)
		return
	}
	if err := transport.ServerHello(conn, cfg.Edge); err != nil {
		fmt.Fprintf(out, "refused %s: %v\n", remote, err)
		return
	}

	geom, err := screen.New().Read()
	if err != nil {
		fmt.Fprintf(out, "cannot read the screen layout: %v\n", err)
		return
	}
	fsm, err := focus.New(geom.Width, geom.Height, cfg.Edge)
	if err != nil {
		fmt.Fprintf(out, "%v\n", err)
		return
	}
	// The peer owns the keyboard until it hands it over.
	fsm.Leave()

	daemon, err := ipc.Dial(cfg.Socket)
	if err != nil {
		fmt.Fprintf(out, "cannot reach the injector daemon: %v\n", err)
		return
	}
	defer daemon.Close()

	// Before the first move, not once at startup: the injector makes a new
	// device whenever it restarts, and KWin does not apply kcminputrc to it.
	// Failing here is not fatal — the link still works, with a cursor that
	// lands off target, and `synk doctor` reports why.
	if err := pointer.New().MakeFlat(inputdev.VirtualNamePrefix); err != nil {
		fmt.Fprintf(out, "could not set a flat pointer profile: %v\n", err)
	}

	fmt.Fprintf(out, "%s (%s) connected; desktop %s, peer screen on the %s\n",
		peer.Host, peer.IP, geom.Describe(), cfg.EdgeName)

	s := &session{
		st:     st,
		conn:   &lockedConn{conn: conn},
		daemon: daemon,
		fsm:    fsm,
		out:    out,
		w:      geom.Width,
		h:      geom.Height,
	}
	// The peer has the pointer, so ours belongs out of the way.
	if err := s.parkCursor(); err != nil {
		fmt.Fprintf(out, "could not park the cursor: %v\n", err)
	}

	st.connected(peer.Host, peer.IP, cfg.Clipboard, "peer")

	// The read loop blocks in ReadFrame and knows nothing of ctx. Until a
	// reload existed, the process simply died on SIGTERM; now the link has to
	// stop on its own while the agent carries on, so ctx ends the read.
	served := make(chan struct{})
	defer close(served)
	go func() {
		select {
		case <-ctx.Done():
			conn.SetReadDeadline(time.Now())
		case <-served:
		}
	}()

	var wg sync.WaitGroup
	loopCtx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		// Unblock the read loop, which is otherwise parked in ReadFrame.
		conn.SetReadDeadline(time.Now())
		wg.Wait()
	}()

	if cfg.Clipboard {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.watchClipboard(loopCtx)
		}()
	}

	if cfg.Audio.Playback {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runPlayback(loopCtx, self, peer.IP, cfg.Port)
		}()
	}

	err = s.serve()
	// Whatever happened, the peer no longer has focus here; releasing is the
	// last thing done before the socket closes.
	_ = daemon.ReleaseAll()
	if ctx.Err() != nil {
		// Stopped on purpose, by a reload or by the agent stopping: not a
		// failure worth keeping as the reason the link went down.
		err = nil
	}
	st.disconnected(err)
	if err == nil {
		fmt.Fprintf(out, "%s disconnected: the link is restarting\n", peer.Host)
	} else {
		fmt.Fprintf(out, "%s disconnected: %v\n", peer.Host, err)
	}
}

// injector is the subset of the daemon client the session drives.
//
// It is an interface so the whole receive path — frame in, injection out — can
// be tested without root and without /dev/uinput, which is where the risk of a
// dispatch bug actually lives.
type injector interface {
	Key(code uint16, pressed bool) error
	Button(btn uint8, pressed bool) error
	MoveRel(dx, dy int32) error
	Scroll(dx, dy int32) error
	ReleaseAll() error
}

// lockedConn serialises writes: the read loop and the clipboard watcher both
// send, and two interleaved frames would desynchronise the peer.
type lockedConn struct {
	mu   sync.Mutex
	conn net.Conn
}

func (c *lockedConn) send(typ proto.MsgType, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return proto.WriteFrame(c.conn, typ, payload)
}

func (c *lockedConn) read() (proto.MsgType, []byte, error) {
	// Only the read loop calls this, so it needs no lock of its own.
	return proto.ReadFrame(c.conn)
}

type session struct {
	st     *state
	conn   *lockedConn
	daemon injector
	fsm    *focus.Machine
	out    io.Writer

	// The desktop the cursor moves across, needed to aim a warp.
	w, h int

	clip     *clipboard.Wayland
	clipSync *clipboard.Sync
}

func (s *session) serve() error {
	for {
		typ, payload, err := s.conn.read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("peer closed the connection")
			}
			return err
		}
		if err := s.handle(typ, payload); err != nil {
			return err
		}
	}
}

func (s *session) handle(typ proto.MsgType, payload []byte) error {
	switch typ {
	case proto.MsgKey:
		k, err := proto.DecodeKey(payload)
		if err != nil {
			return err
		}
		return s.daemon.Key(k.Code, k.Pressed)

	case proto.MsgMouseRel:
		m, err := proto.DecodeMouseRel(payload)
		if err != nil {
			return err
		}
		if err := s.daemon.MoveRel(int32(m.DX), int32(m.DY)); err != nil {
			return err
		}
		return s.trackCursor(int(m.DX), int(m.DY))

	case proto.MsgMouseBtn:
		b, err := proto.DecodeMouseBtn(payload)
		if err != nil {
			return err
		}
		return s.daemon.Button(b.Button, b.Pressed)

	case proto.MsgScroll:
		sc, err := proto.DecodeScroll(payload)
		if err != nil {
			return err
		}
		return s.daemon.Scroll(int32(sc.DX), int32(sc.DY))

	case proto.MsgFocusEnter:
		f, err := proto.DecodeFocusEnter(payload)
		if err != nil {
			return err
		}
		if !f.Edge.Valid() {
			return fmt.Errorf("peer entered from edge %d, which is not a border", uint8(f.Edge))
		}
		s.fsm.Enter(f.Edge, f.Ratio)
		// Put the real cursor where the model says it is. Without this the
		// two disagree from the first frame: Enter moves only the count, and
		// the pointer stays wherever it was last left — which is what made an
		// arriving cursor appear at the top of the screen rather than at the
		// height it crossed at.
		if err := s.enterCursor(f.Ratio); err != nil {
			return err
		}
		s.st.focus("local")
		fmt.Fprintln(s.out, "focus taken by this machine")
		return nil

	case proto.MsgFocusLeave:
		// The peer took focus back on its own initiative; drop anything held
		// so nothing is left pressed here.
		s.fsm.Leave()
		s.st.focus("peer")
		if err := s.daemon.ReleaseAll(); err != nil {
			return err
		}
		return s.parkCursor()

	case proto.MsgReleaseAll:
		return s.daemon.ReleaseAll()

	case proto.MsgPing:
		return s.conn.send(proto.MsgPong, nil)

	case proto.MsgPong:
		return nil

	case proto.MsgClipData:
		return s.applyClipboard(string(payload))

	default:
		// An unknown type from a peer running a newer build is not fatal;
		// ignoring it keeps a version skew survivable.
		return nil
	}
}

// trackCursor integrates the delta we just injected. Wayland will not say where
// the cursor is, so this count is the only way to know when it has reached the
// edge that hands focus back.
func (s *session) trackCursor(dx, dy int) error {
	crossed, ratio := s.fsm.Move(dx, dy)
	if !crossed {
		return nil
	}

	// Let go before handing back: a key still down here would stay down.
	if err := s.daemon.ReleaseAll(); err != nil {
		return err
	}
	if err := s.parkCursor(); err != nil {
		return err
	}
	s.st.focus("peer")
	fmt.Fprintln(s.out, "focus handed back to the peer")
	return s.conn.send(proto.MsgFocusLeave, proto.FocusEnter{
		Edge:  s.fsm.RemoteEdge().Opposite(),
		Ratio: ratio,
	}.Encode())
}

// enterCursor brings the real pointer back to where the peer says it crossed.
//
// Wayland will not let a client place the pointer, and the daemon injects only
// relative motion, so the position has to be reached by moving a known distance
// from a known place. The known place is the corner the pointer was parked in
// when focus left, which is what makes one move enough — and one move is a hard
// requirement, not an optimisation, because the compositor sums moves that
// arrive together and clamps only the total.
//
// The distance is exact only because the virtual device is on a flat
// acceleration profile. Under libinput's default adaptive curve a single large
// delta is scaled by nearly twice — measured here, an intended 500 pixels
// travelled 934 — and the pointer lands somewhere else entirely. `synk
// status` reports the profile for that reason.
func (s *session) enterCursor(ratio uint16) error {
	dx, dy := focus.EnterOffset(s.fsm.RemoteEdge(), ratio, s.w, s.h)
	return s.daemon.MoveRel(int32(dx), int32(dy))
}

// parkCursor flings the pointer into the corner of the border the peer sits
// beyond, so it is not left in the middle of a screen nobody is looking at.
func (s *session) parkCursor() error {
	dx, dy := focus.ParkDelta(s.fsm.RemoteEdge(), s.w, s.h)
	return s.daemon.MoveRel(int32(dx), int32(dy))
}
