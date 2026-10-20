// Package engine keeps folders in sync with the peer: it scans them, trades
// indexes, runs the planner, and carries out what it decides.
//
// The engine runs whether or not the peer is connected: scanning goes on, and
// a connection, when one comes, is a session that both sides run the same
// way. There is no client and server in the protocol; which side dials is the
// transport's business.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ViitoJooj/synk/internal/sync/folders"
	"github.com/ViitoJooj/synk/internal/sync/index"
	"github.com/ViitoJooj/synk/internal/sync/plan"
	"github.com/ViitoJooj/synk/internal/sync/trash"
)

// Config is what the engine needs from the outside.
type Config struct {
	// Name is this machine's, for conflict copies and ties.
	Name string

	// Load reads folders.yaml, every round, so a change made by the command
	// line is picked up without a restart. Edit changes it the way
	// folders.Edit does: loading it fresh, so a change made in between is
	// not overwritten.
	Load func() (*folders.File, error)
	Edit func(func(*folders.File) error) error

	StateDir string
	Trash    *trash.Trash

	// Interval is how often folders are scanned.
	Interval time.Duration

	// MaxFileSize leaves larger files out; 0 is no limit.
	MaxFileSize int64

	// MassDelete is the mass-deletion guard: a round that would delete more
	// than Count files, or more than Ratio of the folder once it is past
	// ten, stops and waits for 'synk folder resume --accept-deletes'.
	MassDeleteCount int
	MassDeleteRatio float64

	Log io.Writer
	// OnChange is called after any change to Status, for the control API's
	// live events.
	OnChange func()
}

func (c *Config) defaults() {
	if c.Interval == 0 {
		c.Interval = 5 * time.Second
	}
	if c.MassDeleteCount == 0 {
		c.MassDeleteCount = 50
	}
	if c.MassDeleteRatio == 0 {
		c.MassDeleteRatio = 0.25
	}
	if c.Log == nil {
		c.Log = io.Discard
	}
	if c.OnChange == nil {
		c.OnChange = func() {}
	}
}

// Engine syncs every folder on the list.
type Engine struct {
	cfg Config

	mu      sync.Mutex
	list    *folders.File
	folders map[string]*folder
	sess    *session
	wake    chan struct{}
}

// folder is one synced folder's working state.
type folder struct {
	conf folders.Folder
	// rules is read by the scan, which runs without the engine's lock.
	rules   atomic.Pointer[folders.Rules]
	scanner *index.Scanner

	local    *index.Snapshot
	localErr error
	savedFP  string // of the last scan written to disk

	// deleted are the times of recent deletions, for the mass-deletion
	// guard: a big deletion seen across several scans is still one.
	deleted []time.Time

	// Per session.
	remote *index.Snapshot
	sentFP string
	state  saved
	path   string // of the saved state

	// For Status.
	phase     string
	reason    string
	preview   *Preview
	skipped   []plan.Skip
	divergent []string
	lastSync  time.Time
}

func New(cfg Config) *Engine {
	cfg.defaults()
	return &Engine{cfg: cfg, folders: map[string]*folder{}, wake: make(chan struct{}, 1)}
}

// Kick asks for a scan now instead of at the next tick: after the folder list
// changed, or a transfer finished.
func (e *Engine) Kick() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Run scans and reconciles until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	tick := time.NewTicker(e.cfg.Interval)
	defer tick.Stop()
	for {
		e.round(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		case <-e.wake:
		}
	}
}

// round reloads the list, scans every folder and reconciles each with the
// peer, if there is one.
func (e *Engine) round(ctx context.Context) {
	if err := e.reload(); err != nil {
		fmt.Fprintf(e.cfg.Log, "sync: %v\n", err)
		return
	}

	e.mu.Lock()
	names := make([]string, 0, len(e.folders))
	for n := range e.folders {
		names = append(names, n)
	}
	e.mu.Unlock()
	sort.Strings(names)

	for _, name := range names {
		e.mu.Lock()
		f := e.folders[name]
		if f == nil {
			e.mu.Unlock()
			continue
		}
		scanner, root, paused := f.scanner, f.conf.Path, f.conf.Paused
		e.mu.Unlock()

		// Scanning is the slow part and happens without the lock; a
		// transfer finishing meanwhile is caught by the next scan.
		var snap *index.Snapshot
		var err error
		if !paused {
			snap, err = scanner.Scan(ctx, root)
		}
		if ctx.Err() != nil {
			return
		}

		var persist bool
		e.mu.Lock()
		if f = e.folders[name]; f != nil {
			if !paused {
				if snap != nil {
					if fp := snap.Fingerprint(); fp != f.savedFP {
						f.savedFP, persist = fp, true
					}
				}
				f.local, f.localErr = snap, err
				if err != nil {
					f.local = nil
					if !errors.Is(err, index.ErrRootMissing) {
						fmt.Fprintf(e.cfg.Log, "sync: scanning %s: %v\n", name, err)
					}
				}
			}
			// A paused folder still tells the peer it is held, so the peer
			// leaves its copy alone.
			if s := e.sess; s != nil {
				s.sendIndex(f)
				e.reconcile(f)
			}
		}
		e.mu.Unlock()
		if persist {
			if err := saveLocal(e.cfg.StateDir, name, snap); err != nil {
				fmt.Fprintf(e.cfg.Log, "sync: saving the scan of %s: %v\n", name, err)
			}
		}
	}
	e.cfg.OnChange()
}

// reload reads folders.yaml and brings the working set in line with it.
func (e *Engine) reload() error {
	list, err := e.cfg.Load()
	if err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = list

	keep := map[string]bool{}
	for _, conf := range list.Folders {
		keep[conf.Name] = true
		rules := list.Rules(conf)
		f := e.folders[conf.Name]
		if f == nil || f.conf.Path != conf.Path {
			f = &folder{conf: conf}
			e.folders[conf.Name] = f
			if s := e.sess; s != nil {
				s.attach(f)
			}
		}
		f.conf = conf
		f.rules.Store(&rules)
		if f.scanner == nil {
			f.scanner = index.NewScanner(rulesRef{f}, e.cfg.MaxFileSize)
			f.scanner.Seed(loadLocal(e.cfg.StateDir, conf.Name))
		}
	}
	for name := range e.folders {
		if !keep[name] {
			delete(e.folders, name)
		}
	}
	if s := e.sess; s != nil {
		s.sendFolders()
	}
	return nil
}

// rulesRef reads the folder's current rules each time, so a new block takes
// effect on the next scan without a new scanner.
type rulesRef struct{ f *folder }

func (r rulesRef) Excluded(rel string, dir bool) bool { return r.f.rules.Load().Excluded(rel, dir) }

var caseInsensitive = runtime.GOOS == "windows"
