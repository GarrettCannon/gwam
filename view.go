package main

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

var (
	tabIdle = lipgloss.NewStyle().
		Padding(0, 1).
		Foreground(lipgloss.Color("245"))
	tabActive = lipgloss.NewStyle().
			Padding(0, 1).
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("63"))
	scrollChip = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("16")).
			Background(lipgloss.Color("214"))

	sessionChip = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("65"))
	prefixChip = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("16")).
			Background(lipgloss.Color("220"))
	prefixChipIdle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Background(lipgloss.Color("238"))

	paneDivider = lipgloss.NewStyle().
			Foreground(lipgloss.Color("238"))

	prefixPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("220")).
			Foreground(lipgloss.Color("252")).
			Padding(1, 2)
	prefixPanelTitle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("220"))
	prefixKey = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("220"))
	prefixArrow = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
	prefixHint = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)

// Rect is an integer (x, y, w, h) — origin top-left, h and w in cells.
type Rect struct{ X, Y, W, H int }

// chipRect is the screen-x range [start, end) of a single tab chip in the
// tab-bar row. Used by Update to route tab-bar clicks to actJumpTab.
type chipRect struct{ start, end int }

// tabBarLayout builds the styled tab-bar row and returns the screen-x range
// of each tab chip. The bar is right-aligned: tab chips, then session,
// then a PREFIX chip, with an optional SCROLL chip on the far left when the
// active pane is scrolled into history. Both render and the click handler
// call this so chip positions agree.
func tabBarLayout(m *Model) (bar string, chipRects []chipRect) {
	s := m.sessions[m.active]
	chips := make([]string, len(s.tabs))
	chipW := make([]int, len(s.tabs))
	for i, tt := range s.tabs {
		label := fmt.Sprintf("%d - %s", i+1, tt.Label())
		if i == s.active {
			chips[i] = tabActive.Render(label)
		} else {
			chips[i] = tabIdle.Render(label)
		}
		chipW[i] = lipgloss.Width(chips[i])
	}
	tabsStr := strings.Join(chips, "")
	sessionStr := sessionChip.Render(s.name)
	var prefixStr string
	if m.prefix {
		prefixStr = prefixChip.Render(" PREFIX C-A ")
	} else {
		prefixStr = prefixChipIdle.Render(" prefix C-A ")
	}
	rightBar := tabsStr + " " + sessionStr + " " + prefixStr
	var leading int
	if p := m.curPane(); p.scrollOff > 0 {
		max := p.vt.Scrollback().Len()
		scrollStr := scrollChip.Render(fmt.Sprintf(" SCROLL %d/%d ", p.scrollOff, max))
		rightBar = scrollStr + " " + rightBar
		// +1 for the space separator between scrollStr and the rest.
		leading = lipgloss.Width(scrollStr) + 1
	}
	rightW := lipgloss.Width(rightBar)
	gap := m.w - rightW
	if gap < 0 {
		gap = 0
	}
	bar = strings.Repeat(" ", gap) + rightBar

	chipRects = make([]chipRect, len(s.tabs))
	cursor := gap + leading
	for i, w := range chipW {
		chipRects[i] = chipRect{start: cursor, end: cursor + w}
		cursor += w
	}
	return bar, chipRects
}

// computeRects walks layout l, sized into (x, y, w, h), and returns a
// rectangle per leaf pane. Dividers eat 1 row/col between siblings; ratios are
// clamped so neither child collapses to zero.
func computeRects(l *Layout, x, y, w, h int) map[*Pane]Rect {
	out := map[*Pane]Rect{}
	var walk func(n *Layout, x, y, w, h int)
	walk = func(n *Layout, x, y, w, h int) {
		if n.IsLeaf() {
			out[n.pane] = Rect{x, y, w, h}
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
			wb := avail - wa
			walk(n.a, x, y, wa, h)
			walk(n.b, x+wa+1, y, wb, h)
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
			hb := avail - ha
			walk(n.a, x, y, w, ha)
			walk(n.b, x, y+ha+1, w, hb)
		}
	}
	walk(l, x, y, w, h)
	return out
}

// dividers describes each separator a split contributes, in absolute coords.
type dividerSpec struct {
	x, y, length int
	vertical     bool
}

