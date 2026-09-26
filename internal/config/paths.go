package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// StateDir is where synk keeps what it works out for itself — the folders'
// agreed state and scan caches — as opposed to what the user sets:
// ~/.local/state/synk on Linux, %LOCALAPPDATA%\synk\state on Windows.
func StateDir() (string, error) { return dataDir("XDG_STATE_HOME", ".local/state", "state") }

// TrashDir is where the sync puts what it deletes or overwrites:
// ~/.local/share/synk/trash on Linux, %LOCALAPPDATA%\synk\trash on Windows.
// It is outside every synced folder, so it is never synced itself.
func TrashDir() (string, error) {
	dir, err := dataDir("XDG_DATA_HOME", ".local/share", "")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "trash"), nil
}

// dataDir resolves the XDG directory on Linux and LOCALAPPDATA on Windows.
func dataDir(xdgVar, xdgDefault, windowsSub string) (string, error) {
	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return "", fmt.Errorf("config: LOCALAPPDATA is not set")
		}
		return filepath.Join(local, "synk", windowsSub), nil
	}
	if dir := os.Getenv(xdgVar); dir != "" {
		return filepath.Join(dir, "synk"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: locating your home directory: %w", err)
	}
	return filepath.Join(home, filepath.FromSlash(xdgDefault), "synk"), nil
}
