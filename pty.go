package main

import (
	"bytes"
	"net/url"
	"os"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// DECSET 2026 "synchronized output" markers. Apps wrap a multi-frame redraw
// in ?2026h … ?2026l to ask the host terminal to atomic-swap the screen at
// the end. charmbracelet/x/vt doesn't implement this, so we double-buffer at
// the consumer (gwam) layer: snapshot vt.Render() at ?2026h, return that
// snapshot from renderPaneBody until ?2026l (or timeout) arrives. Bytes
// still flow through to vt unchanged.
var (
	syncStart = []byte("\x1b[?2026h")
	syncEnd   = []byte("\x1b[?2026l")
)

// If ?2026l never arrives the freeze auto-releases after this long, so a
// buggy app can't wedge the pane forever. The check fires from renderPaneBody
// (pollMsg ticks at 500ms guarantee a render even if no pty bytes arrive).
const syncTimeout = 150 * time.Millisecond

func readPty(p *Pane) tea.Cmd {
	return func() tea.Msg {
		buf := make([]byte, 4096)
		n, err := p.pty.Read(buf)
		dlog("pty_in", buf[:n])
		return ptyReadMsg{pane: p, data: buf[:n], err: err}
	}
}

// SpawnOpts seeds a new pane from a template entry. Zero values are fine —
// they reproduce the default "bare shell, no init" behavior. Name only takes
// effect when the pane is the first (and at that point, only) pane of a tab,
// where it becomes the tab's customName.
type SpawnOpts struct {
	Name    string // sets the enclosing Tab's customName when wrapping this pane
	CWD     string // sets the spawned shell's working directory
	InitCmd string // written to the pty's stdin as one line; user keeps the shell after it exits
}

// spawnPane forks a shell under a fresh pty and wraps it in a Pane.
func spawnPane(rows, cols int, opts SpawnOpts) (*Pane, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if opts.CWD != "" {
		cmd.Dir = opts.CWD
	}

	f, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(rows), Cols: uint16(cols),
	})
	if err != nil {
		return nil, err
	}

	emu := vt.NewSafeEmulator(cols, rows)
	p := &Pane{
		title:         "shell",
		pty:           f,
		vt:            emu,
		cursorVisible: true,
		shellPID:      cmd.Process.Pid,
	}

	emu.SetCallbacks(vt.Callbacks{
		Title:            func(s string) { p.title = s },
		CursorVisibility: func(v bool) { p.cursorVisible = v },
		// DECSCUSR (CSI Ps SP q). The vt screen invokes this with !blink, so
		// the second arg is "steady" (true = not blinking) despite its name.
		CursorStyle: func(style vt.CursorStyle, steady bool) {
			p.cursorStyleSet = true
			p.cursorStyle = style
			p.cursorSteady = steady
		},
		// OSC 7 — fish/zsh emit on each prompt with a file:// URL pointing at
		// the shell's cwd. Strip the URL prefix so Label() can show the basename.
		WorkingDirectory: func(s string) {
			if u, err := url.Parse(s); err == nil && u.Path != "" {
				p.cwd = u.Path
			} else {
				p.cwd = s
			}
		},
	})

	// drain emulator-generated input (capability-query responses) and discard.
	// the emulator buffers these by default; if nothing reads them it eventually
	// blocks. we don't forward them anywhere — shells fall back to "unsupported"
	// when probes go unanswered.
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()

	// Init command — write to the pty master and let the shell pick it up
	// once it's read for input. The shell echoes the line (ECHO is on), runs
	// it, then prints another prompt — so the user lands back in a live shell
	// after the command exits ("shell-exec" semantics).
	if opts.InitCmd != "" {
		_, _ = f.Write([]byte(opts.InitCmd + "\n"))
	}

	return p, nil
}

// newSinglePaneTab builds a Tab whose layout is a single leaf wrapping pane.
// opts.Name (if any) becomes the Tab's customName.
func newSinglePaneTab(pane *Pane, name string) *Tab {
	root := &Layout{pane: pane}
	return &Tab{
		customName: name,
		root:       root,
		active:     pane,
	}
}

