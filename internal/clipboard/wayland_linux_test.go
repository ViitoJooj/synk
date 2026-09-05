//go:build linux

package clipboard

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

type fakeRunner struct {
	out      []byte
	err      error
	lastCmd  string
	lastArgs []string
	lastIn   []byte
}

func (f *fakeRunner) Output(name string, args ...string) ([]byte, error) {
	f.lastCmd, f.lastArgs = name, args
	return f.out, f.err
}

func (f *fakeRunner) RunInput(name string, input []byte, args ...string) error {
	f.lastCmd, f.lastArgs, f.lastIn = name, args, input
	return f.err
}

func TestReadUsesWlPaste(t *testing.T) {
	r := &fakeRunner{out: []byte("copied text")}
	w := NewWaylandWithRunner(r)

	got, err := w.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != "copied text" {
		t.Errorf("Read() = %q, want %q", got, "copied text")
	}
	if r.lastCmd != "wl-paste" {
		t.Errorf("ran %q, want wl-paste", r.lastCmd)
	}
	// --no-newline matters: wl-paste otherwise appends one and every poll
	// looks like a change.
	if !contains(r.lastArgs, "--no-newline") {
		t.Errorf("args = %v, want --no-newline", r.lastArgs)
	}
}

// wl-paste exits non-zero on an empty clipboard. That is an ordinary state,
// not a failure to report upward.
func TestReadOnEmptyClipboardReturnsEmpty(t *testing.T) {
	r := &fakeRunner{err: &exec.ExitError{}}
	w := NewWaylandWithRunner(r)

	got, err := w.Read()
	if err != nil {
		t.Fatalf("Read on an empty clipboard: %v", err)
	}
	if got != "" {
		t.Errorf("Read() = %q, want empty", got)
	}
}

func TestWriteUsesWlCopyWithTheTextOnStdin(t *testing.T) {
	r := &fakeRunner{}
	w := NewWaylandWithRunner(r)

	if err := w.Write("to windows"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if r.lastCmd != "wl-copy" {
		t.Errorf("ran %q, want wl-copy", r.lastCmd)
	}
	if string(r.lastIn) != "to windows" {
		t.Errorf("stdin = %q, want %q", r.lastIn, "to windows")
	}
}

// wl-clipboard is not installed on this machine yet, so the error a user hits
// first has to name the package rather than say "file not found".
func TestMissingWlClipboardNamesThePackage(t *testing.T) {
	r := &fakeRunner{err: errors.New(`exec: "wl-paste": executable file not found in $PATH`)}
	w := NewWaylandWithRunner(r)

	_, err := w.Read()

	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if !strings.Contains(err.Error(), "wl-clipboard") {
		t.Errorf("err = %q, want it to name the wl-clipboard package", err)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
