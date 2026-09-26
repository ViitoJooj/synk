package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ViitoJooj/synk/internal/sync/folders"
	"github.com/ViitoJooj/synk/internal/sync/index"
	"github.com/ViitoJooj/synk/internal/sync/plan"
)

// helloTimeout bounds the exchange of folder lists at the start.
const helloTimeout = 30 * time.Second

// session is one connection to the peer.
//
// Nothing is written to the connection while the engine's lock is held.
// Both sides send an index when a session starts; if each held its lock while
// writing, each reader would wait for that lock, nobody would read, the
// buffers would fill, and both would hang for good. Control messages go
// through an unbounded queue with its own writer; file bytes are written by
// the sender, which never takes the engine's lock while writing.
type session struct {
	e    *Engine
	conn net.Conn
	peer string

	// peerFolders are the peer's folders, with the rules it applies to each.
	peerFolders map[string]folders.Rules

	wmu sync.Mutex // one frame at a time on the connection
	out queue[frame]
	// wants are the peer's requests for files, served one at a time.
	wants queue[wantMsg]

	nextID  uint64
	pending map[uint64]*pull
	keys    map[string]uint64

	in *incoming // the file being received, if any
}

type frame struct {
	typ     byte
	payload []byte
}

// pull is a file this side asked for.
type pull struct {
	folder string
	action plan.Action
}

type incoming struct {
	id      uint64
	file    *os.File
	hash    hash.Hash
	size    int64
	written int64
}

// Serve runs one session over conn until it breaks or ctx ends. Only one
// session runs at a time.
func (e *Engine) Serve(ctx context.Context, conn net.Conn) error {
	defer conn.Close()
	s := &session{
		e: e, conn: conn,
		pending: map[uint64]*pull{},
		keys:    map[string]uint64{},
		out:     newQueue[frame](),
		wants:   newQueue[wantMsg](),
	}

	e.mu.Lock()
	hello := e.helloMsg()
	e.mu.Unlock()
	payload, err := encodeJSON(hello)
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(helloTimeout))
	if err := writeFrame(conn, msgFolders, payload); err != nil {
		return fmt.Errorf("sync: greeting the peer: %w", err)
	}
	typ, payload, err := readFrame(conn)
	if err != nil {
		return fmt.Errorf("sync: waiting for the peer's greeting: %w", err)
	}
	conn.SetDeadline(time.Time{})
	if typ != msgFolders {
		return errors.New("sync: the peer did not start with its folder list")
	}
	var theirs foldersMsg
	if err := json.Unmarshal(payload, &theirs); err != nil {
		return fmt.Errorf("sync: the peer's folder list: %w", err)
	}
	if theirs.Version != protocolVersion {
		return fmt.Errorf("sync: the peer speaks version %d of the protocol and this side %d; install the same synk on both", theirs.Version, protocolVersion)
	}
	// The name comes from the other machine and ends up in a path on this
	// one; it is reduced to safe characters first.
	s.peer = folders.NameFor(theirs.Name)
	if s.peer == "" {
		s.peer = "peer"
	}
	s.peerFolders = offersOf(theirs)

	e.mu.Lock()
	if e.sess != nil {
		e.mu.Unlock()
		return errors.New("sync: already syncing with a peer")
	}
	e.sess = s
	for _, f := range e.folders {
		s.attach(f)
	}
	for _, f := range e.folders {
		s.sendIndex(f)
	}
	e.mu.Unlock()
	e.cfg.OnChange()
	fmt.Fprintf(e.cfg.Log, "sync: connected to %s\n", s.peer)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	defer func() {
		cancel()
		conn.Close()
		s.out.close()
		s.wants.close()
		wg.Wait()
		e.detach(s)
	}()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	wg.Add(2)
	go func() { defer wg.Done(); s.writer() }()
	go func() { defer wg.Done(); s.sender() }()

	err = s.read()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (e *Engine) helloMsg() foldersMsg {
	m := foldersMsg{Version: protocolVersion, Name: e.cfg.Name}
	for _, f := range e.folders {
		m.Folders = append(m.Folders, offer{Name: f.conf.Name, Rules: *f.rules.Load()})
	}
	return m
}

