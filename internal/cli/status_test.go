package cli

import (
	"testing"

	"github.com/ViitoJooj/synk/internal/service"
)

func TestServiceWord(t *testing.T) {
	cases := []struct {
		state service.State
		want  string
	}{
		{service.State{Installed: false, Active: "inactive"}, "not installed"},
		{service.State{Installed: true, Active: "active"}, "running"},
		{service.State{Installed: true, Active: "inactive"}, "stopped"},
		{service.State{Installed: true, Active: "failed"}, "failed"},
		{service.State{Installed: true, Active: "Running"}, "running"},
		{service.State{Installed: true, Active: "Ready"}, "stopped"},
		{service.State{Installed: true, Active: "activating"}, "activating"},
	}
	for _, c := range cases {
		if got := serviceWord(c.state); got != c.want {
			t.Errorf("serviceWord(%+v) = %q, want %q", c.state, got, c.want)
		}
	}
}
