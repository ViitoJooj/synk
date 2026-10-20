//go:build linux

package cli

import (
	"errors"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/service"
)

func init() {
	logsCmd.Flags().BoolVar(&logsInjector, "injector", false, "show the injector's journal instead of the agent's")
}

func runLogs(cmd *cobra.Command, _ []string) error {
	c := service.LogsCommand(logsInjector, logsFollow, logsLines)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()

	err := c.Run()
	// journalctl has already printed why it failed, and Ctrl+C ending -f is
	// how following is supposed to stop.
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return errReported
	}
	return err
}
