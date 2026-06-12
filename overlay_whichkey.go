package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// menuBackSeqs holds the legacy encodings of an optional extra "up a level"
// key, configured via [whichkey] back in config.toml. Backspace always works;
// this is in addition. Keyed by the raw byte string the pump delivers, so the
// overlay can test membership without re-parsing. Empty = no extra key.
var menuBackSeqs = map[string]bool{}

// WhichKeyOverlay is the drill-down half of the which-key cheatsheet. Pressing
// a group leader runs menu.open, which pushes one of these with the root level
// at the bottom of its stack and the chosen group on top. From then on the
// stdin pump routes every keystroke here (menu.open carries FlagOwnsInput), so
// the follow keys resolve against the current level instead of leaking to the
// active pty.
//
// Navigation: a leader descends (push), Backspace (or a configured back key)
// steps up one level — from the first submenu that lands back on the root
// panel, the same view the armed prefix shows — and Esc cancels the whole menu
// at once. Each level is a *menuLevel borrowed from the live keymap, never
// mutated, so it's safe to hold across renders.
type WhichKeyOverlay struct {
	stack []*menuLevel
}

// NewWhichKeyOverlay builds an overlay over the given level stack (bottom →
// top). Callers pass the root level first so backing out of a submenu returns
// to the root panel rather than closing outright.
func NewWhichKeyOverlay(stack ...*menuLevel) *WhichKeyOverlay {
	return &WhichKeyOverlay{stack: append([]*menuLevel(nil), stack...)}
}

func (w *WhichKeyOverlay) cur() *menuLevel { return w.stack[len(w.stack)-1] }

func (w *WhichKeyOverlay) Anchor() Anchor  { return AnchorTopRight{Y: tabBarH} }
func (w *WhichKeyOverlay) OwnsInput() bool { return true }

// up steps back one level, returning whether the overlay should now close
// (true when the top level is all that's left).
func (w *WhichKeyOverlay) up() (closed bool) {
	if len(w.stack) > 1 {
		w.stack = w.stack[:len(w.stack)-1]
		return false
	}
	return true
}

func (w *WhichKeyOverlay) Render(m *Model) string {
	// Breadcrumb header: » PREFIX C-A › tabs › ... The root level has no title,
	// so it contributes no segment — a stack of just [root] reads as the plain
	// prefix panel.
	crumb := "» PREFIX C-A"
	for _, lvl := range w.stack {
		if lvl.title == "" {
			continue
		}
		crumb += " › " + strings.TrimPrefix(lvl.title, "+")
	}
	hint := "esc/backspace: cancel"
	if len(w.stack) > 1 {
		hint = "backspace: up a level · esc: cancel"
	}
	return renderMenuPanel(m, w.cur(), crumb, hint)
}

func (w *WhichKeyOverlay) HandleKey(data []byte, m *Model) (bool, tea.Cmd) {
	// Esc cancels the whole menu; Backspace steps up one level (closing only
	// when already at the bottom). Checked for a lone byte so a multi-byte
	// sequence whose first byte happens to be Esc (a legacy arrow, say) is
	// matched as a binding instead.
	if len(data) == 1 {
		switch data[0] {
		case 0x1b, 0x03: // esc / ctrl-c — cancel the whole menu
			return true, nil
		case 0x7f, 0x08: // backspace — up one level
			return w.up(), nil
		}
	}
	// A configured extra back key steps up too. It takes priority over a
	// same-key binding in the current level, so pick one that isn't used
	// inside the menus (backspace, the default, never is).
	if menuBackSeqs[string(data)] {
		return w.up(), nil
	}
	bd := w.cur().match(data)
	if bd == nil {
		return false, nil // unbound key — stay open, ignore
	}
	// A leader descends instead of dispatching.
	if bd.Action.ID == "menu.open" {
		if sub := defaultKeymap.menus[bd.Args.(*menuOpenArgs).group]; sub != nil {
			w.stack = append(w.stack, sub)
		}
		return false, nil
	}
	// Run the action and close. If the action itself opens an interactive
	// overlay (rename, pick, search — all FlagOwnsInput), its pushOverlay has
	// already run by the time we return closed=true, so removeOverlay drops
	// only this menu and the new overlay keeps input ownership.
	return true, bd.Action.Run(&Ctx{M: m, Args: bd.Args})
}
