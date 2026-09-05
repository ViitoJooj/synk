//go:build linux

package clipboard

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes wl-clipboard, injected so tests need neither the binary nor
// a Wayland session. Mirrors internal/service.Runner.
type Runner interface {
	Output(name string, args ...string) ([]byte, error)
	RunInput(name string, input []byte, args ...string) error
}

type execRunner struct{}

func (execRunner) Output(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

func (execRunner) RunInput(name string, input []byte, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = bytes.NewReader(input)
	return cmd.Run()
}

// Wayland reads and writes the clipboard through wl-clipboard.
//
// There is no Wayland protocol for a background process to touch the clipboard
// — that is deliberate, and wl-copy works by briefly acting as a client of the
// compositor. Shelling out to it is the supported route, not a shortcut.
type Wayland struct{ runner Runner }

func NewWayland() *Wayland { return &Wayland{runner: execRunner{}} }

func NewWaylandWithRunner(r Runner) *Wayland { return &Wayland{runner: r} }

// Read returns the current clipboard text. An empty clipboard is empty text,
// not an error: wl-paste exits non-zero for it, which is an ordinary state.
func (w *Wayland) Read() (string, error) {
	out, err := w.runner.Output("wl-paste", "--no-newline", "--type", "text/plain")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", nil
		}
		return "", missingTool(err, "wl-paste")
	}
	return string(out), nil
}

// Write puts text on the clipboard.
func (w *Wayland) Write(text string) error {
	if err := w.runner.RunInput("wl-copy", []byte(text), "--type", "text/plain"); err != nil {
		return missingTool(err, "wl-copy")
	}
	return nil
}

// missingTool turns Go's "executable file not found" into something that says
// what to install. wl-clipboard is not part of a stock Debian desktop.
func missingTool(err error, tool string) error {
	if strings.Contains(err.Error(), "executable file not found") {
		return fmt.Errorf("clipboard: %s is missing; install the wl-clipboard package: %w", tool, err)
	}
	return fmt.Errorf("clipboard: running %s: %w", tool, err)
}
