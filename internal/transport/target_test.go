package transport

import (
	"errors"
	"testing"
)

// tailnetStatus has one of each case Target has to tell apart: a Windows PC
// online, a second Linux machine online beside this one, one offline, and a
// phone.
const tailnetStatus = `{
  "BackendState": "Running",
  "Self": {"HostName": "laptop", "DNSName": "laptop.tail0000.ts.net.", "TailscaleIPs": ["100.64.0.1"], "OS": "linux", "Online": true},
  "Peer": {
    "a": {"HostName": "DESKTOP-PC", "DNSName": "desktop-pc.tail0000.ts.net.", "TailscaleIPs": ["100.64.0.2"], "OS": "windows", "Online": true, "CurAddr": "10.0.0.5:41641"},
    "b": {"HostName": "server", "DNSName": "server.tail0000.ts.net.", "TailscaleIPs": ["100.64.0.3"], "OS": "linux", "Online": true},
    "c": {"HostName": "old-box", "DNSName": "old-box.tail0000.ts.net.", "TailscaleIPs": ["100.64.0.4"], "OS": "linux", "Online": false},
    "d": {"HostName": "localhost", "DNSName": "iphone.tail0000.ts.net.", "TailscaleIPs": ["100.64.0.5"], "OS": "iOS", "Online": true},
    "e": {"HostName": "mac", "DNSName": "mac.tail0000.ts.net.", "TailscaleIPs": ["100.64.0.6"], "OS": "macOS", "Online": true},
    "f": {"HostName": "mac-old", "DNSName": "mac-old.tail0000.ts.net.", "TailscaleIPs": ["100.64.0.7"], "OS": "macOS", "Online": true}
  }
}`

func fakeTailnet() *Tailscale {
	return NewTailscaleWithRunner(fakeRunner{out: []byte(tailnetStatus)})
}

func TestPeersAreSortedByName(t *testing.T) {
	peers, err := fakeTailnet().Peers()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range peers {
		names = append(names, p.Host)
	}
	want := []string{"desktop-pc", "iphone", "mac", "mac-old", "old-box", "server"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
}

func TestTargetByOSPicksTheOnlyOnlineMachine(t *testing.T) {
	p, err := fakeTailnet().Target("Windows")
	if err != nil {
		t.Fatal(err)
	}
	if p.Host != "desktop-pc" || p.IP != "100.64.0.2" {
		t.Errorf("got %+v, want desktop-pc", p)
	}
}

// Two Macs online: picking either would be a guess, and a wrong guess types
// into the wrong computer.
func TestTargetByOSRefusesToGuess(t *testing.T) {
	_, err := fakeTailnet().Target("macos")

	var te *TargetError
	if !errors.As(err, &te) || !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v, want an ambiguous TargetError", err)
	}
	if len(te.Matches) != 2 || te.Matches[0] != "mac" || te.Matches[1] != "mac-old" {
		t.Errorf("Matches = %v, want both Macs", te.Matches)
	}
}

// The offline Linux box must not count, or "linux" would look ambiguous while
// only one Linux machine can actually be reached.
func TestTargetByOSIgnoresOfflineMachines(t *testing.T) {
	p, err := fakeTailnet().Target("linux")
	if err != nil {
		t.Fatal(err)
	}
	if p.Host != "server" {
		t.Errorf("got %s, want server", p.Host)
	}
}

func TestTargetByNameWinsOverOS(t *testing.T) {
	// "mac" is both a machine's name and nearly an OS; the name must win even
	// though two macOS machines are online.
	p, err := fakeTailnet().Target("mac")
	if err != nil {
		t.Fatal(err)
	}
	if p.Host != "mac" {
		t.Errorf("got %s, want the machine called mac", p.Host)
	}
}

func TestTargetByNameFindsOfflineMachines(t *testing.T) {
	// Naming a machine that is asleep is legitimate: the link comes up when it
	// wakes.
	p, err := fakeTailnet().Target("old-box")
	if err != nil {
		t.Fatal(err)
	}
	if p.Online {
		t.Error("old-box reported online")
	}
}

func TestTargetUnknownNameListsWhatExists(t *testing.T) {
	_, err := fakeTailnet().Target("desk")

	var te *TargetError
	if !errors.As(err, &te) || !errors.Is(err, ErrPeerNotFound) || te.ByOS {
		t.Fatalf("err = %v, want a not-found TargetError for a name", err)
	}
	if len(te.Known) != 6 {
		t.Errorf("Known = %v, want every machine", te.Known)
	}
}

func TestTargetByOSWithNothingOnline(t *testing.T) {
	status := `{"BackendState":"Running","Self":{"TailscaleIPs":["100.64.0.1"]},"Peer":{
	  "a":{"HostName":"pc","DNSName":"pc.t.ts.net.","TailscaleIPs":["100.64.0.2"],"OS":"windows","Online":false}}}`
	_, err := NewTailscaleWithRunner(fakeRunner{out: []byte(status)}).Target("windows")

	var te *TargetError
	if !errors.As(err, &te) || !te.ByOS || !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("err = %v, want a not-found TargetError by OS", err)
	}
}

func TestPhonesCannotRunTheLink(t *testing.T) {
	p, err := fakeTailnet().Target("iphone")
	if err != nil {
		t.Fatal(err)
	}
	if p.CanRunTheLink() {
		t.Error("an iPhone was accepted as a machine to link with")
	}
}

// A config written by hand may use either name, and ls has to mark the machine
// the same way the agent finds it.
func TestSameHostAcceptsEverySpellingPeerDoes(t *testing.T) {
	p, err := fakeTailnet().Target("desktop-pc")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"desktop-pc", "DESKTOP-PC", "desktop-pc.tail0000.ts.net.", "desktop-pc.tail0000.ts.net"} {
		if !SameHost(p, name) {
			t.Errorf("SameHost(%q) = false", name)
		}
	}
	if SameHost(p, "desktop") {
		t.Error("a prefix of the name matched")
	}
}
