package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scan(t *testing.T, s *Scanner, root string) *Snapshot {
	t.Helper()
	snap, err := s.Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return snap
}

func TestCheckRelRefusesWhatCouldEscapeOrBreakWindows(t *testing.T) {
	bad := []string{
		"", ".", "..", "../x", "a/../../x", "/etc/passwd", "a/./b", "a//b", "a/",
		`a\b`, "a\x00b", "C:/x", "file.txt:stream", "a/b?", "tab\there",
		"nul", "NUL.txt", "dir/com1.log", "trailing.", "trailing ",
	}
	for _, rel := range bad {
		if err := CheckRel(rel); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("CheckRel(%q) = %v, want ErrUnsafePath", rel, err)
		}
	}
	good := []string{"a", "a/b/c.txt", "with space.txt", "acentuação.md", ".git/HEAD", "nullable.go", "console/x"}
	for _, rel := range good {
		if err := CheckRel(rel); err != nil {
			t.Errorf("CheckRel(%q) = %v", rel, err)
		}
	}
}

// The attack that matters: a link inside the folder pointing out of it, and a
// peer asking to write through it.
func TestJoinRefusesToWriteThroughASymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "a")); err != nil {
		t.Skip("cannot create symlinks here:", err)
	}

	if _, err := Join(root, "a/passwd"); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("Join through a symlinked directory = %v, want ErrUnsafePath", err)
	}
	if _, err := Join(root, "a"); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("Join onto a symlink = %v, want ErrUnsafePath", err)
	}
}

func TestJoinAcceptsNewDirectories(t *testing.T) {
	root := t.TempDir()
	got, err := Join(root, "new/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "new", "dir", "file.txt") {
		t.Errorf("got %s", got)
	}
}

func TestScanIndexesFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "hello")
	write(t, root, "sub/b.txt", "world")

	snap := scan(t, NewScanner(nil, 0), root)

	if e := snap.Entries["a.txt"]; e == nil || e.Size != 5 || e.Hash == "" || e.Dir {
		t.Errorf("a.txt = %+v", e)
	}
	if e := snap.Entries["sub"]; e == nil || !e.Dir {
		t.Errorf("sub = %+v", e)
	}
	if e := snap.Entries["sub/b.txt"]; e == nil {
		t.Error("sub/b.txt missing")
	}
	if len(snap.Held) != 0 {
		t.Errorf("Held = %v", snap.Held)
	}
}

// An unmounted disk must never become an empty folder.
func TestScanRefusesAMissingRootAndDoesNotCreateIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "gone")

	_, err := NewScanner(nil, 0).Scan(context.Background(), root)
	if !errors.Is(err, ErrRootMissing) {
		t.Fatalf("err = %v, want ErrRootMissing", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Error("Scan created the missing root")
	}
}

func TestScanSkipsSymlinksAndTempFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "real.txt", "x")
	write(t, root, TempPrefix+"123", "partial")
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Skip("cannot create symlinks here:", err)
	}

	snap := scan(t, NewScanner(nil, 0), root)
	if len(snap.Entries) != 1 || snap.Entries["real.txt"] == nil {
		t.Errorf("entries = %v", keys(snap))
	}
}

// excludeDir leaves out any directory with the given name.
type excludeDir string

func (e excludeDir) Excluded(rel string, dir bool) bool {
	return dir && filepath.Base(rel) == string(e)
}

func TestScanHonoursTheExcluder(t *testing.T) {
	root := t.TempDir()
	write(t, root, "proj/main.go", "package main")
	write(t, root, "proj/node_modules/x/index.js", "x")

	snap := scan(t, NewScanner(excludeDir("node_modules"), 0), root)
	for p := range snap.Entries {
		if strings.Contains(p, "node_modules") {
			t.Errorf("indexed %s", p)
		}
	}
}

