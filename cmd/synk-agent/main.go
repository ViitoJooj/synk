// Command synk-agent is the same agent as 'synk agent', built without a
// console (GOOS=windows go build -ldflags "-H=windowsgui") so the Windows
// scheduled task never flashes one open on logon or on 'synk up' /
// start_agent from the window — a console-subsystem exe gets one from
// Windows the instant it is created, before any Go code runs to hide it.
//
// A console subsystem process has no usable stdout/stderr here (there is no
// console to own them), so this redirects both to a log file before running
// the normal CLI with its argument forced to "agent": this binary never does
// anything else, and install.ps1 points the task at it directly instead of
// wrapping synk.exe in a hidden PowerShell.
package main

import (
	"os"
	"path/filepath"

	"github.com/ViitoJooj/synk/internal/cli"
)

func main() {
	logPath := os.Getenv("SYNK_LOG")
	if logPath == "" {
		if exe, err := os.Executable(); err == nil {
			logPath = filepath.Join(filepath.Dir(exe), "synk.log")
		}
	}
	if logPath != "" {
		// Truncated, not appended: install.ps1's old shell-redirection
		// version (*>) overwrote on every restart, and a Task Scheduler
		// RestartCount storm should not grow this file unbounded.
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
			os.Stdout = f
			os.Stderr = f
		}
	}

	os.Args = []string{os.Args[0], "agent"}
	cli.Execute()
}
