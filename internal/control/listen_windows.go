//go:build windows

package control

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// Address is the agent's named pipe. The user's name is part of it so two
// people logged in to the same PC each reach their own agent.
func Address() string {
	return `\\.\pipe\synk-control-` + os.Getenv("USERNAME")
}

// errRefused is never returned here: the pipe's ACL does the refusing.
var errRefused = errors.New("control: connection refused")

// Listen opens the pipe with an ACL that lets in only the user the agent runs
// as. The default ACL would also admit everyone in the Administrators group
// and the system account, which have no business changing a user's settings.
func Listen(addr string) (net.Listener, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, fmt.Errorf("control: finding who this agent runs as: %w", err)
	}
	l, err := winio.ListenPipe(addr, &winio.PipeConfig{
		// D:P = protected DACL (nothing inherited); GA = full access.
		SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")",
	})
	if err != nil {
		return nil, fmt.Errorf("control: listening on %s: %w", addr, err)
	}
	return l, nil
}

func currentUserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func dialAddr(ctx context.Context, addr string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, addr)
}
