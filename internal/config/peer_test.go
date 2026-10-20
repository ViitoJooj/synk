package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetPeerChangesOnlyThePeer(t *testing.T) {
	path := writeConfig(t, `# a máquina do outro lado
peer: old-pc
edge: right # o PC fica à direita
clipboard: true
audio:
  playback: true
`)

	if err := SetPeer(path, "desktop-pc"); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(path)
	got := string(raw)
	for _, keep := range []string{"# a máquina do outro lado", "# o PC fica à direita", "\n  playback: true"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q:\n%s", keep, got)
		}
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("the rewritten file no longer loads: %v\n%s", err, got)
	}
	if cfg.Peer != "desktop-pc" || cfg.EdgeName != "right" || !cfg.Clipboard {
		t.Errorf("got %+v", cfg)
	}
}

// Blank lines and aligned comments are part of how the file reads; a rewrite
// that reflows them is a rewrite nobody asked for.
func TestSetPeerChangesNothingElse(t *testing.T) {
	before := `# cabeçalho

peer: old-pc   # alinhado

edge: right

audio:
  playback: true   # alinhado também
`
	path := writeConfig(t, before)

	if err := SetPeer(path, "new-pc"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	want := strings.Replace(before, "peer: old-pc   # alinhado", "peer: new-pc   # alinhado", 1)
	if string(raw) != want {
		t.Errorf("got\n%s\nwant\n%s", raw, want)
	}
}

// A nested key called peer is not the setting.
func TestSetPeerLeavesNestedKeysAlone(t *testing.T) {
	path := writeConfig(t, "edge: left\nsync:\n  peer: other\n")

	if err := SetPeer(path, "pc"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "  peer: other\n") || !strings.HasPrefix(string(raw), "peer: pc\n") {
		t.Errorf("got\n%s", raw)
	}
}

func TestSetPeerAddsAMissingPeer(t *testing.T) {
	path := writeConfig(t, "edge: left\n")

	if err := SetPeer(path, "pc"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Peer != "pc" {
		t.Errorf("peer = %q", cfg.Peer)
	}
}

// A hostname that YAML would read as something else must stay a string.
func TestSetPeerQuotesWhatWouldNotBeAString(t *testing.T) {
	path := writeConfig(t, "peer: a\nedge: left\n")

	if err := SetPeer(path, "yes"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Peer != "yes" {
		t.Errorf("peer = %q", cfg.Peer)
	}
}

func TestSetPeerKeepsThePermissions(t *testing.T) {
	path := writeConfig(t, "peer: a\nedge: left\n")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	if err := SetPeer(path, "b"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
}

func TestSetPeerNeedsAnExistingFile(t *testing.T) {
	if err := SetPeer(filepath.Join(t.TempDir(), "missing.yaml"), "pc"); err == nil {
		t.Fatal("err = nil, want an error for a missing config")
	}
}

func TestPeerFileRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synk", "peer")

	if got, err := LoadPeerFile(path); err != nil || got != "" {
		t.Fatalf("missing file: got %q, %v; want nothing chosen", got, err)
	}
	if err := SavePeerFile(path, "laptop"); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadPeerFile(path); err != nil || got != "laptop" {
		t.Fatalf("got %q, %v", got, err)
	}
}
