//go:build linux

package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ViitoJooj/synk/internal/audio"
	"github.com/ViitoJooj/synk/internal/transport"
)

// fakeSource stands in for the PipeWire monitor.
type fakeSource struct{ frames chan []byte }

func (f *fakeSource) Frames() <-chan []byte { return f.frames }
func (f *fakeSource) Dropped() uint64       { return 0 }

// sockets returns a bound receiver and a sender connected to it, both on
// loopback, which transport allows precisely so tests need no tailnet.
func sockets(t *testing.T) (*net.UDPConn, *net.UDPConn) {
	t.Helper()

	server, err := transport.ListenAudio("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("ListenAudio: %v", err)
	}
	t.Cleanup(func() { server.Close() })

	client, err := transport.DialAudio("127.0.0.1", server.LocalAddr().(*net.UDPAddr).Port)
	if err != nil {
		t.Fatalf("DialAudio: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	return server, client
}

func silentFrame() []byte { return make([]byte, audio.FrameBytes(audio.Stereo)) }

// toneFrame is a frame that is not silence, so a test can tell real audio from
// an empty buffer.
func toneFrame(v int16) []byte {
	f := silentFrame()
	for i := 0; i+1 < len(f); i += 2 {
		binary.LittleEndian.PutUint16(f[i:], uint16(v))
	}
	return f
}

// Nothing may be sent before the peer has spoken. The peer's source port is
// whatever Windows chose, so it has to be learned — and until it is, there is
// nowhere to send.
func TestPlaybackWaitsForThePeerToAsk(t *testing.T) {
	server, client := sockets(t)
	src := &fakeSource{frames: make(chan []byte, 8)}
	var peer atomic.Pointer[net.UDPAddr]

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &session{out: io.Discard}
	go s.sendPlayback(ctx, server, src, &peer)

	src.frames <- toneFrame(1000)

	client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, err := client.Read(make([]byte, 2048)); err == nil {
		t.Fatalf("received %d bytes before asking for them", n)
	}
}

// Once the peer has been heard from, its audio flows.
func TestPlaybackFlowsAfterTheKeepalive(t *testing.T) {
	server, client := sockets(t)
	src := &fakeSource{frames: make(chan []byte, 8)}
	var peer atomic.Pointer[net.UDPAddr]

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &session{out: io.Discard}
	go s.readAudio(server, "127.0.0.1", &peer)
	go s.sendPlayback(ctx, server, src, &peer)

	if _, err := client.Write(audio.Encode(nil, audio.KindKeepalive, 0, nil)); err != nil {
		t.Fatalf("keepalive: %v", err)
	}
	waitForPeer(t, &peer)

	want := toneFrame(1234)
	src.frames <- want

	buf := make([]byte, 2048)
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("reading playback: %v", err)
	}

	kind, seq, pcm, err := audio.Decode(buf[:n])
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if kind != audio.KindPlayback {
		t.Errorf("kind = %s, want playback", kind)
	}
	if seq != 0 {
		t.Errorf("seq = %d, want the stream to start at 0", seq)
	}
	if !bytes.Equal(pcm, want) {
		t.Error("the audio that arrived is not the audio that was captured")
	}
	// The datagram has to fit the tailnet's MTU, or one lost fragment costs
	// the whole frame.
	if n > 1280 {
		t.Errorf("datagram is %d bytes, over the 1280-byte tailnet MTU", n)
	}
}

// A datagram from anything but the peer must not redirect the stream: it would
// be a way to have this machine's sound delivered somewhere else.
func TestPlaybackIgnoresDatagramsFromStrangers(t *testing.T) {
	server, _ := sockets(t)
	var peer atomic.Pointer[net.UDPAddr]

	s := &session{out: io.Discard}
	// The peer is declared to be some other address entirely, so the loopback
	// sender below is a stranger.
	go s.readAudio(server, "100.64.1.12", &peer)

	stranger, err := transport.DialAudio("127.0.0.1", server.LocalAddr().(*net.UDPAddr).Port)
	if err != nil {
		t.Fatalf("DialAudio: %v", err)
	}
	defer stranger.Close()

	if _, err := stranger.Write(audio.Encode(nil, audio.KindKeepalive, 0, nil)); err != nil {
		t.Fatalf("write: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	if addr := peer.Load(); addr != nil {
		t.Fatalf("a stranger at %s was accepted as the peer", addr)
	}
}

func waitForPeer(t *testing.T, peer *atomic.Pointer[net.UDPAddr]) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if peer.Load() != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the keepalive never registered a peer to send to")
}
