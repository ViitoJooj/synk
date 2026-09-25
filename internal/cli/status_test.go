package cli

import (
	"testing"
	"time"

	"github.com/ViitoJooj/synk/internal/agent"
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

func TestLinkDetail(t *testing.T) {
	now := time.Date(2026, 9, 25, 21, 0, 0, 0, time.UTC)

	up := agent.Link{State: agent.LinkConnected, Peer: "pc", Since: now.Add(-75 * time.Minute), Focus: "local", Audio: "flowing"}
	if got := linkDetail(up, now); got != "with pc for 1h15m, keyboard here, audio flowing" {
		t.Errorf("up: %q", got)
	}

	down := agent.Link{State: agent.LinkWaiting, Peer: "pc", LastError: "peer closed the connection"}
	if got := linkDetail(down, now); got != "for pc; last drop: peer closed the connection" {
		t.Errorf("waiting: %q", got)
	}

	if got := linkDetail(agent.Link{State: agent.LinkWaiting}, now); got != "for the only computer online" {
		t.Errorf("waiting for anyone: %q", got)
	}
}

func TestSince(t *testing.T) {
	now := time.Date(2026, 9, 25, 21, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		40 * time.Second:            "40s",
		12 * time.Minute:            "12m",
		3*time.Hour + 5*time.Minute: "3h05m",
		52 * time.Hour:              "2d4h",
	} {
		if got := since(now.Add(-d), now); got != want {
			t.Errorf("since(%v) = %q, want %q", d, got, want)
		}
	}
}
