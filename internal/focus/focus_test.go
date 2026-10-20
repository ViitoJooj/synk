package focus

import (
	"testing"

	"github.com/ViitoJooj/synk/internal/proto"
)

// The real laptop panel: eDP-1, 1920x1080, scale 1, with the Windows monitor
// physically to the left.
func laptop(t *testing.T) *Machine {
	t.Helper()
	m, err := New(1920, 1080, proto.EdgeLeft)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func TestNewRejectsDegenerateScreen(t *testing.T) {
	for _, tc := range []struct{ w, h int }{{0, 1080}, {1920, 0}, {-1, -1}} {
		if _, err := New(tc.w, tc.h, proto.EdgeLeft); err == nil {
			t.Errorf("New(%d, %d) = nil error, want error", tc.w, tc.h)
		}
	}
}

func TestStartsLocalAtCentre(t *testing.T) {
	m := laptop(t)

	if m.Location() != Local {
		t.Errorf("Location() = %v, want Local", m.Location())
	}
	if x, y := m.Pos(); x != 960 || y != 540 {
		t.Errorf("Pos() = (%d, %d), want (960, 540)", x, y)
	}
}

func TestMoveWithinScreenDoesNotCross(t *testing.T) {
	m := laptop(t)

	crossed, _ := m.Move(100, -40)

	if crossed {
		t.Error("crossed = true, want false")
	}
	if x, y := m.Pos(); x != 1060 || y != 500 {
		t.Errorf("Pos() = (%d, %d), want (1060, 500)", x, y)
	}
}

// The Windows machine is to the left, so only the left edge hands off. Every
// other edge must clamp, exactly as a real cursor does.
func TestNonRemoteEdgesClamp(t *testing.T) {
	tests := []struct {
		name   string
		dx, dy int
		wantX  int
		wantY  int
	}{
		{"right edge clamps", 5000, 0, 1919, 540},
		{"top edge clamps", 0, -5000, 960, 0},
		{"bottom edge clamps", 0, 5000, 960, 1079},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := laptop(t)

			crossed, _ := m.Move(tt.dx, tt.dy)

			if crossed {
				t.Error("crossed = true, want false — that edge has no peer")
			}
			if x, y := m.Pos(); x != tt.wantX || y != tt.wantY {
				t.Errorf("Pos() = (%d, %d), want (%d, %d)", x, y, tt.wantX, tt.wantY)
			}
			if m.Location() != Local {
				t.Errorf("Location() = %v, want Local", m.Location())
			}
		})
	}
}

func TestCrossingRemoteEdgeHandsOff(t *testing.T) {
	m := laptop(t)

	crossed, _ := m.Move(-5000, 0)

	if !crossed {
		t.Fatal("crossed = false, want true")
	}
	if m.Location() != Remote {
		t.Errorf("Location() = %v, want Remote", m.Location())
	}
}

// The ratio tells the far side where along the shared edge to place its
// cursor, so screens of different heights line up.
func TestCrossingReportsRatioAlongEdge(t *testing.T) {
	tests := []struct {
		name      string
		startDY   int
		wantRatio uint16
	}{
		{"crossing at the top", -540, 0},
		{"crossing at the bottom", 539, 65535},
		{"crossing at the middle", 0, 32767},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := laptop(t)
			m.Move(0, tt.startDY)

			_, ratio := m.Move(-5000, 0)

			// One pixel of slack: the centre of a 1080-row screen is y=540,
			// which is half a pixel past the midpoint of 0..1079, so an exact
			// 32767 is the wrong thing to demand.
			const onePixel = 65535 / 1079
			if diff := int(ratio) - int(tt.wantRatio); diff > onePixel || diff < -onePixel {
				t.Errorf("ratio = %d, want %d ±%d", ratio, tt.wantRatio, onePixel)
			}
		})
	}
}

// While the far side owns input, our virtual cursor must stay frozen —
// otherwise it drifts and the return crossing lands in the wrong place.
func TestMoveIsIgnoredWhileRemote(t *testing.T) {
	m := laptop(t)
	m.Move(-5000, 0)
	beforeX, beforeY := m.Pos()

	crossed, _ := m.Move(500, 500)

	if crossed {
		t.Error("crossed = true, want false — already remote")
	}
	if x, y := m.Pos(); x != beforeX || y != beforeY {
		t.Errorf("Pos() = (%d, %d), want frozen at (%d, %d)", x, y, beforeX, beforeY)
	}
}

