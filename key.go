package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Key is the canonical, terminal-independent representation of one
// keystroke a user can bind to. Code names the base key (a printable
// rune codepoint or a named-key constant like KeyEnter); Mods is a
// bitfield of Ctrl/Alt/Shift applied on top.
//
// The byte sequence a terminal actually emits for a Key depends on its
// mode (legacy vs kitty progressive enhancement) and the platform, so
// encoding lives outside this type — a follow-up Key.LegacyBytes() or a
// pump-side match function consults terminal state. The parser, the
// canonical String form, and equality are pure.
type Key struct {
	Code KeyCode
	Mods Mods
}

// KeyCode is either a printable ASCII codepoint (0x20..0x7e) or a named-
// key constant above keyCodeNamedBase. Anchoring named keys at 0xE000
// (Unicode Private Use Area) leaves the entire ASCII range free for
// printable bases, and any future rune-codepoint source can't collide.
type KeyCode int

const keyCodeNamedBase KeyCode = 0xE000

const (
	KeyEnter KeyCode = keyCodeNamedBase + iota
	KeyTab
	KeyEsc
	KeyBackspace
	KeySpace
	KeyDelete
	KeyInsert
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
)

// Mods is a bitfield of modifier keys. Any subset may be combined; the
// canonical iteration order for printing is ctrl, alt, shift.
type Mods uint8

const (
	ModCtrl Mods = 1 << iota
	ModAlt
	ModShift
)

// modAliases maps every accepted modifier spelling to its bit. Aliases
// follow conventions users carry over from Emacs / vim / tmux / readline:
// "C-" / "ctrl-" / "control-", "M-" / "alt-" / "meta-", "S-" / "shift-".
// Lookup is case-insensitive (parser lowercases the head).
var modAliases = map[string]Mods{
	"c":       ModCtrl,
	"ctrl":    ModCtrl,
	"control": ModCtrl,
	"m":       ModAlt,
	"alt":     ModAlt,
	"meta":    ModAlt,
	"s":       ModShift,
	"shift":   ModShift,
}

// namedKeys maps lowercase name → KeyCode for the parser. Multiple
// synonyms can resolve to the same code (e.g. "enter" and "return");
// canonicalNamedKey holds the preferred form for round-tripping.
var namedKeys = map[string]KeyCode{
	"enter":     KeyEnter,
	"return":    KeyEnter,
	"tab":       KeyTab,
	"esc":       KeyEsc,
	"escape":    KeyEsc,
	"space":     KeySpace,
	"backspace": KeyBackspace,
	"bs":        KeyBackspace,
	"delete":    KeyDelete,
	"del":       KeyDelete,
	"insert":    KeyInsert,
	"ins":       KeyInsert,
	"home":      KeyHome,
	"end":       KeyEnd,
	"pgup":      KeyPageUp,
	"pageup":    KeyPageUp,
	"pgdn":      KeyPageDown,
	"pagedown":  KeyPageDown,
	"up":        KeyUp,
	"down":      KeyDown,
	"left":      KeyLeft,
	"right":     KeyRight,
}

// canonicalNamedKey is the preferred display form for each named-key
// code. Key.String() reads from this so a parsed-then-printed Key always
// comes out in one shape regardless of which synonym the input used.
var canonicalNamedKey = map[KeyCode]string{
	KeyEnter:     "enter",
	KeyTab:       "tab",
	KeyEsc:       "esc",
	KeySpace:     "space",
	KeyBackspace: "backspace",
	KeyDelete:    "delete",
	KeyInsert:    "insert",
	KeyHome:      "home",
	KeyEnd:       "end",
	KeyPageUp:    "pgup",
	KeyPageDown:  "pgdn",
	KeyUp:        "up",
	KeyDown:      "down",
	KeyLeft:      "left",
	KeyRight:     "right",
}

