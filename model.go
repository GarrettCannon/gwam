package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/vt"
)

// ---- pane ----

// Pane owns a single pty + emulator. Multiple Panes per Tab are laid out by
// the Layout tree; one Pane is "active" per Tab and receives input + cursor.
type Pane struct {
	title         string
	pty           *os.File
	vt            *vt.SafeEmulator
	cursorVisible bool
	// cursorStyle/cursorSteady track the child's last DECSCUSR (CSI Ps SP q)
	// request — the shape (block/underline/bar) and whether it's steady (not
	// blinking). cursorStyleSet stays false until the child first asks for a
	// shape; until then we suppress DECSCUSR so the host terminal keeps its
	// user-configured cursor. Honoring this is what lets neovim switch to a
	// block cursor in normal mode and a bar in insert mode. Set from the vt
	// CursorStyle callback (same goroutine as Write/View, see pty.go).
	cursorStyleSet bool
	cursorStyle    vt.CursorStyle
	cursorSteady   bool
	// scrollOff is lines scrolled above the live screen. 0 = live; positive
	// values walk back into vt's scrollback. The active pane's value is mirrored
	// to Model.inScroll so the stdin pump can snap back on the next keystroke.
	scrollOff int
	// shellPID is the spawned shell's PID; cwd is updated from OSC 7 (set by
	// fish/zsh on each prompt); fgCmd is the name of the current foreground
	// process when something other than the shell owns the pty's pgrp.
	shellPID int
	cwd      string
	fgCmd    string

	// Synchronized-output (DECSET 2026) bookkeeping. charmbracelet/x/vt
	// doesn't honor ?2026h/l, so while a sync block is open we render the
	// snapshot captured at ?2026h instead of the in-flight live screen —
	// otherwise readers see frame N+1's column-jump CHA writes interleaved
	// with frame N's leftover cells. See writeWithSync in pty.go.
	syncFrozen               bool
	syncSnapshot             string
	syncCursorX, syncCursorY int
	syncStartedAt            time.Time

	// inOsc tracks whether we're inside an \x1b]…\x07 OSC payload across pty
	// reads. vt's ansi parser treats 0x9C as the 8-bit ST terminator
	// unconditionally — but 0x9C is also a common UTF-8 continuation byte
	// (e.g., the middle byte of ✳ U+2733 = 0xE2 0x9C 0xB3), so any OSC with
	// non-ASCII content (like the "✳ Claude Code" title) ends the OSC mid-
	// payload and the rest of the bytes print as text on the screen. We
	// rewrite 0x9C → 0x20 inside OSC payloads to keep vt's parser inside OSC
	// state through the BEL. See sanitizeOscC1 in pty.go.
	inOsc bool
}

// Label is what we show in the tab chip when there's no custom name:
// the foreground command, else the basename of the current directory,
// else the emulator-supplied title.
func (p *Pane) Label() string {
	if p.fgCmd != "" {
		return p.fgCmd
	}
	if p.cwd != "" {
		return filepath.Base(p.cwd)
	}
	// Shells often set the title to "<cmd> <cwd>" (e.g. "fish /long/path")
	// before OSC 7 arrives. Drop the path tail so we don't flash it.
	if i := strings.IndexByte(p.title, ' '); i >= 0 {
		return p.title[:i]
	}
	return p.title
}

// ---- layout ----

type splitDir int

const (
	splitNone splitDir = iota
	splitV             // a left, b right (vertical divider between them)
	splitH             // a top, b bottom (horizontal divider between them)
)

// Layout is a binary tree of split nodes and leaves. A leaf has pane != nil
// and split == splitNone; a split has pane == nil, a non-zero split direction,
// and both children non-nil. parent points to the enclosing split (nil at the
// root). Splitting mutates a leaf in place to become a split node, with the
// original pane in child a and the new pane in child b — that keeps the
// parent's pointer to this leaf valid.
type Layout struct {
	parent *Layout
	pane   *Pane
	split  splitDir
	ratio  float64
	a, b   *Layout
}

func (l *Layout) IsLeaf() bool { return l.pane != nil }

// leaves returns every leaf under l in display order (a before b).
func (l *Layout) leaves() []*Layout {
	var out []*Layout
	var walk func(n *Layout)
	walk = func(n *Layout) {
		if n.IsLeaf() {
			out = append(out, n)
			return
		}
		walk(n.a)
		walk(n.b)
	}
	walk(l)
	return out
}

// findLeaf returns the leaf in this tree whose pane matches p, or nil.
func (l *Layout) findLeaf(p *Pane) *Layout {
	for _, leaf := range l.leaves() {
		if leaf.pane == p {
			return leaf
		}
	}
	return nil
}

