//go:build windows

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// States reports the scheduled task. Windows has no injector: this side owns
// the keyboard and injects nothing.
func (m *Manager) States() ([]State, error) {
	out, err := m.powershell(fmt.Sprintf("(Get-ScheduledTask -TaskName '%s' -ErrorAction Stop).State", TaskName))
	if err != nil {
		// Get-ScheduledTask fails only when the task does not exist; anything
		// else would have failed the same way on every other call too.
		return []State{{Name: "agent", Unit: TaskName, Active: "not installed"}}, nil
	}
	return []State{{Name: "agent", Unit: TaskName, Installed: true, Active: out}}, nil
}

func (m *Manager) Start() error {
	return m.task("Start-ScheduledTask")
}

// Stop ends the task. Task Scheduler stops it the hard way, the same as it
// always has; the capture hooks and the cursor clip go with the process.
func (m *Manager) Stop() error {
	return m.task("Stop-ScheduledTask")
}

func (m *Manager) Restart() error {
	if err := m.Stop(); err != nil {
		return err
	}
	return m.Start()
}

func (m *Manager) task(cmdlet string) error {
	if out, err := m.powershell(fmt.Sprintf("%s -TaskName '%s' -ErrorAction Stop", cmdlet, TaskName)); err != nil {
		return fmt.Errorf("%s: %w: %s", cmdlet, err, out)
	}
	return nil
}

func (m *Manager) powershell(script string) (string, error) {
	return m.run.Output("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
}

// logRedirect finds the file install.ps1 -Hidden sends output to.
var logRedirect = regexp.MustCompile(`\*>\s*'([^']+)'`)

// LogPath is where the agent writes: the file named in the task's own action,
// so it is right even when this synk.exe is a different copy from the one the
// task runs. Without a hidden task, the log sits beside this executable.
func (m *Manager) LogPath() (string, error) {
	args, err := m.powershell(fmt.Sprintf("(Get-ScheduledTask -TaskName '%s' -ErrorAction Stop).Actions[0].Arguments", TaskName))
	if err == nil {
		if match := logRedirect.FindStringSubmatch(args); match != nil {
			return match[1], nil
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "synk.log"), nil
}
