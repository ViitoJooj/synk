//go:build linux

package audio

import (
	"os"
	"sync"
	"testing"
	"time"
)

// These run against the PipeWire session actually on this machine. They skip
// rather than fail where there is no audio server, so the suite still runs in
// a container or over SSH without a session bus.
func requirePulse(t *testing.T) {
	t.Helper()
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		t.Skip("no XDG_RUNTIME_DIR; not a desktop session")
	}
}

func TestIntegrationMonitorCapturesTheDefaultSink(t *testing.T) {
	requirePulse(t)

	m, err := OpenMonitor(64)
	if err != nil {
		t.Skipf("no PipeWire sink monitor available: %v", err)
	}
	defer m.Close()

	select {
	case frame := <-m.Frames():
		if len(frame) != FrameBytes(Stereo) {
			t.Errorf("frame is %d bytes, want %d", len(frame), FrameBytes(Stereo))
		}
		t.Logf("captured a %d-byte frame from the default sink monitor", len(frame))
	case <-time.After(5 * time.Second):
		t.Fatal("no frame in 5s; the sink monitor delivered nothing")
	}
}

// Silence still has to flow: the sink is SUSPENDED when nothing is playing,
// and a stream that only wakes on real audio would stall the link.
func TestIntegrationMonitorDeliversContinuouslyEvenWhenSilent(t *testing.T) {
	requirePulse(t)

	m, err := OpenMonitor(64)
	if err != nil {
		t.Skipf("no PipeWire sink monitor available: %v", err)
	}
	defer m.Close()

	const want = 10 // 100 ms worth
	got := 0
	deadline := time.After(5 * time.Second)
	for got < want {
		select {
		case <-m.Frames():
			got++
		case <-deadline:
			t.Fatalf("got %d frames in 5s, want %d — capture stalls when silent", got, want)
		}
	}
}

func TestIntegrationPlayerRendersToTheDefaultSink(t *testing.T) {
	requirePulse(t)

	j := NewJitter(JitterConfig{
		FrameBytes:  FrameBytes(Stereo),
		TargetDepth: 4,
		MaxDepth:    8,
	})
	p, err := OpenPlayer("", Stereo, j)
	if err != nil {
		t.Skipf("no PipeWire playback available: %v", err)
	}
	defer p.Close()

	// Silence, so running the suite does not make a noise.
	for i := range uint32(8) {
		p.Push(i, make([]byte, FrameBytes(Stereo)))
	}
	time.Sleep(200 * time.Millisecond)

	if err := p.stream.Error(); err != nil {
		t.Errorf("playback stream error: %v", err)
	}
}

// PulseAudio pulls from its own goroutine while the network pushes from
// another. Run under -race, this is what proves the lock is in the right place.
func TestPlayerPushAndPullAreRaceFree(t *testing.T) {
	j := NewJitter(JitterConfig{
		FrameBytes:  FrameBytes(Mono),
		TargetDepth: 2,
		MaxDepth:    6,
	})
	// Built directly rather than through OpenPlayer: this exercises the
	// concurrency, not PipeWire, so it runs everywhere.
	p := &Player{jitter: j}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := range uint32(500) {
			p.Push(i, make([]byte, FrameBytes(Mono)))
		}
	}()
	go func() {
		defer wg.Done()
		out := make([]int16, 128)
		for range 500 {
			if _, err := p.onNeed(out); err != nil {
				t.Errorf("onNeed: %v", err)
				return
			}
		}
	}()

	wg.Wait()
}

// A starved buffer must still hand back a full buffer, or the stream stutters.
func TestPlayerAlwaysFillsTheRequestedBuffer(t *testing.T) {
	j := NewJitter(JitterConfig{
		FrameBytes:  FrameBytes(Mono),
		TargetDepth: 2,
		MaxDepth:    6,
	})
	p := &Player{jitter: j}

	for _, size := range []int{1, 63, 480, 1000} {
		out := make([]int16, size)
		n, err := p.onNeed(out)
		if err != nil {
			t.Fatalf("onNeed(%d): %v", size, err)
		}
		if n != size {
			t.Errorf("onNeed(%d) filled %d, want the whole buffer", size, n)
		}
	}
}