// collectDividers returns the separator runs for a layout, sized into
// (x, y, w, h). These match the gaps left by computeRects.
func collectDividers(l *Layout, x, y, w, h int) []dividerSpec {
	var out []dividerSpec
	var walk func(n *Layout, x, y, w, h int)
	walk = func(n *Layout, x, y, w, h int) {
		if n.IsLeaf() {
			return
		}
		switch n.split {
		case splitV:
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
			out = append(out, dividerSpec{x: x + wa, y: y, length: h, vertical: true})
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
			out = append(out, dividerSpec{x: x, y: y + ha, length: w, vertical: false})
			walk(n.a, x, y, w, ha)
			walk(n.b, x, y+ha+1, w, avail-ha)
		}
	}
	walk(l, x, y, w, h)
	return out
}

func (m *Model) View() tea.View {
	if m.w == 0 || m.h == 0 || len(m.sessions) == 0 {
		return tea.NewView("")
	}

	t := m.curTab()
	tabBar, _ := tabBarLayout(m)

	inner := m.h - 1
	if inner < 1 {
		inner = 1
	}
	rects := computeRects(t.root, 0, 0, m.w, inner)
	body := renderBody(t, rects, m.w, inner)
	base := tabBar + "\n" + body

	// Overlay stack: bottom→top, each composed on top of the previous.
	// Each overlay's Render returns a styled block; Anchor.Place sizes it
	// against (m.w, m.h) using the block's own width/height.
	composed := base
	for _, ov := range m.overlays {
		panel := ov.Render(m)
		x, y := ov.Anchor().Place(m.w, m.h, lipgloss.Width(panel), lipgloss.Height(panel))
		composed = composeOverlay(composed, panel, x, y)
	}
	// Prefix cheatsheet sits on top of any overlays — it's a mode-driven
	// visual, not a popup. align the panel's right edge with the PREFIX
	// chip's right edge so the two visually anchor to the same column.
	if m.prefix {
		panel := renderPrefixPanel(m)
		px := m.w - lipgloss.Width(panel)
		if px < 0 {
			px = 0
		}
		composed = composeOverlay(composed, panel, px, 1)
	}

	v := tea.NewView(composed)
	v.AltScreen = true

	// surface the active pane's pty cursor at its absolute coordinates: the
	// pane's rect origin plus the emulator's cursor offset, plus 1 row for
	// the tab bar. when the shell hides the cursor (DECTCEM off), we're
	// scrolled into history, or an interactive overlay/prefix owns input,
	// leave v.Cursor nil — but give a CursorProvider overlay a chance to
	// paint its own cursor (top-down so the topmost wins).
	p := t.active
	if p.cursorVisible && p.scrollOff == 0 && !m.prefix && m.topInteractiveOverlay() == nil {
		if r, ok := rects[p]; ok {
			// Mirror renderPaneBody's source choice — while a sync block is
			// active the cursor would otherwise jitter across the frame-N+1
			// column-jumps that aren't yet visible. paneRenderSource also
			// handles timeout expiry, so checking syncFrozen here covers the
			// same window.
			var px, py int
			if p.syncFrozen && time.Since(p.syncStartedAt) < syncTimeout {
				px, py = p.syncCursorX, p.syncCursorY
			} else {
				pos := p.vt.CursorPosition()
				px, py = pos.X, pos.Y
			}
			c := tea.NewCursor(r.X+px, r.Y+py+1)
			// suppress bubbletea's DECSCUSR emission so the terminal keeps its
			// user-configured cursor shape. bubbletea only writes the style when
			// encodeCursorStyle(new) != encodeCursorStyle(old); on first render
			// lastView is nil so old encodes to 0. shape=-1, blink=false yields
			// (-1*2)+1+1 = 0, matching, so no DECSCUSR is ever written.
			c.Shape = tea.CursorShape(-1)
			c.Blink = false
			v.Cursor = c
		}
	}
	// If the pane cursor was suppressed, give a CursorProvider overlay a
	// chance to surface one (topmost wins). Most overlays paint a fake
	// reverse-video cursor inline and don't implement this.
	if v.Cursor == nil {
		for i := len(m.overlays) - 1; i >= 0; i-- {
			if cp, ok := m.overlays[i].(CursorProvider); ok {
				if c := cp.Cursor(m); c != nil {
					v.Cursor = c
					break
				}
			}
		}
	}

	return v
}

