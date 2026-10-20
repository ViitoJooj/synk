package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"synk/internal/config"
	"synk/internal/proto"
	"synk/internal/scan"
	"synk/internal/state"
)

const (
	keepaliveEvery = 30 * time.Second
	idleTimeout    = 100 * time.Second
	stateSaveEvery = 15 * time.Second

	requestTTL = 2 * time.Minute

	tombstoneTTL = 60 * time.Second
)

const logRefusing = "[%s] refusing %s: %v"

const osWindows = "windows"

type wantKey struct {
	share string
	path  string
}

type wantState struct {
	hash string
	at   time.Time
}

type incoming struct {
	share   string
	rel     string
	dest    string
	tmpPath string
	file    *os.File
	digest  hash.Hash
	written int64
	modNs   int64
	mode    uint32
	failed  bool
}

type Session struct {
	eng        *Engine
	c          *proto.Conn
	peerID     string
	peerName   string
	peerOS     string
	peerShares map[string]bool
	outbound   bool
	remoteHost string
	state      *state.Store

	mu       sync.Mutex
	remote   map[string]scan.Index
	sentFP   map[string]string
	inflight map[wantKey]wantState
	in       *incoming

	reconcileMu sync.Mutex

	sendQ     chan proto.Want
	wake      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newSession(e *Engine, c *proto.Conn, peer *proto.Hello, outbound bool) (*Session, error) {
	store, err := state.Open(config.StateDir(), peer.NodeID)
	if err != nil {
		return nil, fmt.Errorf("opening sync state: %w", err)
	}

	shares := make(map[string]bool, len(peer.Shares))
	for _, name := range peer.Shares {
		shares[name] = true
	}

	host, _, err := net.SplitHostPort(c.RemoteAddr().String())
	if err != nil {
		host = ""
	}

	return &Session{
		eng:        e,
		c:          c,
		peerID:     peer.NodeID,
		peerName:   peer.NodeName,
		peerOS:     peer.OS,
		peerShares: shares,
		outbound:   outbound,
		remoteHost: host,
		state:      store,
		remote:     make(map[string]scan.Index),
		sentFP:     make(map[string]string),
		inflight:   make(map[wantKey]wantState),
		sendQ:      make(chan proto.Want, 1024),
		wake:       make(chan struct{}, 1),
		done:       make(chan struct{}),
	}, nil
}

func (s *Session) PeerName() string { return s.peerName }
func (s *Session) PeerID() string   { return s.peerID }

func (s *Session) Stop() { s.close() }

func (s *Session) serve() {
	go s.senderLoop()
	go s.wakeLoop()
	go s.housekeepingLoop()

	s.readLoop()

	s.close()
	s.abortIncoming()
	if err := s.state.Save(); err != nil {
		s.eng.log.Printf(logSaveStateFailed, s.peerID, err)
	}
}

func (s *Session) close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.c.Close()
	})
}

func (s *Session) fail(context string, err error) {
	select {
	case <-s.done:
		return
	default:
	}
	s.eng.log.Printf("%s (%s): %v", context, s.peerName, err)
	s.close()
}

func (s *Session) readLoop() {
	for {
		if err := s.c.SetReadDeadline(time.Now().Add(idleTimeout)); err != nil {
			return
		}
		t, payload, err := s.c.ReadFrame()
		if err != nil {
			select {
			case <-s.done:
			default:
				if err != io.EOF {
					s.eng.log.Printf("connection to %s lost: %v", s.peerName, err)
				}
			}
			return
		}

		switch t {
		case proto.TypeIndex:
			s.handleIndex(payload)
		case proto.TypeWant:
			s.handleWant(payload)
		case proto.TypeFileStart:
			s.handleFileStart(payload)
		case proto.TypeFileData:
			s.handleFileData(payload)
		case proto.TypeFileEnd:
			s.handleFileEnd(payload)
		case proto.TypeMkdir:
			s.handleMkdir(payload)
		case proto.TypeDelete:
			s.handleDelete(payload)
		case proto.TypePing:

		default:
			s.eng.log.Printf("ignoring unknown frame %s from %s", t, s.peerName)
		}
	}
}

