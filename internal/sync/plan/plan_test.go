package plan

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ViitoJooj/synk/internal/sync/index"
)

// f is a file with content h at time t.
func f(rel, h string, t int64) *index.Entry {
	return &index.Entry{Path: rel, Hash: h, ModNs: t, Size: int64(len(h))}
}

func d(rel string) *index.Entry { return &index.Entry{Path: rel, Dir: true} }

func snap(entries ...*index.Entry) *index.Snapshot {
	s := index.NewSnapshot()
	for _, e := range entries {
		s.Entries[e.Path] = e
	}
	return s
}

func base(entries ...*index.Entry) map[string]*index.Entry {
	return snap(entries...).Entries
}

func in(l, r *index.Snapshot, b map[string]*index.Entry) Input {
	return Input{Local: l, Remote: r, Base: b, LocalName: "laptop", PeerName: "pc"}
}

// steps renders the actions as "kind path[->to]" for comparison.
func steps(p *Plan) string {
	var out []string
	for _, a := range p.Actions {
		s := a.Kind.String() + " " + a.Path
		if a.To != "" {
			s += "->" + a.To
		}
		out = append(out, s)
	}
	return strings.Join(out, "; ")
}

func expect(t *testing.T, p *Plan, want string) {
	t.Helper()
	if got := steps(p); got != want {
		t.Errorf("actions:\n got  %q\n want %q", got, want)
	}
}

// The three-way table, one row per case.
func TestThreeWay(t *testing.T) {
	v1, v2, v3 := "v1", "v2", "v3"
	cases := []struct {
		name    string
		l, r, b *index.Entry
		want    string
	}{
		{"new on the peer", nil, f("a", v1, 1), nil, "pull a"},
		{"new here", f("a", v1, 1), nil, nil, ""},
		{"same on both", f("a", v1, 1), f("a", v1, 5), f("a", v1, 1), ""},
		{"peer deleted the agreed version", f("a", v1, 1), nil, f("a", v1, 1), "delete a"},
		{"peer deleted, but we edited since", f("a", v2, 2), nil, f("a", v1, 1), ""},
		{"we deleted the agreed version", nil, f("a", v1, 1), f("a", v1, 1), ""},
		{"we deleted, but the peer edited since", nil, f("a", v2, 2), f("a", v1, 1), "pull a"},
		{"only the peer edited", f("a", v1, 1), f("a", v2, 2), f("a", v1, 1), "pull a"},
		{"only we edited", f("a", v2, 2), f("a", v1, 1), f("a", v1, 1), ""},
		{"both edited, the peer later", f("a", v2, 2), f("a", v3, 3), f("a", v1, 1), "pull a"},
		{"both edited, we later", f("a", v3, 3), f("a", v2, 2), f("a", v1, 1), ""},
		{"both created apart, the peer later", f("a", v1, 1), f("a", v2, 2), nil, "pull a"},
		{"new directory on the peer", nil, d("a"), nil, "mkdir a"},
		{"peer removed the agreed directory", d("a"), nil, d("a"), "rmdir a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, r, b := snap(), snap(), base()
			if c.l != nil {
				l = snap(c.l)
			}
			if c.r != nil {
				r = snap(c.r)
			}
			if c.b != nil {
				b = base(c.b)
			}
			expect(t, Make(in(l, r, b)), c.want)
		})
	}
}

// A tie in time is broken by name, and the two sides must break it the same
// way or the file would bounce between them forever.
func TestATieIsBrokenTheSameWayOnBothSides(t *testing.T) {
	a, b := f("x", "from-laptop", 7), f("x", "from-pc", 7)

	laptop := Make(Input{Local: snap(a), Remote: snap(b), Base: base(f("x", "old", 1)), LocalName: "laptop", PeerName: "pc"})
	pc := Make(Input{Local: snap(b), Remote: snap(a), Base: base(f("x", "old", 1)), LocalName: "pc", PeerName: "laptop"})

	// "pc" sorts after "laptop", so the pc's version wins on both.
	expect(t, laptop, "pull x")
	expect(t, pc, "")
}