func offersOf(m foldersMsg) map[string]folders.Rules {
	out := map[string]folders.Rules{}
	for _, o := range m.Folders {
		out[o.Name] = o.Rules
	}
	return out
}

// attach loads what a folder remembers about this peer. The engine's lock is
// held.
func (s *session) attach(f *folder) {
	f.path = statePath(s.e.cfg.StateDir, s.peer, f.conf.Name)
	f.state = loadState(f.path)
	f.remote, f.sentFP = nil, ""
}

func (e *Engine) detach(s *session) {
	e.mu.Lock()
	if e.sess == s {
		e.sess = nil
	}
	for _, f := range e.folders {
		f.remote, f.sentFP, f.preview = nil, "", nil
		if f.phase != PhasePaused && f.phase != PhaseMissing {
			f.phase, f.reason = PhaseWaiting, "the peer is not connected"
		}
	}
	e.mu.Unlock()
	if s.in != nil {
		s.in.file.Close()
		os.Remove(s.in.file.Name())
	}
	fmt.Fprintf(e.cfg.Log, "sync: disconnected from %s\n", s.peer)
	e.cfg.OnChange()
}

// sendFolders tells the peer the folder list changed. The engine's lock is
// held; the message is queued, not written.
func (s *session) sendFolders() {
	if payload, err := encodeJSON(s.e.helloMsg()); err == nil {
		s.out.push(frame{msgFolders, payload})
	}
}

// sendIndex queues a folder's index if it changed since the last one sent.
// A folder that is paused or missing is sent as held entirely, so the peer
// leaves its copy alone instead of reading it as emptied. The engine's lock
// is held.
func (s *session) sendIndex(f *folder) {
	if _, shared := s.peerFolders[f.conf.Name]; !shared {
		return
	}
	snap := f.local
	switch {
	case f.conf.Paused || f.localErr != nil:
		snap = &index.Snapshot{Entries: map[string]*index.Entry{}, Held: []string{""}}
	case snap == nil:
		return // not scanned yet
	}
	fp := snap.Fingerprint()
	if fp == f.sentFP {
		return
	}
	payload, err := encodeIndex(indexMsg{Folder: f.conf.Name, Snapshot: snap})
	if err != nil {
		fmt.Fprintf(s.e.cfg.Log, "sync: encoding the index of %s: %v\n", f.conf.Name, err)
		return
	}
	f.sentFP = fp
	s.out.push(frame{msgIndex, payload})
}

// want asks the peer for a file, once. The engine's lock is held.
func (s *session) want(f *folder, a plan.Action) {
	key := f.conf.Name + "\x00" + a.Path + "\x00" + a.Remote.Hash
	if _, asked := s.keys[key]; asked {
		return
	}
	s.nextID++
	id := s.nextID
	s.keys[key] = id
	s.pending[id] = &pull{folder: f.conf.Name, action: a}
	payload, _ := encodeJSON(wantMsg{ID: id, Folder: f.conf.Name, Path: a.Path, Hash: a.Remote.Hash})
	s.out.push(frame{msgWant, payload})
}

func (s *session) pendingFor(folder string) int {
	n := 0
	for _, p := range s.pending {
		if p.folder == folder {
			n++
		}
	}
	return n
}

func (s *session) done(id uint64) {
	if p, ok := s.pending[id]; ok {
		delete(s.keys, p.folder+"\x00"+p.action.Path+"\x00"+p.action.Remote.Hash)
		delete(s.pending, id)
	}
}

func (s *session) writer() {
	for {
		f, ok := s.out.pop()
		if !ok {
			return
		}
		s.wmu.Lock()
		err := writeFrame(s.conn, f.typ, f.payload)
		s.wmu.Unlock()
		if err != nil {
			s.conn.Close()
			return
		}
	}
}

// read handles everything the peer sends until the connection breaks.
func (s *session) read() error {
	for {
		typ, payload, err := readFrame(s.conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("sync: the peer closed the connection")
			}
			return err
		}
		if err := s.handle(typ, payload); err != nil {
			return err
		}
	}
}

