// Package layout describes how screens are arranged next to each other and
// answers the two geometric questions the rest of the program needs:
// "is the cursor at an outer edge of this desktop?" and "where does the cursor
// appear when it enters a screen through a given side?".
package layout

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Side is one of the four sides of a screen.
type Side uint8

const (
	Left Side = iota
	Right
	Top
	Bottom
)

// Opposite returns the side facing s.
func (s Side) Opposite() Side {
	switch s {
	case Left:
		return Right
	case Right:
		return Left
	case Top:
		return Bottom
	default:
		return Top
	}
}

// Bit returns the bit used for s in an edge mask.
func (s Side) Bit() uint8 { return 1 << s }

func (s Side) String() string {
	switch s {
	case Left:
		return "left"
	case Right:
		return "right"
	case Top:
		return "top"
	case Bottom:
		return "bottom"
	}
	return "?"
}

// ParseSide accepts left/right/top/bottom (and up/down as aliases).
func ParseSide(v string) (Side, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "left":
		return Left, nil
	case "right":
		return Right, nil
	case "top", "up", "above":
		return Top, nil
	case "bottom", "down", "below":
		return Bottom, nil
	}
	return 0, fmt.Errorf("unknown side %q (use left, right, top or bottom)", v)
}

// FirstSide returns the first side present in mask.
func FirstSide(mask uint8) (Side, bool) {
	for s := Left; s <= Bottom; s++ {
		if mask&s.Bit() != 0 {
			return s, true
		}
	}
	return 0, false
}

// Rect is a rectangle in desktop pixels. X+W and Y+H are exclusive.
type Rect struct{ X, Y, W, H int }

func (r Rect) Right() int  { return r.X + r.W }
func (r Rect) Bottom() int { return r.Y + r.H }

// Contains reports whether the point lies inside r.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.Right() && y >= r.Y && y < r.Bottom()
}

// Desktop is the set of monitors of one computer.
type Desktop struct {
	Monitors []Rect
	Bounds   Rect
}

// NewDesktop builds a Desktop and its bounding box. Empty monitors are ignored.
func NewDesktop(mons []Rect) Desktop {
	d := Desktop{}
	for _, m := range mons {
		if m.W <= 0 || m.H <= 0 {
			continue
		}
		d.Monitors = append(d.Monitors, m)
	}
	if len(d.Monitors) == 0 {
		return d
	}
	minX, minY := d.Monitors[0].X, d.Monitors[0].Y
	maxX, maxY := d.Monitors[0].Right(), d.Monitors[0].Bottom()
	for _, m := range d.Monitors[1:] {
		minX, minY = min(minX, m.X), min(minY, m.Y)
		maxX, maxY = max(maxX, m.Right()), max(maxY, m.Bottom())
	}
	d.Bounds = Rect{minX, minY, maxX - minX, maxY - minY}
	return d
}

func (d Desktop) covered(x, y int) bool {
	for _, m := range d.Monitors {
		if m.Contains(x, y) {
			return true
		}
	}
	return false
}

// monitorNear returns the monitor containing the point, or the closest one.
func (d Desktop) monitorNear(x, y int) (Rect, bool) {
	if len(d.Monitors) == 0 {
		return Rect{}, false
	}
	best, bestDist := d.Monitors[0], math.MaxInt
	for _, m := range d.Monitors {
		if m.Contains(x, y) {
			return m, true
		}
		dx := max(m.X-x, 0, x-(m.Right()-1))
		dy := max(m.Y-y, 0, y-(m.Bottom()-1))
		if dist := dx*dx + dy*dy; dist < bestDist {
			best, bestDist = m, dist
		}
	}
	return best, true
}

// Edges returns the mask of outer edges the point is touching (or is beyond).
// An edge between two adjacent monitors is not an outer edge.
func (d Desktop) Edges(x, y int) uint8 {
	m, ok := d.monitorNear(x, y)
	if !ok {
		return 0
	}
	cx := clamp(x, m.X, m.Right()-1)
	cy := clamp(y, m.Y, m.Bottom()-1)
	var mask uint8
	if x <= m.X && !d.covered(m.X-1, cy) {
		mask |= Left.Bit()
	}
	if x >= m.Right()-1 && !d.covered(m.Right(), cy) {
		mask |= Right.Bit()
	}
	if y <= m.Y && !d.covered(cx, m.Y-1) {
		mask |= Top.Bit()
	}
	if y >= m.Bottom()-1 && !d.covered(cx, m.Bottom()) {
		mask |= Bottom.Bit()
	}
	return mask
}

// Frac returns how far along the given side the point is, from 0 to 1,
// measured against the bounding box.
func (d Desktop) Frac(side Side, x, y int) float64 {
	b := d.Bounds
	if side == Left || side == Right {
		return frac(y-b.Y, b.H)
	}
	return frac(x-b.X, b.W)
}

