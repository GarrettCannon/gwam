package main

import (
	tea "charm.land/bubbletea/v2"
)

// Overlay is anything that renders on top of the base view as a compositor
// layer. Two kinds of overlays coexist on Model.overlays:
//
//   - interactive (OwnsInput == true): the stdin pump diverts keystrokes here
//     as overlayKeyMsg; HandleKey closes the overlay by returning true.
//   - passive (OwnsInput == false): cosmetic layers (toasts, status chips)
//     that don't take input — keys keep flowing to whichever interactive
//     overlay is below, or to the pty if none.
//
// Render returns a styled lipgloss block; its width/height are inferred via
// lipgloss.Width / lipgloss.Height. Anchor places that block on the model's
// (W, H) canvas. HandleKey runs in Update; data is the raw byte batch that
// arrived from the pump (a paste of K bytes is one call, not K).
type Overlay interface {
	Render(m *Model) string
	Anchor() Anchor
	OwnsInput() bool
	HandleKey(data []byte, m *Model) (closed bool, cmd tea.Cmd)
}

// CursorProvider is an optional add-on: an overlay can surface a hardware
// terminal cursor by implementing it. Most overlays paint a fake reverse-
// video cursor inline (today's RenameOverlay) and don't need this.
type CursorProvider interface {
	Cursor(m *Model) *tea.Cursor
}

// Anchor positions an overlay's rendered (w, h) on the model's (W, H) canvas.
// Implementations are tiny value types — see AnchorCenter, AnchorTopRight,
// AnchorFractionalY below. Future-Anchor cases (mouse position, anchored to
// a tab chip) can land alongside without touching the interface.
type Anchor interface {
	Place(W, H, w, h int) (x, y int)
}

// AnchorCenter centers the overlay both horizontally and vertically. Clamped
// to (0, 0) so an overflow-large overlay doesn't render off-screen.
type AnchorCenter struct{}

func (AnchorCenter) Place(W, H, w, h int) (x, y int) {
	x = (W - w) / 2
	if x < 0 {
		x = 0
	}
	y = (H - h) / 2
	if y < 0 {
		y = 0
	}
	return
}

// AnchorTopRight pins the overlay's right edge to the screen's right edge
// and its top edge to row Y. Used by the prefix cheatsheet (Y=1, under the
// tab bar) when it migrates onto the stack.
type AnchorTopRight struct{ Y int }

func (a AnchorTopRight) Place(W, H, w, h int) (x, y int) {
	x = W - w
	if x < 0 {
		x = 0
	}
	return x, a.Y
}

// AnchorFractionalY centers horizontally and places the overlay's top edge at
// Frac*H rows down. Used by RenameOverlay (Frac=1/3) so the panel doesn't
// hide the thing being renamed.
type AnchorFractionalY struct{ Frac float64 }

func (a AnchorFractionalY) Place(W, H, w, h int) (x, y int) {
	x = (W - w) / 2
	if x < 0 {
		x = 0
	}
	y = int(float64(H) * a.Frac)
	if y < 1 {
		y = 1
	}
	return
}

// overlayKeyMsg carries a batch of stdin bytes diverted by the input pump
// while an interactive overlay is up. Update dispatches the whole batch to
// the topmost interactive overlay in one HandleKey call.
type overlayKeyMsg struct{ data []byte }

// pushOverlay appends to the stack (top of stack = end of slice) and flips
// the input-ownership atomic if o owns input, so the pump diverts the next
// chunk via overlayKeyMsg.
func (m *Model) pushOverlay(o Overlay) {
	m.overlays = append(m.overlays, o)
	if o.OwnsInput() {
		m.overlayOwnsInput.Store(true)
	}
}

// popOverlay removes the topmost overlay and re-syncs the input-ownership
// atomic to whether any remaining overlay still owns input.
func (m *Model) popOverlay() {
	if len(m.overlays) == 0 {
		return
	}
	m.overlays = m.overlays[:len(m.overlays)-1]
	m.overlayOwnsInput.Store(m.anyOverlayOwnsInput())
}

// removeOverlay drops a specific overlay from the stack by pointer
// equality. Used when an overlay closes itself non-topmost — e.g. a
// notice's tick fires while a confirm sits above it. Re-syncs the
// input-ownership atomic afterward. No-op if the overlay isn't on the
// stack (it may already have been popped by another path).
func (m *Model) removeOverlay(o Overlay) {
	for i := len(m.overlays) - 1; i >= 0; i-- {
		if m.overlays[i] == o {
			m.overlays = append(m.overlays[:i], m.overlays[i+1:]...)
			m.overlayOwnsInput.Store(m.anyOverlayOwnsInput())
			return
		}
	}
}

// topInteractiveOverlay walks the stack top-down and returns the first
// overlay with OwnsInput()==true, or nil. That's the one HandleKey targets;
// it's also what View consults to decide whether to suppress the pane's
// hardware cursor.
func (m *Model) topInteractiveOverlay() Overlay {
	for i := len(m.overlays) - 1; i >= 0; i-- {
		if m.overlays[i].OwnsInput() {
			return m.overlays[i]
		}
	}
	return nil
}

func (m *Model) anyOverlayOwnsInput() bool {
	for _, o := range m.overlays {
		if o.OwnsInput() {
			return true
		}
	}
	return false
}
