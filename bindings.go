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

// menuTitles maps a which-key submenu name to its display title (the header
// shown in the overlay and the label of its leader row at root). A name with
// no entry here falls back to "+<name>". Config can extend/override this via
// the [menus] table before the keymap is rebuilt.
var menuTitles = map[string]string{
	"tabs":     "+tabs",
	"panes":    "+panes",
	"sessions": "+sessions",
}

// menuTitle resolves a submenu's display title. Root ("") has no title.
func menuTitle(name string) string {
	if name == "" {
		return ""
	}
	if t, ok := menuTitles[name]; ok {
		return t
	}
	return "+" + name
}

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
		// Root level: group leaders open which-key submenus, plus a few
		// globals that don't belong to any noun. The submenu a leader opens
		// is named by its menu.open group arg and populated by the bindings
		// tagged with that same Menu below.
		{Trigger: pre("t"), ActionID: "menu.open", Args: map[string]any{"group": "tabs"}, Label: "+tabs"},
		{Trigger: pre("p"), ActionID: "menu.open", Args: map[string]any{"group": "panes"}, Label: "+panes"},
		{Trigger: pre("s"), ActionID: "menu.open", Args: map[string]any{"group": "sessions"}, Label: "+sessions"},
		{Trigger: pre("!"), ActionID: "popup.toggle", Args: map[string]any{"name": "scratch"}, Label: "Scratch popup"},
		{Trigger: pre("m"), ActionID: "mouse.toggle"},
		{Trigger: pre("q"), ActionID: "quit"},
		// ctrl-t fires the tab picker without the prefix — a direct
		// accelerator that bypasses the menu entirely.
		{Trigger: dir("ctrl-t"), ActionID: "tab.pick"},

		// +tabs — shared verb vocabulary: c create, n/p next/prev, l last,
		// x close, r rename, space pick.
		{Trigger: pre("c"), ActionID: "tab.new", Menu: "tabs"},
		{Trigger: pre("n"), ActionID: "tab.next", Menu: "tabs"},
		{Trigger: pre("p"), ActionID: "tab.prev", Menu: "tabs"},
		{Trigger: pre("l"), ActionID: "tab.last", Menu: "tabs"},
		{Trigger: pre("x"), ActionID: "tab.kill", Menu: "tabs"},
		{Trigger: pre("r"), ActionID: "tab.rename", Menu: "tabs"},
		{Trigger: pre("space"), ActionID: "tab.pick", Menu: "tabs"},

		// +sessions — the same verbs as +tabs, so muscle memory transfers.
		{Trigger: pre("c"), ActionID: "session.new", Menu: "sessions"},
		{Trigger: pre("n"), ActionID: "session.next", Menu: "sessions"},
		{Trigger: pre("p"), ActionID: "session.prev", Menu: "sessions"},
		{Trigger: pre("l"), ActionID: "session.last", Menu: "sessions"},
		{Trigger: pre("x"), ActionID: "session.kill", Menu: "sessions"},
		{Trigger: pre("r"), ActionID: "session.rename", Menu: "sessions"},
		{Trigger: pre("space"), ActionID: "session.pick", Menu: "sessions"},

		// +panes — pane-specific verbs (a pane isn't created/renamed like a
		// tab or session, so it carries its own set rather than the shared one).
		{Trigger: pre("|"), ActionID: "pane.split-v", Menu: "panes"},
		{Trigger: pre("_"), ActionID: "pane.split-h", Menu: "panes"},
		{Trigger: pre("o"), ActionID: "pane.cycle", Menu: "panes"},
		{Trigger: pre("left"), ActionID: "pane.select", Args: map[string]any{"dir": "left"}, Label: "Select left", Menu: "panes"},
		{Trigger: pre("right"), ActionID: "pane.select", Args: map[string]any{"dir": "right"}, Label: "Select right", Menu: "panes"},
		{Trigger: pre("up"), ActionID: "pane.select", Args: map[string]any{"dir": "up"}, Label: "Select up", Menu: "panes"},
		{Trigger: pre("down"), ActionID: "pane.select", Args: map[string]any{"dir": "down"}, Label: "Select down", Menu: "panes"},
		{Trigger: pre("z"), ActionID: "pane.zoom", Menu: "panes"},
		{Trigger: pre("x"), ActionID: "pane.kill", Menu: "panes"},
		{Trigger: pre("/"), ActionID: "pane.search", Menu: "panes"},
		{Trigger: pre("h"), ActionID: "pane.resize", Args: map[string]any{"dir": "left"}, Label: "Resize left", Menu: "panes"},
		{Trigger: pre("l"), ActionID: "pane.resize", Args: map[string]any{"dir": "right"}, Label: "Resize right", Menu: "panes"},
		{Trigger: pre("k"), ActionID: "pane.resize", Args: map[string]any{"dir": "up"}, Label: "Resize up", Menu: "panes"},
		{Trigger: pre("j"), ActionID: "pane.resize", Args: map[string]any{"dir": "down"}, Label: "Resize down", Menu: "panes"},
	}
	// tab.jump stays at root: digits don't collide with the verb letters and
	// jumping to a numbered tab is high-frequency enough to want one keystroke
	// after the prefix rather than a trip through +tabs.
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

