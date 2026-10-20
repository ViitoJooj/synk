package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ViitoJooj/synk/internal/sync/index"
	"github.com/ViitoJooj/synk/internal/sync/plan"
)

// apply carries out the actions that need nothing from the peer, and asks the
// peer for the files the rest need. The engine's lock is held.
//
// Every action checks first that the path still holds what the plan saw. A
// file edited since the scan is not overwritten or deleted: it is left for the
// next round, which will see the edit.
func (e *Engine) apply(f *folder, p *plan.Plan) {
	root, name := f.conf.Path, f.conf.Name
	changed := false

	for _, a := range p.Actions {
		var err error
		switch a.Kind {
		case plan.Mkdir:
			err = mkdir(root, a)
		case plan.RenameLocal:
			err = renameLocal(root, a)
		case plan.Delete:
			err = e.remove(name, root, a)
		case plan.Rmdir:
			err = rmdir(root, a)
		case plan.Pull, plan.PullAs:
			e.sess.want(f, a)
			continue
		}
		switch {
		case err == nil:
			changed = true
		case errors.Is(err, errChanged):
			// The next round sees the new state.
		default:
			fmt.Fprintf(e.cfg.Log, "sync: %s: %s %s: %v\n", name, a.Kind, a.Path, err)
		}
	}
	if changed {
		e.Kick()
	}
}

// errChanged means the path no longer holds what the plan saw.
var errChanged = errors.New("changed since the scan")

// holds reports whether the file at abs is still what the scan recorded: the
// same kind, size and modification time, or absent when e is nil.
func holds(abs string, e *index.Entry) error {
	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		if e == nil {
			return nil
		}
		return errChanged
	}
	if err != nil {
		return err
	}
	switch {
	case e == nil:
		return errChanged
	case e.Dir != info.IsDir():
		return errChanged
	case e.Dir:
		return nil
	case !info.Mode().IsRegular(), info.Size() != e.Size, info.ModTime().UnixNano() != e.ModNs:
		return errChanged
	}
	return nil
}

func mkdir(root string, a plan.Action) error {
	abs, err := index.Join(root, a.Path)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(abs); err == nil {
		if info.IsDir() {
			return nil
		}
		return errChanged // a file took the name
	}
	return os.MkdirAll(abs, dirMode(a.Remote))
}

func renameLocal(root string, a plan.Action) error {
	from, err := index.Join(root, a.Path)
	if err != nil {
		return err
	}
	to, err := index.Join(root, a.To)
	if err != nil {
		return err
	}
	if err := holds(from, a.Local); err != nil {
		return err
	}
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%s is already taken", a.To)
	}
	return os.Rename(from, to)
}

// remove sends a file to the trash, never away.
func (e *Engine) remove(folder, root string, a plan.Action) error {
	abs, err := index.Join(root, a.Path)
	if err != nil {
		return err
	}
	if err := holds(abs, a.Local); err != nil {
		return err
	}
	_, err = e.cfg.Trash.Put(folder, a.Path, abs)
	return err
}

// rmdir removes a directory only if it is empty: whatever is still in it is
// something nobody agreed to delete.
func rmdir(root string, a plan.Action) error {
	abs, err := index.Join(root, a.Path)
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errChanged
	}
	return nil
}

// place puts a received file, already verified, where the action says. The
// engine's lock is held.
//
// A version being replaced is linked into the trash first and the new one
// renamed over it: at no moment is the path empty, so a scan can never see
// the file as deleted.
func (e *Engine) place(f *folder, a plan.Action, tmp string) error {
	rel := a.Path
	expect := a.Local
	if a.Kind == plan.PullAs {
		rel, expect = a.To, nil
	}
	abs, err := index.Join(f.conf.Path, rel)
	if err != nil {
		return err
	}
	if err := holds(abs, expect); err != nil {
		return err
	}
	if expect != nil {
		if _, err := e.cfg.Trash.Keep(f.conf.Name, rel, abs); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		mode := os.FileMode(0o644)
		if a.Mode != 0 {
			mode = os.FileMode(a.Mode).Perm()
		}
		if err := os.Chmod(tmp, mode); err != nil {
			return err
		}
	}
	// The peer's time goes with the content, so both scans see the same file
	// and the next round finds nothing to do.
	mtime := time.Unix(0, a.Remote.ModNs)
	if err := os.Chtimes(tmp, mtime, mtime); err != nil {
		return err
	}
	return os.Rename(tmp, abs)
}

func dirMode(e *index.Entry) os.FileMode {
	if e != nil && e.Mode != 0 {
		return os.FileMode(e.Mode).Perm() | 0o700
	}
	return 0o755
}
