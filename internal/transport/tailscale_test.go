package transport

import (
	"errors"
	"strings"
	"testing"
)

// fakeRunner returns canned `tailscale status --json` output.
type fakeRunner struct {
	out []byte
	err error
}

func (f fakeRunner) Run(string, ...string) ([]byte, error) { return f.out, f.err }

const runningStatus = `{
  "BackendState": "Running",
  "Self": {
    "HostName": "laptop",
    "DNSName": "laptop.tail0000.ts.net.",
    "TailscaleIPs": ["fd7a:115c:a1e0::1", "100.101.102.103"],
    "Online": true
  },
  "Peer": {
    "nodekey:aaa": {
      "HostName": "DESKTOP-PC",
      "DNSName": "desktop-pc.tail0000.ts.net.",
      "TailscaleIPs": ["fd7a:115c:a1e0::2", "100.64.10.20"],
      "Online": true,
      "CurAddr": "10.0.0.5:41641",
      "Relay": ""
    },
    "nodekey:bbb": {
      "HostName": "phone",
      "DNSName": "phone.tail0000.ts.net.",
      "TailscaleIPs": ["100.64.10.30"],
      "Online": false,
      "CurAddr": "",
      "Relay": "sao"
    }
  }
}`

func TestSelfReturnsIPv4(t *testing.T) {
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(runningStatus)})

	got, err := ts.Self()
	if err != nil {
		t.Fatalf("Self: %v", err)
	}
	if got != "100.101.102.103" {
		t.Errorf("Self() = %q, want the 100.x IPv4", got)
	}
}

// This is the machine's actual state right now, so the error has to be
// actionable rather than a generic parse failure.
func TestNeedsLoginIsReportedDistinctly(t *testing.T) {
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(`{"BackendState":"NeedsLogin"}`)})

	_, err := ts.Self()

	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("err = %v, want ErrNotLoggedIn", err)
	}
	if !strings.Contains(err.Error(), "tailscale up") {
		t.Errorf("err = %q, want it to name the command that fixes this", err)
	}
}

func TestStoppedBackendIsReported(t *testing.T) {
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(`{"BackendState":"Stopped"}`)})

	if _, err := ts.Self(); err == nil {
		t.Fatal("err = nil, want an error when the backend is stopped")
	}
}

func TestPeerFoundByHostnameIgnoringCase(t *testing.T) {
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(runningStatus)})

	for _, name := range []string{"DESKTOP-PC", "desktop-pc", "Desktop-Pc"} {
		got, err := ts.Peer(name)
		if err != nil {
			t.Fatalf("Peer(%q): %v", name, err)
		}
		if got.IP != "100.64.10.20" {
			t.Errorf("Peer(%q).IP = %q, want 100.64.10.20", name, got.IP)
		}
	}
}

// MagicDNS names are how people actually refer to their machines.
func TestPeerFoundByDNSName(t *testing.T) {
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(runningStatus)})

	got, err := ts.Peer("desktop-pc.tail0000.ts.net")
	if err != nil {
		t.Fatalf("Peer: %v", err)
	}
	if got.IP != "100.64.10.20" {
		t.Errorf("IP = %q, want 100.64.10.20", got.IP)
	}
}

func TestPeerReportsOnlineState(t *testing.T) {
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(runningStatus)})

	desktop, _ := ts.Peer("DESKTOP-PC")
	if !desktop.Online {
		t.Error("DESKTOP-PC Online = false, want true")
	}
	phone, _ := ts.Peer("phone")
	if phone.Online {
		t.Error("phone Online = true, want false")
	}
}

// A relayed connection routes through a DERP server and adds tens of
// milliseconds — usable for audio, miserable for a mouse. The caller warns.
func TestPeerReportsDirectVersusRelayed(t *testing.T) {
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(runningStatus)})

	desktop, _ := ts.Peer("DESKTOP-PC")
	if !desktop.Direct {
		t.Error("DESKTOP-PC Direct = false, want true — CurAddr is set")
	}
	phone, _ := ts.Peer("phone")
	if phone.Direct {
		t.Error("phone Direct = true, want false — it is relayed via DERP")
	}
}

func TestUnknownPeerIsAnError(t *testing.T) {
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(runningStatus)})

	_, err := ts.Peer("no-such-machine")

	if !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("err = %v, want ErrPeerNotFound", err)
	}
	// The message should help the user spot a typo, using the MagicDNS label
	// they see in `tailscale status` rather than the raw hostname.
	if !strings.Contains(err.Error(), "desktop-pc") {
		t.Errorf("err = %q, want it to list the peers that do exist", err)
	}
}

// Real data from this tailnet: an iPhone reports HostName "localhost" while
// its MagicDNS name is phone-1. Listing raw hostnames tells the user
// their peer is called "localhost", which helps nobody.
func TestPeerListingPrefersTheMagicDNSLabel(t *testing.T) {
	const iosPeer = `{
      "BackendState": "Running",
      "Self": {"TailscaleIPs": ["100.1.1.1"]},
      "Peer": {"nodekey:x": {
        "HostName": "localhost",
        "DNSName": "phone-1.tail0000.ts.net.",
        "TailscaleIPs": ["100.85.60.50"]
      }}
    }`
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(iosPeer)})

	_, err := ts.Peer("no-such-machine")

	if err == nil {
		t.Fatal("err = nil, want ErrPeerNotFound")
	}
	if strings.Contains(err.Error(), "localhost") {
		t.Errorf("err = %q, want the MagicDNS label, not the raw hostname", err)
	}
	if !strings.Contains(err.Error(), "phone-1") {
		t.Errorf("err = %q, want it to name phone-1", err)
	}
}

