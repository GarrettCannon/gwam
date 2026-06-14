package main

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/vt"
)

// fillNvimLike writes a screenful of varied, lightly-styled text to emulate an
// nvim buffer (line numbers + syntax-ish colors) so Render() costs are realistic.
func fillNvimLike(e *vt.SafeEmulator, w, h int) {
	var b strings.Builder
	b.WriteString("\x1b[2J\x1b[H")
	for y := 1; y <= h; y++ {
		fmt.Fprintf(&b, "\x1b[%d;1H", y)
		fmt.Fprintf(&b, "\x1b[38;5;240m%4d \x1b[0m", y) // line number
		fmt.Fprintf(&b, "\x1b[38;5;81mfunc\x1b[0m \x1b[38;5;188mdoThing%d\x1b[0m(a, b int) {", y)
		// pad the rest of the line
		rest := w - 30
		if rest > 0 {
			b.WriteString(strings.Repeat(" ", rest))
		}
	}
	e.Write([]byte(b.String()))
}

func BenchmarkVTRender(b *testing.B) {
	const w, h = 120, 40
	e := vt.NewSafeEmulator(w, h)
	fillNvimLike(e, w, h)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = e.Render()
	}
}

func BenchmarkRenderPaneBody(b *testing.B) {
	const w, h = 120, 40
	p := &Pane{vt: vt.NewSafeEmulator(w, h)}
	fillNvimLike(p.vt, w, h)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = renderPaneBody(p, w, h)
	}
}

func BenchmarkCompositorOneLayer(b *testing.B) {
	const w, h = 120, 40
	p := &Pane{vt: vt.NewSafeEmulator(w, h)}
	fillNvimLike(p.vt, w, h)
	body := renderPaneBody(p, w, h)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		layer := lipgloss.NewLayer(body).X(0).Y(1).Z(2)
		_ = lipgloss.NewCompositor(layer).Render()
	}
}
