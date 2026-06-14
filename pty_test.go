package main

import (
	"bytes"
	"testing"
)

// TestTakeSyncCarry covers the cross-read reassembly of 2026 markers: a partial
// trailing marker is held and stitched onto the next read so writeWithSync sees
// it whole.
func TestTakeSyncCarry(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantOut  string // bytes returned for processing this read
		wantHeld string // bytes parked in syncCarry for the next read
	}{
		{"no marker", "hello world", "hello world", ""},
		{"complete start not held", "abc\x1b[?2026h", "abc\x1b[?2026h", ""},
		{"complete end not held", "abc\x1b[?2026l", "abc\x1b[?2026l", ""},
		{"partial shared prefix held", "abc\x1b[?2026", "abc", "\x1b[?2026"},
		{"lone esc held", "abc\x1b", "abc", "\x1b"},
		{"csi open held", "abc\x1b[", "abc", "\x1b["},
		{"query partial held", "abc\x1b[?2026$", "abc", "\x1b[?2026$"},
		{"non-marker esc seq tail not over-held", "abc\x1b[0m", "abc\x1b[0m", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Pane{}
			out := takeSyncCarry(p, []byte(tt.in))
			if string(out) != tt.wantOut {
				t.Errorf("out = %q, want %q", out, tt.wantOut)
			}
			if string(p.syncCarry) != tt.wantHeld {
				t.Errorf("held = %q, want %q", p.syncCarry, tt.wantHeld)
			}
		})
	}
}

// TestTakeSyncCarry_Reassembly feeds a syncEnd marker split across two reads and
// asserts the second call yields the whole marker, so bytes.Index would find it.
func TestTakeSyncCarry_Reassembly(t *testing.T) {
	p := &Pane{}

	out1 := takeSyncCarry(p, []byte("content\x1b[?2026"))
	if string(out1) != "content" {
		t.Fatalf("first read out = %q, want %q", out1, "content")
	}

	out2 := takeSyncCarry(p, []byte("l more"))
	if !bytes.Contains(out2, syncEnd) {
		t.Fatalf("reassembled read %q does not contain syncEnd %q", out2, syncEnd)
	}
	if len(p.syncCarry) != 0 {
		t.Fatalf("carry should be drained, got %q", p.syncCarry)
	}
}
