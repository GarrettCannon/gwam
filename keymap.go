package main

import "fmt"

// Trigger is the input shape that fires a binding. Direct=false routes
// through the prefix (Ctrl-A then Key); Direct=true intercepts Key
// straight from stdin without a prefix, at the cost of permanently
// swallowing that keystroke from every shell inside gwam.
//
// The Key value type carries modifier info (ctrl/alt/shift); buildKeymap
// converts Key → legacy bytes via Key.LegacyBytes for fast pump-side
// dispatch. v1 supports only single-byte encodings; multi-byte triggers
// (arrows, F-keys, alt-*) parse fine but fail keymap-build with a clear
// "not yet encodable" error.
type Trigger struct {
	Key    Key
	Direct bool
}

// BindingSpec is the raw declaration form — what defaultBindings lists and
// what a future config file would deserialize into. The keymap builder
// resolves ActionID against the registry and runs the action's Parse to
// validate Args at load time so bad config fails at startup, not on the
// keystroke that would fire the binding.
type BindingSpec struct {
	Trigger  Trigger
	ActionID string
	Args     map[string]any // nil if the action takes none
	Group    string         // overlay section override; "" = use action's
	Label    string         // overlay label override; "" = use action's
	Menu     string         // which-key submenu this lives in; "" = root
}

// Binding is the resolved form: an action pointer plus already-parsed
// args. The dispatch path reads this directly and never touches the
// registry again.
type Binding struct {
	Trigger Trigger
	Action  *Action
	Args    any
	Group   string
	Label   string
	Menu    string
}

// EffectiveLabel/EffectiveGroup centralize the override fallback chain so
// the overlay and any other reader ask in one place.
func (b *Binding) EffectiveLabel() string {
	if b.Label != "" {
		return b.Label
	}
	return b.Action.Label
}

func (b *Binding) EffectiveGroup() string {
	if b.Group != "" {
		return b.Group
	}
	if b.Action.Group != "" {
		return b.Action.Group
	}
	return "general"
}

// menuLevel is one node of the which-key tree: the set of prefix bindings
// reachable at a given menu. The root level ("") holds the keys that fire
// straight after Ctrl-A — group leaders (menu.open) and any flat actions
// the user kept at root; a submenu like "tabs" holds that group's actions.
// byByte/bySeq are the single-byte and escape-sequence indices (same split
// as the old prefix maps); bindings preserves registration order for the
// panel renderer.
type menuLevel struct {
	name     string
	title    string // "+tabs"; "" for root
	byByte   map[byte]*Binding
	bySeq    map[string]*Binding
	bindings []*Binding
}

func newMenuLevel(name string) *menuLevel {
	return &menuLevel{
		name:   name,
		title:  menuTitle(name),
		byByte: map[byte]*Binding{},
		bySeq:  map[string]*Binding{},
	}
}

// match resolves a raw key batch from the pump against this level: a single
// byte against byByte, anything multi-byte (a legacy arrow/F-key sequence)
// against bySeq. Used by WhichKeyOverlay; the pump's first-follow-key path
// resolves the root level via LookupPrefix/MatchPrefixSeq instead.
func (lvl *menuLevel) match(data []byte) *Binding {
	if len(data) == 1 {
		if bd := lvl.byByte[data[0]]; bd != nil {
			return bd
		}
	}
	return lvl.bySeq[string(data)]
}

// Keymap is the dispatch table. bindings preserves registration order across
// every menu (config check walks it); menus holds one menuLevel per which-key
// node, with menus[""] the root the prefix resolves against; menuOrder is the
// first-seen menu order for deterministic listing. byDirectByte/byDirectSeq
// are the prefix-less direct indices — always root, never nested. One Binding
// may appear under several seq keys (a key like "up" has both CSI and SS3
// encodings).
type Keymap struct {
	bindings     []*Binding
	menus        map[string]*menuLevel
	menuOrder    []string
	byDirectByte map[byte]*Binding
	byDirectSeq  map[string]*Binding
}

