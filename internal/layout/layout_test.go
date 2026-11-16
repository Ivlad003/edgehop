package layout

import "testing"

func TestEdgesSingleMonitor(t *testing.T) {
	d := NewDesktop([]Rect{{0, 0, 1920, 1080}})
	cases := []struct {
		x, y int
		want uint8
	}{
		{960, 540, 0},
		{0, 540, Left.Bit()},
		{-5, 540, Left.Bit()},
		{1919, 540, Right.Bit()},
		{2500, 540, Right.Bit()},
		{960, 0, Top.Bit()},
		{960, 1079, Bottom.Bit()},
		{0, 0, Left.Bit() | Top.Bit()},
		{1919, 1079, Right.Bit() | Bottom.Bit()},
	}
	for _, c := range cases {
		if got := d.Edges(c.x, c.y); got != c.want {
			t.Errorf("Edges(%d,%d) = %04b, want %04b", c.x, c.y, got, c.want)
		}
	}
}

func TestEdgesBetweenMonitorsAreNotOuter(t *testing.T) {
	// Two monitors side by side; the second one is shorter.
	d := NewDesktop([]Rect{{0, 0, 1920, 1080}, {1920, 0, 1280, 720}})
	if got := d.Edges(1919, 300); got != 0 {
		t.Errorf("inner border reported as edge: %04b", got)
	}
	// Below the second monitor the first monitor's right side is an outer edge.
	if got := d.Edges(1919, 900); got != Right.Bit() {
		t.Errorf("jagged right edge = %04b, want right", got)
	}
	if got := d.Edges(3199, 300); got != Right.Bit() {
		t.Errorf("far right edge = %04b, want right", got)
	}
	// The bottom of the short monitor is an outer edge.
	if got := d.Edges(2500, 719); got != Bottom.Bit() {
		t.Errorf("short monitor bottom = %04b, want bottom", got)
	}
	if d.Bounds != (Rect{0, 0, 3200, 1080}) {
		t.Errorf("bounds = %+v", d.Bounds)
	}
}

func TestNegativeOriginMonitor(t *testing.T) {
	d := NewDesktop([]Rect{{-1920, 0, 1920, 1080}, {0, 0, 1920, 1080}})
	if got := d.Edges(-1920, 500); got != Left.Bit() {
		t.Errorf("left edge of left monitor = %04b", got)
	}
	if got := d.Edges(0, 500); got != 0 {
		t.Errorf("inner border = %04b", got)
	}
	x, y := d.Entry(Left, 0.5, 2)
	if x != -1918 || y != 540 {
		t.Errorf("Entry(left) = %d,%d", x, y)
	}
}

func TestFracAndEntryRoundTrip(t *testing.T) {
	d := NewDesktop([]Rect{{0, 0, 1920, 1080}})
	f := d.Frac(Right, 1919, 270)
	x, y := EntryPoint(2560, 1440, Left, f, 2)
	if x != 2 {
		t.Errorf("entry x = %d, want 2", x)
	}
	if y < 358 || y > 362 { // 270/1079 * 1439 ≈ 360
		t.Errorf("entry y = %d, want about 360", y)
	}
	x, y = EntryPoint(2560, 1440, Right, 1, 2)
	if x != 2557 || y != 1439 {
		t.Errorf("entry right/bottom = %d,%d", x, y)
	}
	x, y = EntryPoint(800, 600, Top, 0, 2)
	if x != 0 || y != 2 {
		t.Errorf("entry top = %d,%d", x, y)
	}
	x, y = EntryPoint(800, 600, Bottom, 0.5, 2)
	if x != 400 || y != 597 {
		t.Errorf("entry bottom = %d,%d", x, y)
	}
}

