package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// peerLine is the top-level peer key, with its value and any trailing comment.
var peerLine = regexp.MustCompile(`(?m)^peer:[ \t]*[^#\n]*?([ \t]+#[^\n]*)?[ \t]*$`)

// topLevelKey finds the first setting, which is where a missing peer goes.
var topLevelKey = regexp.MustCompile(`(?m)^[A-Za-z_]`)

// SetPeer rewrites the peer in the config at path and leaves every other byte
// as it was: this is the user's file, with their comments and their blank
// lines, and `synk <peer>` should change one line of it. Round-tripping it
// through a YAML encoder would reflow all of that, so the line is edited in
// place and the result parsed before it is written.
//
// The file must already exist. A config without an edge is not one this
// machine can run with, so there is nothing sensible to create from a name
// alone.
func SetPeer(path, name string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: reading %s: %w", path, err)
	}

	value, err := yaml.Marshal(name)
	if err != nil {
		return fmt.Errorf("config: encoding %q: %w", name, err)
	}
	line := "peer: " + strings.TrimSpace(string(value))

	var out []byte
	if loc := peerLine.FindSubmatchIndex(raw); loc != nil {
		comment := ""
		if loc[2] >= 0 {
			comment = string(raw[loc[2]:loc[3]])
		}
		out = append(out, raw[:loc[0]]...)
		out = append(out, line+comment...)
		out = append(out, raw[loc[1]:]...)
	} else {
		at := len(raw)
		if loc := topLevelKey.FindIndex(raw); loc != nil {
			at = loc[0]
		}
		out = append(out, raw[:at]...)
		out = append(out, line+"\n"...)
		out = append(out, raw[at:]...)
	}

	var check struct {
		Peer string `yaml:"peer"`
	}
	if err := yaml.Unmarshal(out, &check); err != nil || check.Peer != name {
		return fmt.Errorf("config: %s has a layout synk cannot edit safely; set peer: %s there by hand", path, name)
	}
	return writeAtomic(path, out)
}

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
