// Package plan decides what this machine does to its copy of a folder, given
// its own index, the peer's, and the base: what the two last agreed on.
//
// It is a pure function on purpose. Every decision that could lose a file is
// made here, where a table of cases can test it, and nowhere else. Both
// machines run the same function over their own copy and converge without
// coordinating anything beyond their indexes: each only ever changes itself.
//
// The base is what makes deletion safe. Without it, "I have a file you lack"
// could mean "I created it" or "you deleted it", and only the base says
// which. A deletion is followed only when the file being deleted is exactly
// the version both sides had agreed on.
package plan

import (
	"path"
	"sort"
	"strings"

	"github.com/ViitoJooj/synk/internal/sync/index"
)

// Kind is what to do to one path.
type Kind int

const (
	// Pull fetches the peer's version into Path, replacing what is there. A
	// replaced version goes to the trash, never away.
	Pull Kind = iota
	// PullAs fetches the peer's version into To, a conflict copy, and leaves
	// Path alone.
	PullAs
	// RenameLocal moves this side's own version of Path aside to To, before a
	// Pull puts the peer's version in its place.
	RenameLocal
	// Mkdir creates a directory the peer has.
	Mkdir
	// Delete moves this side's file to the trash, because the peer deleted
	// the agreed version of it.
	Delete
	// Rmdir removes a directory the peer removed, if it is empty by then.
	Rmdir
)

func (k Kind) String() string {
	return [...]string{"pull", "pull-as", "rename-local", "mkdir", "delete", "rmdir"}[k]
}

// Action is one step. Local is what the plan saw here, and is checked again
// right before acting: a file edited since the scan is left for the next
// round rather than overwritten.
type Action struct {
	Kind   Kind
	Path   string
	To     string       // RenameLocal, PullAs
	Remote *index.Entry // the version to fetch
	Local  *index.Entry // what is expected here now; nil for "nothing"
	// Mode is the permission to give a fetched file. The peer's, unless it
	// reports none (Windows), in which case what this side had is kept, so a
	// script edited on Windows does not come back without its +x.
	Mode uint32
}

// Side names a machine in a preference.
type Side int

const (
	Local Side = iota + 1
	Peer
)

// Input is everything a decision depends on.
type Input struct {
	Local, Remote *index.Snapshot
	Base          map[string]*index.Entry

	// First is a folder's first sync, before any agreement. Nothing is
	// deleted and nothing is overwritten: the two copies are merged, and a
	// path that differs keeps both versions.
	First bool
	// Prefer settles repositories that differ on the first sync, by the
	// repository's path: without an entry they are held.
	Prefer map[string]Side

	// LocalName and PeerName name the machines: in conflict copies, and to
	// break a tie in modification time the same way on both sides.
	LocalName, PeerName string

	// CaseInsensitive is set where the file system folds case (Windows), so
	// "A.txt" arriving where "a.txt" exists is a collision, not a new file.
	CaseInsensitive bool

	// Excluded is the union of both sides' blocks. A path it matches, or one
	// inside a directory it matches, is not touched.
	Excluded func(rel string, dir bool) bool
}

// Skip is a path left alone, and why.
type Skip struct {
	Path string `json:"path"`
	Why  string `json:"why"`
}

// Plan is the outcome.
type Plan struct {
	Actions []Action
	// Base is the agreement after this round: paths where the two sides now
	// match, carried over where they do not yet, and dropped where both
	// sides deleted.
	Base    map[string]*index.Entry
	Skipped []Skip
	// Divergent are repositories that differ on a first sync, held until
	// Prefer settles them.
	Divergent []string
}

// Counts for the preview and the mass-deletion guard.
func (p *Plan) Count(k Kind) (n int, bytes int64) {
	for _, a := range p.Actions {
		if a.Kind == k {
			n++
			if a.Remote != nil {
				bytes += a.Remote.Size
			}
		}
	}
	return n, bytes
}

