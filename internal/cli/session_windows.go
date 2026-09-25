//go:build windows

package cli

const sessionShort = "Capture this machine's keyboard and mouse and drive the peer with them"

const sessionLong = `Run the sending half of synk.

This machine owns the physical keyboard and mouse. While the pointer is on this
screen nothing changes; when it reaches the border the peer sits beyond, input
is swallowed here and forwarded there instead.

It dials the peer rather than waiting to be dialled, and needs no configuration
file: the peer is found on the tailnet, and the screen layout arrives in its
greeting. Nothing to copy onto this machine but the program itself.

Ctrl+Alt+F12 always returns control to this machine, whatever state the link is
in.`

func init() {
	sessionCmd.Flags().BoolVar(&sessionVerbose, "verbose", false,
		"report captured-event counters, to tell a dead hook from a quiet link")
}
