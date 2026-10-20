package linux

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

func GetUserHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.Getenv("HOME")
	}
	return home
}

func getKDEFolder(folderKey string, fallbackName string) string {
	home := GetUserHome()
	configFile := filepath.Join(home, ".config", "user-dirs.dirs")

	file, err := os.Open(configFile)
	if err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, folderKey) {
				parts := strings.Split(line, "=")
				if len(parts) == 2 {
					val := strings.Trim(parts[1], `"`)
					val = strings.Replace(val, "$HOME", home, 1)
					if _, err := os.Stat(val); err == nil {
						return val
					}
				}
			}
		}
	}

	defaultPath := filepath.Join(home, fallbackName)
	if _, err := os.Stat(defaultPath); err == nil {
		return defaultPath
	}

	return filepath.Join(home, fallbackName)
}

func GetDownloadsPath() string {
	return getKDEFolder("XDG_DOWNLOAD_DIR", "Downloads")
}

func GetDocumentsPath() string {
	return getKDEFolder("XDG_DOCUMENTS_DIR", "Documents")
}

func GetPicturesPath() string {
	return getKDEFolder("XDG_PICTURES_DIR", "Pictures")
}

func GetDesktopPath() string {
	return getKDEFolder("XDG_DESKTOP_DIR", "Desktop")
}

func GetVideosPath() string {
	return getKDEFolder("XDG_VIDEOS_DIR", "Videos")
}

func GetMusicPath() string {
	return getKDEFolder("XDG_MUSIC_DIR", "Music")
}
