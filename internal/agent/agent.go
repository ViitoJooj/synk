// Package agent is the long-running, unprivileged half of synk: it owns the
// connection to the peer and moves keyboard, mouse, clipboard and audio across
// it.
//
// The two platforms play opposite roles. Linux listens, applies the peer's
// input through the injector and sends its sound; Windows owns the keyboard and
// mouse, dials in and plays what arrives. Run is the same call on both.
package agent

import (
	"io"

	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/transport"
)

// Options is what the command line hands the agent.
type Options struct {
	// Config is the settings file, or nil on the machine that reads none.
	Config *config.Config

	// Peer and Port override discovery and the default port. They exist for
	// the machine that carries no configuration file, and as an escape hatch
	// when a tailnet has more than one candidate on it.
	Peer string
	Port int

	// Verbose reports captured-event counters where there are any.
	Verbose bool

	Out io.Writer
}

// ResolvePeer names the other machine: the flag first, then the config, and
// otherwise by finding the single other computer online.
func ResolvePeer(ts *transport.Tailscale, flag string, cfg *config.Config) (transport.Peer, error) {
	switch {
	case flag != "":
		return ts.Peer(flag)
	case cfg != nil && cfg.Peer != "":
		return ts.Peer(cfg.Peer)
	default:
		return ts.SolePeer()
	}
}

// Port picks the port: the flag wins, then the config, then the default.
func Port(flag int, cfg *config.Config) int {
	switch {
	case flag != 0:
		return flag
	case cfg != nil && cfg.Port != 0:
		return cfg.Port
	default:
		return config.DefaultPort
	}
}

// ClipboardEnabled defaults to on when there is no configuration to consult.
// The machine without a config file is the one the user never has to set up,
// and a clipboard that silently does nothing there would be a puzzle; the
// receiving side's config still decides whether anything is sent to it.
func ClipboardEnabled(cfg *config.Config) bool {
	if cfg == nil {
		return true
	}
	return cfg.Clipboard
}
