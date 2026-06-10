package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the on-disk form of ~/.config/gwam/config.toml. Today it only
// carries binding overrides; future sections (style, custom actions, ...)
// will hang off this struct.
type Config struct {
	Bindings []ConfigBinding `toml:"binding"`
}

// ConfigBinding is one [[binding]] entry. Fields mirror BindingSpec but in
// TOML-shaped form: Key is a string parsed by ParseKey (accepts modifier
// syntax like "ctrl-t" / "C-t" / "alt-up" / named keys / "F1".."F12").
// Args is the raw map the action's Parse will consume at keymap-build time.
//
//	[[binding]]
//	key    = "v"
//	action = "pane.split-v"
//
//	[[binding]]
//	key    = "H"
//	action = "pane.resize"
//	args   = { dir = "left" }
//	label  = "Big resize left"
//
//	# direct = true fires without the prefix, but permanently swallows
//	# the keystroke from every shell inside gwam.
//	[[binding]]
//	key    = "ctrl-t"
//	action = "tab.pick"
//	direct = true
//
// Label/Group override the action's display copy in the prefix overlay
// (same fields as BindingSpec.Label/Group). Direct mirrors
// BindingSpec.Trigger.Direct.
type ConfigBinding struct {
	Key    string         `toml:"key"`
	Action string         `toml:"action"`
	Args   map[string]any `toml:"args"`
	Label  string         `toml:"label"`
	Group  string         `toml:"group"`
	Direct bool           `toml:"direct"`
}

// configPath resolves the user config location. Honors $XDG_CONFIG_HOME for
// parity with templatesDir(); falls back to $HOME/.config/gwam/config.toml.
func configPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "gwam", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".config", "gwam", "config.toml"), nil
}

// loadConfig reads the user config if it exists. A missing file is not an
// error — returns (nil, nil) so the caller treats "no config" as "use
// defaults only". Malformed TOML errors out with the path included so the
// user can locate the file.
func loadConfig() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return &c, nil
}

// configBindingsToSpecs converts each ConfigBinding into the BindingSpec
// shape buildKeymap expects. Errors carry the binding index and source
// key so a user staring at config.toml can find the offending entry.
// Action ID resolution and Args validation happen later in buildKeymap;
// this layer only handles the TOML-shaped → in-process conversion.
func configBindingsToSpecs(cbs []ConfigBinding) ([]BindingSpec, error) {
	specs := make([]BindingSpec, 0, len(cbs))
	for i, cb := range cbs {
		if cb.Action == "" {
			return nil, fmt.Errorf("binding[%d]: action is required", i)
		}
		key, err := ParseKey(cb.Key)
		if err != nil {
			return nil, fmt.Errorf("binding[%d] (key=%q): %w", i, cb.Key, err)
		}
		specs = append(specs, BindingSpec{
			Trigger:  Trigger{Key: key, Direct: cb.Direct},
			ActionID: cb.Action,
			Args:     cb.Args,
			Group:    cb.Group,
			Label:    cb.Label,
		})
	}
	return specs, nil
}

// applyUserConfig loads ~/.config/gwam/config.toml, merges its binding
// entries over defaultBindings, and replaces the package-level
// defaultKeymap with the result. A missing config is a no-op; a present
// but malformed config returns an error so run() can surface it before
// raw mode is enabled. Called once at startup; defaultKeymap is read
// concurrently from the stdin pump but only after this returns, so the
// pointer swap is safely visible (happens-before via goroutine spawn).
func applyUserConfig() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg == nil || len(cfg.Bindings) == 0 {
		return nil
	}
	overrides, err := configBindingsToSpecs(cfg.Bindings)
	if err != nil {
		return err
	}
	km, err := buildKeymap(applyOverrides(defaultBindings, overrides))
	if err != nil {
		return err
	}
	defaultKeymap = km
	return nil
}

