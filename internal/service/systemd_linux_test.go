//go:build linux

package service

import (
	"errors"
	"strings"
	"testing"
)

// fakeSystemd answers `systemctl show` from a table and records every
// interactive command it is asked to run.
type fakeSystemd struct {
	units map[string]string // unit -> show output
	ran   []string
	fail  error
}

func (f *fakeSystemd) Output(name string, args ...string) (string, error) {
	for unit, out := range f.units {
		for _, a := range args {
			if a == unit {
				return out, nil
			}
		}
	}
	return "LoadState=not-found\nActiveState=inactive\nSubState=dead", nil
}

func (f *fakeSystemd) Interactive(name string, args ...string) error {
	f.ran = append(f.ran, name+" "+strings.Join(args, " "))
	return f.fail
}

const (
	running = "LoadState=loaded\nActiveState=active\nSubState=running"
	stopped = "LoadState=loaded\nActiveState=inactive\nSubState=dead"
	failed  = "LoadState=loaded\nActiveState=failed\nSubState=failed"
)

func TestStatesReadsBothUnits(t *testing.T) {
	m := NewWithRunner(&fakeSystemd{units: map[string]string{AgentUnit: failed, InjectorUnit: running}})

	states, err := m.States()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 {
		t.Fatalf("got %d states", len(states))
	}
	agent, injector := states[0], states[1]
	if agent.Name != "agent" || agent.Running() || agent.Active != "failed" || !agent.Installed {
		t.Errorf("agent = %+v", agent)
	}
	if injector.Name != "injector" || !injector.Running() || injector.Detail != "running" {
		t.Errorf("injector = %+v", injector)
	}
}

func TestStatesSaysWhenNothingIsInstalled(t *testing.T) {
	states, err := NewWithRunner(&fakeSystemd{}).States()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range states {
		if s.Installed {
			t.Errorf("%s reported installed", s.Name)
		}
	}
}

// The password prompt is the price of starting the injector, so it is paid
// only when the injector is actually down.
func TestStartLeavesARunningInjectorAlone(t *testing.T) {
	f := &fakeSystemd{units: map[string]string{AgentUnit: stopped, InjectorUnit: running}}

	if err := NewWithRunner(f).Start(); err != nil {
		t.Fatal(err)
	}
	if len(f.ran) != 1 || f.ran[0] != "systemctl --user start "+AgentUnit {
		t.Errorf("ran %q", f.ran)
	}
}

func TestStartBringsTheInjectorUpFirst(t *testing.T) {
	f := &fakeSystemd{units: map[string]string{AgentUnit: stopped, InjectorUnit: stopped}}

	if err := NewWithRunner(f).Start(); err != nil {
		t.Fatal(err)
	}
	want := []string{"systemctl start " + InjectorUnit, "systemctl --user start " + AgentUnit}
	if strings.Join(f.ran, "|") != strings.Join(want, "|") {
		t.Errorf("ran %q, want %q", f.ran, want)
	}
}

func TestStartRefusesWithoutAnInjector(t *testing.T) {
	f := &fakeSystemd{units: map[string]string{AgentUnit: stopped}}

	err := NewWithRunner(f).Start()
	if !errors.Is(err, ErrNotInstalled) || !strings.Contains(err.Error(), InjectorUnit) {
		t.Fatalf("err = %v, want ErrNotInstalled naming the missing unit", err)
	}
	if len(f.ran) != 0 {
		t.Errorf("ran %q with nothing installed", f.ran)
	}
}

func TestRestartRefusesWithoutAnAgent(t *testing.T) {
	f := &fakeSystemd{units: map[string]string{InjectorUnit: running}}

	if err := NewWithRunner(f).Restart(); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v, want ErrNotInstalled", err)
	}
	if len(f.ran) != 0 {
		t.Errorf("ran %q with the agent missing", f.ran)
	}
}

func TestStopTouchesOnlyTheAgent(t *testing.T) {
	f := &fakeSystemd{units: map[string]string{AgentUnit: running, InjectorUnit: running}}

	if err := NewWithRunner(f).Stop(); err != nil {
		t.Fatal(err)
	}
	if len(f.ran) != 1 || f.ran[0] != "systemctl --user stop "+AgentUnit {
		t.Errorf("ran %q", f.ran)
	}
}

func TestRestartReportsFailure(t *testing.T) {
	boom := errors.New("exit status 1")
	f := &fakeSystemd{units: map[string]string{AgentUnit: running, InjectorUnit: running}, fail: boom}

	if err := NewWithRunner(f).Restart(); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the systemctl failure", err)
	}
}

func TestLogsCommand(t *testing.T) {
	got := strings.Join(LogsCommand(false, true, 50).Args, " ")
	want := "journalctl --user-unit " + AgentUnit + " --no-pager --lines 50 --output short-iso --follow"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := strings.Join(LogsCommand(true, false, 10).Args, " "); !strings.Contains(got, "--unit "+InjectorUnit) {
		t.Errorf("injector logs: %s", got)
	}
}
