package main

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// RenameOverlay is the rename-input dialog used by `prefix ,` (tab) and
// `prefix $` (session). title labels the panel; apply receives the trimmed
// buffer on Enter — captured as a closure at construction so a later tab
// close / active-tab change can't redirect the rename onto whichever tab
// happens to be focused at commit time. Empty-buffer semantics are the
// caller's responsibility: actRenameTab passes an apply that always writes
// (clearing customName); actRenameSession's apply guards on name != "".
type RenameOverlay struct {
	title string
	buf   string
	apply func(name string)
}

func (r *RenameOverlay) Anchor() Anchor  { return AnchorFractionalY{Frac: 1.0 / 3.0} }
func (r *RenameOverlay) OwnsInput() bool { return true }

func (r *RenameOverlay) Render(_ *Model) string {
	const fieldW = 30
	// Block cursor is drawn inline as a reverse-video space so we can leave
	// v.Cursor nil and keep the terminal's hardware cursor out of the way.
	cursor := lipgloss.NewStyle().Reverse(true).Render(" ")
	// lipgloss.Width counts display cells, not bytes — multi-byte runes
	// (CJK, emoji, accented latin) padded by len() would tear the field.
	pad := fieldW - lipgloss.Width(r.buf) - 1
	if pad < 0 {
		pad = 0
	}
	input := r.buf + cursor + strings.Repeat(" ", pad)
	lines := []string{
		prefixPanelTitle.Render("» " + r.title),
		"",
		input,
		"",
		prefixHint.Render("enter: confirm   esc: cancel"),
	}
	return prefixPanel.Render(strings.Join(lines, "\n"))
}

func (r *RenameOverlay) HandleKey(data []byte, _ *Model) (bool, tea.Cmd) {
	// Process the whole batch in one call so a paste of K bytes is one
	// render, not K. Cancel/submit short-circuit the rest of the batch.
	for _, b := range data {
		switch b {
		case 0x1b, 0x03: // esc / ctrl-c — cancel
			return true, nil
		case 0x0d, 0x0a: // enter — commit
			if r.apply != nil {
				r.apply(strings.TrimSpace(r.buf))
			}
			return true, nil
		case 0x7f, 0x08: // del / backspace — strip one rune so multi-byte names don't shred
			if rs := []rune(r.buf); len(rs) > 0 {
				r.buf = string(rs[:len(rs)-1])
			}
		default:
			// printable ASCII only; cap rune-length so the overlay stays sane.
			if b >= 0x20 && b < 0x7f {
				if utf8.RuneCountInString(r.buf) < 40 {
					r.buf += string(b)
				}
			}
		}
	}
	return false, nil
}