// Make decides.
func Make(in Input) *Plan {
	p := &Plan{Base: map[string]*index.Entry{}}
	for k, v := range in.Base {
		p.Base[k] = v
	}

	local, remote := in.Local.Entries, in.Remote.Entries
	held := append(append([]string(nil), in.Local.Held...), in.Remote.Held...)

	var divergent map[string]bool
	var repos []string
	if in.First {
		repos = repoRoots(local, remote)
		divergent = divergentRepos(in, repos)
		for r := range divergent {
			if in.Prefer[r] == 0 {
				p.Divergent = append(p.Divergent, r)
				held = append(held, r)
			}
		}
		sort.Strings(p.Divergent)
	}

	collisions := caseCollisions(in, p)

	for _, rel := range union(local, remote) {
		l, r := local[rel], remote[rel]
		if index.Under(rel, held) || excluded(in, rel, l, r) {
			continue
		}
		if r != nil {
			if err := index.CheckRel(rel); err != nil {
				p.skip(rel, "the peer's path is unsafe: "+err.Error())
				continue
			}
		}
		if collisions[rel] {
			continue // already reported
		}

		if index.Same(l, r) {
			p.Base[rel] = merged(l, r)
			continue
		}
		if l != nil && r != nil && l.Dir != r.Dir {
			p.skip(rel, "a file on one side and a directory on the other; rename one")
			continue
		}

		if in.First {
			p.first(in, rel, l, r, repos)
		} else {
			p.decide(in, rel, l, r, in.Base[rel])
		}
	}

	// Both sides deleted it: nothing left to agree on.
	for rel := range p.Base {
		if local[rel] == nil && remote[rel] == nil && !index.Under(rel, held) {
			delete(p.Base, rel)
		}
	}

	p.order()
	return p
}

// decide is the three-way rule for a path that differs between the sides.
func (p *Plan) decide(in Input, rel string, l, r, b *index.Entry) {
	switch {
	case l == nil: // the peer has it, we do not
		if b != nil && index.Same(b, r) {
			return // we deleted it; the peer will follow
		}
		// New there, or edited there after we deleted it: an edit beats a
		// deletion, because losing work is worse than keeping a file.
		p.fetch(in, rel, nil, r)

	case r == nil: // we have it, the peer does not
		if b != nil && index.Same(b, l) {
			p.remove(rel, l) // the peer deleted exactly what we agreed on
		}
		// Otherwise it is new here, or edited here after the peer deleted
		// it; the peer will fetch it.

	case b != nil && index.Same(b, l): // only the peer changed it
		p.fetch(in, rel, l, r)

	case b != nil && index.Same(b, r): // only we changed it
		return

	default: // both changed it: the most recent wins
		if newer(in, r, l) {
			p.fetch(in, rel, l, r)
		}
	}
}

// first merges on a first sync: fetch what is missing, delete nothing, and
// where both sides have different content, keep both.
func (p *Plan) first(in Input, rel string, l, r *index.Entry, repos []string) {
	switch {
	case l == nil:
		p.fetch(in, rel, nil, r)
	case r == nil:
		return
	default:
		if repo, ok := repoOf(rel, repos); ok && in.Prefer[repo] != 0 {
			if in.Prefer[repo] == Peer {
				p.fetch(in, rel, l, r)
			}
			return
		}
		// The more recent version keeps the name on both sides and the other
		// becomes "name (host).ext", named after the machine it came from.
		// Both sides reach the same two files, each by changing only itself.
		if newer(in, r, l) {
			aside := CopyName(rel, in.LocalName)
			p.Actions = append(p.Actions, Action{Kind: RenameLocal, Path: rel, To: aside, Local: l})
			p.fetch(in, rel, nil, r)
		} else {
			p.Actions = append(p.Actions, Action{
				Kind: PullAs, Path: rel, To: CopyName(rel, in.PeerName),
				Remote: r, Mode: modeFor(r, nil, nil),
			})
		}
	}
}

func (p *Plan) fetch(in Input, rel string, l, r *index.Entry) {
	if r.Dir {
		p.Actions = append(p.Actions, Action{Kind: Mkdir, Path: rel, Remote: r, Local: l})
		return
	}
	p.Actions = append(p.Actions, Action{
		Kind: Pull, Path: rel, Remote: r, Local: l,
		Mode: modeFor(r, l, in.Base[rel]),
	})
}