func (s *Session) housekeepingLoop() {
	ping := time.NewTicker(keepaliveEvery)
	defer ping.Stop()
	save := time.NewTicker(stateSaveEvery)
	defer save.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ping.C:
			if err := s.c.WriteFrame(proto.TypePing, nil); err != nil {
				s.fail("keepalive", err)
				return
			}
		case <-save.C:
			if err := s.state.Save(); err != nil {
				s.eng.log.Printf("saving state for %s: %v", s.peerID, err)
			}
		}
	}
}

func (s *Session) onLocalChange() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Session) wakeLoop() {
	for {
		select {
		case <-s.done:
			return
		case <-s.wake:
			for _, share := range s.eng.cfg.ShareNames() {
				if !s.peerShares[share] {
					continue
				}
				s.sendIndexIfChanged(share)
				s.reconcile(share)
			}
		}
	}
}

func (s *Session) sendIndexIfChanged(share string) {
	fp, ok := s.eng.LocalFingerprint(share)
	if !ok {
		return
	}
	s.mu.Lock()
	unchanged := s.sentFP[share] == fp
	s.mu.Unlock()
	if unchanged {
		return
	}

	idx := s.eng.LocalIndex(share)
	if idx == nil {
		return
	}
	msg := proto.IndexMsg{Share: share, Entries: idx.Entries()}
	if err := s.c.WriteGzipJSON(proto.TypeIndex, msg); err != nil {
		s.fail("sending index", err)
		return
	}

	s.mu.Lock()
	s.sentFP[share] = fp
	s.mu.Unlock()
}

func (s *Session) handleIndex(payload []byte) {
	var msg proto.IndexMsg
	if err := proto.DecodeGzipJSON(payload, &msg); err != nil {
		s.fail("decoding index", err)
		return
	}
	if _, ok := s.eng.SharePath(msg.Share); !ok {
		return
	}

	s.mu.Lock()
	s.remote[msg.Share] = scan.FromEntries(msg.Entries)
	s.mu.Unlock()

	s.reconcile(msg.Share)
}

type reconcilePlan struct {
	mkdirs    []*scan.Entry
	wants     []*scan.Entry
	delLocal  []*scan.Entry
	delPeer   []*scan.Entry
	converged []*scan.Entry
}

func (s *Session) reconcile(share string) {
	root, ok := s.eng.SharePath(share)
	if !ok || !s.peerShares[share] {
		return
	}

	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()

	local := s.eng.LocalIndex(share)
	if local == nil {
		return
	}
	s.mu.Lock()
	remote := s.remote[share]
	s.mu.Unlock()
	if remote == nil {
		return
	}
	agreed := s.state.Snapshot(share)
	ctx := reconcileCtx{
		share:   share,
		root:    root,
		deletes: s.eng.cfg.PropagateDeletes,
		tombs:   s.pruneTombs(share, local, remote),
	}
	deletes := ctx.deletes

	var plan reconcilePlan
	for p, r := range remote {
		s.planRemoteEntry(ctx, &plan, p, local[p], r, agreed[p])
	}
	for p, l := range local {
		if _, onPeer := remote[p]; onPeer || !deletes {
			continue
		}

		if old := agreed[p]; old != nil && scan.Same(old, l) {
			plan.delLocal = append(plan.delLocal, l)
		}
	}

	s.applyReconcilePlan(share, root, plan)
}

type reconcileCtx struct {
	share   string
	root    string
	deletes bool
	tombs   map[string]int64
}

func (s *Session) pruneTombs(share string, local, remote scan.Index) map[string]int64 {
	tombs := s.state.Tombs(share)
	for p, at := range tombs {
		_, stillOnPeer := remote[p]
		_, backLocally := local[p]
		if !stillOnPeer || backLocally || time.Since(time.Unix(0, at)) > tombstoneTTL {
			s.state.RemoveTomb(share, p)
			delete(tombs, p)
		}
	}
	return tombs
}

