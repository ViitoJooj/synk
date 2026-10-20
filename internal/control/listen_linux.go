//go:build linux

package control

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

// Address is the agent's socket: in the user's runtime directory, which only
// the user can enter and which systemd empties at logout.
func Address() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
	}
	return filepath.Join(dir, "synk", "control.sock")
}

// errRefused is returned by Accept for a connection from another user; Serve
// drops it and goes on.
var errRefused = errors.New("control: connection from another user refused")

// Listen opens the socket. The directory is 0700 and the socket 0600, and each
// connection's user is checked as well: the modes alone would be undone by a
// careless chmod, the check would not.
//
// A socket left behind by an agent that died is removed. One that still
// answers means another agent is running, and that is an error rather than a
// takeover.
func Listen(addr string) (net.Listener, error) {
	dir := filepath.Dir(addr)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("control: creating %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("control: securing %s: %w", dir, err)
	}

	if _, err := os.Stat(addr); err == nil {
		if conn, err := net.Dial("unix", addr); err == nil {
			conn.Close()
			return nil, fmt.Errorf("control: another agent is already answering on %s", addr)
		}
		if err := os.Remove(addr); err != nil {
			return nil, fmt.Errorf("control: removing the stale %s: %w", addr, err)
		}
	}

	l, err := net.Listen("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("control: listening on %s: %w", addr, err)
	}
	if err := os.Chmod(addr, 0o600); err != nil {
		l.Close()
		return nil, fmt.Errorf("control: securing %s: %w", addr, err)
	}
	return &sameUser{Listener: l, uid: os.Getuid()}, nil
}

type sameUser struct {
	net.Listener
	uid int
}

func (l *sameUser) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	uid, err := peerUID(conn)
	if err != nil || uid != l.uid {
		conn.Close()
		return nil, errRefused
	}
	return conn, nil
}

func peerUID(conn net.Conn) (int, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return -1, errors.New("not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if credErr != nil {
		return -1, credErr
	}
	return int(cred.Uid), nil
}

func dialAddr(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", addr)
}
