package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/service"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

// serviceError explains a failed start, stop or restart.
func serviceError(action string, err error) error {
	if errors.Is(err, service.ErrNotInstalled) {
		return errorx.NotInstalled(installHint, err)
	}
	return errorx.Service(action, err)
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start the agent (and, on Linux, the injector if it is down)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := service.New().Start(); err != nil {
			return serviceError("start", err)
		}
		say(cmd.OutOrStdout(), "agent started")
		return nil
	},
}

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop the agent; the link goes down until 'synk up'",
	Long: `Stop the agent. The link goes down until 'synk up' or the next login.

On Linux the injector is left running: without the agent it has nobody to take
commands from, and leaving it up means 'synk up' needs no password.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := service.New().Stop(); err != nil {
			return serviceError("stop", err)
		}
		say(cmd.OutOrStdout(), "agent stopped")
		return nil
	},
}

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the agent, starting it if it was stopped",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := service.New().Restart(); err != nil {
			return serviceError("restart", err)
		}
		say(cmd.OutOrStdout(), "agent restarted")
		return nil
	},
}

func init() { rootCmd.AddCommand(upCmd, downCmd, restartCmd) }