// While git rewrites a repository, the whole repository is held, and the lock
// file itself is never indexed.
func TestScanHoldsARepositoryGitIsWriting(t *testing.T) {
	root := t.TempDir()
	write(t, root, "proj/.git/HEAD", "ref: refs/heads/main")
	write(t, root, "proj/.git/index.lock", "")
	write(t, root, "proj/main.go", "package main")
	write(t, root, "other/.git/HEAD", "ref: refs/heads/main")

	snap := scan(t, NewScanner(nil, 0), root)

	if len(snap.Held) != 1 || snap.Held[0] != "proj" {
		t.Fatalf("Held = %v, want [proj]", snap.Held)
	}
	if snap.Entries["proj/.git/index.lock"] != nil {
		t.Error("the lock file was indexed")
	}
	if !snap.IsHeld("proj/main.go") || !snap.IsHeld("proj/.git/HEAD") || snap.IsHeld("other/.git/HEAD") || snap.IsHeld("projx/a") {
		t.Error("IsHeld does not match the repository exactly")
	}
}

func TestScanHoldsARepositoryAtTheRoot(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".git/refs/heads/main.lock", "")
	snap := scan(t, NewScanner(nil, 0), root)
	if len(snap.Held) != 1 || snap.Held[0] != "" || !snap.IsHeld("anything") {
		t.Errorf("Held = %q, want the whole folder", snap.Held)
	}
}

// A file that cannot be read must look held, never deleted.
func TestScanHoldsWhatItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permissions do not stop this user")
	}
	root := t.TempDir()
	write(t, root, "open.txt", "x")
	write(t, root, "locked.txt", "secret")
	if err := os.Chmod(filepath.Join(root, "locked.txt"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(root, "locked.txt"), 0o644) })

	snap := scan(t, NewScanner(nil, 0), root)
	if snap.Entries["locked.txt"] != nil {
		t.Error("an unreadable file was indexed")
	}
	if !snap.IsHeld("locked.txt") {
		t.Errorf("Held = %v, want locked.txt held", snap.Held)
	}
}

func TestScanReusesHashesUntilSizeOrTimeChange(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "aaaa")
	p := filepath.Join(root, "a.txt")
	stamp := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	os.Chtimes(p, stamp, stamp)

	s := NewScanner(nil, 0)
	first := scan(t, s, root).Entries["a.txt"].Hash

	// Same size and time, different content: the cache is trusted.
	os.WriteFile(p, []byte("bbbb"), 0o644)
	os.Chtimes(p, stamp, stamp)
	if got := scan(t, s, root).Entries["a.txt"].Hash; got != first {
		t.Error("hash recomputed though size and time did not change")
	}

	// A new time means a new read.
	later := stamp.Add(time.Second)
	os.Chtimes(p, later, later)
	if got := scan(t, s, root).Entries["a.txt"].Hash; got == first {
		t.Error("hash not recomputed after the time changed")
	}
}

func TestSeedAvoidsRehashingAfterARestart(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "aaaa")
	stamp := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	os.Chtimes(filepath.Join(root, "a.txt"), stamp, stamp)
	before := scan(t, NewScanner(nil, 0), root)

	os.WriteFile(filepath.Join(root, "a.txt"), []byte("bbbb"), 0o644)
	os.Chtimes(filepath.Join(root, "a.txt"), stamp, stamp)

	s := NewScanner(nil, 0)
	s.Seed(before)
	if scan(t, s, root).Entries["a.txt"].Hash != before.Entries["a.txt"].Hash {
		t.Error("the seeded hash was not reused")
	}
}

func TestFingerprintSeesContentAndHolds(t *testing.T) {
	a := NewSnapshot()
	a.Entries["x"] = &Entry{Path: "x", Hash: "1"}
	b := a.Clone()
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("clones differ")
	}
	b.Entries["x"].ModNs = 99 // time alone is not a change
	if a.Fingerprint() != b.Fingerprint() {
		t.Error("a time change altered the fingerprint")
	}
	b.Held = []string{"repo"}
	if a.Fingerprint() == b.Fingerprint() {
		t.Error("holding a repo did not alter the fingerprint")
	}
}

func keys(s *Snapshot) []string {
	var out []string
	for p := range s.Entries {
		out = append(out, p)
	}
	return out
}
