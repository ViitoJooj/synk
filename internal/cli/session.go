package cli

import (
	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/agent"
)

// sessionVerbose is set by a flag only Windows has: counters of captured
// events mean nothing on the machine that captures none.
var sessionVerbose bool

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: sessionShort,
	Long:  sessionLong,
	Args:  cobra.NoArgs,
	RunE:  runSession,
}

func init() { rootCmd.AddCommand(sessionCmd) }

func runSession(cmd *cobra.Command, _ []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	ctx, stop := signalContext()
	defer stop()

	return agent.Run(ctx, agent.Options{
		Config:  cfg,
		Peer:    linkPeer,
		Port:    linkPort,
		Verbose: sessionVerbose,
		Out:     cmd.OutOrStdout(),
	})
}
