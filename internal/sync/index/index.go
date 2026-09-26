// Package index describes a synced folder as a set of entries: one per file or
// directory, with its size, modification time and SHA-256, never its content.
//
// Two machines compare these to decide what to move. The content only travels
// once the comparison says it has to.
package index

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// Entry is one file or directory, by its path relative to the folder's root,
// always with forward slashes.
type Entry struct {
	Path string `json:"p"`
	Dir  bool   `json:"d,omitempty"`
	Size int64  `json:"s,omitempty"`
	// ModNs is the modification time in Unix nanoseconds.
	ModNs int64 `json:"m,omitempty"`
	// Mode is the permission bits, or 0 where the file system has none to
	// report: Windows knows no executable bit, and a 0 there must not be read
	// as "take the bit away".
	Mode uint32 `json:"o,omitempty"`
	Hash string `json:"h,omitempty"`
}

// Same reports whether two entries hold the same thing: the same content for
// files, the same kind for directories. Times and modes do not count; they
// differ between machines for reasons that are not edits.
func Same(a, b *Entry) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Dir != b.Dir {
		return false
	}
	return a.Dir || a.Hash == b.Hash
}

// Snapshot is a folder at one moment.
type Snapshot struct {
	Entries map[string]*Entry `json:"entries"`

	// Held are the prefixes nobody may act on this round: a git repository
	// in the middle of an operation, whose files are changing under the scan.
	// "" holds the whole folder. The other side skips them too, so a repo is
	// never copied half-written, and nothing in it is read as deleted either.
	Held []string `json:"held,omitempty"`
}

func NewSnapshot() *Snapshot { return &Snapshot{Entries: map[string]*Entry{}} }

// IsHeld reports whether path falls under a held prefix.
func (s *Snapshot) IsHeld(path string) bool {
	return Under(path, s.Held)
}

// Under reports whether path is one of prefixes or inside one.
func Under(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if p == "" || path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// Fingerprint changes whenever anything another machine would act on changes,
// so an unchanged folder is not sent again.
func (s *Snapshot) Fingerprint() string {
	paths := make([]string, 0, len(s.Entries))
	for p := range s.Entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		e := s.Entries[p]
		h.Write([]byte(p))
		if e.Dir {
			h.Write([]byte("\x00d"))
		} else {
			h.Write([]byte("\x00f" + e.Hash))
		}
		h.Write([]byte("\n"))
	}
	held := append([]string(nil), s.Held...)
	sort.Strings(held)
	for _, p := range held {
		h.Write([]byte("\x00held\x00" + p + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Clone is a deep copy, safe to hand to another goroutine.
func (s *Snapshot) Clone() *Snapshot {
	out := &Snapshot{Entries: make(map[string]*Entry, len(s.Entries)), Held: append([]string(nil), s.Held...)}
	for p, e := range s.Entries {
		c := *e
		out.Entries[p] = &c
	}
	return out
}