func TestEnterRestoresLocalFocus(t *testing.T) {
	m := laptop(t)
	m.Move(-5000, 0)

	m.Enter(proto.EdgeLeft, 32767)

	if m.Location() != Local {
		t.Fatalf("Location() = %v, want Local", m.Location())
	}
	if _, y := m.Pos(); y < 530 || y > 550 {
		t.Errorf("Pos().y = %d, want ~540 for ratio 32767", y)
	}
}

// Entering exactly on the boundary would re-trigger the crossing test on the
// very next delta and ping-pong the focus. Entry must land strictly inside.
func TestEnterLandsInsideSoItDoesNotImmediatelyRecross(t *testing.T) {
	m := laptop(t)
	m.Move(-5000, 0)

	m.Enter(proto.EdgeLeft, 0)

	x, _ := m.Pos()
	if x <= 0 {
		t.Fatalf("Pos().x = %d, want > 0 so the cursor is inside the screen", x)
	}
	if crossed, _ := m.Move(0, 1); crossed {
		t.Error("a pure vertical nudge re-crossed; entry position is on the edge")
	}
}

func TestEnterOnEachEdgePlacesCursorCorrectly(t *testing.T) {
	tests := []struct {
		edge   proto.Edge
		ratio  uint16
		checkX func(int) bool
		checkY func(int) bool
		desc   string
	}{
		{proto.EdgeLeft, 0, func(x int) bool { return x > 0 && x < 50 }, func(y int) bool { return y < 50 }, "left edge, top"},
		{proto.EdgeRight, 65535, func(x int) bool { return x > 1870 && x < 1920 }, func(y int) bool { return y > 1030 }, "right edge, bottom"},
		{proto.EdgeTop, 32767, func(x int) bool { return x > 940 && x < 980 }, func(y int) bool { return y > 0 && y < 50 }, "top edge, middle"},
		{proto.EdgeBottom, 32767, func(x int) bool { return x > 940 && x < 980 }, func(y int) bool { return y > 1030 && y < 1080 }, "bottom edge, middle"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			m := laptop(t)
			m.Move(-5000, 0)

			m.Enter(tt.edge, tt.ratio)

			x, y := m.Pos()
			if !tt.checkX(x) || !tt.checkY(y) {
				t.Errorf("Pos() = (%d, %d), outside the expected region for %s", x, y, tt.desc)
			}
		})
	}
}

// A full round trip must leave the machine usable, not wedged in some state.
func TestRoundTripAcrossAndBack(t *testing.T) {
	m := laptop(t)

	if crossed, _ := m.Move(-5000, 0); !crossed {
		t.Fatal("first crossing failed")
	}
	m.Enter(proto.EdgeLeft, 32767)
	if m.Location() != Local {
		t.Fatal("did not return to Local")
	}
	if crossed, _ := m.Move(-5000, 0); !crossed {
		t.Fatal("second crossing failed — machine wedged after a round trip")
	}
	if m.Location() != Remote {
		t.Errorf("Location() = %v, want Remote", m.Location())
	}
}

// On the machine that receives input, focus starts on the far side: the peer
// owns the physical keyboard and mouse until it hands them over. Without a way
// to say so, the receiver would think it had focus before anyone gave it any.
func TestLeaveMarksFocusRemote(t *testing.T) {
	m := laptop(t)

	m.Leave()

	if m.Location() != Remote {
		t.Errorf("Location() = %v, want Remote", m.Location())
	}
}

func TestLeaveThenEnterRoundTrips(t *testing.T) {
	m := laptop(t)
	m.Leave()

	// Deltas arriving before the handoff must be ignored, not integrated.
	if crossed, _ := m.Move(500, 500); crossed {
		t.Error("crossed while remote, want the delta ignored")
	}

	m.Enter(proto.EdgeLeft, 32767)
	if m.Location() != Local {
		t.Fatalf("Location() = %v, want Local after Enter", m.Location())
	}
	if crossed, _ := m.Move(-5000, 0); !crossed {
		t.Error("did not cross after being handed focus")
	}
}

// Calling Leave when focus is already remote must not corrupt anything.
func TestLeaveIsIdempotent(t *testing.T) {
	m := laptop(t)
	m.Leave()
	m.Leave()

	if m.Location() != Remote {
		t.Errorf("Location() = %v, want Remote", m.Location())
	}
}

