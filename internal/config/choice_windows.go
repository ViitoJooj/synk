//go:build windows

package config

import "path/filepath"

// Chosen is the machine this side links with: the one chosen with
// `synk <peer>`, or nothing, which leaves discovery to pick the only computer
// online.
//
// The choice is a file holding one name, not a config. This machine reads no
// config on purpose — a stale one once sent the link to a port nobody listened
// on — and a name that is wrong fails loudly with "peer not found" instead.
func Chosen(*Config) string {
	path, err := choicePath()
	if err != nil {
		return ""
	}
	name, _ := LoadPeerFile(path)
	return name
}

func SaveChoice(name string) error {
	path, err := choicePath()
	if err != nil {
		return err
	}
	return SavePeerFile(path, name)
}

func choicePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "peer"), nil
}

// KeepsConfig: this machine reads no config; see Chosen.
const KeepsConfig = false
