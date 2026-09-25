package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/ViitoJooj/synk/pkg/errorx"
)

func TestSuggestionForAMistypedCommand(t *testing.T) {
	err := withSuggestion(rootCmd, "stauts", errorx.PeerNotFound("stauts", []string{"pc"}))

	var e *errorx.Error
	if !errors.As(err, &e) || !strings.HasPrefix(e.Hint, "did you mean 'synk status'?") {
		t.Errorf("got %v", err)
	}
}

func TestNoSuggestionForARealLookingName(t *testing.T) {
	err := withSuggestion(rootCmd, "desktop-xyz", errorx.PeerNotFound("desktop-xyz", []string{"pc"}))

	var e *errorx.Error
	if errors.As(err, &e) && strings.Contains(e.Hint, "did you mean") {
		t.Errorf("suggested a command for a machine name: %s", e.Hint)
	}
}

// Only a name that fit no machine can be a typo; an ambiguous OS is not.
func TestNoSuggestionForOtherErrors(t *testing.T) {
	in := errorx.PeerAmbiguous("linux", []string{"a", "b"})
	err := withSuggestion(rootCmd, "linux", in)

	var e *errorx.Error
	if !errors.As(err, &e) || strings.Contains(e.Hint, "did you mean") {
		t.Errorf("got %v", err)
	}
}
