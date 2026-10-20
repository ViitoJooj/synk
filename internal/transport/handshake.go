package transport

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	"github.com/ViitoJooj/synk/internal/proto"
)

const (
	protocolVersion = 2

	helloTimeout = 5 * time.Second

	// dialTimeout bounds one connection attempt. The peer being asleep is the
	// normal case, so this is short enough to keep the retry loop responsive.
	dialTimeout = 10 * time.Second
)

var (
	// ErrAuthFailed reports a connection from something that is not the peer.
	ErrAuthFailed = errors.New("transport: connection is not from the configured peer")

	// ErrUnsafeAddress reports an address outside the tailnet, whether we were
	// asked to bind it or to dial it.
	ErrUnsafeAddress = errors.New("transport: refusing an address outside the tailnet")

	ErrPortInUse = errors.New("transport: port already in use")

	// ErrVersionMismatch reports two builds that cannot talk to each other.
	ErrVersionMismatch = errors.New("transport: protocol version mismatch")
)

// tailnet is Tailscale's slice of the CGNAT range, 100.64.0.0/10.
var tailnet = &net.IPNet{
	IP:   net.IPv4(100, 64, 0, 0),
	Mask: net.CIDRMask(10, 32),
}

// AuthorizePeer accepts a connection only from the peer's tailnet address.
//
// This is the whole of the authentication, and it is enough. WireGuard decrypts
// a packet only with the key of the peer it was sent by, and Tailscale accepts
// from that peer only the addresses in its allowed-IPs — so one node on a
// tailnet cannot present itself with another node's 100.x address. "This came
// from the address `tailscale status` says is the peer" is therefore a
// cryptographic statement about identity, not a guess.
//
// An earlier version layered an HMAC pre-shared key on top. It protected
// against nothing this does not, since the tailnet holds only this user's own
// devices, and it cost a key file that had to be identical on both machines —
// whose mismatch surfaced as an authentication failure that read like a network
// fault.
func AuthorizePeer(conn net.Conn, peerIP string) error {
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return fmt.Errorf("transport: reading the remote address: %w", err)
	}

	got, want := net.ParseIP(host), net.ParseIP(peerIP)
	if want == nil {
		return fmt.Errorf("transport: %q is not a usable peer address", peerIP)
	}
	if got == nil || !got.Equal(want) {
		return fmt.Errorf("%w: %s connected, expected %s", ErrAuthFailed, host, peerIP)
	}
	return nil
}

// ServerHello is the listener's half of the opening exchange: it fixes the
// protocol version and tells the dialling machine which edge of this screen the
// peer sits beyond.
//
// Sending the edge is what lets the other machine run with no configuration at
// all. It also removes a class of bug outright: when both sides carried their
// own edge setting, two files could disagree and the pointer would leave by one
// border and be expected at another.
func ServerHello(conn net.Conn, edge proto.Edge) error {
	defer conn.SetDeadline(time.Time{})
	if err := conn.SetDeadline(time.Now().Add(helloTimeout)); err != nil {
		return err
	}

	if err := proto.WriteFrame(conn, proto.MsgHello, []byte{protocolVersion, byte(edge)}); err != nil {
		return fmt.Errorf("transport: sending hello: %w", err)
	}

	typ, payload, err := proto.ReadFrame(conn)
	if err != nil {
		return fmt.Errorf("transport: reading the peer's hello: %w", err)
	}
	if typ != proto.MsgHello {
		return fmt.Errorf("transport: expected a hello, got frame type %d", typ)
	}
	if len(payload) < 1 {
		return errors.New("transport: the peer's hello carried no version")
	}
	return checkVersion(payload[0])
}

// ClientHello is the dialler's half. It returns the edge of the *listener's*
// screen that this machine sits beyond, which is all the geometry this side
// needs: its own handoff border is the opposite one.
func ClientHello(conn net.Conn) (proto.Edge, error) {
	defer conn.SetDeadline(time.Time{})
	if err := conn.SetDeadline(time.Now().Add(helloTimeout)); err != nil {
		return 0, err
	}

	typ, payload, err := proto.ReadFrame(conn)
	if err != nil {
		return 0, fmt.Errorf("transport: reading the peer's hello: %w", err)
	}
	if typ != proto.MsgHello {
		return 0, fmt.Errorf("transport: expected a hello, got frame type %d", typ)
	}
	if len(payload) < 2 {
		return 0, fmt.Errorf("transport: hello is %d bytes, want 2", len(payload))
	}
	if err := checkVersion(payload[0]); err != nil {
		return 0, err
	}

	edge := proto.Edge(payload[1])
	if !edge.Valid() {
		return 0, fmt.Errorf("transport: the peer named edge %d, which is not a border", payload[1])
	}

	if err := proto.WriteFrame(conn, proto.MsgHello, []byte{protocolVersion}); err != nil {
		return 0, fmt.Errorf("transport: sending hello: %w", err)
	}
	return edge, nil
}

// checkVersion reports a peer running an incompatible build, naming both
// versions: the fix is to copy the newer binary across, and that is only
// obvious if the message says which side is behind.
func checkVersion(got byte) error {
	if got != protocolVersion {
		return fmt.Errorf("%w: the peer speaks version %d, this build speaks %d; "+
			"copy the same build to both machines", ErrVersionMismatch, got, protocolVersion)
	}
	return nil
}

// Listen binds a TCP listener, and only to an address that is actually
// protected.
//
// This is the load-bearing security check. If Tailscale is down, the right
// answer is to fail: a silent fall back to 0.0.0.0 would expose keystroke
// injection to every machine on the café wifi.
func Listen(ip string, port int) (net.Listener, error) {
	if err := checkSafeAddress(ip, "bind"); err != nil {
		return nil, err
	}
	l, err := net.Listen("tcp", net.JoinHostPort(ip, fmt.Sprint(port)))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			// Worth spelling out: this is how a collision with another
			// keyboard-sharing tool presents, and "address already in use"
			// alone names neither the port nor a way to find the culprit.
			return nil, fmt.Errorf(
				"%w: port %d on %s is held by another program; "+
					"find it with `ss -ltnp | grep %d` (Linux) or "+
					"`Get-NetTCPConnection -LocalPort %d -State Listen` (Windows), "+
					"or set a different `port:` in your config",
				ErrPortInUse, port, ip, port, port)
		}
		return nil, fmt.Errorf("transport: listening on %s: %w", ip, err)
	}
	return l, nil
}

// Dial opens the control connection to the peer, refusing any address that is
// not on the tailnet for the same reason Listen does: the link's confidentiality
// is WireGuard's, and a connection routed outside it has none.
//
// It also turns Nagle off. Input cannot wait for a segment to fill: a 40 ms
// stall on a key-up is the difference between typing and a stuck key.
func Dial(ip string, port int) (net.Conn, error) {
	if err := checkSafeAddress(ip, "dial"); err != nil {
		return nil, err
	}

	addr := net.JoinHostPort(ip, fmt.Sprint(port))
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("transport: dialling %s: %w", addr, err)
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	return conn, nil
}

// NoDelay applies the same setting to an accepted connection.
func NoDelay(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
}

func checkSafeAddress(ip, verb string) error {
	if isTailnetAddress(ip) || isLoopback(ip) {
		return nil
	}
	return fmt.Errorf("%w: refusing to %s %q, which is not in 100.64.0.0/10", ErrUnsafeAddress, verb, ip)
}

func isTailnetAddress(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	v4 := parsed.To4()
	return v4 != nil && tailnet.Contains(v4)
}

// isLoopback allows the tests, and only the tests, to run without a tailnet.
func isLoopback(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}