// The session needs the configured edge back to tell the peer which side of
// its own screen the cursor is arriving on.
func TestRemoteEdgeIsReportedBack(t *testing.T) {
	for _, want := range []proto.Edge{proto.EdgeLeft, proto.EdgeRight, proto.EdgeTop, proto.EdgeBottom} {
		m, err := New(1920, 1080, want)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if got := m.RemoteEdge(); got != want {
			t.Errorf("RemoteEdge() = %v, want %v", got, want)
		}
	}
}

// The sending machine knows where its cursor really is — Windows will say —
// so it tests the edge directly instead of integrating deltas. Pure, and used
// from code that cannot run here.
func TestAtEdgeDetectsEachBorder(t *testing.T) {
	const w, h = 1920, 1080

	tests := []struct {
		name string
		x, y int
		edge proto.Edge
		want bool
	}{
		{"at the left edge", 0, 540, proto.EdgeLeft, true},
		{"one pixel in from the left", 1, 540, proto.EdgeLeft, false},
		{"at the right edge", 1919, 540, proto.EdgeRight, true},
		{"one pixel in from the right", 1918, 540, proto.EdgeRight, false},
		{"at the top edge", 960, 0, proto.EdgeTop, true},
		{"at the bottom edge", 960, 1079, proto.EdgeBottom, true},
		{"left edge does not fire on the right", 1919, 540, proto.EdgeLeft, false},
		{"beyond the edge still counts", -5, 540, proto.EdgeLeft, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := AtEdge(tt.x, tt.y, w, h, tt.edge)
			if got != tt.want {
				t.Errorf("AtEdge(%d, %d, %v) = %v, want %v", tt.x, tt.y, tt.edge, got, tt.want)
			}
		})
	}
}

// The ratio has to match what the receiving side expects, so the cursor lands
// at the same relative height on a screen of a different size.
func TestAtEdgeReportsPositionAlongTheBorder(t *testing.T) {
	const w, h = 1920, 1080

	if _, ratio := AtEdge(0, 0, w, h, proto.EdgeLeft); ratio != 0 {
		t.Errorf("top of the left edge = %d, want 0", ratio)
	}
	if _, ratio := AtEdge(0, h-1, w, h, proto.EdgeLeft); ratio != 65535 {
		t.Errorf("bottom of the left edge = %d, want 65535", ratio)
	}
	// A horizontal edge measures along x instead.
	if _, ratio := AtEdge(w-1, 0, w, h, proto.EdgeTop); ratio != 65535 {
		t.Errorf("right end of the top edge = %d, want 65535", ratio)
	}
}

// A ratio produced by AtEdge must place the cursor back where it left when fed
// to Enter, or the pointer jumps on every handoff.
func TestAtEdgeAndEnterAgreeOnPosition(t *testing.T) {
	const w, h = 1920, 1080
	for _, y := range []int{0, 270, 540, 810, 1079} {
		_, ratio := AtEdge(0, y, w, h, proto.EdgeLeft)

		m, err := New(w, h, proto.EdgeRight)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		m.Enter(proto.EdgeRight, ratio)

		if _, got := m.Pos(); got != y {
			t.Errorf("left at y=%d, arrived at y=%d", y, got)
		}
	}
}

// ---- re-entry, shared by both machines ----

// The invariant that stops focus ping-ponging between the two machines: a
// cursor placed back on this screen must not immediately satisfy the crossing
// test again.
//
// The Windows side hit exactly this. It released ClipCursor without moving the
// pointer, so the first movement afterwards reported a position still on the
// border, AtEdge fired, and control bounced straight back.
func TestEnterPointNeverLandsOnTheBorderItCameThrough(t *testing.T) {
	const w, h = 1920, 1080

	for _, edge := range []proto.Edge{proto.EdgeLeft, proto.EdgeRight, proto.EdgeTop, proto.EdgeBottom} {
		for _, ratio := range []uint16{0, 1, 32768, 65534, 65535} {
			x, y := EnterPoint(edge, ratio, w, h)

			if reached, _ := AtEdge(x, y, w, h, edge); reached {
				t.Errorf("EnterPoint(%s, %d) = (%d,%d), which AtEdge still calls the border",
					edge, ratio, x, y)
			}
			if x < 0 || x >= w || y < 0 || y >= h {
				t.Errorf("EnterPoint(%s, %d) = (%d,%d), outside a %dx%d screen",
					edge, ratio, x, y, w, h)
			}
		}
	}
}

