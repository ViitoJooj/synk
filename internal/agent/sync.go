package agent

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/sync/engine"
	"github.com/ViitoJooj/synk/internal/sync/folders"
	"github.com/ViitoJooj/synk/internal/sync/trash"
)

// syncPort is the link's port plus one: 27432 by default. Folder sync has a
// connection of its own so a large transfer never queues behind a key press.
func syncPort(opts Options) int { return Port(opts.Port, opts.Config) + 1 }

// newEngine builds the folder sync. It is a variable so the agent's tests can
// run without one.
var newEngine = func(out io.Writer, onChange func()) (*engine.Engine, error) {
	state, err := config.StateDir()
	if err != nil {
		return nil, err
	}
	trashDir, err := config.TrashDir()
	if err != nil {
		return nil, err
	}
	return engine.New(engine.Config{
		Name:     machineName(),
		Load:     folders.LoadDefault,
		Edit:     folders.Edit,
		StateDir: state,
		Trash:    trash.New(trashDir),
		Log:      out,
		OnChange: onChange,
	}), nil
}

// machineName names this machine in conflict copies: "README (samsung-book).md".
func machineName() string {
	host, err := os.Hostname()
	if err != nil {
		return "this-machine"
	}
	if n := folders.NameFor(host); n != "" {
		return n
	}
	return "this-machine"
}

// startSync is runSync, replaced in tests.
var startSync = runSync

// pruneTrash empties what is past the retention, once at start and then daily.
func pruneTrash(ctx context.Context, out io.Writer) {
	dir, err := config.TrashDir()
	if err != nil {
		return
	}
	t := trash.New(dir)
	tick := time.NewTicker(24 * time.Hour)
	defer tick.Stop()
	for {
		t.Prune()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
