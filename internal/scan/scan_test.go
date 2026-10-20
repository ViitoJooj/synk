package scan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanIndexesTreeWithSlashPaths(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "hello")
	if err := os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "sub", "deep", "b.txt"), "world")
	mustWrite(t, filepath.Join(root, "Thumbs.db"), "junk")

	idx, err := NewScanner([]string{"Thumbs.db"}, 0).Scan(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"a.txt", "sub", "sub/deep", "sub/deep/b.txt"} {
		if _, ok := idx[want]; !ok {
			t.Errorf("missing %q in index: %v", want, keys(idx))
		}
	}
	if _, ok := idx["Thumbs.db"]; ok {
		t.Error("excluded file was indexed")
	}
	if !idx["sub"].IsDir {
		t.Error("sub should be indexed as a directory")
	}
	if idx["a.txt"].Hash == "" {
		t.Error("files must carry a content hash")
	}
}

func TestSameIgnoresModTime(t *testing.T) {
	a := &Entry{Path: "x", Hash: "abc", ModNs: 1}
	b := &Entry{Path: "x", Hash: "abc", ModNs: 999}
	if !Same(a, b) {
		t.Error("same content with different mtimes should compare equal")
	}
	if Same(a, &Entry{Path: "x", Hash: "different"}) {
		t.Error("different content must not compare equal")
	}
	if Same(a, &Entry{Path: "x", IsDir: true}) {
		t.Error("a file must never equal a directory")
	}
}

func TestFingerprintChangesWithContent(t *testing.T) {
	base := Index{"a": {Path: "a", Hash: "1"}}
	edited := Index{"a": {Path: "a", Hash: "2"}}
	added := Index{"a": {Path: "a", Hash: "1"}, "b": {Path: "b", Hash: "3"}}

	if Fingerprint(base) == Fingerprint(edited) {
		t.Error("editing a file must change the fingerprint")
	}
	if Fingerprint(base) == Fingerprint(added) {
		t.Error("adding a file must change the fingerprint")
	}
	if Fingerprint(base) != Fingerprint(base.Clone()) {
		t.Error("a clone must fingerprint identically")
	}
}

func TestExcludePatterns(t *testing.T) {
	s := NewScanner([]string{"*.tmp", "node_modules", "~$*"}, 0)
	for _, rel := range []string{"x.tmp", "sub/y.tmp", "node_modules", "sub/node_modules", "~$doc.docx"} {
		if !s.Excluded(rel) {
			t.Errorf("%q should be excluded", rel)
		}
	}
	for _, rel := range []string{"x.txt", "tmp/x.go", "notes.md"} {
		if s.Excluded(rel) {
			t.Errorf("%q should not be excluded", rel)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func keys(idx Index) []string {
	out := make([]string, 0, len(idx))
	for k := range idx {
		out = append(out, k)
	}
	return out
}
