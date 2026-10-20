package transport

import (
	"errors"
	"net"
	"testing"
)

// The audio socket is held to the same rule as the control connection: a stream
// routed off the tailnet has none of the privacy the design assumes.
func TestAudioSocketsRefuseAddressesOffTheTailnet(t *testing.T) {
	for _, ip := range []string{"0.0.0.0", "10.0.0.23", "8.8.8.8", ""} {
		if conn, err := ListenAudio(ip, 0); err == nil {
			conn.Close()
			t.Errorf("ListenAudio(%q) succeeded; want it refused", ip)
		} else if !errors.Is(err, ErrUnsafeAddress) {
			t.Errorf("ListenAudio(%q) err = %v, want ErrUnsafeAddress", ip, err)
		}

		if conn, err := DialAudio(ip, 27431); err == nil {
			conn.Close()
			t.Errorf("DialAudio(%q) succeeded; want it refused", ip)
		} else if !errors.Is(err, ErrUnsafeAddress) {
			t.Errorf("DialAudio(%q) err = %v, want ErrUnsafeAddress", ip, err)
		}
	}
}

func TestAudioSocketBindsLoopbackForTests(t *testing.T) {
	conn, err := ListenAudio("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("ListenAudio: %v", err)
	}
	defer conn.Close()
}

// Every datagram is checked, because UDP offers no connection to authenticate
// once and trust after.
func TestFromPeerAcceptsOnlyThePeersAddress(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"100.64.1.12", true},
		{"100.85.60.50", false},  // another device on the same tailnet
		{"10.0.0.23", false}, // the LAN
		{"127.0.0.1", false},
	}
	for _, tt := range tests {
		addr := &net.UDPAddr{IP: net.ParseIP(tt.addr), Port: 41000}
		if got := FromPeer(addr, "100.64.1.12"); got != tt.want {
			t.Errorf("FromPeer(%s) = %v, want %v", tt.addr, got, tt.want)
		}
	}
	if FromPeer(nil, "100.64.1.12") {
		t.Error("FromPeer(nil) = true, want false")
	}
}

// A real datagram over a real socket, so the packet path is exercised end to
// end rather than only its address arithmetic.
func TestAudioDatagramCrossesALoopbackSocket(t *testing.T) {
	server, err := ListenAudio("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("ListenAudio: %v", err)
	}
	defer server.Close()

	client, err := DialAudio("127.0.0.1", server.LocalAddr().(*net.UDPAddr).Port)
	if err != nil {
		t.Fatalf("DialAudio: %v", err)
	}
	defer client.Close()

	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, 64)
	n, from, err := server.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("got %q, want hello", buf[:n])
	}
	if !FromPeer(from, "127.0.0.1") {
		t.Errorf("FromPeer(%s) = false for the sender's own address", from.IP)
	}
}