// ParseKey parses a textual key spec into a Key. Accepted forms:
//
//	"a"           printable ASCII (single rune; printable means 0x20..0x7e)
//	"?"           ditto for punctuation, including the "-" character itself
//	"enter"       named key (case-insensitive; see namedKeys for the list)
//	"F1".."F12"   function keys (case-insensitive on the F)
//	"ctrl-a"      modifier + base. Modifier aliases: ctrl/c/control,
//	"C-a"         alt/m/meta, shift/s. Case-insensitive. Any order:
//	"alt-ctrl-x"  ctrl-alt-x and alt-ctrl-x parse to the same Key.
//	"shift-a"     Shift + a printable letter is normalized to uppercase
//	              with the Shift bit cleared, since 'A' alone is already
//	              unambiguous. Shift stays set on non-letter bases (shift-up,
//	              shift-F1) where the modifier carries real information.
//
// Modifier prefixes are consumed greedily left-to-right; the first token
// that doesn't look like "<modname>-" starts the base key. That lets the
// base contain its own dashes ("ctrl--" parses as Ctrl + '-') without a
// separate escape syntax.
//
// Errors include the original input string so a `gwam config check`
// listing points the user straight at the offending [[binding]].
func ParseKey(s string) (Key, error) {
	if s == "" {
		return Key{}, fmt.Errorf("empty key spec")
	}
	var mods Mods
	rest := s
	for {
		dash := strings.IndexByte(rest, '-')
		if dash <= 0 {
			break
		}
		head := strings.ToLower(rest[:dash])
		m, ok := modAliases[head]
		if !ok {
			break
		}
		if mods&m != 0 {
			return Key{}, fmt.Errorf("duplicate modifier %q in %q", head, s)
		}
		mods |= m
		rest = rest[dash+1:]
	}
	if rest == "" {
		return Key{}, fmt.Errorf("missing base key after modifier(s) in %q", s)
	}
	code, err := parseBase(rest)
	if err != nil {
		return Key{}, fmt.Errorf("%w in %q", err, s)
	}
	if mods&ModShift != 0 && code >= 'a' && code <= 'z' {
		code -= 32
		mods &^= ModShift
	}
	return Key{Code: code, Mods: mods}, nil
}

// parseBase resolves the post-modifier portion of a key spec to a
// KeyCode. Order of attempts: function-key pattern, named-key map,
// single printable ASCII rune.
func parseBase(s string) (KeyCode, error) {
	if (s[0] == 'F' || s[0] == 'f') && len(s) > 1 && len(s) <= 3 {
		if n, err := strconv.Atoi(s[1:]); err == nil && n >= 1 && n <= 12 {
			return KeyF1 + KeyCode(n-1), nil
		}
	}
	if code, ok := namedKeys[strings.ToLower(s)]; ok {
		return code, nil
	}
	if len(s) == 1 && s[0] >= 0x20 && s[0] < 0x7f {
		return KeyCode(s[0]), nil
	}
	return 0, fmt.Errorf("unknown key %q", s)
}

