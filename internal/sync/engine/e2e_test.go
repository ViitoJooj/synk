package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ViitoJooj/synk/internal/sync/folders"
	"github.com/ViitoJooj/synk/internal/sync/trash"
)

// node is one machine: an engine, a folder on disk, and its folder list kept
// in memory instead of folders.yaml.
type node struct {
	t     *testing.T
	name  string
	e     *Engine
	root  string // the synced folder
	trash *trash.Trash
	tdir  string

	mu   sync.Mutex
	list *folders.File
}

func newNode(t *testing.T, name string) *node {
	t.Helper()
	base := t.TempDir()
	n := &node{
		t:    t,
		name: name,
		root: filepath.Join(base, "code"),
		tdir: filepath.Join(base, "trash"),
		list: &folders.File{},
	}
	if err := os.MkdirAll(n.root, 0o755); err != nil {
		t.Fatal(err)
	}
	n.trash = trash.New(n.tdir)
	n.e = New(Config{
		Name:     name,
		Load:     n.load,
		Edit:     n.edit,
		StateDir: filepath.Join(base, "state"),
		Trash:    n.trash,
		Interval: 20 * time.Millisecond,
	})
	return n
}

func (n *node) load() (*folders.File, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := *n.list
	c.Folders = append([]folders.Folder(nil), n.list.Folders...)
	c.Blocks = append([]string(nil), n.list.Blocks...)
	return &c, nil
}

func (n *node) edit(fn func(*folders.File) error) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return fn(n.list)
}

// share adds the folder as "code", accepted or not.
func (n *node) share(accepted bool) {
	n.mu.Lock()
	n.list.Folders = []folders.Folder{{Name: "code", Path: n.root, Accepted: accepted}}
	n.mu.Unlock()
	n.e.Kick()
}

func (n *node) change(fn func(*folders.Folder)) {
	n.mu.Lock()
	fn(&n.list.Folders[0])
	n.mu.Unlock()
	n.e.Kick()
}

func (n *node) write(rel, body string) {
	n.t.Helper()
	p := filepath.Join(n.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		n.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		n.t.Fatal(err)
	}
}

// writeAt writes with a fixed modification time, for deciding who is newer.
func (n *node) writeAt(rel, body string, at time.Time) {
	n.write(rel, body)
	p := filepath.Join(n.root, filepath.FromSlash(rel))
	if err := os.Chtimes(p, at, at); err != nil {
		n.t.Fatal(err)
	}
}

func (n *node) read(rel string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(n.root, filepath.FromSlash(rel)))
	return string(b), err == nil
}

func (n *node) remove(rel string) {
	if err := os.RemoveAll(filepath.Join(n.root, filepath.FromSlash(rel))); err != nil {
		n.t.Fatal(err)
	}
}

func (n *node) status() FolderStatus {
	for _, f := range n.e.Status().Folders {
		if f.Name == "code" {
			return f
		}
	}
	return FolderStatus{}
}

func (n *node) trashed() []string {
	items, _ := n.trash.List("code")
	var out []string
	for _, it := range items {
		out = append(out, it.Path)
	}
	return out
}

// link runs both engines and a session between them over loopback TCP.
func link(t *testing.T, a, b *node) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	wg.Add(4)
	go func() { defer wg.Done(); a.e.Run(ctx) }()
	go func() { defer wg.Done(); b.e.Run(ctx) }()
	go func() {
		defer wg.Done()
		conn, err := l.Accept()
		if err == nil {
			a.e.Serve(ctx, conn)
		}
	}()
	go func() {
		defer wg.Done()
		conn, err := net.Dial("tcp", l.Addr().String())
		if err == nil {
			b.e.Serve(ctx, conn)
		}
	}()
	t.Cleanup(func() {
		cancel()
		l.Close()
		wg.Wait()
	})
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting until %s", what)
}

