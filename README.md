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
| space     | flip to the previously active tab                   |
| 1-9       | jump to tab N                                       |
| ,         | rename the current tab (empty clears)               |
| &         | kill the current tab and all its panes (cascades)   |
| &#124;    | split the active pane vertically (left/right)       |
| _         | split the active pane horizontally (top/bottom)     |
| o         | cycle focus to the next pane in the current tab     |
| ← → ↑ ↓   | move focus to the nearest pane in that direction    |
| x         | kill the active pane (cascades: last pane → tab)    |
| z         | zoom the active pane to full size (toggle)          |
| h / l     | move nearest vertical divider left / right (5%)     |
| j / k     | move nearest horizontal divider down / up (5%)      |
| s         | new session (one fresh tab)                         |
| w         | next session (wraps)                                |
| L         | flip to the previously active session               |
| W         | pick a session (search + select)                    |
| $         | rename the current session                          |
| T         | pick a tab in the current session (search + select) |
| !         | toggle the "scratch" popup (floating shell pane)    |
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

Bindable keys (direct or prefix):

- printable ASCII (`"a"`, `"?"`)
- `ctrl-<letter>` (a..z, A..Z) and `ctrl-space` (sends NUL)
- the named keys `enter`, `tab`, `esc`, `space`, `backspace`
- `alt-` + any of the single-byte keys above (`alt-x`, `alt-ctrl-g`) —
  except `alt-[` and `alt-O`, whose encodings collide with the CSI / SS3
  sequence introducers
- arrows / `home` / `end` / `pgup` / `pgdn` / `insert` / `delete` /
  `F1`..`F12`, alone or with any modifier combination (`ctrl-up`,
  `shift-F5`), plus `shift-tab`

Multi-byte keys are matched against their known legacy encodings (CSI,
SS3, and tilde variants — e.g. both `\x1b[A` and `\x1bOA` for `up`);
kitty-encoded keystrokes are decoded back to legacy form first, so both
terminal modes hit the same table. One caveat for `alt-*`: the chord is
recognized when ESC and the base byte arrive in the same read — which is
how terminals send it — but pressing Esc and the base key in *very*
quick succession can coalesce into one read and false-trigger. If you
live in vim and want `alt-x`, know that failure mode exists.

Ctrl-A is reserved as the prefix; binding it directly errors out with a
conflict message.

### Action IDs

| ID                | Args                                          | Notes                                  |
|-------------------|-----------------------------------------------|----------------------------------------|
| `tab.new`         | —                                             |                                        |
| `tab.next`        | —                                             |                                        |
| `tab.prev`        | —                                             |                                        |
| `tab.last`        | —                                             | flips to the previously active tab     |
| `tab.rename`      | —                                             | owns input while rename overlay is up  |
| `tab.kill`        | —                                             | cascades tab → session → quit          |
| `tab.jump`        | `{ idx = N }`                                 | 0-based                                |
| `tab.pick`        | —                                             | opens a picker over the current session's tabs |
| `pane.split-v`    | —                                             |                                        |
| `pane.split-h`    | —                                             |                                        |
| `pane.cycle`      | —                                             |                                        |
| `pane.select`     | `{ dir = "left"\|"right"\|"up"\|"down" }`     | moves focus to the nearest pane that way |
| `pane.kill`       | —                                             | cascades pane → tab → session → quit   |
| `pane.zoom`       | —                                             | toggle; layout untouched while zoomed  |
| `pane.resize`     | `{ dir = "left"\|"right"\|"up"\|"down" }`     | moves nearest matching divider by 5%   |
| `session.new`     | —                                             |                                        |
| `session.next`    | —                                             |                                        |
| `session.last`    | —                                             | flips to the previously active session |
| `session.pick`    | —                                             | opens a picker over all sessions       |
| `session.rename`  | —                                             | owns input while rename overlay is up  |
| `popup.toggle`    | `{ name, cmd, cwd, width, height }` (all opt) | floating session-scoped pane; see "Popups" |
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
- `prefix ←` / `→` / `↑` / `↓` move focus to the nearest pane in that
  direction. The pane sharing the most edge across the boundary wins;
  there's no wrap, so it's a no-op when nothing lies that way.
- `prefix x` kills the active pane. If it was the only pane in the tab,
  the tab closes too; if it was the last tab in the session, the session
  closes; if it was the last session, gwam quits.
- `prefix z` zooms the active pane to the full body (toggle). The split
  tree is untouched — unzooming restores the exact layout. A purple
  `ZOOM` chip shows in the tab bar while zoomed; splitting, cycling, or
  a pane dying unzooms first so the visible panes always match the tree.
- `prefix h` / `prefix l` move the **nearest enclosing vertical**
  divider left / right by 5% of the available width.
- `prefix j` / `prefix k` move the **nearest enclosing horizontal**
  divider down / up by 5% of the available height.

Each pane keeps its own scrollback and only the active pane responds to
the mouse wheel. The hardware cursor follows the active pane.

Dividers are drawn as `│` / `─` lines in dim grey, with proper junction
characters (`├ ┤ ┬ ┴ ┼`) where dividers meet. Divider segments bordering
the active pane are tinted purple so the focused pane reads at a glance.

## Popups