func (s *session) handle(typ byte, payload []byte) error {
	e := s.e
	switch typ {
	case msgFolders:
		var m foldersMsg
		if err := json.Unmarshal(payload, &m); err != nil {
			return err
		}
		e.mu.Lock()
		s.peerFolders = offersOf(m)
		for _, f := range e.folders {
			if _, shared := s.peerFolders[f.conf.Name]; !shared {
				f.remote = nil
			}
			f.sentFP = ""
			s.sendIndex(f)
		}
		e.mu.Unlock()
		e.cfg.OnChange()

	case msgIndex:
		m, err := decodeIndex(payload)
		if err != nil {
			return fmt.Errorf("sync: the peer's index: %w", err)
		}
		e.mu.Lock()
		if f := e.folders[m.Folder]; f != nil {
			f.remote = m.Snapshot
			e.reconcile(f)
		}
		e.mu.Unlock()
		e.cfg.OnChange()

	case msgWant:
		var w wantMsg
		if err := json.Unmarshal(payload, &w); err != nil {
			return err
		}
		s.wants.push(w)

	case msgFileStart:
		var m fileStartMsg
		if err := json.Unmarshal(payload, &m); err != nil {
			return err
		}
		return s.startFile(m)

	case msgFileData:
		id, data, err := decodeData(payload)
		if err != nil {
			return err
		}
		return s.fileData(id, data)

	case msgFileEnd:
		var m fileEndMsg
		if err := json.Unmarshal(payload, &m); err != nil {
			return err
		}
		s.endFile(m)
	}
	return nil
}

// startFile opens a temporary file next to where the transfer will land, so
// the final rename stays on one file system and is atomic.
func (s *session) startFile(m fileStartMsg) error {
	if s.in != nil {
		return errors.New("sync: the peer started a file before finishing the last")
	}
	s.e.mu.Lock()
	p := s.pending[m.ID]
	f := s.e.folders[p.folderOr("")]
	var root string
	if f != nil {
		root = f.conf.Path
	}
	s.e.mu.Unlock()
	if p == nil || f == nil {
		return fmt.Errorf("sync: the peer sent file %d, which nobody asked for", m.ID)
	}
	if p.action.Remote.Size != m.Size {
		// The file changed on the peer since its index; the next index will
		// say how. Receive nothing now.
		s.in = &incoming{id: m.ID, size: m.Size}
		return nil
	}

	dest := p.action.Path
	if p.action.Kind == plan.PullAs {
		dest = p.action.To
	}
	abs, err := index.Join(root, dest)
	if err != nil {
		s.in = &incoming{id: m.ID, size: m.Size}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		s.in = &incoming{id: m.ID, size: m.Size}
		return nil
	}
	file, err := os.CreateTemp(filepath.Dir(abs), index.TempPrefix+randomSuffix())
	if err != nil {
		s.in = &incoming{id: m.ID, size: m.Size}
		return nil
	}
	s.in = &incoming{id: m.ID, file: file, hash: sha256.New(), size: m.Size}
	return nil
}

func (s *session) fileData(id uint64, data []byte) error {
	in := s.in
	if in == nil || in.id != id {
		return errors.New("sync: the peer sent file data out of turn")
	}
	in.written += int64(len(data))
	if in.written > in.size {
		return errors.New("sync: the peer sent more than the file's size")
	}
	if in.file == nil {
		return nil // being discarded
	}
	in.hash.Write(data)
	if _, err := in.file.Write(data); err != nil {
		in.file.Close()
		os.Remove(in.file.Name())
		in.file = nil
	}
	return nil
}

// endFile checks what arrived and, if it is exactly what was asked for, puts
// it in place.
func (s *session) endFile(m fileEndMsg) {
	in := s.in
	s.in = nil
	e := s.e

	e.mu.Lock()
	defer e.mu.Unlock()
	p := s.pending[m.ID]
	s.done(m.ID)
	if in == nil || in.id != m.ID || in.file == nil || p == nil {
		if in != nil && in.file != nil {
			in.file.Close()
			os.Remove(in.file.Name())
		}
		e.Kick()
		return
	}

	tmp := in.file.Name()
	closeErr := in.file.Close()
	got := hex.EncodeToString(in.hash.Sum(nil))
	switch {
	case m.Error != "", closeErr != nil, in.written != in.size, got != m.Hash, got != p.action.Remote.Hash:
		// Changed while being sent, or failed: nothing half-right is kept.
		os.Remove(tmp)
	default:
		if f := e.folders[p.folder]; f != nil {
			if err := e.place(f, p.action, tmp); err != nil {
				os.Remove(tmp)
				if !errors.Is(err, errChanged) {
					fmt.Fprintf(e.cfg.Log, "sync: %s: placing %s: %v\n", p.folder, p.action.Path, err)
				}
			}
		} else {
			os.Remove(tmp)
		}
	}
	e.Kick()
}

