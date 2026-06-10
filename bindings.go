package main

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
)

// defaultBindings is the in-process keymap table — the only place that
// names a key and an action ID together. Display copy (Label/Help/Group)
// lives on the Action; per-binding Label here overrides the action's
// label in the overlay (used by pane.resize to show "Resize left" etc.
// instead of one collapsed "Resize pane" row).
//
// Built via a helper because tab.jump fans out across 1..9 with a
// per-binding idx arg — a plain slice literal can't hold the loop.
var defaultBindings = makeDefaultBindings()

// mustKey parses a key spec at init-time, panicking on error. Defaults
// are baked into the binary so a typo here is a programmer error, not a
// runtime condition worth handling — surfacing as a panic on startup is
// the loudest possible signal.
func mustKey(s string) Key {
	k, err := ParseKey(s)
	if err != nil {
		panic(fmt.Sprintf("default binding: invalid key spec %q: %v", s, err))
	}
	return k
}

// pre / dir build the two Trigger flavors in one line. Used only by
// makeDefaultBindings; config-loaded bindings go through ParseKey via
// configBindingsToSpecs.
func pre(s string) Trigger { return Trigger{Key: mustKey(s)} }
func dir(s string) Trigger { return Trigger{Key: mustKey(s), Direct: true} }

func makeDefaultBindings() []BindingSpec {
	bs := []BindingSpec{
		{Trigger: pre("c"), ActionID: "tab.new"},
		{Trigger: pre("n"), ActionID: "tab.next"},
		{Trigger: pre("p"), ActionID: "tab.prev"},
		{Trigger: pre(","), ActionID: "tab.rename"},
		{Trigger: pre("&"), ActionID: "tab.kill"},
		{Trigger: pre("|"), ActionID: "pane.split-v"},
		{Trigger: pre("_"), ActionID: "pane.split-h"},
		{Trigger: pre("o"), ActionID: "pane.cycle"},
		{Trigger: pre("x"), ActionID: "pane.kill"},
		{Trigger: pre("h"), ActionID: "pane.resize", Args: map[string]any{"dir": "left"}, Label: "Resize left"},
		{Trigger: pre("l"), ActionID: "pane.resize", Args: map[string]any{"dir": "right"}, Label: "Resize right"},
		{Trigger: pre("k"), ActionID: "pane.resize", Args: map[string]any{"dir": "up"}, Label: "Resize up"},
		{Trigger: pre("j"), ActionID: "pane.resize", Args: map[string]any{"dir": "down"}, Label: "Resize down"},
		{Trigger: pre("s"), ActionID: "session.new"},
		{Trigger: pre("w"), ActionID: "session.next"},
		{Trigger: pre("W"), ActionID: "session.pick"},
		{Trigger: pre("$"), ActionID: "session.rename"},
		{Trigger: pre("T"), ActionID: "tab.pick"},
		{Trigger: dir("ctrl-t"), ActionID: "tab.pick"},
		{Trigger: pre("m"), ActionID: "mouse.toggle"},
		{Trigger: pre("q"), ActionID: "quit"},
	}
	for i := 0; i < 9; i++ {
		bs = append(bs, BindingSpec{
			Trigger:  pre(string('1' + byte(i))),
			ActionID: "tab.jump",
			Args:     map[string]any{"idx": i},
		})
	}
	return bs
}

// ---- act* methods ----
//
// Each act* is the real behavior the Run wrappers in actions_builtin.go
// call through. They stayed methods on *Model (vs free funcs taking *Ctx)
// to keep this commit pure refactor: only call-sites moved, not bodies.

func mouseStatus(m *Model) string {
	if m.mouseOn.Load() {
		return "(on)"
	}
	return "(off)"
}