// mouseStatus and zoomStatus report whether their toggle is currently on, as
// a non-empty string (the value is unused — the panel highlights the row when
// it's non-empty rather than printing it, so the row width doesn't change as
// the toggle flips).
func mouseStatus(m *Model) string {
	if m.mouseOn.Load() {
		return "on"
	}
	return ""
}

func (m *Model) actNewTab() tea.Cmd {
	p, err := spawnPane(m.bodyHeight(), m.w, SpawnOpts{})
	if err != nil {
		return nil
	}
	s := m.sessions[m.active]
	s.tabs = append(s.tabs, newSinglePaneTab(p, ""))
	s.switchTab(len(s.tabs) - 1)
	m.syncActive()
	return readPty(p)
}

func (m *Model) actNextTab() tea.Cmd {
	s := m.sessions[m.active]
	s.switchTab((s.active + 1) % len(s.tabs))
	m.syncActive()
	return nil
}

func (m *Model) actPrevTab() tea.Cmd {
	s := m.sessions[m.active]
	s.switchTab((s.active - 1 + len(s.tabs)) % len(s.tabs))
	m.syncActive()
	return nil
}

// actLastTab flips to the previously active tab in the current session —
// tmux's last-window. No-op until two tabs have been visited, or when the
// remembered tab has since been killed.
func (m *Model) actLastTab() tea.Cmd {
	s := m.sessions[m.active]
	if s.lastTab == nil {
		return nil
	}
	for i, t := range s.tabs {
		if t == s.lastTab {
			s.switchTab(i)
			m.syncActive()
			return nil
		}
	}
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
	m.switchSession(len(m.sessions) - 1)
	m.syncActive()
	return readPty(p)
}

func (m *Model) actNextSession() tea.Cmd {
	m.switchSession((m.active + 1) % len(m.sessions))
	m.syncActive()
	return nil
}

func (m *Model) actPrevSession() tea.Cmd {
	m.switchSession((m.active - 1 + len(m.sessions)) % len(m.sessions))
	m.syncActive()
	return nil
}

// actKillSession closes the current session and everything in it, cascading
// through removeTab (which drops the session when its last tab goes and quits
// gwam when no sessions remain). All ptys are closed up front; the per-tab
// read loops will EOF and emit ptyReadMsg, but closePane's lookups no-op once
// the panes are unreachable. Tabs are removed from the end so the doomed
// session's active index never has to shuffle.
func (m *Model) actKillSession() tea.Cmd {
	si := m.active
	s := m.sessions[si]
	for _, t := range s.tabs {
		for _, p := range t.panes() {
			p.pty.Close()
		}
	}
	var cmd tea.Cmd
	for len(m.sessions) > si && m.sessions[si] == s && len(s.tabs) > 0 {
		cmd = m.removeTab(si, len(s.tabs)-1)
	}
	return cmd
}