// LegacyBytes returns the byte sequence a legacy-mode terminal emits for k,
// and ok=true if k is encodable in v1. Returns (nil, false) for inputs we
// don't yet support so callers can reject early with a clear error.
//
// v1 scope — single-byte representations only:
//   - printable ASCII (0x20..0x7e) with no modifiers
//   - ctrl-<letter> (a..z, A..Z) → 0x01..0x1a
//   - ctrl-space → 0x00 (the NUL byte; rarely useful but unambiguous)
//   - named single-byte keys: enter (0x0d), tab (0x09), esc (0x1b),
//     space (0x20), backspace (0x7f)
//
// Deliberately excluded from v1:
//   - alt-* — would emit ESC<byte>, a two-byte sequence that races with
//     the shell's own ESC handling (vim, readline). Needs a "lone ESC vs
//     alt prefix" timeout story that's better designed when it's actually
//     needed.
//   - multi-byte named keys (arrows, F-keys, home/end/...) — fine to
//     bind via prefix-mode bytes, but no legacy single-byte encoding.
//   - shift on non-letter bases — no portable terminal encoding.
//   - ctrl with non-letter bases (ctrl-[, ctrl-\, ctrl-], ctrl-^,
//     ctrl-_, ctrl-@) — encodable in principle, but ctrl-[ == 0x1b ==
//     Esc, so allowing it lets the user accidentally rebind Escape;
//     defer until there's a real ask.
func (k Key) LegacyBytes() ([]byte, bool) {
	if k.Mods&ModAlt != 0 {
		return nil, false
	}
	if k.Mods&ModShift != 0 {
		// Shift on a letter base would already have been normalized away
		// by ParseKey. Anything still carrying Shift here is shift on a
		// non-letter (shift-up, shift-F1) — no portable single-byte form.
		return nil, false
	}

	switch k.Code {
	case KeyEnter:
		if k.Mods != 0 {
			return nil, false
		}
		return []byte{0x0d}, true
	case KeyTab:
		if k.Mods != 0 {
			return nil, false
		}
		return []byte{0x09}, true
	case KeyEsc:
		if k.Mods != 0 {
			return nil, false
		}
		return []byte{0x1b}, true
	case KeySpace:
		if k.Mods&ModCtrl != 0 {
			return []byte{0x00}, true
		}
		return []byte{0x20}, true
	case KeyBackspace:
		if k.Mods != 0 {
			return nil, false
		}
		return []byte{0x7f}, true
	}

	// Multi-byte named keys (arrows, F-keys, ...) — no v1 encoding.
	if k.Code >= keyCodeNamedBase {
		return nil, false
	}

	// Printable ASCII base.
	if k.Code < 0x20 || k.Code >= 0x7f {
		return nil, false
	}
	b := byte(k.Code)
	if k.Mods&ModCtrl != 0 {
		switch {
		case b >= 'a' && b <= 'z':
			return []byte{b - 'a' + 1}, true
		case b >= 'A' && b <= 'Z':
			return []byte{b - 'A' + 1}, true
		}
		return nil, false
	}
	return []byte{b}, true
}

// csiLetterFinal is the CSI final byte for keys encoded as \x1b[<fin>
// (plain) / \x1bO<fin> (application cursor mode) / \x1b[1;<mods><fin>
// (modified): arrows, home, end.
var csiLetterFinal = map[KeyCode]byte{
	KeyUp:    'A',
	KeyDown:  'B',
	KeyRight: 'C',
	KeyLeft:  'D',
	KeyHome:  'H',
	KeyEnd:   'F',
}

// tildeNum is the parameter for keys encoded as \x1b[<n>~ (plain) /
// \x1b[<n>;<mods>~ (modified): insert/delete/page keys and F5..F12.
var tildeNum = map[KeyCode]int{
	KeyInsert:   2,
	KeyDelete:   3,
	KeyPageUp:   5,
	KeyPageDown: 6,
	KeyF5:       15,
	KeyF6:       17,
	KeyF7:       18,
	KeyF8:       19,
	KeyF9:       20,
	KeyF10:      21,
	KeyF11:      23,
	KeyF12:      24,
}

// xtermMods is the modifier parameter in modified CSI sequences:
// 1 + (shift=1 | alt=2 | ctrl=4).
func (k Key) xtermMods() int {
	m := 1
	if k.Mods&ModShift != 0 {
		m += 1
	}
	if k.Mods&ModAlt != 0 {
		m += 2
	}
	if k.Mods&ModCtrl != 0 {
		m += 4
	}
	return m
}