// never checks that something does not happen for a while.
func never(t *testing.T, what string, bad func() bool) {
	t.Helper()
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		if bad() {
			t.Fatalf("%s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func has(n *node, rel, body string) func() bool {
	return func() bool { got, ok := n.read(rel); return ok && got == body }
}

func gone(n *node, rel string) func() bool {
	return func() bool { _, ok := n.read(rel); return !ok }
}

func idle(n *node) func() bool {
	return func() bool { return n.status().Phase == PhaseIdle }
}

// synced sets up two nodes that have already been through their first sync.
func synced(t *testing.T) (*node, *node) {
	a, b := newNode(t, "laptop"), newNode(t, "pc")
	a.share(true)
	b.share(true)
	link(t, a, b)
	eventually(t, "both are idle", func() bool { return idle(a)() && idle(b)() })
	return a, b
}

// The worry that shaped the design: the same folder on both machines with
// different content. Nothing is written before accepting; after, nothing is
// lost, and both end with the same files.
func TestFirstSyncPreviewsThenMergesWithoutLosingAnything(t *testing.T) {
	a, b := newNode(t, "laptop"), newNode(t, "pc")
	old, recent := time.Now().Add(-time.Hour), time.Now().Add(-time.Minute)
	a.write("only-laptop.go", "L")
	a.writeAt("README.md", "laptop's readme", old)
	b.write("only-pc.go", "P")
	b.writeAt("README.md", "pc's readme", recent)

	a.share(false)
	b.share(false)
	link(t, a, b)

	eventually(t, "the preview is ready on both", func() bool {
		return a.status().Phase == PhasePreview && b.status().Phase == PhasePreview
	})
	pv := a.status().Preview
	// Two files come: only-pc.go, and the pc's README, which is newer and
	// keeps the name. Two go: only-laptop.go, and the laptop's README, as
	// the copy.
	if pv.Fetch != 2 || pv.Send != 2 || len(pv.Conflicts) != 1 || pv.Conflicts[0] != "README.md" {
		t.Fatalf("laptop's preview = %+v", pv)
	}
	never(t, "a file was written before accepting", func() bool {
		_, x := a.read("only-pc.go")
		_, y := b.read("only-laptop.go")
		return x || y
	})

	a.change(func(f *folders.Folder) { f.Accepted = true })
	b.change(func(f *folders.Folder) { f.Accepted = true })

	for _, n := range []*node{a, b} {
		eventually(t, n.name+" has everything", func() bool {
			return has(n, "only-laptop.go", "L")() && has(n, "only-pc.go", "P")() &&
				has(n, "README.md", "pc's readme")() && has(n, "README (laptop).md", "laptop's readme")()
		})
	}
	eventually(t, "both are idle", func() bool { return idle(a)() && idle(b)() })
}

func TestChangesFlowBothWays(t *testing.T) {
	a, b := synced(t)

	a.write("new.txt", "from the laptop")
	eventually(t, "the pc has the new file", has(b, "new.txt", "from the laptop"))

	b.write("new.txt", "edited on the pc")
	eventually(t, "the laptop has the edit", has(a, "new.txt", "edited on the pc"))
	eventually(t, "the laptop kept its old version in the trash", func() bool {
		return len(a.trashed()) == 1 && a.trashed()[0] == "new.txt"
	})

	b.write("deep/nested/dir/file.go", "package x")
	eventually(t, "the laptop has the nested file", has(a, "deep/nested/dir/file.go", "package x"))
}

func TestADeletionGoesToTheTrash(t *testing.T) {
	a, b := synced(t)
	a.write("doomed.txt", "bye")
	eventually(t, "the pc has it", has(b, "doomed.txt", "bye"))
	eventually(t, "both are idle", func() bool { return idle(a)() && idle(b)() })

	a.remove("doomed.txt")
	eventually(t, "the pc deleted it", gone(b, "doomed.txt"))
	if got := b.trashed(); len(got) != 1 || got[0] != "doomed.txt" {
		t.Errorf("the pc's trash = %v", got)
	}
}

func TestARenameArrivesAsARename(t *testing.T) {
	a, b := synced(t)
	a.write("old-name.go", "package x")
	eventually(t, "the pc has it", has(b, "old-name.go", "package x"))
	eventually(t, "both are idle", func() bool { return idle(a)() && idle(b)() })

	os.Rename(filepath.Join(a.root, "old-name.go"), filepath.Join(a.root, "new-name.go"))
	eventually(t, "the pc has the new name and not the old", func() bool {
		return has(b, "new-name.go", "package x")() && gone(b, "old-name.go")()
	})
}

// Both edit the same file before the other sees it: the most recent wins, and
// the other is in the trash of the machine that had it.
func TestBothEditedTheMostRecentWins(t *testing.T) {
	a, b := synced(t)
	a.write("shared.txt", "v1")
	eventually(t, "the pc has v1", has(b, "shared.txt", "v1"))
	eventually(t, "both are idle", func() bool { return idle(a)() && idle(b)() })

	// Stop the pc from seeing the laptop's edit before making its own.
	b.change(func(f *folders.Folder) { f.Paused = true })
	eventually(t, "the pc is paused", func() bool { return b.status().Phase == PhasePaused })
	a.writeAt("shared.txt", "laptop's edit", time.Now().Add(-time.Minute))
	b.writeAt("shared.txt", "pc's edit", time.Now())
	b.change(func(f *folders.Folder) { f.Paused = false })

	eventually(t, "both have the pc's edit", func() bool {
		return has(a, "shared.txt", "pc's edit")() && has(b, "shared.txt", "pc's edit")()
	})
	if got := a.trashed(); len(got) != 1 {
		t.Errorf("the laptop's trash = %v, want its edit", got)
	}
}

// An unmounted disk looks like an empty folder. It must never become
// "delete everything" on the other side.
func TestAMissingFolderDeletesNothing(t *testing.T) {
	a, b := synced(t)
	for i := range 5 {
		a.write(fmt.Sprintf("f%d.txt", i), "x")
	}
	eventually(t, "the pc has the files", has(b, "f4.txt", "x"))
	eventually(t, "both are idle", func() bool { return idle(a)() && idle(b)() })

	moved := a.root + "-unmounted"
	if err := os.Rename(a.root, moved); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the laptop reports the folder missing", func() bool { return a.status().Phase == PhaseMissing })
	never(t, "the pc deleted files", gone(b, "f0.txt"))
	if _, err := os.Stat(a.root); err == nil {
		t.Error("the missing folder was recreated")
	}
}

func TestTheMassDeletionGuard(t *testing.T) {
	a, b := synced(t)
	for i := range 60 {
		a.write(fmt.Sprintf("many/%02d.txt", i), "x")
	}
	eventually(t, "the pc has them", has(b, "many/59.txt", "x"))
	eventually(t, "both are idle", func() bool { return idle(a)() && idle(b)() })

	a.remove("many")
	eventually(t, "the pc stops", func() bool {
		return b.status().Phase == PhasePaused && strings.Contains(b.status().Reason, "--accept-deletes")
	})
	never(t, "the pc deleted before being told to", gone(b, "many/00.txt"))

	b.change(func(f *folders.Folder) { f.AcceptDeletes = true })
	// All sixty end in the trash, whether the laptop's scan saw the
	// deletion at once or in pieces.
	eventually(t, "the pc deleted all of them, into the trash", func() bool { return len(b.trashed()) == 60 })
	b.mu.Lock()
	used := !b.list.Folders[0].AcceptDeletes
	b.mu.Unlock()
	if !used {
		t.Error("--accept-deletes was not used up")
	}
}

// A block holds in both directions: nothing under it goes out from either
// side, even when only one side blocked it.
func TestABlockHoldsInBothDirections(t *testing.T) {
	a, b := newNode(t, "laptop"), newNode(t, "pc")
	a.list.Blocks = []string{filepath.Join(a.root, "secrets"), "*.env"}
	a.write("secrets/key.pem", "laptop's key")
	a.write(".env", "TOKEN=1")
	b.write("secrets/other.pem", "pc's key")
	b.write("app/.env", "TOKEN=2")
	b.write("app/main.go", "package main")
	a.share(true)
	b.share(true)
	link(t, a, b)

	eventually(t, "the laptop has the unblocked file", has(a, "app/main.go", "package main"))
	eventually(t, "both are idle", func() bool { return idle(a)() && idle(b)() })
	for _, bad := range []func() bool{
		func() bool { _, ok := b.read("secrets/key.pem"); return ok },
		func() bool { _, ok := b.read(".env"); return ok },
		func() bool { _, ok := a.read("secrets/other.pem"); return ok },
		func() bool { _, ok := a.read("app/.env"); return ok },
	} {
		if bad() {
			t.Error("a blocked file crossed over")
		}
	}
}

// While git rewrites a repository, nothing in it moves; after, everything
// does.
func TestARepositoryIsHeldWhileGitWorks(t *testing.T) {
	a, b := synced(t)
	a.write("proj/.git/HEAD", "ref: refs/heads/main")
	a.write("proj/main.go", "v1")
	eventually(t, "the pc has the repo", has(b, "proj/main.go", "v1"))

	a.write("proj/.git/index.lock", "")
	a.write("proj/main.go", "v2 mid-checkout")
	never(t, "a file moved while git held the lock", has(b, "proj/main.go", "v2 mid-checkout"))

	a.remove("proj/.git/index.lock")
	eventually(t, "the pc has the finished checkout", has(b, "proj/main.go", "v2 mid-checkout"))
	if _, ok := b.read("proj/.git/index.lock"); ok {
		t.Error("the lock file itself was copied")
	}
}

// Two different histories of one repository are not merged file by file.
func TestADivergentRepositoryWaitsForASide(t *testing.T) {
	a, b := newNode(t, "laptop"), newNode(t, "pc")
	a.write("proj/.git/HEAD", "ref: refs/heads/main")
	a.write("proj/a.go", "laptop")
	b.write("proj/.git/HEAD", "ref: refs/heads/dev")
	b.write("proj/a.go", "pc")
	b.write("free.txt", "not in a repo")
	a.share(true)
	b.share(true)
	link(t, a, b)

	eventually(t, "the unrelated file arrives", has(a, "free.txt", "not in a repo"))
	eventually(t, "the repository is reported", func() bool {
		d := a.status().Divergent
		return len(d) == 1 && d[0] == "proj"
	})
	never(t, "the held repository was touched", has(a, "proj/a.go", "pc"))

	a.change(func(f *folders.Folder) { f.Prefer = map[string]string{"proj": "peer"} })
	eventually(t, "the laptop took the pc's repository", func() bool {
		return has(a, "proj/a.go", "pc")() && has(a, "proj/.git/HEAD", "ref: refs/heads/dev")()
	})
}

func TestTheExecutableBitSurvives(t *testing.T) {
	a, b := synced(t)
	a.write("build.sh", "#!/bin/sh")
	os.Chmod(filepath.Join(a.root, "build.sh"), 0o755)
	eventually(t, "the pc has it", has(b, "build.sh", "#!/bin/sh"))
	info, err := os.Stat(filepath.Join(b.root, "build.sh"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, %v", info.Mode().Perm(), err)
	}
}

func TestOfferedFolders(t *testing.T) {
	a, b := newNode(t, "laptop"), newNode(t, "pc")
	b.share(true)
	link(t, a, b)
	eventually(t, "the laptop sees the offer", func() bool {
		st := a.e.Status()
		return len(st.Offered) == 1 && st.Offered[0] == "code"
	})
	eventually(t, "the pc says the laptop lacks it", func() bool {
		return b.status().Phase == PhaseWaiting && strings.Contains(b.status().Reason, "same name")
	})
}

// ---- a hostile peer ----

// rogue speaks the protocol by hand, to do what a real engine never would.
type rogue struct {
	t    *testing.T
	conn net.Conn
}

func connectRogue(t *testing.T, n *node, folderNames ...string) *rogue {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); n.e.Run(ctx) }()
	go func() {
		defer wg.Done()
		if server, err := l.Accept(); err == nil {
			n.e.Serve(ctx, server)
		}
	}()
	client, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); client.Close(); wg.Wait() })

	r := &rogue{t: t, conn: client}
	hello := foldersMsg{Version: protocolVersion, Name: "../../evil"}
	for _, name := range folderNames {
		hello.Folders = append(hello.Folders, offer{Name: name})
	}
	r.send(msgFolders, hello)
	return r
}

