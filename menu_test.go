package main

import (
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// test.noop is a side-effect-free action so menu tests can exercise dispatch
// without spawning ptys. Registered once for the test binary.
func init() {
	registerAction(&Action{
		ID: "test.noop", Label: "Noop",
		Run: func(*Ctx) tea.Cmd { return nil },
	})
}

// testMenuSpecs builds a small three-level keymap shape used across the menu
// tests:
//
//	root:  t -> +tabs        (group leader)
//	+tabs: c -> test.noop, g -> +deep, up -> test.noop
//	+deep: d -> test.noop
func testMenuSpecs() []BindingSpec {
	return []BindingSpec{
		{Trigger: pre("t"), ActionID: "menu.open", Args: map[string]any{"group": "tabs"}, Label: "+tabs"},
		{Trigger: pre("c"), ActionID: "test.noop", Menu: "tabs"},
		{Trigger: pre("g"), ActionID: "menu.open", Args: map[string]any{"group": "deep"}, Menu: "tabs"},
		{Trigger: pre("up"), ActionID: "test.noop", Menu: "tabs"},
		{Trigger: pre("d"), ActionID: "test.noop", Menu: "deep"},
	}
}

func TestBuildKeymapPartitionsByMenu(t *testing.T) {
	km, err := buildKeymap(testMenuSpecs())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// Root holds the leader, not the submenu's actions.
	if bd := km.menus[""].byByte['t']; bd == nil || bd.Action.ID != "menu.open" {
		t.Fatalf("root 't' should be menu.open, got %v", bd)
	}
	if _, ok := km.menus[""].byByte['c']; ok {
		t.Errorf("'c' should not be at root — it lives in +tabs")
	}
	// Submenu holds its own actions.
	if bd := km.menus["tabs"].byByte['c']; bd == nil || bd.Action.ID != "test.noop" {
		t.Fatalf("+tabs 'c' should be test.noop, got %v", bd)
	}
}

// The headline property: the same key byte means different things at
// different levels without colliding, because each menu has its own index.
func TestSameKeyDistinctAcrossMenus(t *testing.T) {
	specs := []BindingSpec{
		{Trigger: pre("t"), ActionID: "menu.open", Args: map[string]any{"group": "tabs"}},
		{Trigger: pre("s"), ActionID: "menu.open", Args: map[string]any{"group": "sessions"}},
		{Trigger: pre("c"), ActionID: "test.noop", Menu: "tabs"},
		{Trigger: pre("c"), ActionID: "test.noop", Menu: "sessions"},
	}
	km, err := buildKeymap(specs)
	if err != nil {
		t.Fatalf("build: 'c' in two menus should not collide, got %v", err)
	}
	if km.menus["tabs"].byByte['c'] == nil || km.menus["sessions"].byByte['c'] == nil {
		t.Errorf("'c' should be bound in both +tabs and +sessions")
	}
}

// Mix-and-match: a flat action at a root key slot resolves as that action
// (one keystroke after the prefix), not as a menu — proving the engine
// doesn't force nesting.
func TestRootCanHoldFlatAction(t *testing.T) {
	specs := []BindingSpec{
		{Trigger: pre("p"), ActionID: "test.noop"}, // flat at root, no menu
	}
	km, err := buildKeymap(specs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	bd := km.LookupPrefix('p')
	if bd == nil || bd.Action.ID != "test.noop" {
		t.Fatalf("root 'p' should be the flat test.noop action, got %v", bd)
	}
}

// applyOverrides keys on (Trigger, Menu): adding a key to one menu must not
// replace the same key at root or in another menu — only an exact menu match
// overrides a default.
func TestApplyOverridesScopedByMenu(t *testing.T) {
	defaults := []BindingSpec{
		{Trigger: pre("s"), ActionID: "menu.open", Args: map[string]any{"group": "sessions"}},
		{Trigger: pre("c"), ActionID: "test.noop", Menu: "sessions"},
	}
	overrides := []BindingSpec{
		// same key "s", different menu — must append, not clobber root "s".
		{Trigger: pre("s"), ActionID: "test.noop", Menu: "git"},
		// same key "c" AND menu — must replace the default in place.
		{Trigger: pre("c"), ActionID: "quit", Menu: "sessions"},
	}
	merged := applyOverrides(defaults, overrides)

	var rootS, gitS, sessC *BindingSpec
	for i := range merged {
		s := &merged[i]
		switch {
		case s.Trigger == pre("s") && s.Menu == "":
			rootS = s
		case s.Trigger == pre("s") && s.Menu == "git":
			gitS = s
		case s.Trigger == pre("c") && s.Menu == "sessions":
			sessC = s
		}
	}
	if rootS == nil || rootS.ActionID != "menu.open" {
		t.Errorf("root 's' leader should survive, got %v", rootS)
	}
	if gitS == nil {
		t.Errorf("'s' in +git should be appended, not clobber root 's'")
	}
	if sessC == nil || sessC.ActionID != "quit" {
		t.Errorf("'c' in +sessions should be replaced in place, got %v", sessC)
	}
}

func TestBuildKeymapDuplicateWithinMenu(t *testing.T) {
	specs := []BindingSpec{
		{Trigger: pre("t"), ActionID: "menu.open", Args: map[string]any{"group": "tabs"}},
		{Trigger: pre("c"), ActionID: "test.noop", Menu: "tabs"},
		{Trigger: pre("c"), ActionID: "test.noop", Menu: "tabs"},
	}
	if _, err := buildKeymap(specs); err == nil {
		t.Fatalf("duplicate 'c' within +tabs should error")
	}
}

func TestBuildKeymapLeaderToEmptyGroup(t *testing.T) {
	specs := []BindingSpec{
		{Trigger: pre("t"), ActionID: "menu.open", Args: map[string]any{"group": "nope"}},
	}
	if _, err := buildKeymap(specs); err == nil {
		t.Fatalf("leader to an undefined/empty group should error")
	}
}

func TestMenuLevelMatch(t *testing.T) {
	km, err := buildKeymap(testMenuSpecs())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	tabs := km.menus["tabs"]
	if bd := tabs.match([]byte{'c'}); bd == nil || bd.Action.ID != "test.noop" {
		t.Errorf("byte match 'c' failed: %v", bd)
	}
	// "up" arrow is a multi-byte legacy CSI sequence — must resolve via bySeq.
	if bd := tabs.match([]byte("\x1b[A")); bd == nil || bd.Action.ID != "test.noop" {
		t.Errorf("seq match up-arrow failed: %v", bd)
	}
	if bd := tabs.match([]byte{'z'}); bd != nil {
		t.Errorf("unbound 'z' should not match, got %v", bd)
	}
}

func TestWhichKeyOverlayNavigation(t *testing.T) {
	km, err := buildKeymap(testMenuSpecs())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// The overlay descends via the global defaultKeymap, so point it at our
	// test keymap for the duration.
	old := defaultKeymap
	defaultKeymap = km
	defer func() { defaultKeymap = old }()

	m := &Model{}

	// Esc at the only level closes.
	o := NewWhichKeyOverlay(km.menus["tabs"])
	if closed, _ := o.HandleKey([]byte{0x1b}, m); !closed {
		t.Errorf("esc should close")
	}

	// Unbound key is ignored (stays open).
	o = NewWhichKeyOverlay(km.menus["tabs"])
	if closed, _ := o.HandleKey([]byte{'z'}, m); closed {
		t.Errorf("unbound key should not close")
	}

	// Running an action closes.
	o = NewWhichKeyOverlay(km.menus["tabs"])
	if closed, _ := o.HandleKey([]byte{'c'}, m); !closed {
		t.Errorf("running an action should close")
	}

	// Descend into a subgroup, then backspace pops back without closing.
	o = NewWhichKeyOverlay(km.menus["tabs"])
	if closed, _ := o.HandleKey([]byte{'g'}, m); closed {
		t.Fatalf("descending into +deep should not close")
	}
	if len(o.stack) != 2 || o.cur().name != "deep" {
		t.Fatalf("stack should be [tabs deep], got %v", o.stack)
	}
	if closed, _ := o.HandleKey([]byte{0x7f}, m); closed {
		t.Errorf("backspace from a subgroup should pop, not close")
	}
	if len(o.stack) != 1 || o.cur().name != "tabs" {
		t.Fatalf("after pop, stack should be [tabs], got %v", o.stack)
	}
	// Backspace at the last level closes.
	if closed, _ := o.HandleKey([]byte{0x7f}, m); !closed {
		t.Errorf("backspace at the last level should close")
	}

	// Esc cancels the whole menu from depth in one press, rather than
	// stepping up a level.
	o = NewWhichKeyOverlay(km.menus["tabs"])
	o.HandleKey([]byte{'g'}, m) // -> [tabs deep]
	if closed, _ := o.HandleKey([]byte{0x1b}, m); !closed {
		t.Errorf("esc from a subgroup should cancel the whole menu")
	}
}

// With the root level at the bottom of the stack (how actOpenMenu builds it),
// backspace from the first submenu returns to the root panel instead of
// closing — that's "back to the prefix state".
func TestWhichKeyBackToRoot(t *testing.T) {
	km, err := buildKeymap(testMenuSpecs())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	o := NewWhichKeyOverlay(km.menus[""], km.menus["tabs"])
	if closed, _ := o.HandleKey([]byte{0x7f}, m_or_nil()); closed {
		t.Fatalf("backspace from +tabs should return to root, not close")
	}
	if o.cur().name != "" {
		t.Fatalf("should be back at root level, got %q", o.cur().name)
	}
	// Now at root, backspace closes.
	if closed, _ := o.HandleKey([]byte{0x7f}, m_or_nil()); !closed {
		t.Errorf("backspace at root should close")
	}
}

// A configured extra back key steps up a level just like backspace.
func TestWhichKeyConfigurableBackKey(t *testing.T) {
	km, err := buildKeymap(testMenuSpecs())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	old := menuBackSeqs
	menuBackSeqs = map[string]bool{"b": true}
	defer func() { menuBackSeqs = old }()

	o := NewWhichKeyOverlay(km.menus[""], km.menus["tabs"])
	if closed, _ := o.HandleKey([]byte{'b'}, m_or_nil()); closed {
		t.Fatalf("configured back key from +tabs should return to root, not close")
	}
	if o.cur().name != "" {
		t.Fatalf("should be back at root level, got %q", o.cur().name)
	}
}

// m_or_nil returns a Model safe for HandleKey paths that don't dispatch
// pty-spawning actions (navigation only).
func m_or_nil() *Model { return &Model{} }

// Every level should render at the same panel width so switching groups
// doesn't resize the panel. (testMenuSpecs uses status-free actions, so a
// zero Model is safe here.)
func TestMenuPanelStableWidth(t *testing.T) {
	km, err := buildKeymap(testMenuSpecs())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	old := defaultKeymap
	defaultKeymap = km
	defer func() { defaultKeymap = old }()

	m := &Model{}
	wr := lipgloss.Width(renderMenuPanel(m, km.menus[""], "» PREFIX", "esc"))
	wt := lipgloss.Width(renderMenuPanel(m, km.menus["tabs"], "» PREFIX", "esc"))
	wd := lipgloss.Width(renderMenuPanel(m, km.menus["deep"], "» PREFIX", "esc"))
	if wr != wt || wt != wd {
		t.Errorf("panel widths differ across levels: root=%d tabs=%d deep=%d", wr, wt, wd)
	}
}

// No binding row should wrap — including rows carrying a status suffix like
// the mouse mode "(off)". Renders the real default panels (a zero Model is
// safe: mouseStatus reads an atomic and zoomStatus guards the empty model).
func TestMenuPanelNoWrap(t *testing.T) {
	// A minimal but valid model: zoomStatus reads curTab().zoomed and
	// mouseStatus reads the mouseOn atomic.
	m := &Model{
		sessions: []*Session{{tabs: []*Tab{{}}}},
		mouseOn:  &atomic.Bool{},
	}
	for _, name := range []string{"", "tabs", "panes", "sessions"} {
		lvl := defaultKeymap.menus[name]
		panel := renderMenuPanel(m, lvl, "» PREFIX C-A", "⌫/esc cancel")
		// content rows = title + blank + bindings + blank + hint; the bordered,
		// padded panel adds 4 frame rows (1 border + 1 padding, top and bottom).
		want := (2 + len(menuLines(m, lvl)) + 2) + 4
		if got := lipgloss.Height(panel); got != want {
			t.Errorf("menu %q panel height %d, want %d — a row wrapped", name, got, want)
		}
	}
}

// Toggling a status (mouse on/off) must change neither the panel width nor its
// height: the row is highlighted, not re-texted, so nothing reflows.
func TestMenuPanelStatusDoesNotResize(t *testing.T) {
	root := defaultKeymap.menus[""]
	mouse := &atomic.Bool{}
	m := &Model{sessions: []*Session{{tabs: []*Tab{{}}}}, mouseOn: mouse}

	mouse.Store(false)
	off := renderMenuPanel(m, root, "» PREFIX C-A", "esc")
	mouse.Store(true)
	on := renderMenuPanel(m, root, "» PREFIX C-A", "esc")

	if lipgloss.Width(off) != lipgloss.Width(on) {
		t.Errorf("panel width changed with mouse toggle: off=%d on=%d",
			lipgloss.Width(off), lipgloss.Width(on))
	}
	if lipgloss.Height(off) != lipgloss.Height(on) {
		t.Errorf("panel height changed with mouse toggle: off=%d on=%d",
			lipgloss.Height(off), lipgloss.Height(on))
	}
}

// The shipped defaults should build with the three standard groups present.
func TestDefaultKeymapMenus(t *testing.T) {
	for _, name := range []string{"", "tabs", "panes", "sessions"} {
		if defaultKeymap.menus[name] == nil {
			t.Errorf("default keymap missing menu %q", name)
		}
	}
	// 'p' is the +panes leader at root, but tab.prev inside +tabs — the same
	// byte, two meanings.
	if bd := defaultKeymap.menus[""].byByte['p']; bd == nil || bd.Action.ID != "menu.open" {
		t.Errorf("root 'p' should be the +panes leader, got %v", bd)
	}
	if bd := defaultKeymap.menus["tabs"].byByte['p']; bd == nil || bd.Action.ID != "tab.prev" {
		t.Errorf("+tabs 'p' should be tab.prev, got %v", bd)
	}
}
