//go:build windows

package procx

import (
	"os/exec"
	"syscall"
)

// Hide stops Windows from flashing a console window for cmd. The agent has
// no console of its own (it runs hidden, via install.ps1 -Hidden), so every
// console-subsystem child it spawns — tailscale.exe, powershell.exe — would
// otherwise pop one open on its own, visible for as long as the command
// takes to run.
func Hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