// resizePane updates a pane's pty winsize and emulator dimensions in lockstep.
func resizePane(p *Pane, w, h int) {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	// A snapshot captured at an old (w, h) no longer matches the pane rect,
	// so abandon any in-flight sync block on resize — the next render will
	// pull from the live screen at the new size.
	p.syncFrozen = false
	p.syncSnapshot = ""
	p.vt.Resize(w, h)
	_ = pty.Setsize(p.pty, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
}

// sanitizeOscC1 rewrites the byte 0x9C (8-bit ST, "string terminator") to
// 0x20 inside OSC payloads, leaving every other byte untouched. The ansi
// parser used by vt treats 0x9C as an unconditional OSC terminator, but the
// same byte appears legitimately as a UTF-8 continuation byte — most
// notably in ✳ (U+2733 → 0xE2 0x9C 0xB3) which Claude Code emits in its
// window-title OSC ("\x1b]0;✳ Claude Code\x07"). Without this rewrite, the
// parser bails on the second byte of ✳, dispatches the OSC with a truncated
// payload, and then prints "\xb3 Claude Code" to the live screen.
//
// OSC payloads only legitimately carry text, never an embedded C1 ST, so
// flattening 0x9C → 0x20 in payload position is safe. The 7-bit ST form
// ("\x1b\\") and BEL ("\x07") still terminate normally. Boundary state
// (mid-OSC at chunk end) lives on Pane.inOsc.
//
// Returns the original slice when no rewrite was needed; otherwise a fresh
// buffer. Never mutates the input — it may alias the pty read buffer that
// will be reused on the next read.
func sanitizeOscC1(p *Pane, data []byte) []byte {
	var out []byte
	inOsc := p.inOsc
	for i := 0; i < len(data); i++ {
		b := data[i]
		switch {
		case !inOsc:
			if b == 0x1b && i+1 < len(data) && data[i+1] == ']' {
				inOsc = true
			}
		case b == 0x9c:
			if out == nil {
				out = make([]byte, i, len(data))
				copy(out, data[:i])
			}
			out = append(out, 0x20)
			continue
		case b == 0x07:
			inOsc = false
		case b == 0x1b && i+1 < len(data) && data[i+1] == '\\':
			inOsc = false
		}
		if out != nil {
			out = append(out, b)
		}
	}
	p.inOsc = inOsc
	if out != nil {
		return out
	}
	return data
}

// writeWithSync forwards bytes to the vt emulator, watching for DECSET 2026
// markers so it can snapshot the screen at ?2026h and release the freeze at
// ?2026l. The bytes themselves still go through to vt — the emulator just
// ignores ?2026h/l as unknown modes. The visible effect lives entirely in
// renderPaneBody / the cursor read, which prefer Pane.syncSnapshot while
// frozen.
//
// Limitation: if a marker straddles two pty reads (split across ptyReadMsgs)
// we miss the boundary and the freeze either doesn't start, doesn't release,
// or releases via timeout. Real chunks from a localhost pty are 4096 bytes
// and the markers are 8 bytes each, so the probability is sub-percent — but
// not zero. If this turns up, buffer up to len(syncStart)-1 trailing bytes
// of `data` whose suffix matches the prefix of either marker and prepend
// them to the next call.
func writeWithSync(p *Pane, data []byte) {
	for len(data) > 0 {
		if !p.syncFrozen {
			i := bytes.Index(data, syncStart)
			if i < 0 {
				p.vt.Write(data)
				return
			}
			// Flush everything up to ?2026h so the snapshot reflects the
			// clean frame the app intended us to freeze on.
			if i > 0 {
				p.vt.Write(data[:i])
			}
			p.syncSnapshot = p.vt.Render()
			cp := p.vt.CursorPosition()
			p.syncCursorX, p.syncCursorY = cp.X, cp.Y
			p.syncStartedAt = time.Now()
			p.syncFrozen = true
			// Forward the marker itself — vt ignores it, but keeping the
			// byte stream intact means anything downstream that taps writes
			// (e.g. the pty_in debug log) sees the real traffic.
			p.vt.Write(syncStart)
			data = data[i+len(syncStart):]
			continue
		}
		// Frozen — bytes still mutate the live screen, but renderPaneBody
		// hides that until ?2026l arrives (or the timeout fires).
		i := bytes.Index(data, syncEnd)
		if i < 0 {
			p.vt.Write(data)
			return
		}
		p.vt.Write(data[:i+len(syncEnd)])
		p.syncFrozen = false
		p.syncSnapshot = ""
		data = data[i+len(syncEnd):]
	}
}

// writeMouseMode flips outer-tty mouse reporting. With it on, the host
// terminal emits SGR mouse events (\x1b[<...M/m) for clicks and wheel; with
// it off it falls back to native handling (selection, alt-screen wheel→arrow).
func writeMouseMode(on bool) {
	if on {
		os.Stdout.WriteString("\x1b[?1000h\x1b[?1006h")
	} else {
		os.Stdout.WriteString("\x1b[?1000l\x1b[?1006l")
	}
}

// pollCmd re-arms a periodic pollMsg used to refresh each pane's foreground
// process name. 500ms keeps the strip feeling reactive without sysctl thrash;
// the per-pane work (one ioctl + one sysctl) is cheap.
func pollCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return pollMsg{} })
}

// refreshPane is run off the main loop (the cwd lookup is ~10ms on macOS).
// It asks the kernel for the pty's foreground process group: if it isn't the
// shell, that's the command label. It also reads the shell's cwd — this is
// what makes the idle label work even when the shell doesn't emit OSC 7.
// Both fgCmdOfPgid and cwdOfPID are platform-specific (see platform_*.go).
func refreshPane(p *Pane) tea.Msg {
	out := paneRefreshMsg{pane: p}
	if pgid, err := unix.IoctlGetInt(int(p.pty.Fd()), unix.TIOCGPGRP); err == nil && pgid > 0 && pgid != p.shellPID {
		out.fgCmd = fgCmdOfPgid(pgid)
	}
	out.cwd = cwdOfPID(p.shellPID)
	return out
}
