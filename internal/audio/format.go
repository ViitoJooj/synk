package audio

import "encoding/binary"

// Channels selects the layout of a stream. Playback is stereo; the microphone
// is mono, which halves its bandwidth for no perceptible loss.
type Channels int

const (
	Mono   Channels = 1
	Stereo Channels = 2
)

const (
	// SampleRate is fixed at 48 kHz: it is what PipeWire and WASAPI both run
	// natively here, so neither side has to resample.
	SampleRate = 48000

	// FrameMS sizes one datagram, and 5 ms is chosen by the tailnet's MTU
	// rather than by taste. tailscale0 carries 1280 bytes; a 10 ms stereo
	// frame is 1920 and would be split into two IP fragments, so losing
	// either fragment would cost the whole frame. At 5 ms a stereo frame is
	// 960 bytes and one datagram is one frame, which is also what lets a lost
	// packet cost exactly 5 ms of audio.
	FrameMS = 5

	bytesPerSample = 2 // signed 16-bit
)

// FrameBytes is the size of one FrameMS packet payload.
func FrameBytes(c Channels) int { return SamplesPerFrame(c) * bytesPerSample }

// SamplesPerFrame counts interleaved samples across all channels.
func SamplesPerFrame(c Channels) int { return SampleRate / (1000 / FrameMS) * int(c) }

// pcmToInt16 decodes little-endian s16 PCM. A trailing odd byte is dropped:
// a truncated datagram must not become a garbage sample.
func pcmToInt16(b []byte) []int16 {
	out := make([]int16, len(b)/bytesPerSample)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*bytesPerSample:]))
	}
	return out
}

// int16ToPCM encodes little-endian s16 PCM.
func int16ToPCM(s []int16) []byte {
	out := make([]byte, len(s)*bytesPerSample)
	for i, v := range s {
		binary.LittleEndian.PutUint16(out[i*bytesPerSample:], uint16(v))
	}
	return out
}
