package agent

import (
	"errors"
	"testing"

	"github.com/ViitoJooj/synk/internal/transport"
)

func TestTargetErrorCodes(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{&transport.TargetError{Name: "desk", Known: []string{"pc"}, Err: transport.ErrPeerNotFound}, "snk-00"},
		{&transport.TargetError{Name: "linux", ByOS: true, Matches: []string{"a", "b"}, Err: transport.ErrAmbiguous}, "snk-01"},
		{&transport.TargetError{Name: "linux", ByOS: true, Err: transport.ErrPeerNotFound}, "snk-02"},
		{transport.ErrNotLoggedIn, "snk-03"},
	}
	for _, c := range cases {
		if got := TargetError(c.err); got.Code != c.code {
			t.Errorf("TargetError(%v) = %s, want %s", c.err, got.Code, c.code)
		}
	}
	if !errors.Is(TargetError(transport.ErrNotLoggedIn), transport.ErrNotLoggedIn) {
		t.Error("the Tailscale error is not kept as the cause")
	}
}