func (s *Session) planRemoteEntry(ctx reconcileCtx, plan *reconcilePlan, p string, l, r, old *scan.Entry) {
	if l == nil {
		s.planMissingLocal(ctx, plan, p, r, old)
		return
	}

	if l.IsDir != r.IsDir {
		if r.ModNs > l.ModNs {
			s.removePath(ctx.share, ctx.root, l)
			plan.addCreate(r)
		}
		return
	}

	if scan.Same(l, r) {
		plan.converged = append(plan.converged, l)
		return
	}

	if r.ModNs > l.ModNs || (r.ModNs == l.ModNs && s.peerID > s.eng.cfg.NodeID) {
		plan.wants = append(plan.wants, r)
	}
}

func (s *Session) planMissingLocal(ctx reconcileCtx, plan *reconcilePlan, p string, r, old *scan.Entry) {
	if _, deletedHere := ctx.tombs[p]; deletedHere {
		plan.delPeer = append(plan.delPeer, r)
		return
	}
	if ctx.deletes && old != nil && scan.Same(old, r) {
		plan.delPeer = append(plan.delPeer, r)
		s.state.PutTomb(ctx.share, p, time.Now().UnixNano())
		return
	}
	plan.addCreate(r)
}

func (p *reconcilePlan) addCreate(e *scan.Entry) {
	if e.IsDir {
		p.mkdirs = append(p.mkdirs, e)
	} else {
		p.wants = append(p.wants, e)
	}
}

func (s *Session) applyReconcilePlan(share, root string, plan reconcilePlan) {
	sortEntries(plan.mkdirs, true)
	sortEntries(plan.delLocal, false)
	sortEntries(plan.delPeer, false)

	for _, e := range plan.mkdirs {
		s.applyMkdir(share, root, e)
	}
	for _, e := range plan.delLocal {
		if s.removePath(share, root, e) {
			s.state.Remove(share, e.Path)
			s.eng.log.Printf("[%s] deleted %s (removed on %s)", share, e.Path, s.peerName)
		}
	}
	for _, e := range plan.delPeer {
		msg := proto.Delete{Share: share, Path: e.Path, IsDir: e.IsDir, Hash: e.Hash}
		if err := s.c.WriteJSON(proto.TypeDelete, msg); err != nil {
			s.fail("sending delete", err)
			return
		}
		s.state.Remove(share, e.Path)
	}
	for _, e := range plan.wants {
		s.request(share, e)
	}
	for _, e := range plan.converged {
		s.state.Put(share, e)
	}
}

func (s *Session) request(share string, e *scan.Entry) {
	key := wantKey{share: share, path: e.Path}

	s.mu.Lock()
	prev, ok := s.inflight[key]
	if ok && prev.hash == e.Hash && time.Since(prev.at) < requestTTL {
		s.mu.Unlock()
		return
	}
	s.inflight[key] = wantState{hash: e.Hash, at: time.Now()}
	s.mu.Unlock()

	if err := s.c.WriteJSON(proto.TypeWant, proto.Want{Share: share, Path: e.Path}); err != nil {
		s.fail("sending request", err)
	}
}

func (s *Session) applyMkdir(share, root string, e *scan.Entry) {
	abs, err := SafeJoin(root, e.Path)
	if err != nil {
		s.eng.log.Printf(logRefusing, share, e.Path, err)
		return
	}
	if err := os.MkdirAll(abs, s.dirMode(e.Mode)); err != nil {
		s.eng.log.Printf("[%s] creating folder %s: %v", share, e.Path, err)
		return
	}

	created := &scan.Entry{Path: e.Path, IsDir: true, ModNs: e.ModNs, Mode: e.Mode}
	s.eng.SetLocalEntry(share, created)
	s.state.Put(share, created)
	s.eng.log.Printf("[%s] created folder %s (from %s)", share, e.Path, s.peerName)
}

func (s *Session) removePath(share, root string, e *scan.Entry) bool {
	abs, err := SafeJoin(root, e.Path)
	if err != nil {
		s.eng.log.Printf(logRefusing, share, e.Path, err)
		return false
	}

	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return false
	}

	s.eng.ForgetHash(abs)
	s.eng.DeleteLocalEntry(share, e.Path)
	return true
}

func (s *Session) handleWant(payload []byte) {
	var w proto.Want
	if err := proto.DecodeJSON(payload, &w); err != nil {
		s.fail("decoding request", err)
		return
	}
	select {
	case s.sendQ <- w:
	default:

		s.eng.log.Printf("send queue full, dropping request for %s/%s", w.Share, w.Path)
	}
}