// Entry returns the point where the cursor appears when it enters the desktop
// through side at the given fraction along that side. inset moves the point
// a few pixels inside so that it does not immediately trigger the edge again.
func (d Desktop) Entry(side Side, f float64, inset int) (int, int) {
	b := d.Bounds
	if len(d.Monitors) == 0 {
		return 0, 0
	}
	horizontal := side == Left || side == Right
	var along int
	if horizontal {
		along = b.Y + unfrac(f, b.H)
	} else {
		along = b.X + unfrac(f, b.W)
	}
	// Pick the monitor that forms the outer edge on that side and is closest
	// to the requested position along the edge.
	var best Rect
	bestDist := math.MaxInt
	for _, m := range d.Monitors {
		var touches bool
		var lo, hi int
		switch side {
		case Left:
			touches, lo, hi = m.X == b.X, m.Y, m.Bottom()-1
		case Right:
			touches, lo, hi = m.Right() == b.Right(), m.Y, m.Bottom()-1
		case Top:
			touches, lo, hi = m.Y == b.Y, m.X, m.Right()-1
		default:
			touches, lo, hi = m.Bottom() == b.Bottom(), m.X, m.Right()-1
		}
		if !touches {
			continue
		}
		dist := max(lo-along, 0, along-hi)
		if dist < bestDist {
			best, bestDist = m, dist
		}
	}
	m := best
	insetX := min(inset, max(m.W-1, 0))
	insetY := min(inset, max(m.H-1, 0))
	switch side {
	case Left:
		return m.X + insetX, clamp(along, m.Y, m.Bottom()-1)
	case Right:
		return m.Right() - 1 - insetX, clamp(along, m.Y, m.Bottom()-1)
	case Top:
		return clamp(along, m.X, m.Right()-1), m.Y + insetY
	default:
		return clamp(along, m.X, m.Right()-1), m.Bottom() - 1 - insetY
	}
}

// EntryPoint is Entry for a single rectangular screen of w×h pixels at 0,0.
func EntryPoint(w, h int, side Side, f float64, inset int) (int, int) {
	return NewDesktop([]Rect{{0, 0, w, h}}).Entry(side, f, inset)
}

func frac(pos, size int) float64 {
	if size <= 1 {
		return 0.5
	}
	return float64(clamp(pos, 0, size-1)) / float64(size-1)
}

func unfrac(f float64, size int) int {
	if size <= 1 {
		return 0
	}
	if f < 0 {
		f = 0
	} else if f > 1 {
		f = 1
	}
	return int(math.Round(f * float64(size-1)))
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

// Layout is the graph of screens: for every screen, which screen (if any)
// lies beyond each of its four sides.
type Layout struct {
	nb map[string]*[4]string
}

// New returns an empty layout.
func New() *Layout { return &Layout{nb: map[string]*[4]string{}} }

// Add registers a screen without neighbours.
func (l *Layout) Add(name string) {
	if _, ok := l.nb[name]; !ok {
		l.nb[name] = &[4]string{}
	}
}

// Has reports whether the screen is part of the layout.
func (l *Layout) Has(name string) bool { _, ok := l.nb[name]; return ok }

// Link makes `to` the neighbour of `from` on the given side (one direction).
func (l *Layout) Link(from string, side Side, to string) {
	l.Add(from)
	l.Add(to)
	l.nb[from][side] = to
}

// LinkBoth links two screens in both directions.
func (l *Layout) LinkBoth(a string, side Side, b string) {
	l.Link(a, side, b)
	l.Link(b, side.Opposite(), a)
}

// AddRow links screens left to right.
func (l *Layout) AddRow(names []string) {
	for i, n := range names {
		l.Add(n)
		if i > 0 {
			l.LinkBoth(names[i-1], Right, n)
		}
	}
}

// Neighbor returns the screen beyond the given side, or "".
func (l *Layout) Neighbor(name string, side Side) string {
	if n, ok := l.nb[name]; ok {
		return n[side]
	}
	return ""
}

// Resolve walks from name in the direction of side and returns the first
// screen for which ok returns true, skipping screens that are unavailable
// (for example a computer that is switched off). It returns "" if there is none.
func (l *Layout) Resolve(name string, side Side, ok func(string) bool) string {
	cur := name
	for i := 0; i <= len(l.nb); i++ {
		next := l.Neighbor(cur, side)
		if next == "" || next == name {
			return ""
		}
		if ok(next) {
			return next
		}
		cur = next
	}
	return ""
}

// Names returns all screen names, sorted.
func (l *Layout) Names() []string {
	out := make([]string, 0, len(l.nb))
	for n := range l.nb {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Describe returns a human readable list of links.
func (l *Layout) Describe() string {
	var sb strings.Builder
	for _, n := range l.Names() {
		for s := Left; s <= Bottom; s++ {
			if to := l.nb[n][s]; to != "" {
				fmt.Fprintf(&sb, "  %s --%s--> %s\n", n, s, to)
			}
		}
	}
	return sb.String()
}
