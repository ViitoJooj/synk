//go:build linux

// Package injector is the part of synk that needs root.
//
// It opens /dev/uinput and does nothing else. It has no network access at all —
// the systemd unit gives it PrivateNetwork=yes — and it never reads your
// keyboard: with the Windows PC as the machine that owns the keyboard and
// mouse, injection is the only direction that exists here.
//
// It takes commands from the unprivileged agent over a unix socket, and
// releases every key it is holding the moment that agent goes away.
package injector

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ViitoJooj/synk/internal/inputdev"
	"github.com/ViitoJooj/synk/internal/ipc"
)

// Run serves the socket until ctx is cancelled.
func Run(ctx context.Context, socket string, out io.Writer) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("the daemon needs root for /dev/uinput; run it through systemd, or with sudo to test")
	}

	// systemd's RuntimeDirectory= normally makes this; creating it here keeps
	// a manual run working too.
	if err := os.MkdirAll(filepath.Dir(socket), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(socket), err)
	}

	virtual, err := inputdev.OpenVirtual(inputdev.VirtualNamePrefix)
	if err != nil {
		return err
	}
	defer virtual.Close()

	srv, err := ipc.NewServer(socket, virtual)
	if err != nil {
		return err
	}
	defer srv.Close()

	fmt.Fprintf(out, "synk injector ready on %s\n", socket)
	return srv.Serve(ctx)
}
