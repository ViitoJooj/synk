package transport

import (
	"fmt"
	"net"
)

// Audio carries the PCM streams. It is UDP because a late audio packet is worth
// dropping while a late key-up is not — the opposite of what the control
// connection needs, which is why the two do not share a socket.
//
// Both ends are held to the same rule as the control connection: only the
// tailnet. WireGuard is what makes the stream private, and a datagram routed
// outside it would not be.

// ListenAudio binds the receiving socket, refusing any address that is not on
// the tailnet for the same reason Listen does.
func ListenAudio(ip string, port int) (*net.UDPConn, error) {
	if err := checkSafeAddress(ip, "bind"); err != nil {
		return nil, err
	}
	addr := &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("transport: binding the audio socket on %s: %w", ip, err)
	}
	return conn, nil
}

// DialAudio opens the sending socket, connected to the peer.
//
// The machine behind the stricter firewall must be the one to dial. Windows
// drops an unsolicited inbound datagram unless a rule has been added for it,
// which needs an administrator; a datagram sent from there first opens the path
// for the replies, and needs nothing.
func DialAudio(ip string, port int) (*net.UDPConn, error) {
	if err := checkSafeAddress(ip, "dial"); err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP(ip), Port: port})
	if err != nil {
		return nil, fmt.Errorf("transport: opening the audio socket to %s: %w", ip, err)
	}
	return conn, nil
}

// FromPeer reports whether a datagram came from the peer's tailnet address.
//
// UDP has no connection to authenticate, so every datagram is checked. The
// check is the same one the control connection makes, and rests on the same
// property: Tailscale accepts from a peer only the addresses in its
// allowed-IPs, so no node can present another's.
func FromPeer(addr *net.UDPAddr, peerIP string) bool {
	want := net.ParseIP(peerIP)
	return want != nil && addr != nil && addr.IP.Equal(want)
}