// splitLeaf converts leaf into a split node along dir, placing the original
// pane in child a and newPane in child b at a 50/50 ratio. The caller is
// responsible for sizing the underlying ptys to the new rects.
func (l *Layout) splitLeaf(dir splitDir, newPane *Pane) {
	if !l.IsLeaf() {
		return
	}
	old := l.pane
	l.pane = nil
	l.split = dir
	l.ratio = 0.5
	l.a = &Layout{parent: l, pane: old}
	l.b = &Layout{parent: l, pane: newPane}
}

// collapseLeaf removes leaf from the tree by replacing its parent with the
// sibling's contents. Returns true if the layout still has a leaf, false if
// leaf was the root (caller must cascade to closeTab).
func (l *Layout) collapseLeaf(leaf *Layout) bool {
	if leaf.parent == nil {
		return false
	}
	parent := leaf.parent
	var sib *Layout
	if parent.a == leaf {
		sib = parent.b
	} else {
		sib = parent.a
	}
	// Absorb sibling into parent so the parent's parent's pointer remains
	// valid (we don't walk back up). After this, parent IS what sibling was.
	parent.pane = sib.pane
	parent.split = sib.split
	parent.ratio = sib.ratio
	parent.a = sib.a
	parent.b = sib.b
	if parent.a != nil {
		parent.a.parent = parent
	}
	if parent.b != nil {
		parent.b.parent = parent
	}
	return true
}

// nearestSplit walks from leaf toward the root, returning the closest
// ancestor split whose direction matches dir, and whether leaf descends from
// that split's a-child (vs b-child). Returns (nil, false) if no such split
// exists.
func (l *Layout) nearestSplit(dir splitDir) (*Layout, bool) {
	child := l
	for parent := l.parent; parent != nil; parent = parent.parent {
		if parent.split == dir {
			return parent, parent.a == child
		}
		child = parent
	}
	return nil, false
}

// ---- tab / session / model ----

type Tab struct {
	// customName, if non-empty, wins over the active pane's auto-derived label.
	// Set by the rename action; cleared by re-renaming to an empty string.
	customName string
	root       *Layout
	active     *Pane
	// zoomed renders the active pane at full body size without touching the
	// split tree. Cleared by anything that changes which panes are visible:
	// split, cycle, and pane death (see closePane).
	zoomed bool
}

// Label is what we show in the tab chip: explicit rename if set, else the
// active pane's auto-derived label.
func (t *Tab) Label() string {
	if t.customName != "" {
		return t.customName
	}
	if t.active != nil {
		return t.active.Label()
	}
	return ""
}

// panes returns every pane in this tab in layout order.
func (t *Tab) panes() []*Pane {
	leaves := t.root.leaves()
	out := make([]*Pane, len(leaves))
	for i, l := range leaves {
		out[i] = l.pane
	}
	return out
}

type Session struct {
	name   string
	tabs   []*Tab
	active int
	// lastTab is the previously active tab, for the tab.last flip. A pointer,
	// not an index — tabs can be removed (shifting indices) between the
	// switch that records it and the flip that uses it; a dead pointer just
	// means "nowhere to flip to". Cleared by removeTab when its tab dies.
	lastTab *Tab
	// popups are this session's named floating panes (see popup.go), keyed
	// by the popup.toggle binding's name arg. Lazily allocated on first
	// toggle. Hidden popups keep their pty alive — that's the point.
	popups map[string]*Popup
}

// switchTab records the outgoing tab for tab.last and moves to tab i.
// Every user-driven tab switch routes through here; forced moves (a tab
// dying under the cursor in removeTab) don't — flipping "back" to a tab the
// user never chose to leave would be noise.
func (s *Session) switchTab(i int) {
	if i == s.active {
		return
	}
	s.lastTab = s.tabs[s.active]
	s.active = i
}

type Model struct {
	sessions []*Session
	active   int
	prefix   bool
	w, h     int

	// lastSession is the previously active session, for the session.last
	// flip. Pointer for the same index-shift reasons as Session.lastTab.
	lastSession *Session

	// overlays is the popup stack — bottom→top, last element is on top.
	// The topmost interactive (OwnsInput) overlay receives diverted
	// keystrokes via overlayKeyMsg; passive overlays (toasts) just paint
	// over the base and don't intercept input.
	overlays []Overlay

	// shared with the stdin pump goroutine. mouseOn flips DECSET ?1000/?1006
	// on the outer tty; when on, wheel events arrive as SGR mouse sequences
	// instead of arrow keys (alt-screen scroll translation), at the cost of
	// breaking naive click-drag selection (hold ⌥/Option to select natively).
	// overlayOwnsInput mirrors "any interactive overlay is up" for the pump.
	activePty        *atomic.Pointer[os.File]
	mouseOn          *atomic.Bool
	inScroll         *atomic.Bool
	overlayOwnsInput *atomic.Bool

	// swallowMouseRelease is set when a press was consumed for pane focus
	// switch; the matching release is dropped instead of forwarded so apps in
	// the newly-focused pane don't see a release without a prior press.
	swallowMouseRelease bool
}

// ---- messages ----

type ptyReadMsg struct {
	pane *Pane
	data []byte
	err  error
}