// The ratio is what carries the cursor's position across machines of different
// resolutions, so it has to survive the trip in both directions.
func TestRatioSurvivesTheRoundTripThroughEnterPoint(t *testing.T) {
	const w, h = 1920, 1080

	for _, edge := range []proto.Edge{proto.EdgeLeft, proto.EdgeRight, proto.EdgeTop, proto.EdgeBottom} {
		for _, ratio := range []uint16{0, 16384, 32768, 65535} {
			x, y := EnterPoint(edge, ratio, w, h)
			got := RatioAlong(x, y, w, h, edge)

			// One pixel of rounding is inherent in mapping 0..65535 onto a
			// screen; a whole row of drift is not.
			const tolerance = 65535 / 1080
			if diff := int(got) - int(ratio); diff > tolerance || diff < -tolerance {
				t.Errorf("edge %s: ratio %d came back as %d", edge, ratio, got)
			}
		}
	}
}

// EnterPoint is the arithmetic Machine.Enter uses, so the two must not drift
// apart.
func TestMachineEnterUsesEnterPoint(t *testing.T) {
	const w, h = 1920, 1080

	for _, edge := range []proto.Edge{proto.EdgeLeft, proto.EdgeRight, proto.EdgeTop, proto.EdgeBottom} {
		m, err := New(w, h, proto.EdgeRight)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		m.Enter(edge, 20000)

		wantX, wantY := EnterPoint(edge, 20000, w, h)
		if x, y := m.Pos(); x != wantX || y != wantY {
			t.Errorf("edge %s: Enter put the cursor at (%d,%d), EnterPoint says (%d,%d)",
				edge, x, y, wantX, wantY)
		}
	}
}

// ---- placing the pointer with one relative move ----

// ParkDelta and EnterOffset are a pair: the first puts the pointer somewhere
// known, the second measures from there. If they disagree, every arriving
// cursor lands in the wrong place.
func TestParkThenEnterLandsOnTheEnterPoint(t *testing.T) {
	const w, h = 1920, 1080

	for _, edge := range []proto.Edge{proto.EdgeLeft, proto.EdgeRight, proto.EdgeTop, proto.EdgeBottom} {
		for _, ratio := range []uint16{0, 32768, 65535} {
			// Parking overshoots deliberately; the compositor clamps it, and
			// ParkPoint is where it comes to rest.
			px, py := ParkPoint(edge, w, h)
			dx, dy := EnterOffset(edge, ratio, w, h)

			wantX, wantY := EnterPoint(edge, ratio, w, h)
			if px+dx != wantX || py+dy != wantY {
				t.Errorf("edge %s ratio %d: park (%d,%d) + offset (%d,%d) = (%d,%d), want (%d,%d)",
					edge, ratio, px, py, dx, dy, px+dx, py+dy, wantX, wantY)
			}
		}
	}
}

// The park move has to overshoot in the right direction on both axes, or it
// clamps against the wrong corner.
func TestParkDeltaOvershootsTowardsItsCorner(t *testing.T) {
	const w, h = 1920, 1080

	for _, edge := range []proto.Edge{proto.EdgeLeft, proto.EdgeRight, proto.EdgeTop, proto.EdgeBottom} {
		dx, dy := ParkDelta(edge, w, h)
		px, py := ParkPoint(edge, w, h)

		// From the opposite corner — the worst case — the overshoot must still
		// carry past the target on both axes.
		fromX, fromY := w-1-px, h-1-py
		if got := clamp(fromX+dx, minCoord, w-1); got != px {
			t.Errorf("edge %s: x parked at %d, want %d", edge, got, px)
		}
		if got := clamp(fromY+dy, minCoord, h-1); got != py {
			t.Errorf("edge %s: y parked at %d, want %d", edge, got, py)
		}
	}
}

// The injection protocol carries a delta as an int16. An overshoot computed for
// a very wide desktop must not wrap round into a move in the opposite
// direction, which would fling the pointer to the far side instead.
func TestParkDeltaStaysWithinTheWireFormat(t *testing.T) {
	dx, dy := ParkDelta(proto.EdgeRight, 30000, 20000)

	if dx != int(int16(dx)) || dy != int(int16(dy)) {
		t.Errorf("ParkDelta = (%d,%d), which does not survive an int16", dx, dy)
	}
	if dx <= 0 || dy <= 0 {
		t.Errorf("ParkDelta = (%d,%d), want both positive towards the corner", dx, dy)
	}
}
