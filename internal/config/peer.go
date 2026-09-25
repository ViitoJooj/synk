package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SetPeer rewrites the peer in the config at path; see Set.
func SetPeer(path, name string) error { return Set(path, "peer", name) }

// LoadPeerFile reads the peer chosen with `synk <peer>` on a machine that keeps
// no config, which is the Windows side. A missing file is not an error: it
// means nothing was chosen and discovery decides.
func LoadPeerFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("config: reading %s: %w", path, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// SavePeerFile records the chosen peer, creating the directory if needed.
func SavePeerFile(path, name string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: creating %s: %w", filepath.Dir(path), err)
	}
	return writeAtomic(path, []byte(name+"\n"))
}

// writeAtomic replaces path in one step, so a crash halfway never leaves a
// truncated config for the agent to choke on at the next boot. The original
// file's permissions are kept.
func writeAtomic(path string, data []byte) error {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("config: writing %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // no-op once renamed

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("config: writing %s: %w", path, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("config: writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("config: writing %s: %w", path, err)
	}
	return nil
}