// renderBody composites the body area: a blank base of `inner` rows × w cols,
// each pane's content layered at its rect, dividers between siblings as
// further layers.
func renderBody(t *Tab, rects map[*Pane]Rect, w, inner int) string {
	// Base: solid blank canvas so the compositor's output has exact dimensions.
	blankRow := strings.Repeat(" ", w)
	baseRows := make([]string, inner)
	for i := range baseRows {
		baseRows[i] = blankRow
	}
	baseStr := strings.Join(baseRows, "\n")

	layers := []*lipgloss.Layer{lipgloss.NewLayer(baseStr).X(0).Y(0).Z(0)}

	// Dividers first (Z=1) so pane content (Z=2) overdraws any stray join cells.
	for _, d := range collectDividers(t.root, 0, 0, w, inner) {
		var s string
		if d.vertical {
			rows := make([]string, d.length)
			for i := range rows {
				rows[i] = "│"
			}
			s = paneDivider.Render(strings.Join(rows, "\n"))
		} else {
			s = paneDivider.Render(strings.Repeat("─", d.length))
		}
		layers = append(layers, lipgloss.NewLayer(s).X(d.x).Y(d.y).Z(1))
	}

	// Pane bodies.
	for pane, r := range rects {
		body := renderPaneBody(pane, r.W, r.H)
		layers = append(layers, lipgloss.NewLayer(body).X(r.X).Y(r.Y).Z(2))
	}

	return lipgloss.NewCompositor(layers...).Render()
}

// paneRenderSource returns the bytes renderPaneBody should treat as the
// pane's current screen. When the pane is inside a DECSET 2026 sync block
// (writeWithSync set syncFrozen) we return the snapshot captured at ?2026h —
// the in-flight live screen would show the app's frame-N+1 column-jumps
// overlaying frame-N's leftovers. If the freeze has aged past syncTimeout
// without a ?2026l, drop it so the next render reflects reality.
func paneRenderSource(p *Pane) string {
	if p.syncFrozen {
		if time.Since(p.syncStartedAt) < syncTimeout {
			return p.syncSnapshot
		}
		p.syncFrozen = false
		p.syncSnapshot = ""
	}
	return p.vt.Render()
}

// renderPaneBody returns exactly h rows × w cols of text for a single pane,
// using its scrollback when scrolled and the live screen otherwise.
func renderPaneBody(p *Pane, w, h int) string {
	if p.scrollOff > 0 {
		return renderScrollback(p, w, h)
	}
	raw := paneRenderSource(p)
	dlog("body", []byte(raw))
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	// Pad each line out to w cells so the layer covers its full rect — short
	// lines would otherwise leak through the base or dividers behind them.
	for i, ln := range lines {
		gap := w - lipgloss.Width(ln)
		if gap > 0 {
			lines[i] = ln + strings.Repeat(" ", gap)
		}
	}
	return strings.Join(lines, "\n")
}

