//go:build linux

package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/ViitoJooj/synk/internal/clipboard"
	"github.com/ViitoJooj/synk/internal/proto"
)

// watchClipboard polls the local clipboard and sends anything genuinely new.
//
// Polling rather than `wl-paste --watch`: the watch form is instant but means
// supervising a long-lived subprocess across reconnects, and on a laptop one
// cheap read a second buys simplicity worth more than the latency.
func (s *session) watchClipboard(ctx context.Context) {
	s.clip = clipboard.NewWayland()
	s.clipSync = clipboard.NewSync(clipboardMaxBytes)

	// Seed from the current contents so whatever was already copied before the
	// link came up is not immediately pushed across as if it were new.
	if text, err := s.clip.Read(); err == nil {
		s.clipSync.LocalChanged(text)
	} else {
		fmt.Fprintf(s.out, "clipboard disabled: %v\n", err)
		return
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
			continue // a transient wl-paste failure is not worth tearing down
		}
		send, why := s.clipSync.LocalChanged(text)
		if !send {
			if why != "" && why != "unchanged" && why != "empty" {
				fmt.Fprintf(s.out, "clipboard not sent: %s\n", why)
			}
			continue
		}
		if err := s.conn.send(proto.MsgClipData, []byte(text)); err != nil {
			return // the read loop will report the broken link
		}
	}
}

// applyClipboard writes text the peer copied, unless it is the echo of
// something we sent.
func (s *session) applyClipboard(text string) error {
	if s.clipSync == nil || s.clip == nil {
		return nil // clipboard is switched off in the config
	}
	if !s.clipSync.RemoteReceived(text) {
		return nil
	}
	if err := s.clip.Write(text); err != nil {
		// Losing a clipboard update is not a reason to drop the input link.
		fmt.Fprintf(s.out, "clipboard write failed: %v\n", err)
	}
	return nil
}
