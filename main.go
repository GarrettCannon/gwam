package main

import (
	"fmt"
	"io"
	"os"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var dbg io.Writer = io.Discard

func dlog(tag string, b []byte) {
	if dbg == io.Discard {
		return
	}
	fmt.Fprintf(dbg, "[%s] %q\n", tag, b)
}

const version = "0.0.1"

var templateFlag string

var rootCmd = &cobra.Command{
	Use:     "gwam [SESSION]",
	Short:   "A tmux-style terminal multiplexer",
	Long:    "gwam is a small terminal multiplexer. Run it with no arguments\nto start a session called \"main\", or pass a name to label it. Pass -t/--template to\nopen a preset set of tabs from ~/.config/gwam/templates/<name>.toml.",
	Version: version,
	Args:    cobra.MaximumNArgs(1),
	Run: func(_ *cobra.Command, args []string) {
		sessionName := ""
		if len(args) == 1 {
			sessionName = args[0]
		}
		run(sessionName, templateFlag)
	},
}

var templatesCmd = &cobra.Command{
	Use:   "templates",
	Short: "List templates available under ~/.config/gwam/templates",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		return listTemplates(os.Stdout)
	},
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect ~/.config/gwam/config.toml",
}

var configCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Validate the config and print the effective bindings",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		return checkConfig(os.Stdout)
	},
}

func init() {
	rootCmd.Flags().StringVarP(&templateFlag, "template", "t", "",
		"Load tabs from ~/.config/gwam/templates/<name>.toml")
	rootCmd.AddCommand(templatesCmd)
	configCmd.AddCommand(configCheckCmd)
	rootCmd.AddCommand(configCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// spawnInitialTabs returns the tab(s) the program should start with. With
// no template that's a single bare shell; with a template it's one tab per
// [[tabs]] entry, in file order, with cwd/cmd applied. On partial failure
// (template tab N fails to spawn), already-spawned panes are torn down so
// their pty masters and child shells don't leak before run() exits. Each tab
// starts with a single pane — splits are user-driven, not template-driven.
func spawnInitialTabs(rows, cols int, tmpl *Template) ([]*Tab, error) {
	if tmpl == nil {
		p, err := spawnPane(rows, cols, SpawnOpts{})
		if err != nil {
			return nil, err
		}
		return []*Tab{newSinglePaneTab(p, "")}, nil
	}
	defaultCWD := expandHome(tmpl.CWD)
	tabs := make([]*Tab, 0, len(tmpl.Tabs))
	for _, spec := range tmpl.Tabs {
		cwd := expandHome(spec.CWD)
		if cwd == "" {
			cwd = defaultCWD
		}
		p, err := spawnPane(rows, cols, SpawnOpts{
			CWD:     cwd,
			InitCmd: spec.Cmd,
		})
		if err != nil {
			for _, prev := range tabs {
				prev.active.pty.Close()
			}
			return nil, err
		}
		tabs = append(tabs, newSinglePaneTab(p, spec.Name))
	}
	return tabs, nil
}

// run starts the gwam main loop. sessionName is the positional CLI arg
// ("" if none). templateName is the -t flag value ("" if none). When a
// template is given and the positional is empty, the session inherits
// the template name; otherwise default to "main".
func run(sessionName, templateName string) {
	if os.Getenv("GWAM_DEBUG") != "" {
		f, err := os.Create("/tmp/gwam-debug.log")
		if err == nil {
			dbg = f
			defer f.Close()
		}
	}

	// Overlay user config on top of defaultBindings before anything else
	// touches the keymap. defaultKeymap was built at init from defaults
	// only; if the user has a config.toml we rebuild it with overrides
	// merged in. Failing here exits before raw mode flips so the terminal
	// is left in a sane state.
	if err := applyUserConfig(); err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	// raw mode on stdin — we own input routing
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "raw mode:", err)
		os.Exit(1)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// MakeRaw also clears OPOST/ONLCR — kernel-level "\n -> \r\n" translation.
	// bubbletea v2 sees our input pipe is not a tty and assumes the kernel will
	// add CRs, so it emits plain \n between rendered rows. without OPOST those
	// LFs only advance the row and keep the column, so every body row after the
	// first starts at the previous row's trailing column. re-enable here.
	setOPOST(int(os.Stdin.Fd()))

	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		w, h = 80, 24
	}

	// Resolve session name with the template's name as the fallback so
	// `gwam -t work` names the session "work" (positional arg still wins).
	var tmpl *Template
	if templateName != "" {
		t, err := loadTemplate(templateName)
		if err != nil {
			fmt.Fprintln(os.Stderr, "template:", err)
			os.Exit(1)
		}
		tmpl = t
	}
	if sessionName == "" {
		if templateName != "" {
			sessionName = templateName
		} else {
			sessionName = "main"
		}
	}

	tabs, err := spawnInitialTabs(h-tabBarH, w, tmpl)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spawn:", err)
		os.Exit(1)
	}

	activePty := &atomic.Pointer[os.File]{}
	activePty.Store(tabs[0].active.pty)
	mouseOn := &atomic.Bool{}
	mouseOn.Store(true)
	writeMouseMode(true)
	inScroll := &atomic.Bool{}
	overlayOwnsInput := &atomic.Bool{}
	defer func() {
		if mouseOn.Load() {
			writeMouseMode(false)
		}
	}()

	m := &Model{
		sessions:         []*Session{{name: sessionName, tabs: tabs}},
		w:                w,
		h:                h,
		activePty:        activePty,
		mouseOn:          mouseOn,
		inScroll:         inScroll,
		overlayOwnsInput: overlayOwnsInput,
	}

	// don't let bubbletea read stdin — we passthrough raw to active pty
	emptyR, emptyW, _ := os.Pipe()
	defer emptyR.Close()
	defer emptyW.Close()

	p := tea.NewProgram(m,
		tea.WithInput(emptyR),
		tea.WithWindowSize(w, h),
		// force TrueColor — bubbletea's detection downgraded dim/grey
		// (fish autosuggestion uses SGR 90), making it match foreground.
		tea.WithColorProfile(colorprofile.TrueColor),
	)

	startInputPump(p, activePty, mouseOn, inScroll, overlayOwnsInput)

	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
	}
}
