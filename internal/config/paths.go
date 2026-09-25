package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the settings file inside Dir.
const FileName = "config.yaml"

// Dir is where synk keeps what belongs to the user: ~/.config/synk on Linux,
// %APPDATA%\synk on Windows. Per-user preferences, never system state.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locating your config directory: %w", err)
	}
	return filepath.Join(base, "synk"), nil
}

// Path is the settings file.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// LegacyPaths are where the settings lived under the old names, newest first.
// hacks/install.sh moves the first one it finds to Path; they are listed here
// so doctor can say so when that has not happened yet.
func LegacyPaths() []string {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(base, "conn", "conn.yaml"),  // conn, 2026-09-24 to 2026-09-25
		filepath.Join(base, "wfkit", "link.yaml"), // wfkit-link, until 2026-09-24
	}
}

// Legacy is the first old settings file that exists, or "".
func Legacy() string {
	for _, p := range LegacyPaths() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