// legacyEncodings returns every byte sequence a terminal may emit for k —
// the dispatch table buildKeymap indexes by. Single-byte keys return their
// LegacyBytes form; multi-byte keys return each known legacy variant:
//
//   - arrows / home / end — CSI letter (\x1b[A), SS3 letter (\x1bOA,
//     application cursor mode), and the modified form (\x1b[1;<m>A)
//   - home / end also have tilde variants (\x1b[1~ / \x1b[4~)
//   - F1..F4 — SS3 P..S, the legacy tilde forms (\x1b[11~..\x1b[14~), and
//     modified \x1b[1;<m>P..S
//   - insert / delete / pgup / pgdn / F5..F12 — \x1b[<n>~ and \x1b[<n>;<m>~
//   - shift-tab — \x1b[Z
//   - alt + any single-byte base — ESC-prefixed (\x1b<byte>), except bases
//     that collide with sequence introducers ('[' = CSI, 'O' = SS3, Esc)
//
// Returns nil for keys with no known encoding; buildKeymap surfaces that as
// a config error. Kitty-encoded keystrokes don't appear here — the input
// pump decodes them back to these legacy forms before matching.
func (k Key) legacyEncodings() [][]byte {
	if bs, ok := k.LegacyBytes(); ok {
		return [][]byte{bs}
	}

	mods := k.xtermMods()

	if fin, ok := csiLetterFinal[k.Code]; ok {
		if mods == 1 {
			out := [][]byte{
				{0x1b, '[', fin},
				{0x1b, 'O', fin},
			}
			switch k.Code {
			case KeyHome:
				out = append(out, []byte("\x1b[1~"))
			case KeyEnd:
				out = append(out, []byte("\x1b[4~"))
			}
			return out
		}
		return [][]byte{[]byte(fmt.Sprintf("\x1b[1;%d%c", mods, fin))}
	}

	if k.Code >= KeyF1 && k.Code <= KeyF4 {
		fin := byte('P' + k.Code - KeyF1)
		tilde := 11 + int(k.Code-KeyF1)
		if mods == 1 {
			return [][]byte{
				{0x1b, 'O', fin},
				[]byte(fmt.Sprintf("\x1b[%d~", tilde)),
			}
		}
		return [][]byte{
			[]byte(fmt.Sprintf("\x1b[1;%d%c", mods, fin)),
			[]byte(fmt.Sprintf("\x1b[%d;%d~", tilde, mods)),
		}
	}

	if n, ok := tildeNum[k.Code]; ok {
		if mods == 1 {
			return [][]byte{[]byte(fmt.Sprintf("\x1b[%d~", n))}
		}
		return [][]byte{[]byte(fmt.Sprintf("\x1b[%d;%d~", n, mods))}
	}

	if k.Code == KeyTab && k.Mods == ModShift {
		return [][]byte{[]byte("\x1b[Z")}
	}

	// alt + a key with a single-byte form: ESC prefix. The base must not be
	// a sequence introducer — \x1b[ is CSI, \x1bO is SS3, \x1b\x1b is hostile
	// to every downstream parser.
	if k.Mods&ModAlt != 0 {
		base := k
		base.Mods &^= ModAlt
		if bs, ok := base.LegacyBytes(); ok && len(bs) == 1 {
			b := bs[0]
			if b == '[' || b == 'O' || b == 0x1b {
				return nil
			}
			return [][]byte{{0x1b, b}}
		}
	}

	return nil
}

// String returns the canonical text form of k. Modifier order is fixed
// (ctrl-, alt-, shift-) so Key values that compare equal also stringify
// equal — useful for config-check output and round-trip testing.
//
// Printable bases keep their case; named bases use their canonical
// synonym (e.g. "enter" not "return"); function keys come out as F1..F12.
func (k Key) String() string {
	var b strings.Builder
	if k.Mods&ModCtrl != 0 {
		b.WriteString("ctrl-")
	}
	if k.Mods&ModAlt != 0 {
		b.WriteString("alt-")
	}
	if k.Mods&ModShift != 0 {
		b.WriteString("shift-")
	}
	switch {
	case k.Code >= KeyF1 && k.Code <= KeyF12:
		fmt.Fprintf(&b, "F%d", int(k.Code-KeyF1+1))
	case k.Code >= keyCodeNamedBase:
		if n, ok := canonicalNamedKey[k.Code]; ok {
			b.WriteString(n)
		} else {
			fmt.Fprintf(&b, "<keycode:%d>", int(k.Code))
		}
	default:
		b.WriteByte(byte(k.Code))
	}
	return b.String()
}
