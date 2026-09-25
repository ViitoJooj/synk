// Package proto implements the synk wire format: the framed, ordered
// control stream (input events, focus handoff, clipboard) carried over TCP.
//
// Audio does not use this package — it rides a separate unreliable UDP path,
// because a late audio packet is worth dropping while a late key-up is not.
package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MsgType identifies the payload carried by a frame.
type MsgType uint8

const (
	MsgHello MsgType = iota + 1
	MsgPing
	MsgPong
	MsgKey
	MsgMouseRel
	MsgMouseBtn
	MsgScroll
	MsgFocusEnter
	MsgFocusLeave
	MsgReleaseAll
	MsgClipOffer
	MsgClipData
)

// MaxPayload caps a single frame's payload. It bounds the allocation a peer can
// induce with a length prefix, and is generous enough for a clipboard chunk.
const MaxPayload = 1 << 20

// headerLen is the size of the big-endian length prefix.
const headerLen = 4

var (
	// ErrPayloadTooLarge reports a frame whose payload exceeds MaxPayload,
	// whether we were asked to write it or a peer claimed to be sending it.
	ErrPayloadTooLarge = errors.New("proto: payload too large")

	// ErrBadFrame reports a structurally invalid frame.
	ErrBadFrame = errors.New("proto: malformed frame")
)

// WriteFrame writes one length-prefixed frame. On rejection it writes nothing,
// so a caller that ignores the error cannot desynchronise the stream.
func WriteFrame(w io.Writer, typ MsgType, payload []byte) error {
	if len(payload) > MaxPayload {
		return fmt.Errorf("%w: %d bytes (max %d)", ErrPayloadTooLarge, len(payload), MaxPayload)
	}

	// One write keeps the frame atomic for net.Conn, where a short second
	// write after a successful first would leave a half-frame on the wire.
	frame := make([]byte, headerLen+1+len(payload))
	binary.BigEndian.PutUint32(frame, uint32(1+len(payload)))
	frame[headerLen] = byte(typ)
	copy(frame[headerLen+1:], payload)

	_, err := w.Write(frame)
	return err
}

// ReadFrame reads one frame. It returns io.EOF only at a clean frame boundary,
// so a caller can tell orderly peer shutdown from a truncated frame, which
// comes back as io.ErrUnexpectedEOF.
func ReadFrame(r io.Reader) (MsgType, []byte, error) {
	var hdr [headerLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		// io.ReadFull reports EOF only when it read nothing at all.
		return 0, nil, err
	}

	length := binary.BigEndian.Uint32(hdr[:])
	if length == 0 {
		return 0, nil, fmt.Errorf("%w: zero-length frame", ErrBadFrame)
	}
	// Checked before allocating, so a bogus prefix cannot exhaust memory.
	if length-1 > MaxPayload {
		return 0, nil, fmt.Errorf("%w: peer announced %d bytes (max %d)",
			ErrPayloadTooLarge, length-1, MaxPayload)
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) {
			// Nothing followed a valid header: the frame is truncated,
			// which is corruption rather than orderly shutdown.
			return 0, nil, io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}

	return MsgType(body[0]), body[1:], nil
}
