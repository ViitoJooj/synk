//go:build linux

package screen

import (
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	out []byte
	err error
}

func (f fakeRunner) Run(string, ...string) ([]byte, error) { return f.out, f.err }

// The shape kscreen-doctor actually emits on this machine.
const twoOutputs = `{
  "outputs": [
    {"name":"eDP-1","enabled":true,"connected":true,
     "pos":{"x":0,"y":0},"size":{"width":1920,"height":1080},"scale":1},
    {"name":"HDMI-A-1","enabled":true,"connected":true,
     "pos":{"x":1920,"y":0},"size":{"width":1920,"height":1080},"scale":1}
  ]
}`

const oneOutput = `{
  "outputs": [
    {"name":"eDP-1","enabled":true,"connected":true,
     "pos":{"x":0,"y":0},"size":{"width":1920,"height":1080},"scale":1}
  ]
}`

func TestSingleOutputGivesItsOwnSize(t *testing.T) {
	g, err := NewWithRunner(fakeRunner{out: []byte(oneOutput)}).Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if g.Width != 1920 || g.Height != 1080 {
		t.Errorf("got %dx%d, want 1920x1080", g.Width, g.Height)
	}
	if g.OriginX != 0 || g.OriginY != 0 {
		t.Errorf("origin = (%d, %d), want (0, 0)", g.OriginX, g.OriginY)
	}
}

// Two side-by-side monitors are one desktop as far as the cursor is concerned,
// so the crossing edge is the edge of the union, not of either panel.
func TestTwoOutputsGiveTheCombinedDesktop(t *testing.T) {
	g, err := NewWithRunner(fakeRunner{out: []byte(twoOutputs)}).Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if g.Width != 3840 || g.Height != 1080 {
		t.Errorf("got %dx%d, want the union 3840x1080", g.Width, g.Height)
	}
	if g.Outputs != 2 {
		t.Errorf("Outputs = %d, want 2", g.Outputs)
	}
}

// A monitor placed left of the laptop gives the desktop a negative origin, and
// the crossing test has to use that rather than assume zero.
func TestNegativeOriginIsPreserved(t *testing.T) {
	const leftOfLaptop = `{"outputs":[
	  {"name":"HDMI-A-1","enabled":true,"connected":true,
	   "pos":{"x":-1920,"y":0},"size":{"width":1920,"height":1080},"scale":1},
	  {"name":"eDP-1","enabled":true,"connected":true,
	   "pos":{"x":0,"y":0},"size":{"width":1920,"height":1080},"scale":1}
	]}`

	g, err := NewWithRunner(fakeRunner{out: []byte(leftOfLaptop)}).Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if g.OriginX != -1920 {
		t.Errorf("OriginX = %d, want -1920", g.OriginX)
	}
	if g.Width != 3840 {
		t.Errorf("Width = %d, want 3840", g.Width)
	}
}

func TestDisabledAndDisconnectedOutputsAreIgnored(t *testing.T) {
	const withGhosts = `{"outputs":[
	  {"name":"eDP-1","enabled":true,"connected":true,
	   "pos":{"x":0,"y":0},"size":{"width":1920,"height":1080},"scale":1},
	  {"name":"DP-2","enabled":false,"connected":true,
	   "pos":{"x":1920,"y":0},"size":{"width":3840,"height":2160},"scale":1},
	  {"name":"HDMI-A-2","enabled":true,"connected":false,
	   "pos":{"x":0,"y":1080},"size":{"width":1920,"height":1080},"scale":1}
	]}`

	g, err := NewWithRunner(fakeRunner{out: []byte(withGhosts)}).Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if g.Width != 1920 || g.Height != 1080 {
		t.Errorf("got %dx%d, want only the enabled, connected panel", g.Width, g.Height)
	}
}

// `kscreen-doctor -o --json` prints the JSON and then a block of coloured
// human-readable text. Decoding only the first value survives that, so the
// reader is not broken by whichever invocation a caller reaches for.
func TestTrailingHumanReadableOutputIsTolerated(t *testing.T) {
	noisy := oneOutput + "\n\x1b[01;32mOutput: \x1b[0;0m1 eDP-1\n\tenabled\n"

	g, err := NewWithRunner(fakeRunner{out: []byte(noisy)}).Read()
	if err != nil {
		t.Fatalf("Read with trailing text: %v", err)
	}
	if g.Width != 1920 {
		t.Errorf("Width = %d, want 1920", g.Width)
	}
}

