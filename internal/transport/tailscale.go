// Package transport connects the two machines over their tailnet.
//
// Peer discovery shells out to `tailscale status --json` rather than importing
// tailscale.com/client: the CLI behaves identically on Linux and Windows, needs
// no privilege, and keeps a very large module out of go.mod for what amounts to
// reading two fields.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/ViitoJooj/synk/internal/procx"
)

var (
	ErrNotLoggedIn  = errors.New("transport: tailscale is not logged in")
	ErrPeerNotFound = errors.New("transport: peer not found")
)

// Peer is one machine on the tailnet.
type Peer struct {
	Host   string
	IP     string // the 100.x IPv4 we bind and dial
	OS     string // "linux", "windows", "iOS"; as Tailscale reports it
	Online bool
	// Direct reports a peer-to-peer path. A relayed peer goes through a DERP
	// server, which is fine for audio and awful for pointer latency.
	Direct bool

	// hostName is the machine's own name, which a config may use instead of
	// the MagicDNS label in Host.
	hostName string
}

// Runner executes an external command, injected so tests never need a real
// tailnet. Mirrors internal/service.Runner.
type Runner interface {
	Run(name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	procx.Hide(cmd)
	return cmd.Output()
}

type Tailscale struct {
	runner Runner
	// bin is resolved once: on Windows the CLI is usually not on PATH.
	bin string
	// sleep is injected so the waiting tests finish instantly instead of
	// taking the real backoff. See wait.go.
	sleep func(context.Context, time.Duration) error
}

func NewTailscale() *Tailscale {
	return &Tailscale{runner: execRunner{}, bin: tailscaleBinary(), sleep: realSleep}
}

func NewTailscaleWithRunner(r Runner) *Tailscale {
	return &Tailscale{runner: r, bin: "tailscale", sleep: realSleep}
}

// status mirrors the subset of `tailscale status --json` we depend on.
type status struct {
	BackendState string
	Self         node
	Peer         map[string]node
}

type node struct {
	HostName     string
	DNSName      string
	TailscaleIPs []string
	OS           string
	Online       bool
	CurAddr      string // set only while a direct path is established
	Relay        string // DERP region when relayed
}

// looksMissing recognises "the program is not there" across both platforms'
// wordings, since the underlying errno does not survive into the message the
// os/exec wrapper produces.
func looksMissing(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "cannot find the file") ||
		strings.Contains(msg, "no such file")
}

func (t *Tailscale) status() (*status, error) {
	out, err := t.runner.Run(t.bin, "status", "--json")
	if err != nil {
		if looksMissing(err) {
			// Windows renders this as "the system cannot find the file
			// specified", which reads like a missing config file rather than
			// a missing program — worth saying plainly which is which.
			return nil, fmt.Errorf(
				"transport: the Tailscale CLI could not be run as %q. "+
					"Install Tailscale, or put its directory on PATH "+
					`(Windows installs to C:\Program Files\Tailscale): %w`, t.bin, err)
		}
		return nil, fmt.Errorf("transport: running tailscale status: %w", err)
	}

	var s status
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, fmt.Errorf("transport: parsing tailscale status: %w", err)
	}

	switch s.BackendState {
	case "Running":
		return &s, nil
	case "NeedsLogin", "NoState":
		return nil, fmt.Errorf("%w: run `sudo tailscale up` and authenticate", ErrNotLoggedIn)
	default:
		return nil, fmt.Errorf("transport: tailscale backend is %q, want Running", s.BackendState)
	}
}

// Self returns this machine's tailnet IPv4.
func (t *Tailscale) Self() (string, error) {
	s, err := t.status()
	if err != nil {
		return "", err
	}
	ip := ipv4Of(s.Self)
	if ip == "" {
		return "", errors.New("transport: this node has no tailnet IPv4")
	}
	return ip, nil
}

