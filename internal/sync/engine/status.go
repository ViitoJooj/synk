package engine

import (
	"sort"
	"time"

	"github.com/ViitoJooj/synk/internal/sync/plan"
)

// Status is the sync as the command line and the UI show it.
type Status struct {
	Peer      string         `json:"peer,omitempty"`
	Connected bool           `json:"connected"`
	Folders   []FolderStatus `json:"folders"`
	// Offered are the peer's folders this side has not added: adding one
	// with the same name here starts syncing it.
	Offered []string `json:"offered,omitempty"`
}

type FolderStatus struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Phase    string `json:"phase"`
	Reason   string `json:"reason,omitempty"`
	Accepted bool   `json:"accepted"`
	Files    int    `json:"files"`
	Bytes    int64  `json:"bytes"`
	// Pending counts files asked of the peer and not yet here.
	Pending   int         `json:"pending"`
	LastSync  *time.Time  `json:"last_sync,omitempty"`
	Preview   *Preview    `json:"preview,omitempty"`
	Held      []string    `json:"held,omitempty"`
	Skipped   []plan.Skip `json:"skipped,omitempty"`
	Divergent []string    `json:"divergent,omitempty"`
}

func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()

	var st Status
	if s := e.sess; s != nil {
		st.Peer, st.Connected = s.peer, true
		for name := range s.peerFolders {
			if e.folders[name] == nil {
				st.Offered = append(st.Offered, name)
			}
		}
		sort.Strings(st.Offered)
	}

	for _, f := range e.folders {
		fs := FolderStatus{
			Name:      f.conf.Name,
			Path:      f.conf.Path,
			Phase:     f.phase,
			Reason:    f.reason,
			Accepted:  f.conf.Accepted,
			Preview:   f.preview,
			Skipped:   f.skipped,
			Divergent: f.divergent,
		}
		if fs.Phase == "" {
			fs.Phase = PhaseScanning
		}
		if f.local != nil {
			fs.Held = f.local.Held
			for _, en := range f.local.Entries {
				if !en.Dir {
					fs.Files++
					fs.Bytes += en.Size
				}
			}
		}
		switch {
		case f.conf.Paused:
			fs.Phase, fs.Reason = PhasePaused, "paused with 'synk folder pause'"
		case f.localErr != nil:
			fs.Phase, fs.Reason = PhaseMissing, f.localErr.Error()
		case e.sess == nil:
			fs.Phase, fs.Reason = PhaseWaiting, "the peer is not connected"
		}
		if e.sess != nil {
			fs.Pending = e.sess.pendingFor(f.conf.Name)
		}
		if !f.lastSync.IsZero() {
			t := f.lastSync
			fs.LastSync = &t
		}
		st.Folders = append(st.Folders, fs)
	}
	sort.Slice(st.Folders, func(i, j int) bool { return st.Folders[i].Name < st.Folders[j].Name })
	return st
}
