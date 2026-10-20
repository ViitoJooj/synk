//go:build linux

package cli

import (
	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/injector"
)

var injectorSocket string

// injectorCmd is what the system unit runs; "daemon" is its name from before
// the rename, kept for installed units.
var injectorCmd = &cobra.Command{
	Use:     "injector",
	Aliases: []string{"daemon"},
	Short:   "Run the privileged injector (systemd runs this; you should not need to)",
	Long: `Run the part of synk that needs root.

This process opens /dev/uinput and does nothing else. It has no network access
at all — the systemd unit gives it PrivateNetwork=yes — and it never reads your
keyboard: with the Windows PC as the machine that owns the keyboard and mouse,
injection is the only direction that exists here.

It takes commands from the unprivileged agent over a unix socket, and releases
every key it is holding the moment that agent goes away.`,
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runInjector,
}

func init() {
	injectorCmd.Flags().StringVar(&injectorSocket, "socket", config.DefaultSocket,
		"unix socket the agent connects to")
	rootCmd.AddCommand(injectorCmd)
}

func runInjector(cmd *cobra.Command, _ []string) error {
	ctx, stop := signalContext()
	defer stop()
	return injector.Run(ctx, injectorSocket, cmd.OutOrStdout())
}
