package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// Each Run is a one-line wrapper around the existing act* methods in
// bindings.go — those still hold the real behavior. This file is purely
// the registry side: ID, display copy, args parsing, and the dispatch
// callable Run(*Ctx) tea.Cmd. Resize collapses four bindings into one
// parameterized action; tab.jump similarly handles 1-9 with an idx arg.

func init() {
	registerAction(&Action{
		ID: "tab.new", Label: "New tab", Group: "tabs",
		Help: "Open a new shell tab in the current session.",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actNewTab() },
	})
	registerAction(&Action{
		ID: "tab.next", Label: "Next tab", Group: "tabs",
		Help: "Switch to the next tab in the current session (wraps).",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actNextTab() },
	})
	registerAction(&Action{
		ID: "tab.prev", Label: "Previous tab", Group: "tabs",
		Help: "Switch to the previous tab in the current session (wraps).",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actPrevTab() },
	})
	registerAction(&Action{
		ID: "tab.last", Label: "Last tab", Group: "tabs",
		Help: "Flip to the previously active tab in the current session.",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actLastTab() },
	})
	registerAction(&Action{
		ID: "tab.rename", Label: "Rename tab", Group: "tabs",
		Help:  "Rename the current tab. Enter to confirm, Esc to cancel; empty clears.",
		Flags: FlagOwnsInput,
		Run:   func(c *Ctx) tea.Cmd { return c.M.actRenameTab() },
	})
	registerAction(&Action{
		ID: "tab.kill", Label: "Kill tab", Group: "tabs",
		Help: "Close the current tab and all its panes. Cascades to session / quit if it was the last tab.",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actKillTab() },
	})
	registerAction(&Action{
		ID: "tab.pick", Label: "Pick tab", Group: "tabs",
		Help:  "Open a search-and-pick list of tabs in the current session.",
		Flags: FlagOwnsInput,
		Run:   func(c *Ctx) tea.Cmd { return c.M.actPickTab() },
	})

	// tab.jump takes an idx arg (0-based); the keymap binds 1-9 as nine
	// entries with Args{idx: 0..8} so the dispatch path is uniform with
	// every other action.
	registerAction(&Action{
		ID: "tab.jump", Label: "Jump to tab", Group: "tabs",
		Help:  "Switch to the Nth tab in the current session.",
		Parse: parseTabJumpArgs,
		Run: func(c *Ctx) tea.Cmd {
			a := c.Args.(*tabJumpArgs)
			return c.M.actJumpTab(a.idx)
		},
	})

	registerAction(&Action{
		ID: "pane.split-v", Label: "Split pane vertically", Group: "panes",
		Help: "Split the active pane into a left/right pair (vertical divider).",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actSplitV() },
	})
	registerAction(&Action{
		ID: "pane.split-h", Label: "Split pane horizontally", Group: "panes",
		Help: "Split the active pane into a top/bottom pair (horizontal divider).",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actSplitH() },
	})
	registerAction(&Action{
		ID: "pane.cycle", Label: "Cycle pane", Group: "panes",
		Help: "Move focus to the next pane in the current tab (wraps).",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actCyclePane() },
	})
	// pane.select takes {dir = "left"|"right"|"up"|"down"} like pane.resize:
	// the four arrow bindings each pass a direction, and the overlay shows the
	// per-direction label derived at parse time.
	registerAction(&Action{
		ID: "pane.select", Label: "Select pane", Group: "panes",
		Help:  "Move focus to the nearest pane in the given direction.",
		Parse: parseSelectArgs,
		Run: func(c *Ctx) tea.Cmd {
			a := c.Args.(*selectArgs)
			return c.M.actSelectDir(a.dir, a.step)
		},
	})
	registerAction(&Action{
		ID: "pane.kill", Label: "Kill pane", Group: "panes",
		Help: "Close the active pane. If it's the only pane, the tab closes too (cascades).",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actKillPane() },
	})
	registerAction(&Action{
		ID: "pane.zoom", Label: "Zoom pane", Group: "panes",
		Help:   "Toggle the active pane to full size. The layout is untouched — unzoom restores it.",
		Status: zoomStatus,
		Run:    func(c *Ctx) tea.Cmd { return c.M.actZoomPane() },
	})
	registerAction(&Action{
		ID: "pane.search", Label: "Search output", Group: "panes",
		Help:  "Live mnil-style search over the active pane's output (scrollback + screen). Type to filter, tab to hop matches, ^f to show only matches, ^l to follow.",
		Flags: FlagOwnsInput,
		Run:   func(c *Ctx) tea.Cmd { return c.M.actSearchPane() },
	})

	// pane.resize takes {dir = "left"|"right"|"up"|"down"}. The four
	// historical bindings each pass a different dir; behavior, label, and
	// help row are derived from dir at parse-time so the overlay sees the
	// per-direction label users already know.
	registerAction(&Action{
		ID: "pane.resize", Label: "Resize pane", Group: "panes",
		Help:  "Move the nearest divider in the given direction by 5%.",
		Parse: parseResizeArgs,
		Run: func(c *Ctx) tea.Cmd {
			a := c.Args.(*resizeArgs)
			return c.M.actResize(a.dir, a.step)
		},
	})

	registerAction(&Action{
		ID: "session.new", Label: "New session", Group: "sessions",
		Help: "Create a new session containing one fresh shell tab.",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actNewSession() },
	})
	registerAction(&Action{
		ID: "session.next", Label: "Next session", Group: "sessions",
		Help: "Switch to the next session (wraps).",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actNextSession() },
	})
	registerAction(&Action{
		ID: "session.prev", Label: "Previous session", Group: "sessions",
		Help: "Switch to the previous session (wraps).",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actPrevSession() },
	})
	registerAction(&Action{
		ID: "session.kill", Label: "Kill session", Group: "sessions",
		Help: "Close the current session and all its tabs and panes. Quits gwam if it was the last session.",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actKillSession() },
	})
	registerAction(&Action{
		ID: "session.last", Label: "Last session", Group: "sessions",
		Help: "Flip to the previously active session.",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actLastSession() },
	})
	registerAction(&Action{
		ID: "session.pick", Label: "Pick session", Group: "sessions",
		Help:  "Open a search-and-pick list of all sessions.",
		Flags: FlagOwnsInput,
		Run:   func(c *Ctx) tea.Cmd { return c.M.actPickSession() },
	})
	registerAction(&Action{
		ID: "session.rename", Label: "Rename session", Group: "sessions",
		Help:  "Rename the current session. Enter to confirm, Esc to cancel.",
		Flags: FlagOwnsInput,
		Run:   func(c *Ctx) tea.Cmd { return c.M.actRenameSession() },
	})

	// popup.toggle shows/hides a named floating pane scoped to the current
	// session. Args (all optional): name distinguishes popups within a
	// session; cmd/cwd seed the shell on first create; width/height size
	// the popup as fractions (or percents) of the screen. The popup's pty
	// stays alive while hidden, so a TUI like lazygit resumes where it was.
	registerAction(&Action{
		ID: "popup.toggle", Label: "Toggle popup", Group: "popups",
		Help:  "Show/hide a floating popup pane in the current session. Keeps running while hidden.",
		Parse: parsePopupToggleArgs,
		Run: func(c *Ctx) tea.Cmd {
			return c.M.actPopupToggle(c.Args.(*popupToggleArgs))
		},
	})

	// menu.open opens a which-key submenu of related actions. Its arg names
	// the target group (a menu name shared with the submenu's bindings'
	// Menu field); FlagOwnsInput hands keystroke routing to the overlay it
	// pushes, so the follow keys resolve against the submenu rather than
	// leaking to the active pty.
	registerAction(&Action{
		ID: "menu.open", Label: "Open menu",
		Help:  "Open a which-key submenu of related actions.",
		Flags: FlagOwnsInput,
		Parse: parseMenuOpenArgs,
		Run: func(c *Ctx) tea.Cmd {
			return c.M.actOpenMenu(c.Args.(*menuOpenArgs).group)
		},
	})

	registerAction(&Action{
		ID: "mouse.toggle", Label: "Toggle mouse mode",
		Help:   "Enable host-terminal mouse reporting so the wheel scrolls history. Breaks native click-drag selection — hold Option to bypass.",
		Status: mouseStatus,
		Run:    func(c *Ctx) tea.Cmd { return c.M.actToggleMouse() },
	})
	registerAction(&Action{
		ID: "quit", Label: "Quit",
		Help: "Exit gwam, terminating all sessions and tabs.",
		Run:  func(c *Ctx) tea.Cmd { return c.M.actQuit() },
	})
}