func TestEntrySnapsToRealMonitor(t *testing.T) {
	// Entering from the right at the very bottom: the right-most monitor is
	// only 720 px tall, so the point must be clamped into it.
	d := NewDesktop([]Rect{{0, 0, 1920, 1080}, {1920, 0, 1280, 720}})
	x, y := d.Entry(Right, 1, 2)
	if x != 3197 || y != 719 {
		t.Errorf("Entry = %d,%d, want 3197,719", x, y)
	}
	if !d.covered(x, y) {
		t.Errorf("entry point is outside every monitor")
	}
}

func TestLayoutRowAndResolve(t *testing.T) {
	l := New()
	l.AddRow([]string{"a", "b", "c"})
	if l.Neighbor("a", Right) != "b" || l.Neighbor("b", Left) != "a" || l.Neighbor("c", Left) != "b" {
		t.Fatalf("row links wrong:\n%s", l.Describe())
	}
	if l.Neighbor("a", Left) != "" || l.Neighbor("c", Right) != "" {
		t.Fatalf("row ends must have no neighbours")
	}
	all := func(string) bool { return true }
	if got := l.Resolve("a", Right, all); got != "b" {
		t.Errorf("Resolve = %q", got)
	}
	// b is offline: skip over it.
	notB := func(n string) bool { return n != "b" }
	if got := l.Resolve("a", Right, notB); got != "c" {
		t.Errorf("Resolve skipping b = %q, want c", got)
	}
	if got := l.Resolve("c", Right, all); got != "" {
		t.Errorf("Resolve past the end = %q", got)
	}
	none := func(string) bool { return false }
	if got := l.Resolve("a", Right, none); got != "" {
		t.Errorf("Resolve with nothing available = %q", got)
	}
}

func TestResolveRingTerminates(t *testing.T) {
	l := New()
	l.AddRow([]string{"a", "b"})
	l.Link("b", Right, "a") // ring
	if got := l.Resolve("a", Right, func(n string) bool { return false }); got != "" {
		t.Errorf("ring Resolve = %q", got)
	}
}

func TestParseSide(t *testing.T) {
	for in, want := range map[string]Side{"left": Left, "Right": Right, " top ": Top, "down": Bottom} {
		got, err := ParseSide(in)
		if err != nil || got != want {
			t.Errorf("ParseSide(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseSide("diagonal"); err == nil {
		t.Errorf("expected an error")
	}
	if Left.Opposite() != Right || Top.Opposite() != Bottom || Bottom.Opposite() != Top {
		t.Errorf("Opposite is wrong")
	}
}

func TestCorner(t *testing.T) {
	// A wide monitor with a smaller one to its right, top edges aligned.
	d := NewDesktop([]Rect{{0, 0, 1920, 1080}, {1920, 0, 1280, 720}})
	cases := []struct {
		side       Side
		x, y       int
		dist, span int
	}{
		{Left, 0, 0, 0, 1080},
		{Left, 0, 30, 30, 1080},
		{Left, 0, 540, 539, 1080}, // past the middle the far corner is nearer
		{Left, 0, 1079, 0, 1080},
		{Left, -5, 1050, 29, 1080},     // beyond the edge counts as on it
		{Right, 3199, 700, 19, 720},    // the small monitor has its own corners
		{Right, 3199, 800, 0, 720},     // below it: clamped to its bottom corner
		{Top, 100, 0, 100, 1920},       //
		{Top, 1900, 0, 19, 1920},       // near the inner corner of the big monitor
		{Top, 1925, 0, 5, 1280},        // just across, on the small one
		{Bottom, 2560, 719, 639, 1280}, //
	}
	for _, c := range cases {
		dist, span := d.Corner(c.side, c.x, c.y)
		if dist != c.dist || span != c.span {
			t.Errorf("Corner(%s, %d,%d) = %d of %d, want %d of %d", c.side, c.x, c.y, dist, span, c.dist, c.span)
		}
	}
	if dist, span := (Desktop{}).Corner(Left, 0, 0); dist != 0 || span != 0 {
		t.Errorf("a desktop without monitors: %d of %d", dist, span)
	}
}