func (p *Plan) remove(rel string, l *index.Entry) {
	k := Delete
	if l.Dir {
		k = Rmdir
	}
	p.Actions = append(p.Actions, Action{Kind: k, Path: rel, Local: l})
}

func (p *Plan) skip(rel, why string) { p.Skipped = append(p.Skipped, Skip{Path: rel, Why: why}) }

// order puts directories before what goes in them, deletions of files before
// the directories that held them, and the deepest directories first.
func (p *Plan) order() {
	rank := func(a Action) int {
		switch a.Kind {
		case Mkdir:
			return 0
		case RenameLocal:
			return 1
		case Pull, PullAs:
			return 2
		case Delete:
			return 3
		default: // Rmdir
			return 4
		}
	}
	sort.SliceStable(p.Actions, func(i, j int) bool {
		a, b := p.Actions[i], p.Actions[j]
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		da, db := strings.Count(a.Path, "/"), strings.Count(b.Path, "/")
		if a.Kind == Rmdir && da != db {
			return da > db
		}
		if da != db {
			return da < db
		}
		return a.Path < b.Path
	})
	sort.Slice(p.Skipped, func(i, j int) bool { return p.Skipped[i].Path < p.Skipped[j].Path })
}

// newer reports whether a should win over b: later modification time, and on
// a tie the machine whose name sorts last, so both sides pick the same one.
func newer(in Input, remote, local *index.Entry) bool {
	if remote.ModNs != local.ModNs {
		return remote.ModNs > local.ModNs
	}
	return in.PeerName > in.LocalName
}

// modeFor keeps an executable bit alive across a machine that has none.
func modeFor(r, l, b *index.Entry) uint32 {
	switch {
	case r.Mode != 0:
		return r.Mode
	case l != nil && l.Mode != 0:
		return l.Mode
	case b != nil && b.Mode != 0:
		return b.Mode
	}
	return 0
}

// merged is the agreed entry for two that match: the local one, with the
// peer's mode where this side reports none.
func merged(l, r *index.Entry) *index.Entry {
	e := *l
	if e.Mode == 0 {
		e.Mode = r.Mode
	}
	return &e
}

// CopyName is where a conflicting version goes: "notes.md" from samsung-book
// becomes "notes (samsung-book).md". Directories and dotfiles take the suffix
// at the end.
func CopyName(rel, host string) string {
	dir, file := path.Split(rel)
	ext := path.Ext(file)
	stem := strings.TrimSuffix(file, ext)
	if stem == "" { // ".bashrc"
		stem, ext = file, ""
	}
	return dir + stem + " (" + host + ")" + ext
}

func excluded(in Input, rel string, l, r *index.Entry) bool {
	if in.Excluded == nil {
		return false
	}
	dir := (l != nil && l.Dir) || (r != nil && r.Dir)
	if in.Excluded(rel, dir) {
		return true
	}
	// The scan skips inside an excluded directory, but the peer's index may
	// not have: check every directory on the way.
	for i := strings.LastIndexByte(rel, '/'); i > 0; i = strings.LastIndexByte(rel[:i], '/') {
		if in.Excluded(rel[:i], true) {
			return true
		}
	}
	return false
}

// caseCollisions finds, on a case-insensitive side, peer paths that would land
// on a different local file of the same name in another case, or on each
// other. They are reported once and left alone: writing them would overwrite.
func caseCollisions(in Input, p *Plan) map[string]bool {
	out := map[string]bool{}
	if !in.CaseInsensitive {
		return out
	}
	taken := map[string]string{}
	for rel := range in.Local.Entries {
		taken[strings.ToLower(rel)] = rel
	}
	remote := make([]string, 0, len(in.Remote.Entries))
	for rel := range in.Remote.Entries {
		remote = append(remote, rel)
	}
	sort.Strings(remote)
	for _, rel := range remote {
		low := strings.ToLower(rel)
		if other, ok := taken[low]; ok && other != rel {
			out[rel] = true
			p.skip(rel, "differs only in letter case from "+other+", and this side cannot hold both")
			continue
		}
		taken[low] = rel
	}
	return out
}

func union(a, b map[string]*index.Entry) []string {
	seen := make(map[string]bool, len(a)+len(b))
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
