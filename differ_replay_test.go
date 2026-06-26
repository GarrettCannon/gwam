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

// TestDifferReplay replays a captured pty_in stream through gwam's FULL render
// path — one render per [pty_in] line, exactly as the live ptyReadMsg handler
// does — and pushes each composed frame through bubbletea's TerminalRenderer
// (the curses differ, with its default hardscroll optimization) into a running
// "physical" emulator. The differ is supposed to be lossless, so after every
// frame the physical screen must equal the frame gwam intended. The FIRST
// divergence is the reproduced corruption, with the offending frame dumped.
//
// Capture a session that exhibits the bug with:
//
//	GWAM_DEBUG=1 gwam        # writes /tmp/gwam-debug.log (pty_in only — small)
//
// then run:
//
//	GWAM_REPLAY=/tmp/gwam-debug.log go test -run TestDifferReplay -v
//
// Unlike TestReplayLog (which only checks whether vt itself reproduces the
// corruption), this exercises the differ — the prime suspect for the duplicate /
// out-of-order line rendering.
func TestDifferReplay(t *testing.T) {
	path := os.Getenv("GWAM_REPLAY")
	if path == "" {
		t.Skip("set GWAM_REPLAY=/tmp/gwam-debug.log to run")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Pass 1: collect pty_in chunks (render boundaries) and infer geometry from
	// the largest body frame, mirroring TestReplayLog.
	var chunks [][]byte
	w, paneH := 0, 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "[pty_in] "):
			if b, err := strconv.Unquote(line[len("[pty_in] "):]); err == nil {
				chunks = append(chunks, []byte(b))
			}
		case strings.HasPrefix(line, "[body] "):
			if b, err := strconv.Unquote(line[len("[body] "):]); err == nil {
				rs := strings.Split(strings.TrimRight(b, "\n"), "\n")
				if len(rs) > paneH {
					paneH = len(rs)
				}
				for _, r := range rs {
					if n := len([]rune(stripANSI(r))); n > w {
						w = n
					}
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	// Geometry: inferred from [body] frames if present, else from GWAM_W/GWAM_H
	// (the full window size) so a lightweight pty_in-only capture works too.
	if ew := os.Getenv("GWAM_W"); ew != "" {
		w, _ = strconv.Atoi(ew)
	}
	if eh := os.Getenv("GWAM_H"); eh != "" {
		if hh, _ := strconv.Atoi(eh); hh > 0 {
			paneH = hh - tabBarH
		}
	}
	if w == 0 || paneH == 0 {
		t.Skip("no geometry: capture with GWAM_DEBUG_BODY=1, or pass GWAM_W=<cols> GWAM_H=<rows>")
	}
	rows := paneH + tabBarH
	t.Logf("replaying %d pty_in chunks at %dx%d (pane %dx%d)", len(chunks), w, rows, w, paneH)

	_ = rows
	// gwam ingest + render state.
	pe := vt.NewSafeEmulator(w, paneH)
	drainSafe(pe)
	p := &Pane{vt: pe}

	// Ground-truth emulator: the SAME pty bytes with NO freeze/snapshot layer.
	truth := vt.NewSafeEmulator(w, paneH)
	drainSafe(truth)

	dupFrame := -1
	var dupRows, dupTruth []string
	var dupMsg string
	dupFrozen := false
	mismatchFrame := -1
	var mmGwam, mmTruth []string
	for i, ch := range chunks {
		data := takeSyncCarry(p, ch)
		if len(data) > 0 {
			writeWithSync(p, sanitizeOscC1(p, data))
		}
		// Feed ground truth the raw bytes (strip the 2026 markers so it's not
		// confused; it ignores them anyway) — no freeze.
		truth.Write(ch)

		gwamBody := splitRows(renderPaneBody(p, w, paneH), paneH)

		// (1) Internal corruption in gwam's OWN frame: a duplicated or backwards
		// line number — exactly image #1 — regardless of timing or the differ.
		if dupFrame < 0 {
			if msg, bad := lineNumCorrupt(gwamBody); bad {
				dupFrame, dupRows, dupMsg = i, gwamBody, msg
				dupTruth = splitRows(truth.Render(), paneH)
				dupFrozen = p.syncFrozen
			}
		}

		// (2) Settled divergence: when NOT inside a sync freeze, gwam's frame must
		// equal ground truth. A persistent mismatch while live = the snapshot layer
		// surfacing a stale/torn frame.
		if !p.syncFrozen && mismatchFrame < 0 {
			tr := splitRows(truth.Render(), paneH)
			if !rowsMatch(gwamBody, tr) {
				mismatchFrame, mmGwam, mmTruth = i, gwamBody, tr
			}
		}
	}

	switch {
	case dupFrame >= 0:
		t.Errorf("gwam produced an internally corrupt frame at %d: %s (syncFrozen=%v)", dupFrame, dupMsg, dupFrozen)
		_, truthBad := lineNumCorrupt(dupTruth)
		t.Logf("ground-truth emulator (raw bytes, NO sync/carry layer) corrupt=%v at same frame", truthBad)
		t.Logf("--- gwam frame (left) vs ground truth (right), first 8 rows ---")
		for i := 0; i < 8 && i < len(dupRows); i++ {
			g := dupRows[i]
			if len(g) > 60 {
				g = g[:60]
			}
			tr := ""
			if i < len(dupTruth) {
				tr = dupTruth[i]
				if len(tr) > 60 {
					tr = tr[:60]
				}
			}
			t.Logf("row %2d  gwam=%-62q truth=%q", i, g, tr)
		}
	case mismatchFrame >= 0:
		t.Errorf("gwam's live frame %d diverges from ground truth (snapshot layer stale/torn)", mismatchFrame)
		for i := 0; i < paneH; i++ {
			mark := "  "
			if mmGwam[i] != mmTruth[i] {
				mark = ">>"
			}
			t.Logf("%s row %2d  gwam=%-50q truth=%q", mark, i, mmGwam[i], mmTruth[i])
		}
	default:
		t.Logf("clean: no internal corruption and gwam matched ground truth on every live frame (%d frames)", len(chunks))
	}
}

// lineNumCorrupt scans rendered body rows for a leading line number and reports
// the first duplicated or backwards step — the signature of image #1.
func lineNumCorrupt(rows []string) (string, bool) {
	lead := regexp.MustCompile(`^\s*(\d+)\b`)
	prev, prevRow := -1, -1
	for i, r := range rows {
		m := lead.FindStringSubmatch(r)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		if prev >= 0 {
			if n == prev {
				return "duplicate line number " + m[1] + " at rows " + strconv.Itoa(prevRow) + "&" + strconv.Itoa(i), true
			}
			if n < prev {
				return "line number went backwards at row " + strconv.Itoa(i), true
			}
		}
		prev, prevRow = n, i
	}
	return "", false
}

func drainSafe(e *vt.SafeEmulator) {
	go func() {
		b := make([]byte, 4096)
		for {
			if _, err := e.Read(b); err != nil {
				return
			}
		}
	}()
}

func splitRows(s string, h int) []string {
	rs := strings.Split(strings.TrimRight(s, "\n"), "\n")
	out := make([]string, h)
	for i := 0; i < h; i++ {
		if i < len(rs) {
			out[i] = strings.TrimRight(stripANSI(rs[i]), " ")
		}
	}
	return out
}

func rowsMatch(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
