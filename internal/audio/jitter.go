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
	stats   JitterStats
}

// JitterStats counts what went wrong since the buffer was created, to tell a
// lossy network (Lost) from clocks drifting (Starved, Trimmed) from reordering
// (Late).
type JitterStats struct {
	Pushed  uint64 // packets accepted
	Late    uint64 // arrived after their slot had played
	Lost    uint64 // slots skipped because the packet never came
	Starved uint64 // ran dry and went back to prebuffering
	Trimmed uint64 // dropped to bring latency back down
	MaxSeen int    // deepest the queue has been
}

// Stats returns the counters. Not safe for concurrent use, like the rest.
func (j *Jitter) Stats() JitterStats { return j.stats }

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
		j.stats.Late++
		return // its slot has already played
	}
	if _, dup := j.packets[seq]; dup {
		return
	}

	// Copy: the caller reuses its UDP read buffer for the next datagram.
	buf := make([]byte, len(payload))
	copy(buf, payload)
	j.packets[seq] = buf
	j.stats.Pushed++
	if d := len(j.packets); d > j.stats.MaxSeen {
		j.stats.MaxSeen = d
	}
}

// Pop returns the next frame to play. real reports whether it is received
// audio rather than concealment silence.
func (j *Jitter) Pop() ([]byte, bool) {
	if !j.playing {
		if j.Depth() < j.cfg.TargetDepth {
			return j.silence, false
		}
		j.playing = true
		// Recomeça pelo mais antigo que temos. Sem isto, voltar a tocar depois
		// de uma fome longa gastaria um Pop de silêncio por número de sequência
		// pulado — dez segundos de silêncio para uma parada de dez segundos.
		if o, ok := j.oldest(); ok {
			j.next = o
		}
	}

	j.trim()

	if p, ok := j.packets[j.next]; ok {
		delete(j.packets, j.next)
		j.next++
		return p, true
	}

	// Buffer VAZIO não é pacote perdido, é fome — e a diferença é a causa do
	// som que ia ficando cada vez mais picotado.
	//
	// Relógios livres de 48 kHz nos dois lados nunca são exatamente iguais.
	// Quando o receptor é o mais rápido, ele chega ao slot antes do pacote. Se
	// `next` avançasse aqui, o pacote que está a caminho entraria no Push com
	// `seq < next` e seria DESCARTADO: cada déficit queimaria um pacote para
	// sempre, o buffer nunca voltaria a encher, e a proporção de silêncio
	// cresceria sem parar. Uma catraca.
	//
	// Então: não avança, e volta a pré-encher. O custo é uma pausa limitada,
	// uma vez, em vez de degradação permanente.
	if j.Depth() == 0 {
		j.playing = false
		j.stats.Starved++
		return j.silence, false
	}

	// Slot vazio COM pacotes posteriores presentes é perda de verdade: aquele
	// pacote não vem mais. Avança, para que um perdido custe 10 ms em vez de
	// travar o fluxo.
	j.next++
	j.stats.Lost++
	return j.silence, false
}

// oldest é o menor número de sequência guardado. A profundidade é pequena, então
// a varredura linear sai mais barata que manter um heap.
func (j *Jitter) oldest() (uint32, bool) {
	var menor uint32
	found := false
	for seq := range j.packets {
		if !found || seq < menor {
			menor, found = seq, true
		}
	}
	return menor, found
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
		velho, found := j.oldest()
		if !found {
			return
		}
		delete(j.packets, velho)
		j.stats.Trimmed++
		j.next = velho + 1
	}
}

// Buffering reports whether playback is paused, either prebuffering at the
// start or refilling after the queue ran dry. Frames popped meanwhile are
// concealment silence rather than a stand-in for a lost packet.
func (j *Jitter) Buffering() bool { return !j.playing }

// Depth reports how many packets are buffered.
func (j *Jitter) Depth() int { return len(j.packets) }
