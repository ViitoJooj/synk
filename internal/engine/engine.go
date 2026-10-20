package engine

import (
	"context"
	"fmt"
	"log"
	"net"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"synk/internal/config"
	"synk/internal/proto"
	"synk/internal/scan"
)

const logSaveStateFailed = "saving state for %s: %v"

type localShare struct {
	idx scan.Index
	fp  string
}

type Engine struct {
	cfg     *config.Config
	log     *log.Logger
	scanner *scan.Scanner

	mu       sync.RWMutex
	local    map[string]*localShare
	sessions map[string]*Session
}

func New(cfg *config.Config, logger *log.Logger) *Engine {
	return &Engine{
		cfg:      cfg,
		log:      logger,
		scanner:  scan.NewScanner(cfg.Excludes, cfg.MaxFileSize),
		local:    make(map[string]*localShare),
		sessions: make(map[string]*Session),
	}
}

func (e *Engine) Config() *config.Config { return e.cfg }
func (e *Engine) Log() *log.Logger       { return e.log }

func (e *Engine) Run(ctx context.Context) {
	e.scanAll()

	ticker := time.NewTicker(e.cfg.Interval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.scanAll()
		}
	}
}

func (e *Engine) scanAll() {
	for _, sh := range e.cfg.Shares {
		idx, err := e.scanner.Scan(sh.Path)
		if err != nil {
			e.log.Printf("scan %s (%s): %v", sh.Name, sh.Path, err)
			continue
		}
		e.mu.Lock()
		e.local[sh.Name] = &localShare{idx: idx, fp: scan.Fingerprint(idx)}
		e.mu.Unlock()
	}

	for _, s := range e.Sessions() {
		s.onLocalChange()
	}
}

func (e *Engine) LocalIndex(share string) scan.Index {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ls, ok := e.local[share]
	if !ok {
		return nil
	}
	return ls.idx.Clone()
}

func (e *Engine) LocalFingerprint(share string) (string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ls, ok := e.local[share]
	if !ok {
		return "", false
	}
	return ls.fp, true
}

func (e *Engine) LocalEntry(share, rel string) *scan.Entry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ls, ok := e.local[share]
	if !ok {
		return nil
	}
	entry, ok := ls.idx[rel]
	if !ok {
		return nil
	}
	copied := *entry
	return &copied
}

func (e *Engine) SetLocalEntry(share string, entry *scan.Entry) {
	if entry == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	ls, ok := e.local[share]
	if !ok {
		return
	}
	copied := *entry
	ls.idx[entry.Path] = &copied
	ls.fp = scan.Fingerprint(ls.idx)
}

func (e *Engine) DeleteLocalEntry(share, rel string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ls, ok := e.local[share]
	if !ok {
		return
	}
	delete(ls.idx, rel)
	ls.fp = scan.Fingerprint(ls.idx)
}

func (e *Engine) ForgetHash(absPath string) { e.scanner.Forget(absPath) }

func (e *Engine) SharePath(name string) (string, bool) { return e.cfg.SharePath(name) }

func (e *Engine) hello() proto.Hello {
	return proto.Hello{
		Version:  proto.Version,
		NodeID:   e.cfg.NodeID,
		NodeName: e.cfg.NodeName,
		OS:       runtime.GOOS,
		Shares:   e.cfg.ShareNames(),
	}
}

func (e *Engine) HandleConn(conn net.Conn, outbound bool) {
	c := proto.NewConn(conn)

	var (
		peer *proto.Hello
		err  error
	)
	if outbound {
		peer, err = proto.ClientHandshake(c, e.cfg.Token, e.hello())
	} else {
		peer, err = proto.ServerHandshake(c, e.cfg.Token, e.hello())
	}
	if err != nil {
		e.log.Printf("handshake with %s failed: %v", conn.RemoteAddr(), err)
		c.Close()
		return
	}

	if peer.NodeID == e.cfg.NodeID {
		e.log.Printf("refusing connection from %s: same node id as ours (%s) — "+
			"each machine needs its own \"node_id\"", conn.RemoteAddr(), peer.NodeID)
		c.Close()
		return
	}

	session, err := newSession(e, c, peer, outbound)
	if err != nil {
		e.log.Printf("session with %s failed: %v", peer.NodeName, err)
		c.Close()
		return
	}

	if !e.register(session) {
		c.Close()
		return
	}

	e.log.Printf("connected to %s (%s, %s) via %s", peer.NodeName, peer.NodeID, peer.OS, conn.RemoteAddr())
	session.serve()

	e.unregister(session)
	e.log.Printf("disconnected from %s (%s)", peer.NodeName, peer.NodeID)
}

func (e *Engine) register(s *Session) bool {
	e.mu.Lock()
	existing, ok := e.sessions[s.peerID]
	if ok {
		keepOutbound := e.cfg.NodeID > s.peerID
		if s.outbound != keepOutbound {
			e.mu.Unlock()
			return false
		}
		e.mu.Unlock()
		existing.close()
		e.mu.Lock()
	}
	e.sessions[s.peerID] = s
	e.mu.Unlock()

	s.onLocalChange()
	return true
}

func (e *Engine) unregister(s *Session) {
	e.mu.Lock()
	if current, ok := e.sessions[s.peerID]; ok && current == s {
		delete(e.sessions, s.peerID)
	}
	e.mu.Unlock()

	if err := s.state.Save(); err != nil {
		e.log.Printf(logSaveStateFailed, s.peerID, err)
	}
}

func (e *Engine) Shutdown() {
	for _, s := range e.Sessions() {
		s.Stop()
	}
}

func (e *Engine) Sessions() []*Session {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*Session, 0, len(e.sessions))
	for _, s := range e.sessions {
		out = append(out, s)
	}
	return out
}

func (e *Engine) ConnectedTo(host string) bool {
	if host == "" {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, s := range e.sessions {
		if strings.EqualFold(s.remoteHost, host) {
			return true
		}
	}
	return false
}

const errIllegalPath = "illegal path %q"

func SafeJoin(root, rel string) (string, error) {
	if rel == "" || rel == "." {
		return "", fmt.Errorf("empty relative path")
	}
	if strings.ContainsRune(rel, '\\') || strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf(errIllegalPath, rel)
	}
	if strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("path %q is not relative", rel)
	}
	for _, segment := range strings.Split(rel, "/") {
		if segment == ".." {
			return "", fmt.Errorf("path %q walks outside the share", rel)
		}
	}
	clean := path.Clean("/" + rel)
	if clean == "/" {
		return "", fmt.Errorf(errIllegalPath, rel)
	}

	root = filepath.Clean(root)
	abs := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes share root", rel)
	}
	return abs, nil
}
