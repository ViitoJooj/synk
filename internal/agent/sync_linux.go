//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ViitoJooj/synk/internal/sync/engine"
	"github.com/ViitoJooj/synk/internal/transport"
)

// runSync listens for the peer's folder-sync connection, like the link: this
// side listens, the Windows side dials, and only the peer's own tailnet
// address is accepted.
func runSync(ctx context.Context, opts Options, eng *engine.Engine) {
	ts := transport.NewTailscale()
	var self string
	for self == "" {
		ip, err := ts.Self()
		if err == nil {
			self = ip
			break
		}
		if sleep(ctx, 5*time.Second) != nil {
			return
		}
	}

	l, err := transport.Listen(self, syncPort(opts))
	if err != nil {
		fmt.Fprintf(opts.Out, "folder sync off: %v\n", err)
		return
	}
	defer l.Close()
	go func() {
		<-ctx.Done()
		l.Close()
	}()

	for ctx.Err() == nil {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(opts.Out, "folder sync: accept failed: %v\n", err)
				sleep(ctx, time.Second)
			}
			continue
		}
		// Resolved per connection: the peer may have come back on another
		// address.
		peer, err := ResolvePeer(ts, opts.Peer, opts.Config)
		if err == nil {
			err = transport.AuthorizePeer(conn, peer.IP)
		}
		if err != nil {
			fmt.Fprintf(opts.Out, "folder sync: refused %s: %v\n", conn.RemoteAddr(), err)
			conn.Close()
			continue
		}
		if err := eng.Serve(ctx, conn); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(opts.Out, "folder sync: %v\n", err)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
