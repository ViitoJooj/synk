package clipboard

import (
	"strings"
	"testing"
)

func newSync() *Sync { return NewSync(1024) }

func TestFirstLocalChangeIsSent(t *testing.T) {
	s := newSync()

	if send, why := s.LocalChanged("hello"); !send {
		t.Errorf("send = false (%s), want true", why)
	}
}

// Polling reports the same content over and over; only changes travel.
func TestUnchangedLocalContentIsNotResent(t *testing.T) {
	s := newSync()
	s.LocalChanged("hello")

	if send, _ := s.LocalChanged("hello"); send {
		t.Error("send = true for unchanged content, want false")
	}
}

// The loop that matters: text arriving from the peer is written to the local
// clipboard, the local watcher sees a change, and without this it goes
// straight back — for ever.
func TestTextReceivedFromThePeerIsNotEchoedBack(t *testing.T) {
	s := newSync()

	if apply := s.RemoteReceived("from windows"); !apply {
		t.Fatal("apply = false, want the remote text applied locally")
	}
	// The local watcher now observes exactly what we just wrote.
	if send, _ := s.LocalChanged("from windows"); send {
		t.Error("send = true, want false — this is the echo of what we received")
	}
}

// After the echo is absorbed, a genuine local copy must still travel.
func TestALocalCopyAfterAReceiveIsStillSent(t *testing.T) {
	s := newSync()
	s.RemoteReceived("from windows")
	s.LocalChanged("from windows") // the echo, swallowed

	if send, why := s.LocalChanged("typed here"); !send {
		t.Errorf("send = false (%s), want true for a real local copy", why)
	}
}

// Copying the same thing on both machines must not deadlock the sync.
func TestReceivingWhatWeJustSentIsNotReapplied(t *testing.T) {
	s := newSync()
	s.LocalChanged("shared")

	if apply := s.RemoteReceived("shared"); apply {
		t.Error("apply = true, want false — the clipboard already holds this")
	}
}

// The clipboard crosses a machine boundary, so a password copied by accident
// leaves this machine. A cap bounds the damage and keeps a stray 100 MB paste
// from stalling the link.
func TestOversizedContentIsNotSent(t *testing.T) {
	s := NewSync(16)

	send, why := s.LocalChanged(strings.Repeat("x", 17))

	if send {
		t.Error("send = true for oversized content, want false")
	}
	if !strings.Contains(why, "too large") {
		t.Errorf("reason = %q, want it to say the content is too large", why)
	}
}

func TestContentExactlyAtTheCapIsSent(t *testing.T) {
	s := NewSync(16)

	if send, why := s.LocalChanged(strings.Repeat("x", 16)); !send {
		t.Errorf("send = false (%s), want true at exactly the cap", why)
	}
}

func TestOversizedRemoteContentIsRejected(t *testing.T) {
	s := NewSync(16)

	if apply := s.RemoteReceived(strings.Repeat("x", 17)); apply {
		t.Error("apply = true for oversized remote content, want false")
	}
}

// An empty clipboard is a normal state, not something to broadcast.
func TestEmptyContentIsNotSent(t *testing.T) {
	s := newSync()

	if send, _ := s.LocalChanged(""); send {
		t.Error("send = true for empty content, want false")
	}
}

// Alternating copies on both machines must keep flowing in both directions.
func TestAlternatingCopiesKeepSyncing(t *testing.T) {
	s := newSync()

	if send, _ := s.LocalChanged("one"); !send {
		t.Fatal("local 'one' was not sent")
	}
	if apply := s.RemoteReceived("two"); !apply {
		t.Fatal("remote 'two' was not applied")
	}
	if send, _ := s.LocalChanged("two"); send {
		t.Fatal("echo of 'two' was sent back")
	}
	if send, _ := s.LocalChanged("three"); !send {
		t.Fatal("local 'three' was not sent")
	}
	if apply := s.RemoteReceived("four"); !apply {
		t.Fatal("remote 'four' was not applied")
	}
}
