package shared

import (
	"os"
	"runtime"

	"synk/internal/linux"
	"synk/internal/windows"
)

const osWindows = "windows"

type folders struct {
	home      func() string
	documents func() string
	downloads func() string
	pictures  func() string
	desktop   func() string
	videos    func() string
	music     func() string
}

var current = newFolders(runtime.GOOS)

func newFolders(goos string) folders {
	if goos == osWindows {
		return folders{
			home:      windows.GetUserHome,
			documents: windows.GetDocumentsPath,
			downloads: windows.GetDownloadsPath,
			pictures:  windows.GetPicturesPath,
			desktop:   windows.GetDesktopPath,
			videos:    windows.GetVideosPath,
			music:     windows.GetMusicPath,
		}
	}
	return folders{
		home:      linux.GetUserHome,
		documents: linux.GetDocumentsPath,
		downloads: linux.GetDownloadsPath,
		pictures:  linux.GetPicturesPath,
		desktop:   linux.GetDesktopPath,
		videos:    linux.GetVideosPath,
		music:     linux.GetMusicPath,
	}
}

func GetSystem() string { return runtime.GOOS }

func Hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "unknown"
	}
	return name
}

func Home() string      { return current.home() }
func Documents() string { return current.documents() }
func Downloads() string { return current.downloads() }
func Pictures() string  { return current.pictures() }
func Desktop() string   { return current.desktop() }
func Videos() string    { return current.videos() }
func Music() string     { return current.music() }
