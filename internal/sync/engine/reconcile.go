package engine

import (
	"fmt"
	"time"

	"github.com/ViitoJooj/synk/internal/sync/folders"
	"github.com/ViitoJooj/synk/internal/sync/index"
	"github.com/ViitoJooj/synk/internal/sync/plan"
)

// Folder phases, as Status reports them.
const (
	PhaseWaiting  = "waiting"  // no peer, or the peer lacks this folder
	PhasePreview  = "preview"  // first sync compared, waiting for 'synk folder accept'
	PhaseSyncing  = "syncing"  // actions under way
	PhaseIdle     = "idle"     // the two copies match
	PhasePaused   = "paused"   // by the user, or by a guard; see the reason
	PhaseMissing  = "missing"  // the folder is not on disk
	PhaseScanning = "scanning" // not scanned yet
)

// Preview is what a first sync would do, shown before anything is written.
type Preview struct {
	Fetch      int   `json:"fetch"`
	FetchBytes int64 `json:"fetch_bytes"`
	Send       int   `json:"send"`
	SendBytes  int64 `json:"send_bytes"`
	// Conflicts exist on both sides with different content; both versions
	// are kept.
	Conflicts []string `json:"conflicts,omitempty"`
	// Divergent repositories are held until a side is chosen for them.
	Divergent []string `json:"divergent,omitempty"`
}

// reconcile plans a folder against the peer's copy and carries out this
// side's part. The engine's lock is held.
func (e *Engine) reconcile(f *folder) {
	s := e.sess
	peerRules, shared := s.peerFolders[f.conf.Name]
	switch {
	case f.conf.Paused:
		f.phase, f.reason = PhasePaused, "paused with 'synk folder pause'"
		return
	case f.localErr != nil:
		f.phase, f.reason = PhaseMissing, f.localErr.Error()
		return
	case f.local == nil:
		f.phase, f.reason = PhaseScanning, ""
		return
	case !shared:
		f.phase, f.reason = PhaseWaiting, "the peer has no folder called "+f.conf.Name+"; add it there with the same name"
		return
	case f.remote == nil:
		f.phase, f.reason = PhaseWaiting, "waiting for the peer's index"
		return
	}

	rules := folders.Merge(*f.rules.Load(), peerRules)
	in := plan.Input{
		Local:           f.local,
		Remote:          f.remote,
		Base:            f.state.Base,
		First:           !f.state.Agreed,
		Prefer:          prefer(f.conf.Prefer),
		LocalName:       e.cfg.Name,
		PeerName:        s.peer,
		CaseInsensitive: caseInsensitive,
		Excluded:        rules.Excluded,
	}
	p := plan.Make(in)
	f.skipped, f.divergent = p.Skipped, p.Divergent

	if !f.conf.Accepted {
		f.phase, f.reason = PhasePreview, "review with 'synk folder show "+f.conf.Name+"', then 'synk folder accept "+f.conf.Name+"'"
		f.preview = preview(in, p)
		return
	}
	f.preview = nil

	accepted := false
	if !in.First {
		// Counted over the last minute, not per round: an "rm -rf" caught
		// half done by a scan arrives as a few deletions and then the rest,
		// and each half alone could pass.
		recent := f.recentDeletions(time.Now())
		if n := deletions(p); n > 0 && e.tooMany(recent+n, len(f.state.Base)+recent) {
			n += recent
			if !f.conf.AcceptDeletes {
				f.phase = PhasePaused
				f.reason = fmt.Sprintf("the peer's changes would delete %d files here; if that is right, run 'synk folder resume %s --accept-deletes'", n, f.conf.Name)
				return
			}
			e.clearAcceptDeletes(f.conf.Name)
			accepted = true
		}
	}

	e.apply(f, p)
	if accepted {
		// What the user just approved does not count against the next
		// deletion too.
		f.deleted = nil
	} else {
		for range deletions(p) {
			f.deleted = append(f.deleted, time.Now())
		}
	}

	pending := s.pendingFor(f.conf.Name)
	f.state.Base = p.Base
	// The first sync has run to the end only when nothing is left to do and
	// no repository is still held. Agreeing with one held would end first-
	// sync rules for it, and its files would then be settled one by one by
	// "most recent wins": the very merge holding it exists to prevent.
	if !f.state.Agreed && len(p.Actions) == 0 && pending == 0 && len(p.Divergent) == 0 {
		f.state.Agreed = true
	}
	if err := f.state.save(f.path); err != nil {
		fmt.Fprintf(e.cfg.Log, "sync: saving the state of %s: %v\n", f.conf.Name, err)
	}

	if len(p.Actions) == 0 && pending == 0 {
		if f.phase != PhaseIdle {
			f.lastSync = time.Now()
		}
		f.phase, f.reason = PhaseIdle, ""
	} else {
		f.phase, f.reason = PhaseSyncing, fmt.Sprintf("%d changes to apply, %d files on the way", len(p.Actions), pending)
	}
}

// guardWindow is how long deletions add up for the mass-deletion guard.
const guardWindow = time.Minute

func (f *folder) recentDeletions(now time.Time) int {
	keep := f.deleted[:0]
	for _, t := range f.deleted {
		if now.Sub(t) < guardWindow {
			keep = append(keep, t)
		}
	}
	f.deleted = keep
	return len(keep)
}

func deletions(p *plan.Plan) int {
	n, _ := p.Count(plan.Delete)
	return n
}

// tooMany is the mass-deletion guard. An unmounted disk, a folder moved away,
// a checkout of a nearly empty branch: all look like "delete everything" from
// the other side, and none should be followed without asking.
func (e *Engine) tooMany(n, total int) bool {
	if n > e.cfg.MassDeleteCount {
		return true
	}
	return n > 10 && float64(n) > e.cfg.MassDeleteRatio*float64(total)
}

func (e *Engine) clearAcceptDeletes(name string) {
	err := e.cfg.Edit(func(f *folders.File) error {
		if conf, err := f.Get(name); err == nil {
			conf.AcceptDeletes = false
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(e.cfg.Log, "sync: %v\n", err)
	}
}

func prefer(in map[string]string) map[string]plan.Side {
	out := map[string]plan.Side{}
	for repo, side := range in {
		switch side {
		case "local":
			out[repo] = plan.Local
		case "peer":
			out[repo] = plan.Peer
		}
	}
	return out
}

// preview counts both directions. What this side sends is what the peer's
// plan would fetch, so the peer's plan is made here too, from its side.
func preview(in plan.Input, p *plan.Plan) *Preview {
	pv := &Preview{Divergent: p.Divergent}
	pv.Fetch, pv.FetchBytes = p.Count(plan.Pull)
	for _, a := range p.Actions {
		switch a.Kind {
		case plan.PullAs, plan.RenameLocal:
			pv.Conflicts = append(pv.Conflicts, a.Path)
		}
	}

	back := in
	back.Local, back.Remote = in.Remote, in.Local
	back.LocalName, back.PeerName = in.PeerName, in.LocalName
	theirs := plan.Make(back)
	pv.Send, pv.SendBytes = theirs.Count(plan.Pull)
	n, b := theirs.Count(plan.PullAs)
	pv.Send, pv.SendBytes = pv.Send+n, pv.SendBytes+b
	return pv
}

// entryOf is what the snapshot says about rel, for checking before acting.
func entryOf(s *index.Snapshot, rel string) *index.Entry {
	if s == nil {
		return nil
	}
	return s.Entries[rel]
}
