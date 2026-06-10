package main

import (
	tea "charm.land/bubbletea/v2"
)

func (m *Model) Init() tea.Cmd {
	// Kick off the read loop for every initial pane — once a ptyReadMsg
	// arrives, Update rearms readPty(pane) itself.
	cmds := []tea.Cmd{pollCmd()}
	for _, s := range m.sessions {
		for _, t := range s.tabs {
			for _, p := range t.panes() {
				cmds = append(cmds, readPty(p))
			}
		}
	}
	return tea.Batch(cmds...)
}

// applyLayoutSizes walks every tab and resizes each pane to the rect it
// occupies in the current window. Called after window resize, split, kill,
// and divider-drag — anything that changes a leaf's geometry.
func (m *Model) applyLayoutSizes() {
	inner := m.bodyHeight()
	for _, s := range m.sessions {
		for _, t := range s.tabs {
			rects := computeRects(t.root, 0, 0, m.w, inner)
			for pane, r := range rects {
				resizePane(pane, r.W, r.H)
			}
		}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.applyLayoutSizes()
		return m, nil

	case ptyReadMsg:
		// pty Read can return (n > 0, io.EOF) when the shell prints a final
		// message and exits in the same syscall. Write the bytes first so
		// that final output isn't dropped, then close on error.
		if len(msg.data) > 0 {
			writeWithSync(msg.pane, sanitizeOscC1(msg.pane, msg.data))
		}
		if msg.err != nil {
			return m.closePane(msg.pane)
		}
		return m, readPty(msg.pane)

	case wheelMsg:
		p := m.curPane()
		max := p.vt.Scrollback().Len()
		step := 3
		if msg.up {
			p.scrollOff += step
			if p.scrollOff > max {
				p.scrollOff = max
			}
		} else {
			p.scrollOff -= step
			if p.scrollOff < 0 {
				p.scrollOff = 0
			}
		}
		m.inScroll.Store(p.scrollOff > 0)
		return m, nil

	case snapMsg:
		p := m.curPane()
		p.scrollOff = 0
		m.inScroll.Store(false)
		return m, nil

	case pollMsg:
		// Fan out lsof+sysctl per pane as concurrent tea.Cmds so the main loop
		// stays responsive. Each returns a paneRefreshMsg consumed below.
		cmds := []tea.Cmd{pollCmd()}
		for _, s := range m.sessions {
			for _, t := range s.tabs {
				for _, p := range t.panes() {
					p := p
					cmds = append(cmds, func() tea.Msg { return refreshPane(p) })
				}
			}
		}
		return m, tea.Batch(cmds...)

	case paneRefreshMsg:
		msg.pane.fgCmd = msg.fgCmd
		if msg.cwd != "" {
			msg.pane.cwd = msg.cwd
		}
		return m, nil

	case mousePressMsg:
		// SGR coords are 1-indexed; the body starts below the tab bar.
		bx := msg.x - 1
		by := msg.y - 1 - tabBarH
		if by < 0 {
			// Click landed on the tab bar. Map x against the chip rects
			// produced by tabBarLayout — same function the renderer uses
			// so the rects always agree.
			_, chips := tabBarLayout(m)
			for i, r := range chips {
				if bx >= r.start && bx < r.end {
					m.swallowMouseRelease = true
					return m, m.actJumpTab(i)
				}
			}
			return m, nil
		}
		t := m.curTab()
		rects := computeRects(t.root, 0, 0, m.w, m.bodyHeight())
		var target *Pane
		for pane, r := range rects {
			if bx >= r.X && bx < r.X+r.W && by >= r.Y && by < r.Y+r.H {
				target = pane
				break
			}
		}
		if target == nil {
			return m, nil
		}
		if target != t.active {
			// Focus switch — swallow the press and the matching release so
			// no app sees a half click.
			t.active = target
			m.syncActive()
			m.swallowMouseRelease = true
			return m, nil
		}
		// Click landed in the active pane. Snap out of scrollback (matching
		// the keystroke behavior) and forward the raw SGR bytes.
		if target.scrollOff > 0 {
			target.scrollOff = 0
			m.inScroll.Store(false)
		}
		target.pty.Write(msg.data)
		return m, nil

	case mouseReleaseMsg:
		if m.swallowMouseRelease {
			m.swallowMouseRelease = false
			return m, nil
		}
		p := m.curPane()
		if p.scrollOff > 0 {
			p.scrollOff = 0
			m.inScroll.Store(false)
		}
		p.pty.Write(msg.data)
		return m, nil

	case prefixMsg:
		m.prefix = true
		return m, nil

	case prefixFollowMsg:
		m.prefix = false
		if msg.b == 0x1b { // esc cancels — no action
			return m, nil
		}
		if b := defaultKeymap.LookupPrefix(msg.b); b != nil {
			return m, b.Action.Run(&Ctx{M: m, Args: b.Args})
		}
		return m, nil

	case directKeyMsg:
		// Pump matched a byte to a direct binding. Dispatch like prefix-
		// follow but without the prefix-mode toggle since direct never
		// armed it. Re-lookup defensively so a config swap mid-flight
		// can't fire a stale action — defaultKeymap is replaced once at
		// startup so this is just future-proofing.
		if b := defaultKeymap.LookupDirect(msg.b); b != nil {
			return m, b.Action.Run(&Ctx{M: m, Args: b.Args})
		}
		return m, nil

	case noticeDismissMsg:
		// A notice's ttl tick fired. Remove that specific instance
		// (it may have been buried under a later overlay, so don't pop the top).
		m.removeOverlay(msg.notice)
		return m, nil

	case overlayKeyMsg:
		// Route to the topmost interactive overlay; if it returns closed,
		// drop that specific overlay (passive overlays may sit above it,
		// so don't blanket-pop the top).
		o := m.topInteractiveOverlay()
		if o == nil {
			return m, nil
		}
		closed, cmd := o.HandleKey(msg.data, m)
		if closed {
			m.removeOverlay(o)
		}
		return m, cmd
	}
	return m, nil
}
