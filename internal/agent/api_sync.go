package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/control"
	"github.com/ViitoJooj/synk/internal/sync/folders"
	"github.com/ViitoJooj/synk/internal/sync/trash"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

// syncVerbs are folders.*, blocks.* and trash.*. Changes go through
// folders.Edit, as the command line's do, and then wake the engine so they
// take effect now rather than at the next scan.
func (a *runner) syncVerbs(s *control.Server) {
	h := func(verb string, fn func(json.RawMessage) (any, error)) {
		s.Handle(verb, func(_ context.Context, raw json.RawMessage) (any, error) {
			if a.eng == nil {
				return nil, errorx.Failed("sync folders", errNoEngine)
			}
			return fn(raw)
		})
	}
	edit := func(action string, fn func(*folders.File) error) (any, error) {
		if err := folders.Edit(fn); err != nil {
			return nil, folders.Explain(action, err)
		}
		a.eng.Kick()
		return a.eng.Status(), nil
	}

	h("folders", func(json.RawMessage) (any, error) { return a.eng.Status(), nil })
	h("folders.kick", func(json.RawMessage) (any, error) { a.eng.Kick(); return nil, nil })

	h("folders.add", func(raw json.RawMessage) (any, error) {
		var args struct{ Path, Name string }
		if err := decode(raw, &args); err != nil {
			return nil, err
		}
		return edit("add "+args.Path, func(f *folders.File) error { _, err := f.Add(args.Path, args.Name); return err })
	})
	h("folders.rm", func(raw json.RawMessage) (any, error) {
		var args struct{ Name string }
		if err := decode(raw, &args); err != nil {
			return nil, err
		}
		return edit("remove "+args.Name, func(f *folders.File) error { return f.Remove(args.Name) })
	})
	h("folders.pause", func(raw json.RawMessage) (any, error) {
		var args struct{ Name string }
		if err := decode(raw, &args); err != nil {
			return nil, err
		}
		return edit("pause "+args.Name, func(f *folders.File) error { return f.Pause(args.Name) })
	})
	h("folders.resume", func(raw json.RawMessage) (any, error) {
		var args struct {
			Name          string
			AcceptDeletes bool `json:"accept_deletes"`
		}
		if err := decode(raw, &args); err != nil {
			return nil, err
		}
		return edit("resume "+args.Name, func(f *folders.File) error { return f.Resume(args.Name, args.AcceptDeletes) })
	})
	h("folders.accept", func(raw json.RawMessage) (any, error) {
		var args struct {
			Name   string
			Prefer map[string]string
		}
		if err := decode(raw, &args); err != nil {
			return nil, err
		}
		return edit("accept "+args.Name, func(f *folders.File) error { return f.Accept(args.Name, args.Prefer) })
	})

	h("blocks", func(json.RawMessage) (any, error) {
		list, err := folders.LoadDefault()
		if err != nil {
			return nil, folders.Explain("read the blocks", err)
		}
		return list.BlockList(), nil
	})
	h("blocks.add", func(raw json.RawMessage) (any, error) {
		var args struct{ What string }
		if err := decode(raw, &args); err != nil {
			return nil, err
		}
		return edit("block "+args.What, func(f *folders.File) error { _, err := f.Block(args.What); return err })
	})
	h("blocks.rm", func(raw json.RawMessage) (any, error) {
		var args struct{ What string }
		if err := decode(raw, &args); err != nil {
			return nil, err
		}
		return edit("unblock "+args.What, func(f *folders.File) error { _, err := f.Unblock(args.What); return err })
	})

	h("trash", func(raw json.RawMessage) (any, error) {
		var args struct{ Folder string }
		if err := decode(raw, &args); err != nil {
			return nil, err
		}
		t, err := openTrash()
		if err != nil {
			return nil, err
		}
		items, err := t.List(args.Folder)
		if err != nil {
			return nil, errorx.Failed("read the trash", err)
		}
		return items, nil
	})
	h("trash.restore", func(raw json.RawMessage) (any, error) {
		var args struct {
			ID    string
			Force bool
		}
		if err := decode(raw, &args); err != nil {
			return nil, err
		}
		t, err := openTrash()
		if err != nil {
			return nil, err
		}
		list, err := folders.LoadDefault()
		if err != nil {
			return nil, folders.Explain("read the folders", err)
		}
		name, _, _ := strings.Cut(args.ID, "/")
		f, err := list.Get(name)
		if err != nil {
			return nil, folders.Explain("restore "+args.ID, err)
		}
		dest, err := t.Restore(args.ID, f.Path, args.Force)
		if err != nil {
			return nil, errorx.Failed("restore "+args.ID, err)
		}
		a.eng.Kick()
		return map[string]string{"restored": dest}, nil
	})
}

var errNoEngine = errors.New("folder sync did not start; see 'synk logs'")

func openTrash() (*trash.Trash, error) {
	dir, err := config.TrashDir()
	if err != nil {
		return nil, errorx.Failed("locate the trash", err)
	}
	return trash.New(dir), nil
}