func TestBaseTracksAgreement(t *testing.T) {
	p := Make(in(
		snap(f("same", "s", 1), f("mine", "m", 1), f("pending", "old", 1)),
		snap(f("same", "s", 9), f("pending", "new", 2)),
		base(f("pending", "old", 1), f("gone", "g", 1)),
	))

	if p.Base["same"] == nil {
		t.Error("an agreed path is not in the base")
	}
	if p.Base["mine"] != nil {
		t.Error("a path only one side has was agreed")
	}
	if b := p.Base["pending"]; b == nil || b.Hash != "old" {
		t.Errorf("the base of a path still being synced changed: %+v", b)
	}
	if p.Base["gone"] != nil {
		t.Error("a path both sides deleted stayed in the base")
	}
}

// The executable bit from Linux must survive a trip through Windows, which
// reports no mode at all.
func TestModeSurvivesASideWithoutOne(t *testing.T) {
	script := f("build.sh", "v1", 1)
	script.Mode = 0o755
	fromWindows := f("build.sh", "v2", 2) // Mode 0

	p := Make(in(snap(script), snap(fromWindows), base(script)))
	if len(p.Actions) != 1 || p.Actions[0].Mode != 0o755 {
		t.Fatalf("actions = %+v, want a pull keeping 0755", p.Actions)
	}

	agreed := Make(in(snap(script), snap(f("build.sh", "v1", 5)), nil))
	if agreed.Base["build.sh"].Mode != 0o755 {
		t.Errorf("the base lost the mode: %o", agreed.Base["build.sh"].Mode)
	}
}

// The first sync merges: nothing is deleted, even what would look deleted.
func TestFirstSyncNeverDeletes(t *testing.T) {
	i := in(snap(f("only-here", "x", 1), d("dir-here")), snap(f("only-there", "y", 1)), nil)
	i.First = true
	expect(t, Make(i), "pull only-there")
}

// The case the user worries about: the same path, different content, on the
// first sync. Neither is lost, and both sides end with the same two files.
func TestFirstSyncKeepsBothVersionsOfAConflict(t *testing.T) {
	older, newer := f("README.md", "laptop's", 1), f("README.md", "pc's", 2)

	laptop := Input{Local: snap(older), Remote: snap(newer), LocalName: "laptop", PeerName: "pc", First: true}
	pc := Input{Local: snap(newer), Remote: snap(older), LocalName: "pc", PeerName: "laptop", First: true}

	// The pc's is newer: it keeps the name, and the laptop's becomes the copy.
	expect(t, Make(laptop), "rename-local README.md->README (laptop).md; pull README.md")
	expect(t, Make(pc), "pull-as README.md->README (laptop).md")
}

