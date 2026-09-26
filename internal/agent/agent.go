// Package agent is the long-running, unprivileged half of synk: it owns the
// connection to the peer and moves keyboard, mouse, clipboard and audio across
// it.
//
// The two platforms play opposite roles. Linux listens, applies the peer's
// input through the injector and sends its sound; Windows owns the keyboard and
// mouse, dials in and plays what arrives. Run is the same call on both.
package agent

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/control"
	"github.com/ViitoJooj/synk/internal/sync/engine"
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

	// Reload reads the settings again, for a change made while the agent
	// runs: `synk <peer>`, `synk config set`, the UI. Without it, a reload
	// restarts the link with the options it already had.
	Reload func() (Options, error)
}

// Run is the agent: the control API and the link, with one life cycle.
//
// The link runs until ctx is cancelled. A reload cancels only the link,
// reads the settings again and starts it over, all in this process: the
// control socket stays up, so the UI never sees the agent disappear, and
// Windows does not wait out the scheduled task's one-minute restart.
//
// Failing to open the control socket is reported and survived. The link is
// what the agent is for; the API is how it is watched.
func Run(ctx context.Context, opts Options) error {
	// Everything the agent says also goes to a ring the "logs" verb reads.
	ring := newLogRing(500)
	opts.Out = io.MultiWriter(opts.Out, ring)
	a := &runner{opts: opts, st: newState(role), ts: transport.NewTailscale(), logs: ring}

	if l, err := control.Listen(control.Address()); err != nil {
		fmt.Fprintf(opts.Out, "control API off: %v\n", err)
	} else {
		// Its own context: when the link fails and Run returns, the server
		// has to stop too, or Run would wait on it forever and the service
		// manager would see a hung agent instead of one to restart.
		srvCtx, stopSrv := context.WithCancel(ctx)
		srv := a.server()
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := srv.Serve(srvCtx, l); err != nil {
				fmt.Fprintf(opts.Out, "control API stopped: %v\n", err)
			}
		}()
		defer func() {
			stopSrv()
			<-done
		}()
	}

	// Folder sync scans and keeps its state across link restarts; only its
	// connection to the peer follows the link. Failing to start it, like the
	// API, is reported and survived.
	eng, err := newEngine(opts.Out, func() {
		if a.eng != nil {
			a.st.setSync(a.eng.Status())
		}
	})
	if err != nil {
		fmt.Fprintf(opts.Out, "folder sync off: %v\n", err)
	}
	if eng != nil {
		a.eng = eng
		engCtx, stopEng := context.WithCancel(ctx)
		engDone := make(chan struct{})
		go func() {
			defer close(engDone)
			eng.Run(engCtx)
		}()
		go pruneTrash(engCtx, opts.Out)
		defer func() {
			stopEng()
			<-engDone
		}()
	}

	for {
		linkCtx, cancel := context.WithCancel(ctx)
		a.mu.Lock()
		a.cancelLink, a.reloading = cancel, false
		current := a.opts
		a.mu.Unlock()

		syncDone := make(chan struct{})
		go func() {
			defer close(syncDone)
			if eng != nil {
				startSync(linkCtx, current, eng)
			}
		}()
		err := startLink(linkCtx, current, a.st)
		cancel()
		<-syncDone
		if ctx.Err() != nil {
			return nil
		}

		a.mu.Lock()
		again := a.reloading
		a.mu.Unlock()
		if !again {
			return err
		}
		fmt.Fprintln(opts.Out, "settings changed; restarting the link")
	}
}

// startLink is runLink, replaced in tests by a link that needs no peer.
var startLink = runLink

// runner is the state Run shares with the control API's handlers.
type runner struct {
	st  *state
	ts  *transport.Tailscale
	eng  *engine.Engine // nil when folder sync could not start
	logs *logRing

	mu         sync.Mutex
	opts       Options
	cancelLink context.CancelFunc
	reloading  bool
}

// reload reads the settings and restarts the link with them. Settings that do
// not load leave the running link alone: better the old settings working than
// the new ones not.
func (a *runner) reload() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.opts.Reload != nil {
		next, err := a.opts.Reload()
		if err != nil {
			return err
		}
		next.Reload = a.opts.Reload
		next.Out = a.opts.Out // keep writing to the ring
		a.opts = next
	}
	a.reloading = true
	if a.cancelLink != nil {
		a.cancelLink()
	}
	return nil
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
