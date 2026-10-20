//go:build linux

package audio

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/jfreymuth/pulse"
)

// captureLatencyMS is how much audio the server buffers for the monitor stream.
// At FrameMS the server held a single frame, so any stall of this process —
// a GC pause, a scheduling delay — overran it and lost samples for good;
// measured at about 6% of the stream missing. Frames are still cut at FrameMS,
// they just leave in bursts, which the receiver's jitter buffer absorbs.
const captureLatencyMS = 20

// appName is what shows up in pavucontrol and KDE's volume applet.
const appName = "synk"

// Monitor captures whatever the default sink is playing — the source of the
// Linux to Windows playback stream.
//
// It records the sink's *monitor*, not a microphone, which is how a sink's
// own output is captured without any loopback cable or virtual device.
type Monitor struct {
	client  *pulse.Client
	stream  *pulse.RecordStream
	frames  chan []byte
	pending []int16 // partial frame, touched only by the PulseAudio callback
	dropped atomic.Uint64
	perFrm  int
	mute    *sinkMute
}

// OpenMonitor starts capturing the default sink's monitor.
func OpenMonitor(buffer int) (*Monitor, error) {
	c, err := pulse.NewClient(pulse.ClientApplicationName(appName))
	if err != nil {
		return nil, fmt.Errorf("audio: connecting to PipeWire: %w", err)
	}

	sink, err := c.DefaultSink()
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("audio: finding the default sink: %w", err)
	}

	m := &Monitor{
		client: c,
		frames: make(chan []byte, buffer),
		perFrm: SamplesPerFrame(Stereo),
		// O MESMO sink que está sendo capturado. Mutar outro silenciaria a
		// máquina sem calar o que está sendo enviado.
		mute: newSinkMute(sink.ID()),
	}

	m.stream, err = c.NewRecord(
		pulse.Int16Writer(m.onSamples),
		pulse.RecordMonitor(sink),
		pulse.RecordStereo,
		pulse.RecordSampleRate(SampleRate),
		pulse.RecordLatency(float64(captureLatencyMS)/1000),
	)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("audio: opening monitor of %q: %w", sink.Name(), err)
	}

	m.stream.Start()
	return m, nil
}

// onSamples runs on PulseAudio's goroutine. It slices the arbitrary-sized
// chunks the server delivers into the exact frames the wire format expects.
func (m *Monitor) onSamples(in []int16) (int, error) {
	m.pending = append(m.pending, in...)

	for len(m.pending) >= m.perFrm {
		frame := int16ToPCM(m.pending[:m.perFrm])
		m.pending = m.pending[m.perFrm:]

		select {
		case m.frames <- frame:
		default:
			// Real-time audio: a reader that has fallen behind is better
			// served by fresh audio than by a growing backlog.
			m.dropped.Add(1)
		}
	}
	return len(in), nil
}

// Frames yields FrameBytes-sized PCM frames.
func (m *Monitor) Frames() <-chan []byte { return m.frames }

// Dropped counts frames discarded because the consumer fell behind.
func (m *Monitor) Dropped() uint64 { return m.dropped.Load() }

// SilenceLocal cala as caixas desta máquina enquanto o som está indo para o
// peer, e devolve o estado anterior no Close.
//
// Separado do OpenMonitor de propósito: falhar em mutar não é motivo para
// derrubar o áudio. O pior caso é o som sair nos dois lugares, que é
// exatamente onde estávamos antes — e quem chama tem onde dizer isso ao
// usuário.
func (m *Monitor) SilenceLocal() error { return m.mute.silence() }

func (m *Monitor) Close() error {
	// Restaurar primeiro: se o teardown do stream entrar em pânico, o usuário
	// pelo menos não fica com o notebook mudo.
	err := m.mute.restore()
	m.stream.Close()
	m.client.Close()
	return err
}

// Player renders received PCM into a sink. It is fed by a Jitter buffer, so
// network reordering and clock drift are absorbed before the audio device
// ever sees them.
type Player struct {
	client *pulse.Client
	stream *pulse.PlaybackStream

	// PulseAudio calls onNeed from its own goroutine while the network
	// goroutine calls Push. Jitter is deliberately not self-synchronised, so
	// the lock lives here, at the one place both threads meet.
	mu      sync.Mutex
	jitter  *Jitter
	pending []int16
}

// OpenPlayer renders into sinkName, or the default sink when empty.
func OpenPlayer(sinkName string, ch Channels, j *Jitter) (*Player, error) {
	c, err := pulse.NewClient(pulse.ClientApplicationName(appName))
	if err != nil {
		return nil, fmt.Errorf("audio: connecting to PipeWire: %w", err)
	}

	p := &Player{client: c, jitter: j}

	opts := []pulse.PlaybackOption{
		pulse.PlaybackSampleRate(SampleRate),
		pulse.PlaybackLatency(float64(FrameMS) / 1000),
	}
	if ch == Stereo {
		opts = append(opts, pulse.PlaybackStereo)
	} else {
		opts = append(opts, pulse.PlaybackMono)
	}
	if sinkName != "" {
		sink, err := c.SinkByID(sinkName)
		if err != nil {
			c.Close()
			return nil, fmt.Errorf("audio: finding sink %q: %w", sinkName, err)
		}
		opts = append(opts, pulse.PlaybackSink(sink))
	}

	p.stream, err = c.NewPlayback(pulse.Int16Reader(p.onNeed), opts...)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("audio: opening playback: %w", err)
	}

	p.stream.Start()
	return p, nil
}

// Push hands one received datagram to the jitter buffer.
func (p *Player) Push(seq uint32, pcm []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.jitter.Push(seq, pcm)
}

// Depth reports the jitter buffer's current occupancy, for status output.
func (p *Player) Depth() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.jitter.Depth()
}

// onNeed runs on PulseAudio's goroutine and must always return a full buffer:
// returning short would stutter the stream, so a starved buffer yields the
// jitter buffer's concealment silence instead.
func (p *Player) onNeed(out []int16) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	filled := 0
	for filled < len(out) {
		if len(p.pending) == 0 {
			frame, _ := p.jitter.Pop()
			p.pending = pcmToInt16(frame)
		}
		n := copy(out[filled:], p.pending)
		p.pending = p.pending[n:]
		filled += n
	}
	return filled, nil
}

func (p *Player) Close() error {
	p.stream.Close()
	p.client.Close()
	return nil
}