func (r *rogue) send(typ byte, v any) {
	r.t.Helper()
	var payload []byte
	var err error
	if typ == msgIndex {
		payload, err = encodeIndex(v.(indexMsg))
	} else {
		payload, err = encodeJSON(v)
	}
	if err != nil {
		r.t.Fatal(err)
	}
	if err := writeFrame(r.conn, typ, payload); err != nil {
		r.t.Fatal(err)
	}
}

// answer reads until the reply to a want arrives, and returns whether any
// bytes of the file came with it.
func (r *rogue) answer(id uint64) (gotBytes bool, end fileEndMsg) {
	r.t.Helper()
	r.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		typ, payload, err := readFrame(r.conn)
		if err != nil {
			r.t.Fatalf("reading: %v", err)
		}
		switch typ {
		case msgFileData:
			if got, _, _ := decodeData(payload); got == id {
				gotBytes = true
			}
		case msgFileEnd:
			var m fileEndMsg
			if err := json.Unmarshal(payload, &m); err == nil && m.ID == id {
				return gotBytes, m
			}
		}
	}
}

// A peer may only read what this side indexed: nothing outside the folder,
// nothing blocked, nothing under another name.
func TestAHostilePeerCannotReadOutsideTheFolder(t *testing.T) {
	n := newNode(t, "laptop")
	n.list.Blocks = []string{"*.pem"}
	n.write("public.txt", "fine")
	n.write("key.pem", "secret")
	os.WriteFile(filepath.Join(filepath.Dir(n.root), "outside.txt"), []byte("secret"), 0o644)
	n.share(true)
	r := connectRogue(t, n, "code")

	eventually(t, "the folder is scanned", func() bool { return n.status().Files > 0 })
	hashOf := func(rel string) string {
		n.e.mu.Lock()
		defer n.e.mu.Unlock()
		if e := entryOf(n.e.folders["code"].local, rel); e != nil {
			return e.Hash
		}
		return "0000"
	}

	for i, path := range []string{"../outside.txt", "/etc/passwd", "key.pem", "public.txt\x00", "nothing-here.txt"} {
		r.send(msgWant, wantMsg{ID: uint64(i + 1), Folder: "code", Path: path, Hash: hashOf(path)})
		if gotBytes, end := r.answer(uint64(i + 1)); gotBytes || end.Error == "" {
			t.Errorf("want %q: bytes %v, end %+v", path, gotBytes, end)
		}
	}

	r.send(msgWant, wantMsg{ID: 99, Folder: "code", Path: "public.txt", Hash: hashOf("public.txt")})
	if gotBytes, end := r.answer(99); !gotBytes || end.Error != "" {
		t.Errorf("a legitimate want was refused: %+v", end)
	}
}