// ---- typed args ----

type menuOpenArgs struct{ group string }

func parseMenuOpenArgs(raw map[string]any) (any, error) {
	g, ok := raw["group"].(string)
	if !ok || g == "" {
		return nil, fmt.Errorf("menu.open: missing group (the submenu name to open)")
	}
	return &menuOpenArgs{group: g}, nil
}

type tabJumpArgs struct{ idx int }

func parseTabJumpArgs(raw map[string]any) (any, error) {
	v, ok := raw["idx"]
	if !ok {
		return nil, fmt.Errorf("tab.jump: missing idx")
	}
	// TOML decodes integers as int64; the in-process keymap builder uses
	// plain int. Accept both so both paths work without a coerce helper.
	var idx int
	switch n := v.(type) {
	case int:
		idx = n
	case int64:
		idx = int(n)
	default:
		return nil, fmt.Errorf("tab.jump: idx must be int, got %T", v)
	}
	if idx < 0 {
		return nil, fmt.Errorf("tab.jump: idx must be >= 0")
	}
	return &tabJumpArgs{idx: idx}, nil
}

type resizeArgs struct {
	dir  splitDir
	step int
}

func parseResizeArgs(raw map[string]any) (any, error) {
	dir, step, err := parseDirStep("pane.resize", raw)
	if err != nil {
		return nil, err
	}
	return &resizeArgs{dir: dir, step: step}, nil
}

type selectArgs struct {
	dir  splitDir
	step int
}

func parseSelectArgs(raw map[string]any) (any, error) {
	dir, step, err := parseDirStep("pane.select", raw)
	if err != nil {
		return nil, err
	}
	return &selectArgs{dir: dir, step: step}, nil
}

// parseDirStep maps a {dir = "left"|"right"|"up"|"down"} arg to the (axis,
// step) pair shared by pane.resize and pane.select: splitV is the left/right
// axis, splitH up/down; step is -1 toward left/up, +1 toward right/down. id
// names the calling action so a bad config value points at the right binding.
func parseDirStep(id string, raw map[string]any) (splitDir, int, error) {
	d, ok := raw["dir"].(string)
	if !ok {
		return 0, 0, fmt.Errorf("%s: missing dir (left/right/up/down)", id)
	}
	switch d {
	case "left":
		return splitV, -1, nil
	case "right":
		return splitV, +1, nil
	case "up":
		return splitH, -1, nil
	case "down":
		return splitH, +1, nil
	}
	return 0, 0, fmt.Errorf("%s: dir must be left/right/up/down, got %q", id, d)
}
