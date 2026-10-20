//go:build windows

package audio

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	ole "github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// Renderer plays received PCM on this machine's speakers.
//
// Shared mode, not exclusive: the point is to mix with whatever else the
// machine is playing, not to take the device over. The stream is opened with
// AUTOCONVERTPCM so Windows resamples our 48 kHz signed 16-bit stereo into
// whatever the endpoint's mix format happens to be, rather than us carrying a
// resampler for the case where someone's device runs at 44.1 kHz.
type Renderer struct {
	client *wca.IAudioClient
	render *wca.IAudioRenderClient
	device *wca.IMMDevice
	enum   *wca.IMMDeviceEnumerator

	// mu serializa TODO acesso ao jitter. Ele e escrito pela goroutine de rede
	// (Push) e lido pela do laco de render (copyInto), e o Jitter documenta que
	// nao e seguro para uso concorrente: sem esta trava o runtime derruba o
	// processo inteiro com "fatal error: concurrent map writes". O Player do
	// Linux ja fazia assim; este lado nasceu sem, e so apareceu quando alguem
	// finalmente executou o WASAPI.
	mu     sync.Mutex
	jitter *Jitter

	// bufferFrames is the endpoint's buffer in audio frames — samples per
	// channel — which is the unit every WASAPI call below counts in.
	bufferFrames uint32
	blockAlign   uint32

	// wake is signalled by WASAPI whenever it wants more audio, so the render
	// loop runs exactly when needed instead of on a coarse timer.
	wake windows.Handle

	pending []byte // the tail of a popped packet that did not fit last time
	stop    chan struct{}
	done    chan struct{}
}

// bufferDuration is how much audio the endpoint buffer holds. 100 ms is far
// more than the jitter buffer's target: the endpoint buffer absorbs Windows
// scheduling, the jitter buffer absorbs the network, and conflating the two
// costs latency for no benefit.
const bufferDuration = 100 * time.Millisecond

// fillTarget is how much audio is kept queued in the endpoint buffer. Writing
// the whole buffer would drain the jitter buffer in one gulp and then pad the
// endpoint with silence ahead of audio that arrives a moment later.
const fillTarget = 30 * time.Millisecond

// waitTimeout bounds a wait for WASAPI's event, so Close is never stuck behind
// a device that stopped signalling.
const waitTimeout = 50

// refTimesPerMs is WASAPI's REFERENCE_TIME unit: 100-nanosecond ticks.
const refTimesPerMs = 10000

// OpenRenderer starts playback on the default output device.
//
// COM must already be initialised on the calling goroutine — see InitCOM.
func OpenRenderer(j *Jitter) (*Renderer, error) {
	r := &Renderer{
		jitter: j,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}

	if err := wca.CoCreateInstance(
		wca.CLSID_MMDeviceEnumerator, 0,
		wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &r.enum,
	); err != nil {
		return nil, fmt.Errorf("audio: creating the device enumerator: %w", err)
	}

	if err := r.enum.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &r.device); err != nil {
		r.closeCOM()
		return nil, fmt.Errorf("audio: finding the default playback device: %w", err)
	}

	if err := r.device.Activate(
		wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &r.client,
	); err != nil {
		r.closeCOM()
		return nil, fmt.Errorf("audio: opening the playback device: %w", err)
	}

	format := ourFormat()
	flags := uint32(wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM |
		wca.AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY |
		wca.AUDCLNT_STREAMFLAGS_EVENTCALLBACK)

	if err := r.client.Initialize(
		wca.AUDCLNT_SHAREMODE_SHARED, flags,
		wca.REFERENCE_TIME(bufferDuration.Milliseconds()*refTimesPerMs), 0,
		format, nil,
	); err != nil {
		r.closeCOM()
		return nil, fmt.Errorf("audio: preparing the playback stream: %w", err)
	}

	wake, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		r.closeCOM()
		return nil, fmt.Errorf("audio: creating the wake event: %w", err)
	}
	r.wake = wake
	if err := r.client.SetEventHandle(uintptr(wake)); err != nil {
		r.closeCOM()
		return nil, fmt.Errorf("audio: registering the wake event: %w", err)
	}

	if err := r.client.GetBufferSize(&r.bufferFrames); err != nil {
		r.closeCOM()
		return nil, fmt.Errorf("audio: reading the buffer size: %w", err)
	}
	r.blockAlign = uint32(format.NBlockAlign)

	if err := r.client.GetService(wca.IID_IAudioRenderClient, &r.render); err != nil {
		r.closeCOM()
		return nil, fmt.Errorf("audio: getting the render client: %w", err)
	}

	if err := r.client.Start(); err != nil {
		r.closeCOM()
		return nil, fmt.Errorf("audio: starting playback: %w", err)
	}

	go r.run()
	return r, nil
}

// ourFormat is the one format the link speaks. Windows converts from it.
func ourFormat() *wca.WAVEFORMATEX {
	const bits = 16
	channels := uint16(Stereo)
	blockAlign := channels * bits / 8

	return &wca.WAVEFORMATEX{
		WFormatTag:      wca.WAVE_FORMAT_PCM,
		NChannels:       channels,
		NSamplesPerSec:  SampleRate,
		NAvgBytesPerSec: SampleRate * uint32(blockAlign),
		NBlockAlign:     blockAlign,
		WBitsPerSample:  bits,
		CbSize:          0,
	}
}

