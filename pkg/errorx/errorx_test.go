package errorx

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPrintUsesTheDevxLayout(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, PeerNotFound("desk", []string{"laptop", "pc"}))

	want := "code: snk-00\n" +
		"error: no machine called 'desk' on your tailnet\n" +
		"error_type: user error.\n" +
		"message: known peers: laptop, pc. list the machines on your tailnet with 'synk ls'\n"
	if buf.String() != want {
		t.Errorf("got\n%s\nwant\n%s", buf.String(), want)
	}
}

func TestPrintShowsTheCause(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, Tailscale(errors.New("backend is Stopped")))

	if !strings.Contains(buf.String(), "cause: backend is Stopped\n") {
		t.Errorf("the underlying error is missing:\n%s", buf.String())
	}
}

func TestPrintTreatsAPlainErrorAsInternal(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, errors.New("boom"))

	if !strings.HasPrefix(buf.String(), "code: snk-99\nerror: boom\nerror_type: internal error.\n") {
		t.Errorf("got\n%s", buf.String())
	}
}

func TestErrorsUnwrap(t *testing.T) {
	cause := errors.New("cause")
	if !errors.Is(Service("start", cause), cause) {
		t.Error("errors.Is cannot see through *Error")
	}
}