func (m *Model) actNewTab() tea.Cmd {
	p, err := spawnPane(m.bodyHeight(), m.w, SpawnOpts{})
	if err != nil {
		return nil
	}
	s := m.sessions[m.active]
	s.tabs = append(s.tabs, newSinglePaneTab(p, ""))
	s.active = len(s.tabs) - 1
	m.syncActive()
	return readPty(p)
}

func (m *Model) actNextTab() tea.Cmd {
	s := m.sessions[m.active]
	s.active = (s.active + 1) % len(s.tabs)
	m.syncActive()
	return nil
}

func (m *Model) actPrevTab() tea.Cmd {
	s := m.sessions[m.active]
	s.active = (s.active - 1 + len(s.tabs)) % len(s.tabs)
	m.syncActive()
	return nil
}

func (m *Model) actNewSession() tea.Cmd {
	p, err := spawnPane(m.bodyHeight(), m.w, SpawnOpts{})
	if err != nil {
		return nil
	}
	m.sessions = append(m.sessions, &Session{
		name: fmt.Sprintf("s%d", len(m.sessions)),
		tabs: []*Tab{newSinglePaneTab(p, "")},
	})
	m.active = len(m.sessions) - 1
	m.syncActive()
	return readPty(p)
}

func (m *Model) actNextSession() tea.Cmd {
	m.active = (m.active + 1) % len(m.sessions)
	m.syncActive()
	return nil
}

func (m *Model) actToggleMouse() tea.Cmd {
	on := !m.mouseOn.Load()
	m.mouseOn.Store(on)
	writeMouseMode(on)
	// Flash a toast so the user gets immediate feedback — the cheatsheet
	// status suffix is only visible while the prefix overlay is up.
	text := "mouse off"
	if on {
		text = "mouse on"
	}
	n := NewNoticeOverlay(text, 1500*time.Millisecond)
	m.pushOverlay(n)
	return n.DismissCmd()
}

func (m *Model) actQuit() tea.Cmd {
	return tea.Quit
}

func (m *Model) actRenameTab() tea.Cmd {
	t := m.curTab()
	// Empty buffer clears customName (reverts to the auto-derived label).
	// Capture t so the rename targets the tab the user invoked against,
	// even if the active tab moves before commit.
	m.pushOverlay(&RenameOverlay{
		title: "rename tab",
		buf:   t.customName,
		apply: func(name string) { t.customName = name },
	})
	return nil
}

func (m *Model) actRenameSession() tea.Cmd {
	s := m.sessions[m.active]
	// Empty buffer is treated as cancel: a nameless session chip looks
	// broken, so the apply func guards on name != "".
	m.pushOverlay(&RenameOverlay{
		title: "rename session",
		buf:   s.name,
		apply: func(name string) {
			if name != "" {
				s.name = name
			}
		},
	})
	return nil
}

// actPickSession opens a picker over every session. Items carry the
// *Session pointer, not its index — sessions can vanish via tab/pane
// cascade while the picker is up (a ptyReadMsg can still mutate state from
// under us), which shifts indices; the pointer is resolved back to its
// current position at commit time, and a vanished session is a no-op.
func (m *Model) actPickSession() tea.Cmd {
	items := make([]PickerItem, len(m.sessions))
	for i, s := range m.sessions {
		items[i] = PickerItem{Label: s.name, Data: s}
	}
	m.pushOverlay(NewPickerOverlay("sessions", items, func(it PickerItem) {
		target := it.Data.(*Session)
		for i, s := range m.sessions {
			if s == target {
				m.active = i
				m.syncActive()
				return
			}
		}
	}))
	return nil
}