A popup is a floating pane that toggles on and off over the current
session — picture opening lazygit once at work start and flipping it up
whenever you need it. The popup's shell and whatever runs in it **keep
running while hidden**: toggling off only hides the window, and toggling
back on shows the same instance exactly where it was.

- Popups are **scoped to a session** and keyed by `name` — `prefix !` in
  two different sessions gives you two independent "scratch" popups.
  Switching sessions hides/restores each session's own popup state.
- One popup per session is visible at a time; showing one hides the others.
- While a popup is visible it owns the keyboard (the prefix still works)
  and the mouse: clicks inside forward to the popup, clicks outside are
  swallowed. The SCROLL chip and wheel scrollback target the popup.
- A popup dies when its shell exits (`exit` / Ctrl-D) — quitting just the
  program inside (e.g. `q` in lazygit) drops you to the popup's shell,
  which keeps the popup alive. The next toggle of that name spawns fresh.

The default binding is `prefix !` → an 80%×80% shell popup named
"scratch". Real configs bind their own:

```toml
# prefix g: lazygit in a big popup, started in the repo
[[binding]]
key    = "g"
action = "popup.toggle"
args   = { name = "git", cmd = "lazygit", cwd = "~/projects/gwam", width = 0.9, height = 0.9 }
label  = "Lazygit"

# ctrl-g from anywhere, no prefix (swallows ctrl-g from inner shells)
[[binding]]
key    = "ctrl-g"
action = "popup.toggle"
args   = { name = "git", cmd = "lazygit" }
direct = true
```

Args (all optional): `name` identifies the popup within a session
(default "default"); `cmd` is written to the popup's shell on first
create (same shell-exec semantics as templates); `cwd` sets its working
directory (`~` expands); `width`/`height` size it as fractions of the
screen (`0.8`) or whole percents (`80`). `cmd`, `cwd`, and the size are
fixed when the popup is first created — later toggles of the same name
reuse the running instance and ignore them.

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
- Matches multi-byte bound keys (arrows, F-keys, alt-chords) against the
  keymap's escape-sequence index — both as direct keystrokes and as
  prefix follow keys.
- Decodes kitty keyboard CSI-u sequences (e.g. `\x1b[99;5u` → `0x03`) back
  to legacy bytes so the inner shell understands them. Bubbletea pushes
  kitty mode at startup, so without this Ctrl-C wouldn't reach a running
  command like `ping`.
- Skips terminal device-report responses (`\x1b[?…` / OSC) that bubbletea's
  capability queries trigger.
- Holds escape sequences split across reads so fragments never leak to
  the pty, and swallows complete-but-unbound sequences that arrive while
  the prefix is armed (an arrow after Ctrl-A cancels instead of typing
  `[A` into the shell).
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
  `collapseLeaf`, `nearestSplit`); `cur*` accessors, the `switchTab` /
  `switchSession` last-flip bookkeeping, and `closePane` (the
  pane → tab → session → quit cascade).
- `layout.go` — pure layout geometry: `Rect`, `layoutGeometry` (the
  rects + dividers walk), `computeRects`, `Tab.geometry` (zoom-aware),
  and the divider junction math (`dividerArms`, `dividerRune`).
- `actions.go` — `Action{ID, Label, Help, Group, Status, Flags, Parse,
  Run}` + the global `actions` registry. `Ctx{M, Args}` is what every
  action's `Run` receives.
- `actions_builtin.go` — one `registerAction` call per built-in action,
  each a thin wrapper around the matching `act*` method.
- `keymap.go` — `Trigger`, `BindingSpec` (raw declaration form),
  `Binding` (resolved form), `Keymap` with byte indexes for single-byte
  keys and sequence indexes for multi-byte ones; `buildKeymap` validates
  args up-front; `applyOverrides` overlays user config on top of
  defaults. `defaultKeymap` is built at init from defaults;
  `applyUserConfig` replaces it with the merged keymap.
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
- `view.go` — styles, `Model.View`, and the tab-bar / body / scrollback /
  overlay renderers (layout math lives in `layout.go`).
- `overlay.go` — `Overlay` interface, `Anchor` impls (center, top-right,
  fractional-y), `Model.overlays` stack, push/pop/remove helpers, and
  `overlayKeyMsg`.
- `overlay_rename.go`, `overlay_picker.go`, `overlay_confirm.go`,
  `overlay_notice.go` — concrete overlay kits. Each is one struct
  implementing `Overlay`, plus a small constructor.
- `popup.go` — session-scoped floating panes: `Popup`, `Session.popups`
  helpers (`visiblePopup`, `focusPane`), `popupRect`/`renderPopup`,
  `actPopupToggle`, and `popup.toggle` arg parsing.
- `input.go` — the stdin pump (`startInputPump`): catches Ctrl-A (legacy
  `0x01` and kitty `\x1b[97;5u`), matches direct/prefix-bound escape
  sequences, decodes kitty CSI-u keystrokes back to legacy bytes, drops
  terminal device-report responses, holds split sequences across reads,
  and forwards everything else to the active pane's pty.
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
- Pane renames aren't supported — `,` renames the tab; pane labels are
  always auto-derived from their fg-process / cwd.
- The cheatsheet has no nested groups (LazyVim-style sub-panels).
- Title parsing assumes well-behaved shells for OSC 7; idle fallback via
  per-platform process lookup covers shells that don't emit it.