// sender serves the peer's requests, one file at a time.
func (s *session) sender() {
	for {
		w, ok := s.wants.pop()
		if !ok {
			return
		}
		if err := s.serveWant(w); err != nil {
			s.conn.Close()
			return
		}
	}
}

// serveWant sends a file the peer asked for, if it may have it.
//
// It may only have what this side itself indexed, with the hash the index
// holds: a request for anything else — a path outside the folder, a blocked
// one, a file this side never offered — is answered with an error and no
// bytes. Otherwise a peer could read any file this user can.
func (s *session) serveWant(w wantMsg) error {
	e := s.e
	e.mu.Lock()
	f := e.folders[w.Folder]
	var root string
	var why string
	switch {
	case f == nil:
		why = "no such folder here"
	case f.conf.Paused:
		why = "the folder is paused here"
	default:
		root = f.conf.Path
		entry := entryOf(f.local, w.Path)
		rules := folders.Merge(*f.rules.Load(), s.peerFolders[w.Folder])
		switch {
		case index.CheckRel(w.Path) != nil:
			why = "unsafe path"
		case entry == nil || entry.Dir:
			why = "not a file this side has"
		case entry.Hash != w.Hash:
			why = "this side has another version now"
		case blocked(rules, w.Path):
			why = "blocked"
		}
	}
	e.mu.Unlock()

	if why != "" {
		return s.writeJSON(msgFileEnd, fileEndMsg{ID: w.ID, Error: why})
	}

	abs, err := index.Join(root, w.Path)
	if err != nil {
		return s.writeJSON(msgFileEnd, fileEndMsg{ID: w.ID, Error: "unsafe path"})
	}
	file, err := os.Open(abs)
	if err != nil {
		return s.writeJSON(msgFileEnd, fileEndMsg{ID: w.ID, Error: "cannot open it"})
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return s.writeJSON(msgFileEnd, fileEndMsg{ID: w.ID, Error: "cannot read it"})
	}

	if err := s.writeJSON(msgFileStart, fileStartMsg{ID: w.ID, Size: info.Size()}); err != nil {
		return err
	}
	h := sha256.New()
	buf := make([]byte, chunk)
	var sent int64
	for sent < info.Size() {
		n, err := file.Read(buf[:min(int64(len(buf)), info.Size()-sent)])
		if n > 0 {
			h.Write(buf[:n])
			if err := s.write(msgFileData, encodeData(w.ID, buf[:n])); err != nil {
				return err
			}
			sent += int64(n)
		}
		if err != nil {
			break
		}
	}
	end := fileEndMsg{ID: w.ID, Hash: hex.EncodeToString(h.Sum(nil))}
	if sent != info.Size() {
		end = fileEndMsg{ID: w.ID, Error: "the file shrank while being sent"}
	}
	return s.writeJSON(msgFileEnd, end)
}

func (s *session) write(typ byte, payload []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return writeFrame(s.conn, typ, payload)
}

func (s *session) writeJSON(typ byte, v any) error {
	payload, err := encodeJSON(v)
	if err != nil {
		return err
	}
	return s.write(typ, payload)
}

// blocked applies the rules to a path and every directory on the way to it.
func blocked(r folders.Rules, rel string) bool {
	if r.Excluded(rel, false) {
		return true
	}
	for i := strings.LastIndexByte(rel, '/'); i > 0; i = strings.LastIndexByte(rel[:i], '/') {
		if r.Excluded(rel[:i], true) {
			return true
		}
	}
	return false
}

func (p *pull) folderOr(def string) string {
	if p == nil {
		return def
	}
	return p.folder
}

func randomSuffix() string {
	var b [6]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:]) + "-"
}
