package transport

import (
	"strings"
	"testing"
)

// The Tailscale installer for Windows does not put its CLI on PATH, so looking
// it up by bare name fails with "the system cannot find the file specified".
// These are the places it actually installs itself.
func TestWindowsCandidatesCoverBothProgramFiles(t *testing.T) {
	got := windowsCandidates(`C:\Program Files`, `C:\Program Files (x86)`)

	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2: %v", len(got), got)
	}
	for _, want := range []string{
		`C:\Program Files\Tailscale\tailscale.exe`,
		`C:\Program Files (x86)\Tailscale\tailscale.exe`,
	} {
		if !contains(got, want) {
			t.Errorf("candidates %v are missing %q", got, want)
		}
	}
}

// A machine with no ProgramFiles(x86) — pure ARM64, say — must not produce a
// candidate rooted at an empty path, which would resolve somewhere unintended.
func TestWindowsCandidatesSkipEmptyRoots(t *testing.T) {
	got := windowsCandidates(`C:\Program Files`, "")

	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %v", len(got), got)
	}
	for _, c := range got {
		if strings.HasPrefix(c, `\Tailscale`) || strings.HasPrefix(c, "/") {
			t.Errorf("candidate %q is rooted at an empty ProgramFiles", c)
		}
	}
}

func TestWindowsCandidatesWithNoEnvironmentAtAll(t *testing.T) {
	if got := windowsCandidates("", ""); len(got) != 0 {
		t.Errorf("got %v, want no candidates when neither root is set", got)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
