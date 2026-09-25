package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/service"
	"github.com/ViitoJooj/synk/internal/transport"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

// runConnect is `synk <peer>`: resolve the machine, remember it, and restart
// the agent so the link moves there.
//
// The direction does not change. Linux still listens and Windows still dials;
// what changes is which machine each side will talk to.
func runConnect(cmd *cobra.Command, name string) error {
	out := cmd.OutOrStdout()

	peer, err := transport.NewTailscale().Target(name)
	if err != nil {
		return targetError(cmd, name, err)
	}
	if !peer.CanRunTheLink() {
		return errorx.NotAComputer(peer.Host, peer.OS)
	}

	if err := saveChoice(peer.Host); err != nil {
		return err
	}
	if err := service.New().Restart(); err != nil {
		// The choice is saved either way, and the agent will use it once it
		// is installed; saying so saves running this again.
		if errors.Is(err, service.ErrNotInstalled) {
			say(out, "saved %s as the peer; the agent will link with it once it is installed", peer.Host)
		}
		return serviceError("restart", err)
	}

	say(out, "synk now links with %s (%s, %s)", peer.Host, peer.OS, peer.IP)
	if !peer.Online {
		say(out, "%s is offline; the link comes up when it wakes", peer.Host)
	}
	return nil
}

// targetError explains a name that picked no machine. A name that fits no
// machine but looks like a mistyped command gets that suggestion first: with
// `synk <peer>` at the root, `synk stauts` arrives here.
func targetError(cmd *cobra.Command, name string, err error) error {
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

	e := errorx.PeerNotFound(name, te.Known)
	if s := cmd.Root().SuggestionsFor(name); len(s) > 0 {
		e.Hint = "did you mean 'synk " + strings.Join(s, "' or 'synk ") + "'? " + e.Hint
	}
	return e
}
