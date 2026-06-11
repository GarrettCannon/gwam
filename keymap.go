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

// Keymap is the dispatch table. bindings preserves registration order
// (the overlay walks it); byPrefixByte and byDirectByte are the fast
// indices for single-byte keys; byPrefixSeq and byDirectSeq hold the
// multi-byte escape-sequence encodings (arrows, F-keys, alt-*) keyed by
// the raw byte string. All indices live here so the pump only sees one
// type. One Binding may appear under several seq keys — a key like "up"
// has both CSI and SS3 encodings.
type Keymap struct {
	bindings     []*Binding
	byPrefixByte map[byte]*Binding
	byDirectByte map[byte]*Binding
	byPrefixSeq  map[string]*Binding
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
		byPrefixByte: map[byte]*Binding{},
		byDirectByte: map[byte]*Binding{},
		byPrefixSeq:  map[string]*Binding{},
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
		}
		kind := "prefix"
		byteIdx, seqIdx := k.byPrefixByte, k.byPrefixSeq
		if s.Trigger.Direct {
			kind = "direct"
			byteIdx, seqIdx = k.byDirectByte, k.byDirectSeq
		}
		for _, e := range encs {
			if len(e) == 1 {
				b := e[0]
				if s.Trigger.Direct && b == 0x01 {
					return nil, fmt.Errorf("key %s: conflicts with built-in Ctrl-A prefix", s.Trigger.Key)
				}
				if _, dup := byteIdx[b]; dup {
					return nil, fmt.Errorf("duplicate %s binding for key %s", kind, s.Trigger.Key)
				}
				byteIdx[b] = bd
				continue
			}
			seq := string(e)
			if _, dup := seqIdx[seq]; dup {
				return nil, fmt.Errorf("duplicate %s binding for key %s", kind, s.Trigger.Key)
			}
			seqIdx[seq] = bd
		}
		k.bindings = append(k.bindings, bd)
	}
	return k, nil
}

// LookupPrefix returns the binding registered for a post-prefix byte, or
// nil if none. Read-only and safe to call from the stdin pump goroutine
// because the keymap pointer is set once at startup (defaults +
// optional config overrides) and never mutated after.
func (k *Keymap) LookupPrefix(b byte) *Binding {
	return k.byPrefixByte[b]
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
	return matchSeq(k.byPrefixSeq, c, i)
}

func (k *Keymap) MatchDirectSeq(c []byte, i int) (*Binding, int) {
	return matchSeq(k.byDirectSeq, c, i)
}

// LookupPrefixSeq / LookupDirectSeq re-resolve a matched sequence in
// Update, mirroring LookupPrefix/LookupDirect for the byte paths.
func (k *Keymap) LookupPrefixSeq(seq string) *Binding {
	return k.byPrefixSeq[seq]
}

func (k *Keymap) LookupDirectSeq(seq string) *Binding {
	return k.byDirectSeq[seq]
}

// applyOverrides overlays a user spec slice on top of defaults. For each
// override, if its Trigger matches an entry in defaults, it replaces that
// entry in place; otherwise it appends. Default order is preserved so the
// prefix overlay still reads top-to-bottom in the same shape the user is
// used to, with config-added bindings landing at the end.
func applyOverrides(defaults, overrides []BindingSpec) []BindingSpec {
	out := append([]BindingSpec(nil), defaults...)
	idx := make(map[Trigger]int, len(out))
	for i, s := range out {
		idx[s.Trigger] = i
	}
	for _, s := range overrides {
		if i, ok := idx[s.Trigger]; ok {
			out[i] = s
			continue
		}
		idx[s.Trigger] = len(out)
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
