package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// ErrNotRunning means nothing answers at the address: the agent is stopped,
// or not installed.
var ErrNotRunning = errors.New("control: the agent is not running")

// dialTimeout keeps a command line from hanging on an agent that is wedged.
const dialTimeout = 2 * time.Second

// Call sends one request and decodes the result into out, which may be nil.
// A failure the agent reported comes back as an *errorx.Error.
func Call(ctx context.Context, addr, verb string, args, out any) error {
	conn, err := dial(ctx, addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	if err := send(conn, verb, args); err != nil {
		return err
	}

	in := bufio.NewScanner(conn)
	in.Buffer(make([]byte, 0, 4096), maxLine)
	resp, err := receive(in)
	if err != nil {
		return err
	}
	if out != nil && resp.Result != nil {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("control: decoding the answer to %s: %w", verb, err)
		}
	}
	return nil
}

// Watch calls fn with every status the agent sends, starting with the current
// one, until ctx is cancelled, fn fails, or the agent goes away.
func Watch(ctx context.Context, addr string, fn func(json.RawMessage) error) error {
	conn, err := dial(ctx, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	if err := send(conn, "events", nil); err != nil {
		return err
	}
	in := bufio.NewScanner(conn)
	in.Buffer(make([]byte, 0, 4096), maxLine)
	for {
		resp, err := receive(in)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := fn(resp.Result); err != nil {
			return err
		}
	}
}

func dial(ctx context.Context, addr string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := dialAddr(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	return conn, nil
}

func send(conn net.Conn, verb string, args any) error {
	req := Request{Verb: verb}
	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			return err
		}
		req.Args = raw
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(raw, '\n'))
	return err
}

func receive(in *bufio.Scanner) (Response, error) {
	if !in.Scan() {
		if err := in.Err(); err != nil {
			return Response{}, fmt.Errorf("control: reading the answer: %w", err)
		}
		return Response{}, errors.New("control: the agent closed the connection without answering")
	}
	var resp Response
	if err := json.Unmarshal(in.Bytes(), &resp); err != nil {
		return Response{}, fmt.Errorf("control: malformed answer: %w", err)
	}
	if resp.Error != nil {
		return Response{}, resp.Error.errorx()
	}
	return resp, nil
}
