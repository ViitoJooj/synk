//go:build !windows

package procx

import "os/exec"

// Hide is a no-op outside Windows: no other platform flashes a console for a
// background process.
func Hide(*exec.Cmd) {}
