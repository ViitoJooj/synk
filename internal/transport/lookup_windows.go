//go:build windows

package transport

import (
	"os"
	"os/exec"
)

// tailscaleBinary finds the Tailscale CLI: PATH first, then the standard
// install locations. If nothing is found it returns the bare name, so the
// failure surfaces as an ordinary "not found" naming the command.
func tailscaleBinary() string {
	if path, err := exec.LookPath("tailscale"); err == nil {
		return path
	}
	for _, candidate := range windowsCandidates(os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return "tailscale"
}
