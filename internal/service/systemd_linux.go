//go:build linux

package service

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// States reports the agent (a user unit) and the injector (a system unit).
func (m *Manager) States() ([]State, error) {
	agent, err := m.unit("agent", AgentUnit, true)
	if err != nil {
		return nil, err
	}
	injector, err := m.unit("injector", InjectorUnit, false)
	if err != nil {
		return nil, err
	}
	return []State{agent, injector}, nil
}

func (m *Manager) unit(name, unit string, user bool) (State, error) {
	args := []string{"show", unit, "--property=LoadState,ActiveState,SubState"}
	if user {
		args = append([]string{"--user"}, args...)
	}
	out, err := m.run.Output("systemctl", args...)
	if err != nil {
		return State{}, fmt.Errorf("systemctl show %s: %w: %s", unit, err, out)
	}

	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			props[k] = v
		}
	}
	return State{
		Name:      name,
		Unit:      unit,
		Installed: props["LoadState"] == "loaded",
		Active:    props["ActiveState"],
		Detail:    props["SubState"],
	}, nil
}

// Start brings the injector up if it is down, then the agent. The injector is
// a system unit, so starting it may ask for your password; the agent is yours
// and never does.
func (m *Manager) Start() error {
	if err := m.ensureInstalled(); err != nil {
		return err
	}
	if err := m.ensureInjector(); err != nil {
		return err
	}
	return m.run.Interactive("systemctl", "--user", "start", AgentUnit)
}

// EnsureRunning starts the agent, and the injector under it, when the agent is
// down. Nothing starts at boot: synk runs from the moment a command or the
// window asks for it, so this is what those call first. It does nothing when
// the agent is already up.
func (m *Manager) EnsureRunning() error {
	agent, err := m.unit("agent", AgentUnit, true)
	if err != nil {
		return err
	}
	if !agent.Installed {
		return fmt.Errorf("%w: %s is missing", ErrNotInstalled, AgentUnit)
	}
	if agent.Running() {
		return nil
	}
	return m.Start()
}

// Stop stops the agent only. The injector does nothing without it, and leaving
// it up means `synk up` needs no password.
func (m *Manager) Stop() error {
	return m.run.Interactive("systemctl", "--user", "stop", AgentUnit)
}

// Restart restarts the agent, starting it if it was stopped.
func (m *Manager) Restart() error {
	if err := m.ensureInstalled(); err != nil {
		return err
	}
	if err := m.ensureInjector(); err != nil {
		return err
	}
	return m.run.Interactive("systemctl", "--user", "restart", AgentUnit)
}

// ensureInstalled checks both units before touching either, so a half
// install fails with one clear error rather than half a start.
func (m *Manager) ensureInstalled() error {
	states, err := m.States()
	if err != nil {
		return err
	}
	for _, s := range states {
		if !s.Installed {
			return fmt.Errorf("%w: %s is missing", ErrNotInstalled, s.Unit)
		}
	}
	return nil
}

func (m *Manager) ensureInjector() error {
	s, err := m.unit("injector", InjectorUnit, false)
	if err != nil {
		return err
	}
	if s.Running() {
		return nil
	}
	return m.run.Interactive("systemctl", "start", InjectorUnit)
}

// LogsCommand is journalctl for the agent, or for the injector. The caller
// runs it with the terminal attached so -f can follow.
func LogsCommand(injector, follow bool, lines int) *exec.Cmd {
	args := []string{"--user-unit", AgentUnit}
	if injector {
		args = []string{"--unit", InjectorUnit}
	}
	args = append(args, "--no-pager", "--lines", strconv.Itoa(lines), "--output", "short-iso")
	if follow {
		args = append(args, "--follow")
	}
	return exec.Command("journalctl", args...)
}
