package main

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// bufScreen adapts a *uv.Buffer to the uv.Screen interface that
// StyledString.Draw needs — Buffer has everything except WidthMethod. We use
// WcWidth, matching both the vt emulator's default and bubbletea's cell buffer.
type bufScreen struct{ *uv.Buffer }

func (bufScreen) WidthMethod() uv.WidthMethod { return ansi.WcWidth }

// draw composites a styled string block into buf with its top-left at (x, y),
// clipped to the area (x, y, w, h). StyledString.Draw clears the area first, so a
// block fully overwrites whatever it covers — opaque-panel semantics.
func draw(buf *uv.Buffer, s string, x, y, w, h int) {
	uv.NewStyledString(s).Draw(bufScreen{buf}, uv.Rect(x, y, w, h))
}

var (
	tabIdle = lipgloss.NewStyle().
		Padding(0, 1).
		Foreground(lipgloss.Color("245"))
	tabActive = lipgloss.NewStyle().
			Padding(0, 1).
			Bold(true).
			Foreground(lipgloss.Color("16")).
			Background(lipgloss.Color("220"))
	scrollChip = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("16")).
			Background(lipgloss.Color("214"))
	zoomChip = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("63"))

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
	paneDividerActive = lipgloss.NewStyle().
				Foreground(lipgloss.Color("220"))

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
	// prefixActive recolors a menu row whose toggle (mouse, zoom) is on.
	prefixActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("114"))
)

// tabBarH is the height of the tab bar in rows. Every piece of vertical
// chrome math — body height, mouse-coordinate mapping, cursor placement,
// overlay anchoring — derives from this one constant so adding a status
// line later is a single-site change.
//
// The tab bar lives at the BOTTOM, below the pane body: panes occupy screen
// rows [0, bodyHeight) and the bar sits on the final row. Keeping panes at the
// top-left origin (0,0) makes pane geometry, cursor, and mouse math offset-free
// (no +tabBarH anywhere) and sets up a future single-pane passthrough fast path.
const tabBarH = 1