// renderPrefixPanel builds the floating cheatsheet shown while prefix is
// armed. Single-column LazyVim/which-key layout: a cyan title row, then
// one "<keys> → <label>" line per (action, effective label) pair.
// Bindings that resolve to the same action+label collapse into one row —
// tab.jump's 1..9 nine bindings render as "1-9"; pane.resize's h/l/k/j
// stay four rows because each one overrides Label per direction.
func renderPrefixPanel(m *Model) string {
	type row struct {
		keys   []Key
		label  string
		group  string
		status func(*Model) string
	}
	// Dedupe by effective (group, label) — that's the visual identity of
	// the row. Two bindings with the same action and label collapse. We
	// skip direct bindings here: the prefix overlay's job is to show what
	// the prefix can do, and a direct binding is by definition not gated
	// on prefix. They still show up in `gwam config check` output.
	seen := map[string]*row{}
	var rows []*row
	var groupOrder []string
	groupSeen := map[string]bool{}
	for _, b := range defaultKeymap.bindings {
		if b.Trigger.Direct {
			continue
		}
		group := b.EffectiveGroup()
		label := b.EffectiveLabel()
		key := group + "\x00" + label
		if r, ok := seen[key]; ok {
			r.keys = append(r.keys, b.Trigger.Key)
			continue
		}
		if !groupSeen[group] {
			groupSeen[group] = true
			groupOrder = append(groupOrder, group)
		}
		r := &row{
			keys:   []Key{b.Trigger.Key},
			label:  label,
			group:  group,
			status: b.Action.Status,
		}
		seen[key] = r
		rows = append(rows, r)
	}

	byGroup := map[string][]*row{}
	for _, r := range rows {
		byGroup[r.group] = append(byGroup[r.group], r)
	}

	type line struct{ key, label string }
	var lines []line
	for _, g := range groupOrder {
		for _, r := range byGroup[g] {
			label := r.label
			if r.status != nil {
				label += " " + r.status(m)
			}
			lines = append(lines, line{key: collapseKeys(r.keys), label: label})
		}
	}

	// right-align keys in their own column so the arrows line up
	keyW := 3
	for _, ln := range lines {
		if w := len(ln.key); w > keyW {
			keyW = w
		}
	}
	keyCol := lipgloss.NewStyle().Width(keyW).Align(lipgloss.Right)

	out := []string{
		prefixPanelTitle.Render("» PREFIX C-A") + "   " + prefixHint.Render("esc to cancel"),
		"",
	}
	for _, ln := range lines {
		out = append(out, keyCol.Render(prefixKey.Render(ln.key))+prefixArrow.Render(" → ")+ln.label)
	}
	return prefixPanel.Render(strings.Join(out, "\n"))
}

// collapseKeys formats a Key list for one overlay row. Multiple
// contiguous ASCII digits with no modifiers compress to "first-last" (so
// 1..9 → "1-9"); otherwise the keys are joined with "/". Single keys
// pass through Key.String() for canonical form (so a ctrl-modified key
// renders as "ctrl-x", not just "x").
func collapseKeys(keys []Key) string {
	if len(keys) == 1 {
		return keys[0].String()
	}
	allPlainDigits := true
	for _, k := range keys {
		if k.Mods != 0 || k.Code < '0' || k.Code > '9' {
			allPlainDigits = false
			break
		}
	}
	if allPlainDigits {
		contiguous := true
		for i := 1; i < len(keys); i++ {
			if keys[i].Code != keys[i-1].Code+1 {
				contiguous = false
				break
			}
		}
		if contiguous {
			return keys[0].String() + "-" + keys[len(keys)-1].String()
		}
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k.String()
	}
	return strings.Join(parts, "/")
}

// composeOverlay stacks `panel` over `base` at (x, y) with the panel on top.
// All overlays use this so Z-ordering, layer construction, and the base
// layer's anchor live in one place.
func composeOverlay(base, panel string, x, y int) string {
	baseLayer := lipgloss.NewLayer(base).X(0).Y(0).Z(0)
	panelLayer := lipgloss.NewLayer(panel).X(x).Y(y).Z(1)
	return lipgloss.NewCompositor(baseLayer, panelLayer).Render()
}

// renderScrollback paints `inner` rows of the viewport for a single pane,
// with rows above the live screen filled from p.vt's scrollback.
// virtualHeight = scrollback.Len() + inner; the viewport's top virtual row is
// scrollback.Len() - scrollOff, so rows in [0, scrollback.Len()) come from
// history and rows beyond come from the current screen. The live screen is
// read cell-by-cell because the vt package doesn't expose row-level access on
// SafeEmulator.
func renderScrollback(p *Pane, w, inner int) string {
	sb := p.vt.Scrollback()
	sbLen := sb.Len()
	top := sbLen - p.scrollOff
	lines := make(uv.Lines, inner)
	for r := 0; r < inner; r++ {
		v := top + r
		switch {
		case v < 0:
			lines[r] = uv.NewLine(w)
		case v < sbLen:
			src := sb.Line(v)
			ln := uv.NewLine(w)
			for x := 0; x < w && x < len(src); x++ {
				ln.Set(x, src.At(x))
			}
			lines[r] = ln
		default:
			sy := v - sbLen
			ln := uv.NewLine(w)
			for x := 0; x < w; x++ {
				if c := p.vt.CellAt(x, sy); c != nil {
					ln.Set(x, c)
				}
			}
			lines[r] = ln
		}
	}
	return lines.Render()
}
