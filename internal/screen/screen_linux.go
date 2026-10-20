//go:build linux

// Package screen reads the desktop's geometry from KWin.
//
// Wayland gives a background process no way to ask where the cursor is, so
// conn integrates pointer deltas against these bounds instead. That makes
// the numbers here load-bearing: get the desktop size wrong and the handoff
// edge lands in the middle of a monitor.
package screen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var ErrUnsupportedScale = errors.New("screen: fractional scaling is not supported")

// Geometry is the bounding box of every enabled output — the one desktop the
// cursor actually moves across, however many panels make it up.
type Geometry struct {
	OriginX, OriginY int
	Width, Height    int
	Outputs          int
}

// Runner executes kscreen-doctor, injected so tests need no compositor.
type Runner interface {
	Run(name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

type Reader struct{ runner Runner }

func New() *Reader { return &Reader{runner: execRunner{}} }

func NewWithRunner(r Runner) *Reader { return &Reader{runner: r} }

type kscreenOutput struct {
	Name      string  `json:"name"`
	Enabled   bool    `json:"enabled"`
	Connected bool    `json:"connected"`
	Scale     float64 `json:"scale"`
	Pos       struct {
		X int `json:"x"`
		Y int `json:"y"`
	} `json:"pos"`
	Size struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"size"`
}

type kscreenReply struct {
	Outputs []kscreenOutput `json:"outputs"`
}

func (r *Reader) Read() (Geometry, error) {
	// Checked before running anything: without the session environment
	// kscreen-doctor aborts and dumps core, and "signal: aborted" names
	// neither the cause nor the cure.
	if err := sessionEnvError(
		os.Getenv("XDG_RUNTIME_DIR"),
		os.Getenv("WAYLAND_DISPLAY"),
		os.Getenv("DISPLAY"),
	); err != nil {
		return Geometry{}, err
	}

	out, err := r.runner.Run("kscreen-doctor", "--json")
	if err != nil {
		if strings.Contains(err.Error(), "signal:") {
			return Geometry{}, fmt.Errorf(
				"screen: kscreen-doctor crashed (%v). It needs a live graphical "+
					"session; this usually means the process was started without one", err)
		}
		return Geometry{}, fmt.Errorf("screen: running kscreen-doctor: %w", err)
	}

	var reply kscreenReply
	// A stream decoder rather than Unmarshal: `kscreen-doctor -o --json`
	// prints the JSON and then a block of coloured human-readable text, and
	// Unmarshal rejects the whole thing as "extra data".
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&reply); err != nil {
		return Geometry{}, fmt.Errorf("screen: parsing kscreen-doctor output: %w", err)
	}

	return unionOf(reply.Outputs)
}

// sessionEnvError reports a environment that cannot reach a compositor.
//
// The failure this prevents is nasty to diagnose from the outside: a shell from
// `su -` looks perfectly normal, but has dropped XDG_RUNTIME_DIR, so Qt cannot
// open the Wayland socket and kscreen-doctor aborts.
func sessionEnvError(runtimeDir, waylandDisplay, x11Display string) error {
	const fix = "run synk from your graphical session — log out and back " +
		"in rather than using `su`, which drops these variables"

	if runtimeDir == "" {
		return fmt.Errorf("screen: XDG_RUNTIME_DIR is not set, so there is no compositor socket to reach; %s", fix)
	}
	if waylandDisplay == "" && x11Display == "" {
		return fmt.Errorf("screen: neither WAYLAND_DISPLAY nor DISPLAY is set, so there is no graphical session; %s", fix)
	}
	return nil
}

// unionOf reduces the enabled outputs to the desktop's bounding box.
func unionOf(outputs []kscreenOutput) (Geometry, error) {
	var (
		g     Geometry
		minX  = 1 << 30
		minY  = 1 << 30
		maxX  = -(1 << 30)
		maxY  = -(1 << 30)
		names []string
	)

	for _, o := range outputs {
		if !o.Enabled || !o.Connected {
			continue
		}
		// Fractional scaling makes logical and device pixels diverge, and
		// every coordinate here would be quietly off. Better to refuse.
		if o.Scale != 0 && o.Scale != 1 {
			return Geometry{}, fmt.Errorf("%w: output %s is at scale %g", ErrUnsupportedScale, o.Name, o.Scale)
		}

		minX = min(minX, o.Pos.X)
		minY = min(minY, o.Pos.Y)
		maxX = max(maxX, o.Pos.X+o.Size.Width)
		maxY = max(maxY, o.Pos.Y+o.Size.Height)
		g.Outputs++
		names = append(names, o.Name)
	}

	if g.Outputs == 0 {
		return Geometry{}, errors.New("screen: no enabled, connected output found")
	}

	g.OriginX, g.OriginY = minX, minY
	g.Width, g.Height = maxX-minX, maxY-minY
	return g, nil
}

// Describe renders the geometry for status output.
func (g Geometry) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%dx%d", g.Width, g.Height)
	if g.OriginX != 0 || g.OriginY != 0 {
		fmt.Fprintf(&b, " at (%d, %d)", g.OriginX, g.OriginY)
	}
	if g.Outputs > 1 {
		fmt.Fprintf(&b, " across %d outputs", g.Outputs)
	}
	return b.String()
}
