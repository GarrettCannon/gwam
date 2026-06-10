package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/BurntSushi/toml"
)

// Template is the on-disk form of a `gwam -t <name>` preset: a CWD that
// applies to every tab unless overridden, plus an ordered list of tab
// specs. Loaded from ~/.config/gwam/templates/<name>.toml.
type Template struct {
	CWD  string    `toml:"cwd"`
	Tabs []TabSpec `toml:"tabs"`
}

type TabSpec struct {
	Name string `toml:"name"`
	CWD  string `toml:"cwd"`
	Cmd  string `toml:"cmd"`
}

// templatesDir returns the directory templates are read from. Always
// $XDG_CONFIG_HOME/gwam/templates (or $HOME/.config/gwam/templates) on all
// platforms — os.UserConfigDir would point at ~/Library/Application Support
// on macOS, which is wrong for a CLI tool.
func templatesDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "gwam", "templates"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".config", "gwam", "templates"), nil
}

// loadTemplate reads <templatesDir>/<name>.toml and decodes it. Returns a
// wrapped error if the file is missing or malformed.
func loadTemplate(name string) (*Template, error) {
	dir, err := templatesDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name+".toml")
	var t Template
	if _, err := toml.DecodeFile(path, &t); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(t.Tabs) == 0 {
		return nil, fmt.Errorf("template %s has no [[tabs]] entries", name)
	}
	return &t, nil
}

// listTemplates prints one row per *.toml file in templatesDir() — the bare
// name plus a one-line summary of its tabs ("editor → nvim, server → go run .").
// Malformed templates appear as "<name>  (error: ...)" so the listing surfaces
// problems instead of silently skipping them.
func listTemplates(out io.Writer) error {
	dir, err := templatesDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(out, "no templates directory: %s\n", dir)
			return nil
		}
		return err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
	}
	if len(names) == 0 {
		fmt.Fprintf(out, "no templates in %s\n", dir)
		return nil
	}
	// names come back from ReadDir already sorted lexically, so we can rely
	// on that for stable output. Column width uses display cells (via
	// lipgloss.Width) so wide-rune names align.
	nameW := 0
	for _, n := range names {
		if w := lipgloss.Width(n); w > nameW {
			nameW = w
		}
	}
	pad := func(name string) string {
		if gap := nameW - lipgloss.Width(name); gap > 0 {
			return name + strings.Repeat(" ", gap)
		}
		return name
	}
	for _, name := range names {
		t, err := loadTemplate(name)
		if err != nil {
			fmt.Fprintf(out, "%s  (error: %v)\n", pad(name), err)
			continue
		}
		fmt.Fprintf(out, "%s  %s\n", pad(name), summarizeTabs(t.Tabs))
	}
	return nil
}

// summarizeTabs reduces a template's tabs to one line for the templates
// listing: each tab as "<name>" if it's a bare shell, "<name> → <cmd>" if a
// command runs. Long commands are truncated with an ellipsis.
func summarizeTabs(tabs []TabSpec) string {
	const maxCmd = 30
	parts := make([]string, 0, len(tabs))
	for _, t := range tabs {
		name := t.Name
		if name == "" {
			name = "(unnamed)"
		}
		if t.Cmd == "" {
			parts = append(parts, name)
			continue
		}
		cmd := t.Cmd
		if len(cmd) > maxCmd {
			cmd = cmd[:maxCmd-1] + "…"
		}
		parts = append(parts, name+" → "+cmd)
	}
	return strings.Join(parts, ", ")
}

// expandHome rewrites a leading "~/" to the user's home directory. Bare "~"
// is also expanded. Paths without "~" are returned unchanged so absolute and
// already-resolved paths flow through.
func expandHome(p string) string {
	if p == "" {
		return p
	}
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
