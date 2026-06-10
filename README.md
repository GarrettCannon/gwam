# gwam

A small tmux-style terminal multiplexer written in Go using bubbletea v2,
lipgloss v2, and `charmbracelet/x/vt`.

## Run

```
gwam              # start session "main"
gwam work         # start session "work"
gwam -t work      # load tabs from ~/.config/gwam/templates/work.toml
                  #   (session inherits the template name unless overridden)
gwam -t work hub  # load template "work" into a session named "hub"
gwam templates    # list installed templates and their tabs
gwam config check # validate ~/.config/gwam/config.toml + print effective bindings
gwam --help
gwam --version
```

## Templates

A template is a TOML file under `~/.config/gwam/templates/<name>.toml` that
pre-spawns a set of tabs when you launch with `-t <name>`:

```toml
# applies to every tab unless overridden
cwd = "~/projects/gwam"

[[tabs]]
name = "editor"
cmd  = "nvim"

[[tabs]]
name = "server"
cmd  = "go run ."

[[tabs]]
name = "logs"
cwd  = "/var/log"
cmd  = "tail -f app.log"

[[tabs]]
name = "shell"     # no cmd — just a shell at the template cwd
```

Fields:

- Top-level `cwd` — default working directory for every tab.
- `[[tabs]]` — repeat once per tab, spawned in file order. First tab is focused.
  - `name` — sets the tab chip label (same field the `,` rename binding writes).
  - `cwd` — overrides the top-level default for this tab; `~` is expanded.
  - `cmd` — written to the tab's shell as one line after spawn. The shell
    echoes it, runs it, and prints another prompt — so when the command exits
    (or you Ctrl-C it) you land back in a live shell instead of closing the tab.

## Prefix and bindings

The prefix is **Ctrl-A**. Press it, then a key:

| Key       | Action                                              |
|-----------|-----------------------------------------------------|
| c         | new tab in the current session                      |
| n / p     | next / previous tab (wraps)                         |
| 1-9       | jump to tab N                                       |
| ,         | rename the current tab (empty clears)               |
| &         | kill the current tab and all its panes (cascades)   |
| &#124;    | split the active pane vertically (left/right)       |
| _         | split the active pane horizontally (top/bottom)     |
| o         | cycle focus to the next pane in the current tab     |
| x         | kill the active pane (cascades: last pane → tab)    |
| h / l     | move nearest vertical divider left / right (5%)     |
| j / k     | move nearest horizontal divider down / up (5%)      |
| s         | new session (one fresh tab)                         |
| w         | next session (wraps)                                |
| W         | pick a session (search + select)                    |
| $         | rename the current session                          |
| T         | pick a tab in the current session (search + select) |
| m         | toggle mouse mode (on by default) — flashes a toast |

Also, **outside the prefix**: `ctrl-t` directly opens the tab picker. See
"Direct bindings" below for the tradeoff this carries.
| q         | quit                                                |
| esc       | cancel the prefix                                   |

While the prefix is armed, a yellow cheatsheet panel pops up in the top
right showing the bindings.

## Config

`~/.config/gwam/config.toml` (or `$XDG_CONFIG_HOME/gwam/config.toml`) lets
you override or add prefix bindings. The file is optional — without it,
the defaults above apply.

```toml
# rebind: prefix v also splits vertically
[[binding]]
key    = "v"
action = "pane.split-v"

# add: prefix H takes a bigger resize left, with a custom overlay label
[[binding]]
key    = "H"
action = "pane.resize"
args   = { dir = "left" }
label  = "Big resize left"

# direct binding: ctrl-t opens the tab picker WITHOUT pressing the prefix
# first. This permanently swallows ctrl-t from every shell inside gwam.
[[binding]]
key    = "ctrl-t"
action = "tab.pick"
direct = true
```

Merge semantics: each `[[binding]]` whose `(key, direct)` tuple matches a
default replaces that default in place; a new pair appends. Defaults keep
their original order in the prefix overlay; new bindings land at the end
of their group.

Fields:

- `key` — required. Parsed by gwam's key-string syntax: plain printable
  ASCII (`"v"`, `"?"`), modifier combinations (`"ctrl-t"` / `"C-t"`,
  `"alt-x"` / `"M-x"`), or named keys (`"enter"`, `"esc"`, `"tab"`,
  `"space"`, `"backspace"`, `"F1"`..`"F12"`, plus arrows / home / end /
  pgup / pgdn / insert / delete). Modifier order and case don't matter.
- `action` — required, one of the action IDs below.
- `args` — table of typed args; required only for parameterized actions.
- `label` — optional, overrides the action's overlay label for this key.
- `group` — optional, overrides the action's overlay group for this key.
- `direct` — optional (default false). See "Direct bindings" below.

### Direct bindings

A binding with `direct = true` fires straight from stdin — no prefix
press required. The cost: that exact key is permanently invisible to
every shell or app running inside gwam. `ctrl-t` is bash's
transpose-chars, fish's pager toggle, vim's tag-jump; pick what you
direct-bind carefully.

v1 only supports single-byte legacy encodings for the key:

- printable ASCII (`"a"`, `"?"`)
- `ctrl-<letter>` (a..z, A..Z)
- `ctrl-space` (sends NUL)
- the named keys `enter`, `tab`, `esc`, `space`, `backspace`

`alt-*`, arrows, F-keys, and other multi-byte keys parse fine but error
out at startup with "not encodable as legacy bytes" — they need a
multi-byte matcher in the input pump that hasn't been built yet.

Ctrl-A is reserved as the prefix; binding it directly errors out with a
conflict message.

### Action IDs

| ID                | Args                                          | Notes                                  |
|-------------------|-----------------------------------------------|----------------------------------------|
| `tab.new`         | —                                             |                                        |
| `tab.next`        | —                                             |                                        |
| `tab.prev`        | —                                             |                                        |
| `tab.rename`      | —                                             | owns input while rename overlay is up  |
| `tab.kill`        | —                                             | cascades tab → session → quit          |
| `tab.jump`        | `{ idx = N }`                                 | 0-based                                |
| `tab.pick`        | —                                             | opens a picker over the current session's tabs |
| `pane.split-v`    | —                                             |                                        |
| `pane.split-h`    | —                                             |                                        |
| `pane.cycle`      | —                                             |                                        |
| `pane.kill`       | —                                             | cascades pane → tab → session → quit   |
| `pane.resize`     | `{ dir = "left"\|"right"\|"up"\|"down" }`     | moves nearest matching divider by 5%   |
| `session.new`     | —                                             |                                        |
| `session.next`    | —                                             |                                        |
| `session.pick`    | —                                             | opens a picker over all sessions       |
| `session.rename`  | —                                             | owns input while rename overlay is up  |
| `mouse.toggle`    | —                                             |                                        |
| `quit`            | —                                             |                                        |

### Checking your config

`gwam config check` parses the file and prints the full effective binding
table (defaults + overrides resolved). Use it to confirm a rebind landed
where you expected; errors surface here with the offending binding's
index and key so you can find them in the source file.

## Panes

A tab can be split into multiple panes — each pane is an independent
shell with its own pty, scrollback, and cursor. Panes are arranged in a
binary tree of splits: every split has a direction (vertical = a
left/right pair, horizontal = a top/bottom pair) and a ratio (the
fraction of the tab body occupied by the first child).

- `prefix |` splits the active pane into a left/right pair at 50/50.
- `prefix _` splits the active pane into a top/bottom pair at 50/50.
- `prefix o` cycles focus through panes in layout order (wraps).
- `prefix x` kills the active pane. If it was the only pane in the tab,
  the tab closes too; if it was the last tab in the session, the session
  closes; if it was the last session, gwam quits.
- `prefix h` / `prefix l` move the **nearest enclosing vertical**
  divider left / right by 5% of the available width.
- `prefix j` / `prefix k` move the **nearest enclosing horizontal**
  divider down / up by 5% of the available height.

Each pane keeps its own scrollback and only the active pane responds to
the mouse wheel. The hardware cursor follows the active pane.

