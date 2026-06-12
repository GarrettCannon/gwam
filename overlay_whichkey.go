package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// WhichKeyOverlay is the drill-down half of the which-key cheatsheet. The
// prefix panel shows the root level (group leaders + flat root actions);
// pressing a leader runs menu.open, which pushes one of these scoped to that
// group. From then on the stdin pump routes every keystroke here (the action
// carries FlagOwnsInput), so the follow keys resolve against the submenu
// instead of leaking to the active pty.
//
// The stack lets groups nest: descending into a subgroup pushes its level,
// Esc/Backspace pops back up, and Esc at the root level closes the overlay.
// Each level is a *menuLevel borrowed from the live keymap — never mutated,
// so it's safe to hold across renders.
type WhichKeyOverlay struct {
	stack []*menuLevel
}

func NewWhichKeyOverlay(root *menuLevel) *WhichKeyOverlay {
	return &WhichKeyOverlay{stack: []*menuLevel{root}}
}

func (w *WhichKeyOverlay) cur() *menuLevel { return w.stack[len(w.stack)-1] }

func (w *WhichKeyOverlay) Anchor() Anchor  { return AnchorTopRight{Y: tabBarH} }
func (w *WhichKeyOverlay) OwnsInput() bool { return true }

// pop drops the top level, returning whether the overlay should now close
// (true when there's nothing left to pop back to).
func (w *WhichKeyOverlay) pop() (closed bool) {
	if len(w.stack) > 1 {
		w.stack = w.stack[:len(w.stack)-1]
		return false
	}
	return true
}

func (w *WhichKeyOverlay) Render(m *Model) string {
	// Breadcrumb header: » PREFIX › tabs › ... — strip the leading "+" from
	// each level's title so it reads as a path rather than a row label.
	crumb := "» PREFIX"
	for _, lvl := range w.stack {
		crumb += " › " + strings.TrimPrefix(lvl.title, "+")
	}
	return renderMenuPanel(m, w.cur(), crumb, "esc back")
}

func (w *WhichKeyOverlay) HandleKey(data []byte, m *Model) (bool, tea.Cmd) {
	// Esc/Ctrl-C and Backspace navigate up a level (or close at root).
	// Check these only for a lone byte so a multi-byte sequence whose first
	// byte happens to be Esc (a legacy arrow, say) is matched as a binding.
	if len(data) == 1 {
		switch data[0] {
		case 0x1b, 0x03: // esc / ctrl-c
			return w.pop(), nil
		case 0x7f, 0x08: // backspace
			return w.pop(), nil
		}
	}
	bd := w.cur().match(data)
	if bd == nil {
		return false, nil // unbound key — stay open, ignore
	}
	// A leader inside a submenu descends instead of dispatching.
	if bd.Action.ID == "menu.open" {
		if sub := defaultKeymap.menus[bd.Args.(*menuOpenArgs).group]; sub != nil {
			w.stack = append(w.stack, sub)
		}
		return false, nil
	}
	// Run the action and close. If the action itself opens an interactive
	// overlay (rename, pick, search — all FlagOwnsInput), its pushOverlay
	// has already run by the time we return closed=true, so removeOverlay
	// drops only this menu and the new overlay keeps input ownership.
	return true, bd.Action.Run(&Ctx{M: m, Args: bd.Args})
}