// buildKeymap resolves each spec against the action registry, runs the
// action's Parse on its raw args, encodes the trigger Key to its legacy
// byte encodings, and indexes each into the prefix or direct table per
// Trigger.Direct — single-byte encodings into the byte index, escape
// sequences into the seq index. Errors out on unknown action ID, parse
// failure, args supplied to a no-arg action, unencodable trigger key,
// duplicate trigger, or direct binding that conflicts with the built-in
// Ctrl-A prefix.
func buildKeymap(specs []BindingSpec) (*Keymap, error) {
	k := &Keymap{
		menus:        map[string]*menuLevel{"": newMenuLevel("")},
		menuOrder:    []string{""},
		byDirectByte: map[byte]*Binding{},
		byDirectSeq:  map[string]*Binding{},
	}
	for _, s := range specs {
		a, ok := actions[s.ActionID]
		if !ok {
			return nil, fmt.Errorf("key %s: unknown action %q", s.Trigger.Key, s.ActionID)
		}
		var args any
		if a.Parse != nil {
			v, err := a.Parse(s.Args)
			if err != nil {
				return nil, fmt.Errorf("key %s: %w", s.Trigger.Key, err)
			}
			args = v
		} else if len(s.Args) > 0 {
			return nil, fmt.Errorf("key %s: action %q takes no args", s.Trigger.Key, s.ActionID)
		}
		encs := s.Trigger.Key.legacyEncodings()
		if len(encs) == 0 {
			return nil, fmt.Errorf("key %s: no known terminal encoding", s.Trigger.Key)
		}
		bd := &Binding{
			Trigger: s.Trigger,
			Action:  a,
			Args:    args,
			Group:   s.Group,
			Label:   s.Label,
			Menu:    s.Menu,
		}
		// Direct bindings are always root and prefix-less — they never live
		// in a menu. Prefix bindings land in their declared menu's level, so
		// the same key (e.g. "c") can mean different things in +tabs vs
		// +sessions without colliding.
		if s.Trigger.Direct {
			if err := indexEncodings(k.byDirectByte, k.byDirectSeq, bd, encs, "direct", true); err != nil {
				return nil, err
			}
			k.bindings = append(k.bindings, bd)
			continue
		}
		lvl := k.menus[s.Menu]
		if lvl == nil {
			lvl = newMenuLevel(s.Menu)
			k.menus[s.Menu] = lvl
			k.menuOrder = append(k.menuOrder, s.Menu)
		}
		kind := "prefix"
		if s.Menu != "" {
			kind = "prefix(" + s.Menu + ")"
		}
		if err := indexEncodings(lvl.byByte, lvl.bySeq, bd, encs, kind, false); err != nil {
			return nil, err
		}
		lvl.bindings = append(lvl.bindings, bd)
		k.bindings = append(k.bindings, bd)
	}
	// Every group leader must point at a menu that actually has entries;
	// a leader to an empty/undefined submenu is a dead key, so fail loudly
	// at build time rather than silently no-op on the keystroke.
	for _, bd := range k.bindings {
		if bd.Action.ID != "menu.open" {
			continue
		}
		g := bd.Args.(*menuOpenArgs).group
		if lvl, ok := k.menus[g]; !ok || len(lvl.bindings) == 0 {
			return nil, fmt.Errorf("key %s: menu.open targets group %q which has no bindings", bd.Trigger.Key, g)
		}
	}
	return k, nil
}

// indexEncodings inserts bd under each of its legacy encodings into the given
// byte/seq index pair, rejecting duplicates within that scope and (for direct
// bindings) any clash with the built-in Ctrl-A prefix. kind names the scope
// for error messages ("direct", "prefix", "prefix(tabs)").
func indexEncodings(byteIdx map[byte]*Binding, seqIdx map[string]*Binding, bd *Binding, encs [][]byte, kind string, direct bool) error {
	for _, e := range encs {
		if len(e) == 1 {
			b := e[0]
			if direct && b == 0x01 {
				return fmt.Errorf("key %s: conflicts with built-in Ctrl-A prefix", bd.Trigger.Key)
			}
			if _, dup := byteIdx[b]; dup {
				return fmt.Errorf("duplicate %s binding for key %s", kind, bd.Trigger.Key)
			}
			byteIdx[b] = bd
			continue
		}
		seq := string(e)
		if _, dup := seqIdx[seq]; dup {
			return fmt.Errorf("duplicate %s binding for key %s", kind, bd.Trigger.Key)
		}
		seqIdx[seq] = bd
	}
	return nil
}

