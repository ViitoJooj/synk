package windows

import (
	"os"
	"path/filepath"
)

func GetUserHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return os.Getenv("USERPROFILE")
	}
	return home
}

func GetDownloadsPath() string {
	return filepath.Join(GetUserHome(), "Downloads")
}

func GetDocumentsPath() string {
	return filepath.Join(GetUserHome(), "Documents")
}

func GetPicturesPath() string {
	return filepath.Join(GetUserHome(), "Pictures")
}

func GetDesktopPath() string {
	return filepath.Join(GetUserHome(), "Desktop")
}

func GetVideosPath() string {
	return filepath.Join(GetUserHome(), "Videos")
}

func GetMusicPath() string {
	return filepath.Join(GetUserHome(), "Music")
}
