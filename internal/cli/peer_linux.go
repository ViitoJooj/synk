//go:build linux

package cli

import (
	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

// chosenPeer is the machine to link with: --peer, or the config's. This side
// listens, and accepts only that machine.
func chosenPeer(cfg *config.Config) string {
	if linkPeer != "" {
		return linkPeer
	}
	if cfg != nil {
		return cfg.Peer
	}
	return ""
}

// saveChoice writes the peer into the config, which on this machine is where
// it has always lived.
func saveChoice(name string) error {
	path, err := configPath()
	if err != nil {
		return errorx.Failed("locate the config", err)
	}
	if err := config.SetPeer(path, name); err != nil {
		return errorx.NoConfig(path, err)
	}
	return nil
}
