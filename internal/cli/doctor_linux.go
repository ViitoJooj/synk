//go:build linux

package cli

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/inputdev"
	"github.com/ViitoJooj/synk/internal/pointer"
	"github.com/ViitoJooj/synk/internal/transport"
)

// loadConfig reads the settings file. This is the machine that receives input,
// so it is the one that has to be told which border the peer sits beyond; the
// sending side learns that over the wire.
func loadConfig() (*config.Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	return config.Load(path)
}

// checkDaemon is Linux-only: the privileged injector exists only on the machine
// that receives input. On Windows, which owns the keyboard here, there is
// nothing to inject with and no socket to look for.
func checkDaemon(cfg *config.Config) []check {
	socket := config.DefaultSocket
	if cfg != nil {
		socket = cfg.Socket
	}

	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err == nil {
		closeQuietly(conn)
		return []check{{name: "injector", ok: true, detail: "listening on " + socket}}
	}

	// Why the dial failed changes the advice completely, and getting this
	// wrong is worse than saying nothing: a running daemon whose socket we
	// merely cannot open would otherwise be reported as stopped, sending the
	// user off to start a service that is already up.
	switch {
	case errors.Is(err, os.ErrPermission):
		return []check{{
			name:   "injector",
			detail: "running, but this session has no permission to open " + socket,
			fix: "you are in the synk group but this login session predates it; " +
				"log out and back in, or run: exec su - $USER",
		}}

	case errors.Is(err, os.ErrNotExist):
		return []check{{
			name:   "injector",
			detail: "not running — " + socket + " does not exist",
			fix:    "synk up",
		}}

	default:
		// A socket that exists but refuses connections means the daemon died
		// without cleaning up.
		return []check{{
			name:   "injector",
			detail: err.Error(),
			fix:    "see why with: synk logs --injector",
		}}
	}
}

// checkLink reports whether the session is actually listening.
//
// This is the check that answers "why is nothing happening?" on this side: a
// daemon that is up and a config that is right still add up to nothing if the
// session unit died, and systemd's own status is one more place to look rather
// than an answer.
func checkLink(cfg *config.Config, self string, _ *transport.Peer) []check {
	if cfg == nil || self == "" {
		return nil
	}

	addr := net.JoinHostPort(self, fmt.Sprint(cfg.Port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return []check{{
			name:   "listening",
			detail: "nothing is accepting connections on " + addr,
			fix:    "synk up",
		}}
	}
	closeQuietly(conn)
	return []check{{
		name:   "listening",
		ok:     true,
		detail: addr + " is accepting connections",
	}}
}

// checkPointer reports the acceleration profile of the virtual pointer.
//
// The link places the cursor by injecting a relative delta, which only lands
// where intended when one injected unit is one pixel. libinput's default
// adaptive curve makes it nearly two: measured here, an intended 500 pixels
// travelled 934. The visible symptom is a cursor that arrives at the wrong
// place after crossing — which is exactly what it looks like when nobody knows
// this setting exists, so it gets its own line in the report.
func checkPointer() []check {
	flat, err := pointer.New().Flat(inputdev.VirtualNamePrefix)
	if err != nil {
		if errors.Is(err, pointer.ErrDeviceNotFound) {
			// Nothing to configure until the daemon has created the device.
			return nil
		}
		return []check{{
			name:   "pointer",
			ok:     true,
			detail: "could not read the acceleration profile: " + err.Error(),
		}}
	}
	if !flat {
		return []check{{
			name:   "pointer",
			detail: "the virtual pointer is on the adaptive acceleration profile",
			fix: "the cursor will land in the wrong place after crossing. The agent sets a flat profile " +
				"each time the peer connects, so this clears on the next connection; if it stays, " +
				"'synk logs' says why the agent could not set it",
		}}
	}
	return []check{{name: "pointer", ok: true, detail: "flat profile; injected motion is 1:1"}}
}

// checkSync reports whether folder sync is listening, on the port after the
// link's.
func checkSync(cfg *config.Config, self string, _ *transport.Peer) []check {
	if cfg == nil || self == "" {
		return nil
	}
	addr := net.JoinHostPort(self, fmt.Sprint(cfg.Port+1))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return []check{{
			name:   "folder sync",
			detail: "nothing is accepting connections on " + addr,
			fix:    "synk restart; if it stays down, 'synk logs' says why",
		}}
	}
	closeQuietly(conn)
	return []check{{name: "folder sync", ok: true, detail: addr + " is accepting connections"}}
}
