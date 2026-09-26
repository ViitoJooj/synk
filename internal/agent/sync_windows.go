//go:build windows

package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/ViitoJooj/synk/internal/sync/engine"
	"github.com/ViitoJooj/synk/internal/transport"
)

// runSync dials the peer's folder-sync port, like the link: outbound, so no
// firewall rule is needed on this side. It reconnects until ctx ends.
func runSync(ctx context.Context, opts Options, eng *engine.Engine) {
	ts := transport.NewTailscale()
	backoff, last := time.Second, ""
	for ctx.Err() == nil {
		err := func() error {
			peer, err := ResolvePeer(ts, opts.Peer, opts.Config)
			if err != nil {
				return err
			}
			conn, err := transport.Dial(peer.IP, syncPort(opts))
			if err != nil {
				return err
			}
			backoff = time.Second
			return eng.Serve(ctx, conn)
		}()
		if ctx.Err() != nil {
			return
		}
		// As with the link, only a change of reason is worth a line.
		if msg := fmt.Sprint(err); msg != last {
			last = msg
			fmt.Fprintf(opts.Out, "folder sync down: %s; retrying\n", msg)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}
