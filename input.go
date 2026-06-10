package main

import (
	"os"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
)

// startInputPump owns the outer-tty stdin: it intercepts Ctrl-A to arm the
// prefix, decodes kitty CSI-u keystrokes back to legacy bytes, drops terminal
// device-report responses, and forwards everything else to the active tab's
// pty. While an interactive overlay is up (overlayOwnsInput), raw bytes are
// diverted to overlayKeyMsg instead of the pty. The pump exits when stdin
// closes.
func startInputPump(
	p *tea.Program,
	activePty *atomic.Pointer[os.File],
	mouseOn, inScroll, overlayOwnsInput *atomic.Bool,
) {
	go func() {
		buf := make([]byte, 4096)
		armed := false
		// tryDirect checks whether a single decoded byte is bound as a
		// direct keystroke. If so, dispatches via directKeyMsg, pre-arms
		// overlay-owns-input when the action opens an interactive
		// overlay (so any same-chunk follow bytes route through
		// overlayKeyMsg instead of leaking to the active pty), and
		// returns true. The caller skips forwarding the byte. Skipped
		// while an overlay already owns input so the user can type the
		// direct key into a picker query without firing it.
		tryDirect := func(b byte) bool {
			if overlayOwnsInput.Load() {
				return false
			}
			bd := defaultKeymap.LookupDirect(b)
			if bd == nil {
				return false
			}
			if bd.Action.Flags&FlagOwnsInput != 0 {
				overlayOwnsInput.Store(true)
			}
			p.Send(directKeyMsg{b: b})
			return true
		}
		writePty := func(b []byte) {
			if len(b) == 0 {
				return
			}
			// An interactive overlay owns input — divert the whole slice as
			// one overlayKeyMsg so a K-byte paste is one Update + one render,
			// not K. We copy because `b` aliases the pump's read buffer
			// which is reused on the next Read.
			if overlayOwnsInput.Load() {
				data := make([]byte, len(b))
				copy(data, b)
				p.Send(overlayKeyMsg{data: data})
				return
			}
			// any pty-bound input snaps us out of scrollback so the user
			// doesn't type into a frozen view. snap before the bytes land
			// so the next render shows the live screen.
			if inScroll.Load() {
				p.Send(snapMsg{})
			}
			if f := activePty.Load(); f != nil {
				f.Write(b)
			}
		}
		kittyCtrlA := []byte("\x1b[97;5u")
		matchPrefix := func(c []byte, i int) int {
			if c[i] == 0x01 {
				return 1
			}
			if i+len(kittyCtrlA) <= len(c) &&
				string(c[i:i+len(kittyCtrlA)]) == string(kittyCtrlA) {
				return len(kittyCtrlA)
			}
			return 0
		}
		// SGR mouse event: \x1b[<{btn};{x};{y}{M|m}. Returns the consumed
		// length, the button code, the 1-indexed (x, y), and whether this is
		// a press (M) or release (m). Only matches when mouseOn so we don't
		// accidentally chew binary that happens to look mouse-shaped while
		// the terminal isn't reporting. Wheel codes are 64 (up), 65 (down),
		// 66/67 (h-scroll); buttons are <64.
		matchMouseSGR := func(c []byte, i int) (consumed, btn, x, y int, press bool) {
			if !mouseOn.Load() || i+2 >= len(c) || c[i] != 0x1b || c[i+1] != '[' || c[i+2] != '<' {
				return 0, 0, 0, 0, false
			}
			j := i + 3
			readNum := func() (int, bool) {
				v, n := 0, 0
				for ; j < len(c) && c[j] >= '0' && c[j] <= '9'; j++ {
					v = v*10 + int(c[j]-'0')
					n++
				}
				return v, n > 0
			}
			var ok bool
			if btn, ok = readNum(); !ok || j >= len(c) || c[j] != ';' {
				return 0, 0, 0, 0, false
			}
			j++
			if x, ok = readNum(); !ok || j >= len(c) || c[j] != ';' {
				return 0, 0, 0, 0, false
			}
			j++
			if y, ok = readNum(); !ok || j >= len(c) {
				return 0, 0, 0, 0, false
			}
			if c[j] != 'M' && c[j] != 'm' {
				return 0, 0, 0, 0, false
			}
			press = c[j] == 'M'
			return j - i + 1, btn, x, y, press
		}
		// Kitty progressive enhancement encodes keystrokes as
		// \x1b[<codepoint>(;<mods>(;<event>))u. Bubbletea pushes this mode at
		// startup so the host terminal sends it to us, but the shells we wrap
		// don't understand it — so we decode here and forward the legacy bytes.
		// Returns (length, replacement) on match; replacement is nil to drop.
		matchKittyCSIu := func(c []byte, i int) (int, []byte) {
			if i+2 >= len(c) || c[i] != 0x1b || c[i+1] != '[' {
				return 0, nil
			}
			j := i + 2
			cp, n := 0, 0
			for ; j < len(c) && c[j] >= '0' && c[j] <= '9'; j++ {
				cp = cp*10 + int(c[j]-'0')
				n++
			}
			if n == 0 {
				return 0, nil
			}
			mods := 1
			if j < len(c) && c[j] == ';' {
				j++
				m, n2 := 0, 0
				for ; j < len(c) && c[j] >= '0' && c[j] <= '9'; j++ {
					m = m*10 + int(c[j]-'0')
					n2++
				}
				if n2 == 0 {
					return 0, nil
				}
				mods = m
				// optional ;event-type ignored (we only forward presses anyway)
				if j < len(c) && c[j] == ';' {
					for j < len(c) && c[j] != 'u' && (c[j] < 0x40 || c[j] > 0x7e) {
						j++
					}
				}
			}
			if j >= len(c) || c[j] != 'u' {
				return 0, nil
			}
			consumed := j - i + 1

			mbits := mods - 1
			shift, alt, ctrl := mbits&1 != 0, mbits&2 != 0, mbits&4 != 0
			var b byte
			switch {
			case cp >= 'a' && cp <= 'z':
				b = byte(cp)
				if ctrl {
					b = byte(cp - 'a' + 1)
				} else if shift {
					b = byte(cp - 32)
				}
			case cp >= 0x20 && cp < 0x7f:
				b = byte(cp)
			case cp == 9, cp == 13, cp == 27, cp == 127:
				b = byte(cp)
			default:
				return consumed, nil
			}
			if alt {
				return consumed, []byte{0x1b, b}
			}
			return consumed, []byte{b}
		}
		// incompleteSeqTail reports whether c[i:] is the start of a CSI or
		// OSC sequence whose terminator hasn't arrived in this chunk. Such a
		// tail is held back (pending) and prepended to the next read so a
		// sequence split across read boundaries — a mouse SGR event, a kitty
		// CSI-u keystroke, a device report — doesn't leak fragments to the
		// pty (with ECHO on, half a mouse event echoes as text). A lone
		// trailing ESC is NOT held: it's far more likely the Esc key than a
		// sequence split exactly after its first byte, and holding it would
		// delay the keypress until the next read.
		incompleteSeqTail := func(c []byte, i int) bool {
			if c[i] != 0x1b || i+1 >= len(c) {
				return false
			}
			switch c[i+1] {
			case '[':
				for j := i + 2; j < len(c); j++ {
					if c[j] >= 0x40 && c[j] <= 0x7e {
						return false // complete CSI — the matchers had their chance
					}
				}
				return true
			case ']':
				for j := i + 2; j < len(c); j++ {
					if c[j] == 0x07 {
						return false
					}
					if c[j] == 0x1b && j+1 < len(c) && c[j+1] == '\\' {
						return false
					}
				}
				return true
			}
			return false
		}
		// Carry-over cap: a tail that keeps growing past this without a
		// terminator isn't a real control sequence (e.g. cat-ing a binary
		// with a stray ESC[) — give up and forward it raw, as before.
		const maxPendingSeq = 256
		var pending []byte
		// private CSI (\x1b[?...{final}) and OSC (\x1b]...{BEL|ST}) are never
		// keystrokes — they're terminal device responses.
		skipDeviceReport := func(c []byte, i int) int {
			if i+1 >= len(c) || c[i] != 0x1b {
				return 0
			}
			switch c[i+1] {
			case '[':
				if i+2 >= len(c) || c[i+2] != '?' {
					return 0
				}
				for j := i + 3; j < len(c); j++ {
					b := c[j]
					if b >= 0x40 && b <= 0x7e {
						return j - i + 1
					}
					if b < 0x20 || b > 0x3f {
						return 0
					}
				}
			case ']':
				for j := i + 2; j < len(c); j++ {
					if c[j] == 0x07 {
						return j - i + 1
					}
					if c[j] == 0x1b && j+1 < len(c) && c[j+1] == '\\' {
						return j - i + 2
					}
				}
			}
			return 0
		}
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			chunk := buf[:n]
			if len(pending) > 0 {
				chunk = append(pending, chunk...)
				pending = nil
			}

			flush := 0
			i := 0
			for i < len(chunk) {
				if armed {
					// A kitty-encoded follow key split across reads would
					// otherwise be consumed byte-by-byte; hold the tail and
					// stay armed for the next chunk.
					if incompleteSeqTail(chunk, i) && len(chunk)-i <= maxPendingSeq {
						writePty(chunk[flush:i])
						pending = append([]byte(nil), chunk[i:]...)
						i = len(chunk)
						flush = i
						continue
					}
					armed = false
					b := chunk[i]
					consumed := 1
					// kitty-encoded follow keys (rare, but ghostty may send
					// e.g. \x1b[27;1u for plain ESC) — decode in place so the
					// trailing CSI bytes don't leak to the pty.
					if klen, repl := matchKittyCSIu(chunk, i); klen > 0 && len(repl) == 1 {
						b = repl[0]
						consumed = klen
					}
					// Optimistically arm overlay-owns-input in the pump when
					// the follow-byte is bound to an action that opens an
					// interactive overlay, so subsequent bytes in the same
					// chunk divert to overlayKeyMsg rather than leaking to
					// the active pty before Update has had a chance to
					// process prefixFollowMsg. pushOverlay sets the same
					// flag — idempotent. defaultKeymap is built at init()
					// and never mutated, so this concurrent read is safe.
					if bd := defaultKeymap.LookupPrefix(b); bd != nil && bd.Action.Flags&FlagOwnsInput != 0 {
						overlayOwnsInput.Store(true)
					}
					p.Send(prefixFollowMsg{b: b})
					i += consumed
					flush = i
					continue
				}
				// prefix is suppressed while an interactive overlay owns
				// input — ctrl-a there is just an ordinary byte the
				// overlay can choose to consume or ignore.
				if !overlayOwnsInput.Load() {
					if plen := matchPrefix(chunk, i); plen > 0 {
						writePty(chunk[flush:i])
						armed = true
						p.Send(prefixMsg{})
						i += plen
						flush = i
						continue
					}
				}
				if slen := skipDeviceReport(chunk, i); slen > 0 {
					writePty(chunk[flush:i])
					i += slen
					flush = i
					continue
				}
				if klen, repl := matchKittyCSIu(chunk, i); klen > 0 {
					writePty(chunk[flush:i])
					// Direct binding on the decoded byte takes priority
					// over forwarding — same precedence as the legacy-
					// byte branch below. Only checked for single-byte
					// replacements; multi-byte (Alt-prefixed) replacements
					// aren't bindable in v1 anyway.
					if len(repl) == 1 && tryDirect(repl[0]) {
						i += klen
						flush = i
						continue
					}
					// writePty handles overlay routing; for live pty we want
					// the same path so backspace/enter encoded by kitty land
					// in any active overlay too.
					writePty(repl)
					i += klen
					flush = i
					continue
				}
				if mlen, btn, mx, my, press := matchMouseSGR(chunk, i); mlen > 0 {
					writePty(chunk[flush:i])
					if !overlayOwnsInput.Load() {
						switch {
						case btn == 64:
							p.Send(wheelMsg{up: true})
						case btn == 65:
							p.Send(wheelMsg{up: false})
						case btn == 66, btn == 67:
							// h-scroll — gwam doesn't have horizontal scrollback,
							// and most CLI apps don't read it; forwarding the raw
							// SGR would just echo as text into shells with ECHO on.
						case press:
							// button press: hand off to Update so it can map the
							// click coords against the layout and either switch
							// focus or forward the event to the target pane.
							data := make([]byte, mlen)
							copy(data, chunk[i:i+mlen])
							p.Send(mousePressMsg{data: data, x: mx, y: my})
						default:
							// release (or any non-press button event): Update
							// either drops it (matching a swallowed press) or
							// forwards to the active pane.
							data := make([]byte, mlen)
							copy(data, chunk[i:i+mlen])
							p.Send(mouseReleaseMsg{data: data})
						}
					}
					i += mlen
					flush = i
					continue
				}
				// No matcher consumed this ESC but it starts a CSI/OSC whose
				// terminator hasn't arrived — hold the tail for the next read
				// instead of leaking the fragment to the pty.
				if incompleteSeqTail(chunk, i) && len(chunk)-i <= maxPendingSeq {
					writePty(chunk[flush:i])
					pending = append([]byte(nil), chunk[i:]...)
					i = len(chunk)
					flush = i
					continue
				}
				// Legacy single-byte direct binding: ctrl-t arrives as 0x14,
				// printable ASCII as its literal byte. Flush whatever was
				// accumulated for the pty up to here, dispatch, and advance.
				if tryDirect(chunk[i]) {
					writePty(chunk[flush:i])
					i++
					flush = i
					continue
				}
				i++
			}
			writePty(chunk[flush:])
		}
	}()
}
