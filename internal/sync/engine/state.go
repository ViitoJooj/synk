package engine

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/ViitoJooj/synk/internal/sync/index"
)

// saved is what a folder remembers between runs, per peer: the agreement is
// between two machines, and pairing with a different one starts over.
type saved struct {
	// Agreed is set once the first sync has run to the end. Until then every
	// round is a first sync: merge, never delete.
	Agreed bool `json:"agreed"`
	// Base is the last agreed version of each path.
	Base map[string]*index.Entry `json:"base"`
}

func statePath(dir, peer, folder string) string {
	return filepath.Join(dir, peer, folder+".json")
}

// loadState reads what a folder remembers. A file that is missing or cannot
// be read is a fresh start, which is the safe direction: an empty base makes
// the next round a first sync, which merges and deletes nothing.
func loadState(path string) saved {
	s := saved{Base: map[string]*index.Entry{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var got saved
	if json.Unmarshal(raw, &got) != nil || got.Base == nil {
		return s
	}
	return got
}

func (s saved) save(path string) error { return writeJSON(path, s) }

func writeJSON(path string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// The last scan of each folder is kept apart from the agreement, which is per
// peer: it exists before any peer connects, so a restart reuses its hashes
// instead of reading every file again.
func localPath(dir, folder string) string { return filepath.Join(dir, "scan", folder+".json") }

func loadLocal(dir, folder string) *index.Snapshot {
	raw, err := os.ReadFile(localPath(dir, folder))
	if err != nil {
		return nil
	}
	var s index.Snapshot
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	return &s
}

func saveLocal(dir, folder string, s *index.Snapshot) error {
	return writeJSON(localPath(dir, folder), s)
}