// checkConfig is the implementation of `gwam config check`. It reports the
// resolved config path, whether the file is present and parses cleanly,
// and the full effective binding table (defaults + overrides). Errors
// during load or keymap build are returned so the command exits non-zero;
// a missing file is not an error — it's a normal state worth printing.
func checkConfig(w io.Writer) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "config: %s\n", path)

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	var specs []BindingSpec
	switch {
	case cfg == nil:
		fmt.Fprintln(w, "status: not present (using defaults only)")
		specs = defaultBindings
	case len(cfg.Bindings) == 0:
		fmt.Fprintln(w, "status: OK (no [[binding]] entries — using defaults only)")
		specs = defaultBindings
	default:
		overrides, err := configBindingsToSpecs(cfg.Bindings)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "status: OK (%d override(s))\n", len(overrides))
		specs = applyOverrides(defaultBindings, overrides)
	}

	km, err := buildKeymap(specs)
	if err != nil {
		return err
	}

	fmt.Fprintln(w, "\neffective bindings:")
	printBindings(w, km.bindings)
	return nil
}

// printBindings writes one row per binding, grouped by effective group
// with group headers, columns aligned. Used by `gwam config check` so
// users can see at a glance which key fires which action with which args.
func printBindings(w io.Writer, bs []*Binding) {
	if len(bs) == 0 {
		return
	}
	// Bucket by group preserving the input order's first-seen-group order.
	var groupOrder []string
	seen := map[string]bool{}
	groups := map[string][]*Binding{}
	for _, b := range bs {
		g := b.EffectiveGroup()
		if !seen[g] {
			seen[g] = true
			groupOrder = append(groupOrder, g)
		}
		groups[g] = append(groups[g], b)
	}

	// Pre-compute column widths across all rows so cross-group columns
	// still align — easier to scan than per-group alignment. Direct
	// bindings show their full canonical form ("ctrl-t") plus a "(direct)"
	// marker so the user can tell at a glance which keys fire without the
	// prefix.
	keyLabel := func(b *Binding) string {
		s := b.Trigger.Key.String()
		if b.Trigger.Direct {
			s += " (direct)"
		}
		return s
	}
	keyW, actW, lblW := 3, 0, 0
	for _, b := range bs {
		if w := len(keyLabel(b)); w > keyW {
			keyW = w
		}
		if w := len(b.Action.ID); w > actW {
			actW = w
		}
		if w := len(b.EffectiveLabel()); w > lblW {
			lblW = w
		}
	}

	for _, g := range groupOrder {
		fmt.Fprintf(w, "  [%s]\n", g)
		for _, b := range groups[g] {
			line := fmt.Sprintf("    %-*s  %-*s  %-*s",
				keyW, keyLabel(b),
				actW, b.Action.ID,
				lblW, b.EffectiveLabel(),
			)
			if a := formatArgs(b.Args); a != "" {
				line += "  " + a
			}
			fmt.Fprintln(w, line)
		}
	}
}

// formatArgs stringifies a parsed Args value for the check listing.
// Returns "" when there are no args. The output is informational only,
// not a TOML round-trip; struct field names depend on the action's
// args type, so we use reflection-free type switches on the few shapes
// we know about today.
func formatArgs(args any) string {
	switch a := args.(type) {
	case nil:
		return ""
	case *resizeArgs:
		var dir string
		switch a.dir {
		case splitV:
			if a.step < 0 {
				dir = "left"
			} else {
				dir = "right"
			}
		case splitH:
			if a.step < 0 {
				dir = "up"
			} else {
				dir = "down"
			}
		}
		return fmt.Sprintf("args={dir=%s}", dir)
	case *tabJumpArgs:
		return fmt.Sprintf("args={idx=%d}", a.idx)
	case *popupToggleArgs:
		s := fmt.Sprintf("args={name=%s", a.name)
		if a.cmd != "" {
			s += fmt.Sprintf(" cmd=%q", a.cmd)
		}
		if a.cwd != "" {
			s += fmt.Sprintf(" cwd=%q", a.cwd)
		}
		return s + fmt.Sprintf(" width=%.2f height=%.2f}", a.width, a.height)
	default:
		// Unknown action arg type; fall through to a best-effort %+v so
		// future additions still show *something* without needing this
		// switch updated.
		return fmt.Sprintf("args=%+v", args)
	}
}
