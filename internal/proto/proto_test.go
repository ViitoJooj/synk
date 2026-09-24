package proto

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		typ     MsgType
		payload []byte
	}{
		{"empty payload", MsgPing, nil},
		{"small payload", MsgKey, []byte{0x1e, 0x01}},
		{"payload with zero bytes", MsgClipData, []byte{0x00, 0x00, 0xff, 0x00}},
		{"max payload", MsgClipData, bytes.Repeat([]byte{0xab}, MaxPayload)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFrame(&buf, tt.typ, tt.payload); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}

			gotType, gotPayload, err := ReadFrame(&buf)
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if gotType != tt.typ {
				t.Errorf("type = %v, want %v", gotType, tt.typ)
			}
			if !bytes.Equal(gotPayload, tt.payload) {
				t.Errorf("payload = %v (len %d), want %v (len %d)",
					gotPayload, len(gotPayload), tt.payload, len(tt.payload))
			}
		})
	}
}

func TestWriteFrameRejectsOversizedPayload(t *testing.T) {
	var buf bytes.Buffer
	err := WriteFrame(&buf, MsgClipData, bytes.Repeat([]byte{1}, MaxPayload+1))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %d bytes on rejection, want 0", buf.Len())
	}
}

// A hostile or corrupt peer must not be able to make us allocate an arbitrary
// buffer just by claiming a huge length.
func TestReadFrameRejectsOversizedLengthWithoutAllocating(t *testing.T) {
	// length prefix claims 4 GiB, but no body follows.
	hdr := []byte{0xff, 0xff, 0xff, 0xff}
	_, _, err := ReadFrame(bytes.NewReader(hdr))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
}

func TestReadFrameRejectsZeroLength(t *testing.T) {
	// A frame must carry at least the type byte.
	_, _, err := ReadFrame(bytes.NewReader([]byte{0x00, 0x00, 0x00, 0x00}))
	if err == nil {
		t.Fatal("err = nil, want error for zero-length frame")
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want a framing error not EOF", err)
	}
}

func TestReadFrameTruncatedBodyIsUnexpectedEOF(t *testing.T) {
	// Claims 10 bytes of body, supplies 2.
	r := strings.NewReader("\x00\x00\x00\x0a\x01\x02")
	_, _, err := ReadFrame(r)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadFrameCleanEOFAtBoundary(t *testing.T) {
	// A peer closing between frames is normal shutdown, not corruption.
	_, _, err := ReadFrame(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestSequentialFramesOnOneStream(t *testing.T) {
	var buf bytes.Buffer
	want := []struct {
		typ     MsgType
		payload []byte
	}{
		{MsgHello, []byte("conn")},
		{MsgPing, nil},
		{MsgKey, []byte{0x2a, 0x01}},
	}
	for _, w := range want {
		if err := WriteFrame(&buf, w.typ, w.payload); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}

	for i, w := range want {
		typ, payload, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("frame %d: ReadFrame: %v", i, err)
		}
		if typ != w.typ || !bytes.Equal(payload, w.payload) {
			t.Errorf("frame %d = (%v, %v), want (%v, %v)", i, typ, payload, w.typ, w.payload)
		}
	}
	if _, _, err := ReadFrame(&buf); !errors.Is(err, io.EOF) {
		t.Errorf("after last frame: err = %v, want io.EOF", err)
	}
}
