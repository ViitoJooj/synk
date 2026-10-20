package cli

import (
	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/agent"
	"github.com/ViitoJooj/synk/internal/config"
)

// agentVerbose is set by a flag only Windows has: counters of captured events
// mean nothing on the machine that captures none.
var agentVerbose bool

// agentCmd is what the service manager runs. It is hidden because nobody else
// should: `synk up` starts it the right way. "session" is its name from before
// the rename, kept so an installed unit that still says it keeps working.
var agentCmd = &cobra.Command{
	Use:     "agent",
	Aliases: []string{"session"},
	Short:   agentShort,
	Long:    agentLong,
	Hidden:  true,
	Args:    cobra.NoArgs,
	RunE:    runAgent,
}

func init() { rootCmd.AddCommand(agentCmd) }

func runAgent(cmd *cobra.Command, _ []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	ctx, stop := signalContext()
	defer stop()

	return agent.Run(ctx, agentOptions(cmd, cfg))
}

// agentOptions builds what the agent runs with, and how it rebuilds that when
// the settings change under it.
func agentOptions(cmd *cobra.Command, cfg *config.Config) agent.Options {
	return agent.Options{
		Config:  cfg,
		Peer:    chosenPeer(cfg),
		Port:    linkPort,
		Verbose: agentVerbose,
		Out:     cmd.OutOrStdout(),
		Reload: func() (agent.Options, error) {
			cfg, err := loadConfig()
			if err != nil {
				return agent.Options{}, err
			}
			return agentOptions(cmd, cfg), nil
		},
	}
}
