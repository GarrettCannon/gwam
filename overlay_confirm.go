package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// ConfirmOverlay is a y/n prompt. Anchored a third of the way down so the
// thing being confirmed stays visible. Esc/Ctrl-C and 'n' both invoke onNo
// (or no-op if nil); 'y' and Enter invoke onYes. Any other key is ignored —
// no accidental commits from stray input.
//
// title is the panel header ("quit?"); prompt is the body line ("close
// gwam and all sessions?"). Both are caller-supplied so the overlay stays
// content-agnostic.
type ConfirmOverlay struct {
	title  string
	prompt string
	onYes  func()
	onNo   func() // optional
}

func NewConfirmOverlay(title, prompt string, onYes func()) *ConfirmOverlay {
	return &ConfirmOverlay{title: title, prompt: prompt, onYes: onYes}
}

// WithOnNo registers a callback to run when the user declines. Without it,
// declining is a silent close.
func (c *ConfirmOverlay) WithOnNo(fn func()) *ConfirmOverlay {
	c.onNo = fn
	return c
}

func (c *ConfirmOverlay) Anchor() Anchor  { return AnchorFractionalY{Frac: 1.0 / 3.0} }
func (c *ConfirmOverlay) OwnsInput() bool { return true }

func (c *ConfirmOverlay) Render(_ *Model) string {
	lines := []string{
		prefixPanelTitle.Render("» " + c.title),
		"",
		c.prompt,
		"",
		prefixHint.Render("y: yes   n: no   esc: cancel"),
	}
	return prefixPanel.Render(strings.Join(lines, "\n"))
}

func (c *ConfirmOverlay) HandleKey(data []byte, _ *Model) (bool, tea.Cmd) {
	for _, b := range data {
		switch b {
		case 'y', 'Y', 0x0d, 0x0a: // y / enter — accept
			if c.onYes != nil {
				c.onYes()
			}
			return true, nil
		case 'n', 'N', 0x1b, 0x03: // n / esc / ctrl-c — decline
			if c.onNo != nil {
				c.onNo()
			}
			return true, nil
		}
		// any other byte is ignored — don't auto-commit on stray input
	}
	return false, nil
}
