package agent

import (
	"errors"

	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/transport"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

// Choose resolves what the user typed to a machine and remembers it as the
// peer: `synk <peer>` from the command line, and "connect" over the control
// API. Errors are *errorx.Error, ready to show.
//
// It does not restart anything; the caller decides how the agent picks the
// choice up.
func Choose(ts *transport.Tailscale, name string) (transport.Peer, error) {
	peer, err := ts.Target(name)
	if err != nil {
		return transport.Peer{}, TargetError(err)
	}
	if !peer.CanRunTheLink() {
		return transport.Peer{}, errorx.NotAComputer(peer.Host, peer.OS)
	}
	if err := config.SaveChoice(peer.Host); err != nil {
		if config.KeepsConfig {
			path, _ := config.Path()
			return transport.Peer{}, errorx.NoConfig(path, err)
		}
		return transport.Peer{}, errorx.Failed("save the chosen peer", err)
	}
	return peer, nil
}

// TargetError explains a name that picked no single machine.
func TargetError(err error) *errorx.Error {
	var te *transport.TargetError
	if !errors.As(err, &te) {
		return errorx.Tailscale(err)
	}
	switch {
	case errors.Is(err, transport.ErrAmbiguous):
		return errorx.PeerAmbiguous(te.Name, te.Matches)
	case te.ByOS:
		return errorx.NoneOnline(te.Name)
	}
	return errorx.PeerNotFound(te.Name, te.Known)
}
