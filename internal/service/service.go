// Package service starts, stops and inspects synk's background processes: the
// systemd units on Linux and the scheduled task on Windows.
//
// It does not install them: hacks/install.sh and infra/windows/install.ps1 do,
// because installing needs root on Linux and an interactive PowerShell on
// Windows.
package service

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/ViitoJooj/synk/internal/procx"
)

// Names of what infra/ installs.
const (
	AgentUnit    = "synk-agent.service"
	InjectorUnit = "synk-injector.service"
	TaskName     = "synk"
)

// ErrNotInstalled means there is no unit or task to start: the installer has
// not run on this machine.
var ErrNotInstalled = errors.New("service: synk is not installed on this machine")

// State is one background process as the service manager sees it.
type State struct {
	// Name is what synk calls it: "agent" or "injector".
	Name string `json:"name"`
	// Unit is what the service manager calls it.
	Unit      string `json:"unit"`
	Installed bool   `json:"installed"`
	// Active is the manager's own word: systemd's ActiveState ("active",
	// "inactive", "failed", ...) or the task's state ("Running", "Ready").
	Active string `json:"active"`
	// Detail adds what the manager says beyond that, such as systemd's
	// SubState.
	Detail string `json:"detail,omitempty"`
}

// Running reports whether the process is up.
func (s State) Running() bool {
	switch s.Active {
	case "active", "Running":
		return true
	}
	return false
}

// Runner runs the service manager's commands. Output returns what the command
// printed; Interactive hands it the terminal, which systemctl needs to ask for
// a password through polkit.
type Runner interface {
	Output(name string, args ...string) (string, error)
	Interactive(name string, args ...string) error
}

type execRunner struct{}

func (execRunner) Output(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	procx.Hide(cmd)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (execRunner) Interactive(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

type Manager struct{ run Runner }

func New() *Manager { return &Manager{run: execRunner{}} }

func NewWithRunner(r Runner) *Manager { return &Manager{run: r} }
