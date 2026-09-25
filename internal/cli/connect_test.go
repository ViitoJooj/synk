package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/ViitoJooj/synk/internal/transport"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

func codeOf(t *testing.T, err error) *errorx.Error {
	t.Helper()
	var e *errorx.Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v, want an *errorx.Error", err)
	}
	return e
}

func TestTargetErrorSuggestsAMistypedCommand(t *testing.T) {
	err := targetError(rootCmd, "stauts", &transport.TargetError{
		Name: "stauts", Known: []string{"pc"}, Err: transport.ErrPeerNotFound,
	})

	e := codeOf(t, err)
	if e.Code != "snk-00" || !strings.HasPrefix(e.Hint, "did you mean 'synk status'?") {
		t.Errorf("got %s: %s", e.Code, e.Hint)
	}
}

func TestTargetErrorDoesNotSuggestForARealLookingName(t *testing.T) {
	err := targetError(rootCmd, "desktop-xyz", &transport.TargetError{
		Name: "desktop-xyz", Known: []string{"pc"}, Err: transport.ErrPeerNotFound,
	})

	if e := codeOf(t, err); strings.Contains(e.Hint, "did you mean") {
		t.Errorf("suggested a command for a machine name: %s", e.Hint)
	}
}

func TestTargetErrorByOS(t *testing.T) {
	ambiguous := targetError(rootCmd, "linux", &transport.TargetError{
		Name: "linux", ByOS: true, Matches: []string{"a", "b"}, Err: transport.ErrAmbiguous,
	})
	if e := codeOf(t, ambiguous); e.Code != "snk-01" || !strings.Contains(e.Hint, "synk a") {
		t.Errorf("ambiguous: %s: %s", e.Code, e.Hint)
	}

	none := targetError(rootCmd, "linux", &transport.TargetError{
		Name: "linux", ByOS: true, Err: transport.ErrPeerNotFound,
	})
	if e := codeOf(t, none); e.Code != "snk-02" {
		t.Errorf("none online: %s", e.Code)
	}
}

func TestTargetErrorFromTailscaleItself(t *testing.T) {
	err := targetError(rootCmd, "pc", transport.ErrNotLoggedIn)
	if e := codeOf(t, err); e.Code != "snk-03" || !errors.Is(err, transport.ErrNotLoggedIn) {
		t.Errorf("got %s", e.Code)
	}
}
