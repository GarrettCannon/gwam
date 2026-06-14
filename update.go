package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// sgrMouse builds an SGR mouse report — ESC [ < btn ; x ; y (M|m) — with x,y
// as 1-indexed terminal coordinates. press selects the M (down) / m (up) final
// byte. Wheel events are reported as presses with btn 64 (up) / 65 (down).
func sgrMouse(btn, x, y int, press bool) []byte {
	final := byte('m')
	if press {
		final = 'M'
	}
	return fmt.Appendf(nil, "\x1b[<%d;%d;%d%c", btn, x, y, final)
}

// paneContentRect returns pane p's content area as a 0-indexed screen origin
// (x0, y0) and size (w, h): a point (sx, sy) maps to pane-local (sx-x0, sy-y0),
// in range when both lie within [1, w] / [1, h]. The terminal reports clicks in
// screen space, but the child app numbers its own grid from 1 at the pane's
// top-left, so the origin folds in the tab bar plus the layout rect (or the
// popup border) — without it a click resolves to the wrong cell (notably one
// row low under the tab bar). ok is false when p isn't currently on screen.
func (m *Model) paneContentRect(p *Pane) (x0, y0, w, h int, ok bool) {
	if pu := m.visiblePopup(); pu != nil && pu.pane == p {
		// Inner content starts one cell inside the border at popupRect's origin.
		r := m.popupRect(pu)
		return r.X + 1, r.Y + 1, r.W - 2, r.H - 2, true
	}
	rects, _ := m.curTab().geometry(m.w, m.bodyHeight())
	r, ok := rects[p]
	if !ok {
		return 0, 0, 0, 0, false
	}
	return r.X, r.Y + tabBarH, r.W, r.H, true
}

// paneLocalMouse maps 1-indexed screen coordinates to 1-indexed coordinates
// inside pane p's content area, returning ok=false when the point lies outside
// p (the tab bar, a popup border, or a different pane).
func (m *Model) paneLocalMouse(p *Pane, sx, sy int) (int, int, bool) {
	x0, y0, w, h, ok := m.paneContentRect(p)
	if !ok {
		return 0, 0, false
	}
	x, y := sx-x0, sy-y0
	if x < 1 || x > w || y < 1 || y > h {
		return 0, 0, false
	}
	return x, y, true
}

// paneLocalMouseClamped is paneLocalMouse but pins an out-of-bounds point to the
// nearest cell inside the pane instead of rejecting it. Used for button-up so an
// app that saw the press always sees the matching release even when the pointer
// drifted off the pane; ok is false only when p isn't on screen.
func (m *Model) paneLocalMouseClamped(p *Pane, sx, sy int) (int, int, bool) {
	x0, y0, w, h, ok := m.paneContentRect(p)
	if !ok {
		return 0, 0, false
	}
	return clampInt(sx-x0, 1, w), clampInt(sy-y0, 1, h), true
}

// paneAt returns the leaf pane under 1-indexed screen coords plus the pane-local
// coordinates, or ok=false for the tab bar and inter-pane gaps. A visible popup
// is modal: only its inner area hits, everything else returns ok=false.
func (m *Model) paneAt(sx, sy int) (*Pane, int, int, bool) {
	if pu := m.visiblePopup(); pu != nil {
		if x, y, ok := m.paneLocalMouse(pu.pane, sx, sy); ok {
			return pu.pane, x, y, true
		}
		return nil, 0, 0, false
	}
	bx, by := sx-1, sy-1-tabBarH
	if by < 0 {
		return nil, 0, 0, false // tab bar
	}
	rects, _ := m.curTab().geometry(m.w, m.bodyHeight())
	for pane, r := range rects {
		if bx >= r.X && bx < r.X+r.W && by >= r.Y && by < r.Y+r.H {
			return pane, bx - r.X + 1, by - r.Y + 1, true
		}
	}
	return nil, 0, 0, false
}

