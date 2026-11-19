package engine

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestSafeJoinRejectsEscapes(t *testing.T) {
	root := filepath.Join(string(filepath.Separator)+"srv", "share")

	backslash := string(rune(92))
	nul := string(rune(0))

	bad := []string{
		"",
		".",
		"..",
		"../outside.txt",
		"a/../../outside.txt",
		"/etc/passwd",
		"sub" + backslash + "file.txt",
		"a" + nul + "b",
	}

	for _, rel := range bad {
		if got, err := SafeJoin(root, rel); err == nil {
			t.Errorf("SafeJoin(%q) = %q, want an error", rel, got)
		}
	}
}

func TestSafeJoinAcceptsNormalPaths(t *testing.T) {
	root := filepath.Join(string(filepath.Separator)+"srv", "share")

	cases := map[string]string{
		"file.txt":       filepath.Join(root, "file.txt"),
		"sub/file.txt":   filepath.Join(root, "sub", "file.txt"),
		"a/b/c/deep.txt": filepath.Join(root, "a", "b", "c", "deep.txt"),
		"with space.txt": filepath.Join(root, "with space.txt"),
		"acentuação.txt": filepath.Join(root, "acentuação.txt"),
	}
	for rel, want := range cases {
		got, err := SafeJoin(root, rel)
		if err != nil {
			t.Errorf("SafeJoin(%q) failed: %v", rel, err)
			continue
		}
		if got != want {
			t.Errorf("SafeJoin(%q) = %q, want %q", rel, got, want)
		}
	}
	_ = runtime.GOOS
}