// bodyHeight is the number of rows above the tab bar available to panes. This
// is the only correct height to spawn or resize a pane against, and — since the
// body starts at row 0 — it is also the screen row the tab bar occupies.
func (m *Model) bodyHeight() int {
	h := m.h - tabBarH
	if h < 1 {
		return 1
	}
	return h
}

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
		prefixStr = prefixChip.Render(" PREFIX " + prefixLabel() + " ")
	} else {
		prefixStr = prefixChipIdle.Render(" prefix " + prefixLabel() + " ")
	}
	rightBar := tabsStr + " " + sessionStr + " " + prefixStr
	var leading int
	if s.tabs[s.active].zoomed {
		zoomStr := zoomChip.Render(" ZOOM ")
		rightBar = zoomStr + " " + rightBar
		leading += lipgloss.Width(zoomStr) + 1
	}
	if p := m.focusPane(); p.scrollOff > 0 {
		max := p.vt.Scrollback().Len()
		scrollStr := scrollChip.Render(fmt.Sprintf(" SCROLL %d/%d ", p.scrollOff, max))
		rightBar = scrollStr + " " + rightBar
		// +1 for the space separator between scrollStr and the rest.
		leading += lipgloss.Width(scrollStr) + 1
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

func (m *Model) View() tea.View {
	if m.w == 0 || m.h == 0 || len(m.sessions) == 0 {
		return tea.NewView("")
	}

	t := m.curTab()
	tabBar, _ := tabBarLayout(m)

	inner := m.bodyHeight()
	rects, divs := t.geometry(m.w, inner)

	// One full-screen cell buffer. Everything composites into it, then renders
	// once: pane bodies + dividers fill rows [0, inner); the tab bar takes the
	// bottom row; floating chrome (popup, overlays, prefix cheatsheet) draws on
	// top. This replaces the old chain of whole-screen string recompositions.
	buf := uv.NewBuffer(m.w, m.h)
	renderBodyInto(buf, rects, divs, t.active)
	draw(buf, tabBar, 0, inner, m.w, tabBarH)

	// Session popup floats over the body, below the overlay stack (so a picker or
	// rename opened while it's up still renders on top) and below the prefix
	// cheatsheet.
	pu := m.visiblePopup()
	if pu != nil {
		r := m.popupRect(pu)
		drawPanel(buf, renderPopup(pu, r), r.X, r.Y)
	}
	// Overlay stack: bottom→top, each drawn over the previous. Each overlay's
	// Render returns a styled block; Anchor.Place sizes it against (m.w, m.h)
	// using the block's own width/height.
	for _, ov := range m.overlays {
		panel := ov.Render(m)
		x, y := ov.Anchor().Place(m.w, m.h, lipgloss.Width(panel), lipgloss.Height(panel))
		drawPanel(buf, panel, x, y)
	}
	// Prefix cheatsheet sits on top of any overlays — it's a mode-driven visual,
	// not a popup. Bottom-right, just above the tab bar (the same place the
	// which-key drill-down anchors), so the panel grows up from next to the
	// PREFIX chip rather than dropping from the top.
	if m.prefix {
		panel := renderPrefixPanel(m)
		px, py := AnchorBottomRight{}.Place(m.w, m.h, lipgloss.Width(panel), lipgloss.Height(panel))
		drawPanel(buf, panel, px, py)
	}

	v := tea.NewView(buf.Render())
	v.AltScreen = true

	// surface the focused pane's pty cursor at its absolute coordinates:
	// the pane's on-screen origin plus the emulator's cursor offset. The
	// focused pane is the visible popup's if one is up (origin = popup
	// rect + 1 for the border), else the active tab pane (origin = the body
	// rect itself, since the body starts at row 0). when the shell hides the
	// cursor (DECTCEM off),
	// we're scrolled into history, or an interactive overlay/prefix owns
	// input, leave v.Cursor nil — but give a CursorProvider overlay a
	// chance to paint its own cursor (top-down so the topmost wins).
	p := t.active
	ox, oy, haveOrigin := 0, 0, false
	if pu != nil {
		p = pu.pane
		r := m.popupRect(pu)
		ox, oy, haveOrigin = r.X+1, r.Y+1, true
	} else if r, ok := rects[p]; ok {
		c := contentRect(r, divs)
		ox, oy, haveOrigin = c.X, c.Y, true
	}
	if haveOrigin && p.cursorVisible && p.scrollOff == 0 && !m.prefix && m.topInteractiveOverlay() == nil {
		px, py := paneCursorPos(p)
		c := tea.NewCursor(ox+px, oy+py)
		if p.cursorStyleSet {
			// Honor the child's DECSCUSR. vt's CursorStyle enum matches
			// tea's (block=0, underline=1, bar=2); cursorSteady is the
			// inverse of blink. bubbletea diffs against the last emitted
			// style and only writes DECSCUSR when it changes, so switching
			// panes/tabs re-emits the new active pane's shape correctly.
			c.Shape = tea.CursorShape(p.cursorStyle)
			c.Blink = !p.cursorSteady
		} else {
			// No DECSCUSR seen yet — suppress bubbletea's emission so the
			// terminal keeps its user-configured cursor shape. bubbletea only
			// writes the style when encodeCursorStyle(new) != encodeCursorStyle(old);
			// on first render lastView is nil so old encodes to 0. shape=-1,
			// blink=false yields (-1*2)+1+1 = 0, matching, so nothing is written.
			c.Shape = tea.CursorShape(-1)
			c.Blink = false
		}
		v.Cursor = c
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

// renderBodyInto composites the body area — each pane's content at its rect, with
// dividers between siblings — directly into the shared full-screen cell buffer.
// rects and divs come from the same layoutGeometry walk. Because the body starts
// at screen row 0 (the tab bar is on the bottom), a pane's body-space rect maps
// straight to buffer coordinates with no offset. Divider cells abutting another
// divider render as junctions (├ ┤ ┬ ┴ ┼); cells bordering the active pane's rect
// render in the focus color so the focused pane reads at a glance. Dividers are
// drawn before pane bodies so pane content overdraws any stray junction cells.
func renderBodyInto(buf *uv.Buffer, rects map[*Pane]Rect, divs []dividerSpec, active *Pane) {
	arms := dividerArms(divs)
	activeRect, hasActive := rects[active]
	// hot reports whether a divider cell runs alongside the active pane — the
	// four border dividers, each bounded to the pane's own span on the
	// perpendicular axis. Corners are excluded on purpose: a corner cell sits
	// on a divider that continues past the pane (a shared junction), so lighting
	// it would bleed the highlight one cell beyond the pane's edge.
	hot := func(x, y int) bool {
		if !hasActive {
			return false
		}
		r := activeRect
		onSide := (x == r.X-1 || x == r.X+r.W) && y >= r.Y && y < r.Y+r.H
		onEnd := (y == r.Y-1 || y == r.Y+r.H) && x >= r.X && x < r.X+r.W
		return onSide || onEnd
	}
	style := func(active bool) lipgloss.Style {
		if active {
			return paneDividerActive
		}
		return paneDivider
	}

	for _, d := range divs {
		var s string
		var aw, ah int
		if d.vertical {
			// One cell per row; each row carries its own style.
			rows := make([]string, d.length)
			for i := range rows {
				x, y := d.x, d.y+i
				rows[i] = style(hot(x, y)).Render(string(dividerRune(arms[cellPos{x, y}])))
			}
			s = strings.Join(rows, "\n")
			aw, ah = 1, d.length
		} else {
			// Group contiguous same-style cells into runs so a span renders
			// as a handful of styled segments, not one SGR per cell.
			var b strings.Builder
			runStart := 0
			runHot := hot(d.x, d.y)
			var run strings.Builder
			flush := func(end int) {
				if end > runStart {
					b.WriteString(style(runHot).Render(run.String()))
					run.Reset()
					runStart = end
				}
			}
			for i := 0; i < d.length; i++ {
				x, y := d.x+i, d.y
				if h := hot(x, y); h != runHot {
					flush(i)
					runHot = h
				}
				run.WriteRune(dividerRune(arms[cellPos{x, y}]))
			}
			flush(d.length)
			s = b.String()
			aw, ah = d.length, 1
		}
		draw(buf, s, d.x, d.y, aw, ah)
	}

	for pane, r := range rects {
		c := contentRect(r, divs)
		body := renderPaneBody(pane, c.W, c.H)
		draw(buf, body, c.X, c.Y, c.W, c.H)
	}
}

// renderBody composites the body into a fresh w×inner cell buffer and returns it
// as a styled string. View draws the body straight into the full-screen buffer via
// renderBodyInto; this string form is for tests and standalone body renders.
func renderBody(rects map[*Pane]Rect, divs []dividerSpec, w, inner int, active *Pane) string {
	buf := uv.NewBuffer(w, inner)
	renderBodyInto(buf, rects, divs, active)
	return buf.Render()
}

// drawPanel composites a styled-string block onto buf with its top-left at (x, y),
// sized to the block's own width/height and clipped to the buffer. This replaces
// the old string-based composeOverlay: each panel is drawn straight into the
// shared cell buffer instead of re-parsing the whole screen per overlay.
func drawPanel(buf *uv.Buffer, panel string, x, y int) {
	draw(buf, panel, x, y, lipgloss.Width(panel), lipgloss.Height(panel))
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

// paneCursorPos mirrors paneRenderSource's choice for the cursor: while a
// DECSET 2026 sync block is active, return the position captured at ?2026h —
// the live position would jitter across frame-N+1 column-jumps that aren't
// visible yet. Timeout expiry falls back to the live cursor, matching the
// render path.
func paneCursorPos(p *Pane) (int, int) {
	if p.syncFrozen && time.Since(p.syncStartedAt) < syncTimeout {
		return p.syncCursorX, p.syncCursorY
	}
	pos := p.vt.CursorPosition()
	return pos.X, pos.Y
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

// prefixLabel renders the configured prefix in the short "C-A" / "C-Space"
// style the status chips and cheatsheet header use. setPrefix guarantees the
// prefix is a ctrl chord, so the modifier is always "C-"; the base is the
// uppercased letter or a named key like Space.
func prefixLabel() string {
	k := prefixKeyDef
	var base string
	switch {
	case k.Code == KeySpace:
		base = "Space"
	case k.Code >= 'a' && k.Code <= 'z':
		base = string(rune(k.Code - 32))
	default:
		base = string(rune(k.Code))
	}
	return "C-" + base
}

// renderPrefixPanel builds the floating cheatsheet shown while the prefix is
// armed. It renders the root which-key level: group leaders ("t → +tabs")
// alongside any flat actions kept at root. Pressing a leader opens a
// WhichKeyOverlay for that submenu, which renders with the same helper.
func renderPrefixPanel(m *Model) string {
	return renderMenuPanel(m, defaultKeymap.menus[""], "» PREFIX "+prefixLabel(), "esc to cancel")
}

type menuLine struct {
	key    string
	label  string
	active bool // the action's toggle is currently on — render highlighted
}

// menuLines builds the "<keys> → <label>" rows for one level: bindings that
// resolve to the same label collapse into one row with their keys merged —
// tab.jump's 1..9 render as "1-9"; pane.resize's four directions stay separate
// because each overrides Label. A row whose action is a toggle that's
// currently on is marked active so the panel can highlight it — the label text
// itself never carries on/off state, so the row width is stable.
func menuLines(m *Model, lvl *menuLevel) []menuLine {
	type row struct {
		keys   []Key
		label  string
		status func(*Model) string
	}
	seen := map[string]*row{}
	var rows []*row
	for _, b := range lvl.bindings {
		label := b.EffectiveLabel()
		if r, ok := seen[label]; ok {
			r.keys = append(r.keys, b.Trigger.Key)
			continue
		}
		r := &row{keys: []Key{b.Trigger.Key}, label: label, status: b.Action.Status}
		seen[label] = r
		rows = append(rows, r)
	}
	lines := make([]menuLine, 0, len(rows))
	for _, r := range rows {
		active := r.status != nil && r.status(m) != ""
		lines = append(lines, menuLine{key: collapseKeys(r.keys), label: r.label, active: active})
	}
	return lines
}

// menuGeometry computes the key-column width and content width shared by every
// which-key panel, taken as the max across all menu levels. Rendering every
// level at these dimensions means switching groups (or stepping in/out of a
// submenu) doesn't resize the panel or shift the arrow column. Labels carry no
// runtime state, so these widths don't change as toggles flip.
func menuGeometry(m *Model) (keyW, contentW int) {
	keyW = 3
	for _, lvl := range defaultKeymap.menus {
		for _, ln := range menuLines(m, lvl) {
			if w := lipgloss.Width(ln.key); w > keyW {
				keyW = w
			}
		}
	}
	keyCol := lipgloss.NewStyle().Width(keyW).Align(lipgloss.Right)
	for _, lvl := range defaultKeymap.menus {
		for _, ln := range menuLines(m, lvl) {
			w := lipgloss.Width(keyCol.Render(prefixKey.Render(ln.key)) + prefixArrow.Render(" → ") + ln.label)
			if w > contentW {
				contentW = w
			}
		}
	}
	return keyW, contentW
}

// renderMenuPanel draws one which-key level as the floating panel: a cyan
// header (title or breadcrumb), one "<keys> → <label>" line per binding, and a
// hint footer. Every level renders at the same key-column and content width
// (see menuGeometry) so the panel holds its size as the user moves between
// groups. Shared by the root prefix panel and WhichKeyOverlay so the two
// always look identical.
func renderMenuPanel(m *Model, lvl *menuLevel, header, hint string) string {
	keyW, contentW := menuGeometry(m)
	keyCol := lipgloss.NewStyle().Width(keyW).Align(lipgloss.Right)

	out := []string{prefixPanelTitle.Render(header), ""}
	for _, ln := range menuLines(m, lvl) {
		label := ln.label
		if ln.active {
			// Toggle is on — recolor the label (same text, same width) so its
			// state reads at a glance without a width-changing "(on)" suffix.
			label = prefixActive.Render(label)
		}
		out = append(out, keyCol.Render(prefixKey.Render(ln.key))+prefixArrow.Render(" → ")+label)
	}
	// Hint sits at the bottom, below a blank spacer — same placement as the
	// picker overlay's footer.
	out = append(out, "", prefixHint.Render(hint))

	// Pad every line to a shared width (never truncating: a long breadcrumb
	// header or hint can exceed the binding rows, so grow to fit it), then let
	// the bordered panel auto-size around it. Padding the content rather than
	// forcing the panel's Width keeps us clear of how lipgloss folds border +
	// padding into Width — setting Width to the content width would shrink the
	// text area by the frame and wrap the longest row.
	width := contentW
	for _, l := range out {
		if w := lipgloss.Width(l); w > width {
			width = w
		}
	}
	pad := lipgloss.NewStyle().Width(width)
	for i, l := range out {
		out[i] = pad.Render(l)
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
