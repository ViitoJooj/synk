// Package clipboard keeps the two machines' clipboards in step.
//
// Text only, and capped: the clipboard is the one channel here that carries
// whatever you happened to copy — passwords included — across a machine
// boundary, so it stays as narrow as it can be while still being useful.
package clipboard

import "fmt"

// Sync decides what should cross the link. It holds no I/O, so the rule that
// actually matters — not echoing back what the peer just sent — is testable on
// its own.
//
// Not safe for concurrent use; the session goroutine owns it.
type Sync struct {
	maxBytes int
	// current is what both clipboards are believed to hold. Anything equal to
	// it is an echo rather than news, whichever direction it arrives from.
	current string
	primed  bool
}

func NewSync(maxBytes int) *Sync { return &Sync{maxBytes: maxBytes} }

// LocalChanged reports whether locally observed clipboard text should be sent
// to the peer, and why not when it should not.
func (s *Sync) LocalChanged(text string) (bool, string) {
	if text == "" {
		return false, "empty"
	}
	if len(text) > s.maxBytes {
		return false, fmt.Sprintf("too large: %d bytes, cap is %d", len(text), s.maxBytes)
	}
	// This is the loop guard. Writing received text to the local clipboard
	// makes the watcher fire; without this the text bounces for ever.
	if s.primed && text == s.current {
		return false, "unchanged"
	}

	s.current, s.primed = text, true
	return true, ""
}

// RemoteReceived records text from the peer and reports whether it should be
// written to the local clipboard.
func (s *Sync) RemoteReceived(text string) bool {
	if text == "" || len(text) > s.maxBytes {
		return false
	}
	// Both machines copying the same thing is not a reason to write again.
	if s.primed && text == s.current {
		return false
	}

	s.current, s.primed = text, true
	return true
}

// Current is what the sync believes both clipboards hold, for status output.
func (s *Sync) Current() string { return s.current }
