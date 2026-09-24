//go:build windows

package main

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"conn/internal/audio"
	"conn/internal/transport"
)

const (
	// keepaliveEvery paces the datagrams that tell the peer where to send.
	//
	// They are what make this work without a firewall rule: Windows drops an
	// unsolicited inbound datagram, but allows replies to one it sent. The
	// mapping a firewall keeps for an outbound datagram expires in tens of
	// seconds at worst, so a second is comfortably often enough and costs five
	// bytes.
	keepaliveEvery = 1 * time.Second

	audioReadBuffer = 2048

	// jitterTarget is how many packets to hold before playing, in 5 ms units.
	// 40 ms absorbs ordinary wifi jitter without being heard as delay.
	jitterTarget = 8

	// jitterMax is where the buffer is trimmed back to target. It bounds how
	// far latency can creep when the sender's clock runs faster than ours.
	jitterMax = 16
)

// runPlayback receives what the peer is playing and puts it on this machine's
// speakers.
//
// This side dials and speaks first, which is the whole reason the direction is
// arranged this way: the peer cannot know where to send until it has heard from
// us, and an inbound datagram nobody asked for would need an administrator to
// let it through.
func runPlayback(ctx context.Context, out writerFunc, peerIP string, port int) {
	// COM is per thread, and the interfaces the renderer creates here are used
	// by its own loop, so this goroutine is pinned for as long as they live.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := audio.InitCOM(); err != nil {
		out("audio off: %v\n", err)
		return
	}
	defer audio.UninitCOM()

	conn, err := transport.DialAudio(peerIP, port)
	if err != nil {
		out("audio off: %v\n", err)
		return
	}
	defer conn.Close()

	renderer, err := audio.OpenRenderer(audio.NewJitter(audio.JitterConfig{
		FrameBytes:  audio.FrameBytes(audio.Stereo),
		TargetDepth: jitterTarget,
		MaxDepth:    jitterMax,
	}))
	if err != nil {
		out("audio off: %v\n", err)
		return
	}
	defer renderer.Close()

	out("audio: playing %s's output on this machine's speakers\n", peerIP)

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	go keepAlive(ctx, conn)
	receivePlayback(conn, renderer, out)
}

// keepAlive holds the return path open.
func keepAlive(ctx context.Context, conn interface{ Write([]byte) (int, error) }) {
	ticker := time.NewTicker(keepaliveEvery)
	defer ticker.Stop()

	var buf []byte
	for {
		buf = audio.Encode(buf, audio.KindKeepalive, 0, nil)
		if _, err := conn.Write(buf); err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// receivePlayback feeds arriving audio to the renderer until the socket closes.
//
// The socket is connected to the peer, so the operating system already refuses
// datagrams from anywhere else and there is no per-packet address check to make
// here.
func receivePlayback(conn interface{ Read([]byte) (int, error) }, renderer *audio.Renderer, out writerFunc) {
	buf := make([]byte, audioReadBuffer)
	var announced bool

	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		kind, seq, pcm, err := audio.Decode(buf[:n])
		if err != nil || kind != audio.KindPlayback {
			continue
		}

		if !announced {
			announced = true
			out("audio: sound is arriving from the peer\n")
		}
		renderer.Push(seq, pcm)
	}
}

// writerFunc is how the audio path reports, without dragging the session's
// io.Writer and its formatting through every signature.
type writerFunc func(format string, args ...any)

func printTo(w interface{ Write([]byte) (int, error) }) writerFunc {
	return func(format string, args ...any) { fmt.Fprintf(w, format, args...) }
}
