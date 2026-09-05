package audio

import (
	"bytes"
	"errors"
	"testing"
)

func TestPacketRoundTrips(t *testing.T) {
	pcm := bytes.Repeat([]byte{0x11, 0x22}, FrameBytes(Stereo)/2)

	kind, seq, got, err := Decode(Encode(nil, KindPlayback, 4242, pcm))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if kind != KindPlayback {
		t.Errorf("kind = %s, want playback", kind)
	}
	if seq != 4242 {
		t.Errorf("seq = %d, want 4242", seq)
	}
	if !bytes.Equal(got, pcm) {
		t.Error("the PCM did not survive the round trip")
	}
}

// The sender reuses one buffer at two hundred datagrams a second, so encoding
// must not carry anything over from the packet before.
func TestEncodeReusesItsBufferCleanly(t *testing.T) {
	buf := Encode(nil, KindPlayback, 1, bytes.Repeat([]byte{0xFF}, 64))

	buf = Encode(buf, KindMic, 2, []byte{0x01, 0x02})

	kind, seq, pcm, err := Decode(buf)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if kind != KindMic || seq != 2 {
		t.Errorf("got kind %s seq %d, want mic 2", kind, seq)
	}
	if !bytes.Equal(pcm, []byte{0x01, 0x02}) {
		t.Errorf("pcm = %v, want the second packet's two bytes only", pcm)
	}
}

// A keepalive carries no audio, and must decode as a valid empty packet rather
// than as a truncated one.
func TestKeepaliveHasNoPayload(t *testing.T) {
	kind, _, pcm, err := Decode(Encode(nil, KindKeepalive, 0, nil))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if kind != KindKeepalive {
		t.Errorf("kind = %s, want keepalive", kind)
	}
	if len(pcm) != 0 {
		t.Errorf("pcm = %v, want empty", pcm)
	}
}

// Datagrams arrive from the network. A runt one must be an error, not an index
// out of range.
func TestDecodeRejectsARuntDatagram(t *testing.T) {
	for n := range HeaderBytes {
		if _, _, _, err := Decode(make([]byte, n)); !errors.Is(err, ErrShortPacket) {
			t.Errorf("Decode(%d bytes) err = %v, want ErrShortPacket", n, err)
		}
	}
}
