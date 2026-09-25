package cli

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/agent"
	"github.com/ViitoJooj/synk/internal/control"
	"github.com/ViitoJooj/synk/internal/service"
	"github.com/ViitoJooj/synk/internal/transport"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

// callTimeout bounds a request to the agent. Anything it does on request is
// quick; a connect waits on `tailscale status`, which is the slow part.
const callTimeout = 10 * time.Second

// call sends a request to the running agent.
func call(verb string, args, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return control.Call(ctx, control.Address(), verb, args, out)
}

// runConnect is `synk <peer>`: resolve the machine, remember it, and move the
// link there.
//
// With the agent running, the agent does it and restarts only its link, in
// place. Without one, this process saves the choice and asks the service
// manager to start the agent.
//
// The direction does not change. Linux still listens and Windows still dials;
// what changes is which machine each side will talk to.
func runConnect(cmd *cobra.Command, name string) error {
	out := cmd.OutOrStdout()

	var info agent.PeerInfo
	err := call("connect", map[string]string{"peer": name}, &info)
	if errors.Is(err, control.ErrNotRunning) {
		return connectWithoutAgent(cmd, name)
	}
	if err != nil {
		return withSuggestion(cmd, name, err)
	}

	say(out, "synk now links with %s (%s, %s)", info.Name, info.OS, info.IP)
	if !info.Online {
		say(out, "%s is offline; the link comes up when it wakes", info.Name)
	}
	return nil
}

func connectWithoutAgent(cmd *cobra.Command, name string) error {
	out := cmd.OutOrStdout()

	peer, err := agent.Choose(transport.NewTailscale(), name)
	if err != nil {
		return withSuggestion(cmd, name, err)
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

// withSuggestion adds "did you mean" to a name that fits no machine but looks
// like a mistyped command: with `synk <peer>` at the root, `synk stauts`
// arrives here.
func withSuggestion(cmd *cobra.Command, name string, err error) error {
	var e *errorx.Error
	if !errors.As(err, &e) || e.Code != "snk-00" {
		return err
	}
	if s := cmd.Root().SuggestionsFor(name); len(s) > 0 {
		e.Hint = "did you mean 'synk " + strings.Join(s, "' or 'synk ") + "'? " + e.Hint
	}
	return e
}