func (s *Session) senderLoop() {
	for {
		select {
		case <-s.done:
			return
		case w := <-s.sendQ:
			if err := s.sendFile(w); err != nil {
				select {
				case <-s.done:
					return
				default:
				}
				s.eng.log.Printf("sending %s/%s: %v", w.Share, w.Path, err)
			}
		}
	}
}

func (s *Session) sendFile(w proto.Want) error {
	root, ok := s.eng.SharePath(w.Share)
	if !ok {
		return nil
	}
	abs, err := SafeJoin(root, w.Path)
	if err != nil {
		return err
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil
	}
	if !info.Mode().IsRegular() {
		return nil
	}

	f, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer f.Close()

	start := proto.FileStart{
		Share: w.Share,
		Path:  w.Path,
		Size:  info.Size(),
		ModNs: info.ModTime().UnixNano(),
		Mode:  uint32(info.Mode().Perm()),
	}
	if err := s.c.WriteJSON(proto.TypeFileStart, start); err != nil {
		return err
	}

	digest := sha256.New()
	buf := make([]byte, proto.ChunkSize)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			digest.Write(buf[:n])
			if err := s.c.WriteFrame(proto.TypeFileData, buf[:n]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return s.c.WriteJSON(proto.TypeFileEnd, proto.FileEnd{
				Share: w.Share, Path: w.Path, OK: false,
			})
		}
	}

	end := proto.FileEnd{
		Share: w.Share,
		Path:  w.Path,
		Hash:  hex.EncodeToString(digest.Sum(nil)),
		OK:    true,
	}
	return s.c.WriteJSON(proto.TypeFileEnd, end)
}