// exitScroll snaps a pane back to the live screen (out of scrollback) and clears
// the shared in-scroll flag. No-op when the pane is already at the bottom.
func (m *Model) exitScroll(p *Pane) {
	if p.scrollOff > 0 {
		p.scrollOff = 0
		m.inScroll.Store(false)
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (m *Model) Init() tea.Cmd {
	// Kick off the read loop for every initial pane — once a ptyReadMsg
	// arrives, Update rearms readPty(pane) itself.
	cmds := []tea.Cmd{pollCmd(), cwdPollCmd()}
	m.eachPane(func(p *Pane) { cmds = append(cmds, readPty(p)) })
	return tea.Batch(cmds...)
}

// eachPane calls fn for every pane across all sessions and tabs.
func (m *Model) eachPane(fn func(*Pane)) {
	for _, s := range m.sessions {
		for _, t := range s.tabs {
			for _, p := range t.panes() {
				fn(p)
			}
		}
	}
}

// applyLayoutSizes walks every tab and resizes each pane to the rect it
// occupies in the current window. Called after window resize, split, kill,
// and divider-drag — anything that changes a leaf's geometry.
func (m *Model) applyLayoutSizes() {
	inner := m.bodyHeight()
	for _, s := range m.sessions {
		for _, t := range s.tabs {
			// Zoomed tabs resize only the zoomed pane; the hidden panes keep
			// their last layout size and get their winch on unzoom.
			rects, divs := t.geometry(m.w, inner)
			for pane, r := range rects {
				c := contentRect(r, divs)
				resizePane(pane, c.W, c.H)
			}
		}
		// Hidden popups are resized too, so the app inside (which keeps
		// running) sees the winch now and is already laid out when re-shown.
		for _, pu := range s.popups {
			r := m.popupRect(pu)
			resizePane(pu.pane, r.W-2, r.H-2)
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
			// Reattach any partial 2026 marker held from the last read and hold
			// back a fresh trailing partial, so a marker split across reads is
			// seen whole by the scans below (else the freeze tears a frame).
			data := takeSyncCarry(msg.pane, msg.data)
			if len(data) > 0 {
				answerSyncQuery(msg.pane, data)
				writeWithSync(msg.pane, sanitizeOscC1(msg.pane, data))
			}
		}
		if msg.err != nil {
			return m.closePane(msg.pane)
		}
		return m, readPty(msg.pane)

	case wheelMsg:
		// On the alternate screen (nvim, htop, less, …) the app owns scrolling
		// and gwam has no scrollback to drive — forward the wheel to the pane
		// under the pointer as a pane-local SGR event (btn carries any modifier
		// bits). Apps that haven't enabled mouse reporting just ignore it;
		// without this the wheel was swallowed entirely, so nvim only scrolled
		// when outer mouse reporting was off.
		if hover, x, y, ok := m.paneAt(msg.x, msg.y); ok && hover.vt.IsAltScreen() {
			// Backpressure: don't pile wheel events onto an app that's still
			// painting the last scroll (its sync block is open). Forwarding
			// faster than it redraws builds a multi-second backlog — the app
			// stops sending its ?2026l, our freeze times out mid-frame, and we
			// render the half-painted live grid (a torn frame). Skipping while
			// frozen rate-limits us to the app's own frame cadence; the next
			// notch forwards as soon as it releases.
			if hover.syncFrozen {
				return m, nil
			}
			hover.pty.Write(sgrMouse(msg.btn, x, y, true))
			return m, nil
		}
		// Otherwise drive the focused pane's scrollback. A focused alt-screen
		// app whose pane isn't under the pointer gets nothing (matches the old
		// behavior, where the out-of-pane forward was dropped).
		p := m.focusPane()
		if p.vt.IsAltScreen() {
			return m, nil
		}
		up := msg.btn&1 == 0
		max := p.vt.Scrollback().Len()
		step := 3
		if up {
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
		m.exitScroll(m.focusPane())
		return m, nil

	case pollMsg:
		// Fast tick: refresh only the focused pane's foreground process — the
		// one pane whose cursor is rendered, so its DECSCUSR reset on program
		// exit feels prompt. Background panes ride the slow tick below.
		cmds := []tea.Cmd{pollCmd()}
		if p := m.focusPane(); p != nil {
			cmds = append(cmds, func() tea.Msg { return refreshFg(p) })
		}
		return m, tea.Batch(cmds...)

	case cwdPollMsg:
		// Slow tick: fan out the expensive lsof cwd lookup per pane, plus an fg
		// refresh so panes the fast tick skips still get a (less prompt) label.
		cmds := []tea.Cmd{cwdPollCmd()}
		m.eachPane(func(p *Pane) {
			cmds = append(cmds,
				func() tea.Msg { return refreshFg(p) },
				func() tea.Msg { return refreshCwd(p) },
			)
		})
		return m, tea.Batch(cmds...)

	case paneFgMsg:
		// fgCmd clearing means the foreground returned to the shell — a program
		// like neovim that set a cursor shape via DECSCUSR has exited. Drop the
		// tracked style so View falls back to suppression (CSI 0 SP q) and the
		// host terminal restores its user-configured cursor.
		if msg.pane.fgCmd != "" && msg.fgCmd == "" {
			msg.pane.cursorStyleSet = false
		}
		msg.pane.fgCmd = msg.fgCmd
		return m, nil

	case paneCwdMsg:
		if msg.cwd != "" {
			msg.pane.cwd = msg.cwd
		}
		return m, nil

	case mousePressMsg:
		// A visible popup is modal for the mouse: presses inside its inner
		// area forward to its pty; anything outside (tab bar included) is
		// swallowed so a click can't half-interact with what's underneath.
		if pu := m.visiblePopup(); pu != nil {
			if x, y, ok := m.paneLocalMouse(pu.pane, msg.x, msg.y); ok {
				m.exitScroll(pu.pane)
				pu.pane.pty.Write(sgrMouse(msg.btn, x, y, true))
			} else {
				m.swallowMouseRelease = true
			}
			return m, nil
		}
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
		rects, _ := t.geometry(m.w, m.bodyHeight())
		var target *Pane
		var tr Rect
		for pane, r := range rects {
			if bx >= r.X && bx < r.X+r.W && by >= r.Y && by < r.Y+r.H {
				target, tr = pane, r
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
		// Click landed in the active pane. Snap out of scrollback (matching the
		// keystroke behavior) and forward with pane-local coords derived from
		// the rect we already matched (in range by construction, so always sent).
		m.exitScroll(target)
		target.pty.Write(sgrMouse(msg.btn, bx-tr.X+1, by-tr.Y+1, true))
		return m, nil

	case mouseReleaseMsg:
		if m.swallowMouseRelease {
			m.swallowMouseRelease = false
			return m, nil
		}
		p := m.focusPane()
		m.exitScroll(p)
		// Always deliver the button-up (coords clamped into the pane): the app
		// saw the press, so it must see the release even if the pointer drifted
		// off the pane before release — otherwise it stays stuck mid-drag.
		if x, y, ok := m.paneLocalMouseClamped(p, msg.x, msg.y); ok {
			p.pty.Write(sgrMouse(msg.btn, x, y, false))
		}
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

	case prefixFollowSeqMsg:
		m.prefix = false
		if b := defaultKeymap.LookupPrefixSeq(msg.seq); b != nil {
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

	case directSeqMsg:
		// Sequence flavor of directKeyMsg — same defensive re-lookup.
		if b := defaultKeymap.LookupDirectSeq(msg.seq); b != nil {
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
