//go:build linux

package control

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ViitoJooj/synk/pkg/errorx"
)

// fakeState is what the agent's state looks like to the server.
type fakeState struct {
	mu    sync.Mutex
	value string
	subs  []chan struct{}
}

func (f *fakeState) subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	f.mu.Lock()
	f.subs = append(f.subs, ch)
	f.mu.Unlock()
	return ch, func() {}
}

func (f *fakeState) snapshot() any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return map[string]string{"link": f.value}
}

func (f *fakeState) set(v string) {
	f.mu.Lock()
	f.value = v
	subs := f.subs
	f.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// serve starts a server on a socket in a fresh directory and returns its
// address.
func serve(t *testing.T, st *fakeState) string {
	t.Helper()
	addr := filepath.Join(t.TempDir(), "run", "control.sock")

	s := NewServer()
	s.Handle("echo", func(_ context.Context, args json.RawMessage) (any, error) {
		var in map[string]string
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		return in, nil
	})
	s.Handle("fail", func(context.Context, json.RawMessage) (any, error) {
		return nil, errorx.PeerNotFound("desk", []string{"pc"})
	})
	if st != nil {
		s.Events(st.subscribe, st.snapshot)
	}

	l, err := Listen(addr)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- s.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return addr
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestCallRoundTrips(t *testing.T) {
	addr := serve(t, nil)

	var out map[string]string
	if err := Call(ctx(t), addr, "echo", map[string]string{"peer": "pc"}, &out); err != nil {
		t.Fatal(err)
	}
	if out["peer"] != "pc" {
		t.Errorf("got %v", out)
	}
}

// The command line prints the agent's failures exactly as its own, so the code
// and the hint have to survive the trip.
func TestCallCarriesTheErrorCodeAndHint(t *testing.T) {
	addr := serve(t, nil)

	err := Call(ctx(t), addr, "fail", nil, nil)
	var e *errorx.Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v, want an *errorx.Error", err)
	}
	if e.Code != "snk-00" || e.Kind != errorx.User || !strings.Contains(e.Hint, "synk ls") {
		t.Errorf("got %+v", e)
	}
}

func TestUnknownVerbSaysSo(t *testing.T) {
	addr := serve(t, nil)

	err := Call(ctx(t), addr, "folders.add", nil, nil)
	var e *errorx.Error
	if !errors.As(err, &e) || e.Code != "snk-11" {
		t.Fatalf("err = %v, want snk-11", err)
	}
}

func TestNothingListeningIsNotRunning(t *testing.T) {
	err := Call(ctx(t), filepath.Join(t.TempDir(), "none.sock"), "status", nil, nil)
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("err = %v, want ErrNotRunning", err)
	}
}

func TestEventsSendTheStatusAndEveryChange(t *testing.T) {
	st := &fakeState{value: "waiting"}
	addr := serve(t, st)

	c, cancel := context.WithCancel(ctx(t))
	defer cancel()
	seen := make(chan string, 10)
	go Watch(c, addr, func(raw json.RawMessage) error {
		var v map[string]string
		json.Unmarshal(raw, &v)
		seen <- v["link"]
		return nil
	})

	expect := func(want string) {
		t.Helper()
		select {
		case got := <-seen:
			if got != want {
				t.Fatalf("event %q, want %q", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("no event, want %q", want)
		}
	}
	expect("waiting")

	// The subscription is registered by the time the first event arrives.
	st.set("connected")
	expect("connected")
}

// The agent stopping must end a watcher, not leave the UI hanging on a dead
// connection.
func TestServerStopEndsTheStream(t *testing.T) {
	st := &fakeState{value: "waiting"}
	addr := filepath.Join(t.TempDir(), "control.sock")
	s := NewServer()
	s.Events(st.subscribe, st.snapshot)
	l, err := Listen(addr)
	if err != nil {
		t.Fatal(err)
	}
	sctx, stop := context.WithCancel(context.Background())
	served := make(chan error)
	go func() { served <- s.Serve(sctx, l) }()

	got := make(chan struct{}, 1)
	watched := make(chan error)
	go func() {
		watched <- Watch(ctx(t), addr, func(json.RawMessage) error { got <- struct{}{}; return nil })
	}()
	<-got
	stop()
	<-served

	select {
	case err := <-watched:
		if err == nil {
			t.Error("Watch returned nil when the agent went away; want an error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Watch still running after the server stopped")
	}
}

func TestSocketIsPrivate(t *testing.T) {
	addr := serve(t, nil)

	for path, want := range map[string]os.FileMode{filepath.Dir(addr): 0o700, addr: 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %v, want %v", path, got, want)
		}
	}
}

func TestStaleSocketIsReplaced(t *testing.T) {
	addr := filepath.Join(t.TempDir(), "control.sock")
	// A socket file nobody listens on: an agent that died.
	l, err := net.Listen("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()

	l2, err := Listen(addr)
	if err != nil {
		t.Fatalf("Listen over a stale socket: %v", err)
	}
	l2.Close()
}

func TestASecondAgentIsRefused(t *testing.T) {
	addr := serve(t, nil)

	if l, err := Listen(addr); err == nil {
		l.Close()
		t.Fatal("a second Listen on a live socket succeeded")
	}
}

func TestOversizedRequestIsRefused(t *testing.T) {
	addr := serve(t, nil)

	err := Call(ctx(t), addr, "echo", map[string]string{"x": strings.Repeat("a", maxLine)}, nil)
	if err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Errorf("err = %v, want a refusal", err)
	}
}

func TestMalformedRequestIsAnswered(t *testing.T) {
	addr := serve(t, nil)

	conn, err := net.Dial("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("not json\n"))
	buf := make([]byte, 512)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "malformed request") {
		t.Errorf("got %q", buf[:n])
	}
}
