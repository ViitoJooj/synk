package audio

import (
	"bytes"
	"testing"
)

// A frame plus its header has to fit one datagram on a link whose MTU is 1280,
// or a single lost IP fragment costs the whole frame. This is the arithmetic
// that decides FrameMS, so it is pinned rather than left to be rediscovered.
func TestFrameBytesFitsTheTailnetMTU(t *testing.T) {
	// 48000 Hz / 200 = 240 samples per channel per 5 ms frame, 2 bytes each.
	if got := FrameBytes(Stereo); got != 960 {
		t.Errorf("FrameBytes(Stereo) = %d, want 960", got)
	}
	if got := FrameBytes(Mono); got != 480 {
		t.Errorf("FrameBytes(Mono) = %d, want 480", got)
	}

	const tailnetMTU = 1280
	if got := FrameBytes(Stereo) + HeaderBytes; got > tailnetMTU {
		t.Errorf("a stereo datagram is %d bytes, over the %d-byte MTU; it would be fragmented",
			got, tailnetMTU)
	}
}

func TestSamplesPerFrame(t *testing.T) {
	if got := SamplesPerFrame(Stereo); got != 480 {
		t.Errorf("SamplesPerFrame(Stereo) = %d, want 480 (240 per channel, interleaved)", got)
	}
	if got := SamplesPerFrame(Mono); got != 240 {
		t.Errorf("SamplesPerFrame(Mono) = %d, want 240", got)
	}
}

// The wire is little-endian s16. Getting this backwards produces audio that is
// loud, wrong and unmistakable, so pin it.
func TestPCMToInt16IsLittleEndian(t *testing.T) {
	// 0x0100 little-endian is 256; big-endian would read 1.
	got := pcmToInt16([]byte{0x00, 0x01, 0xff, 0xff})

	want := []int16{256, -1}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample %d = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestInt16ToPCMIsLittleEndian(t *testing.T) {
	got := int16ToPCM([]int16{256, -1})

	if want := []byte{0x00, 0x01, 0xff, 0xff}; !bytes.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestPCMConversionRoundTrips(t *testing.T) {
	want := []int16{0, 1, -1, 32767, -32768, 1234, -4321}

	got := pcmToInt16(int16ToPCM(want))

	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample %d = %d, want %d", i, got[i], want[i])
		}
	}
}

// A datagram truncated in flight must not produce a half sample.
func TestPCMToInt16IgnoresTrailingOddByte(t *testing.T) {
	got := pcmToInt16([]byte{0x00, 0x01, 0xff})

	if len(got) != 1 {
		t.Errorf("len = %d, want 1 — the dangling byte should be dropped", len(got))
	}
}