// actPickTab opens a picker over the current session's tabs. Captures the
// session pointer at open-time so a session switch between open and pick
// can't redirect the result. Items carry the *Tab pointer, resolved back to
// its current index at commit time — a cascade can remove tabs (shifting
// indices) while the picker is up; a vanished tab is a no-op, and if the
// whole session died its tabs slice is empty so the loop finds nothing.
func (m *Model) actPickTab() tea.Cmd {
	s := m.sessions[m.active]
	items := make([]PickerItem, len(s.tabs))
	for i, t := range s.tabs {
		// Match against the bare name so typing "s" finds "shell" without
		// the user having to type past the "1 - " positional chrome.
		items[i] = PickerItem{
			Label:  fmt.Sprintf("%d - %s", i+1, t.Label()),
			Search: t.Label(),
			Data:   t,
		}
	}
	m.pushOverlay(NewPickerOverlay("tabs", items, func(it PickerItem) {
		target := it.Data.(*Tab)
		for i, t := range s.tabs {
			if t == target {
				s.active = i
				m.syncActive()
				return
			}
		}
	}))
	return nil
}

func (m *Model) actJumpTab(idx int) tea.Cmd {
	s := m.sessions[m.active]
	if idx < len(s.tabs) {
		s.active = idx
		m.syncActive()
	}
	return nil
}

// actSplitV/actSplitH split the active pane along the requested direction.
// The new pane lands in child b at a 50/50 ratio; the layout-aware resize
// pass that runs on every render adjusts each pane's pty size to its new rect.
func (m *Model) actSplitV() tea.Cmd { return m.split(splitV) }
func (m *Model) actSplitH() tea.Cmd { return m.split(splitH) }

func (m *Model) split(dir splitDir) tea.Cmd {
	t := m.curTab()
	leaf := t.root.findLeaf(t.active)
	if leaf == nil {
		return nil
	}
	// Spawn the new pane at the full body size; the resize pass on the next
	// render will trim both panes to their post-split rects.
	p, err := spawnPane(m.bodyHeight(), m.w, SpawnOpts{})
	if err != nil {
		return nil
	}
	leaf.splitLeaf(dir, p)
	t.active = p
	m.syncActive()
	m.applyLayoutSizes()
	return readPty(p)
}

// actCyclePane moves focus to the next leaf in layout order, wrapping at the
// end. Single-pane tabs are a no-op.
func (m *Model) actCyclePane() tea.Cmd {
	t := m.curTab()
	panes := t.panes()
	if len(panes) < 2 {
		return nil
	}
	idx := 0
	for i, p := range panes {
		if p == t.active {
			idx = i
			break
		}
	}
	t.active = panes[(idx+1)%len(panes)]
	m.syncActive()
	return nil
}

// actKillPane closes the active pane. If it was the last pane in the tab,
// closePane cascades up (tab → session → quit).
func (m *Model) actKillPane() tea.Cmd {
	p := m.curPane()
	_, cmd := m.closePane(p)
	m.applyLayoutSizes()
	return cmd
}

// actKillTab closes every pane in the current tab and removes the tab via
// the shared removeTab cascade (session removed if it empties, quit when no
// sessions remain). Each pane's pty is closed here; the read loops will
// see EOF and emit ptyReadMsg{err: ...}, but closePane's lookup will no-op
// because the panes are no longer reachable from any tab.
func (m *Model) actKillTab() tea.Cmd {
	s := m.sessions[m.active]
	t := s.tabs[s.active]
	for _, p := range t.panes() {
		p.pty.Close()
	}
	return m.removeTab(m.active, s.active)
}

// actResize finds the nearest ancestor split matching dir and nudges its
// ratio by step*0.05, clamped so neither child collapses below 1 cell.
func (m *Model) actResize(dir splitDir, step int) tea.Cmd {
	t := m.curTab()
	leaf := t.root.findLeaf(t.active)
	if leaf == nil {
		return nil
	}
	node, _ := leaf.nearestSplit(dir)
	if node == nil {
		return nil
	}
	const delta = 0.05
	node.ratio += float64(step) * delta
	if node.ratio < 0.1 {
		node.ratio = 0.1
	}
	if node.ratio > 0.9 {
		node.ratio = 0.9
	}
	m.applyLayoutSizes()
	return nil
}