// A peer whose index names paths outside the folder gets nothing written.
func TestAHostileIndexWritesNothingOutside(t *testing.T) {
	n := newNode(t, "laptop")
	n.share(true)
	r := connectRogue(t, n, "code")

	evil := `{"entries":{"../escaped.txt":{"p":"../escaped.txt","s":4,"h":"x"},"ok.txt":{"p":"ok.txt","s":2,"h":"y"}}}`
	var snap indexMsg
	snap.Folder = "code"
	if err := json.Unmarshal([]byte(evil), &snap.Snapshot); err != nil {
		t.Fatal(err)
	}
	r.send(msgIndex, snap)

	eventually(t, "the unsafe path is reported", func() bool {
		for _, s := range n.status().Skipped {
			if s.Path == "../escaped.txt" {
				return true
			}
		}
		return false
	})
	if _, err := os.Stat(filepath.Join(filepath.Dir(n.root), "escaped.txt")); err == nil {
		t.Error("a file was written outside the folder")
	}
	// The peer's name went into a path: it must not have escaped either.
	entries, _ := os.ReadDir(filepath.Join(filepath.Dir(n.root), "state"))
	for _, e := range entries {
		if strings.Contains(e.Name(), "..") {
			t.Errorf("state directory %q", e.Name())
		}
	}
}

