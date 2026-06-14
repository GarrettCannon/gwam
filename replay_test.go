package main

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;:]*[a-zA-Z]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// TestReplayLog feeds the captured pty_in byte stream straight into a fresh
// emulator (no freeze/snapshot layer) and prints the resulting grid. If the
// grid reproduces the duplicate/skip corruption, the bug is in vt or in our
// byte handling; if the grid is clean, the bug is in the snapshot/freeze layer.
// Skips unless GWAM_REPLAY=/path/to/log is set.
func TestReplayLog(t *testing.T) {
	path := os.Getenv("GWAM_REPLAY")
	if path == "" {
		t.Skip("set GWAM_REPLAY=/tmp/gwam-debug.log to run")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var ptyIn []byte
	var lastBody string
	w, h := 0, 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "[pty_in] "):
			if b, err := strconv.Unquote(line[len("[pty_in] "):]); err == nil {
				ptyIn = append(ptyIn, b...)
			}
		case strings.HasPrefix(line, "[body] "):
			b, err := strconv.Unquote(line[len("[body] "):])
			if err != nil {
				continue
			}
			rows := strings.Split(strings.TrimRight(b, "\n"), "\n")
			// Size the emulator from the largest frame seen (the full pane);
			// small frames are popups/overlays.
			if len(rows) > h {
				h = len(rows)
				lastBody = b
			}
			for _, r := range rows {
				if n := len([]rune(stripANSI(r))); n > w {
					w = n
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	bodyRows := strings.Split(strings.TrimRight(lastBody, "\n"), "\n")
	t.Logf("replaying %d bytes into %dx%d emulator", len(ptyIn), w, h)

	e := vt.NewSafeEmulator(w, h)
	// Drain emulator-generated query responses, exactly like spawnPane does —
	// otherwise the response pipe fills and Write blocks forever.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := e.Read(buf); err != nil {
				return
			}
		}
	}()
	e.Write(ptyIn)
	grid := e.Render()
	gridRows := strings.Split(strings.TrimRight(grid, "\n"), "\n")

	t.Log("=== replayed emulator grid (line-number gutter) ===")
	for i, r := range gridRows {
		gut := stripANSI(r)
		if len(gut) > 10 {
			gut = gut[:10]
		}
		want := ""
		if i < len(bodyRows) {
			w2 := stripANSI(bodyRows[i])
			if len(w2) > 10 {
				w2 = w2[:10]
			}
			want = w2
		}
		t.Logf("row %2d  replay=%-12q  onscreen=%q", i, gut, want)
	}
}
