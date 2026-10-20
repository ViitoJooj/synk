// Package trash keeps every file the sync deletes or overwrites, for a while.
//
// Nothing synk removes from a folder is gone: it is moved here first, under
// <trash>/<folder>/<when>/<path>, and stays until it is older than the
// retention. The trash lives outside every synced folder, so it is never
// synced itself.
package trash

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Retention is how long a trashed file is kept.
const Retention = 30 * 24 * time.Hour

const stampLayout = "20060102-150405"

// ErrExists means a restore would overwrite a file that is there now.
var ErrExists = errors.New("trash: a file is already there")

type Trash struct {
	dir string
	now func() time.Time
}

func New(dir string) *Trash { return &Trash{dir: dir, now: time.Now} }

// Put moves the file or empty directory at abs, which is rel inside folder,
// into the trash and returns where it went.
func (t *Trash) Put(folder, rel, abs string) (string, error) {
	stamp := t.now().Format(stampLayout)
	dest := filepath.Join(t.dir, folder, stamp, filepath.FromSlash(rel))
	// Two versions of one path trashed in the same second keep both.
	for n := 2; exists(dest); n++ {
		dest = filepath.Join(t.dir, folder, fmt.Sprintf("%s-%d", stamp, n), filepath.FromSlash(rel))
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", fmt.Errorf("trash: %w", err)
	}
	if err := move(abs, dest); err != nil {
		return "", fmt.Errorf("trash: moving %s: %w", rel, err)
	}
	return dest, nil
}

// Keep puts a copy of the file at abs in the trash and leaves the file where
// it is, for a caller about to replace it in one rename. Moving it first would
// leave a moment with no file at all, and a scan landing in that moment would
// report it deleted. A hard link costs nothing; a copy is the fallback where
// links do not work.
func (t *Trash) Keep(folder, rel, abs string) (string, error) {
	stamp := t.now().Format(stampLayout)
	dest := filepath.Join(t.dir, folder, stamp, filepath.FromSlash(rel))
	for n := 2; exists(dest); n++ {
		dest = filepath.Join(t.dir, folder, fmt.Sprintf("%s-%d", stamp, n), filepath.FromSlash(rel))
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", fmt.Errorf("trash: %w", err)
	}
	if err := os.Link(abs, dest); err == nil {
		return dest, nil
	}
	if err := copyFile(abs, dest); err != nil {
		return "", fmt.Errorf("trash: keeping %s: %w", rel, err)
	}
	return dest, nil
}

// Item is one trashed file.
type Item struct {
	// ID is what Restore takes: folder/when/path.
	ID      string    `json:"id"`
	Folder  string    `json:"folder"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Trashed time.Time `json:"trashed"`
}

// List returns the trashed files of one folder, or of all with "", newest
// first.
func (t *Trash) List(folder string) ([]Item, error) {
	var out []Item
	folders := []string{folder}
	if folder == "" {
		entries, err := os.ReadDir(t.dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		folders = nil
		for _, e := range entries {
			if e.IsDir() {
				folders = append(folders, e.Name())
			}
		}
	}

	for _, name := range folders {
		stamps, err := os.ReadDir(filepath.Join(t.dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, s := range stamps {
			when, err := parseStamp(s.Name())
			if err != nil || !s.IsDir() {
				continue
			}
			base := filepath.Join(t.dir, name, s.Name())
			filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return nil
				}
				rel, _ := filepath.Rel(base, p)
				rel = filepath.ToSlash(rel)
				out = append(out, Item{
					ID:      name + "/" + s.Name() + "/" + rel,
					Folder:  name,
					Path:    rel,
					Size:    info.Size(),
					Trashed: when,
				})
				return nil
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Trashed.Equal(out[j].Trashed) {
			return out[i].Trashed.After(out[j].Trashed)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Restore puts a trashed file back at root/path, where root is its folder's
// root. A file already there is not overwritten unless force is set, and then
// it goes to the trash itself rather than away.
func (t *Trash) Restore(id, root string, force bool) (string, error) {
	parts := strings.SplitN(id, "/", 3)
	if len(parts) != 3 || strings.Contains(id, "..") {
		return "", fmt.Errorf("trash: %q is not an item from 'synk trash ls'", id)
	}
	folder, rel := parts[0], parts[2]
	src := filepath.Join(t.dir, filepath.FromSlash(id))
	if !exists(src) {
		return "", fmt.Errorf("trash: %q is not in the trash", id)
	}

	dest := filepath.Join(root, filepath.FromSlash(rel))
	if exists(dest) {
		if !force {
			return "", fmt.Errorf("%w: %s", ErrExists, dest)
		}
		if _, err := t.Put(folder, rel, dest); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := move(src, dest); err != nil {
		return "", err
	}
	removeEmptyParents(filepath.Dir(src), t.dir)
	return dest, nil
}

// Prune deletes what is older than the retention, for real this time, and
// returns how many rounds of deletions went.
func (t *Trash) Prune() (int, error) {
	cutoff := t.now().Add(-Retention)
	folders, err := os.ReadDir(t.dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range folders {
		stamps, err := os.ReadDir(filepath.Join(t.dir, f.Name()))
		if err != nil {
			continue
		}
		for _, s := range stamps {
			when, err := parseStamp(s.Name())
			if err != nil || when.After(cutoff) {
				continue
			}
			if err := os.RemoveAll(filepath.Join(t.dir, f.Name(), s.Name())); err == nil {
				n++
			}
		}
	}
	return n, nil
}

// parseStamp reads a round's directory name: "20260926-113000", or with a
// "-2" after it for a second round in the same second.
func parseStamp(name string) (time.Time, error) {
	if len(name) < len(stampLayout) {
		return time.Time{}, fmt.Errorf("trash: %q is not a timestamp", name)
	}
	return time.ParseInLocation(stampLayout, name[:len(stampLayout)], time.Local)
}

// move renames, and copies across file systems when it has to: the trash may
// sit on another disk than the folder.
func move(src, dest string) error {
	err := os.Rename(src, dest)
	if err == nil || !errors.Is(err, syscall.EXDEV) {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(dest, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Remove(src)
	}
	if err := copyFile(src, dest); err != nil {
		return err
	}
	return os.Remove(src)
}

// copyFile copies content, permissions and modification time.
func copyFile(src, dest string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dest)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dest)
		return err
	}
	return os.Chtimes(dest, info.ModTime(), info.ModTime())
}

func removeEmptyParents(dir, stop string) {
	for dir != stop && strings.HasPrefix(dir, stop) {
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
