package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Entry struct {
	Path  string `json:"p"`
	IsDir bool   `json:"d,omitempty"`
	Size  int64  `json:"s,omitempty"`
	ModNs int64  `json:"m,omitempty"`
	Mode  uint32 `json:"o,omitempty"`
	Hash  string `json:"h,omitempty"`
}

type Index map[string]*Entry

type cached struct {
	size  int64
	modNs int64
	hash  string
}

type Scanner struct {
	excludes []string
	maxSize  int64

	mu    sync.Mutex
	cache map[string]cached
}

func NewScanner(excludes []string, maxSize int64) *Scanner {
	return &Scanner{
		excludes: excludes,
		maxSize:  maxSize,
		cache:    make(map[string]cached),
	}
}

func (s *Scanner) Excluded(rel string) bool {
	base := path.Base(rel)
	for _, pat := range s.excludes {
		if strings.EqualFold(pat, base) {
			return true
		}
		if ok, err := path.Match(pat, base); err == nil && ok {
			return true
		}
		if ok, err := path.Match(pat, rel); err == nil && ok {
			return true
		}
	}
	return false
}

func (s *Scanner) Scan(root string) (Index, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}

	idx := make(Index)
	seen := make(map[string]struct{})

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		return s.visit(root, p, d, walkErr, idx, seen)
	})
	if err != nil {
		return nil, err
	}

	s.pruneCache(seen)
	return idx, nil
}

func (s *Scanner) visit(root, p string, d fs.DirEntry, walkErr error, idx Index, seen map[string]struct{}) error {
	if walkErr != nil {
		if p == root {
			return walkErr
		}
		return nil
	}
	if p == root {
		return nil
	}

	rel, err := filepath.Rel(root, p)
	if err != nil {
		return nil
	}
	rel = filepath.ToSlash(rel)

	if s.Excluded(rel) {
		return skipDirOrNil(d)
	}

	info, err := d.Info()
	if err != nil {
		return nil
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return skipDirOrNil(d)
	}

	if d.IsDir() {
		idx[rel] = &Entry{
			Path:  rel,
			IsDir: true,
			ModNs: info.ModTime().UnixNano(),
			Mode:  uint32(info.Mode().Perm()),
		}
		return nil
	}

	return s.indexFile(p, rel, info, idx, seen)
}

func skipDirOrNil(d fs.DirEntry) error {
	if d.IsDir() {
		return fs.SkipDir
	}
	return nil
}

func (s *Scanner) indexFile(p, rel string, info fs.FileInfo, idx Index, seen map[string]struct{}) error {
	if !info.Mode().IsRegular() {
		return nil
	}
	if s.maxSize > 0 && info.Size() > s.maxSize {
		return nil
	}

	hash, err := s.hashCached(p, info.Size(), info.ModTime().UnixNano())
	if err != nil {
		return nil
	}
	seen[p] = struct{}{}
	idx[rel] = &Entry{
		Path:  rel,
		Size:  info.Size(),
		ModNs: info.ModTime().UnixNano(),
		Mode:  uint32(info.Mode().Perm()),
		Hash:  hash,
	}
	return nil
}

func (s *Scanner) Forget(absPath string) {
	s.mu.Lock()
	delete(s.cache, absPath)
	s.mu.Unlock()
}

func (s *Scanner) hashCached(p string, size, modNs int64) (string, error) {
	s.mu.Lock()
	c, ok := s.cache[p]
	s.mu.Unlock()
	if ok && c.size == size && c.modNs == modNs {
		return c.hash, nil
	}

	sum, err := HashFile(p)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	s.cache[p] = cached{size: size, modNs: modNs, hash: sum}
	s.mu.Unlock()
	return sum, nil
}

func (s *Scanner) pruneCache(seen map[string]struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range s.cache {
		if _, ok := seen[p]; !ok {
			delete(s.cache, p)
		}
	}
}

func HashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func Same(a, b *Entry) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.IsDir != b.IsDir {
		return false
	}
	if a.IsDir {
		return true
	}
	return a.Hash == b.Hash
}

func Fingerprint(idx Index) string {
	paths := make([]string, 0, len(idx))
	for p := range idx {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		e := idx[p]
		h.Write([]byte(p))
		if e.IsDir {
			h.Write([]byte("\x00d"))
		} else {
			h.Write([]byte("\x00f" + e.Hash))
		}
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (idx Index) Entries() []Entry {
	out := make([]Entry, 0, len(idx))
	for _, e := range idx {
		out = append(out, *e)
	}
	return out
}

func FromEntries(entries []Entry) Index {
	idx := make(Index, len(entries))
	for i := range entries {
		e := entries[i]
		idx[e.Path] = &e
	}
	return idx
}

func (idx Index) Clone() Index {
	out := make(Index, len(idx))
	for p, e := range idx {
		copied := *e
		out[p] = &copied
	}
	return out
}
