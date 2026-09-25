//go:build windows

package cli

import (
	"path/filepath"

	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

// chosenPeer is the machine to link with: --peer, then the one chosen with
// `synk <peer>`, and otherwise nothing, which leaves discovery to pick the only
// computer online.
//
// The choice is a file holding one name, not a config. This machine reads no
// config on purpose — a stale one once sent the link to a port nobody listened
// on — and a name that is wrong fails loudly with "peer not found" instead.
func chosenPeer(*config.Config) string {
	if linkPeer != "" {
		return linkPeer
	}
	path, err := choicePath()
	if err != nil {
		return ""
	}
	name, _ := config.LoadPeerFile(path)
	return name
}

func saveChoice(name string) error {
	path, err := choicePath()
	if err != nil {
		return errorx.Failed("locate the config directory", err)
	}
	if err := config.SavePeerFile(path, name); err != nil {
		return errorx.Failed("save the chosen peer", err)
	}
	return nil
}

func choicePath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "peer"), nil
}

// keepsConfig: this machine reads no config; see chosenPeer.
const keepsConfig = false