Dividers are drawn as plain `│` / `─` lines in dim grey. T-junctions
where a vertical and horizontal divider meet aren't drawn specially —
one overlaps the other.

## Tab labels

Each tab chip shows `<n> - <label>`. The label is:

1. An explicit name set via the rename binding (`prefix ,`) if any.
2. Otherwise the label of the **active pane**, which is:
   1. The name of the current foreground process (e.g. `ping`, `vim`)
      when anything other than the shell owns the pty's foreground
      process group.
   2. Otherwise the basename of the shell's current working directory —
      read via OSC 7 if the shell emits it, else resolved per-platform
      (`lsof` on macOS, `/proc/<pid>/cwd` on Linux). Updates every 500 ms.

## Rename overlay

`prefix ,` renames the current tab; `prefix $` renames the current
session. Both pop a small centered input box: type ASCII, Enter to
confirm, Esc (or Ctrl-C) to cancel, Backspace to edit. Renaming a tab
with an empty buffer clears the override and falls back to the
auto-derived label. While the overlay is up, mouse/wheel events and the
prefix key are dropped — keystrokes are owned by the input field.

## Sessions

A session is a named bundle of tabs. The session name shows as a green
chip in the top-right area of the tab bar. New sessions are named `s1`,
`s2`, ... by default; the first session is named by the CLI argument
(default `main`).

Exiting the last tab of the last session quits gwam (tmux-style cascade).

## Scrollback

Mouse wheel scrolls the active tab's history. We rely on enabling
xterm SGR mouse reporting (DECSET ?1000 + ?1006) so the host terminal
emits actual mouse events instead of converting wheel into arrow keys —
that's why `mouse mode` exists as a toggle. Cost: while it's on, plain
click-and-drag text selection is hijacked; hold Option (macOS) to fall
back to native selection.

While scrolled:

- An orange `SCROLL N/M` chip appears in the tab bar.
- Any keystroke snaps back to live.
- Non-wheel mouse events (clicks, drags) still forward to the inner pty,
  so vim/less mouse keeps working.

## Input handling

The stdin pump owns input routing. It:

- Catches Ctrl-A (legacy `0x01` and kitty `\x1b[97;5u`) to arm the prefix.
- Decodes kitty keyboard CSI-u sequences (e.g. `\x1b[99;5u` → `0x03`) back
  to legacy bytes so the inner shell understands them. Bubbletea pushes
  kitty mode at startup, so without this Ctrl-C wouldn't reach a running
  command like `ping`.
- Skips terminal device-report responses (`\x1b[?…` / OSC) that bubbletea's
  capability queries trigger.
- Forwards everything else to the active tab's pty.

## Overlays

Built on `lipgloss.Compositor` + `Layer`. A small `Overlay` interface
(`overlay.go`) plus a stack on `Model` powers four kinds of floating UI:

- **Rename** (`overlay_rename.go`) — title + single-line input. Powers
  `prefix ,` and `prefix $`.
- **Picker** (`overlay_picker.go`) — search input + filtered list with
  arrow / Ctrl-P,N navigation, prefix-match filter. Powers `prefix W`
  (sessions) and `prefix T` (tabs).
- **Confirm** (`overlay_confirm.go`) — y/n prompt with a callback. No
  default binding yet.
- **Notice** (`overlay_notice.go`) — passive auto-dismissing toast in the
  top-right corner; doesn't own input, so the user can keep typing. Used
  for the `prefix m` mouse-mode flash.

Interactive overlays (rename, picker, confirm) divert keystrokes from the
stdin pump via `overlayKeyMsg`; passive overlays (notices) don't, and
the keys flow past them. Multiple overlays can stack — the topmost
interactive one owns input and Esc.

The **prefix cheatsheet** isn't an overlay — it's the visual half of
prefix mode, drawn on top of the overlay stack. Anchored to the right
edge so it sits flush under the `PREFIX C-A` chip when armed.

## Code layout

Per-feature Go files:

- `main.go` — CLI (cobra), `run`, `spawnInitialTabs`, debug log.
- `template.go` — TOML template loading and the `templates` listing.
- `model.go` — `Pane`, `Layout`, `Tab`, `Session`, `Model` types + tea
  messages; tree helpers (`leaves`, `findLeaf`, `splitLeaf`,
  `collapseLeaf`, `nearestSplit`); `cur*` accessors and `closePane` (the
  pane → tab → session → quit cascade).
- `actions.go` — `Action{ID, Label, Help, Group, Status, Flags, Parse,
  Run}` + the global `actions` registry. `Ctx{M, Args}` is what every
  action's `Run` receives.
- `actions_builtin.go` — one `registerAction` call per built-in action,
  each a thin wrapper around the matching `act*` method.
- `keymap.go` — `Trigger`, `BindingSpec` (raw declaration form),
  `Binding` (resolved form), `Keymap` with prefix-byte index;
  `buildKeymap` validates args up-front; `applyOverrides` overlays user
  config on top of defaults. `defaultKeymap` is built at init from
  defaults; `applyUserConfig` replaces it with the merged keymap.
- `bindings.go` — `defaultBindings` (the in-process binding table that
  references action IDs) and all `act*` methods.
- `config.go` — `Config`/`ConfigBinding` TOML types, `loadConfig`,
  `applyUserConfig` (called from `run()`), and `checkConfig` (the
  `gwam config check` listing).
- `pty.go` — `spawnPane` starts a shell under a pty and wires the vt
  emulator's `Title`, `CursorVisibility`, and `WorkingDirectory` (OSC 7)
  callbacks; `resizePane` keeps the pty winsize and emulator in lockstep;
  `pollCmd` + `refreshPane` drive the 500ms fg-cmd / cwd poll.
- `update.go` — `Model.Init`, `Model.Update`, `applyLayoutSizes` (the
  walk that resizes every pane to its current rect after any layout
  change).
- `view.go` — styles, `Model.View`, layout math (`Rect`, `computeRects`,
  `collectDividers`), and the body / scrollback / overlay renderers.
- `overlay.go` — `Overlay` interface, `Anchor` impls (center, top-right,
  fractional-y), `Model.overlays` stack, push/pop/remove helpers, and
  `overlayKeyMsg`.
- `overlay_rename.go`, `overlay_picker.go`, `overlay_confirm.go`,
  `overlay_notice.go` — concrete overlay kits. Each is one struct
  implementing `Overlay`, plus a small constructor.
- `input.go` — the stdin pump (`startInputPump`): catches Ctrl-A (legacy
  `0x01` and kitty `\x1b[97;5u`), decodes kitty CSI-u keystrokes back to
  legacy bytes, drops terminal device-report responses, and forwards
  everything else to the active pane's pty.
- `platform_darwin.go` — macOS termios + process lookups (sysctl, `lsof`).
- `platform_linux.go` — Linux termios + process lookups (`/proc/<pid>/comm`,
  `/proc/<pid>/cwd`).

The `overlayOwnsInput` atomic lets the stdin pump divert keystrokes into
`overlayKeyMsg` while any interactive overlay is up; `Update` routes the
byte batch to the topmost interactive overlay's `HandleKey`. The
`activePty` atomic always points at the currently focused pane's pty so
the pump can passthrough raw bytes without going through `Update`.

## Known limits / open work

- macOS + Linux only — Windows would need a different pty backend and
  process-lookup story.
- No "detach / attach" — gwam is a single-process foreground tool.
- Pane dividers don't draw junctions (├ ┤ ┬ ┴ ┼) at intersections —
  whichever divider was added later overdraws the other.
- No visual highlight on the active pane's borders — focus is identified
  by the hardware cursor only.
- Pane renames aren't supported — `,` renames the tab; pane labels are
  always auto-derived from their fg-process / cwd.
- The cheatsheet has no nested groups (LazyVim-style sub-panels).
- Title parsing assumes well-behaved shells for OSC 7; idle fallback via
  per-platform process lookup covers shells that don't emit it.
