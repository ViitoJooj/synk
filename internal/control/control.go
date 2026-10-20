// Package control is how the command line and the UI talk to a running agent:
// JSON, one message per line, over a unix socket on Linux and a named pipe on
// Windows. Both are reachable only by the user the agent runs as.
//
// It is separate from internal/ipc on purpose. That socket belongs to the
// injector, which can type on your behalf; this one belongs to the agent,
// which cannot. Nothing said here reaches the injector except through the
// agent's own link, so the boundary between "can ask to type" and "can
// change settings" stays where it was.
//
// A connection carries one request and its response, then closes. The one
// exception is "events", which stays open and sends the full status again on
// every change, which is what a UI redrawing live needs.
package control

import (
	"encoding/json"
	"errors"

	"github.com/ViitoJooj/synk/pkg/errorx"
)

// maxLine bounds a message. Requests are a verb and a few arguments; anything
// near this is a mistake or an attack, and is refused rather than buffered.
const maxLine = 1 << 20

// Request is one line from a client.
type Request struct {
	Verb string          `json:"verb"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Response is one line from the agent: the answer to a request, or, on an
// events connection, one event.
type Response struct {
	OK     bool            `json:"ok"`
	Event  string          `json:"event,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is an errorx.Error on the wire, so the command line prints the same
// code and hint whether the failure happened in it or in the agent.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Kind    string `json:"kind"`
	Hint    string `json:"hint,omitempty"`
	Cause   string `json:"cause,omitempty"`
}

func errorOf(err error) *Error {
	var e *errorx.Error
	if !errors.As(err, &e) {
		return &Error{Code: "snk-99", Message: err.Error(), Kind: string(errorx.Internal)}
	}
	out := &Error{Code: e.Code, Message: e.Msg, Kind: string(e.Kind), Hint: e.Hint}
	if e.Err != nil {
		out.Cause = e.Err.Error()
	}
	return out
}

func (e *Error) errorx() *errorx.Error {
	out := &errorx.Error{Code: e.Code, Msg: e.Message, Kind: errorx.Kind(e.Kind), Hint: e.Hint}
	if e.Cause != "" {
		out.Err = errors.New(e.Cause)
	}
	return out
}
