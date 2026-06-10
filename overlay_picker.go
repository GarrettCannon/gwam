package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// pickerMaxRows caps the visible list height. The cursor scrolls a window
// of this many items; full list lengths beyond this are fine, the user
// just sees a slice around the cursor.
const pickerMaxRows = 8

// PickerItem is one row in a PickerOverlay's list. Label is what renders;
// Search is what the filter matches against (falls back to Label if empty).
// Splitting the two lets a row carry decorative chrome (e.g. "1 - shell")
// while the user still types just the meaningful part ("shell"). Data is
// opaque caller payload that comes back through onPick.
type PickerItem struct {
	Label  string
	Search string
	Data   any
}

// matchKey is what the filter compares the query against — Search if set,
// otherwise Label. Centralized so future match modes (substring, fuzzy)
// only need to change filtered().
func (it PickerItem) matchKey() string {
	if it.Search != "" {
		return it.Search
	}
	return it.Label
}

// PickerOverlay is a search-input + filtered-list selector. Default match
// is case-insensitive prefix on Label — upgrading to substring/fuzzy is a
// one-line change in filtered().
//
// Navigation: ↑/↓ or Ctrl-P/Ctrl-N move the cursor (wraps). Enter calls
// onPick with the highlighted item. Esc/Ctrl-C closes without picking.
// Backspace edits the query. Printable ASCII appends to the query and
// resets the cursor to 0.
type PickerOverlay struct {
	title  string
	items  []PickerItem
	query  string
	cursor int
	onPick func(item PickerItem)
}

func NewPickerOverlay(title string, items []PickerItem, onPick func(PickerItem)) *PickerOverlay {
	return &PickerOverlay{title: title, items: items, onPick: onPick}
}

func (p *PickerOverlay) Anchor() Anchor  { return AnchorFractionalY{Frac: 1.0 / 4.0} }
func (p *PickerOverlay) OwnsInput() bool { return true }

// filtered returns items whose matchKey has p.query as a case-insensitive
// prefix. Empty query returns all items.
func (p *PickerOverlay) filtered() []PickerItem {
	if p.query == "" {
		return p.items
	}
	q := strings.ToLower(p.query)
	out := make([]PickerItem, 0, len(p.items))
	for _, it := range p.items {
		if strings.HasPrefix(strings.ToLower(it.matchKey()), q) {
			out = append(out, it)
		}
	}
	return out
}

// moveCursor walks the filtered list by d positions, wrapping at both ends.
// Wrap is cheap and matches typical CLI picker UX (tmux, fzf both wrap).
func (p *PickerOverlay) moveCursor(d int) {
	n := len(p.filtered())
	if n == 0 {
		p.cursor = 0
		return
	}
	p.cursor = ((p.cursor+d)%n + n) % n
}

func (p *PickerOverlay) Render(_ *Model) string {
	filt := p.filtered()
	// Clamp cursor in case the filter just shortened the list out from under us.
	if p.cursor >= len(filt) {
		p.cursor = len(filt) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}

	// Input row: › query▍   (inline reverse-video cursor cell, same trick
	// as RenameOverlay so the hardware cursor stays out of the way).
	cursorCell := lipgloss.NewStyle().Reverse(true).Render(" ")
	input := pickerPromptStyle.Render("› ") + p.query + cursorCell

	// Sliding window of up to pickerMaxRows centered on the cursor. Once the
	// cursor reaches the bottom of the window, the window scrolls down.
	start := 0
	if p.cursor >= pickerMaxRows {
		start = p.cursor - pickerMaxRows + 1
	}
	end := start + pickerMaxRows
	if end > len(filt) {
		end = len(filt)
	}

	var listLines []string
	if len(filt) == 0 {
		listLines = []string{prefixHint.Render("  (no matches)")}
	} else {
		for i := start; i < end; i++ {
			row := "  " + filt[i].Label
			if i == p.cursor {
				row = pickerSelected.Render("▶ " + filt[i].Label)
			}
			listLines = append(listLines, row)
		}
	}

	lines := []string{
		prefixPanelTitle.Render("» " + p.title),
		"",
		input,
		"",
	}
	lines = append(lines, listLines...)
	lines = append(lines, "")
	lines = append(lines, prefixHint.Render("↑/↓: move   enter: select   esc: cancel"))
	return prefixPanel.Render(strings.Join(lines, "\n"))
}

func (p *PickerOverlay) HandleKey(data []byte, _ *Model) (bool, tea.Cmd) {
	// Walk by index so we can consume multi-byte legacy CSI sequences
	// (\x1b[A / \x1b[B) without leaking the trailing bytes as printable text
	// into the query. The pump strips kitty CSI-u and SGR mouse upstream,
	// but legacy arrows pass through verbatim.
	for i := 0; i < len(data); i++ {
		b := data[i]

		// Legacy arrows: \x1b[A (up), \x1b[B (down). Anything else after
		// \x1b[ is unknown — fall through and let \x1b be handled as esc
		// below (closes the overlay), which is the safest default.
		if b == 0x1b && i+2 < len(data) && data[i+1] == '[' {
			switch data[i+2] {
			case 'A':
				p.moveCursor(-1)
				i += 2
				continue
			case 'B':
				p.moveCursor(1)
				i += 2
				continue
			}
		}

		switch b {
		case 0x1b, 0x03: // esc / ctrl-c — cancel
			return true, nil
		case 0x0d, 0x0a: // enter — commit
			filt := p.filtered()
			if p.cursor < len(filt) && p.onPick != nil {
				p.onPick(filt[p.cursor])
			}
			return true, nil
		case 0x7f, 0x08: // backspace
			if rs := []rune(p.query); len(rs) > 0 {
				p.query = string(rs[:len(rs)-1])
				p.cursor = 0
			}
		case 0x10: // ctrl-p
			p.moveCursor(-1)
		case 0x0e: // ctrl-n
			p.moveCursor(1)
		default:
			// printable ASCII appends to query. Cap so the input doesn't
			// grow unbounded — past ~40 chars a prefix match has nothing
			// useful left to do.
			if b >= 0x20 && b < 0x7f && len(p.query) < 40 {
				p.query += string(b)
				p.cursor = 0
			}
		}
	}
	return false, nil
}

var (
	pickerPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("220")).
				Bold(true)
	pickerSelected = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("63"))
)