func TestCopyName(t *testing.T) {
	for in, want := range map[string]string{
		"notes.md":         "notes (pc).md",
		"a/b/archive.tar":  "a/b/archive (pc).tar",
		"Makefile":         "Makefile (pc)",
		".bashrc":          ".bashrc (pc)",
		"dir/.env.example": "dir/.env (pc).example",
	} {
		if got := CopyName(in, "pc"); got != want {
			t.Errorf("CopyName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A repository present on both sides with different content is held on the
// first sync: copying HEAD next to HEAD would break it.
func TestFirstSyncHoldsADivergentRepository(t *testing.T) {
	local := snap(d("proj"), d("proj/.git"), f("proj/.git/HEAD", "main", 1), f("proj/a.go", "a1", 1), f("notes.txt", "n", 1))
	remote := snap(d("proj"), d("proj/.git"), f("proj/.git/HEAD", "dev", 2), f("proj/b.go", "b", 1), f("other.txt", "o", 1))
	i := in(local, remote, nil)
	i.First = true

	p := Make(i)
	if len(p.Divergent) != 1 || p.Divergent[0] != "proj" {
		t.Fatalf("Divergent = %v", p.Divergent)
	}
	// Nothing inside proj moves, not even the file only the peer has.
	expect(t, p, "pull other.txt")
}

func TestFirstSyncTakesTheChosenSideOfARepository(t *testing.T) {
	local := snap(d("proj/.git"), f("proj/.git/HEAD", "main", 5), f("proj/mine.go", "m", 1))
	remote := snap(d("proj/.git"), f("proj/.git/HEAD", "dev", 1), f("proj/theirs.go", "t", 1))

	takePeer := in(local, remote, nil)
	takePeer.First, takePeer.Prefer = true, map[string]Side{"proj": Peer}
	// The peer's HEAD replaces ours even though ours is newer; files only one
	// side has are still merged.
	expect(t, Make(takePeer), "pull proj/theirs.go; pull proj/.git/HEAD")

	keepOurs := in(local, remote, nil)
	keepOurs.First, keepOurs.Prefer = true, map[string]Side{"proj": Local}
	expect(t, Make(keepOurs), "pull proj/theirs.go")
}

// A repository the peer lacks is not divergent: it is simply copied.
func TestARepositoryOnOneSideIsCopied(t *testing.T) {
	i := in(snap(), snap(d("proj"), d("proj/.git"), f("proj/.git/HEAD", "main", 1)), nil)
	i.First = true
	p := Make(i)
	if len(p.Divergent) != 0 {
		t.Errorf("Divergent = %v", p.Divergent)
	}
	expect(t, p, "mkdir proj; mkdir proj/.git; pull proj/.git/HEAD")
}

// Held paths — git mid-operation, unreadable files — are left alone by both
// sides, and their base is kept for when they come back.
func TestHeldPathsAreLeftAlone(t *testing.T) {
	l := snap(f("repo/a", "1", 1), f("free", "1", 1))
	r := snap(f("free", "2", 2))
	r.Held = []string{"repo"}
	p := Make(in(l, r, base(f("repo/a", "1", 1), f("free", "1", 1))))

	expect(t, p, "pull free")
	if p.Base["repo/a"] == nil {
		t.Error("the base of a held path was dropped")
	}
}

func TestExcludedPathsAreLeftAlone(t *testing.T) {
	blocked := func(rel string, dir bool) bool { return rel == "secrets" || rel == "node_modules" }
	i := in(
		snap(f("secrets/key", "mine", 1)),
		snap(f("secrets/key", "theirs", 2), f("node_modules/x/y.js", "y", 1), f("ok", "o", 1)),
		base(f("secrets/key", "old", 1)),
	)
	i.Excluded = blocked
	expect(t, Make(i), "pull ok")
}

// A malicious or broken peer: none of these may be acted on.
func TestUnsafePathsFromThePeerAreSkipped(t *testing.T) {
	var entries []*index.Entry
	for _, rel := range []string{"../escape", "/etc/passwd", "a/../../b", `win\path`, "CON", "x:y"} {
		entries = append(entries, f(rel, "evil", 1))
	}
	entries = append(entries, f("fine.txt", "ok", 1))

	p := Make(in(snap(), snap(entries...), nil))
	expect(t, p, "pull fine.txt")
	if len(p.Skipped) != 6 {
		t.Errorf("Skipped = %v", p.Skipped)
	}
}

func TestCaseCollisionsOnWindows(t *testing.T) {
	i := in(snap(f("Readme.md", "mine", 1)), snap(f("README.md", "theirs", 2), f("a.txt", "1", 1), f("A.TXT", "2", 1)), nil)
	i.CaseInsensitive = true
	p := Make(i)

	// "A.TXT" sorts first and is taken; "a.txt" then collides with it.
	expect(t, p, "pull A.TXT")
	if len(p.Skipped) != 2 {
		t.Errorf("Skipped = %v", p.Skipped)
	}

	i.CaseInsensitive = false
	expect(t, Make(i), "pull A.TXT; pull README.md; pull a.txt")
}

func TestFileAgainstDirectoryIsSkipped(t *testing.T) {
	p := Make(in(snap(f("x", "file", 1)), snap(d("x")), nil))
	expect(t, p, "")
	if len(p.Skipped) != 1 {
		t.Errorf("Skipped = %v", p.Skipped)
	}
}

func TestOrder(t *testing.T) {
	l := snap(d("old"), d("old/deep"), f("old/deep/f", "1", 1), f("gone", "1", 1))
	r := snap(d("new"), d("new/sub"), f("new/sub/f", "1", 1))
	p := Make(in(l, r, base(d("old"), d("old/deep"), f("old/deep/f", "1", 1), f("gone", "1", 1))))

	expect(t, p, "mkdir new; mkdir new/sub; pull new/sub/f; delete gone; delete old/deep/f; rmdir old/deep; rmdir old")
}

func TestCount(t *testing.T) {
	p := Make(in(snap(), snap(f("a", "12345", 1), f("b", "123", 1)), nil))
	if n, bytes := p.Count(Pull); n != 2 || bytes != 8 {
		t.Errorf("Count = %d, %d", n, bytes)
	}
}

func Example_steps() {
	p := Make(in(snap(), snap(f("hello.txt", "hi", 1)), nil))
	fmt.Println(steps(p))
	// Output: pull hello.txt
}
