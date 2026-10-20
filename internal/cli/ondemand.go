package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/control"
	"github.com/ViitoJooj/synk/internal/service"
)

// startsNothing lists the top-level commands that must not bring the agent up:
// the ones that start or stop it themselves, the ones the service manager runs,
// and the ones that read no agent state ('ls', 'logs', 'doctor' — which has to
// report an agent that is down, not hide it).
var startsNothing = map[string]bool{
	"up": true, "down": true, "restart": true,
	"agent": true, "session": true, "injector": true,
	"ls": true, "logs": true, "doctor": true,
	"help": true, "completion": true, "version": true,
}

// startOnDemand is what makes synk run only when it is used. The units are not
// enabled at boot; any command that talks to the agent starts it first. The
// window does the same through its own start_agent.
//
// A failed start is a warning, not an error: the command then says by itself
// that no agent answers, and that is the message that points at 'synk doctor'.
func startOnDemand(cmd *cobra.Command, _ []string) error {
	top := cmd
	for top.HasParent() && top.Parent() != rootCmd {
		top = top.Parent()
	}
	// The root itself is 'synk <peer>', which starts the agent as part of
	// switching, or the help.
	if top == rootCmd || startsNothing[top.Name()] {
		return nil
	}

	if err := service.New().EnsureRunning(); err != nil {
		if !quiet {
			fmt.Fprintf(os.Stderr, "synk: could not start the agent: %v\n", err)
		}
		return nil
	}
	waitForAgent()
	return nil
}

// waitForAgent gives a freshly started agent a moment to open its control
// socket, so the command that follows does not find it "not running".
func waitForAgent() {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := control.Call(ctx, control.Address(), "status", nil, nil)
		cancel()
		if !errors.Is(err, control.ErrNotRunning) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