// Bytes that do not match the hash asked for are thrown away: a file changed
// mid-send, or a peer lying about it.
func TestBytesThatDoNotMatchAreDiscarded(t *testing.T) {
	n := newNode(t, "laptop")
	n.share(true)
	r := connectRogue(t, n, "code")

	var snap indexMsg
	snap.Folder = "code"
	json.Unmarshal([]byte(`{"entries":{"doc.txt":{"p":"doc.txt","s":5,"m":1,"h":"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}}}`), &snap.Snapshot)
	r.send(msgIndex, snap)

	// Wait for the want, then answer it with other bytes of the same size.
	r.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var want wantMsg
	for want.ID == 0 {
		typ, payload, err := readFrame(r.conn)
		if err != nil {
			t.Fatal(err)
		}
		if typ == msgWant {
			json.Unmarshal(payload, &want)
		}
	}
	r.send(msgFileStart, fileStartMsg{ID: want.ID, Size: 5})
	writeFrame(r.conn, msgFileData, encodeData(want.ID, []byte("WORLD")))
	sum := "a5c1a5d7f6f2a5b3" // not the hash of WORLD either; any mismatch will do
	r.send(msgFileEnd, fileEndMsg{ID: want.ID, Hash: sum})

	never(t, "the wrong bytes were kept", func() bool { _, ok := n.read("doc.txt"); return ok })
	entries, _ := os.ReadDir(n.root)
	for _, e := range entries {
		if bytes.HasPrefix([]byte(e.Name()), []byte(".synk-tmp-")) {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}
