package main

// Pure layout geometry: how a Layout tree maps onto a rectangle of cells.
// No rendering or styling here — view.go consumes these shapes.

// Rect is an integer (x, y, w, h) — origin top-left, h and w in cells.
type Rect struct{ X, Y, W, H int }

// dividerSpec describes each separator a split contributes, in absolute coords.
type dividerSpec struct {
	x, y, length int
	vertical     bool
}

// layoutGeometry walks layout l, sized into (x, y, w, h), and returns a
// rectangle per leaf pane plus the divider run each split contributes.
// Dividers eat 1 row/col between siblings; ratios are clamped so neither
// child collapses to zero. Rects and dividers come out of the same walk so
// the split arithmetic can never disagree between the two.
func layoutGeometry(l *Layout, x, y, w, h int) (map[*Pane]Rect, []dividerSpec) {
	rects := map[*Pane]Rect{}
	var divs []dividerSpec
	var walk func(n *Layout, x, y, w, h int)
	walk = func(n *Layout, x, y, w, h int) {
		if n.IsLeaf() {
			rects[n.pane] = Rect{x, y, w, h}
			return
		}
		switch n.split {
		case splitV:
			// Reserve 1 column for the divider, then split the rest by ratio.
			avail := w - 1
			if avail < 2 {
				avail = 2
			}
			wa := int(float64(avail) * n.ratio)
			if wa < 1 {
				wa = 1
			}
			if wa > avail-1 {
				wa = avail - 1
			}
			divs = append(divs, dividerSpec{x: x + wa, y: y, length: h, vertical: true})
			walk(n.a, x, y, wa, h)
			walk(n.b, x+wa+1, y, avail-wa, h)
		case splitH:
			avail := h - 1
			if avail < 2 {
				avail = 2
			}
			ha := int(float64(avail) * n.ratio)
			if ha < 1 {
				ha = 1
			}
			if ha > avail-1 {
				ha = avail - 1
			}
			divs = append(divs, dividerSpec{x: x, y: y + ha, length: w, vertical: false})
			walk(n.a, x, y, w, ha)
			walk(n.b, x, y+ha+1, w, avail-ha)
		}
	}
	walk(l, x, y, w, h)
	return rects, divs
}

// paneHPad is the blank columns kept between a pane's content and a vertical
// divider it borders, so text doesn't sit flush against the line. Only sides
// abutting a vertical divider are inset — screen edges and horizontal dividers
// stay flush. The divider itself doesn't move, so junctions stay intact.
const paneHPad = 1

// contentRect insets r horizontally by paneHPad on each side that borders a
// vertical divider. It's what the pty is sized to and what the body/cursor
// render against; the full rect still drives dividers and hit-testing.
func contentRect(r Rect, divs []dividerSpec) Rect {
	if abutsVDivider(divs, r.X-1, r.Y, r.H) {
		r.X += paneHPad
		r.W -= paneHPad
	}
	if abutsVDivider(divs, r.X+r.W, r.Y, r.H) {
		r.W -= paneHPad
	}
	if r.W < 1 {
		r.W = 1
	}
	return r
}

// abutsVDivider reports whether a vertical divider occupies column col over any
// of rows [y, y+h).
func abutsVDivider(divs []dividerSpec, col, y, h int) bool {
	for _, d := range divs {
		if d.vertical && d.x == col && d.y < y+h && d.y+d.length > y {
			return true
		}
	}
	return false
}

// computeRects is layoutGeometry for callers that only need the pane rects
// (layout sizing, mouse hit-testing).
func computeRects(l *Layout, x, y, w, h int) map[*Pane]Rect {
	rects, _ := layoutGeometry(l, x, y, w, h)
	return rects
}

// geometry is the tab's visible pane geometry at origin (0,0): the full
// layout walk normally, or a single full-size rect for the active pane while
// zoomed. Every rect consumer (render, pty sizing, mouse hit-testing) goes
// through this so they can never disagree about what's on screen.
func (t *Tab) geometry(w, h int) (map[*Pane]Rect, []dividerSpec) {
	if t.zoomed && t.active != nil {
		return map[*Pane]Rect{t.active: {X: 0, Y: 0, W: w, H: h}}, nil
	}
	return layoutGeometry(t.root, 0, 0, w, h)
}

