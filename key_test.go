package main

import "testing"

func encStrings(t *testing.T, spec string) []string {
	t.Helper()
	k, err := ParseKey(spec)
	if err != nil {
		t.Fatalf("ParseKey(%q): %v", spec, err)
	}
	encs := k.legacyEncodings()
	out := make([]string, len(encs))
	for i, e := range encs {
		out[i] = string(e)
	}
	return out
}

func TestLegacyEncodings(t *testing.T) {
	cases := []struct {
		spec string
		want []string // every entry must be present (order-insensitive)
	}{
		{"a", []string{"a"}},
		{"ctrl-t", []string{"\x14"}},
		{"enter", []string{"\x0d"}},
		{"alt-x", []string{"\x1bx"}},
		{"alt-ctrl-x", []string{"\x1b\x18"}},
		{"up", []string{"\x1b[A", "\x1bOA"}},
		{"ctrl-up", []string{"\x1b[1;5A"}},
		{"shift-alt-down", []string{"\x1b[1;4B"}},
		{"home", []string{"\x1b[H", "\x1bOH", "\x1b[1~"}},
		{"F1", []string{"\x1bOP", "\x1b[11~"}},
		{"ctrl-F1", []string{"\x1b[1;5P", "\x1b[11;5~"}},
		{"F5", []string{"\x1b[15~"}},
		{"F12", []string{"\x1b[24~"}},
		{"ctrl-pgup", []string{"\x1b[5;5~"}},
		{"delete", []string{"\x1b[3~"}},
		{"shift-tab", []string{"\x1b[Z"}},
	}
	for _, c := range cases {
		got := encStrings(t, c.spec)
		for _, want := range c.want {
			found := false
			for _, g := range got {
				if g == want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: missing encoding %q (got %q)", c.spec, want, got)
			}
		}
	}

	// Sequence-introducer bases must not be alt-bindable.
	for _, spec := range []string{"alt-[", "alt-O"} {
		k, err := ParseKey(spec)
		if err != nil {
			t.Fatalf("ParseKey(%q): %v", spec, err)
		}
		if encs := k.legacyEncodings(); encs != nil {
			t.Errorf("%s: want nil encodings (CSI/SS3 introducer), got %q", spec, encs)
		}
	}
}

func TestBuildKeymapMultiByte(t *testing.T) {
	specs := []BindingSpec{
		{Trigger: Trigger{Key: mustKey("F1"), Direct: true}, ActionID: "tab.next"},
		{Trigger: Trigger{Key: mustKey("alt-t")}, ActionID: "tab.prev"},
	}
	km, err := buildKeymap(specs)
	if err != nil {
		t.Fatalf("buildKeymap: %v", err)
	}

	// Direct F1 matches both of its registered encodings mid-stream.
	for _, seq := range []string{"\x1bOP", "\x1b[11~"} {
		buf := []byte("xx" + seq + "yy")
		bd, n := km.MatchDirectSeq(buf, 2)
		if bd == nil || n != len(seq) {
			t.Errorf("MatchDirectSeq(%q): got (%v, %d), want match of len %d", seq, bd, n, len(seq))
		} else if bd.Action.ID != "tab.next" {
			t.Errorf("MatchDirectSeq(%q): wrong action %s", seq, bd.Action.ID)
		}
	}

	// Prefix alt-t matches via the prefix seq index, not the direct one.
	if bd, _ := km.MatchPrefixSeq([]byte("\x1bt"), 0); bd == nil || bd.Action.ID != "tab.prev" {
		t.Errorf("MatchPrefixSeq(alt-t): got %v", bd)
	}
	if bd, _ := km.MatchDirectSeq([]byte("\x1bt"), 0); bd != nil {
		t.Errorf("MatchDirectSeq(alt-t): want nil, got %s", bd.Action.ID)
	}

	// Unmatched sequences don't fire.
	if bd, _ := km.MatchDirectSeq([]byte("\x1b[15~"), 0); bd != nil {
		t.Errorf("MatchDirectSeq(F5): want nil, got %s", bd.Action.ID)
	}
}
