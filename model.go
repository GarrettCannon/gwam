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
}

type Model struct {
	sessions []*Session
	active   int
	prefix   bool
	w, h     int

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
type wheelMsg struct{ up bool }
type snapMsg struct{}
type pollMsg struct{}
type paneRefreshMsg struct {
	pane  *Pane
	fgCmd string
	cwd   string
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

func (m *Model) curTab() *Tab {
	s := m.sessions[m.active]
	return s.tabs[s.active]
}

func (m *Model) curPane() *Pane {
	return m.curTab().active
}

func (m *Model) syncActive() {
	p := m.curPane()
	m.activePty.Store(p.pty)
	m.inScroll.Store(p.scrollOff > 0)
}

// closePane removes p from its tab. If the tab empties, it's removed too; if
// the session empties, it's removed too; if the last session goes, the
// program quits. Cascades like tmux all the way up.
func (m *Model) closePane(p *Pane) (tea.Model, tea.Cmd) {
	p.pty.Close()
	for si, s := range m.sessions {
		for ti, t := range s.tabs {
			leaf := t.root.findLeaf(p)
			if leaf == nil {
				continue
			}
			if t.root.collapseLeaf(leaf) {
				// Tab survives — pick a new active pane (first leaf is fine).
				t.active = t.root.leaves()[0].pane
				if si == m.active && ti == s.active {
					m.syncActive()
				}
				return m, nil
			}
			// Tab dies — drop it and cascade.
			s.tabs = append(s.tabs[:ti], s.tabs[ti+1:]...)
			if len(s.tabs) > 0 {
				if s.active >= len(s.tabs) {
					s.active = len(s.tabs) - 1
				}
				if si == m.active {
					m.syncActive()
				}
				return m, nil
			}
			m.sessions = append(m.sessions[:si], m.sessions[si+1:]...)
			if len(m.sessions) == 0 {
				return m, tea.Quit
			}
			if m.active >= len(m.sessions) {
				m.active = len(m.sessions) - 1
			}
			m.syncActive()
			return m, nil
		}
	}
	return m, nil
}
