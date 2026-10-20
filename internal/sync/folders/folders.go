// Package folders is the list of synced folders and of blocked paths, kept in
// folders.yaml next to the config.
//
// It is a separate file from config.yaml because both machines need it: the
// Windows side reads no config.yaml on purpose, but it has folders of its own.
// synk writes this file; editing it by hand works, but 'synk folder' and
// 'synk block' check what they write and hand-edits are not.
package folders

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ViitoJooj/synk/internal/config"
)

// FileName is the list inside the config directory.
const FileName = "folders.yaml"

// Folder is one synced folder. The name is what pairs it with the peer's: the
// same name on both machines is the same folder, wherever each keeps it.
type Folder struct {
	Name   string `yaml:"name" json:"name"`
	Path   string `yaml:"path" json:"path"`
	Paused bool   `yaml:"paused,omitempty" json:"paused"`

	// Accepted is set by 'synk folder accept' after the first sync's
	// preview. Until then the folder is compared and nothing is written.
	Accepted bool `yaml:"accepted,omitempty" json:"accepted"`

	// Prefer settles repositories that differ on the first sync, by their
	// path in the folder: "local" or "peer".
	Prefer map[string]string `yaml:"prefer,omitempty" json:"prefer,omitempty"`

	// AcceptDeletes lets one round past the mass-deletion guard, and is
	// cleared when that round has run.
	AcceptDeletes bool `yaml:"accept_deletes,omitempty" json:"accept_deletes,omitempty"`
}

// File is folders.yaml.
type File struct {
	Folders []Folder `yaml:"folders"`
	// Blocks are absolute paths, or patterns without a slash that match a
	// file or directory name anywhere in any folder.
	Blocks []string `yaml:"blocks"`
}

// DefaultBlocks are the patterns a new list starts with: what a build
// produces. Each system makes its own, a Linux binary is no use on Windows,
// and they are large. 'synk unblock' takes any of them off.
var DefaultBlocks = []string{"node_modules", "target", "bin", "obj", ".venv", "venv", "__pycache__", "dist"}

var (
	ErrExists     = errors.New("folders: already in the list")
	ErrNotFound   = errors.New("folders: not in the list")
	ErrForbidden  = errors.New("folders: this path must never be synced")
	ErrBadName    = errors.New("folders: a folder name is lowercase letters, digits, '.', '_' and '-'")
	ErrOverlap    = errors.New("folders: overlaps a folder already synced")
	ErrNotADir    = errors.New("folders: not a directory")
	ErrBlockedDir = errors.New("folders: inside a blocked path")
	ErrBlocked    = errors.New("folders: already blocked")
	ErrBuiltIn    = errors.New("folders: a built-in block")
)

// Path is folders.yaml.
func Path() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads the list, or starts one with the default blocks if there is none.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &File{Blocks: append([]string(nil), DefaultBlocks...)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("folders: reading %s: %w", path, err)
	}
	var f File
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("folders: parsing %s: %w", path, err)
	}
	return &f, nil
}

// Save writes the list in one step.
func (f *File) Save(path string) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return err
	}
	raw := buf.Bytes()
	header := "# synk's synced folders and blocked paths. Written by 'synk folder' and\n" +
		"# 'synk block'; use those, which check what they write.\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+FileName+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append([]byte(header), raw...)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Get finds a folder by name.
func (f *File) Get(name string) (*Folder, error) {
	for i := range f.Folders {
		if f.Folders[i].Name == name {
			return &f.Folders[i], nil
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrNotFound, name)
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// NameFor is the default name for a folder: its directory's name, lowercased,
// with anything else turned into '-'.
func NameFor(path string) string {
	base := strings.ToLower(filepath.Base(path))
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}

// Add puts a folder on the list after every check that keeps it from reaching
// something it should not.
func (f *File) Add(path, name string) (*Folder, error) {
	abs, err := Resolve(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNotADir, abs)
	}
	if name == "" {
		name = NameFor(abs)
	}
	if !validName.MatchString(name) {
		return nil, fmt.Errorf("%w: %q", ErrBadName, name)
	}

	if hard := hardBlockHit(abs); hard != "" {
		return nil, fmt.Errorf("%w: %s is or holds %s", ErrForbidden, abs, hard)
	}
	for _, b := range f.pathBlocks() {
		if within(abs, b) {
			return nil, fmt.Errorf("%w: %s is inside %s", ErrBlockedDir, abs, b)
		}
	}
	for _, other := range f.Folders {
		if other.Name == name {
			return nil, fmt.Errorf("%w: a folder is already called %q", ErrExists, name)
		}
		if within(abs, other.Path) || within(other.Path, abs) {
			return nil, fmt.Errorf("%w: %s and %s (%s)", ErrOverlap, abs, other.Path, other.Name)
		}
	}

	f.Folders = append(f.Folders, Folder{Name: name, Path: abs})
	return &f.Folders[len(f.Folders)-1], nil
}

// Remove takes a folder off the list. Its files are not touched.
func (f *File) Remove(name string) error {
	for i := range f.Folders {
		if f.Folders[i].Name == name {
			f.Folders = append(f.Folders[:i], f.Folders[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrNotFound, name)
}

// Resolve turns what the user typed into a clean absolute path, following
// "~" and symbolic links, so every check compares like with like.
func Resolve(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, p[1:])
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return filepath.Clean(abs), nil
}

// within reports whether p is dir or inside it, ignoring case where the file
// system does.
func within(p, dir string) bool {
	if runtime.GOOS == "windows" {
		p, dir = strings.ToLower(p), strings.ToLower(dir)
	}
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// LoadDefault reads the list from its usual place.
func LoadDefault() (*File, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return Load(path)
}

// SaveDefault writes the list to its usual place.
func SaveDefault(f *File) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return f.Save(path)
}

// Edit loads the list, lets fn change it, and saves it if fn succeeds. It is
// how the command line and the control API both change folders and blocks,
// so they check the same things.
func Edit(fn func(*File) error) error {
	f, err := LoadDefault()
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		return err
	}
	return SaveDefault(f)
}

// Accept lets a folder's first sync run. prefer settles repositories that
// differ on both sides, by their path in the folder: "local" keeps this
// side's version of every differing file in it, "peer" takes the other's.
func (f *File) Accept(name string, prefer map[string]string) error {
	conf, err := f.Get(name)
	if err != nil {
		return err
	}
	for repo, side := range prefer {
		if side != "local" && side != "peer" {
			return fmt.Errorf("folders: %q for %s is neither local nor peer", side, repo)
		}
		if conf.Prefer == nil {
			conf.Prefer = map[string]string{}
		}
		conf.Prefer[repo] = side
	}
	conf.Accepted = true
	return nil
}

// Pause stops a folder: nothing is scanned, sent or written until Resume.
func (f *File) Pause(name string) error {
	conf, err := f.Get(name)
	if err != nil {
		return err
	}
	conf.Paused = true
	return nil
}

// Resume restarts a paused folder. acceptDeletes also lets one round past the
// mass-deletion guard, for when the deletions it stopped are the ones meant.
func (f *File) Resume(name string, acceptDeletes bool) error {
	conf, err := f.Get(name)
	if err != nil {
		return err
	}
	conf.Paused = false
	conf.AcceptDeletes = conf.AcceptDeletes || acceptDeletes
	return nil
}
