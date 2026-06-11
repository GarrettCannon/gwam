package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Popup is a floating pane scoped to one session, toggled by the
// popup.toggle action. The pane's pty and read loop stay alive while
// hidden — visible only controls rendering and input routing — so a TUI
// like lazygit picks up exactly where it was when re-shown. At most one
// popup per session is visible at a time; showing one hides the others.
//
// A popup is not part of any tab's layout tree: it floats centered over
// the body, above pane content, below interactive overlays (picker,
// rename) and the prefix cheatsheet.
type Popup struct {
	name    string
	pane    *Pane
	visible bool
	// width/height are the fractions of the screen width / body height the
	// popup's outer border covers. Fixed when the popup is first created;
	// later toggles of the same name reuse the existing instance and ignore
	// the binding's size (and cmd/cwd) args.
	width, height float64
}

var popupBorder = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("220"))

// visiblePopup returns the current session's visible popup, or nil. At most
// one is visible per session (enforced by actPopupToggle), so first match
// wins.
func (m *Model) visiblePopup() *Popup {
	if len(m.sessions) == 0 {
		return nil
	}
	for _, pu := range m.sessions[m.active].popups {
		if pu.visible {
			return pu
		}
	}
	return nil
}

// focusPane is the pane that owns input and focus-driven UI (wheel scroll,
// the SCROLL chip, the hardware cursor): the visible popup's pane if there
// is one, else the active tab's active pane. syncActive mirrors this into
// the activePty/inScroll atomics for the stdin pump.
func (m *Model) focusPane() *Pane {
	if pu := m.visiblePopup(); pu != nil {
		return pu.pane
	}
	return m.curPane()
}

// popupRect is the popup's outer rect (border included) in screen
// coordinates, centered over the body. The inner pty area is (W-2, H-2).
// Recomputed from the stored fractions on every use so window resizes
// just work.
func (m *Model) popupRect(pu *Popup) Rect {
	w := int(float64(m.w) * pu.width)
	h := int(float64(m.bodyHeight()) * pu.height)
	if w > m.w {
		w = m.w
	}
	if h > m.bodyHeight() {
		h = m.bodyHeight()
	}
	// Floor at a usable inner area even on tiny windows — the compositor
	// clips overflow if the screen is smaller than this.
	if w < 4 {
		w = 4
	}
	if h < 3 {
		h = 3
	}
	x := (m.w - w) / 2
	if x < 0 {
		x = 0
	}
	y := tabBarH + (m.bodyHeight()-h)/2
	if y < tabBarH {
		y = tabBarH
	}
	return Rect{X: x, Y: y, W: w, H: h}
}

// renderPopup draws the popup's pane body inside its border at the rect
// computed by popupRect.
func renderPopup(pu *Popup, r Rect) string {
	return popupBorder.Render(renderPaneBody(pu.pane, r.W-2, r.H-2))
}

// actPopupToggle shows/hides the named popup in the current session,
// spawning it on first use. Hidden popups keep their pty (and whatever is
// running on it) alive; the read loop keeps draining so the emulator state
// is current when re-shown.
func (m *Model) actPopupToggle(a *popupToggleArgs) tea.Cmd {
	s := m.sessions[m.active]
	if pu := s.popups[a.name]; pu != nil {
		if pu.visible {
			pu.visible = false
		} else {
			for _, other := range s.popups {
				other.visible = false
			}
			pu.visible = true
		}
		m.syncActive()
		return nil
	}

	pu := &Popup{name: a.name, width: a.width, height: a.height}
	r := m.popupRect(pu)
	p, err := spawnPane(r.H-2, r.W-2, SpawnOpts{
		CWD:     expandHome(a.cwd),
		InitCmd: a.cmd,
	})
	if err != nil {
		return nil
	}
	pu.pane = p
	if s.popups == nil {
		s.popups = map[string]*Popup{}
	}
	for _, other := range s.popups {
		other.visible = false
	}
	pu.visible = true
	s.popups[a.name] = pu
	m.syncActive()
	return readPty(p)
}

// ---- popup.toggle args ----

// popupToggleArgs is the parsed form of popup.toggle's binding args. All
// fields are optional; an argless binding toggles a default 80%x80% shell
// popup named "default".
type popupToggleArgs struct {
	name   string  // popup identity within a session; distinct names coexist
	cmd    string  // init command written to the shell on first create
	cwd    string  // working directory on first create; ~ is expanded
	width  float64 // fraction of screen width, (0, 1]
	height float64 // fraction of body height, (0, 1]
}

func parsePopupToggleArgs(raw map[string]any) (any, error) {
	a := &popupToggleArgs{name: "default", width: 0.8, height: 0.8}
	for k, v := range raw {
		switch k {
		case "name":
			s, ok := v.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("popup.toggle: name must be a non-empty string")
			}
			a.name = s
		case "cmd":
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("popup.toggle: cmd must be a string, got %T", v)
			}
			a.cmd = s
		case "cwd":
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("popup.toggle: cwd must be a string, got %T", v)
			}
			a.cwd = s
		case "width", "height":
			f, err := popupFrac(k, v)
			if err != nil {
				return nil, err
			}
			if k == "width" {
				a.width = f
			} else {
				a.height = f
			}
		default:
			return nil, fmt.Errorf("popup.toggle: unknown arg %q (want name/cmd/cwd/width/height)", k)
		}
	}
	return a, nil
}

// popupFrac coerces a width/height arg to a fraction. Accepts a float
// fraction (0.8) or a whole-number percent (80); 1 means full size.
func popupFrac(key string, v any) (float64, error) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case int64:
		f = float64(n)
	case int:
		f = float64(n)
	default:
		return 0, fmt.Errorf("popup.toggle: %s must be a number, got %T", key, v)
	}
	if f > 1 {
		f /= 100
	}
	if f <= 0 || f > 1 {
		return 0, fmt.Errorf("popup.toggle: %s must be a fraction in (0, 1] or a percent in (1, 100]", key)
	}
	return f, nil
}