func (s *Session) handleFileStart(payload []byte) {
	var start proto.FileStart
	if err := proto.DecodeJSON(payload, &start); err != nil {
		s.fail("decoding file header", err)
		return
	}
	s.abortIncoming()

	root, ok := s.eng.SharePath(start.Share)
	if !ok {
		return
	}
	dest, err := SafeJoin(root, start.Path)
	if err != nil {
		s.eng.log.Printf(logRefusing, start.Share, start.Path, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		s.eng.log.Printf("[%s] preparing %s: %v", start.Share, start.Path, err)
		return
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".synk-tmp-*")
	if err != nil {
		s.eng.log.Printf("[%s] preparing %s: %v", start.Share, start.Path, err)
		return
	}

	s.mu.Lock()
	s.in = &incoming{
		share:   start.Share,
		rel:     start.Path,
		dest:    dest,
		tmpPath: tmp.Name(),
		file:    tmp,
		digest:  sha256.New(),
		modNs:   start.ModNs,
		mode:    start.Mode,
	}
	s.mu.Unlock()
}

func (s *Session) handleFileData(payload []byte) {
	s.mu.Lock()
	in := s.in
	s.mu.Unlock()
	if in == nil || in.failed {
		return
	}

	if _, err := in.file.Write(payload); err != nil {
		s.eng.log.Printf("[%s] writing %s: %v", in.share, in.rel, err)
		in.failed = true
		return
	}
	in.digest.Write(payload)
	in.written += int64(len(payload))
}

func (s *Session) handleFileEnd(payload []byte) {
	var end proto.FileEnd
	if err := proto.DecodeJSON(payload, &end); err != nil {
		s.fail("decoding file trailer", err)
		return
	}

	s.mu.Lock()
	in := s.in
	s.in = nil
	s.mu.Unlock()
	if in == nil {
		return
	}

	cleanup := func() {
		in.file.Close()
		os.Remove(in.tmpPath)
	}
	if in.failed || !end.OK || in.share != end.Share || in.rel != end.Path {
		cleanup()
		return
	}

	got := hex.EncodeToString(in.digest.Sum(nil))
	if end.Hash != "" && got != end.Hash {
		s.eng.log.Printf("[%s] %s arrived corrupted or changed mid-transfer, retrying later", in.share, in.rel)
		cleanup()
		return
	}
	if err := in.file.Close(); err != nil {
		os.Remove(in.tmpPath)
		s.eng.log.Printf("[%s] writing %s: %v", in.share, in.rel, err)
		return
	}

	mod := time.Unix(0, in.modNs)
	if err := os.Chtimes(in.tmpPath, mod, mod); err != nil {
		s.eng.log.Printf("[%s] setting time on %s: %v", in.share, in.rel, err)
	}
	if err := os.Chmod(in.tmpPath, s.fileMode(in.mode)); err != nil {
		s.eng.log.Printf("[%s] setting permissions on %s: %v", in.share, in.rel, err)
	}

	if info, err := os.Stat(in.dest); err == nil && info.IsDir() {
		if err := os.RemoveAll(in.dest); err != nil {
			os.Remove(in.tmpPath)
			s.eng.log.Printf("[%s] replacing folder %s: %v", in.share, in.rel, err)
			return
		}
	}
	if err := os.Rename(in.tmpPath, in.dest); err != nil {
		os.Remove(in.tmpPath)
		s.eng.log.Printf("[%s] saving %s: %v", in.share, in.rel, err)
		return
	}

	entry := &scan.Entry{
		Path:  in.rel,
		Size:  in.written,
		ModNs: in.modNs,
		Mode:  uint32(s.fileMode(in.mode).Perm()),
		Hash:  got,
	}
	if info, err := os.Stat(in.dest); err == nil {
		entry.ModNs = info.ModTime().UnixNano()
		entry.Size = info.Size()
	}

	s.eng.ForgetHash(in.dest)
	s.eng.SetLocalEntry(in.share, entry)
	s.state.Put(in.share, entry)

	s.mu.Lock()
	delete(s.inflight, wantKey{share: in.share, path: in.rel})
	s.mu.Unlock()

	s.eng.log.Printf("[%s] received %s (%s) from %s", in.share, in.rel, humanBytes(in.written), s.peerName)
}

func (s *Session) abortIncoming() {
	s.mu.Lock()
	in := s.in
	s.in = nil
	s.mu.Unlock()
	if in == nil {
		return
	}
	in.file.Close()
	os.Remove(in.tmpPath)
}

func (s *Session) handleMkdir(payload []byte) {
	var m proto.Mkdir
	if err := proto.DecodeJSON(payload, &m); err != nil {
		s.fail("decoding mkdir", err)
		return
	}
	root, ok := s.eng.SharePath(m.Share)
	if !ok {
		return
	}
	s.applyMkdir(m.Share, root, &scan.Entry{Path: m.Path, IsDir: true, ModNs: m.ModNs, Mode: m.Mode})
}

func (s *Session) handleDelete(payload []byte) {
	var d proto.Delete
	if err := proto.DecodeJSON(payload, &d); err != nil {
		s.fail("decoding delete", err)
		return
	}
	if !s.eng.cfg.PropagateDeletes {
		return
	}
	root, ok := s.eng.SharePath(d.Share)
	if !ok {
		return
	}

	local := s.eng.LocalEntry(d.Share, d.Path)
	if local == nil {
		s.state.Remove(d.Share, d.Path)
		return
	}

	if !local.IsDir {
		sameVersion := d.Hash != "" && d.Hash == local.Hash
		if !sameVersion {
			agreed := s.state.Lookup(d.Share, d.Path)
			if agreed == nil || !scan.Same(agreed, local) {
				return
			}
		}
	}

	if s.removePath(d.Share, root, local) {
		s.state.Remove(d.Share, d.Path)
		s.eng.log.Printf("[%s] deleted %s (%s asked for it)", d.Share, d.Path, s.peerName)
	}
}

func (s *Session) modeOrDefault(mode uint32, def os.FileMode) os.FileMode {
	if s.peerOS == osWindows || mode == 0 {
		return def
	}
	return os.FileMode(mode) & 0o777
}

func (s *Session) fileMode(mode uint32) os.FileMode { return s.modeOrDefault(mode, 0o644) }
func (s *Session) dirMode(mode uint32) os.FileMode  { return s.modeOrDefault(mode, 0o755) }

func sortEntries(entries []*scan.Entry, ascending bool) {
	sort.Slice(entries, func(i, j int) bool {
		if ascending {
			return entries[i].Path < entries[j].Path
		}
		return entries[i].Path > entries[j].Path
	})
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
