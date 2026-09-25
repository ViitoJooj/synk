//go:build linux

package cli

const sessionShort = "Accept the peer's connection and apply its input locally"

const sessionLong = `Run the unprivileged half of synk.

This process owns the network connection, the clipboard and the screen
geometry. It holds no privilege of its own: to type, it asks the daemon over a
unix socket.

This machine listens and the peer dials in. That direction is deliberate: an
inbound listener on Windows needs a firewall rule and an administrator to
approve it, while an outbound connection needs neither — and this is the side
whose logs are easy to read when something goes wrong.

Only the peer's own tailnet address is accepted. Nothing else on the tailnet,
and nothing off it, can connect.`