type prefixMsg struct{}
type prefixFollowMsg struct{ b byte }
type directKeyMsg struct{ b byte }

// prefixFollowSeqMsg / directSeqMsg are the multi-byte (escape sequence)
// counterparts of prefixFollowMsg / directKeyMsg — seq is the raw byte
// string the pump matched against the keymap's sequence index.
type prefixFollowSeqMsg struct{ seq string }
type directSeqMsg struct{ seq string }
type wheelMsg struct{ up bool }
type snapMsg struct{}
type pollMsg struct{}    // fast tick: refresh each pane's foreground process
type cwdPollMsg struct{} // slow tick: refresh each pane's cwd (lsof)
type paneFgMsg struct {
	pane  *Pane
	fgCmd string
}
type paneCwdMsg struct {
	pane *Pane
	cwd  string
}

// mousePressMsg is a button-down SGR event with the original byte sequence
// and the click position (1-indexed, as the terminal reports it).
type mousePressMsg struct {
	data []byte
	x, y int
}

// mouseReleaseMsg is a button-up SGR event with the original byte sequence.
// We don't need coordinates — release routing is "to the currently active
// pane, unless we swallowed the matching press."
type mouseReleaseMsg struct {
	data []byte
}

// switchSession records the outgoing session for session.last and moves to
// session i. Same routing rule as Session.switchTab.
func (m *Model) switchSession(i int) {
	if i == m.active {
		return
	}
	m.lastSession = m.sessions[m.active]
	m.active = i
}

func (m *Model) curTab() *Tab {
	s := m.sessions[m.active]
	return s.tabs[s.active]
}

func (m *Model) curPane() *Pane {
	return m.curTab().active
}

// syncActive mirrors the focused pane (the visible popup's pane if any,
// else the active tab's active pane) into the atomics the stdin pump reads,
// so raw keystrokes land on whatever the user is looking at.
func (m *Model) syncActive() {
	p := m.focusPane()
	m.activePty.Store(p.pty)
	m.inScroll.Store(p.scrollOff > 0)
}

// closePane removes p from its tab. If the tab empties, it's removed too; if
// the session empties, it's removed too; if the last session goes, the
// program quits. Cascades like tmux all the way up.
func (m *Model) closePane(p *Pane) (tea.Model, tea.Cmd) {
	p.pty.Close()
	// Popup panes live outside the tab tree — a popup whose pty dies (shell
	// exited) is dropped from its session, and input falls back to the tab
	// pane if it was the visible one.
	for _, s := range m.sessions {
		for name, pu := range s.popups {
			if pu.pane == p {
				delete(s.popups, name)
				m.syncActive()
				return m, nil
			}
		}
	}
	for si, s := range m.sessions {
		for ti, t := range s.tabs {
			leaf := t.root.findLeaf(p)
			if leaf == nil {
				continue
			}
			if t.root.collapseLeaf(leaf) {
				// Tab survives — refocus only if the dying pane was the
				// active one; a background pane exiting shouldn't steal focus.
				if t.active == p {
					t.active = t.root.leaves()[0].pane
				}
				// The visible pane set changed: drop any zoom and resize the
				// survivors into the space the dead pane gave back.
				t.zoomed = false
				m.applyLayoutSizes()
				if si == m.active && ti == s.active {
					m.syncActive()
				}
				return m, nil
			}
			// Tab dies — drop it and cascade.
			return m, m.removeTab(si, ti)
		}
	}
	return m, nil
}

// removeTab drops tab ti from session si and cascades: the session is removed
// if it empties, and the program quits when no sessions remain. Active indices
// are shifted (not just clamped) so removing a tab/session *below* the active
// one doesn't silently move focus to a different neighbor — the pane the user
// was looking at stays focused. The caller is responsible for the tab's ptys.
func (m *Model) removeTab(si, ti int) tea.Cmd {
	s := m.sessions[si]
	if s.lastTab == s.tabs[ti] {
		s.lastTab = nil
	}
	s.tabs = append(s.tabs[:ti], s.tabs[ti+1:]...)
	if len(s.tabs) > 0 {
		if ti < s.active {
			s.active--
		} else if s.active >= len(s.tabs) {
			s.active = len(s.tabs) - 1
		}
		if si == m.active {
			m.syncActive()
		}
		return nil
	}
	// Session dies — close its popup ptys too. Their read loops will EOF
	// and emit ptyReadMsg, but closePane's lookups no-op because the
	// session is no longer reachable.
	for _, pu := range s.popups {
		pu.pane.pty.Close()
	}
	if m.lastSession == s {
		m.lastSession = nil
	}
	m.sessions = append(m.sessions[:si], m.sessions[si+1:]...)
	if len(m.sessions) == 0 {
		return tea.Quit
	}
	if si < m.active {
		m.active--
	} else if m.active >= len(m.sessions) {
		m.active = len(m.sessions) - 1
	}
	m.syncActive()
	return nil
}
