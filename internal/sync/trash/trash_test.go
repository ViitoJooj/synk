package trash

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func put(t *testing.T, tr *Trash, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dest, err := tr.Put("codes", rel, p)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the file is still in the folder")
	}
	return dest
}

func clock(tr *Trash, at time.Time) *time.Time {
	now := at
	tr.now = func() time.Time { return now }
	return &now
}

func TestPutListRestore(t *testing.T) {
	tr, root := New(t.TempDir()), t.TempDir()
	clock(tr, time.Date(2026, 9, 26, 11, 30, 0, 0, time.Local))

	put(t, tr, root, "proj/main.go", "package main")

	items, err := tr.List("codes")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Path != "proj/main.go" || items[0].ID != "codes/20260926-113000/proj/main.go" || items[0].Size != 12 {
		t.Fatalf("items = %+v", items)
	}

	dest, err := tr.Restore(items[0].ID, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(dest); string(body) != "package main" {
		t.Errorf("restored %q", body)
	}
	if left, _ := tr.List(""); len(left) != 0 {
		t.Errorf("still in the trash: %+v", left)
	}
}

// Two versions of one file trashed in the same second: both kept.
func TestSameSecondKeepsBoth(t *testing.T) {
	tr, root := New(t.TempDir()), t.TempDir()
	clock(tr, time.Date(2026, 9, 26, 11, 30, 0, 0, time.Local))

	a := put(t, tr, root, "notes.md", "first")
	b := put(t, tr, root, "notes.md", "second")
	if a == b {
		t.Fatal("the second overwrote the first in the trash")
	}
	if items, _ := tr.List("codes"); len(items) != 2 {
		t.Errorf("items = %+v", items)
	}
}

func TestRestoreDoesNotOverwriteUnlessForced(t *testing.T) {
	tr, root := New(t.TempDir()), t.TempDir()
	clock(tr, time.Date(2026, 9, 26, 11, 30, 0, 0, time.Local))
	put(t, tr, root, "a.txt", "old")
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("new"), 0o644)
	items, _ := tr.List("codes")

	if _, err := tr.Restore(items[0].ID, root, false); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if _, err := tr.Restore(items[0].ID, root, true); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(body) != "old" {
		t.Errorf("got %q", body)
	}
	// The one that was there went to the trash, not away.
	items, _ = tr.List("codes")
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	if body, _ := os.ReadFile(filepath.Join(tr.dir, filepath.FromSlash(items[0].ID))); string(body) != "new" {
		t.Errorf("the overwritten file is %q in the trash", body)
	}
}

func TestRestoreRefusesForeignIDs(t *testing.T) {
	tr := New(t.TempDir())
	for _, id := range []string{"x", "codes/../../etc/passwd", "codes/20260926-113000/missing"} {
		if _, err := tr.Restore(id, t.TempDir(), false); err == nil {
			t.Errorf("Restore(%q) succeeded", id)
		}
	}
}

func TestPruneKeepsTheRetention(t *testing.T) {
	tr, root := New(t.TempDir()), t.TempDir()
	now := clock(tr, time.Date(2026, 8, 1, 10, 0, 0, 0, time.Local))
	put(t, tr, root, "old.txt", "o")
	*now = time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)
	put(t, tr, root, "recent.txt", "r")

	*now = time.Date(2026, 9, 26, 10, 0, 0, 0, time.Local)
	n, err := tr.Prune()
	if err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v", n, err)
	}
	items, _ := tr.List("")
	if len(items) != 1 || items[0].Path != "recent.txt" {
		t.Errorf("items = %+v", items)
	}
}

func TestListOfAnEmptyTrash(t *testing.T) {
	items, err := New(filepath.Join(t.TempDir(), "none")).List("")
	if err != nil || len(items) != 0 {
		t.Errorf("got %v, %v", items, err)
	}
}
