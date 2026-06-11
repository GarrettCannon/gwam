package main

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// LogSearchOverlay is an mnil-style live search over a single pane's output
// (scrollback + live screen). It takes over the body as a full-width viewport
// with a search bar pinned to the bottom: type to filter, tab/shift-tab to
// hop matches, ^f to collapse to matching lines only, ^l to follow the tail.
//
// The source pane keeps streaming while the overlay is up — every pty read
// re-renders the model, and Render re-reads the pane buffer each time, so a
// running dev server's new lines flow into the search live.
//
// Unlike the picker, this never returns a selection and never touches the
// pane's own render path; it's a self-contained viewer composed on top.
type LogSearchOverlay struct {
	// pane is captured at open (the active pane). A pointer, so a pane-cycle
	// or tab-switch while the overlay is up can't redirect the search onto
	// whatever happens to be focused at render time.
	pane *Pane

	query      string
	queryLower string

	// Rebuilt every sync from the pane: disp is the full line buffer, matches
	// indexes into disp, rowIdx is the displayed subset (all lines, or just
	// matches in filter mode).
	disp    []uv.Line
	matches []int
	rowIdx  []int
	cur     int // index into matches, or -1

	filter bool // ^f — show only matching lines
	follow bool // ^l — stick to the tail as new lines arrive
	yoff   int  // top row of the visible window into rowIdx
}

func (o *LogSearchOverlay) Anchor() Anchor  { return AnchorBody{} }
func (o *LogSearchOverlay) OwnsInput() bool { return true }

// AnchorBody pins an overlay to the full body region, just under the tab bar.
// The overlay itself renders at (m.w, bodyHeight), so it fully covers the
// panes behind it.
type AnchorBody struct{}

func (AnchorBody) Place(W, H, w, h int) (x, y int) { return 0, tabBarH }

// ---- buffer plumbing ----

