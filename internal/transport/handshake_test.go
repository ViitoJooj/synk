package transport

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ViitoJooj/synk/internal/proto"
)

// ---- peer authorisation ----

// fakeAddr lets a test present any remote address without needing a machine at
// that address to exist.
type fakeAddr string

func (fakeAddr) Network() string      { return "tcp" }
func (a fakeAddr) String() string     { return string(a) }
func fakeConn(remote string) net.Conn { return &addrConn{remote: fakeAddr(remote)} }

type addrConn struct {
	net.Conn
	remote net.Addr
}

func (c *addrConn) RemoteAddr() net.Addr { return c.remote }

// The peer's tailnet address is the whole of the authentication, so this is the
// test that stands where the pre-shared key used to.
func TestAuthorizePeerAcceptsThePeersAddress(t *testing.T) {
	if err := AuthorizePeer(fakeConn("100.64.1.12:51234"), "100.64.1.12"); err != nil {
		t.Errorf("AuthorizePeer rejected the peer: %v", err)
	}
}

// Another device on the same tailnet is exactly what this check exists to keep
// out: it is inside WireGuard, so nothing else stops it.
func TestAuthorizePeerRejectsAnotherTailnetDevice(t *testing.T) {
	err := AuthorizePeer(fakeConn("100.85.60.50:41000"), "100.64.1.12")
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
	// The message has to name who actually connected, or a genuine
	// misconfiguration is impossible to tell from an intrusion.
	if !strings.Contains(err.Error(), "100.85.60.50") {
		t.Errorf("err = %q, want it to name the address that connected", err)
	}
}

func TestAuthorizePeerRejectsOffTailnetAddresses(t *testing.T) {
	for _, remote := range []string{"10.0.0.23:5000", "127.0.0.1:5000", "8.8.8.8:5000"} {
		if err := AuthorizePeer(fakeConn(remote), "100.64.1.12"); !errors.Is(err, ErrAuthFailed) {
			t.Errorf("AuthorizePeer(%s) err = %v, want ErrAuthFailed", remote, err)
		}
	}
}

func TestAuthorizePeerRejectsAnUnusablePeerAddress(t *testing.T) {
	if err := AuthorizePeer(fakeConn("100.64.1.12:51234"), ""); err == nil {
		t.Error("an empty peer address was accepted")
	}
}

// ---- hello ----

// runHello drives both halves over an in-memory pipe.
func runHello(t *testing.T, edge proto.Edge) (proto.Edge, error, error) {
	t.Helper()
	s, c := net.Pipe()
	defer s.Close()
	defer c.Close()

	done := make(chan error, 1)
	go func() { done <- ServerHello(s, edge) }()

	got, clientErr := ClientHello(c)
	if clientErr != nil {
		c.Close()
	}
	select {
	case serverErr := <-done:
		return got, serverErr, clientErr
	case <-time.After(3 * time.Second):
		t.Fatal("ServerHello never returned")
		return 0, nil, nil
	}
}

// The dialling machine carries no configuration, so the edge it needs has to
// arrive here or it has no way to know which border to watch.
func TestHelloCarriesTheListenersEdge(t *testing.T) {
	for _, edge := range []proto.Edge{proto.EdgeLeft, proto.EdgeRight, proto.EdgeTop, proto.EdgeBottom} {
		got, serverErr, clientErr := runHello(t, edge)
		if serverErr != nil || clientErr != nil {
			t.Fatalf("edge %s: server = %v, client = %v", edge, serverErr, clientErr)
		}
		if got != edge {
			t.Errorf("client got edge %s, want %s", got, edge)
		}
	}
}

// A version mismatch has to be reported on both sides: whoever is looking at
// either machine should be told to copy the same build across.
func TestHelloRejectsAVersionMismatchOnBothSides(t *testing.T) {
	s, c := net.Pipe()
	defer s.Close()
	defer c.Close()

	done := make(chan error, 1)
	go func() { done <- ServerHello(s, proto.EdgeRight) }()

	// A peer from a future build: it reads our hello and answers with a
	// version we do not speak.
	go func() {
		defer c.Close()
		_, _, _ = proto.ReadFrame(c)
		_ = proto.WriteFrame(c, proto.MsgHello, []byte{protocolVersion + 99})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrVersionMismatch) {
			t.Fatalf("err = %v, want ErrVersionMismatch", err)
		}
		if !strings.Contains(err.Error(), fmt.Sprint(protocolVersion)) {
			t.Errorf("err = %q, want it to name this build's version", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ServerHello never returned")
	}
}

func TestClientHelloRejectsAVersionItDoesNotSpeak(t *testing.T) {
	s, c := net.Pipe()
	defer s.Close()
	defer c.Close()

	go func() {
		defer s.Close()
		_ = proto.WriteFrame(s, proto.MsgHello, []byte{protocolVersion + 99, byte(proto.EdgeRight)})
	}()

	if _, err := ClientHello(c); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("err = %v, want ErrVersionMismatch", err)
	}
}