// Push hands one received datagram to the jitter buffer.
//
// The render loop is the only reader, and Jitter is not safe for concurrent
// use, so this crosses into it through the same channel the loop drains rather
// than touching it directly.
func (r *Renderer) Push(seq uint32, pcm []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jitter.Push(seq, pcm)
}

// run keeps the endpoint buffer fed.
//
// Polling rather than the event-driven callback: the loop wakes on a timer,
// asks how much room there is, and fills it. It is a few lines instead of an
// event handle and a wait, and at a period of half the buffer it is in no
// danger of underrunning.
func (r *Renderer) run() {
	defer close(r.done)

	// The interfaces were created on another goroutine, so this one has to be
	// in the same apartment to touch them. That is why InitCOM asks for the
	// multi-threaded apartment: in an apartment-threaded one, an interface
	// pointer is only valid on the thread that made it, and this loop would be
	// making cross-apartment calls on a raw vtable — which does not fail
	// loudly, it corrupts.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := InitCOM(); err != nil {
		return
	}
	defer UninitCOM()

	for {
		select {
		case <-r.stop:
			return
		default:
		}

		// Event-driven: woken by the device, or by the timeout so a stop request
		// is always noticed.
		_, _ = windows.WaitForSingleObject(r.wake, waitTimeout)

		if err := r.fill(); err != nil {
			return
		}
	}
}

// fill writes as much audio as the endpoint has room for.
func (r *Renderer) fill() error {
	var padding uint32
	if err := r.client.GetCurrentPadding(&padding); err != nil {
		return err
	}

	free := r.bufferFrames - padding
	if want := uint32(fillTarget.Milliseconds()) * SampleRate / 1000; padding >= want {
		return nil
	} else if free > want-padding {
		free = want - padding
	}
	if free == 0 {
		return nil
	}

	var data *byte
	if err := r.render.GetBuffer(free, &data); err != nil {
		return err
	}

	// The buffer WASAPI handed back is unmanaged memory it owns for the
	// duration of this call, which is exactly the case unsafe.Slice is for.
	dst := unsafe.Slice(data, free*r.blockAlign)
	written := r.copyInto(dst)

	// Frames, not bytes: everything WASAPI counts is in frames.
	return r.render.ReleaseBuffer(uint32(written)/r.blockAlign, 0)
}

// copyInto fills dst from the jitter buffer and returns how many bytes it
// wrote. It stops short when the jitter buffer is starved or still
// prebuffering: padding the endpoint with silence now would only play ahead of
// the real audio arriving a moment later. A packet that was lost mid-stream is
// still filled with silence, which keeps the timeline intact.
func (r *Renderer) copyInto(dst []byte) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	filled := 0
	for filled < len(dst) {
		if len(r.pending) == 0 {
			frame, _ := r.jitter.Pop()
			if r.jitter.Buffering() {
				break
			}
			r.pending = frame
		}
		n := copy(dst[filled:], r.pending)
		r.pending = r.pending[n:]
		filled += n
	}
	return filled
}

// Stats returns the jitter buffer's counters, with the current depth.
func (r *Renderer) Stats() (JitterStats, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jitter.Stats(), r.jitter.Depth()
}

// Depth reports the jitter buffer's occupancy, for status output.
func (r *Renderer) Depth() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jitter.Depth()
}

func (r *Renderer) Close() error {
	close(r.stop)
	<-r.done
	if r.client != nil {
		r.client.Stop()
	}
	r.closeCOM()
	return nil
}

func (r *Renderer) closeCOM() {
	if r.wake != 0 {
		windows.CloseHandle(r.wake)
		r.wake = 0
	}
	if r.render != nil {
		r.render.Release()
	}
	if r.client != nil {
		r.client.Release()
	}
	if r.device != nil {
		r.device.Release()
	}
	if r.enum != nil {
		r.enum.Release()
	}
}

// sFalse is COM's "already initialised on this thread", which go-ole reports as
// an error because it reports every non-zero HRESULT as one. It is a success.
const sFalse = 0x00000001

// InitCOM puts the calling thread in the multi-threaded apartment.
//
// Multi-threaded rather than apartment-threaded because the interfaces are
// created on one goroutine and used by the render loop on another. In an
// apartment-threaded process that would require marshalling; in the MTA an
// interface pointer is usable from any thread in the process, which is what
// makes the split safe.
//
// The caller must have locked its goroutine to its thread first: apartments
// belong to threads, and a goroutine the scheduler moves would leave its
// apartment behind.
func InitCOM() error {
	err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED)
	if err == nil {
		return nil
	}

	var oleErr *ole.OleError
	if errors.As(err, &oleErr) && oleErr.Code() == sFalse {
		return nil
	}
	return fmt.Errorf("audio: initialising COM: %w", err)
}

func UninitCOM() { ole.CoUninitialize() }