// actOpenMenu pushes a which-key overlay scoped to the named submenu, with the
// root level beneath it so backspace from the submenu returns to the root
// panel rather than closing. A leader pointing at an undefined group is
// rejected at keymap-build time, so a nil level here would only mean a
// programming error — guard anyway and no-op rather than panic on a keystroke.
func (m *Model) actOpenMenu(group string) tea.Cmd {
	lvl := defaultKeymap.menus[group]
	if lvl == nil {
		return nil
	}
	m.pushOverlay(NewWhichKeyOverlay(defaultKeymap.menus[""], lvl))
	return nil
}

// actLastSession flips to the previously active session — tmux's
// last-session. Same no-op rules as actLastTab.
func (m *Model) actLastSession() tea.Cmd {
	if m.lastSession == nil {
		return nil
	}
	for i, s := range m.sessions {
		if s == m.lastSession {
			m.switchSession(i)
			m.syncActive()
			return nil
		}
	}
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
				m.switchSession(i)
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
				s.switchTab(i)
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
		s.switchTab(idx)
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
	// Splitting while zoomed unzooms — the new pane must be visible.
	t.zoomed = false
	leaf.splitLeaf(dir, p)
	t.active = p
	m.syncActive()
	m.applyLayoutSizes()
	return readPty(p)
}

// actZoomPane toggles rendering the active pane at full body size. The
// split tree is untouched — unzooming restores the exact layout. No-op on
// single-pane tabs (the pane already fills the body).
func (m *Model) actZoomPane() tea.Cmd {
	t := m.curTab()
	if len(t.panes()) < 2 {
		return nil
	}
	t.zoomed = !t.zoomed
	m.applyLayoutSizes()
	return nil
}

func zoomStatus(m *Model) string {
	if m.curTab().zoomed {
		return "on"
	}
	return ""
}

// actSearchPane opens a live log-search overlay over the active pane. The
// pane pointer is captured now so a pane-cycle or tab-switch while the
// overlay is up can't redirect the search; follow starts on so the view
// tracks the tail of a streaming log until the user types or scrolls.
func (m *Model) actSearchPane() tea.Cmd {
	m.pushOverlay(&LogSearchOverlay{pane: m.curPane(), follow: true})
	return nil
}

// actCyclePane moves focus to the next leaf in layout order, wrapping at the
// end. Single-pane tabs are a no-op. Cycling while zoomed unzooms first —
// otherwise focus would move to a pane that isn't on screen.
func (m *Model) actCyclePane() tea.Cmd {
	t := m.curTab()
	panes := t.panes()
	if len(panes) < 2 {
		return nil
	}
	if t.zoomed {
		t.zoomed = false
		m.applyLayoutSizes()
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

// actSelectDir moves focus to the nearest pane in the given direction, using
// the same dir/step encoding as resize (splitV/-1 left … splitH/+1 down).
// No-op on single-pane tabs or when no pane lies that way. Selecting while
// zoomed unzooms first, otherwise focus would land on a pane that's off screen.
func (m *Model) actSelectDir(dir splitDir, step int) tea.Cmd {
	t := m.curTab()
	if len(t.panes()) < 2 {
		return nil
	}
	if t.zoomed {
		t.zoomed = false
		m.applyLayoutSizes()
	}
	rects, _ := t.geometry(m.w, m.bodyHeight())
	target := paneInDir(rects, t.active, dir, step)
	if target == nil || target == t.active {
		return nil
	}
	t.active = target
	m.syncActive()
	return nil
}

// actKillPane closes the active pane. If it was the last pane in the tab,
// closePane cascades up (tab → session → quit). closePane resizes the
// surviving panes itself.
func (m *Model) actKillPane() tea.Cmd {
	p := m.curPane()
	_, cmd := m.closePane(p)
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
