package engine

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/ViitoJooj/synk/internal/sync/folders"
	"github.com/ViitoJooj/synk/internal/sync/index"
)

// The wire format: a one-byte type, a four-byte big-endian length, the
// payload. Everything is JSON except a file's bytes, and an index, which is
// JSON compressed: a folder with a git history runs to many thousands of
// entries.
const (
	msgFolders byte = iota + 1
	msgIndex
	msgWant
	msgFileStart
	msgFileData
	msgFileEnd
)

const (
	// maxFrame bounds one message. An index is the largest thing sent in one
	// piece; this is room for well over a million entries compressed.
	maxFrame = 256 << 20
	// chunk is how much of a file goes in one message.
	chunk = 256 << 10
)

// protocolVersion changes when two builds could no longer understand each
// other, so the mismatch is reported instead of misread.
const protocolVersion = 1

type foldersMsg struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	// Folders this side has, each with the rules that keep paths out of it.
	Folders []offer `json:"folders"`
}

type offer struct {
	Name  string        `json:"name"`
	Rules folders.Rules `json:"rules"`
}

type indexMsg struct {
	Folder   string          `json:"folder"`
	Snapshot *index.Snapshot `json:"snapshot"`
}

type wantMsg struct {
	ID     uint64 `json:"id"`
	Folder string `json:"folder"`
	Path   string `json:"path"`
	Hash   string `json:"hash"`
}

type fileStartMsg struct {
	ID   uint64 `json:"id"`
	Size int64  `json:"size"`
}

type fileEndMsg struct {
	ID uint64 `json:"id"`
	// Hash is of the bytes actually sent. If the file changed while it was
	// being read, it will not match what was wanted, and the receiver drops
	// the copy instead of keeping a mix of two versions.
	Hash  string `json:"hash,omitempty"`
	Error string `json:"error,omitempty"`
}

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > maxFrame {
		return fmt.Errorf("engine: message of %d bytes is over the limit", len(payload))
	}
	var head [5]byte
	head[0] = typ
	binary.BigEndian.PutUint32(head[1:], uint32(len(payload)))
	if _, err := w.Write(head[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[1:])
	if n > maxFrame {
		return 0, nil, fmt.Errorf("engine: the peer sent a message of %d bytes, over the limit", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return head[0], payload, nil
}

func encodeJSON(v any) ([]byte, error) { return json.Marshal(v) }

func encodeIndex(m indexMsg) ([]byte, error) {
	var buf bytes.Buffer
	z := gzip.NewWriter(&buf)
	if err := json.NewEncoder(z).Encode(m); err != nil {
		return nil, err
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeIndex(payload []byte) (indexMsg, error) {
	z, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return indexMsg{}, err
	}
	// The decompressed size is bounded too: a small message could otherwise
	// expand into all of memory.
	var m indexMsg
	if err := json.NewDecoder(io.LimitReader(z, 4*maxFrame)).Decode(&m); err != nil {
		return indexMsg{}, err
	}
	if m.Snapshot == nil || m.Snapshot.Entries == nil {
		return indexMsg{}, errors.New("engine: an index without entries")
	}
	return m, nil
}

// fileData carries a transfer's id before the bytes, so a chunk can never be
// taken for another file's.
func encodeData(id uint64, b []byte) []byte {
	out := make([]byte, 8+len(b))
	binary.BigEndian.PutUint64(out, id)
	copy(out[8:], b)
	return out
}

func decodeData(payload []byte) (uint64, []byte, error) {
	if len(payload) < 8 {
		return 0, nil, errors.New("engine: a file chunk without an id")
	}
	return binary.BigEndian.Uint64(payload), payload[8:], nil
}