// LookupPrefix returns the binding registered for a post-prefix byte, or
// nil if none. Read-only and safe to call from the stdin pump goroutine
// because the keymap pointer is set once at startup (defaults +
// optional config overrides) and never mutated after.
func (k *Keymap) LookupPrefix(b byte) *Binding {
	return k.menus[""].byByte[b]
}

// LookupDirect returns the binding registered for a byte that should fire
// without the prefix, or nil. Same concurrency guarantees as LookupPrefix.
func (k *Keymap) LookupDirect(b byte) *Binding {
	return k.byDirectByte[b]
}

// matchSeq returns the longest registered escape sequence that prefixes
// c[i:], with its length, or (nil, 0). The seq maps hold a handful of
// entries at most, so a linear scan beats building a trie.
func matchSeq(idx map[string]*Binding, c []byte, i int) (*Binding, int) {
	var best *Binding
	bestLen := 0
	for seq, bd := range idx {
		if len(seq) > bestLen && i+len(seq) <= len(c) && string(c[i:i+len(seq)]) == seq {
			best, bestLen = bd, len(seq)
		}
	}
	return best, bestLen
}

// MatchPrefixSeq / MatchDirectSeq are the pump-side sequence matchers —
// same concurrency guarantees as LookupPrefix.
func (k *Keymap) MatchPrefixSeq(c []byte, i int) (*Binding, int) {
	return matchSeq(k.menus[""].bySeq, c, i)
}

func (k *Keymap) MatchDirectSeq(c []byte, i int) (*Binding, int) {
	return matchSeq(k.byDirectSeq, c, i)
}

// LookupPrefixSeq / LookupDirectSeq re-resolve a matched sequence in
// Update, mirroring LookupPrefix/LookupDirect for the byte paths.
func (k *Keymap) LookupPrefixSeq(seq string) *Binding {
	return k.menus[""].bySeq[seq]
}

func (k *Keymap) LookupDirectSeq(seq string) *Binding {
	return k.byDirectSeq[seq]
}

// applyOverrides overlays a user spec slice on top of defaults. For each
// override, if its (Trigger, Menu) matches an entry in defaults, it replaces
// that entry in place; otherwise it appends. Default order is preserved so
// the menu panels still read top-to-bottom in the same shape the user is
// used to, with config-added bindings landing at the end.
//
// Menu is part of the identity: the same key in two different menus (e.g. "c"
// in +tabs and +sessions) is two distinct bindings, so adding a key to one
// submenu must not clobber the same key in another menu or at root.
func applyOverrides(defaults, overrides []BindingSpec) []BindingSpec {
	type slot struct {
		Trigger Trigger
		Menu    string
	}
	out := append([]BindingSpec(nil), defaults...)
	idx := make(map[slot]int, len(out))
	for i, s := range out {
		idx[slot{s.Trigger, s.Menu}] = i
	}
	for _, s := range overrides {
		key := slot{s.Trigger, s.Menu}
		if i, ok := idx[key]; ok {
			out[i] = s
			continue
		}
		idx[key] = len(out)
		out = append(out, s)
	}
	return out
}

// defaultKeymap is the in-process keymap built at startup from
// defaultBindings (defined in bindings.go). Lives in init() rather than
// as a `var = buildKeymap(...)` so the action registry — populated by
// actions_builtin.go's init() — is in place before we resolve specs.
// Go runs init() funcs in source-file lexical order, and actions_builtin
// sorts before keymap.
var defaultKeymap *Keymap

func init() {
	k, err := buildKeymap(defaultBindings)
	if err != nil {
		panic(fmt.Sprintf("default keymap: %v", err))
	}
	defaultKeymap = k
}
