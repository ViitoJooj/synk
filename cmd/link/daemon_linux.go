//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"wfkit-link/internal/config"
	"wfkit-link/internal/inputdev"
	"wfkit-link/internal/ipc"
)

var daemonSocket string

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the privileged injector (systemd runs this; you should not need to)",
	Long: `Run the part of wfkit-link that needs root.

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
	if os.Geteuid() != 0 {
		return fmt.Errorf("the daemon needs root for /dev/uinput; run it through systemd, or with sudo to test")
	}

	// systemd's RuntimeDirectory= normally makes this; creating it here keeps
	// a manual run working too.
	if err := os.MkdirAll(filepath.Dir(daemonSocket), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(daemonSocket), err)
	}

	virtual, err := inputdev.OpenVirtual(inputdev.VirtualNamePrefix)
	if err != nil {
		return err
	}
	defer virtual.Close()

	srv, err := ipc.NewServer(daemonSocket, virtual)
	if err != nil {
		return err
	}
	defer srv.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(cmd.OutOrStdout(), "wfkit-link daemon ready on %s\n", daemonSocket)
	return srv.Serve(ctx)
}