// Peer resolves a peer by hostname or MagicDNS name, both case-insensitively.
func (t *Tailscale) Peer(name string) (Peer, error) {
	s, err := t.status()
	if err != nil {
		return Peer{}, err
	}

	want := strings.ToLower(strings.TrimSuffix(name, "."))
	for _, n := range s.Peer {
		if !matches(n, want) {
			continue
		}
		ip := ipv4Of(n)
		if ip == "" {
			return Peer{}, fmt.Errorf("transport: peer %q has no tailnet IPv4", n.HostName)
		}
		return peerOf(n, ip), nil
	}

	return Peer{}, fmt.Errorf("%w: %q; known peers: %s",
		ErrPeerNotFound, name, strings.Join(hostnames(s.Peer), ", "))
}

// SolePeer resolves the machine to link with when the user named none.
//
// It exists so the Windows side needs no configuration file at all: with one
// binary and nothing else on that machine, there is nothing to copy wrong. The
// rule is deliberately strict — exactly one candidate, or an error naming the
// alternatives — because guessing wrong here means typing into the wrong
// computer.
//
// Phones and tablets are excluded: they cannot run the link, and letting an
// iPhone that happens to be awake make the choice ambiguous would break
// discovery for no reason.
func (t *Tailscale) SolePeer() (Peer, error) {
	s, err := t.status()
	if err != nil {
		return Peer{}, err
	}

	var candidates []Peer
	var skipped []string
	for _, n := range s.Peer {
		if !n.Online {
			continue
		}
		ip := ipv4Of(n)
		if ip == "" {
			continue
		}
		p := peerOf(n, ip)
		if !canRunTheLink(n.OS) {
			skipped = append(skipped, p.Host)
			continue
		}
		candidates = append(candidates, p)
	}

	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		detail := ""
		if len(skipped) > 0 {
			sort.Strings(skipped)
			detail = fmt.Sprintf(" (ignoring %s: not a computer)", strings.Join(skipped, ", "))
		}
		return Peer{}, fmt.Errorf(
			"%w: no other computer on your tailnet is online%s; "+
				"start the other machine, or name it with --peer", ErrPeerNotFound, detail)
	default:
		names := make([]string, 0, len(candidates))
		for _, p := range candidates {
			names = append(names, p.Host)
		}
		sort.Strings(names)
		return Peer{}, fmt.Errorf(
			"more than one computer is online (%s); say which one with --peer",
			strings.Join(names, ", "))
	}
}

// canRunTheLink reports whether a node is a machine with a keyboard and mouse
// to share. Tailscale's OS strings are lowercase for the desktops and
// mixed-case for the phones, so the comparison is case-insensitive.
func canRunTheLink(os string) bool {
	switch strings.ToLower(os) {
	case "linux", "windows", "macos":
		return true
	}
	return false
}

func peerOf(n node, ip string) Peer {
	return Peer{
		Host:   displayName(n),
		IP:     ip,
		OS:     n.OS,
		Online: n.Online,
		Direct: n.CurAddr != "",

		hostName: n.HostName,
	}
}

// displayName is the name the user recognises: the first label of the MagicDNS
// name. Raw HostName is a poor substitute — an iPhone on this very tailnet
// reports HostName "localhost", which names nothing useful in an error.
func displayName(n node) string {
	if dns := strings.TrimSuffix(n.DNSName, "."); dns != "" {
		if i := strings.Index(dns, "."); i > 0 {
			return dns[:i]
		}
		return dns
	}
	return n.HostName
}

func matches(n node, want string) bool {
	if strings.ToLower(n.HostName) == want {
		return true
	}
	dns := strings.ToLower(strings.TrimSuffix(n.DNSName, "."))
	// Accept both the full MagicDNS name and its leading label.
	return dns != "" && (dns == want || strings.HasPrefix(dns, want+"."))
}

// ipv4Of picks the 100.x address; the list also holds a fd7a:: IPv6.
func ipv4Of(n node) string {
	for _, ip := range n.TailscaleIPs {
		if !strings.Contains(ip, ":") {
			return ip
		}
	}
	return ""
}

func hostnames(peers map[string]node) []string {
	out := make([]string, 0, len(peers))
	for _, n := range peers {
		out = append(out, displayName(n))
	}
	sort.Strings(out) // map order is random; a stable message is easier to read
	return out
}
