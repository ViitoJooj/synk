//go:build linux

package agent

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ViitoJooj/synk/internal/control"
	"github.com/ViitoJooj/synk/internal/sync/engine"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

// fakeLink stands in for runLink: it reports each start and blocks until its
// context ends, the way the real one does while waiting for the peer.
// noSync keeps the agent's tests off the real folder list and network.
func noSync(t *testing.T) {
	t.Helper()
	oldEngine, oldSync := newEngine, startSync
	newEngine = func(io.Writer, func()) (*engine.Engine, error) { return nil, nil }
	startSync = func(context.Context, Options, *engine.Engine) {}
	t.Cleanup(func() { newEngine, startSync = oldEngine, oldSync })
}

func fakeLink(t *testing.T) (starts chan Options) {
	t.Helper()
	noSync(t)
	starts = make(chan Options, 10)
	old := startLink
	startLink = func(ctx context.Context, opts Options, st *state) error {
		st.waiting(opts.Peer)
		starts <- opts
		<-ctx.Done()
		return nil
	}
	t.Cleanup(func() { startLink = old })
	return starts
}

func runAgent(t *testing.T, opts Options) (stop func() error) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()

	// Wait for the socket.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := control.Call(context.Background(), control.Address(), "status", nil, nil); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(3 * time.Second):
			t.Fatal("Run did not return after cancel")
			return nil
		}
	}
}

func next(t *testing.T, starts chan Options) Options {
	t.Helper()
	select {
	case o := <-starts:
		return o
	case <-time.After(3 * time.Second):
		t.Fatal("the link did not start")
		return Options{}
	}
}

// The point of reloading in process: the link restarts with the new settings
// while the agent, and its socket, stay up.
func TestReloadRestartsOnlyTheLink(t *testing.T) {
	starts := fakeLink(t)
	var reloads atomic.Int32
	stop := runAgent(t, Options{
		Peer: "old-pc",
		Out:  io.Discard,
		Reload: func() (Options, error) {
			reloads.Add(1)
			return Options{Peer: "new-pc", Out: io.Discard}, nil
		},
	})

	if o := next(t, starts); o.Peer != "old-pc" {
		t.Fatalf("first start with %q", o.Peer)
	}

	var st Status
	if err := control.Call(context.Background(), control.Address(), "reload", nil, &st); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if o := next(t, starts); o.Peer != "new-pc" {
		t.Fatalf("restart with %q, want the reloaded peer", o.Peer)
	}
	if reloads.Load() != 1 {
		t.Errorf("Reload called %d times", reloads.Load())
	}

	var after Status
	if err := control.Call(context.Background(), control.Address(), "status", nil, &after); err != nil {
		t.Fatalf("status after reload: %v", err)
	}
	if after.Role != role || after.Link.Peer != "new-pc" {
		t.Errorf("status = %+v", after)
	}
	if err := stop(); err != nil {
		t.Errorf("Run: %v", err)
	}
}

// Settings that do not load must leave the running link alone.
func TestAFailedReloadKeepsTheLink(t *testing.T) {
	starts := fakeLink(t)
	stop := runAgent(t, Options{
		Peer:   "pc",
		Out:    io.Discard,
		Reload: func() (Options, error) { return Options{}, errors.New("config: edge missing") },
	})
	next(t, starts)

	err := control.Call(context.Background(), control.Address(), "reload", nil, nil)
	var e *errorx.Error
	if !errors.As(err, &e) || e.Code != "snk-07" {
		t.Fatalf("err = %v, want snk-07", err)
	}
	select {
	case <-starts:
		t.Fatal("the link restarted after a reload that failed")
	case <-time.After(200 * time.Millisecond):
	}
	stop()
}

func TestAFailingLinkEndsTheAgent(t *testing.T) {
	noSync(t)
	boom := errors.New("tailscale: backend Stopped")
	old := startLink
	startLink = func(context.Context, Options, *state) error { return boom }
	t.Cleanup(func() { startLink = old })

	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if err := Run(context.Background(), Options{Out: io.Discard}); !errors.Is(err, boom) {
		t.Errorf("Run = %v, want the link's error, so the service manager restarts it", err)
	}
}
