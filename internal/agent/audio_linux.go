//go:build linux

package agent

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/ViitoJooj/synk/internal/audio"
	"github.com/ViitoJooj/synk/internal/transport"
)

const (
	// monitorBuffer holds captured frames waiting to go out. At 5 ms a frame
	// this is two seconds of slack, which a scheduling hiccup will never use
	// and a stalled network should not be allowed to grow past.
	monitorBuffer = 400

	// audioReadBuffer takes the largest datagram the peer can send, with room
	// to notice one that is too large rather than silently truncating it.
	audioReadBuffer = 2048

	// statsEvery paces the one line that says whether audio is flowing.
	audioStatsEvery = 30 * time.Second
)

// frameSource is where playback comes from, narrowed to what the send loop
// uses so it can be driven by a test without a sound server.
type frameSource interface {
	Frames() <-chan []byte
	Dropped() uint64
}

// runPlayback captures whatever this machine is playing and sends it to the
// peer's speakers.
//
// The socket is bound here and the peer dials in, mirroring the control
// connection and for the same reason: an unsolicited inbound datagram to the
// Windows machine is dropped unless an administrator has added a firewall rule,
// while its own outbound keepalives open the return path for free. So this side
// does not know where to send until the peer has spoken, and that is by design.
func (s *session) runPlayback(ctx context.Context, self, peerIP string, port int) {
	conn, err := transport.ListenAudio(self, port)
	if err != nil {
		fmt.Fprintf(s.out, "audio off: %v\n", err)
		return
	}
	defer conn.Close()

	monitor, err := audio.OpenMonitor(monitorBuffer)
	if err != nil {
		fmt.Fprintf(s.out, "audio off: %v\n", err)
		return
	}
	defer monitor.Close()

	// Enquanto o som vai para o peer, ele não sai também aqui. O monitor do
	// sink continua carregando o áudio mesmo mudo — medido —, então isto cala
	// as caixas sem afetar o que é enviado.
	if err := monitor.SilenceLocal(); err != nil {
		fmt.Fprintf(s.out, "audio: could not silence this machine, sound will play in both places: %v\n", err)
	} else {
		fmt.Fprintln(s.out, "audio: this machine's speakers muted while the peer is playing")
	}

	s.st.audio("waiting")
	fmt.Fprintf(s.out, "audio: capturing this machine's output, waiting for %s to ask for it\n", peerIP)

	// Closing the socket is what unblocks both loops.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	var peer atomic.Pointer[net.UDPAddr]
	go s.readAudio(conn, peerIP, &peer)
	s.sendPlayback(ctx, conn, monitor, &peer)
}

// readAudio learns where to send, and is where the peer's own streams will
// arrive when the microphone direction is built.
//
// The peer's source port is whatever its operating system chose, so it has to
// be learned rather than assumed; only its address is checked.
func (s *session) readAudio(conn *net.UDPConn, peerIP string, peer *atomic.Pointer[net.UDPAddr]) {
	buf := make([]byte, audioReadBuffer)
	announced := false

	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return // the socket was closed; the session is going away
		}
		if !transport.FromPeer(from, peerIP) {
			continue // not our peer: silently ignored, like any stray datagram
		}

		kind, _, _, err := audio.Decode(buf[:n])
		if err != nil {
			continue
		}
		if kind != audio.KindKeepalive {
			continue // nothing else arrives here yet
		}

		peer.Store(from)
		if !announced {
			announced = true
			s.st.audio("flowing")
			fmt.Fprintf(s.out, "audio: %s is listening; playback is flowing\n", from)
		}
	}
}

// sendPlayback forwards every captured frame to the peer, once there is a peer
// to send it to.
func (s *session) sendPlayback(ctx context.Context, conn *net.UDPConn, monitor frameSource, peer *atomic.Pointer[net.UDPAddr]) {
	var (
		seq  uint32
		out  []byte
		sent uint64
	)

	stats := time.NewTicker(audioStatsEvery)
	defer stats.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-stats.C:
			if dropped := monitor.Dropped(); dropped > 0 {
				// Frames are dropped when the sender cannot keep up, which is
				// audible as gaps rather than as a delay.
				fmt.Fprintf(s.out, "audio: sent %d frames, dropped %d\n", sent, dropped)
			}

		case frame, ok := <-monitor.Frames():
			if !ok {
				return
			}
			addr := peer.Load()
			if addr == nil {
				continue // nobody has asked yet; capturing costs nothing
			}
			out = audio.Encode(out, audio.KindPlayback, seq, frame)
			seq++
			if _, err := conn.WriteToUDP(out, addr); err != nil {
				return
			}
			sent++
		}
	}
}
