package main

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// NoticeOverlay is a passive, auto-dismissing toast. Anchored top-right under
// the tab bar so it doesn't cover the active pane's content. Doesn't own
// input — keystrokes flow past it to whatever interactive overlay sits below,
// or to the pty if none.
//
// Lifecycle: callers push the overlay and return DismissCmd from the action
// that opened it. The tick fires once and Update removes this specific
// instance via removeOverlay (so a notice that opened, was buried under a
// later overlay, and timed out still gets cleared correctly).
type NoticeOverlay struct {
	text  string
	style lipgloss.Style
	ttl   time.Duration
}

// NewNoticeOverlay builds a default-styled toast. style.IsZero check would be
// awkward (lipgloss styles have no IsZero), so we always assign — pass
// (lipgloss.Style{}) explicitly to keep the default. ttl <= 0 disables
// auto-dismiss; the caller becomes responsible for removal.
func NewNoticeOverlay(text string, ttl time.Duration) *NoticeOverlay {
	return &NoticeOverlay{
		text:  text,
		ttl:   ttl,
		style: noticeStyle,
	}
}

// WithStyle returns a copy with a custom style — chainable so callers can
// vary toast color without exporting a struct field.
func (n *NoticeOverlay) WithStyle(s lipgloss.Style) *NoticeOverlay {
	n.style = s
	return n
}

func (n *NoticeOverlay) Anchor() Anchor  { return AnchorTopRight{Y: 1} }
func (n *NoticeOverlay) OwnsInput() bool { return false }

// HandleKey is unreachable in practice — the dispatcher only delivers to
// interactive overlays. Present to satisfy the interface.
func (n *NoticeOverlay) HandleKey(_ []byte, _ *Model) (bool, tea.Cmd) {
	return false, nil
}

func (n *NoticeOverlay) Render(_ *Model) string {
	return n.style.Render(" " + n.text + " ")
}

// DismissCmd schedules a one-shot tick that removes this specific notice
// after ttl. Returns nil if ttl <= 0 — the caller will dismiss manually.
func (n *NoticeOverlay) DismissCmd() tea.Cmd {
	if n.ttl <= 0 {
		return nil
	}
	return tea.Tick(n.ttl, func(time.Time) tea.Msg {
		return noticeDismissMsg{notice: n}
	})
}

// noticeDismissMsg removes a specific NoticeOverlay instance from the stack.
// The notice pointer is what identifies it — using indices would race with
// any push/pop that happened in the meantime.
type noticeDismissMsg struct{ notice *NoticeOverlay }

// noticeStyle is the default toast appearance — muted background, light
// foreground. Matches the prefixChipIdle aesthetic so notices don't look
// alarming.
var noticeStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("231")).
	Background(lipgloss.Color("65")).
	Padding(0, 1)
