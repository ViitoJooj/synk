package agent

import (
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/ViitoJooj/synk/internal/sync/engine"
)

// Status is what the agent says about itself over the control API: to
// `synk status`, and to the UI, which redraws on every change.
type Status struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	// Role is "listener" on Linux, which waits for the peer to dial in, and
	// "dialer" on Windows, which dials.
	Role string `json:"role"`
	Link Link   `json:"link"`
	// Sync is the folder sync, when it runs.
	Sync *engine.Status `json:"sync,omitempty"`
}

// Link is the connection to the peer.
type Link struct {
	// State is "starting" while Tailscale comes up, "waiting" for the peer,
	// or "connected".
	State string    `json:"state"`
	Since time.Time `json:"since"`

	Peer   string `json:"peer,omitempty"`
	PeerIP string `json:"peer_ip,omitempty"`

	// Focus is which machine the keyboard and mouse are driving while
	// connected: "local" or "peer".
	Focus string `json:"focus,omitempty"`
	// Audio is "off", "waiting" for the other side, or "flowing".
	Audio     string `json:"audio"`
	Clipboard bool   `json:"clipboard"`

	// LastError is why the previous connection ended, kept after it is gone:
	// "why did it drop?" is asked after the fact.
	LastError string `json:"last_error,omitempty"`
}

// Link states.
const (
	LinkStarting  = "starting"
	LinkWaiting   = "waiting"
	LinkConnected = "connected"
)

// state holds the Status and tells subscribers when it changes. Subscribers
// get a wake-up, not the value: a slow reader then sees only the latest
// state instead of a backlog of stale ones, and a stuck one never blocks the
// link.
type state struct {
	mu   sync.Mutex
	st   Status
	subs map[chan struct{}]struct{}
	now  func() time.Time
}

func newState(role string) *state {
	s := &state{subs: map[chan struct{}]struct{}{}, now: time.Now}
	s.st = Status{
		PID:     os.Getpid(),
		Started: s.now(),
		Role:    role,
		Link:    Link{State: LinkStarting, Since: s.now(), Audio: "off"},
	}
	return s
}

func (s *state) Snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

// Subscribe returns a channel that receives after every change, and the
// function that stops it.
func (s *state) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

// update applies f and wakes subscribers. A nil state ignores it, which is
// what the link's own tests run with.
func (s *state) update(f func(*Link)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	before := s.st.Link
	f(&s.st.Link)
	if s.st.Link.State != before.State {
		s.st.Link.Since = s.now()
	}
	changed := s.st.Link != before
	for ch := range s.subs {
		if !changed {
			break
		}
		select {
		case ch <- struct{}{}:
		default: // already has a wake-up pending
		}
	}
	s.mu.Unlock()
}

// setSync records the folder sync's status and wakes subscribers only if it
// changed: the engine reports after every scan, every few seconds, and a UI
// should not redraw for nothing.
func (s *state) setSync(st engine.Status) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.Sync != nil && reflect.DeepEqual(*s.st.Sync, st) {
		return
	}
	s.st.Sync = &st
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *state) waiting(peer string) {
	s.update(func(l *Link) {
		l.State, l.Peer, l.PeerIP, l.Focus, l.Audio = LinkWaiting, peer, "", "", "off"
	})
}

// connected records a link up. focus is who has the keyboard at that moment:
// the Windows side owns it, so it starts "local" there and "peer" on Linux.
func (s *state) connected(peer, ip string, clipboard bool, focus string) {
	s.update(func(l *Link) {
		l.State, l.Peer, l.PeerIP, l.Clipboard = LinkConnected, peer, ip, clipboard
		l.Focus, l.Audio = focus, "off"
	})
}

func (s *state) disconnected(err error) {
	s.update(func(l *Link) {
		l.State, l.PeerIP, l.Focus, l.Audio = LinkWaiting, "", "", "off"
		if err != nil {
			l.LastError = err.Error()
		}
	})
}

func (s *state) focus(where string) { s.update(func(l *Link) { l.Focus = where }) }

func (s *state) audio(word string) { s.update(func(l *Link) { l.Audio = word }) }
