// Package audio carries the PCM streams: playback captured on Linux and
// rendered on Windows, and the Windows microphone rendered into a PipeWire
// virtual source.
package audio

// JitterConfig sizes the buffer in whole packets.
type JitterConfig struct {
	FrameBytes  int
	TargetDepth int
	MaxDepth    int
}

// Jitter reorders UDP audio packets, conceals losses and corrects clock drift.
//
// The two machines run free-running 48 kHz clocks that are never exactly equal.
// When the sender's is faster the queue grows and latency creeps up until the
// audio is noticeably behind; when the receiver's is faster the queue starves.
// Popping silence covers the starving case and trimming covers the growing one,
// which keeps latency bounded without any resampling.
//
// Not safe for concurrent use; the reader goroutine owns it.
type Jitter struct {
	cfg     JitterConfig
	packets map[uint32][]byte
	next    uint32
	started bool // next has been seeded from the first packet seen
	playing bool // prebuffering is done
	silence []byte
}

func NewJitter(cfg JitterConfig) *Jitter {
	return &Jitter{
		cfg:     cfg,
		packets: make(map[uint32][]byte),
		silence: make([]byte, cfg.FrameBytes),
	}
}

// Push accepts one received packet. Malformed, duplicate and already-played
// packets are dropped rather than allowed to reach the audio device.
//
// Sequence numbers are assumed not to wrap: at one packet per 10 ms a uint32
// lasts over a year of unbroken streaming, and a reconnect reseeds anyway.
func (j *Jitter) Push(seq uint32, payload []byte) {
	if len(payload) != j.cfg.FrameBytes {
		return
	}
	if !j.started {
		j.next = seq
		j.started = true
	}
	if seq < j.next {
		return // its slot has already played
	}
	if _, dup := j.packets[seq]; dup {
		return
	}

	// Copy: the caller reuses its UDP read buffer for the next datagram.
	buf := make([]byte, len(payload))
	copy(buf, payload)
	j.packets[seq] = buf
}

// Pop returns the next frame to play. real reports whether it is received
// audio rather than concealment silence.
func (j *Jitter) Pop() ([]byte, bool) {
	if !j.playing {
		if j.Depth() < j.cfg.TargetDepth {
			return j.silence, false
		}
		j.playing = true
	}

	j.trim()

	if p, ok := j.packets[j.next]; ok {
		delete(j.packets, j.next)
		j.next++
		return p, true
	}

	// Nothing for this slot: emit silence and move on, so one lost packet
	// costs 10 ms of audio rather than permanently shifting the stream.
	j.next++
	return j.silence, false
}

// trim discards the oldest packets when the queue has outgrown MaxDepth,
// bringing latency back to TargetDepth. Depth stays small, so the linear scan
// for the oldest sequence is cheaper than maintaining a heap.
func (j *Jitter) trim() {
	// Hysteresis: tolerate growth up to MaxDepth, but once past it go all the
	// way back to TargetDepth. Trimming to MaxDepth instead would leave the
	// queue on the threshold and drop a packet on almost every Pop.
	if j.Depth() <= j.cfg.MaxDepth {
		return
	}

	for j.Depth() > j.cfg.TargetDepth {
		oldest := j.next
		found := false
		for seq := range j.packets {
			if !found || seq < oldest {
				oldest, found = seq, true
			}
		}
		if !found {
			return
		}
		delete(j.packets, oldest)
		j.next = oldest + 1
	}
}

// Depth reports how many packets are buffered.
func (j *Jitter) Depth() int { return len(j.packets) }
