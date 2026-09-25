// Package ipc carries injection commands from the unprivileged session process
// to the privileged daemon over a unix socket.
//
// The split is the security boundary: the daemon holds /dev/uinput and nothing
// else — no network, no audio, no clipboard — so the component that can type on
// your behalf has the smallest possible surface. The session, which does talk to
// the network, holds no privilege at all.
//
// The wire is the same framing and the same messages as the network link, so
// there is one protocol to get right rather than two.
package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/ViitoJooj/synk/internal/keystate"
	"github.com/ViitoJooj/synk/internal/proto"
)

// socketMode keeps the socket to its owner and group. Opening it means being
// able to inject keystrokes, so "other" gets nothing.
const socketMode = 0o660

// Injector is what the privileged daemon can do on the session's behalf.
type Injector interface {
	Key(code uint16, pressed bool) error
	Button(btn uint8, pressed bool) error
	MoveRel(dx, dy int32) error
	Scroll(dx, dy int32) error
}

type Server struct {
	listener  net.Listener
	inj       Injector
	closeOnce sync.Once
}

// NewServer binds the socket. A stale socket left by a crashed daemon is
// replaced rather than treated as fatal.
func NewServer(path string, inj Injector) (*Server, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("ipc: clearing %s: %w", path, err)
	}

	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("ipc: listening on %s: %w", path, err)
	}
	// Listen honours umask, so the mode is set explicitly rather than hoped for.
	if err := os.Chmod(path, socketMode); err != nil {
		l.Close()
		return nil, fmt.Errorf("ipc: securing %s: %w", path, err)
	}

	return &Server{listener: l, inj: inj}, nil
}

// Serve accepts sessions one at a time until ctx is cancelled. Only one session
// makes sense: two processes injecting at once would interleave keystrokes.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		s.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("ipc: accepting: %w", err)
		}
		// A session that sends garbage loses its connection; the daemon
		// keeps running and serves the next one.
		s.handle(conn)
	}
}

func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() { err = s.listener.Close() })
	return err
}

// handle serves one session for as long as it stays connected.
//
// Everything the session presses is tracked, so that when it goes away — a
// crash, a kill, a lost socket — the daemon can let go of exactly what was
// still down. Without this the laptop is left with a stuck Shift and no
// process left to blame.
func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

	held := keystate.New()
	defer s.releaseAll(held)

	for {
		typ, payload, err := proto.ReadFrame(conn)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				// Corrupt or truncated: drop the session, keep the daemon.
				return
			}
			return
		}
		if err := s.apply(typ, payload, held); err != nil {
			return
		}
	}
}

func (s *Server) apply(typ proto.MsgType, payload []byte, held *keystate.Tracker) error {
	switch typ {
	case proto.MsgKey:
		k, err := proto.DecodeKey(payload)
		if err != nil {
			return err
		}
		held.Key(k.Code, k.Pressed)
		return s.inj.Key(k.Code, k.Pressed)

	case proto.MsgMouseBtn:
		b, err := proto.DecodeMouseBtn(payload)
		if err != nil {
			return err
		}
		held.Button(b.Button, b.Pressed)
		return s.inj.Button(b.Button, b.Pressed)

	case proto.MsgMouseRel:
		m, err := proto.DecodeMouseRel(payload)
		if err != nil {
			return err
		}
		return s.inj.MoveRel(int32(m.DX), int32(m.DY))

	case proto.MsgScroll:
		sc, err := proto.DecodeScroll(payload)
		if err != nil {
			return err
		}
		return s.inj.Scroll(int32(sc.DX), int32(sc.DY))

	case proto.MsgReleaseAll:
		s.releaseAll(held)
		return nil

	default:
		return fmt.Errorf("ipc: unknown message type %d", typ)
	}
}

// releaseAll lets go of everything still held, ordinary keys before modifiers.
func (s *Server) releaseAll(held *keystate.Tracker) {
	for _, e := range held.ReleaseAll() {
		if e.Kind == keystate.KindButton {
			_ = s.inj.Button(uint8(e.Code), false)
			continue
		}
		_ = s.inj.Key(e.Code, false)
	}
}

// Client is the session's handle on the daemon.
type Client struct {
	conn net.Conn
	mu   sync.Mutex // one frame at a time; frames must not interleave
}

func Dial(path string) (*Client, error) {
	conn, err := dialRaw(path)
	if err != nil {
		return nil, fmt.Errorf("ipc: connecting to the daemon at %s: %w", path, err)
	}
	return &Client{conn: conn}, nil
}

func (c *Client) Key(code uint16, pressed bool) error {
	return c.send(proto.MsgKey, proto.KeyEvent{Code: code, Pressed: pressed}.Encode())
}

func (c *Client) Button(btn uint8, pressed bool) error {
	return c.send(proto.MsgMouseBtn, proto.MouseBtn{Button: btn, Pressed: pressed}.Encode())
}

func (c *Client) MoveRel(dx, dy int32) error {
	return c.send(proto.MsgMouseRel, proto.MouseRel{DX: int16(dx), DY: int16(dy)}.Encode())
}

func (c *Client) Scroll(dx, dy int32) error {
	return c.send(proto.MsgScroll, proto.Scroll{DX: int16(dx), DY: int16(dy)}.Encode())
}

// ReleaseAll asks the daemon to let go of everything it is holding, used on
// every focus handoff rather than waiting for a disconnect.
func (c *Client) ReleaseAll() error {
	return c.send(proto.MsgReleaseAll, nil)
}

func (c *Client) send(typ proto.MsgType, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return proto.WriteFrame(c.conn, typ, payload)
}

func (c *Client) Close() error { return c.conn.Close() }

func dialRaw(path string) (net.Conn, error) { return net.Dial("unix", path) }