// Peer.Host is what status output shows the user, so it should be the name
// they recognise.
func TestPeerHostIsTheMagicDNSLabel(t *testing.T) {
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(runningStatus)})

	got, err := ts.Peer("desktop-pc")
	if err != nil {
		t.Fatalf("Peer: %v", err)
	}
	if got.Host != "desktop-pc" {
		t.Errorf("Host = %q, want the MagicDNS label \"desktop-pc\"", got.Host)
	}
}

func TestMalformedJSONIsAnErrorNotAPanic(t *testing.T) {
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte("not json at all")})

	if _, err := ts.Self(); err == nil {
		t.Error("err = nil, want a parse error")
	}
}

// The bare Go error is "executable file not found", which on Windows surfaces
// as "the system cannot find the file specified" — indistinguishable from a
// missing config file unless we say what is actually missing.
func TestMissingTailscaleBinaryExplainsTheFix(t *testing.T) {
	for _, raw := range []string{
		`exec: "tailscale": executable file not found in $PATH`,
		`exec: "tailscale": executable file not found in %PATH%`,
		"The system cannot find the file specified.",
	} {
		ts := NewTailscaleWithRunner(fakeRunner{err: errors.New(raw)})

		_, err := ts.Self()

		if err == nil {
			t.Fatalf("%q: err = nil, want an error", raw)
		}
		if !strings.Contains(err.Error(), "Tailscale CLI") {
			t.Errorf("%q -> %q, want it to name the missing program", raw, err)
		}
		if !strings.Contains(err.Error(), "PATH") {
			t.Errorf("%q -> %q, want it to mention PATH as the fix", raw, err)
		}
	}
}

// A peer with only an IPv6 address cannot carry our IPv4 listener.
func TestPeerWithoutIPv4IsAnError(t *testing.T) {
	const v6Only = `{
      "BackendState": "Running",
      "Self": {"TailscaleIPs": ["100.1.1.1"]},
      "Peer": {"nodekey:x": {"HostName": "v6box", "TailscaleIPs": ["fd7a:115c:a1e0::9"], "Online": true}}
    }`
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(v6Only)})

	if _, err := ts.Peer("v6box"); err == nil {
		t.Error("err = nil, want an error for a peer with no IPv4")
	}
}

// ---- discovery for the machine that carries no configuration ----

// soleStatus mirrors this user's real tailnet: a laptop, a PC, and an iPhone.
// The phone is online but cannot run the link, and must not make the choice
// ambiguous.
const soleStatus = `{
  "BackendState": "Running",
  "Self": {
    "HostName": "DESKTOP-PC",
    "DNSName": "desktop-pc.tail0000.ts.net.",
    "TailscaleIPs": ["100.64.1.12"],
    "OS": "windows",
    "Online": true
  },
  "Peer": {
    "nodekey:aaa": {
      "HostName": "laptop",
      "DNSName": "laptop-1.tail0000.ts.net.",
      "TailscaleIPs": ["100.64.1.11"],
      "OS": "linux",
      "Online": true
    },
    "nodekey:bbb": {
      "HostName": "localhost",
      "DNSName": "phone-1.tail0000.ts.net.",
      "TailscaleIPs": ["100.85.60.50"],
      "OS": "iOS",
      "Online": true
    },
    "nodekey:ccc": {
      "HostName": "old-laptop",
      "DNSName": "old-laptop.tail0000.ts.net.",
      "TailscaleIPs": ["100.64.1.13"],
      "OS": "linux",
      "Online": false
    }
  }
}`

func TestSolePeerFindsTheOnlyComputerOnline(t *testing.T) {
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(soleStatus)})

	peer, err := ts.SolePeer()
	if err != nil {
		t.Fatalf("SolePeer: %v", err)
	}
	if peer.IP != "100.64.1.11" {
		t.Errorf("peer.IP = %q, want the laptop's", peer.IP)
	}
	// The MagicDNS label, not HostName: an iPhone on this very tailnet reports
	// HostName "localhost", which names nothing useful.
	if peer.Host != "laptop-1" {
		t.Errorf("peer.Host = %q, want laptop-1", peer.Host)
	}
}

func TestSolePeerRefusesToGuessBetweenTwoComputers(t *testing.T) {
	status := strings.Replace(soleStatus, `"OS": "iOS"`, `"OS": "linux"`, 1)
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(status)})

	_, err := ts.SolePeer()
	if err == nil {
		t.Fatal("SolePeer picked one of two computers; want it to refuse")
	}
	// Typing into the wrong computer is the failure this prevents, so the
	// message has to name the candidates and the way to choose.
	for _, want := range []string{"laptop-1", "phone-1", "--peer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
}

func TestSolePeerSaysWhenNothingIsOnline(t *testing.T) {
	status := strings.Replace(soleStatus, `"OS": "linux",
      "Online": true`, `"OS": "linux",
      "Online": false`, 1)
	ts := NewTailscaleWithRunner(fakeRunner{out: []byte(status)})

	_, err := ts.SolePeer()
	if !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("err = %v, want ErrPeerNotFound", err)
	}
	// The phone is online; saying so stops "nothing is online" reading as a
	// Tailscale fault when it is really "the PC is asleep".
	if !strings.Contains(err.Error(), "phone-1") {
		t.Errorf("err = %q, want it to say the phone was ignored", err)
	}
}
