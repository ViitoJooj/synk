//go:build linux

package cli

import (
	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/injector"
)

var daemonSocket string

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the privileged injector (systemd runs this; you should not need to)",
	Long: `Run the part of synk that needs root.

This process opens /dev/uinput and does nothing else. It has no network access
at all — the systemd unit gives it PrivateNetwork=yes — and it never reads your
keyboard: with the Windows PC as the machine that owns the keyboard and mouse,
injection is the only direction that exists here.

It takes commands from the unprivileged session process over a unix socket, and
releases every key it is holding the moment that session goes away.`,
	Args: cobra.NoArgs,
	RunE: runDaemon,
}

func init() {
	daemonCmd.Flags().StringVar(&daemonSocket, "socket", config.DefaultSocket,
		"unix socket the session connects to")
	rootCmd.AddCommand(daemonCmd)
}

func runDaemon(cmd *cobra.Command, _ []string) error {
	ctx, stop := signalContext()
	defer stop()
	return injector.Run(ctx, daemonSocket, cmd.OutOrStdout())
}
