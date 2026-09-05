package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Kind tags what a datagram carries.
type Kind uint8

const (
	// KindKeepalive carries no audio. It exists so the machine behind the
	// stricter firewall speaks first: an inbound UDP datagram to a Windows
	// machine is dropped unless that machine's own outbound traffic has
	// already opened the path, and nothing here may require an administrator
	// to add a firewall rule.
	KindKeepalive Kind = iota

	// KindPlayback is what this machine is playing, on its way to the peer's
	// speakers.
	KindPlayback

	// KindMic is the peer's microphone, on its way here.
	KindMic
)

// HeaderBytes is the fixed prefix on every datagram: one byte of kind and a
// four-byte sequence number.
const HeaderBytes = 5

// ErrShortPacket reports a datagram too small to carry a header.
var ErrShortPacket = errors.New("audio: datagram is shorter than its header")

func (k Kind) String() string {
	switch k {
	case KindKeepalive:
		return "keepalive"
	case KindPlayback:
		return "playback"
	case KindMic:
		return "mic"
	}
	return fmt.Sprintf("kind(%d)", uint8(k))
}

// Encode lays out one datagram. The buffer is reused across calls by the
// sender, so it is passed in rather than allocated a hundred times a second.
func Encode(buf []byte, kind Kind, seq uint32, pcm []byte) []byte {
	buf = append(buf[:0], byte(kind))
	buf = binary.BigEndian.AppendUint32(buf, seq)
	return append(buf, pcm...)
}

// Decode splits a received datagram. The PCM aliases the caller's buffer, which
// the jitter buffer copies; nothing else may hold on to it.
func Decode(b []byte) (Kind, uint32, []byte, error) {
	if len(b) < HeaderBytes {
		return 0, 0, nil, fmt.Errorf("%w: %d bytes", ErrShortPacket, len(b))
	}
	return Kind(b[0]), binary.BigEndian.Uint32(b[1:]), b[HeaderBytes:], nil
}
