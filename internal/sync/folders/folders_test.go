package folders

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// home makes a fake home directory with a Documents folder and returns it.
func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	if real, err := filepath.EvalSymlinks(h); err == nil {
		h = real
	}
	t.Setenv("HOME", h)
	for _, d := range []string{"Documentos/codes", "Documentos/codes/proj", ".ssh", "Downloads"} {
		if err := os.MkdirAll(filepath.Join(h, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func TestAddAFolder(t *testing.T) {
	h := home(t)
	f := &File{}

	got, err := f.Add(filepath.Join(h, "Documentos", "codes"), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "codes" || got.Path != filepath.Join(h, "Documentos", "codes") || got.Accepted {
		t.Errorf("got %+v", got)
	}
}

func TestAddExpandsTheHomeDirectory(t *testing.T) {
	h := home(t)
	got, err := (&File{}).Add("~/Downloads", "dl")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != filepath.Join(h, "Downloads") {
		t.Errorf("Path = %s", got.Path)
	}
}

// The folders that must never be synced, and the ones around them.
func TestAddRefusesWhatHoldsSecrets(t *testing.T) {
	h := home(t)
	for _, p := range []string{h, filepath.Join(h, ".ssh"), "/", "/etc"} {
		if _, err := (&File{}).Add(p, "x"); !errors.Is(err, ErrForbidden) {
			t.Errorf("Add(%s) = %v, want ErrForbidden", p, err)
		}
	}
}

func TestAddRefusesOverlapsAndDuplicates(t *testing.T) {
	h := home(t)
	f := &File{}
	if _, err := f.Add(filepath.Join(h, "Documentos", "codes"), "codes"); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		path, name string
		want       error
	}{
		"inside":          {filepath.Join(h, "Documentos", "codes", "proj"), "proj", ErrOverlap},
		"around":          {filepath.Join(h, "Documentos"), "docs", ErrOverlap},
		"same name":       {filepath.Join(h, "Downloads"), "codes", ErrExists},
		"bad name":        {filepath.Join(h, "Downloads"), "My Stuff", ErrBadName},
		"not there":       {filepath.Join(h, "nope"), "nope", os.ErrNotExist},
		"not a directory": {filepath.Join(h, "file.txt"), "file", ErrNotADir},
	}
	os.WriteFile(filepath.Join(h, "file.txt"), nil, 0o644)
	for name, c := range cases {
		if _, err := f.Add(c.path, c.name); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
}

func TestAddRefusesAFolderInsideABlockedPath(t *testing.T) {
	h := home(t)
	f := &File{}
	if _, err := f.Block(filepath.Join(h, "Documentos")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Add(filepath.Join(h, "Documentos", "codes"), ""); !errors.Is(err, ErrBlockedDir) {
		t.Errorf("err = %v, want ErrBlockedDir", err)
	}
}

func TestNameFor(t *testing.T) {
	for in, want := range map[string]string{
		"/home/x/codes":        "codes",
		"/home/x/Meus Códigos": "meus-c-digos",
		"/home/x/Área":         "rea",
		"/home/x/.dotfiles":    "dotfiles",
	} {
		if got := NameFor(in); got != want {
			t.Errorf("NameFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBlockPatternsAndPaths(t *testing.T) {
	h := home(t)
	f := &File{}

	if got, err := f.Block("*.env"); err != nil || got != "*.env" {
		t.Fatalf("pattern: %q, %v", got, err)
	}
	secrets := filepath.Join(h, "Documentos", "codes", "proj")
	if got, err := f.Block("~/Documentos/codes/proj"); err != nil || got != secrets {
		t.Fatalf("path: %q, %v", got, err)
	}
	if _, err := f.Block("*.env"); !errors.Is(err, ErrBlocked) {
		t.Errorf("a second block = %v, want ErrBlocked", err)
	}
	if _, err := f.Block("[bad"); err == nil {
		t.Error("an invalid pattern was accepted")
	}

	if _, err := f.Unblock("*.env"); err != nil {
		t.Error(err)
	}
	if _, err := f.Unblock("Thumbs.db"); !errors.Is(err, ErrBuiltIn) {
		t.Errorf("unblocking junk = %v, want ErrBuiltIn", err)
	}
	if _, err := f.Unblock(filepath.Join(h, ".ssh")); !errors.Is(err, ErrBuiltIn) {
		t.Errorf("unblocking ~/.ssh = %v, want ErrBuiltIn", err)
	}
	if _, err := f.Unblock("never-blocked"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestIsPattern(t *testing.T) {
	for s, want := range map[string]bool{
		"node_modules": true, "*.env": true, ".git": true,
		"./secrets": false, "~/keys": false, "/srv/x": false, `C:\x`: false, "a/b": false,
	} {
		if got := IsPattern(s); got != want {
			t.Errorf("IsPattern(%q) = %v", s, got)
		}
	}
}

func TestRulesForAFolder(t *testing.T) {
	h := home(t)
	codes := Folder{Name: "codes", Path: filepath.Join(h, "Documentos", "codes")}
	f := &File{Blocks: []string{
		"node_modules",
		filepath.Join(h, "Documentos", "codes", "proj", "secrets"),
		filepath.Join(h, "Downloads"), // outside: not a rule here
	}}

	r := f.Rules(codes)
	if len(r.Paths) != 1 || r.Paths[0] != "proj/secrets" {
		t.Errorf("Paths = %v", r.Paths)
	}

	for rel, want := range map[string]bool{
		"proj/secrets":         true,
		"proj/secrets/key.pem": true,
		"proj/secretsx":        false,
		"proj/node_modules":    true,
		"web/NODE_MODULES":     true, // Windows does not care about case, so neither do blocks
		"docs/Thumbs.db":       true,
		"report/~$budget.xlsx": true,
		"proj/main.go":         false,
	} {
		if got := r.Excluded(rel, false); got != want {
			t.Errorf("Excluded(%q) = %v, want %v", rel, got, want)
		}
	}
}

// A block on one side holds on the other too.
func TestMergeIsTheUnion(t *testing.T) {
	a := Rules{Patterns: []string{"x"}, Paths: []string{"p"}}
	b := Rules{Patterns: []string{"y", "x"}, Paths: []string{"q"}}
	m := Merge(a, b)
	if len(m.Patterns) != 2 || len(m.Paths) != 2 {
		t.Errorf("got %+v", m)
	}
	if !m.Excluded("q/deep", false) || !m.Excluded("y", false) {
		t.Error("the peer's rules do not apply")
	}
}

func TestLoadStartsWithTheDefaultBlocksAndSaveRoundTrips(t *testing.T) {
	h := home(t)
	path := filepath.Join(t.TempDir(), FileName)

	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Blocks) != len(DefaultBlocks) {
		t.Errorf("Blocks = %v", f.Blocks)
	}
	if _, err := f.Add(filepath.Join(h, "Downloads"), ""); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(path); err != nil {
		t.Fatal(err)
	}

	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Folders) != 1 || again.Folders[0].Name != "downloads" || len(again.Blocks) != len(DefaultBlocks) {
		t.Errorf("got %+v", again)
	}
}
