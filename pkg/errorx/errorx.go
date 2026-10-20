// Package errorx is how synk tells the user what went wrong: a stable code,
// what happened, whose fault it is, and what to do about it.
//
// The printed form is the one devxx uses, so the tools read alike. Unlike
// devxx these are returned rather than exiting on the spot: the command line
// prints them once, at the top, and everything below stays testable.
package errorx

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// Kind says whose move it is.
type Kind string

const (
	// User means the input was wrong: a typo, a name that does not exist.
	User Kind = "user error"
	// Environment means something outside synk is not ready: Tailscale down,
	// a service stopped, the peer asleep.
	Environment Kind = "environment error"
	// Internal means synk itself failed.
	Internal Kind = "internal error"
)

type Error struct {
	Code string
	Msg  string
	Kind Kind
	// Hint is the next thing to try. It is what the user reads first, so it
	// names a command whenever there is one.
	Hint string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

// Print writes err in the code/error/error_type/message form. An error that is
// not an *Error is printed as internal, with its own text as the message.
func Print(w io.Writer, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Code: "snk-99", Msg: err.Error(), Kind: Internal}
	}

	fmt.Fprintln(w, "code: "+e.Code)
	fmt.Fprintln(w, "error: "+e.Msg)
	fmt.Fprintln(w, "error_type: "+string(e.Kind)+".")
	if e.Err != nil {
		fmt.Fprintln(w, "cause: "+e.Err.Error())
	}
	if e.Hint != "" {
		fmt.Fprintln(w, "message: "+e.Hint)
	}
}

func PeerNotFound(name string, known []string) *Error {
	hint := "list the machines on your tailnet with 'synk ls'"
	if len(known) > 0 {
		hint = "known peers: " + strings.Join(known, ", ") + ". " + hint
	}
	return &Error{
		Code: "snk-00",
		Msg:  fmt.Sprintf("no machine called '%s' on your tailnet", name),
		Kind: User,
		Hint: hint,
	}
}

func PeerAmbiguous(target string, names []string) *Error {
	return &Error{
		Code: "snk-01",
		Msg:  fmt.Sprintf("more than one %s machine is online (%s)", target, strings.Join(names, ", ")),
		Kind: User,
		Hint: "name the one you want: synk " + names[0],
	}
}

func NoneOnline(target string) *Error {
	return &Error{
		Code: "snk-02",
		Msg:  fmt.Sprintf("no %s machine is online on your tailnet", target),
		Kind: Environment,
		Hint: "wake the other machine and check that Tailscale is running there, then see 'synk ls'",
	}
}

func Tailscale(err error) *Error {
	return &Error{
		Code: "snk-03",
		Msg:  "cannot read the tailnet",
		Kind: Environment,
		Hint: "check 'tailscale status'; if it is logged out, run 'sudo tailscale up'",
		Err:  err,
	}
}

func NoConfig(path string, err error) *Error {
	return &Error{
		Code: "snk-04",
		Msg:  "cannot use the config at " + path,
		Kind: User,
		Hint: "this machine receives input, so it needs a config naming the peer and the edge its screen sits beyond; 'synk doctor' says what is missing",
		Err:  err,
	}
}

func Service(action string, err error) *Error {
	return &Error{
		Code: "snk-05",
		Msg:  "could not " + action + " the agent",
		Kind: Environment,
		Hint: "see why with 'synk logs'",
		Err:  err,
	}
}

func NotAComputer(name, os string) *Error {
	return &Error{
		Code: "snk-06",
		Msg:  fmt.Sprintf("'%s' runs %s, which has no keyboard and mouse to share", name, os),
		Kind: User,
		Hint: "pick a Linux, Windows or macOS machine from 'synk ls'",
	}
}

func Failed(action string, err error) *Error {
	return &Error{Code: "snk-07", Msg: "could not " + action, Kind: Internal, Err: err}
}

// NotInstalled takes the installer to run, which differs by platform.
func NotInstalled(install string, err error) *Error {
	return &Error{
		Code: "snk-10",
		Msg:  "the agent is not installed on this machine",
		Kind: User,
		Hint: install,
		Err:  err,
	}
}

func NoConfigHere() *Error {
	return &Error{
		Code: "snk-08",
		Msg:  "this machine keeps no config",
		Kind: User,
		Hint: "the settings live on the Linux machine, which receives the input; run 'synk config' there. Here, 'synk <peer>' is all there is to choose",
	}
}

func BadSetting(err error) *Error {
	return &Error{
		Code: "snk-09",
		Msg:  "cannot change that setting",
		Kind: User,
		Hint: "see the keys and their values with 'synk config ls'",
		Err:  err,
	}
}

// Folder explains a refused change to the folders or blocks. The hint depends
// on what was refused, which err says through the folders package's errors.
func Folder(action, hint string, err error) *Error {
	return &Error{Code: "snk-13", Msg: "cannot " + action, Kind: User, Hint: hint, Err: err}
}

func NotSyncing() *Error {
	return &Error{
		Code: "snk-14",
		Msg:  "the agent is not running, so there is no sync status to show",
		Kind: Environment,
		Hint: "start it with 'synk up'; the folder list itself is in 'synk folder ls --offline'",
	}
}
