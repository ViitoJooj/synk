//go:build linux

package config

// Chosen is the machine this side links with: the config's peer. This side
// listens, and accepts only that machine.
func Chosen(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Peer
}

// SaveChoice writes the peer into the config, which on this machine is where
// it has always lived.
func SaveChoice(name string) error {
	path, err := Path()
	if err != nil {
		return err
	}
	return SetPeer(path, name)
}

// KeepsConfig: this machine receives input, so it is the one with settings.
const KeepsConfig = true
