package audio

import (
	"bytes"
	"testing"
)

// 10 ms of 48 kHz stereo s16 is 1920 bytes; the tests use a small stand-in.
const frameBytes = 8

func newTestBuffer() *Jitter {
	return NewJitter(JitterConfig{FrameBytes: frameBytes, TargetDepth: 4, MaxDepth: 8})
}

func frame(fill byte) []byte { return bytes.Repeat([]byte{fill}, frameBytes) }

// Playing the moment the first packet lands would underrun on the next
// network hiccup, so the buffer fills to TargetDepth first.
func TestPrebuffersBeforePlaying(t *testing.T) {
	b := newTestBuffer()
	b.Push(0, frame(1))
	b.Push(1, frame(2))

	data, real := b.Pop()

	if real {
		t.Error("real = true, want false while still prebuffering")
	}
	if !bytes.Equal(data, make([]byte, frameBytes)) {
		t.Errorf("data = %v, want silence", data)
	}
}

func TestPlaysInOrderOnceFilled(t *testing.T) {
	b := newTestBuffer()
	for i := range 4 {
		b.Push(uint32(i), frame(byte(i+1)))
	}

	for i := range 4 {
		data, real := b.Pop()
		if !real {
			t.Fatalf("pop %d: real = false, want true", i)
		}
		if !bytes.Equal(data, frame(byte(i+1))) {
			t.Errorf("pop %d = %v, want %v", i, data, frame(byte(i+1)))
		}
	}
}

// UDP reorders freely; the buffer exists to undo that.
func TestReordersOutOfOrderPackets(t *testing.T) {
	b := newTestBuffer()
	b.Push(0, frame(1))
	b.Push(3, frame(4))
	b.Push(1, frame(2))
	b.Push(2, frame(3))

	for i := range 4 {
		data, real := b.Pop()
		if !real {
			t.Fatalf("pop %d: real = false, want true", i)
		}
		if !bytes.Equal(data, frame(byte(i+1))) {
			t.Errorf("pop %d = %v, want %v", i, data, frame(byte(i+1)))
		}
	}
}

// A dropped packet must produce silence in its slot, not shift every later
// packet forward — that would turn one lost packet into a permanent offset.
func TestMissingPacketBecomesSilenceWithoutShifting(t *testing.T) {
	b := newTestBuffer()
	b.Push(0, frame(1))
	// seq 1 never arrives
	b.Push(2, frame(3))
	b.Push(3, frame(4))
	b.Push(4, frame(5))

	if data, real := b.Pop(); !real || !bytes.Equal(data, frame(1)) {
		t.Fatalf("pop 0 = (%v, %v), want frame 1", data, real)
	}
	data, real := b.Pop()
	if real {
		t.Error("pop 1: real = true, want false for the lost packet")
	}
	if !bytes.Equal(data, make([]byte, frameBytes)) {
		t.Errorf("pop 1 = %v, want silence", data)
	}
	if data, real := b.Pop(); !real || !bytes.Equal(data, frame(3)) {
		t.Errorf("pop 2 = (%v, %v), want frame 3 — the stream shifted", data, real)
	}
}

func TestDuplicatePacketIsIgnored(t *testing.T) {
	b := newTestBuffer()
	for i := range 4 {
		b.Push(uint32(i), frame(byte(i+1)))
	}
	b.Push(1, frame(99)) // duplicate, different payload

	b.Pop()
	data, _ := b.Pop()
	if !bytes.Equal(data, frame(2)) {
		t.Errorf("pop 1 = %v, want the original frame 2, not the duplicate", data)
	}
}

// A packet that shows up after its slot has already played is useless; keeping
// it would corrupt the sequence.
func TestLatePacketIsDropped(t *testing.T) {
	b := newTestBuffer()
	for i := range 4 {
		b.Push(uint32(i), frame(byte(i+1)))
	}
	b.Pop() // plays seq 0
	b.Pop() // plays seq 1

	b.Push(0, frame(99)) // far too late

	if data, real := b.Pop(); !real || !bytes.Equal(data, frame(3)) {
		t.Errorf("pop = (%v, %v), want frame 3 — a late packet corrupted the order", data, real)
	}
}

// Two free-running 48 kHz clocks drift apart. When the sender is faster the
// buffer grows without bound and latency creeps up, so it is trimmed back.
func TestOverfullBufferDropsBackToTarget(t *testing.T) {
	b := newTestBuffer()
	for i := range 12 { // MaxDepth is 8
		b.Push(uint32(i), frame(byte(i+1)))
	}

	if got := b.Depth(); got <= 8 {
		t.Fatalf("Depth() = %d, want > MaxDepth before the first Pop", got)
	}
	b.Pop()

	if got := b.Depth(); got > 4 {
		t.Errorf("Depth() after trim = %d, want <= TargetDepth (4)", got)
	}
}

// Trimming must discard the oldest audio, so what plays next is the freshest
// available rather than a jump backwards.
func TestTrimDiscardsOldestNotNewest(t *testing.T) {
	b := newTestBuffer()
	for i := range 12 {
		b.Push(uint32(i), frame(byte(i+1)))
	}

	data, real := b.Pop()

	if !real {
		t.Fatal("real = false, want a real packet after trimming")
	}
	if bytes.Equal(data, frame(1)) {
		t.Error("played the oldest packet, want it discarded by the trim")
	}
	if !bytes.Equal(data, frame(9)) {
		t.Errorf("data = %v, want frame 9 — the oldest kept packet", data)
	}
}

func TestPopOnEmptyBufferReturnsSilence(t *testing.T) {
	b := newTestBuffer()

	data, real := b.Pop()

	if real {
		t.Error("real = true, want false on an empty buffer")
	}
	if len(data) != frameBytes {
		t.Errorf("len(data) = %d, want %d", len(data), frameBytes)
	}
}

// A short packet from a corrupt or hostile sender must not reach the audio
// device, where a partial frame would click.
func TestPushRejectsWrongSizedFrame(t *testing.T) {
	b := newTestBuffer()

	b.Push(0, []byte{1, 2, 3})

	if got := b.Depth(); got != 0 {
		t.Errorf("Depth() = %d, want 0 — the malformed frame was accepted", got)
	}
}
