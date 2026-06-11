package main

import "testing"

// Tree: vertical root split; right child split horizontally.
//
//	┌────────┬────────┐
//	│        │   p2   │
//	│   p1   ├────────┤   ← the T-junction under test
//	│        │   p3   │
//	└────────┴────────┘
func TestDividerArmsTJunction(t *testing.T) {
	p1, p2, p3 := &Pane{}, &Pane{}, &Pane{}
	right := &Layout{split: splitH, ratio: 0.5}
	right.a = &Layout{parent: right, pane: p2}
	right.b = &Layout{parent: right, pane: p3}
	root := &Layout{split: splitV, ratio: 0.5}
	root.a = &Layout{parent: root, pane: p1}
	root.b = right
	right.parent = root

	const w, h = 80, 24
	rects, divs := layoutGeometry(root, 0, 0, w, h)
	if len(divs) != 2 {
		t.Fatalf("want 2 dividers, got %d", len(divs))
	}
	arms := dividerArms(divs)

	// Geometry per layoutGeometry's math: vertical divider at x=39 spanning
	// the full height; horizontal divider at y=11 spanning the right half.
	vx := 39
	hy := 11
	if r := rects[p1]; r.W != vx {
		t.Fatalf("p1 width: want %d, got %d (rect %+v)", vx, r.W, r)
	}

	if got := dividerRune(arms[cellPos{vx, hy}]); got != '├' {
		t.Errorf("junction cell (%d,%d): want ├, got %c", vx, hy, got)
	}
	if got := dividerRune(arms[cellPos{vx, 0}]); got != '│' {
		t.Errorf("plain vertical cell: want │, got %c", got)
	}
	if got := dividerRune(arms[cellPos{vx + 3, hy}]); got != '─' {
		t.Errorf("plain horizontal cell: want ─, got %c", got)
	}
	// The horizontal divider's first cell is adjacent to the junction but is
	// itself a plain run cell.
	if got := dividerRune(arms[cellPos{vx + 1, hy}]); got != '─' {
		t.Errorf("horizontal cell next to junction: want ─, got %c", got)
	}
}

// Layout for directional-focus tests: p1 fills the left half; the right half
// stacks p2 over p3.
//
//	┌────────┬────────┐
//	│        │   p2   │
//	│   p1   ├────────┤
//	│        │   p3   │
//	└────────┴────────┘
func paneInDirLayout() (p1, p2, p3 *Pane, rects map[*Pane]Rect) {
	p1, p2, p3 = &Pane{}, &Pane{}, &Pane{}
	right := &Layout{split: splitH, ratio: 0.5}
	right.a = &Layout{parent: right, pane: p2}
	right.b = &Layout{parent: right, pane: p3}
	root := &Layout{split: splitV, ratio: 0.5}
	root.a = &Layout{parent: root, pane: p1}
	root.b = right
	right.parent = root
	rects, _ = layoutGeometry(root, 0, 0, 80, 24)
	return
}

func TestPaneInDir(t *testing.T) {
	p1, p2, p3, rects := paneInDirLayout()
	cases := []struct {
		name string
		from *Pane
		dir  splitDir
		step int
		want *Pane
	}{
		{"right from p1 picks upper neighbour", p1, splitV, +1, p2},
		{"left from p2 crosses to p1", p2, splitV, -1, p1},
		{"left from p3 crosses to p1", p3, splitV, -1, p1},
		{"down from p2 to p3", p2, splitH, +1, p3},
		{"up from p3 to p2", p3, splitH, -1, p2},
		{"left from p1 has no neighbour", p1, splitV, -1, nil},
		{"up from p1 has no neighbour", p1, splitH, -1, nil},
		{"down from p1 has no neighbour", p1, splitH, +1, nil},
	}
	for _, c := range cases {
		if got := paneInDir(rects, c.from, c.dir, c.step); got != c.want {
			t.Errorf("%s: got %p, want %p", c.name, got, c.want)
		}
	}
}

// Mirrored tree (left child split horizontally) produces ┤.
func TestDividerArmsTJunctionMirrored(t *testing.T) {
	p1, p2, p3 := &Pane{}, &Pane{}, &Pane{}
	left := &Layout{split: splitH, ratio: 0.5}
	left.a = &Layout{parent: left, pane: p1}
	left.b = &Layout{parent: left, pane: p2}
	root := &Layout{split: splitV, ratio: 0.5}
	root.b = &Layout{parent: root, pane: p3}
	root.a = left
	left.parent = root

	_, divs := layoutGeometry(root, 0, 0, 80, 24)
	arms := dividerArms(divs)

	if got := dividerRune(arms[cellPos{39, 11}]); got != '┤' {
		t.Errorf("junction cell: want ┤, got %c", got)
	}
}
