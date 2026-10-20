// Package focus decides which machine owns the pointer, by integrating
// relative mouse deltas against the local screen geometry.
//
// Wayland offers no way to ask where the cursor is, so we keep our own count.
// It stays honest because the compositor pins the real cursor at the border
// while the far side has focus, so both sides agree again on every handoff.
package focus

import (
	"fmt"

	"github.com/ViitoJooj/synk/internal/proto"
)

type Location uint8

const (
	Local Location = iota
	Remote
)

func (l Location) String() string {
	if l == Remote {
		return "Remote"
	}
	return "Local"
}

// insetPx is how far inside the screen a re-entering cursor is placed. Landing
// exactly on the border would satisfy the crossing test on the next delta and
// bounce the focus back and forth.
const insetPx = 2

type Machine struct {
	w, h       int
	remoteEdge proto.Edge
	x, y       int
	loc        Location
}

// New builds a machine for a screen of w by h logical pixels, with the peer
// attached at remoteEdge. Focus starts local, cursor at the centre.
func New(w, h int, remoteEdge proto.Edge) (*Machine, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("focus: screen must be positive, got %dx%d", w, h)
	}
	return &Machine{w: w, h: h, remoteEdge: remoteEdge, x: w / 2, y: h / 2}, nil
}

func (m *Machine) Location() Location { return m.loc }

func (m *Machine) Pos() (int, int) { return m.x, m.y }

// RemoteEdge is the border the peer sits beyond.
func (m *Machine) RemoteEdge() proto.Edge { return m.remoteEdge }

// Move integrates one relative delta. If the cursor leaves through the peer's
// edge it returns true plus the position along that edge as a 0..65535 ratio,
// and focus becomes Remote. Deltas arriving while Remote are ignored: the far
// side owns the pointer, and integrating them here would desynchronise us.
func (m *Machine) Move(dx, dy int) (bool, uint16) {
	if m.loc == Remote {
		return false, 0
	}

	nx, ny := m.x+dx, m.y+dy

	if m.crosses(nx, ny) {
		// Clamp the off-screen axis before measuring, so a diagonal exit
		// still reports a ratio that lies on the edge.
		m.loc = Remote
		return true, m.ratioAt(clamp(nx, 0, m.w-1), clamp(ny, 0, m.h-1))
	}

	m.x = clamp(nx, 0, m.w-1)
	m.y = clamp(ny, 0, m.h-1)
	return false, 0
}

// Leave marks focus as belonging to the peer.
//
// The machine that receives input starts here: the peer owns the physical
// keyboard and mouse until it hands them over, so a freshly connected receiver
// must not believe it already has focus. It is also the safe state to fall back
// to when a link drops mid-handoff.
func (m *Machine) Leave() { m.loc = Remote }

// Enter returns focus to this screen, placing the cursor just inside the given
// edge at the given ratio along it.
func (m *Machine) Enter(edge proto.Edge, ratio uint16) {
	m.loc = Local
	m.x, m.y = EnterPoint(edge, ratio, m.w, m.h)
}

// EnterPoint is where a cursor returning through edge at ratio belongs: just
// inside the border, at the matching position along it.
//
// The inset is the whole point. Landing exactly on the border satisfies the
// crossing test again on the very next movement, and focus ping-pongs between
// the two machines. Both sides need this — the one that integrates deltas and
// the one that can read its own cursor — so the arithmetic lives here once.
func EnterPoint(edge proto.Edge, ratio uint16, w, h int) (int, int) {
	switch edge {
	case proto.EdgeLeft:
		return insetPx, alongAxis(ratio, h)
	case proto.EdgeRight:
		return w - 1 - insetPx, alongAxis(ratio, h)
	case proto.EdgeTop:
		return alongAxis(ratio, w), insetPx
	default:
		return alongAxis(ratio, w), h - 1 - insetPx
	}
}

// minCoord is where a pointer flung past the top-left corner actually stops.
//
// Measured on KWin, not assumed: a delta large enough to leave the screen lands
// on 1, not 0, while the far side clamps to the last pixel as expected. The
// value matters because ParkPoint is what EnterOffset measures from, so being
// wrong about it would put every arriving cursor one pixel out.
const minCoord = 1