// paneInDir picks the pane to move focus to from active in one direction,
// using the same (axis, step) encoding as resize: splitV is the left/right
// axis, splitH up/down; step -1 travels left/up, +1 right/down. rects is the
// tab's current geometry.
//
// A neighbour qualifies only if it sits wholly on the requested side along the
// travel axis (its near edge is past active's far edge) — so a pane merely off
// to the side never counts as "above". Among those, one that overlaps active
// on the perpendicular axis and sits nearest along the travel axis wins — the
// pane visually adjacent across the shared edge — with ties broken toward the
// top-/left-most. With no overlapping neighbour (an L-shaped layout where
// nothing lines up) it falls back to the nearest qualifier by travel gap.
// Returns nil when nothing lies that way.
func paneInDir(rects map[*Pane]Rect, active *Pane, dir splitDir, step int) *Pane {
	a, ok := rects[active]
	if !ok {
		return nil
	}
	// axes projects a rect onto (travel, perpendicular) spans for the active
	// axis: travel is the one we move along, perpendicular the one edges share.
	horiz := dir == splitV
	axes := func(r Rect) (tLo, tHi, pLo, pHi int) {
		if horiz {
			return r.X, r.X + r.W, r.Y, r.Y + r.H
		}
		return r.Y, r.Y + r.H, r.X, r.X + r.W
	}
	atLo, atHi, apLo, apHi := axes(a)

	var best, fallback *Pane
	bestGap, bestPerp, fbGap := 1<<30, 1<<30, 1<<30
	for p, r := range rects {
		if p == active {
			continue
		}
		tLo, tHi, pLo, pHi := axes(r)
		var gap int
		if step > 0 {
			if tLo < atHi { // not past active's far edge
				continue
			}
			gap = tLo - atHi
		} else {
			if tHi > atLo {
				continue
			}
			gap = atLo - tHi
		}
		if min(apHi, pHi) > max(apLo, pLo) { // perpendicular overlap
			if gap < bestGap || (gap == bestGap && pLo < bestPerp) {
				bestGap, bestPerp, best = gap, pLo, p
			}
		} else if gap < fbGap {
			fbGap, fallback = gap, p
		}
	}
	if best != nil {
		return best
	}
	return fallback
}

// ---- divider junctions ----

type cellPos struct{ x, y int }

// Connection arms of a divider cell. A plain vertical run is up|down, a
// plain horizontal run is left|right; junction cells carry three or four.
const (
	armUp uint8 = 1 << iota
	armDown
	armLeft
	armRight
)

// dividerArms maps every divider cell to its connection bitmask. Base pass:
// each span marks its cells with its own axis' arms. Junction pass: a cell
// gains an arm toward any neighboring divider cell whose own arm points back
// at it — that's exactly the abutment a T-junction is. Dividers in a binary
// split tree never cross (each is contained in its subtree's rect), so the
// only junctions are Ts, plus a ┼ when two spans abut one cell from opposite
// sides.
func dividerArms(divs []dividerSpec) map[cellPos]uint8 {
	base := make(map[cellPos]uint8)
	for _, d := range divs {
		if d.vertical {
			for i := 0; i < d.length; i++ {
				base[cellPos{d.x, d.y + i}] |= armUp | armDown
			}
		} else {
			for i := 0; i < d.length; i++ {
				base[cellPos{d.x + i, d.y}] |= armLeft | armRight
			}
		}
	}
	// Neighbor lookups read base, writes go to out — junction arms must not
	// cascade into further junctions.
	out := make(map[cellPos]uint8, len(base))
	for c, a := range base {
		if n, ok := base[cellPos{c.x, c.y - 1}]; ok && n&armDown != 0 {
			a |= armUp
		}
		if n, ok := base[cellPos{c.x, c.y + 1}]; ok && n&armUp != 0 {
			a |= armDown
		}
		if n, ok := base[cellPos{c.x - 1, c.y}]; ok && n&armRight != 0 {
			a |= armLeft
		}
		if n, ok := base[cellPos{c.x + 1, c.y}]; ok && n&armLeft != 0 {
			a |= armRight
		}
		out[c] = a
	}
	return out
}

// dividerRune picks the box-drawing rune for a cell's connection arms.
func dividerRune(a uint8) rune {
	switch a {
	case armUp | armDown:
		return '│'
	case armLeft | armRight:
		return '─'
	case armUp | armDown | armLeft:
		return '┤'
	case armUp | armDown | armRight:
		return '├'
	case armLeft | armRight | armUp:
		return '┴'
	case armLeft | armRight | armDown:
		return '┬'
	case armUp | armDown | armLeft | armRight:
		return '┼'
	}
	// Unreachable for spans produced by layoutGeometry; fall back by axis.
	if a&(armUp|armDown) != 0 {
		return '│'
	}
	return '─'
}
