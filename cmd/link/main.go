// Command wfkit-link shares one keyboard, mouse, clipboard and sound system
// between this machine and a peer on the same tailnet.
//
// It is a separate binary rather than a `wfkit` subcommand because
// skills/cli_commands.md fixes the wfkit command set, and because this one has
// to cross-compile to Windows, which the wfkit CLI cannot (internal/registry
// wraps chattr).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"wfkit-link/internal/config"
)

const configName = "link.yaml"

// linkPeer and linkPort override discovery and the default port. They exist for
// the machine that carries no configuration file, and as an escape hatch when a
// tailnet has more than one candidate on it.
var (
	linkPeer string
	linkPort int
)

var rootCmd = &cobra.Command{
	Use:   "wfkit-link",
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

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "wfkit-link:", err)
		os.Exit(1)
	}
}

// configDir is ~/.config/wfkit, matching skills/folder_structure.md: per-user
// preferences live there, never system state.
func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating your config directory: %w", err)
	}
	return filepath.Join(base, "wfkit"), nil
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configName), nil
}

// portFor picks the port: the flag wins, then the config, then the default.
func portFor(cfg *config.Config) int {
	switch {
	case linkPort != 0:
		return linkPort
	case cfg != nil && cfg.Port != 0:
		return cfg.Port
	default:
		return config.DefaultPort
	}
}

// clipboardEnabled defaults to on when there is no configuration to consult.
// The machine without a config file is the one the user never has to set up,
// and a clipboard that silently does nothing there would be a puzzle; the
// receiving side's config still decides whether anything is sent to it.
func clipboardEnabled(cfg *config.Config) bool {
	if cfg == nil {
		return true
	}
	return cfg.Clipboard
}