// An edge is used to index screen geometry, so a byte off the wire is checked
// before it is trusted.
func TestClientHelloRejectsAnImpossibleEdge(t *testing.T) {
	s, c := net.Pipe()
	defer s.Close()
	defer c.Close()

	go func() {
		defer s.Close()
		_ = proto.WriteFrame(s, proto.MsgHello, []byte{protocolVersion, 99})
	}()

	if _, err := ClientHello(c); err == nil {
		t.Fatal("an out-of-range edge was accepted")
	}
}

func TestHelloRejectsAFrameThatIsNotAHello(t *testing.T) {
	s, c := net.Pipe()
	defer s.Close()
	defer c.Close()

	go func() {
		defer s.Close()
		_ = proto.WriteFrame(s, proto.MsgKey, proto.KeyEvent{Code: 30, Pressed: true}.Encode())
	}()

	if _, err := ClientHello(c); err == nil {
		t.Fatal("a key frame was accepted as a hello")
	}
}

// ---- address safety ----

// The whole security model rests on being reachable only over the tailnet.
// Falling back to a wildcard bind when Tailscale is down would silently expose
// input injection to the local network.
func TestListenRefusesWildcardAddresses(t *testing.T) {
	for _, addr := range []string{"0.0.0.0", "", "::", "[::]"} {
		l, err := Listen(addr, 0)
		if err == nil {
			l.Close()
			t.Errorf("Listen(%q) succeeded; want it refused", addr)
			continue
		}
		if !errors.Is(err, ErrUnsafeAddress) {
			t.Errorf("Listen(%q) err = %v, want ErrUnsafeAddress", addr, err)
		}
	}
}

// Only the tailnet range is acceptable. A LAN address would work but would not
// be protected by WireGuard.
func TestListenRefusesNonTailnetAddress(t *testing.T) {
	if l, err := Listen("10.0.0.23", 0); err == nil {
		l.Close()
		t.Error("Listen on a LAN address succeeded; want it refused")
	} else if !errors.Is(err, ErrUnsafeAddress) {
		t.Errorf("err = %v, want ErrUnsafeAddress", err)
	}
}

// Dialling is held to the same rule as binding: a link routed outside the
// tailnet has none of the confidentiality the design assumes.
func TestDialRefusesNonTailnetAddress(t *testing.T) {
	if conn, err := Dial("10.0.0.23", 27431); err == nil {
		conn.Close()
		t.Error("Dial to a LAN address succeeded; want it refused")
	} else if !errors.Is(err, ErrUnsafeAddress) {
		t.Errorf("err = %v, want ErrUnsafeAddress", err)
	}
}

func TestListenAcceptsLoopbackForTests(t *testing.T) {
	// 127.0.0.1 is allowed so the integration tests can run without a tailnet.
	l, err := Listen("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("Listen on loopback: %v", err)
	}
	defer l.Close()
}

func TestIsTailnetAddressMatchesTheCGNATRange(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"100.64.0.0", true},
		{"100.101.102.103", true},
		{"100.127.255.255", true},
		{"100.63.255.255", false}, // just below the range
		{"100.128.0.0", false},    // just above it
		{"10.0.0.23", false},
		{"10.0.0.1", false},
		{"not an ip", false},
	}
	for _, tt := range tests {
		if got := isTailnetAddress(tt.ip); got != tt.want {
			t.Errorf("isTailnetAddress(%q) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

// ---- end to end over a real socket ----

func TestHelloOverRealTCPConnection(t *testing.T) {
	l, err := Listen("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	done := make(chan error, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		done <- ServerHello(conn, proto.EdgeRight)
	}()

	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	edge, err := ClientHello(conn)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if edge != proto.EdgeRight {
		t.Errorf("edge = %s, want right", edge)
	}
	if err := <-done; err != nil {
		t.Fatalf("server: %v", err)
	}
}

// A port already taken by another program is a real scenario, not a hypothetical:
// Deskflow holds 24800 on this user's PC, and the bare "address already in use"
// gives no clue which program to blame or what to do.
func TestListenExplainsAPortAlreadyInUse(t *testing.T) {
	first, err := Listen("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("first Listen: %v", err)
	}
	defer first.Close()

	port := first.Addr().(*net.TCPAddr).Port

	_, err = Listen("127.0.0.1", port)

	if err == nil {
		t.Fatal("second Listen succeeded, want it refused")
	}
	if !errors.Is(err, ErrPortInUse) {
		t.Fatalf("err = %v, want ErrPortInUse", err)
	}
	// The message has to name the port and point at finding the culprit.
	if !strings.Contains(err.Error(), fmt.Sprint(port)) {
		t.Errorf("err = %q, want it to name port %d", err, port)
	}
	if !strings.Contains(err.Error(), "another program") {
		t.Errorf("err = %q, want it to say another program holds the port", err)
	}
}