// ParkPoint is where a screen that has just lost focus leaves its pointer: the
// corner of the border the peer sits beyond.
//
// It cannot be hidden — no Wayland client may move or hide another's pointer —
// so the next best thing is to put it where it is not in the way. The corner of
// the crossing edge is the natural choice: it is the direction the user just
// left in, and it is out of the working area.
//
// It is also what makes an arriving cursor placeable at all. See ParkDelta.
func ParkPoint(edge proto.Edge, w, h int) (int, int) {
	switch edge {
	case proto.EdgeLeft:
		return minCoord, h - 1
	case proto.EdgeTop:
		return w - 1, minCoord
	default:
		return w - 1, h - 1
	}
}

// ParkDelta is a relative move certain to reach ParkPoint from anywhere on the
// screen, by overshooting the corner and letting the compositor clamp.
//
// It has to be ONE move, and that is the whole reason this pair of functions
// exists rather than a general "put the pointer here". The compositor
// accumulates relative motion and clamps the total, so two moves sent back to
// back behave as their sum: an overshoot of -3840 followed by a correction of
// +1919 does not visit the corner at all, it travels -1921 and pins the pointer
// against the opposite edge. That was a real bug — the cursor arrived on the
// far left of the screen every time — and a single move is what forecloses it.
func ParkDelta(edge proto.Edge, w, h int) (int, int) {
	dx, dy := over(w), over(h)
	switch edge {
	case proto.EdgeLeft:
		return -dx, dy
	case proto.EdgeTop:
		return dx, -dy
	default:
		return dx, dy
	}
}

// EnterOffset is the single relative move from the parked corner to where the
// pointer should reappear, for a screen taking focus back.
func EnterOffset(edge proto.Edge, ratio uint16, w, h int) (int, int) {
	px, py := ParkPoint(edge, w, h)
	x, y := EnterPoint(edge, ratio, w, h)
	return x - px, y - py
}

// over is a delta certain to cross an extent, capped so it survives the int16
// the injection protocol carries it in rather than wrapping into a move in the
// opposite direction.
func over(extent int) int {
	return min(2*extent, 32000)
}

// RatioAlong maps a cursor position onto the 0..65535 position along edge, the
// inverse of what EnterPoint consumes.
func RatioAlong(x, y, w, h int, edge proto.Edge) uint16 {
	if edge == proto.EdgeLeft || edge == proto.EdgeRight {
		return toRatio(y, h)
	}
	return toRatio(x, w)
}

func (m *Machine) crosses(nx, ny int) bool {
	switch m.remoteEdge {
	case proto.EdgeLeft:
		return nx < 0
	case proto.EdgeRight:
		return nx >= m.w
	case proto.EdgeTop:
		return ny < 0
	case proto.EdgeBottom:
		return ny >= m.h
	}
	return false
}

// ratioAt maps the coordinate that runs along the peer's edge into 0..65535.
func (m *Machine) ratioAt(x, y int) uint16 {
	switch m.remoteEdge {
	case proto.EdgeLeft, proto.EdgeRight:
		return toRatio(y, m.h)
	default:
		return toRatio(x, m.w)
	}
}

// toRatio maps 0..extent-1 onto 0..65535 so the far side can place its cursor
// proportionally, whatever its own resolution is.
func toRatio(v, extent int) uint16 {
	if extent <= 1 {
		return 0
	}
	return uint16(int64(clamp(v, 0, extent-1)) * 65535 / int64(extent-1))
}

// alongAxis is toRatio's inverse.
//
// It rounds rather than truncates: toRatio has already truncated once, and a
// second truncation here loses a whole pixel, so the cursor arrives one row
// above where it left on every single handoff.
func alongAxis(ratio uint16, extent int) int {
	if extent <= 1 {
		return 0
	}
	scaled := int64(ratio)*int64(extent-1) + 65535/2
	return clamp(int(scaled/65535), 0, extent-1)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// AtEdge reports whether a known cursor position has reached the border the
// peer sits beyond, and where along that border it is.
//
// This is the counterpart to Move, for the machine that can simply ask where
// its cursor is. Windows answers that question; Wayland does not, which is why
// both approaches exist.
func AtEdge(x, y, w, h int, e proto.Edge) (bool, uint16) {
	var reached bool
	switch e {
	case proto.EdgeLeft:
		reached = x <= 0
	case proto.EdgeRight:
		reached = x >= w-1
	case proto.EdgeTop:
		reached = y <= 0
	case proto.EdgeBottom:
		reached = y >= h-1
	}
	if !reached {
		return false, 0
	}

	// Measure along the border itself: a vertical border varies with y.
	if e == proto.EdgeLeft || e == proto.EdgeRight {
		return true, toRatio(y, h)
	}
	return true, toRatio(x, w)
}
