package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/ViitoJooj/synk/pkg/errorx"
)

// Handler answers one verb. Its result is sent as JSON; its error, as an
// errorx code and hint.
type Handler func(ctx context.Context, args json.RawMessage) (any, error)

// Server dispatches requests to handlers.
type Server struct {
	verbs map[string]Handler

	// subscribe and snapshot serve "events": a wake-up on every change, and
	// the value to send when woken.
	subscribe func() (<-chan struct{}, func())
	snapshot  func() any
}

func NewServer() *Server { return &Server{verbs: map[string]Handler{}} }

func (s *Server) Handle(verb string, h Handler) { s.verbs[verb] = h }

// Events enables the "events" verb.
func (s *Server) Events(subscribe func() (<-chan struct{}, func()), snapshot func() any) {
	s.subscribe, s.snapshot = subscribe, snapshot
}

// Serve accepts connections until ctx is cancelled, then closes l and waits for
// the connections in flight.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	var wg sync.WaitGroup
	defer wg.Wait()

	go func() {
		<-ctx.Done()
		l.Close()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, errRefused) {
				continue
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.serve(ctx, conn)
		}()
	}
}

func (s *Server) serve(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	// Closing the connection is what unblocks a read or a stream when the
	// agent is going away.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	in := bufio.NewScanner(conn)
	in.Buffer(make([]byte, 0, 4096), maxLine)
	if !in.Scan() {
		if errors.Is(in.Err(), bufio.ErrTooLong) {
			writeLine(conn, Response{Error: errorOf(fmt.Errorf("control: request longer than %d bytes", maxLine))})
		}
		return
	}

	var req Request
	if err := json.Unmarshal(in.Bytes(), &req); err != nil {
		writeLine(conn, Response{Error: errorOf(fmt.Errorf("control: malformed request: %w", err))})
		return
	}

	if req.Verb == "events" && s.subscribe != nil {
		s.stream(ctx, conn, in)
		return
	}

	h, ok := s.verbs[req.Verb]
	if !ok {
		writeLine(conn, Response{Error: errorOf(&errorx.Error{
			Code: "snk-11", Msg: fmt.Sprintf("the agent does not know %q", req.Verb), Kind: errorx.User,
			Hint: "the command line and the agent may be different versions; reinstall to match them",
		})})
		return
	}

	result, err := h(ctx, req.Args)
	if err != nil {
		writeLine(conn, Response{Error: errorOf(err)})
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		writeLine(conn, Response{Error: errorOf(err)})
		return
	}
	writeLine(conn, Response{OK: true, Result: raw})
}

// stream sends the status now and after every change, until the client goes
// away or the agent stops.
func (s *Server) stream(ctx context.Context, conn net.Conn, in *bufio.Scanner) {
	changed, stop := s.subscribe()
	defer stop()

	// A client has nothing more to say on this connection, so the only thing
	// a read can return is the client hanging up.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		for in.Scan() {
		}
	}()

	for {
		raw, err := json.Marshal(s.snapshot())
		if err != nil {
			return
		}
		if writeLine(conn, Response{OK: true, Event: "status", Result: raw}) != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-gone:
			return
		case <-changed:
		}
	}
}

func writeLine(conn net.Conn, r Response) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(raw, '\n'))
	return err
}
