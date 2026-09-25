package cli

import "github.com/ViitoJooj/synk/internal/config"

// chosenPeer is the machine to link with: --peer, or the one chosen with
// `synk <peer>`.
func chosenPeer(cfg *config.Config) string {
	if linkPeer != "" {
		return linkPeer
	}
	return config.Chosen(cfg)
}

const keepsConfig = config.KeepsConfig
