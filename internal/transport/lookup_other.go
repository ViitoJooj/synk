//go:build !windows

package transport

// tailscaleBinary is just the command name everywhere but Windows: package
// managers put it on PATH, which is where it belongs.
func tailscaleBinary() string { return "tailscale" }
