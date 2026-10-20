package agent

import (
	"errors"
	"testing"
	"time"
)

func TestStateTracksALinkComingAndGoing(t *testing.T) {
	s := newState("listener")
	clock := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }

	s.waiting("pc")
	clock = clock.Add(time.Minute)
	s.connected("pc", "100.64.0.2", true, "peer")
	s.focus("local")
	s.audio("flowing")

	l := s.Snapshot().Link
	if l.State != LinkConnected || l.PeerIP != "100.64.0.2" || l.Focus != "local" || l.Audio != "flowing" || !l.Clipboard {
		t.Fatalf("connected: %+v", l)
	}
	if !l.Since.Equal(clock) {
		t.Errorf("Since = %v, want the moment it connected", l.Since)
	}

	clock = clock.Add(time.Minute)
	s.disconnected(errors.New("peer closed the connection"))
	l = s.Snapshot().Link
	if l.State != LinkWaiting || l.Focus != "" || l.Audio != "off" || l.Peer != "pc" {
		t.Fatalf("after disconnect: %+v", l)
	}
	if l.LastError != "peer closed the connection" {
		t.Errorf("LastError = %q", l.LastError)
	}
}

// Focus moving must not look like a reconnection.
func TestSinceMovesOnlyWithTheState(t *testing.T) {
	s := newState("listener")
	start := time.Date(2026, 9, 25, 20, 0, 0, 0, time.UTC)
	clock := start
	s.now = func() time.Time { return clock }
	s.connected("pc", "ip", false, "peer")

	clock = clock.Add(time.Hour)
	s.focus("local")
	if got := s.Snapshot().Link.Since; !got.Equal(start) {
		t.Errorf("Since = %v after a focus change, want %v", got, start)
	}
}

func TestSubscribersAreWokenAndNeverBlockTheLink(t *testing.T) {
	s := newState("listener")
	ch, stop := s.Subscribe()
	defer stop()

	// Many changes with nobody reading must not block.
	for i := 0; i < 100; i++ {
		s.focus([]string{"local", "peer"}[i%2])
	}
	select {
	case <-ch:
	default:
		t.Fatal("no wake-up after a change")
	}
	select {
	case <-ch:
		t.Fatal("wake-ups queued up instead of coalescing")
	default:
	}
}

func TestNoWakeUpWithoutAChange(t *testing.T) {
	s := newState("listener")
	s.focus("local")
	ch, stop := s.Subscribe()
	defer stop()

	s.focus("local")
	select {
	case <-ch:
		t.Fatal("woken for a change that changed nothing")
	default:
	}
}

func TestANilStateIgnoresUpdates(t *testing.T) {
	var s *state
	s.focus("local") // must not panic
	s.disconnected(errors.New("x"))
}
