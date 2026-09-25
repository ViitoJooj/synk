// Package cli is synk's command line: it parses flags, finds the config and
// hands the work to the agent and the injector.
//
// synk began as a subcommand of the wfkit CLI, as wfkit-link and then conn, and
// left to be its own project: it is a two-machine app with its own life cycle,
// and it has to cross-compile to Windows, which the wfkit CLI cannot.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
)

const configName = "conn.yaml"

// linkPeer and linkPort override discovery and the default port. They exist for
// the machine that carries no configuration file, and as an escape hatch when a
// tailnet has more than one candidate on it.
var (
	linkPeer string
	linkPort int
)

var rootCmd = &cobra.Command{
	Use:   "synk",
	Short: "Share keyboard, mouse, clipboard and audio with a machine on your tailnet",

	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&linkPeer, "peer", "",
		"tailnet name of the other machine (default: the one named in the config, or the only computer online)")
	rootCmd.PersistentFlags().IntVar(&linkPort, "port", 0,
		"port to use instead of the configured one")
}

// Execute runs the command line and exits non-zero on failure.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "synk:", err)
		os.Exit(1)
	}
}

// signalContext is cancelled by Ctrl+C or by systemd stopping the unit.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// configDir is ~/.config/conn: per-user preferences, never system state.
// Until the rename it was ~/.config/wfkit/link.yaml; infra/install.sh moves
// an existing one over.
func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating your config directory: %w", err)
	}
	return filepath.Join(base, "conn"), nil
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configName), nil
}