// Fractional scaling would put the cursor in the wrong place, and silently
// being slightly wrong is worse than saying so.
func TestFractionalScaleIsReported(t *testing.T) {
	const scaled = `{"outputs":[
	  {"name":"eDP-1","enabled":true,"connected":true,
	   "pos":{"x":0,"y":0},"size":{"width":1920,"height":1080},"scale":1.5}
	]}`

	_, err := NewWithRunner(fakeRunner{out: []byte(scaled)}).Read()

	if !errors.Is(err, ErrUnsupportedScale) {
		t.Fatalf("err = %v, want ErrUnsupportedScale", err)
	}
	if !strings.Contains(err.Error(), "1.5") {
		t.Errorf("err = %q, want it to name the scale it found", err)
	}
}

func TestNoEnabledOutputsIsAnError(t *testing.T) {
	_, err := NewWithRunner(fakeRunner{out: []byte(`{"outputs":[]}`)}).Read()

	if err == nil {
		t.Fatal("err = nil, want an error when nothing is enabled")
	}
}

func TestMissingKscreenDoctorNamesIt(t *testing.T) {
	r := fakeRunner{err: errors.New(`exec: "kscreen-doctor": executable file not found in $PATH`)}

	_, err := NewWithRunner(r).Read()

	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if !strings.Contains(err.Error(), "kscreen-doctor") {
		t.Errorf("err = %q, want it to name kscreen-doctor", err)
	}
}

// ---- against the real compositor ----

func TestIntegrationReadsThisMachinesScreens(t *testing.T) {
	g, err := New().Read()
	if err != nil {
		t.Skipf("no usable kscreen-doctor here: %v", err)
	}
	t.Logf("desktop: %dx%d at (%d, %d) across %d output(s)",
		g.Width, g.Height, g.OriginX, g.OriginY, g.Outputs)

	if g.Width <= 0 || g.Height <= 0 {
		t.Errorf("got %dx%d, want a positive desktop", g.Width, g.Height)
	}
}

// Running without the graphical session's environment makes kscreen-doctor
// abort with SIGABRT and dump core. "signal: aborted (core dumped)" tells a
// user nothing about what is actually wrong, so the situation is caught before
// the command is ever run.
func TestSessionEnvErrorNamesWhatIsMissing(t *testing.T) {
	tests := []struct {
		name                     string
		runtimeDir, wayland, x11 string
		wantErr                  bool
		wantMentions             string
	}{
		{"complete wayland session", "/run/user/1000", "wayland-0", ":1", false, ""},
		{"complete X11 session", "/run/user/1000", "", ":0", false, ""},
		{"no runtime dir", "", "wayland-0", ":1", true, "XDG_RUNTIME_DIR"},
		{"no display at all", "/run/user/1000", "", "", true, "WAYLAND_DISPLAY"},
		{"nothing set", "", "", "", true, "XDG_RUNTIME_DIR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := sessionEnvError(tt.runtimeDir, tt.wayland, tt.x11)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("err = nil, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantMentions) {
				t.Errorf("err = %q, want it to name %s", err, tt.wantMentions)
			}
			// The user needs to know what to actually do about it.
			if !strings.Contains(err.Error(), "log out") {
				t.Errorf("err = %q, want it to say logging back in is the fix", err)
			}
		})
	}
}

// A command killed by a signal is a crash, not a failed run, and deserves a
// different explanation.
func TestReadReportsACrashAsAnEnvironmentProblem(t *testing.T) {
	r := NewWithRunner(fakeRunner{err: errors.New("signal: aborted (core dumped)")})

	_, err := r.Read()

	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if !strings.Contains(err.Error(), "crashed") {
		t.Errorf("err = %q, want it to say the command crashed", err)
	}
	if !strings.Contains(err.Error(), "graphical session") {
		t.Errorf("err = %q, want it to point at the session environment", err)
	}
}
