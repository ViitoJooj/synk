package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"synk/internal/scan"
)

type Store struct {
	path string

	mu    sync.Mutex
	data  map[string]scan.Index
	tombs map[string]map[string]int64
	dirty bool
}

type persisted struct {
	Agreed map[string]scan.Index       `json:"agreed"`
	Tombs  map[string]map[string]int64 `json:"tombstones"`
}

func Open(dir, peerID string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	s := &Store{
		path:  filepath.Join(dir, "peer-"+peerID+".json"),
		data:  make(map[string]scan.Index),
		tombs: make(map[string]map[string]int64),
	}

	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}

	var stored persisted
	if err := json.Unmarshal(raw, &stored); err != nil {
		return s, nil
	}
	if stored.Agreed != nil {
		s.data = stored.Agreed
	}
	if stored.Tombs != nil {
		s.tombs = stored.Tombs
	}
	return s, nil
}

func (s *Store) Tombs(share string) map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.tombs[share]))
	for p, at := range s.tombs[share] {
		out[p] = at
	}
	return out
}

func (s *Store) PutTomb(share, path string, at int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tombs[share] == nil {
		s.tombs[share] = make(map[string]int64)
	}
	if _, exists := s.tombs[share][path]; exists {
		return
	}
	s.tombs[share][path] = at
	s.dirty = true
}

func (s *Store) RemoveTomb(share, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.tombs[share]; ok {
		delete(m, path)
		s.dirty = true
	}
}

func (s *Store) Snapshot(share string) scan.Index {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, ok := s.data[share]
	if !ok {
		return scan.Index{}
	}
	return idx.Clone()
}

func (s *Store) Lookup(share, path string) *scan.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, ok := s.data[share]
	if !ok {
		return nil
	}
	e, ok := idx[path]
	if !ok {
		return nil
	}
	copied := *e
	return &copied
}

func (s *Store) Put(share string, entry *scan.Entry) {
	if entry == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data[share] == nil {
		s.data[share] = scan.Index{}
	}
	copied := *entry
	s.data[share][entry.Path] = &copied
	s.dirty = true
}

func (s *Store) Remove(share, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx, ok := s.data[share]; ok {
		delete(idx, path)
		s.dirty = true
	}
}

func (s *Store) Save() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	raw, err := json.Marshal(persisted{Agreed: s.data, Tombs: s.tombs})
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		return err
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