// paneBufferLines snapshots the pane's scrollback followed by its live screen
// as one flat line list, oldest first. Scrollback lines alias vt's internal
// storage (read-only here); live rows are freshly built from CellAt. Trailing
// all-blank live rows are trimmed so an unfilled screen doesn't pad the view.
func paneBufferLines(p *Pane) []uv.Line {
	sb := p.vt.Scrollback()
	n := sb.Len()
	w, h := p.vt.Width(), p.vt.Height()
	lines := make([]uv.Line, 0, n+h)
	for i := 0; i < n; i++ {
		lines = append(lines, sb.Line(i))
	}
	for y := 0; y < h; y++ {
		ln := make(uv.Line, w)
		for x := 0; x < w; x++ {
			if c := p.vt.CellAt(x, y); c != nil {
				ln[x] = *c
			}
		}
		lines = append(lines, ln)
	}
	// Trim trailing blank lines (common: the live screen's unused bottom rows).
	for len(lines) > 0 && lineBlank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func lineBlank(l uv.Line) bool {
	for i := range l {
		if c := l[i].Content; c != "" && c != " " {
			return false
		}
	}
	return true
}

// linePlainLower returns the line's text, lowercased, for substring matching.
func linePlainLower(l uv.Line) string {
	var b strings.Builder
	for i := range l {
		c := l[i].Content
		if c == "" {
			c = " "
		}
		b.WriteString(c)
	}
	return strings.ToLower(strings.TrimRight(b.String(), " "))
}

// matchSpans returns the cell [start,end) ranges in l that contain qLower.
// Computed only for the handful of on-screen lines, so the per-byte cell map
// it builds is cheap. Returns nil when qLower is empty or absent.
func matchSpans(l uv.Line, qLower string) [][2]int {
	if qLower == "" {
		return nil
	}
	var b strings.Builder
	var cellAt []int
	for i := range l {
		c := l[i].Content
		if c == "" {
			c = " "
		}
		lc := strings.ToLower(c)
		for j := 0; j < len(lc); j++ {
			cellAt = append(cellAt, i)
		}
		b.WriteString(lc)
	}
	s := b.String()
	var spans [][2]int
	for from := 0; ; {
		idx := strings.Index(s[from:], qLower)
		if idx < 0 {
			break
		}
		bs := from + idx
		be := bs + len(qLower)
		spans = append(spans, [2]int{cellAt[bs], cellAt[be-1] + 1})
		from = be
	}
	return spans
}

// sync rebuilds the line buffer from the pane and recomputes the match set
// and displayed-row index for the current query. Called at the top of both
// Render and HandleKey so commands and rendering always see the live buffer.
func (o *LogSearchOverlay) sync() {
	o.disp = paneBufferLines(o.pane)

	o.matches = o.matches[:0]
	if o.queryLower != "" {
		for i := range o.disp {
			if strings.Contains(linePlainLower(o.disp[i]), o.queryLower) {
				o.matches = append(o.matches, i)
			}
		}
	}
	// Clamp the current-match pointer to the (possibly resized) match set.
	switch {
	case len(o.matches) == 0:
		o.cur = -1
	case o.cur < 0:
		o.cur = 0
	case o.cur >= len(o.matches):
		o.cur = len(o.matches) - 1
	}

	o.rowIdx = o.rowIdx[:0]
	if o.filter && o.queryLower != "" {
		o.rowIdx = append(o.rowIdx, o.matches...)
	} else {
		for i := range o.disp {
			o.rowIdx = append(o.rowIdx, i)
		}
	}
}

// rowPosOfCur returns the position within rowIdx of the current match's line,
// or -1. In filter mode rowIdx == matches, so it's just o.cur; otherwise the
// line sits at its own index.
func (o *LogSearchOverlay) rowPosOfCur() int {
	if o.cur < 0 || o.cur >= len(o.matches) {
		return -1
	}
	if o.filter && o.queryLower != "" {
		return o.cur
	}
	return o.matches[o.cur]
}

// scrollToCur parks the current match about a third of the way down the
// listH-row window.
func (o *LogSearchOverlay) scrollToCur(listH int) {
	pos := o.rowPosOfCur()
	if pos < 0 {
		return
	}
	o.yoff = pos - listH/3
	if o.yoff < 0 {
		o.yoff = 0
	}
}

// ---- input ----

func (o *LogSearchOverlay) HandleKey(data []byte, m *Model) (bool, tea.Cmd) {
	o.sync()
	listH := o.listH(m)
	queryChanged := false

	for i := 0; i < len(data); i++ {
		b := data[i]

		// Escape sequences: arrows, shift-tab, page/home/end. Anything else
		// after ESC[ falls through and the lone ESC closes the overlay.
		if b == 0x1b && i+2 < len(data) && data[i+1] == '[' {
			handled := true
			switch data[i+2] {
			case 'A': // up
				o.scrollBy(-1, listH)
			case 'B': // down
				o.scrollBy(1, listH)
			case 'H': // home
				o.yoff, o.follow = 0, false
			case 'F': // end
				o.follow = true
			case 'Z': // shift-tab — previous match
				o.stepMatch(-1, listH)
			case '5': // pgup — "\x1b[5~"
				o.scrollBy(-listH, listH)
				if i+3 < len(data) && data[i+3] == '~' {
					i++
				}
			case '6': // pgdn
				o.scrollBy(listH, listH)
				if i+3 < len(data) && data[i+3] == '~' {
					i++
				}
			default:
				handled = false
			}
			if handled {
				i += 2
				continue
			}
		}

		switch b {
		case 0x1b, 0x03: // esc / ctrl-c — close
			return true, nil
		case 0x09: // tab — next match
			o.stepMatch(1, listH)
		case 0x06: // ctrl-f — toggle filter
			o.filter = !o.filter
			o.sync()
			o.scrollToCur(listH)
		case 0x0c: // ctrl-l — toggle follow
			o.follow = !o.follow
		case 0x7f, 0x08: // backspace
			if rs := []rune(o.query); len(rs) > 0 {
				o.query = string(rs[:len(rs)-1])
				queryChanged = true
			}
		default:
			if b >= 0x20 && b < 0x7f && len(o.query) < 256 {
				o.query += string(b)
				queryChanged = true
			}
		}
	}

	if queryChanged {
		o.queryLower = strings.ToLower(o.query)
		o.sync()
		o.follow = false
		// Land on the most recent match — for logs that's usually what you want.
		o.cur = len(o.matches) - 1
		o.scrollToCur(listH)
	}
	return false, nil
}

func (o *LogSearchOverlay) stepMatch(delta, listH int) {
	if len(o.matches) == 0 {
		return
	}
	o.cur = (o.cur + delta + len(o.matches)) % len(o.matches)
	o.follow = false
	o.scrollToCur(listH)
}

func (o *LogSearchOverlay) scrollBy(delta, listH int) {
	o.follow = false
	o.yoff += delta
	o.clampYoff(listH)
}

func (o *LogSearchOverlay) clampYoff(listH int) {
	maxOff := len(o.rowIdx) - listH
	if maxOff < 0 {
		maxOff = 0
	}
	if o.yoff > maxOff {
		o.yoff = maxOff
	}
	if o.yoff < 0 {
		o.yoff = 0
	}
}

// listH is the number of log rows visible above the two-line search bar plus
// its separator.
func (o *LogSearchOverlay) listH(m *Model) int {
	h := m.bodyHeight() - 3
	if h < 1 {
		h = 1
	}
	return h
}

// ---- render ----

var (
	logSep    = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	logCount  = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	logPrompt = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	logHint   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	logOn     = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
)

func (o *LogSearchOverlay) Render(m *Model) string {
	o.sync()
	w := m.w
	listH := o.listH(m)

	if o.follow {
		o.yoff = len(o.rowIdx) - listH
	}
	o.clampYoff(listH)

	curLine := -1
	if o.cur >= 0 && o.cur < len(o.matches) {
		curLine = o.matches[o.cur]
	}

	rows := make([]string, 0, m.bodyHeight())
	for r := 0; r < listH; r++ {
		vi := o.yoff + r
		if vi < 0 || vi >= len(o.rowIdx) {
			rows = append(rows, strings.Repeat(" ", w))
			continue
		}
		di := o.rowIdx[vi]
		var spans [][2]int
		if o.queryLower != "" {
			spans = matchSpans(o.disp[di], o.queryLower)
		}
		rows = append(rows, renderLogRow(o.disp[di], spans, di == curLine, w))
	}

	rows = append(rows, logSep.Render(strings.Repeat("─", w)))
	rows = append(rows, o.inputRow(w))
	rows = append(rows, o.hintRow(w))
	return strings.Join(rows, "\n")
}

// renderLogRow renders one buffer line clipped to w cells, with match spans
// background-highlighted (gold; orange for the current match) while the rest
// of the line keeps its original colors. Padded out to w so the overlay
// fully occludes the pane behind it.
func renderLogRow(src uv.Line, spans [][2]int, current bool, w int) string {
	line := src
	if len(line) > w {
		line = line[:w]
	}
	var rendered string
	if len(spans) == 0 {
		rendered = line.Render()
	} else {
		cp := make(uv.Line, len(line))
		copy(cp, line)
		bg := color.RGBA{R: 0xff, G: 0xd7, B: 0x00, A: 0xff}
		if current {
			bg = color.RGBA{R: 0xff, G: 0x87, B: 0x00, A: 0xff}
		}
		fg := color.RGBA{A: 0xff}
		for _, sp := range spans {
			for x := sp[0]; x < sp[1] && x < len(cp); x++ {
				cp[x].Style.Fg = fg
				cp[x].Style.Bg = bg
			}
		}
		rendered = cp.Render()
	}
	if gap := w - lipgloss.Width(rendered); gap > 0 {
		rendered += strings.Repeat(" ", gap)
	}
	return rendered
}

func (o *LogSearchOverlay) inputRow(w int) string {
	left := logCount.Render(fmt.Sprintf("%d lines", len(o.disp)))
	cursor := lipgloss.NewStyle().Reverse(true).Render(" ")
	mid := logPrompt.Render("/ ") + o.query + cursor

	var right string
	switch {
	case o.queryLower == "":
		right = logHint.Render("type to search")
	case len(o.matches) == 0:
		right = logHint.Render("no matches")
	default:
		right = logCount.Render(fmt.Sprintf("%d/%d", o.cur+1, len(o.matches)))
	}

	leftPart := " " + left + "  " + mid
	gap := w - lipgloss.Width(leftPart) - lipgloss.Width(right) - 1
	if gap < 0 {
		gap = 0
	}
	return padRight(leftPart+strings.Repeat(" ", gap)+right+" ", w)
}

func (o *LogSearchOverlay) hintRow(w int) string {
	onoff := func(on bool) string {
		if on {
			return logOn.Render("on")
		}
		return logHint.Render("off")
	}
	h := logHint.Render(" tab/⇧tab jump · ") +
		logHint.Render("^f filter ") + onoff(o.filter) +
		logHint.Render(" · ^l live ") + onoff(o.follow) +
		logHint.Render(" · ↑↓ scroll · esc close ")
	return padRight(h, w)
}

// padRight pads s with spaces to display width w (never truncates).
func padRight(s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}
