//go:build windows

package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/ViitoJooj/synk/internal/agent"
	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/transport"
)

// loadConfig deliberately reads nothing on this machine.
//
// Everything this side needs — which machine to reach, and which border of its
// screen leads there — arrives from the peer or from the tailnet. Reading a
// file here would only add a way to be wrong, and it already was: a stale
// conn.yaml naming the old port would silently send the link to a port nobody
// is listening on, and the symptom would be a connection that never happens
// with nothing anywhere saying why.
//
// --peer and --port remain as escapes for the cases discovery cannot decide.
func loadConfig() (*config.Config, error) { return nil, nil }

// checkDaemon reports nothing on Windows. The privileged injector runs only on
// the machine being typed into; this one owns the keyboard and sends.
func checkDaemon(*config.Config) []check { return nil }

// checkLink dials the peer and says what came back.
//
// It is the most useful line in the report on this machine: it distinguishes
// "the other side is not running" (refused, immediately) from "something is
// dropping the packets" (timeout) — and a timeout here is the signature of a
// firewall, which is exactly the failure this direction of connection was
// chosen to avoid.
func checkLink(cfg *config.Config, _ string, peer *transport.Peer) []check {
	if peer == nil {
		return nil
	}

	port := agent.Port(linkPort, cfg)

	conn, err := transport.Dial(peer.IP, port)
	if err != nil {
		return []check{{
			name:   "peer link",
			detail: fmt.Sprintf("cannot reach %s:%d: %v", peer.IP, port, err),
			fix:    dialAdvice(err),
		}}
	}
	closeQuietly(conn)
	return []check{{
		name:   "peer link",
		ok:     true,
		detail: fmt.Sprintf("%s:%d answered", peer.IP, port),
	}}
}

// dialAdvice turns the two failures that actually happen into the two different
// things to go and do.
func dialAdvice(err error) string {
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "the peer is reachable but did not answer in time; " +
			"check that `synk status` shows the agent running there, and that nothing is filtering the port"
	}
	return "run `synk up` on the other machine"
}

// checkPointer has no counterpart here: this machine's pointer is its own, and
// nothing injects motion into it.
func checkPointer() []check { return nil }

// checkSync dials the peer's folder-sync port, the one after the link's, and
// checks git's line endings here.
func checkSync(cfg *config.Config, _ string, peer *transport.Peer) []check {
	out := checkLineEndings()
	if peer == nil {
		return out
	}
	port := agent.Port(linkPort, cfg) + 1
	conn, err := transport.Dial(peer.IP, port)
	if err != nil {
		return append(out, check{
			name:   "folder sync",
			detail: fmt.Sprintf("cannot reach %s:%d: %v", peer.IP, port, err),
			fix:    "check 'synk doctor' on the other machine",
		})
	}
	closeQuietly(conn)
	return append(out, check{name: "folder sync", ok: true, detail: fmt.Sprintf("%s:%d answered", peer.IP, port)})
}

// checkLineEndings warns about core.autocrlf. With it on, git on Windows
// rewrites text files with CRLF on every checkout; synced to Linux, each
// checkout here becomes an "edit" of hundreds of files, and back.
func checkLineEndings() []check {
	out, err := exec.Command("git", "config", "--global", "core.autocrlf").Output()
	if err != nil {
		return nil // no git, or the setting is unset: nothing rewrites
	}
	if v := strings.TrimSpace(string(out)); v == "true" {
		return []check{{
			name:   "line endings",
			detail: "git has core.autocrlf=true, which rewrites synced files on every checkout",
			fix:    "git config --global core.autocrlf false  (or set line endings per repository in .gitattributes)",
		}}
	}
	return []check{{name: "line endings", ok: true, detail: "git leaves line endings alone"}}
}
