package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ErrRootMissing means the folder itself is not there: an unmounted disk, a
// renamed directory. It is never created in its place. An empty directory
// standing where a full one was would read as "everything was deleted", and
// the other side would follow.
var ErrRootMissing = errors.New("index: the folder is not there")

// TempPrefix marks files synk is writing. They are never indexed, so a
// transfer in progress is never sent back, or read as a new file.
const TempPrefix = ".synk-tmp-"

// Excluder says what stays out of the folder's index: blocked paths, build
// output, anything the user chose to keep on one machine.
type Excluder interface {
	Excluded(rel string, dir bool) bool
}

type cached struct {
	size, modNs int64
	hash        string
}

// Scanner indexes one folder. It remembers each file's hash by size and time,
// so only files that changed are read again.
type Scanner struct {
	exclude Excluder
	maxSize int64
	cache   map[string]cached
}

func NewScanner(exclude Excluder, maxSize int64) *Scanner {
	return &Scanner{exclude: exclude, maxSize: maxSize, cache: map[string]cached{}}
}

// Seed lets the scanner reuse the hashes of a previous snapshot, so a restart
// does not read every file again.
func (s *Scanner) Seed(snap *Snapshot) {
	if snap == nil {
		return
	}
	for p, e := range snap.Entries {
		if !e.Dir && e.Hash != "" {
			s.cache[p] = cached{size: e.Size, modNs: e.ModNs, hash: e.Hash}
		}
	}
}

// Scan indexes root.
//
// What cannot be read is held, never left out. Left out, it would look deleted
// to the other machine: a file Windows keeps locked while a program has it
// open would be deleted on the peer. Held, both sides leave it alone until it
// can be read.
func (s *Scanner) Scan(ctx context.Context, root string) (*Snapshot, error) {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrRootMissing, root)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("index: %s is not a directory", root)
	}

	snap := NewSnapshot()
	held := map[string]bool{}
	seen := map[string]bool{}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == root {
			return walkErr
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		if walkErr != nil {
			held[rel] = true
			return skipDir(d)
		}
		if strings.HasPrefix(d.Name(), TempPrefix) {
			return nil
		}
		if repo, ok := gitLock(rel); ok {
			// git is writing this repository; the lock files themselves are
			// never synced.
			held[repo] = true
			return nil
		}
		if s.exclude != nil && s.exclude.Excluded(rel, d.IsDir()) {
			return skipDir(d)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // never followed: it could lead out of the folder
		}

		fi, err := d.Info()
		if err != nil {
			held[rel] = true
			return skipDir(d)
		}
		if d.IsDir() {
			snap.Entries[rel] = &Entry{Path: rel, Dir: true, ModNs: fi.ModTime().UnixNano(), Mode: mode(fi)}
			return nil
		}
		if !fi.Mode().IsRegular() {
			return nil // sockets, devices, pipes: nothing to copy
		}
		if s.maxSize > 0 && fi.Size() > s.maxSize {
			return nil
		}

		hash, err := s.hash(p, rel, fi)
		if err != nil {
			held[rel] = true
			return nil
		}
		seen[rel] = true
		snap.Entries[rel] = &Entry{
			Path:  rel,
			Size:  fi.Size(),
			ModNs: fi.ModTime().UnixNano(),
			Mode:  mode(fi),
			Hash:  hash,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for p := range s.cache {
		if !seen[p] {
			delete(s.cache, p)
		}
	}
	for p := range held {
		snap.Held = append(snap.Held, p)
	}
	sort.Strings(snap.Held)
	return snap, nil
}

func (s *Scanner) hash(abs, rel string, fi fs.FileInfo) (string, error) {
	if c, ok := s.cache[rel]; ok && c.size == fi.Size() && c.modNs == fi.ModTime().UnixNano() {
		return c.hash, nil
	}
	sum, err := HashFile(abs)
	if err != nil {
		return "", err
	}
	s.cache[rel] = cached{size: fi.Size(), modNs: fi.ModTime().UnixNano(), hash: sum}
	return sum, nil
}

// gitLock recognises a lock file inside a repository's .git directory and
// returns the repository's root. Git takes these for the length of any
// operation that rewrites the repository — commit, checkout, rebase, fetch —
// so while one exists, the repository is a mix of before and after.
func gitLock(rel string) (repo string, ok bool) {
	if !strings.HasSuffix(rel, ".lock") {
		return "", false
	}
	switch {
	case strings.HasPrefix(rel, ".git/"):
		return "", true
	default:
		i := strings.Index(rel, "/.git/")
		if i < 0 {
			return "", false
		}
		return rel[:i], true
	}
}

func mode(fi fs.FileInfo) uint32 {
	if runtime.GOOS == "windows" {
		return 0
	}
	return uint32(fi.Mode().Perm())
}

func skipDir(d fs.DirEntry) error {
	if d != nil && d.IsDir() {
		return fs.SkipDir
	}
	return nil
}

// HashFile is the SHA-256 of a file's content.
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
