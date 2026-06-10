package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// Ctx is the argument every Action.Run receives. M is the whole program
// state; Args is this binding's pre-parsed typed args (nil if the Action
// takes none). Future cross-cutting concerns (Send, Logger, ...) hang off
// Ctx so individual actions don't need new parameters.
type Ctx struct {
	M    *Model
	Args any
}

// ActionFlag is a bitset of action-level traits the dispatch path needs to
// know about before invoking Run. Today the only flag tells the stdin pump
// the action will open an interactive overlay that takes over input routing.
type ActionFlag uint32

const (
	// FlagOwnsInput means the action opens an interactive overlay that
	// takes over keystroke routing. The stdin pump flips
	// overlayOwnsInput.Store(true) on dispatch so subsequent bytes in the
	// same chunk get diverted to overlayKeyMsg instead of leaking to the
	// active pty before Update has had a chance to push the overlay.
	FlagOwnsInput ActionFlag = 1 << iota
)

// Action is a registered primitive — the unit a Binding points at. Display
// fields (Label/Help/Group/Status) live here, not on the binding, so the
// overlay reads them once per action regardless of how many keys bind it.
//
//   - ID     — stable identifier ("tab.new", "pane.resize"); the keymap
//     stores this string, the registry resolves it at lookup.
//   - Label  — overlay row text ("Resize left").
//   - Help   — full sentence for help surfaces.
//   - Group  — overlay section ("tabs", "panes", ...); "" = general.
//   - Status — optional live suffix (e.g. mouse "(on)"). Reads Model, not
//     Ctx, because the panel renders bindings without firing them.
//   - Flags  — see ActionFlag.
//   - Parse  — converts a raw TOML arg map into the action's typed args
//     struct, validating up-front. nil means "takes no args";
//     callers MUST pass Args = nil for actions without a Parse.
//   - Run    — executes against ctx; ctx.Args is whatever Parse returned.
type Action struct {
	ID     string
	Label  string
	Help   string
	Group  string
	Status func(*Model) string
	Flags  ActionFlag
	Parse  func(raw map[string]any) (any, error)
	Run    func(*Ctx) tea.Cmd
}

// actions is the global registry, populated by init() funcs across
// actions_builtin.go. Keymap entries reference actions by ID so the
// table-of-bindings is plain data; resolution happens at dispatch time.
var actions = map[string]*Action{}

// registerAction inserts a into the global registry. Duplicate IDs panic
// at init — this is a programmer error, not a runtime condition.
func registerAction(a *Action) {
	if _, dup := actions[a.ID]; dup {
		panic(fmt.Sprintf("action %q already registered", a.ID))
	}
	actions[a.ID] = a
}
