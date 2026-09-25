package transport

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrAmbiguous means more than one online machine fits what the user typed.
var ErrAmbiguous = errors.New("transport: more than one peer matches")

// TargetError says why a name typed after `synk` picked no single machine,
// with what the command line needs to say something useful about it.
type TargetError struct {
	Name string
	// ByOS is set when Name was taken as an operating system, not a machine.
	ByOS bool
	// Matches are the online machines that fit, when there were several.
	Matches []string
	// Known is every machine on the tailnet, for a name that fit none.
	Known []string
	Err   error
}

func (e *TargetError) Error() string {
	switch {
	case errors.Is(e.Err, ErrAmbiguous):
		return fmt.Sprintf("%v: %q fits %s", e.Err, e.Name, strings.Join(e.Matches, ", "))
	case e.ByOS:
		return fmt.Sprintf("%v: no %s machine is online", e.Err, e.Name)
	default:
		return fmt.Sprintf("%v: %q; known peers: %s", e.Err, e.Name, strings.Join(e.Known, ", "))
	}
}

func (e *TargetError) Unwrap() error { return e.Err }

// Peers lists every other machine on the tailnet, sorted by name. Machines
// without a tailnet IPv4 are left out: nothing could reach them.
func (t *Tailscale) Peers() ([]Peer, error) {
	s, err := t.status()
	if err != nil {
		return nil, err
	}
	return peersOf(s), nil
}

func peersOf(s *status) []Peer {
	out := make([]Peer, 0, len(s.Peer))
	for _, n := range s.Peer {
		if ip := ipv4Of(n); ip != "" {
			out = append(out, peerOf(n, ip))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// Target resolves what the user typed after `synk`: a machine's name, or an
// operating system.
//
// A name wins over an operating system, so a machine that happens to be called
// "linux" can still be picked. Taken as an operating system, the word picks the
// one online computer running it and refuses to guess between two: guessing
// wrong here means typing into the wrong machine. That is the same rule
// SolePeer applies when nothing is named at all.
func (t *Tailscale) Target(name string) (Peer, error) {
	s, err := t.status()
	if err != nil {
		return Peer{}, err
	}
	peers := peersOf(s)

	want := strings.ToLower(strings.TrimSuffix(name, "."))
	for _, n := range s.Peer {
		if matches(n, want) {
			if ip := ipv4Of(n); ip != "" {
				return peerOf(n, ip), nil
			}
		}
	}

	if !isOS(want) {
		known := make([]string, 0, len(peers))
		for _, p := range peers {
			known = append(known, p.Host)
		}
		return Peer{}, &TargetError{Name: name, Known: known, Err: ErrPeerNotFound}
	}

	var fit []Peer
	for _, p := range peers {
		if p.Online && strings.EqualFold(p.OS, want) {
			fit = append(fit, p)
		}
	}
	switch len(fit) {
	case 1:
		return fit[0], nil
	case 0:
		return Peer{}, &TargetError{Name: want, ByOS: true, Err: ErrPeerNotFound}
	default:
		names := make([]string, 0, len(fit))
		for _, p := range fit {
			names = append(names, p.Host)
		}
		return Peer{}, &TargetError{Name: want, ByOS: true, Matches: names, Err: ErrAmbiguous}
	}
}

// CanRunTheLink reports whether the machine has a keyboard and mouse to share.
func (p Peer) CanRunTheLink() bool { return canRunTheLink(p.OS) }

// isOS reports whether a word names an operating system synk runs on, spelt
// the way Tailscale reports it.
func isOS(word string) bool { return canRunTheLink(word) }

// SameHost reports whether name, as a user or a config would write it, is p.
// It accepts the same spellings Peer does: the host name or the MagicDNS name,
// ignoring case.
func SameHost(p Peer, name string) bool {
	want := strings.ToLower(strings.TrimSuffix(name, "."))
	host := strings.ToLower(p.Host)
	return want == host || strings.HasPrefix(want, host+".") || strings.EqualFold(p.hostName, want)
}
