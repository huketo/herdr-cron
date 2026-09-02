---
title: Bubble Tea for a mouse-interactive, cross-platform herdr-cron TUI
date: 2026-09-02
author: research pass for herdr-cron
scope: TUI layer only. gocron (scheduling engine), Herdr (host multiplexer), and the Agent Skill are covered by sibling documents.
---

# Bubble Tea for a mouse-interactive, cross-platform herdr-cron TUI

## Read this first: the API you remember no longer exists

Bubble Tea **v2 is released and stable**. `charm.land/bubbletea/v2` v2.0.9 is the current
release, published 2026-08-19 `[REL]`. Every tutorial, blog post and LLM memory that says
`tea.WithAltScreen()`, `tea.WithMouseCellMotion()`, `tea.MouseMsg{X, Y, Button, Action}`,
`View() string` or `github.com/charmbracelet/bubbletea` is describing **v1**, which is a
different library with a different module path.

The three load-bearing changes for this document:

1. `View()` returns a **`tea.View` struct**, not a `string`. Terminal modes — alt screen,
   mouse mode, focus reporting, window title, cursor — are **declarative fields on that
   struct**, not program options and not commands `[BT tea.go:84-186]`.
2. `tea.MouseMsg` is an **interface**, not a struct. Concrete events are
   `MouseClickMsg`, `MouseReleaseMsg`, `MouseWheelMsg`, `MouseMotionMsg`
   `[BT mouse.go:45-144]`.
3. Lip Gloss v2 ships a **layer compositor with built-in hit testing**
   (`lipgloss.NewLayer`, `NewCompositor`, `comp.Hit(x, y)`) `[LG layer.go:150-303]`, and
   Bubble Tea v2 has a `View.OnMouse` hook designed to consume it
   `[BT tea.go:98-126]`. This changes the answer to "how do I know which widget was
   clicked" — `bubblezone` is no longer the only option.

Everything below is v2 unless explicitly labelled v1.

## Citation tags

| Tag | Source | Pin |
|---|---|---|
| `[BT]` | `charmbracelet/bubbletea` clone at `/tmp/hc-research/bubbletea` | commit `73b6d91ac1c3854dd4af046ab5f9e51d3b3b4290`, tagged `v2.0.9`; module path `charm.land/bubbletea/v2` |
| `[BB]` | `charmbracelet/bubbles` clone at `/tmp/hc-research/bubbles` | commit `0a69b19b0690e9504a511fc231f69cea59ba1cc6` (main, 2026-09-01); latest release `v2.2.1`; module `charm.land/bubbles/v2` |
| `[LG]` | `charmbracelet/lipgloss` clone at `/tmp/hc-research/lipgloss` | commit `868e8b5c4fb6056c294b9fda2fb9d1090052daee` (main, 2026-08-31); latest release `v2.0.6`; module `charm.land/lipgloss/v2` |
| `[BZ]` | `lrstanley/bubblezone` clone at `/tmp/hc-research/bubblezone` | commit `9615dc030c1e0df6d6bcb4c1ac2ba2998fb5bc42` (main); v2 API landed in `v2.0.0` = `82067fab510d8f967837f943aec5848a9c0db463`; module `github.com/lrstanley/bubblezone/v2` |
| `[UV]` | `charmbracelet/ultraviolet` clone at `/tmp/hc-research/ultraviolet` | commit `af8eda3ed7011f486d1b9abbae3cbd51e5866862`; Bubble Tea's input/render substrate |
| `[XA]` | `github.com/charmbracelet/x/ansi@v0.11.7` from the local module cache (`/home/huke/go/pkg/mod/...`) | v0.11.7 |
| `[TT]` | `charmbracelet/x` sparse clone at `/tmp/hc-research/charmx` | commit `a5dee49b28632257cd9a475e8ca36e98a62ff155`; package `exp/teatest/v2` |
| `[REL]` | `gh release view -R <repo> --json tagName,publishedAt` run 2026-09-02 | bubbletea v2.0.9 (2026-08-19), bubbles v2.2.1 (2026-08-24), lipgloss v2.0.6 (2026-08-11) |
| `[RUN]` | Command executed locally on this machine (Linux/WSL2, Windows Terminal host, go1.26.2); the exact command is quoted at the claim |
| `[GH]` | GitHub issue/PR, read via `gh issue view <n> -R charmbracelet/bubbletea` or the search API |
| `[MS]` | Microsoft Learn documentation, URL quoted at the claim |

Version pins verified with `cd /tmp/hc-research/<repo> && git rev-parse HEAD` and
`git tag --points-at HEAD` `[RUN]`.

---

## 1. The core loop

### 1.1 `tea.Model`

```go
// Model contains the program's state as well as its core functions.
type Model interface {
	// Init is the first function that will be called. It returns an optional
	// initial command. To not perform an initial command return nil.
	Init() Cmd

	// Update is called when a message is received. Use it to inspect messages
	// and, in response, update the model and/or send a command.
	Update(Msg) (Model, Cmd)

	// View renders the program's UI, which can be a string or a [Layer]. The
	// view is rendered after every Update.
	View() View
}
```
`[BT tea.go:51-64]`

`Msg` is an alias, not a struct: `type Msg = uv.Event` `[BT tea.go:49]`. `Cmd` is
`type Cmd func() Msg` `[BT tea.go:398]`.

### 1.2 `tea.View` — the declarative surface

```go
type View struct {
	Content                   string
	OnMouse                   func(msg MouseMsg) Cmd
	Cursor                    *Cursor
	BackgroundColor           color.Color
	ForegroundColor           color.Color
	WindowTitle               string
	ProgressBar               *ProgressBar
	AltScreen                 bool
	ReportFocus               bool
	DisableBracketedPasteMode bool
	MouseMode                 MouseMode
	KeyboardEnhancements      KeyboardEnhancements
}
```
Field list quoted from the v2.0.0 release notes `[GH release v2.0.0]`; each field verified
individually against `[BT tea.go:84-186]`. Helpers: `func NewView(s string) View`
`[BT tea.go:76-80]` and `func (v *View) SetContent(s string)` `[BT tea.go:279-281]`.

The renderer diffs consecutive views and only emits mode changes when a field actually
changes `[BT cursed_renderer.go:384-401, 855-866]`. That means toggling `MouseMode` between
frames is cheap and idempotent — no bookkeeping needed in your model.

### 1.3 `tea.Program`

```go
func NewProgram(model Model, opts ...ProgramOption) *Program   // [BT tea.go:603]
func (p *Program) Run() (returnModel Model, returnErr error)   // [BT tea.go:999]
func (p *Program) Send(msg Msg)                                // [BT tea.go:1192]
func (p *Program) Quit()                                       // [BT tea.go:1206]
func (p *Program) Kill()                                       // [BT tea.go:1213]
func (p *Program) Wait()                                       // [BT tea.go:1218]
func (p *Program) ReleaseTerminal() error                      // [BT tea.go:1328]
func (p *Program) RestoreTerminal() error                      // [BT tea.go:1353]
func (p *Program) Println(args ...any)                         // [BT tea.go:1381]
func (p *Program) Printf(template string, args ...any)         // [BT tea.go:1395]
```

Surviving program options — note the absence of every mouse/altscreen option:
`WithContext`, `WithOutput`, `WithInput`, `WithEnvironment`, `WithoutSignalHandler`,
`WithoutCatchPanics`, `WithoutSignals`, `WithoutRenderer`, `WithFilter`, `WithFPS`,
`WithColorProfile`, `WithWindowSize` `[BT options.go:1-168]` — that is the complete file.

### 1.4 Commands

```go
func Batch(cmds ...Cmd) Cmd                                  // concurrent, no ordering  [BT commands.go:15]
func Sequence(cmds ...Cmd) Cmd                               // one at a time, in order  [BT commands.go:25]
func Tick(d time.Duration, fn func(time.Time) Msg) Cmd       // [BT commands.go:157]
func Every(duration time.Duration, fn func(time.Time) Msg) Cmd // clock-aligned  [BT commands.go:101]
func RequestWindowSize() Msg                                 // [BT commands.go:168]
```

`Batch` and `Sequence` both funnel through `compactCmds`, which drops `nil` commands and
returns the single command directly when only one survives `[BT commands.go:35-53]` — so
`tea.Batch(nil, cmd, nil)` costs nothing.

`Tick` and `Every` are **one-shot**. The doc comment is explicit:

```
// Beginners' note: Tick sends a single message and won't automatically
// dispatch messages at an interval. To do that, you'll want to return another
// Tick command after receiving your tick message.
```
`[BT commands.go:125-128]`

`Every` truncates to the system clock (`n.Truncate(duration).Add(duration).Sub(n)`
`[BT commands.go:101-103]`), so the first tick fires at the next wall-clock boundary. For a
cron UI where "next run at 09:00" must line up with the clock, `Every` is the correct
primitive for the countdown display; `Tick` is correct for "poll again 500ms after the last
poll completed".

### 1.5 Polling a background scheduler: `tea.Tick` vs `p.Send()` vs a channel command

Three distinct patterns, all present in the repo. They are not interchangeable.

**(a) `p.Send()` from a plain goroutine.** The goroutine is spawned outside the program and
pushes messages in. Verbatim from the `send-msg` example:

```go
func main() {
	p := tea.NewProgram(newModel())

	// Simulate activity
	go func() {
		for {
			pause := time.Duration(rand.Int63n(899)+100) * time.Millisecond // nolint:gosec
			time.Sleep(pause)

			// Send the Bubble Tea program a message from outside the
			// tea.Program. This will block until it is ready to receive
			// messages.
			p.Send(resultMsg{food: randomFood(), duration: pause})
		}
	}()

	if _, err := p.Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
```
`[BT examples/send-msg/main.go:104-124]`

`Send` is safe after shutdown — it selects on the program context and drops the message
rather than panicking:

```go
func (p *Program) Send(msg Msg) {
	select {
	case <-p.ctx.Done():
	case p.msgs <- msg:
	}
}
```
`[BT tea.go:1192-1197]`

The comment in the example is load-bearing: `p.Send` **blocks** until the event loop is
ready. A scheduler goroutine that fires many events must not hold a lock while calling
`Send`.

**(b) A channel drained by a `Cmd` (the "listen once, re-arm" pattern).** From the
`realtime` example:

```go
// A command that waits for the activity on a channel.
func waitForActivity(sub chan struct{}) tea.Cmd {
	return func() tea.Msg {
		return responseMsg(<-sub)
	}
}
```
…and in `Update`:
```go
	case responseMsg:
		m.responses++                    // record external activity
		return m, waitForActivity(m.sub) // wait for next event
```
`[BT examples/realtime/main.go:33-37, 60-62]`

**(c) `tea.Tick` polling.** Re-arm on every tick, as the doc comment shows
`[BT commands.go:125-148]`.

Consequences for herdr-cron, where the scheduler (gocron) runs in-process alongside the TUI:

- (b) is the right default. The scheduler already knows when a job's state changed; a
  buffered `chan Event` plus a re-arming `waitForEvent` command gives push semantics with
  no polling cost and no reference to `*tea.Program` inside the scheduler. The scheduler
  stays testable without a TUI.
- (a) is needed only if something outside the model tree must inject messages — e.g. a
  file watcher on the job store, or a signal handler. It requires handing `*tea.Program` to
  non-UI code, which couples them; prefer (b).
- (c) is still required for anything the scheduler cannot notify you about: a
  "next run in 00:04:31" countdown, or a relative-time column ("ran 3m ago"). Use
  `tea.Every(time.Second, …)` for those so the display flips exactly on the second.

A practical combination: (b) for state changes, (c) at 1 Hz purely for re-rendering
relative timestamps.

---

## 2. Mouse

### 2.1 Enabling mouse: cell motion vs all motion

There are no program options. You set a field:

```go
func (m model) View() tea.View {
	v := tea.NewView("...")
	v.MouseMode = tea.MouseModeCellMotion
	return v
}
```
`[BT UPGRADE_GUIDE_V2.md:275-282]`

```go
// MouseMode represents the mouse mode of a view.
type MouseMode int

const (
	// MouseModeNone disables mouse events.
	MouseModeNone MouseMode = iota

	// MouseModeCellMotion enables mouse click, release, and wheel events.
	// Mouse movement events are also captured if a mouse button is pressed
	// (i.e., drag events). Cell motion mode is better supported than all
	// motion mode.
	//
	// This will try to enable the mouse in extended mode (SGR), if that is not
	// supported by the terminal it will fall back to normal mode (X10).
	MouseModeCellMotion

	// MouseModeAllMotion enables all mouse events, including click, release,
	// wheel, and movement events. You will receive mouse movement events even
	// when no buttons are pressed.
	//
	// This will try to enable the mouse in extended mode (SGR), if that is not
	// supported by the terminal it will fall back to normal mode (X10).
	MouseModeAllMotion
)
```
`[BT tea.go:283-306]`

The exact difference, from the renderer:

```go
	switch view.MouseMode {
	case MouseModeNone:
		...
	case MouseModeCellMotion:
		...
		_, _ = s.scr.WriteString(ansi.SetModeMouseButtonEvent + ansi.SetModeMouseExtSgr)
	case MouseModeAllMotion:
		...
		_, _ = s.scr.WriteString(ansi.SetModeMouseAnyEvent + ansi.SetModeMouseExtSgr)
	}
```
`[BT cursed_renderer.go:385-401]`

with

```go
	SetModeMouseButtonEvent     = "\x1b[?1002h"
	SetModeMouseAnyEvent        = "\x1b[?1003h"
	SetModeMouseExtSgr          = "\x1b[?1006h"
```
`[XA mode.go:474, 486, 521]`

So: **cell motion = DECSET 1002 + 1006**, **all motion = DECSET 1003 + 1006**.

Verified empirically. Running the `cellbuffer` example (which sets `MouseModeCellMotion`)
under a pty and dumping the byte stream:

```
$ cd /tmp/hc-research/bubbletea/examples && timeout 8 script -qec 'go run ./cellbuffer' /dev/null < <(printf 'q') | od -c
... 033 [ ? 1 0 4 9 h 033 [ ? 2 5 l 033 [ ? 5 W 033 [ ? 2 0 0 4 h
033 [ ? 1 0 0 2 h 033 [ ? 1 0 0 6 h ...
... 033 [ ? 1 0 4 9 l 033 [ ? 2 5 h 033 [ ? 2 0 0 4 l
033 [ ? 1 0 0 2 l 033 [ ? 1 0 0 3 l 033 [ ? 1 0 0 6 l ...
```
`[RUN]`. The `mouse` example (`MouseModeAllMotion`) emits `ESC[?1003h ESC[?1006h` instead,
same command shape `[RUN]`. On exit both 1002 and 1003 are reset unconditionally
`[BT cursed_renderer.go:214-219]`, so a mode change mid-session cannot leak.

**Cost.** 1003 delivers a message for *every cell the pointer crosses*, whether or not a
button is down. Each one runs the full `Update` → `View` → diff cycle. The renderer is
capped (default 60 FPS, max 120 `[BT renderer.go:10-15]`, `[BT options.go:139-146]`), so the
render side is bounded, but `Update` is not: it runs per message on the event loop
goroutine `[BT tea.go:879-880]`. With `View.OnMouse` set, every motion event *also* invokes
your hit-test closure `[BT tea.go:808-816]`.

**Which to use.** `MouseModeCellMotion` for herdr-cron. It gives click, release, wheel and
drag — everything a job list, a scrollable detail pane and a set of buttons need. The only
thing 1003 buys is hover highlighting without a held button. The doc comment states plainly
that "Cell motion mode is better supported than all motion mode" `[BT tea.go:290-293]`.
If hover is wanted later, it is a one-field change and can be gated behind a setting.

### 2.2 The message types and the `Mouse` payload

`MouseMsg` is an interface:

```go
// MouseMsg represents a mouse message. This is a generic mouse message that
// can represent any kind of mouse event.
type MouseMsg interface {
	fmt.Stringer

	// Mouse returns the underlying mouse event.
	Mouse() Mouse
}
```
`[BT mouse.go:44-51]`

The payload — note there is **no `Action` field and no `Alt`/`Ctrl`/`Shift` booleans**:

```go
type Mouse struct {
	X, Y   int
	Button MouseButton
	Mod    KeyMod
}
```
`[BT mouse.go:69-74]`. "The X and Y coordinates are zero-based, with (0,0) being the upper
left corner of the terminal." `[BT mouse.go:53-56]`

Four concrete types, each a distinct Go type over `Mouse`:

```go
type MouseClickMsg Mouse    // [BT mouse.go:78]
type MouseReleaseMsg Mouse  // [BT mouse.go:95]
type MouseWheelMsg Mouse    // [BT mouse.go:112]
type MouseMotionMsg Mouse   // [BT mouse.go:129]
```

Because they are defined *as* `Mouse`, the fields are promoted: `msg.X`, `msg.Y`,
`msg.Button`, `msg.Mod` all work directly on a `MouseClickMsg`. `msg.Mouse()` exists so you
can write one handler over the interface `[BT mouse.go:86-90]`.

**Buttons:**

```go
const (
	MouseNone       = uv.MouseNone
	MouseLeft       = uv.MouseLeft
	MouseMiddle     = uv.MouseMiddle
	MouseRight      = uv.MouseRight
	MouseWheelUp    = uv.MouseWheelUp
	MouseWheelDown  = uv.MouseWheelDown
	MouseWheelLeft  = uv.MouseWheelLeft
	MouseWheelRight = uv.MouseWheelRight
	MouseBackward   = uv.MouseBackward
	MouseForward    = uv.MouseForward
	MouseButton10   = uv.MouseButton10
	MouseButton11   = uv.MouseButton11
)
```
`[BT mouse.go:28-42]`, aliasing `ansi.Mouse*` through ultraviolet `[UV mouse.go:62-74]`.

**Are wheel events buttons or actions? Both, and neither in the v1 sense.** There is no
`MouseAction` enum in v2 at all — the "action" is encoded in the *message type*
(`Click`/`Release`/`Wheel`/`Motion`), and the wheel *direction* is encoded in the `Button`
field (`MouseWheelUp`, `MouseWheelDown`, `MouseWheelLeft`, `MouseWheelRight`). So a scroll
arrives as a `MouseWheelMsg` whose `Button` is `MouseWheelDown`. The canonical handling,
from the real `viewport` component:

```go
	case tea.MouseWheelMsg:
		if !m.MouseWheelEnabled {
			break
		}
		switch msg.Button {
		case tea.MouseWheelDown:
			// NOTE: some terminal emulators don't send the shift event for
			// mouse actions.
			if msg.Mod.Contains(tea.ModShift) {
				m.ScrollRight(m.horizontalStep)
				break
			}
			m.ScrollDown(m.MouseWheelDelta)
		case tea.MouseWheelUp:
			...
			m.ScrollUp(m.MouseWheelDelta)
		case tea.MouseWheelLeft:
			m.ScrollLeft(m.horizontalStep)
		case tea.MouseWheelRight:
			m.ScrollRight(m.horizontalStep)
		}
```
`[BB viewport/viewport.go:696-721]`

**Modifiers** live in `Mod KeyMod`, a bitmask:

```go
type KeyMod int

const (
	ModShift KeyMod = 1 << iota
	ModAlt
	ModCtrl
	ModMeta
	...
	ModHyper
	ModSuper // Windows/Command keys
	...
	ModCapsLock
	ModNumLock
)
```
`[UV key.go:12-31]`, with `Contains` documented as

```
//	m := ModAlt | ModCtrl
//	m.Contains(ModCtrl) // true
//	m.Contains(ModAlt | ModCtrl) // true
//	m.Contains(ModAlt | ModCtrl | ModShift) // false
```
`[UV key.go:39-42]`

The viewport source carries a warning worth internalising: *"some terminal emulators don't
send the shift event for mouse actions"* `[BB viewport/viewport.go:701-702]`. Do not make
shift+click a required interaction.

### 2.3 Hit testing

Bubble Tea hands you absolute terminal cell coordinates. Nothing maps them to widgets. You
have three options; all three are real and in-tree.

#### Option A — manual arithmetic

Track each region's origin and size in the model, update them from `tea.WindowSizeMsg`, and
compare. The `View.OnMouse` doc comment shows the shape of it (string-offset flavour):

```go
//  content := "Hello, World!"
//  v := tea.NewView(content)
//  v.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
//      return func() tea.Msg {
//        m := msg.Mouse()
//        // Check if the mouse is within the bounds of "World!"
//        start := strings.Index(content, "World!")
//        end := start + len("World!")
//        if m.Y == 0 && m.X >= start && m.X < end {
//          // Mouse is over "World!"
//          return MyCustomMsg{
//            MouseMsg: msg,
//          }
//		  }
//      }
//    }
//    return nil
//  }
```
`[BT tea.go:104-125]`

Viable for a fixed two-pane layout with a known chrome height, and it costs zero
dependencies. It becomes a maintenance tax the moment borders, padding, a variable-height
help footer, or scroll offsets enter the picture — every layout tweak is a second edit in
the hit-test arithmetic, and the two drift silently.

#### Option B — Lip Gloss layers + compositor (new in v2, no extra dependency)

Lip Gloss v2 has a real scene graph with a hit test.

```go
type Layer struct {
	id            string
	content       string
	width, height int
	x, y, z       int
	layers        []*Layer
}

func NewLayer(content string, layers ...*Layer) *Layer
func (l *Layer) ID(id string) *Layer
func (l *Layer) X(x int) *Layer
func (l *Layer) Y(y int) *Layer
func (l *Layer) Z(z int) *Layer
func (l *Layer) AddLayers(layers ...*Layer) *Layer
```
`[LG layer.go:11-95]`

```go
// Hit performs a hit test at the given (x, y) coordinates. If a layer is hit,
// it returns the ID of the top-most layer at that point. Layers with empty IDs
// are ignored. If no layer is hit, it returns an empty [LayerHit].
func (c *Compositor) Hit(x, y int) LayerHit
```
`[LG layer.go:277-303]`, returning

```go
type LayerHit struct {
	id     string
	layer  *Layer
	bounds image.Rectangle
}

func (lh LayerHit) Empty() bool
func (lh LayerHit) ID() string
func (lh LayerHit) Layer() *Layer
func (lh LayerHit) Bounds() image.Rectangle
```
`[LG layer.go:155-176]`

`NewCompositor` flattens the tree once, records absolute bounds per layer, sorts by
z-index, and indexes IDs `[LG layer.go:206-260]`. `Hit` walks highest-z first and returns
the first `id != ""` rectangle containing the point `[LG layer.go:280-302]`. Rendering goes
through `Compositor.Render()`, which allocates a canvas of the union bounds
`[LG layer.go:322-327]`.

The complete wiring, verbatim from `examples/clickable`:

```go
	root := lipgloss.NewLayer(bg).ID("bg")
	for i, d := range m.dialogs {
		root.AddLayers(d.view().Z(i + 1))
	}

	comp := lipgloss.NewCompositor(root)

	v.MouseMode = tea.MouseModeAllMotion
	v.AltScreen = true
	v.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
		return func() tea.Msg {
			mouse := msg.Mouse()
			x, y := mouse.X, mouse.Y
			if id := comp.Hit(x, y).ID(); id != "" {
				return LayerHitMsg{
					ID:    id,
					Mouse: msg,
				}
			}
			return nil
		}
	}
	v.SetContent(comp.Render())
```
`[BT examples/clickable/main.go:234-256]`

and the message it defines:

```go
type LayerHitMsg struct {
	ID    string
	Mouse tea.MouseMsg
}
```
`[BT examples/clickable/main.go:16-19]`

`Update` then switches on `LayerHitMsg` and inspects `msg.Mouse.(type)` to distinguish
press/motion/release — the same example implements click-to-spawn, hover highlighting, and
**drag-to-move with z-reordering** on top of this `[BT examples/clickable/main.go:78-197]`.
`go vet ./clickable` passes on the pinned commit `[RUN]`.

Two semantics you must know before relying on `OnMouse`:

1. **It runs against the previous frame.** `cursedRenderer.onMouse` reads
   `s.lastView.OnMouse` `[BT cursed_renderer.go:816-827]` — the closure from the most
   recently *rendered* view, so the compositor it captured describes the layout the user
   was actually looking at. That is the correct behaviour, but it means the closure must
   capture the compositor by value, as the example does.
2. **It is additive and asynchronous.** The event loop calls `onMouse` and dispatches the
   result on a new goroutine, then falls through and delivers the raw mouse message to
   `Update` as normal:

```go
			case MouseMsg:
				switch msg.(type) {
				case MouseClickMsg, MouseReleaseMsg, MouseWheelMsg, MouseMotionMsg:
					// Only send mouse messages to the renderer if they are an
					// actual mouse event.
					if cmd := p.renderer.onMouse(msg); cmd != nil {
						go p.Send(cmd())
					}
				}
```
`[BT tea.go:808-816]`, and a few lines later, unconditionally:
```go
			var cmd Cmd
			model, cmd = model.Update(msg) // run update
```
`[BT tea.go:879-880]`

So for one physical click your `Update` sees the raw `MouseClickMsg` **first** and the
derived `LayerHitMsg` **later**, on a separate goroutine's `Send`. Do not write logic that
assumes they arrive together or in a guaranteed interleaving across two clicks. The
`clickable` example handles this by ignoring raw mouse messages entirely and acting only on
`LayerHitMsg` `[BT examples/clickable/main.go:78-197]` — that is the pattern to copy.

#### Option C — bubblezone

`lrstanley/bubblezone` v2 exists and targets the v2 stack: its `go.mod` requires
`charm.land/bubbletea/v2 v2.0.0` and `charm.land/lipgloss/v2 v2.0.0`
`[BZ go.mod:1-8]`. `go build ./...` succeeds on the pinned commit `[RUN]`. There are no
GitHub releases published for the repo `[REL]`; the v2 API arrived in tag `v2.0.0`
(`82067fa`, commit subject `breaking: bubbletea/lipgloss/bubbles v2 (#51)`) `[RUN]`.

API:

```go
func NewGlobal()                                 // [BZ manager_global.go:19-25]
func Mark(id, v string) string                   // [BZ manager_global.go:98-101]
func Scan(v string) string                       // [BZ manager_global.go:130-133]
func Get(id string) (a *ZoneInfo)                // [BZ manager_global.go:110-113]
func NewPrefix() string                          // [BZ manager_global.go:86-89]
func Clear(id string)                            // [BZ manager_global.go:104-106]
func SetEnabled(v bool)                          // [BZ manager_global.go:38-41]
func Close()                                     // [BZ manager_global.go:27-30]
func New() (m *Manager)                          // non-global manager  [BZ manager.go:39-52]
```

```go
type ZoneInfo struct {
	id        string
	iteration int

	StartX int // StartX is the x coordinate of the top left cell of the zone (with 0 basis).
	StartY int
	EndX   int
	EndY   int
}

func (z *ZoneInfo) InBounds(msg tea.MouseMsg) bool
func (z *ZoneInfo) Pos(msg tea.MouseMsg) (x, y int)
func (z *ZoneInfo) IsZero() bool
```
`[BZ zoneinfo.go:9-65]`. `InBounds` calls `msg.Mouse()` internally and does a plain
rectangle test `[BZ zoneinfo.go:31-52]`; `Pos` returns coordinates relative to the zone or
`(-1, -1)` if outside `[BZ zoneinfo.go:54-65]`.

Usage, verbatim from the package doc comment:

```go
//	func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
//		switch msg := msg.(type) {
//		// [...]
//		case tea.MouseMsg:
//			// [...]
//			for i, item := range m.items {
//				if zone.Get(m.id + item.name).InBounds(msg) {
//					m.active = i
//					break
//				}
//			}
//		}
//		return m, nil
//	}
//
//	func (m model) View() string {
//		return zone.Mark(m.id+"some-other-id", "rendered stuff here")
//	}
```
`[BZ manager.go:111-137]`

**The wrapping constraint.** `Scan` must be called exactly once, on the outermost view:

```
// Scan will scan the view output, searching for zone markers, returning the
// original view output with the zone markers stripped. Scan() should be used
// by the outer most model/component of your application, and not inside of a
// model/component child.
//
// Scan buffers the zone info to be stored, so an immediate call to Get(id) may
// not return the correct information. Thus it's recommended to primarily use
// Get(id) for actions like mouse events, which don't occur immediately after a
// view shift (where the previously stored zone info might be different).
```
`[BZ manager.go:207-224]`

Two real consequences: (1) if you forget `Scan`, the marker sequences ship to the terminal
and no zone is ever registered; (2) zone info is written through a channel consumed by a
background goroutine (`m.setChan`, `go m.zoneWorker()` `[BZ manager.go:44-51, 190-206]`),
so `Get` right after `Scan` may see stale bounds. Zones from previous iterations are
garbage-collected by comparing an `iteration` stamp `[BZ manager.go:194-203]`.

**Correction to a common belief about the marker.** bubblezone does *not* use a private-use
Unicode codepoint. It emits an **ANSI CSI sequence with a private final byte**:

```go
const (
	// Have to use ansi escape codes to ensure lipgloss doesn't consider ID's as
	// part of the width of the view.
	identStart   = '\x1B' // ANSI escape code.
	identBracket = '['
	// The escape terminator.
	//
	// Refs:
	//  - https://en.wikipedia.org/wiki/ANSI_escape_code#CSI_(Control_Sequence_Introducer)_sequences
	//    > A subset of arrangements was declared "private" so that terminal manufacturers could insert
	//    > their own sequences without conflicting with the standard. Sequences containing the parameter
	//    > bytes <=>? or the final bytes 0x70-0x7E (p-z{|}~) are private.
	identEnd = 'z'
)
```
`[BZ manager.go:16-27]`

and the marker itself is `ESC [ <counter> z`, wrapped around the content on both sides:

```go
	gid = string(identStart) + string(identBracket) + strconv.FormatInt(atomic.AddInt64(&markerCounter, 1), 10) + string(identEnd)
	...
	return gid + v + gid
```
`[BZ manager.go:159-166]`

The reason for choosing an escape sequence rather than a printable rune is stated in the
comment above: Lip Gloss's width functions ignore ANSI sequences, so markers do not corrupt
width math `[BZ manager.go:17-19]`. The scanner tracks column position with its own
ANSI-aware `printableRuneWidth`, which uses `runewidth.RuneWidth` per rune
`[BZ scanner.go:155-175]`.

That last detail is a real difference from Lip Gloss: bubblezone measures with
`mattn/go-runewidth` per *rune*, whereas Lip Gloss v2 measures with `ansi.StringWidth`,
which iterates **grapheme clusters** `[XA width.go:65-67, 78-104]`. For Korean text the two
agree (Hangul syllables are single wide runes); for emoji with ZWJ sequences or combining
marks they can disagree, which would offset a zone's `StartX`.

#### Which one for herdr-cron

Lip Gloss layers (Option B). Rationale, in order of weight:

- Zero new dependency. Lip Gloss is already required for styling; bubblezone would be a
  fourth third-party module with a single maintainer and no published releases.
- One source of truth. With `Compositor`, the thing that *renders* the pane is the same
  object that *hit-tests* it. With bubblezone, rendering and zone registration are the same
  string but bounds live in a separate async map. Option B cannot drift.
- Correct z-order for overlays. herdr-cron will want a confirm dialog ("delete this
  job?") and a cron-expression helper popup. `Hit` walks highest-z first
  `[LG layer.go:280-302]`, so an overlay naturally swallows clicks meant for the list
  underneath. Doing that with zones means manually suppressing every zone behind the modal.
- It is the pattern Charm itself demonstrates, in-tree, at the pinned commit
  `[BT examples/clickable/main.go]`.

Cost accepted: `NewCompositor` re-flattens the whole tree on every construction
`[LG layer.go:206-215]`, and `Render()` allocates a fresh canvas each frame
`[LG layer.go:322-327]`. `View()` runs after every `Update` `[BT tea.go:879-887]`, so with
`MouseModeAllMotion` that is a flatten+canvas per pointer cell. With `MouseModeCellMotion`
and a layer count in the tens, this is not a concern.

Where bubblezone would still win: marking *many small dynamic regions inside already-styled
strings* — e.g. making each of 200 table rows individually clickable without computing 200
layer rectangles. If a row-level hit test proves awkward with layers, the fallback is not
bubblezone but arithmetic: one layer for the table body, then
`rowIndex = mouse.Y - hit.Bounds().Min.Y + scrollOffset`. That is three lines and uses
`LayerHit.Bounds()` `[LG layer.go:174-176]`, which is exactly what it is for.

### 2.4 The text-selection tradeoff

**Confirmed, and it is not a Bubble Tea bug.** Enabling mouse reporting takes click-drag
away from the terminal, so the terminal's native selection-and-copy stops working.

Issue #162, *"Allow both native text selection and mouse wheel scrolling"*, opened
2021-11-25, **still open** as of this read `[GH #162,
https://github.com/charmbracelet/bubbletea/issues/162]`. The maintainer's answer:

> Hi! Unfortunately, when the mouse is enabled in a terminal native text selection is no
> longer possible. It’s a limitation of pretty much all terminals.
>
> The plan is to implement text selection in Bubble Tea to overcome this limitation
> (similar to how it’s done in tmux) however it will take some time to implement it as it’s
> a rather large endeavor.

— meowgorithm, 2021-11-25 `[GH #162]`

That plan did **not** land in v2: the issue is still open, and a later comment notes the
selection support that shipped in Charm's own Crush was
"very customized for the specific component in question within Crush, and I don't think
that implementation can necessarily be ported to bubbletea" — lrstanley, 2025-08-13
`[GH #162]`.

The same thread contains the mitigation, expressed in v2 terms (t3snake, 2026-08-25)
`[GH #162]`:

```go
case tea.MouseClickMsg:
	c.is_selecting = true    // c is the model with boolean flag

case tea.MouseReleaseMsg:
	c.is_selecting = false
```
```go
if c.is_selecting {
	v.MouseMode = tea.MouseModeNone
} else {
	v.MouseMode = tea.MouseModeCellMotion
}
```
with the author's own caveat: *"In my opinion such a weird UX cant be a default in the
framework itself. But it should be documented somewhere for users."* `[GH #162]`

**Note the v1 API named in the task description is gone.** There is no
`tea.DisableMouse` / `tea.EnableMouseCellMotion` command in v2 — both are listed under
"Removed Commands", replaced by `view.MouseMode = tea.MouseModeNone` /
`tea.MouseModeCellMotion` `[BT UPGRADE_GUIDE_V2.md:300-312]`. The mitigation is therefore
*simpler* in v2 than in v1: one boolean in the model, one branch in `View()`, and the
renderer emits the reset/set sequences only on the transition
`[BT cursed_renderer.go:384-401]`.

The recommended mitigation for herdr-cron is the **explicit toggle**, not the
press-hold heuristic:

- Bind a key (`m`) that flips a `mouseEnabled bool`, and render the state in the help
  footer, e.g. `mouse: on (m to disable for copy)`.
- Reason to prefer it over the press-hold trick: the press-hold version consumes the first
  click of a drag to *decide* it is a drag, so the click never reaches the widget — every
  interaction becomes ambiguous, and (as the thread notes) scrolling clears the selection
  anyway.
- Also mind the alt screen: in the alternate buffer there is no scrollback to select from
  and copied output vanishes on exit (see §6). A user who wants to copy a job's error
  output is better served by an explicit "copy to clipboard" action. Bubble Tea v2 has
  first-class clipboard commands — `tea.SetClipboard` is used in the v2.0.0 release notes'
  key-handling example `[GH release v2.0.0]` and `clipboard.go` exists in-tree
  `[BT clipboard.go]`.

### 2.5 Mouse support in `bubbles` components

Read directly, not from documentation. Across the whole `bubbles` module at the pinned
commit, exactly **one** non-test file references any mouse message:

```
$ cd /tmp/hc-research/bubbles && grep -rln "MouseMsg\|MouseWheelMsg\|MouseClickMsg" --include=*.go . | grep -v _test
./viewport/viewport.go
```
`[RUN]`, `[BB]`

| Component | Handles mouse? | Evidence |
|---|---|---|
| `viewport` | **Yes — wheel only.** Vertical and horizontal scroll, gated on `MouseWheelEnabled bool` (default `true`) with `MouseWheelDelta int` (default `3`). | `[BB viewport/viewport.go:66-69, 150-151, 696-721]` |
| `table` | **No.** `Update` handles `tea.KeyPressMsg` and nothing else. No mouse row selection, no wheel scroll — and it does **not** forward messages to its internal viewport either (`viewport.Update` is never called; the viewport is used purely as a render/scroll buffer via `UpdateViewport`). | `[BB table/table.go:201-230]`, `[BB table/table.go]` method list contains no `viewport.Update` call |
| `list` | **No.** `Update` switches on `KeyPressMsg`, `FilterMatchesMsg`, `spinner.TickMsg`, `statusMessageTimeoutMsg`, then delegates to `handleFiltering`/`handleBrowsing`, both of which only match `tea.KeyPressMsg`. Paging is via `Paginator`, not a viewport, so there is no wheel path at all. | `[BB list/list.go:818-908]` |
| `textinput` | No. | no mouse reference `[RUN]` |
| `spinner`, `help`, `key`, `paginator`, `progress`, `timer`, `stopwatch`, `textarea`, `tree`, `filepicker`, `cursor` | No. | no mouse reference `[RUN]` |

**This is the single most important planning fact in this document.** The obvious
implementation — "use `bubbles/table` for the job list" — gives you a table that cannot be
clicked and cannot be scrolled with the wheel. `table.Update` will ignore your
`MouseClickMsg` entirely.

The fix is mechanical, because `table` exposes the cursor imperatively:

```go
func (m Model) Cursor() int          // [BB table/table.go:345-347]
func (m *Model) SetCursor(n int)     // [BB table/table.go:350-353]
func (m *Model) MoveUp(n int)        // [BB table/table.go:357-372]
func (m *Model) MoveDown(n int)      // [BB table/table.go:375-390]
func (m Model) SelectedRow() Row     // [BB table/table.go:287-293]
func (m Model) Height() int          // [BB table/table.go:335-337]
```

So the parent model owns the mouse: on a `MouseClickMsg` inside the table's layer, compute
the row from `mouse.Y - bounds.Min.Y` (minus the one header line —
`View()` is `m.headersView() + "\n" + m.viewport.View()` `[BB table/table.go:251-253]`) and
call `SetCursor`. On a `MouseWheelMsg`, call `MoveUp(3)`/`MoveDown(3)`. Roughly fifteen
lines, and it lives in your code, not a fork.

---

## 3. Windows

The user requires Windows/macOS/Linux. This section is the answer to "will the mouse work
in Windows Terminal".

### 3.1 What Bubble Tea does to the Windows console

```go
//go:build windows

func (p *Program) initInput() (err error) {
	// Save stdin state and enable VT input
	if f, ok := p.input.(term.File); ok && term.IsTerminal(f.Fd()) {
		p.ttyInput = f
		p.previousTtyInputState, err = term.MakeRaw(p.ttyInput.Fd())
		...
		// Enable VT input
		var mode uint32
		if err := windows.GetConsoleMode(windows.Handle(p.ttyInput.Fd()), &mode); err != nil { ... }

		if err := windows.SetConsoleMode(windows.Handle(p.ttyInput.Fd()), mode|windows.ENABLE_VIRTUAL_TERMINAL_INPUT); err != nil { ... }
	}

	// Save output screen buffer state and enable VT processing.
	if f, ok := p.output.(term.File); ok && term.IsTerminal(f.Fd()) {
		...
		if err := windows.SetConsoleMode(windows.Handle(p.ttyOutput.Fd()),
			mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|
				windows.DISABLE_NEWLINE_AUTO_RETURN); err != nil { ... }
		...
	}
	return
}

const suspendSupported = false

func suspendProcess() {}
```
`[BT tty_windows.go:1-64]` — that is the entire file.

So: **`ENABLE_VIRTUAL_TERMINAL_PROCESSING` on the output handle**,
**`ENABLE_VIRTUAL_TERMINAL_INPUT` on the input handle**, plus raw mode via
`charmbracelet/x/term` (`term.MakeRaw`). `ENABLE_MOUSE_INPUT` is **not** set here.
Dependencies confirmed in `go.mod`: `github.com/charmbracelet/x/term v0.2.2`,
`github.com/charmbracelet/x/windows v0.2.2`, `golang.org/x/sys v0.46.0` `[BT go.mod]`.
There is no `windows-ansi` dependency in v2.

### 3.2 Where the input actually comes from

Bubble Tea does not read the console itself. It builds an ultraviolet reader:

```go
	p.cancelReader, err = uv.NewCancelReader(p.input)
	...
	drv := uv.NewTerminalReader(p.cancelReader, term)
	drv.SetLogger(p.logger)
	p.inputScanner = drv
```
`[BT tty.go:68-78]`

On Windows, `uv.NewCancelReader` sets:

```go
	modes := []uint32{
		windows.ENABLE_VIRTUAL_TERMINAL_INPUT,
		windows.ENABLE_WINDOW_INPUT,
		windows.ENABLE_EXTENDED_FLAGS,
	}
```
`[UV cancelreader_windows.go:51-56]`

and reads through `ReadConsoleInput`-style record polling, converting records to VT bytes:

```go
		// We convert Windows Input Records to VT input sequences for easier
		// processing especially when dealing with UTF-16 decoding and
		// Win32-Input-Mode processing.
		d.serializeWin32InputRecords(records, &buf)
```
`[UV terminal_reader_windows.go:59-62]`

`serializeWin32InputRecords` has a `MOUSE_EVENT` branch that **synthesises SGR mouse
sequences from Win32 console records**:

```go
		case xwindows.MOUSE_EVENT:
			if d.MouseMode == nil || *d.MouseMode == 0 {
				continue
			}
			...
			// Encode mouse events as SGR mouse sequences that can be read by [EventDecoder].
			buf.WriteString(ansi.MouseSgr(
				ansi.EncodeMouseButton(button, isMotion, shift, alt, ctrl),
				int(mevent.MousePositon.X), int(mevent.MousePositon.Y), isRelease,
			))
```
`[UV terminal_reader_windows.go:125-172]`

**But Bubble Tea never sets `TerminalReader.MouseMode`.** The only references to
`inputScanner` in the entire repo are the field declaration, the assignment, and
`StreamEvents`:

```
$ cd /tmp/hc-research/bubbletea && grep -rn "inputScanner\|NewTerminalReader" --include=*.go .
./tty.go:74:	drv := uv.NewTerminalReader(p.cancelReader, term)
./tty.go:76:	p.inputScanner = drv
./tty.go:87:	if err := p.inputScanner.StreamEvents(p.ctx, p.msgs); err != nil {
./tea.go:536:	inputScanner          *uv.TerminalReader
```
`[RUN]`, `[BT tty.go, tea.go:536]`

`MouseMode` is a `*MouseMode` on the reader `[UV terminal_reader.go:35-39]`, so it stays
`nil`, so that `continue` fires and the record-synthesis path is **inert** under Bubble Tea
v2. Windows mouse input therefore depends on the same mechanism as Unix: Bubble Tea writes
DECSET 1002/1003 + 1006 to the output (§2.1), and the console/terminal delivers SGR mouse
sequences into the input stream, which the VT decoder parses. `ENABLE_VIRTUAL_TERMINAL_INPUT`
is what makes that translation happen — ultraviolet's own comment on the other Windows
reader path says so:

```go
	// Enabling virtual terminal input is necessary for processing certain
	// types of input like X10 mouse events and arrows keys with the current
	// bytes-based input reader.
	newMode |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
```
`[UV poll_windows.go:199-202]`

Microsoft documents `ENABLE_VIRTUAL_TERMINAL_INPUT` as causing user input to be converted
into VT sequences available through the console input stream, and documents mouse-mode
DECSET sequences (1000/1002/1003/1006) as supported console VT input sequences
(`https://learn.microsoft.com/en-us/windows/console/console-virtual-terminal-sequences`,
`https://learn.microsoft.com/en-us/windows/console/setconsolemode`) `[MS]`.

### 3.3 What is required, and what is known to have been broken

**Required:** nothing from the application. Bubble Tea v2 sets both console modes itself
`[BT tty_windows.go]`, and SGR encoding (1006) is always requested alongside the tracking
mode `[BT cursed_renderer.go:392-401]`, which removes the legacy coordinate ceiling.

**Known-broken history, from the tracker:**

- **#359, "bug: windows missing mouse events past column 95"** (opened 2022-07-01, closed
  2022-09-01) `[GH #359, https://github.com/charmbracelet/bubbletea/issues/359]`. Filed by
  bubblezone's author. Root cause quoted in the issue from the Windows Terminal tracker:
  *"The legacy, byte-based protocol only allows row and column numbers up to 223, because
  32 is added to this number and this is sent as a single byte… beginning at column 95 the
  generated data is not 7-bit clean and not valid UTF-8"*. This is exactly the failure that
  SGR (1006) fixes, and v2 always enables 1006 — verified in the byte dump in §2.1
  `[RUN]`. **Do not ship a build that falls back to legacy encoding**; the `MouseMode`
  doc comment warns that fallback to X10 happens if SGR is unsupported
  `[BT tea.go:294-297]`, and on such a terminal wide windows will silently lose clicks
  past column 95.
- **#1391, "Mouse does not work in AltScreen on Windows terminal"** (opened 2025-04-15,
  **closed** 2025-05-15) `[GH #1391]`. Reporter: Windows 11, PowerShell, Windows Terminal.
  `examples/mouse` worked; adding `tea.WithAltScreen()` (v1 API) killed all mouse events;
  also reproduced under WSL in Windows Terminal. Closed as completed. **This is the exact
  combination herdr-cron will ship** (alt screen + mouse + Windows Terminal), so it belongs
  on the manual test checklist even though it is closed.
- **#1313, "[Windows] Can't select text on the terminal when inside a program"** (opened
  2025-02-05, closed 2025-02-25) `[GH #1313]`. Windows Terminal under PowerShell/CMD lost
  click-drag selection *in a program that never asked for the mouse*. Cause and fix are
  stated in the PR that closed it, #1340 *"fix: windows: enable mouse mode on demand"*:
  > Since we added native Windows API input events support, we always enable mouse events
  > even if the user doesn't want them. This is a problem because it breaks applications
  > that don't expect mouse events and selection stops working without any modifier key.
  > See: https://learn.microsoft.com/en-us/windows/terminal/selection

  `[GH #1340, merged 2025-02-25]`; the v2 counterpart is #1341 *"(v2) fix: windows: enable
  mouse mode on demand"*, merged 2025-03-11 `[GH #1341]`. That merge is why `MouseMode` on
  the ultraviolet reader is opt-in and therefore `nil` today (§3.2) — the fix is load-bearing,
  not vestigial.
- **Windows Terminal's own selection modifier.** Microsoft documents that when an
  application has mouse mode enabled, holding a modifier key restores manual selection
  (`https://learn.microsoft.com/en-us/windows/terminal/selection`, cited by #1340) `[MS]`.
  This is a genuinely better mitigation than a keybinding **on Windows Terminal
  specifically**; it does not exist on conhost or on most Unix terminals, so it cannot be
  the only answer.

**Open mouse-adjacent issues** at time of reading (`gh search issues --repo
charmbracelet/bubbletea mouse --state open`) `[RUN]`:

| Issue | Opened | Title |
|---|---|---|
| #162 | 2021-11-25 | Allow both native text selection and mouse wheel scrolling |
| #169 | 2021-12-12 | Drag and drop |
| #309 | 2022-05-08 | Compositing |
| #1424 | 2025-05-25 | `tea.ExecProcess` does not restore mouse motion |
| #1455 | 2025-07-26 | `EnterAltScreen` doesn't create true alternate screen in Terminal.app |
| #1481 | 2025-08-28 | Significant Performance Degradation in v2 (2x slower at-least) |
| #1571 | 2026-01-20 | v2: Scrollback lost on `tea.Quit` |

None of these is Windows-mouse-specific. **#1424 is directly relevant**: *"Run vim with
`tea.ExecProcess`, exit vim -> no mouse events, also some problems with altscreen"*,
reported on macOS/Terminal.app, still open `[GH #1424]`. If herdr-cron ever shells out
(open `$EDITOR` on a job definition), mouse and alt screen may not come back. Mitigation
available: v2 recomputes modes from the declarative `View` on the next render
`[BT cursed_renderer.go:384-401]`, so forcing a re-render after the exec, or calling
`ReleaseTerminal`/`RestoreTerminal` `[BT tea.go:1328-1360]`, is the lever to test.

### 3.4 Resize on Windows

`SIGWINCH` does not exist on Windows, and Bubble Tea says so:

```go
//go:build windows

// listenForResize is not available on windows because windows does not
// implement syscall.SIGWINCH.
func (p *Program) listenForResize(done chan struct{}) {
	close(done)
}
```
`[BT signals_windows.go:1-11]` — the whole file.

Resize still works, because ultraviolet turns the console's
`WINDOW_BUFFER_SIZE_EVENT` record into a VT window-op sequence that the decoder understands:

```go
		case xwindows.WINDOW_BUFFER_SIZE_EVENT:
			wevent := record.WindowBufferSizeEvent()
			if wevent.Size.X != d.lastWinsizeX || wevent.Size.Y != d.lastWinsizeY {
				d.lastWinsizeX, d.lastWinsizeY = wevent.Size.X, wevent.Size.Y
				// We encode window resize events as CSI 4 ; height ; width t
				// sequence which the [EventDecoder] understands.
				buf.WriteString(
					ansi.WindowOp(
						8,                  // Terminal window size in cells
						int(wevent.Size.Y), // height
						int(wevent.Size.X), // width
					),
				)
			}
```
`[UV terminal_reader_windows.go:174-187]`. (The code comment says `CSI 4`; the actual
argument is `8`. Trust the code.) `ENABLE_WINDOW_INPUT` — which
`uv.NewCancelReader` sets `[UV cancelreader_windows.go:53]` — is what makes those records
appear. On Unix the same information arrives via `SIGWINCH` →
`Program.checkResize()` → `term.GetSize` → `p.Send(WindowSizeMsg{...})`
`[BT signals_unix.go:15-32]`, `[BT tty.go:107-127]`.

Net: `tea.WindowSizeMsg` is delivered on all three platforms; do not special-case.

### 3.5 Windows summary

- Mouse **should** work in Windows Terminal and modern conhost with no application-side
  configuration, over SGR-encoded VT sequences, because Bubble Tea enables
  `ENABLE_VIRTUAL_TERMINAL_INPUT` + `ENABLE_VIRTUAL_TERMINAL_PROCESSING`
  `[BT tty_windows.go]` and always requests 1006 `[BT cursed_renderer.go:392-401]`.
- Mouse reporting **will** break native click-drag selection on Windows, as it does
  everywhere; on Windows Terminal a modifier key restores it `[GH #1340]`, `[MS]`.
- Alt screen + mouse + Windows Terminal is a combination with a real historical bug
  (#1391, closed) `[GH #1391]`. Manual verification on Windows is not optional.
- No `ENABLE_MOUSE_INPUT`, no `windows-ansi`, no console-record mouse path is in play under
  v2 (§3.2).

---

## 4. Components to reuse from `bubbles`

Constructors, read from source at the pinned commit. Note the v2 shift toward variadic
`Option` for several components.

```go
// charm.land/bubbles/v2/table
func New(opts ...Option) Model                       // [BB table/table.go:133]
func WithColumns(cols []Column) Option               // [BB table/table.go:147]
func WithRows(rows []Row) Option                     // [BB table/table.go:154]
func WithHeight(h int) Option                        // [BB table/table.go:161]
func WithWidth(w int) Option                         // [BB table/table.go:168]
func WithFocused(f bool) Option                      // [BB table/table.go:175]
func WithStyles(s Styles) Option                     // [BB table/table.go:182]
func WithKeyMap(km KeyMap) Option                    // [BB table/table.go:189]

type Row []string                                    // [BB table/table.go:33]
type Column struct { Title string; Width int }       // [BB table/table.go:35-38]

// charm.land/bubbles/v2/list
func New(items []Item, delegate ItemDelegate, width, height int) Model  // [BB list/list.go:207]

// charm.land/bubbles/v2/viewport
func New(opts ...Option) (m Model)                   // [BB viewport/viewport.go:43]
func WithWidth(w int) Option                         // [BB viewport/viewport.go:27]
func WithHeight(h int) Option                        // [BB viewport/viewport.go:35]

// charm.land/bubbles/v2/textinput
func New() Model                                     // [BB textinput/textinput.go:157]

// charm.land/bubbles/v2/spinner
func New(opts ...Option) Model                       // [BB spinner/spinner.go:110]

// charm.land/bubbles/v2/help
func New() Model                                     // [BB help/help.go:93]
func (m Model) View(k KeyMap) string                 // [BB help/help.go:107-112]
func (m *Model) SetWidth(w int)                      // [BB help/help.go:115-117]

// charm.land/bubbles/v2/key
func NewBinding(opts ...BindingOpt) Binding          // [BB key/key.go:54]
func WithKeys(keys ...string) BindingOpt             // [BB key/key.go:64]
func WithHelp(key, desc string) BindingOpt           // [BB key/key.go:71]
func WithDisabled() BindingOpt                       // [BB key/key.go:78]
```

### 4.1 `table` in detail — read closely, as requested

```go
type Model struct {
	KeyMap KeyMap
	Help   help.Model

	cols   []Column
	rows   []Row
	cursor int
	focus  bool
	styles Styles

	viewport viewport.Model
	start    int
	end      int
}
```
`[BB table/table.go:15-29]`

```go
// Update is the Bubble Tea update loop.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.focus {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.KeyMap.LineUp):
			m.MoveUp(1)
		case key.Matches(msg, m.KeyMap.LineDown):
			m.MoveDown(1)
		case key.Matches(msg, m.KeyMap.PageUp):
			m.MoveUp(m.viewport.Height())
		case key.Matches(msg, m.KeyMap.PageDown):
			m.MoveDown(m.viewport.Height())
		...
		}
	}

	return m, nil
}
```
`[BB table/table.go:201-230]`

**Answers, explicitly:**

- **Mouse row selection out of the box?** No. `MouseClickMsg` matches nothing.
- **Mouse scrolling out of the box?** No. There is an internal `viewport.Model`, but
  `Update` never forwards to it — `viewport.Update` is not called anywhere in
  `table/table.go` `[RUN]`. The viewport is driven only through `UpdateViewport()`, which
  re-renders the row window around the cursor `[BB table/table.go:264-284]`. So the
  viewport's own wheel handling is unreachable.
- **Does it respect focus?** Yes, and it hard-returns when unfocused
  `[BB table/table.go:203-205]`. In a two-pane layout, `Focus()`/`Blur()`
  `[BB table/table.go:239-248]` must be driven by your click handler, or clicking the
  detail pane will leave the table still eating arrow keys.
- **Rendering shape**, needed for hit-test arithmetic:
  `func (m Model) View() string { return m.headersView() + "\n" + m.viewport.View() }`
  `[BB table/table.go:251-253]` — exactly one header row above the scrolling body. And
  `SetHeight` subtracts the header itself: `m.viewport.SetHeight(h - lipgloss.Height(m.headersView()))`
  `[BB table/table.go:329-332]`, so pass the **total** height including the header.
- Columns with `Width <= 0` are skipped in both the header and row renderers
  `[BB table/table.go:418-435]`, which is a clean way to hide a column responsively at
  narrow widths.

### 4.2 Fit for a job-list / job-detail / run-history layout

| Job | Component | Verdict |
|---|---|---|
| Job list (name, schedule, next run, last status) | `table` | **Yes.** Columnar data is what it is for; `Row` is `[]string` and `SelectedRow()` gives the row back `[BB table/table.go:287-293]`. Costs you ~15 lines of mouse glue (§2.5). Fixed `Column.Width` means you resize columns yourself on `WindowSizeMsg`. |
| Job list, alternative | `list` | **No.** `list` is built for prose-ish items with a filter and pagination, has a hardcoded dark-only default style at this commit (`// XXX: Let the user choose between light and dark colors. We've temporarily hardcoded the dark colors here.` `[BB list/list.go:207-210]`), and gives you *no* mouse and *no* wheel. Its one advantage is the built-in fuzzy filter (`Filter: DefaultFilter` at `[BB list/list.go:236]`, `FilterInput` at `[BB list/list.go:239]`, built from `textinput.New()` at `[BB list/list.go:213-218]`). If filtering matters more than columns, reconsider; otherwise `table`. |
| Job detail (cron expr, command, env, owner, description) | `viewport` | **Yes**, and it is the only component with working wheel scroll `[BB viewport/viewport.go:696-721]`. Content is a plain styled string you build with Lip Gloss. |
| Run history (timestamp, duration, exit code, output tail) | `table` for the index + `viewport` for the selected run's output | **Yes.** Same mouse glue as the job list. Output can be long; `viewport` handles it and has highlight/search machinery in v2 (`m.highlights`, `findNearestMatch` `[BB viewport/viewport.go:640-653]`). |
| Filter / rename / cron expression entry | `textinput` | **Yes.** Keyboard-only, which is fine — clicking the field just focuses it, and you own that click. |
| "Running now" indicator | `spinner` | **Yes.** Re-arm with `m.spinner.Tick` in `Init`/`Update`, as in `[BT examples/realtime/main.go:47-51, 63-66]`. |
| Footer key hints | `help` + `key` | **Yes.** `help.Model.View(KeyMap)` where your `KeyMap` implements `ShortHelp() []key.Binding` / `FullHelp() [][]key.Binding` — `table.KeyMap` is a worked example `[BB table/table.go:52-63]`. `key.Binding.Enabled()` returns false for disabled or empty bindings and such bindings "won't show up in help" `[BB key/key.go:103-108]`, which is exactly how to hide "Run now" while a job is already running. |

---

## 5. Layout with Lip Gloss

### 5.1 Primitives

```go
func JoinHorizontal(pos Position, strs ...string) string  // [LG join.go:28]
func JoinVertical(pos Position, strs ...string) string    // [LG join.go:116]

func Place(width, height int, hPos, vPos Position, str string, opts ...WhitespaceOption) string  // [LG position.go:36]
func PlaceHorizontal(width int, pos Position, str string, opts ...WhitespaceOption) string       // [LG position.go:43]
func PlaceVertical(height int, pos Position, str string, opts ...WhitespaceOption) string        // [LG position.go:90]
```

```go
type Position float64

// Position aliases.
const (
	Top    Position = 0.0
	Bottom Position = 1.0
	Center Position = 0.5
	Left   Position = 0.0
	Right  Position = 1.0
)
```
`[LG position.go:19-32]`. `Position` is a float in `[0,1]`, clamped `[LG position.go:21-23]`
— so `JoinHorizontal(0.3, a, b)` is legal, not just the named constants. `Place` is just
`PlaceVertical(PlaceHorizontal(...))` `[LG position.go:36-38]`.

Borders: `func (s Style) Border(b Border, sides ...bool) Style`, plus
`BorderStyle`, `BorderTop/Right/Bottom/Left`, and per-side foreground/background
`[RUN: go doc charm.land/lipgloss/v2 Style]`. Built-in border sets: `NormalBorder`,
`RoundedBorder`, `BlockBorder`, `ThickBorder`, `DoubleBorder`, `HiddenBorder`
`[LG borders.go:223-262]`.

### 5.2 Responsive two-pane reaction to `tea.WindowSizeMsg`

```go
// WindowSizeMsg is used to report the terminal size. It's sent to Update once
// initially and then on every terminal resize.
type WindowSizeMsg struct {
	Width  int
	Height int
}
```
`[BT screen.go:5-10]`

The shape that works (composed from the `clickable` example's use of
`m.width/m.height` `[BT examples/clickable/main.go:80-81, 210-256]` and the component
setters cited above):

```
Update:
  case tea.WindowSizeMsg:
      m.width, m.height = msg.Width, msg.Height
      chromeH := lipgloss.Height(m.headerView()) + lipgloss.Height(m.footerView())
      bodyH   := m.height - chromeH
      listW   := clamp(m.width*40/100, 28, 60)
      detailW := m.width - listW
      m.jobs.SetWidth(listW);   m.jobs.SetHeight(bodyH)
      m.detail.SetWidth(detailW - 2 /* border */); m.detail.SetHeight(bodyH - 2)
      m.help.SetWidth(m.width)
```

Measure chrome with `lipgloss.Height`, never a hardcoded constant — `Height` is
`strings.Count(str, "\n") + 1` `[LG size.go:29-31]`, so it is exact and cheap. Below a
threshold width, collapse to a single pane (list *or* detail) rather than squeezing both;
`table` hides columns whose `Width <= 0` `[BB table/table.go:418-435]`, which makes column
shedding trivial.

Then compose panes as layers so the same structure answers both "what does it look like"
and "what did the user click" (§2.3):

```
list layer   -> .ID("pane.jobs").X(0).Y(headerH)
detail layer -> .ID("pane.detail").X(listW).Y(headerH)
footer layer -> .ID("pane.help").X(0).Y(m.height-footerH)
```

`Layer.AddLayers` recomputes the layer's own width/height from the union of its children
`[LG layer.go:85-96]`, and `Compositor` computes absolute bounds during flatten
`[LG layer.go:229-259]`, so nested children (a button inside the detail pane) get correct
absolute hit rectangles for free — demonstrated by the button-inside-dialog case
`[BT examples/clickable/main.go:319-339]`.

### 5.3 The width pitfall with wide/CJK runes — this matters here

```go
// Width returns the cell width of characters in the string. ANSI sequences are
// ignored and characters wider than one cell (such as Chinese characters and
// emojis) are appropriately measured.
//
// You should use this instead of len(string) or len([]rune(string) as neither
// will give you accurate results.
func Width(str string) (width int) {
	for l := range strings.SplitSeq(str, "\n") {
		w := ansi.StringWidth(l)
		if w > width {
			width = w
		}
	}

	return width
}
```
`[LG size.go:9-24]`, confirmed by `go doc charm.land/lipgloss/v2 Width` `[RUN]`.

Underneath, `ansi.StringWidth` walks **grapheme clusters** and sums their display widths,
skipping escape sequences:

```go
// StringWidth returns the width of a string in cells. This is the number of
// cells that the string will occupy when printed in a terminal. ANSI escape
// codes are ignored and wide characters (such as East Asians and emojis) are
// accounted for.
// This treats the text as a sequence of grapheme clusters.
func StringWidth(s string) int {
	return stringWidth(GraphemeWidth, s)
}
```
`[XA width.go:60-67]`, implementation at `[XA width.go:78-107]`.

Scale of the error, for a realistic herdr-cron job name:

```
"일일보고 스케줄"  ->  runes: 8   bytes: 22   terminal cells: 15
```
computed with `unicodedata.east_asian_width` over each codepoint (`W`/`F` = 2 cells)
`[RUN]`.

So `len(s)` overstates by 14 and `len([]rune(s))` understates by 7. Concretely:

- Any column-width or truncation math on `len()` produces borders that jag by half a
  pane on a Korean job name.
- **Hit testing inherits the bug.** `Layer` bounds come from `Width(l.content)` and
  `Height(l.content)` `[LG layer.go:127-136, 229-233]` — correct, because they use
  `lipgloss.Width`. But any hand-rolled "click was at column N, so it's the Nth character"
  logic will be wrong by the accumulated wide-rune delta. Never convert an X coordinate to
  a string index by slicing; if you need it, walk grapheme clusters and accumulate widths.
- Rules: `lipgloss.Width` / `lipgloss.Height` / `lipgloss.Size` `[LG size.go]` for all
  measurement; `Style.Width(n)` for all padding-to-width; never `len`, never `%-20s` in
  `fmt.Sprintf` for a column containing user text. `Size` returns both
  `[LG size.go:33-38]`.
- Terminal-side caveat: whether a given emoji or a ZWJ sequence occupies 1 or 2 cells is
  terminal-dependent. Bubble Tea negotiates this at runtime — it queries Unicode Core mode
  and switches the renderer's width method on the reply
  (`case ansi.ModeUnicodeCore: ... p.renderer.setWidthMethod(ansi.GraphemeWidth)`
  `[BT tea.go:803-806]`). For Hangul this is a non-issue; for emoji status badges it is a
  reason to prefer ASCII glyphs or box-drawing characters.

---

## 6. Alt screen, resize, panics, cleanup

### 6.1 Alt screen

```go
	// AltScreen puts the program in the alternate screen buffer
	// (i.e. the program goes into full window mode). Note that the altscreen will
	// be automatically exited when the program quits.
	//
	// Example:
	//
	//	func (m model) View() tea.View {
	//	    v := tea.NewView("Hello, World!")
	//	    v.AltScreen = true
	//	    return v
	//	}
	AltScreen bool
```
`[BT tea.go:149-161]`. `tea.WithAltScreen()` and `tea.EnterAltScreen`/`ExitAltScreen` do
not exist `[BT UPGRADE_GUIDE_V2.md:288-312]`.

Verified: alt screen is `ESC[?1049h` on entry and `ESC[?1049l` on exit `[RUN]`, from the
`cellbuffer` byte dump in §2.1.

Tradeoffs worth stating rather than deciding silently:

- **Alt screen on**: a stable full-window app; nothing scrolls away; the terminal's
  scrollback is untouched. But there is nothing to select or scroll back through, so a user
  who wants to keep a job's error output must be given an explicit copy/export action.
  Known bug: alt screen is reported not to work correctly in macOS Terminal.app — #1455,
  open `[GH #1455]`.
- **Alt screen off (inline)**: output stays in scrollback, native selection works when
  mouse mode is off, `tea.Println`/`Printf` interleave with shell history
  `[BT tea.go:1381-1399]`. But the UI scrolls with the terminal and a mouse-driven
  two-pane layout in the primary buffer fights the user's own scrollback.
- Related open report: #1571, *"v2: Scrollback lost on tea.Quit"* — WezTerm-specific,
  requires `reset` afterwards `[GH #1571]`.

For a two-pane mouse-driven cron manager, alt screen on is the coherent choice; the copy
problem is answered by an explicit clipboard action (§2.4), not by abandoning alt screen.

### 6.2 Panics and terminal restoration

Panic catching is **on by default** and is what puts the terminal back.

```go
// WithoutCatchPanics disables the panic catching that Bubble Tea does by
// default. If panic catching is disabled the terminal will be in a fairly
// unusable state after a panic because Bubble Tea will not perform its usual
// cleanup on exit.
func WithoutCatchPanics() ProgramOption
```
`[BT options.go:72-80]`

Three recover sites: the event loop / `Run` `[BT tea.go:1035-1040]`, command execution
`[BT tea.go:732-736]`, and batch/sequence execution `[BT tea.go:900-950]`. On the `Run`
path the returned error is wrapped:

```go
		defer func() {
			if r := recover(); r != nil {
				returnErr = fmt.Errorf("%w: %w", ErrProgramKilled, ErrProgramPanic)
				p.recoverFromPanic(r)
			}
		}()
```
`[BT tea.go:1035-1040]`, with sentinels

```go
var ErrProgramPanic = errors.New("program experienced a panic")   // [BT tea.go:39]
var ErrProgramKilled = errors.New("program was killed")           // [BT tea.go:42]
var ErrInterrupted = errors.New("program was interrupted")         // [BT tea.go:45]
```

Rules for herdr-cron:

- Never pass `WithoutCatchPanics()`. The only reason to is if you install your own
  `recover` that calls `p.Kill()` and restores state — extra risk, no benefit.
- Always inspect `p.Run()`'s error and branch with `errors.Is`. A panic and a clean quit
  are distinguishable, and a scheduler daemon must exit non-zero on the former.
- `SIGINT`/`SIGTERM` are handled: `SIGINT` becomes `InterruptMsg{}`, others `QuitMsg{}`
  `[BT tea.go:651-688]`. In raw mode `^C` normally arrives as a key event, not a signal —
  the comment says so explicitly `[BT tea.go:654-661]` — so you must bind `ctrl+c` yourself
  or the app will not quit on it.
- `WithoutSignalHandler()` exists if you own signal handling `[BT options.go:64-70]`;
  for a TUI, don't.
- Suspend (`ctrl+z`) works on Unix only: `suspendProcess` sends `SIGTSTP` to the process
  group and blocks for `SIGCONT` `[BT tty_unix.go:39-46]`; on Windows
  `const suspendSupported = false` and `suspendProcess()` is empty
  `[BT tty_windows.go:62-64]`. Do not advertise a suspend keybinding cross-platform.

### 6.3 Resize

`WindowSizeMsg` is sent once at startup and on every resize `[BT screen.go:5-7]`. Unix:
`SIGWINCH` → `checkResize` → `term.GetSize` → `Send` `[BT signals_unix.go:15-32]`,
`[BT tty.go:107-127]`. Windows: console records → synthetic window-op sequence (§3.4).
`tea.RequestWindowSize()` re-queries on demand `[BT commands.go:161-170]`.

The renderer is frame-capped, default 60 FPS and hard-capped at 120
`[BT renderer.go:10-15]`, `[BT options.go:139-146]`, so a resize drag will not melt the
process even though every intermediate size produces a message.

---

## 7. Testing a Bubble Tea TUI

`teatest` is usable, and there is a **v2 module**: `github.com/charmbracelet/x/exp/teatest/v2`
`[TT exp/teatest/v2/go.mod:1]`, requiring `charm.land/bubbletea/v2` `[TT ...go.mod:5]`. Note
it is under `exp/` — experimental by placement.

```go
// Package teatest provides helper functions to test tea.Model's.
package teatest

func NewTestModel(tb testing.TB, m tea.Model, options ...TestOption) *TestModel  // [TT teatest.go:129]
func WithInitialTermSize(x, y int) TestOption                                    // [TT teatest.go:34]
func WithProgramOptions(options ...tea.ProgramOption) TestOption                 // [TT teatest.go:45]

func (tm *TestModel) Send(m tea.Msg)                                             // [TT teatest.go:260]
func (tm *TestModel) Type(s string)                                              // [TT teatest.go:271]
func (tm *TestModel) Quit() error                                                // [TT teatest.go:265]
func (tm *TestModel) Output() io.Reader                                          // [TT teatest.go:255]
func (tm *TestModel) FinalOutput(tb testing.TB, opts ...FinalOpt) io.Reader       // [TT teatest.go:249]
func (tm *TestModel) FinalModel(tb testing.TB, opts ...FinalOpt) tea.Model        // [TT teatest.go:235]
func (tm *TestModel) WaitFinished(tb testing.TB, opts ...FinalOpt)                // [TT teatest.go:228]
func (tm *TestModel) GetProgram() *tea.Program                                    // [TT teatest.go:281]

func WaitFor(tb testing.TB, r io.Reader, condition func(bts []byte) bool, options ...WaitForOption)  // [TT teatest.go:78]
func WithCheckInterval(d time.Duration) WaitForOption                             // [TT teatest.go:62]
func WithDuration(d time.Duration) WaitForOption                                  // [TT teatest.go:69]
func WithFinalTimeout(d time.Duration) FinalOpt                                   // [TT teatest.go:219]
func WithTimeoutFn(fn func(tb testing.TB)) FinalOpt                               // [TT teatest.go:211]
```

**Golden-file workflow: yes.**

```go
// RequireEqualOutput is a helper function to assert the given output is
// the expected from the golden files, printing its diff in case it is not.
//
// Important: this uses the system `diff` tool.
//
// You can update the golden files by running your tests with the -update flag.
func RequireEqualOutput(tb testing.TB, out []byte) {
	tb.Helper()
	golden.RequireEqualEscape(tb, out, true) //nolint:staticcheck
}
```
`[TT teatest.go:285-294]`, delegating to `github.com/charmbracelet/x/exp/golden`
`[TT exp/teatest/v2/go.mod:6]`. Goldens live in `testdata/` — the package's own
`testdata/` directory exists in the clone `[TT exp/teatest/v2/testdata]`.

A complete real test from the package, showing size, input, waiting, golden assert, and
final-model assert:

```go
func TestApp(t *testing.T) {
	m := model(10)
	tm := teatest.NewTestModel(
		t, m,
		teatest.WithInitialTermSize(70, 30),
	)
	t.Cleanup(func() {
		if err := tm.Quit(); err != nil {
			t.Fatal(err)
		}
	})

	time.Sleep(time.Second + time.Millisecond*200)
	tm.Type("I'm typing things, but it'll be ignored by my program")
	tm.Send("ignored msg")
	tm.Send(tea.KeyPressMsg{
		Code: tea.KeyEnter,
	})

	if err := tm.Quit(); err != nil {
		t.Fatal(err)
	}

	out := readBts(t, tm.FinalOutput(t, teatest.WithFinalTimeout(time.Second)))
	if !regexp.MustCompile(`This program will exit in \d+ seconds`).Match(out) {
		t.Fatalf("output does not match the given regular expression: %s", string(out))
	}
	teatest.RequireEqualOutput(t, out)

	if tm.FinalModel(t).(model) != 9 {
		t.Errorf("expected model to be 10, was %d", m)
	}
}
```
`[TT exp/teatest/v2/app_test.go:15-46]`

and the polling helper in use:

```go
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("7"))
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(time.Millisecond*10))
```
`[TT exp/teatest/v2/app_test.go:60-63]`

Caveats to plan around:

- `WaitFor` defaults to a **1 s** budget with a 50 ms check interval
  `[TT teatest.go:73-76, 88-93]`, and `doWaitFor` accumulates output in a buffer and
  re-tests the whole buffer each poll `[TT teatest.go:90-108]`. Anything slower than 1 s
  needs `WithDuration` explicitly.
- The tests in the package use `time.Sleep` `[TT app_test.go:26]`. Bubble Tea's own tracker
  carries #1746, *"TestViewModel is flaky under the Taskfile's test flags"*, open `[GH #1746]`
  — timing-based TUI tests are flaky by nature. Prefer `WaitFor` over `Sleep`, and prefer
  asserting on `FinalModel` state over golden bytes where you can.
- **Mouse can be tested without a terminal.** `tm.Send(tea.MouseClickMsg{X: 12, Y: 4, Button: tea.MouseLeft})`
  is just a message — the concrete mouse types are plain structs over `Mouse`
  `[BT mouse.go:69-78]`, and `Send` forwards to the program `[TT teatest.go:260-263]`. This
  makes hit testing genuinely unit-testable: build the model, send a synthetic click at a
  known cell, assert the selected job changed. **Do this**; it is the only affordable way to
  keep mouse regressions out on three platforms.
- Golden files record raw ANSI including mode-setting sequences, so a golden test over a
  view that toggles `MouseMode` will churn. Scope goldens to steady-state screens.
- For the harness itself, `tea.WithoutRenderer()` `[BT options.go:90-102]` and
  `tea.WithWindowSize(w, h)` / `tea.WithColorProfile(p)` `[BT options.go:148-168]` are the
  determinism levers; the latter two are new in v2 and explicitly documented as being for
  testing `[BT UPGRADE_GUIDE_V2.md:342-348]`.

---

## 8. Implications for herdr-cron  `[INFERENCE — design proposal, not source]`

Everything in this section is my recommendation derived from the evidence above. It is not
quoted from any source.

### 8.1 Library choices

| Concern | Choice | Why |
|---|---|---|
| TUI framework | `charm.land/bubbletea/v2` (pin ≥ v2.0.9) | Only supported line; v1 is a different module. |
| Styling & layout | `charm.land/lipgloss/v2` (≥ v2.0.6) | Required by Bubble Tea v2; correct CJK width. |
| Hit testing | `lipgloss.Layer` + `Compositor.Hit` + `View.OnMouse` | No extra dependency; z-ordering for modals; render and hit-test share one structure (§2.3). |
| bubblezone | **Not adopted** | Redundant given the compositor; extra module, no published releases, per-rune width measurement (§2.3 Option C). Revisit only if per-row layers prove unworkable. |
| Widgets | `bubbles/v2`: `table`, `viewport`, `textinput`, `spinner`, `help`, `key` | See §4. **Not** `list`. |
| Mouse mode | `tea.MouseModeCellMotion`, user-toggleable to `MouseModeNone` | Best-supported mode; toggle answers the copy/paste problem (§2.1, §2.4). |
| Alt screen | On | Two-pane full-window app (§6.1). |
| Tests | `github.com/charmbracelet/x/exp/teatest/v2` + synthetic `MouseClickMsg` | §7. |

Cross-document note: the scheduling engine is **gocron** and the host is **Herdr** — both
covered by sibling research. The only contract this document assumes is that the scheduler
exposes (a) a snapshot query and (b) a `<-chan Event` for state changes; §1.5 pattern (b)
consumes the latter.

### 8.2 Screens and their mouse affordances

**Screen 1 — Job list (root).** Two panes plus header and footer.

```
┌ herdr-cron ──────────────────────────────── mouse: on (m) ┐
│ ● daily-report   0 18 * * 1-5   in 02:14   ok    │ detail │
│ ○ nightly-sync   0 3 * * *      disabled   —     │  pane  │
│ ● gitlab-poll    */5 * * * *    in 00:03   fail  │        │
├──────────────────────────────────────────────────┴────────┤
│ ↑/↓ move · enter detail · space toggle · r run now · m …  │
└───────────────────────────────────────────────────────────┘
```

Layers: `pane.jobs`, `pane.detail`, `pane.help`, plus one layer per visible row
(`row.<jobID>`) or a single body layer with `mouse.Y - bounds.Min.Y` arithmetic.

| Affordance | Interaction | Implementation |
|---|---|---|
| Select a job | left click on a row | `LayerHitMsg` → `table.SetCursor(row)` (§2.5) |
| Open detail | double-click, or click then `enter` | v2 has no double-click message; track `(lastClickID, lastClickTime)` in the model and treat two clicks within ~400 ms on the same ID as a double-click. Keep `enter` as the accessible path. |
| Scroll the list | wheel over `pane.jobs` | `MouseWheelMsg` → `table.MoveUp/MoveDown(3)`; `table` will not do it (§4.1) |
| Enable/disable | click the `●`/`○` status glyph | give the glyph its own child layer `row.<id>.toggle`; `Hit` returns the innermost/highest-z layer, so the glyph wins over the row `[LG layer.go:280-302]` |
| Run now | click a `▶` affordance in the row's right gutter | child layer `row.<id>.run`; on click emit `RunNowMsg{JobID}` and disable the affordance while running (mirror it in `help` via `key.Binding.Enabled()`) |
| Focus a pane | click anywhere in it | drive `table.Focus()`/`Blur()` from the hit ID, or unfocused arrow keys go nowhere (§4.1) |
| Toggle mouse | click the `mouse: on (m)` header, or press `m` | flips `MouseMode` between `CellMotion` and `None`. Note: once off, the click target is unreachable — the key must exist and the footer must say so. |

**Screen 2 — Job detail.** `viewport` for the definition (cron expr, next N fire times,
command, cwd, env, timeout, overlap policy), with a run-history `table` beneath it.

| Affordance | Interaction |
|---|---|
| Scroll definition | wheel over the viewport — works natively `[BB viewport/viewport.go:696-721]` |
| Back to list | click the breadcrumb layer `nav.back`, or `esc` |
| Edit / delete | clickable button layers that open a modal (below) |
| Select a run | click a history row → loads that run's output into Screen 3 |

**Screen 3 — Run history / run output.** History `table` (started-at, duration, exit code,
trigger) plus a `viewport` for the captured output of the selected run.

| Affordance | Interaction |
|---|---|
| Scroll output | wheel over the output viewport |
| Copy output | click `copy` button → `tea.SetClipboard(...)`. **This is the designed answer** to mouse-mode killing native selection (§2.4) — do not rely on the user turning the mouse off to select text. |
| Re-run this job | click `▶ run again` → `RunNowMsg{JobID}` |

**Screen 4 — Modal overlay** (confirm delete, cron-expression helper). One layer at
`Z(100)` over a dimmed background layer. Because `Hit` scans highest-z first
`[LG layer.go:280-302]`, clicks land on the modal and clicks on the dimmed background can
be interpreted as "dismiss" by matching the background layer's own ID. This is the concrete
reason to choose layers over zones.

### 8.3 Message vocabulary

Keep every mouse consequence in one derived message type, so `Update` has a single mouse
entry point and the raw `MouseMsg` cases stay empty:

```
LayerHitMsg{ID string, Mouse tea.MouseMsg}   // from View.OnMouse, per clickable/example
JobsLoadedMsg{[]Job}                         // scheduler snapshot
JobStateChangedMsg{JobID, State}             // from the scheduler channel, pattern (b)
RunStartedMsg / RunFinishedMsg{RunID, ExitCode, Duration}
ClockTickMsg time.Time                       // tea.Every(time.Second) for countdowns
```

Because `OnMouse`'s command is dispatched asynchronously (`go p.Send(cmd())`
`[BT tea.go:813-815]`), never assume a raw `MouseClickMsg` and its `LayerHitMsg` arrive
adjacently. Follow `examples/clickable` and act only on `LayerHitMsg`
`[BT examples/clickable/main.go:78-197]`.

### 8.4 Non-negotiables

1. Every mouse affordance has a keyboard equivalent, shown in `help`. Mouse mode can be
   off (for copy/paste), the terminal may not report mouse at all, and a Herdr-hosted pane
   may sit behind a multiplexer that swallows it.
2. All measurement through `lipgloss.Width`/`Height`. Korean job names and descriptions are
   expected; `len()` is off by ~2× per Hangul syllable (§5.3).
3. Manual verification on Windows Terminal with alt screen **and** mouse enabled, before
   claiming Windows support — #1391 is exactly that combination `[GH #1391]`.
4. Verify SGR (1006) is negotiated on every target terminal. Legacy X10 fallback silently
   drops events past column 95 `[GH #359]`, `[BT tea.go:294-297]`.
5. Synthetic-`MouseClickMsg` tests for every hit-test region (§7). Hit-test arithmetic is
   the highest-risk, least-visible code in the TUI.
6. No suspend keybinding advertised cross-platform — it is a no-op on Windows
   `[BT tty_windows.go:62-64]`.

---

## Could not verify

- **Windows behaviour, empirically.** Everything in §3 is read from source (`tty_windows.go`,
  `signals_windows.go`, ultraviolet's `*_windows.go`) plus the issue tracker. This machine
  is Linux/WSL2, so I could not run a Bubble Tea v2 binary against conhost or Windows
  Terminal natively. In particular I could not confirm that mouse clicks are delivered with
  alt screen active on Windows Terminal (the #1391 scenario, closed but untested here), nor
  the interaction with `ENABLE_QUICK_EDIT_MODE`, which ultraviolet's *other* Windows reader
  path (`preparePollConsole`) explicitly sets `[UV poll_windows.go:196]` while also clearing
  `ENABLE_MOUSE_INPUT` `[UV poll_windows.go:191]`. Which of the two Windows reader paths
  (`conInputReader` vs `conReader`) Bubble Tea ends up on at runtime is decided inside
  ultraviolet and I did not trace it to a conclusion.
- **Whether conhost/Windows Terminal, under `ENABLE_VIRTUAL_TERMINAL_INPUT`, delivers
  SGR mouse bytes once DECSET 1002/1006 is set.** The Bubble Tea/ultraviolet side is
  established (§3.2: `TerminalReader.MouseMode` is never set, so the record-synthesis path
  is inert). The console side is Microsoft's behaviour; I cite the Microsoft console-mode
  and VT-sequence docs `[MS]` but did not observe the byte stream on Windows. Treat "mouse
  works on Windows" as *expected from the code path*, not *measured*.
- **`lipgloss.Width` on Korean text, measured.** I read the implementation
  (`ansi.StringWidth`, grapheme-cluster walk) and confirmed the doc via `go doc`, and I
  computed cell widths independently with Python's `unicodedata.east_asian_width` `[RUN]`.
  I did not execute Go code that calls `lipgloss.Width("일일보고 스케줄")`, because the
  assignment prohibits creating Go source files. The two should agree for Hangul; they may
  differ for emoji/ZWJ, which I did not test.
- **No official hosted Bubble Tea documentation site was consulted.** The authoritative
  written sources for v2 are in-repo (`UPGRADE_GUIDE_V2.md`) and the v2.0.0 GitHub release
  notes; I used both. I did not find or read a `charm.land` docs page for Bubble Tea v2, so
  I cannot report whether one exists and whether it contradicts the repo.
- **`bubbles` HEAD is not a tagged release.** The clone is at `0a69b19` (main, 2026-09-01),
  which is ahead of `v2.2.1` (2026-08-24) `[REL]`. Component signatures cited from
  `[BB]` are from main; a released `v2.2.1` build could differ in unreleased commits. I did
  not diff main against the tag.
- **The double-click threshold** in §8.2 is my invention. Bubble Tea v2 has no
  double-click message type and no configurable threshold that I found; `mouse.go` defines
  no such concept `[BT mouse.go]`. The Windows console record path does surface
  `DOUBLE_CLICK` `[UV terminal_reader_windows.go:140]`, but it is folded into an ordinary
  click and, per §3.2, that path is inert under Bubble Tea.
- **Performance claims are qualitative.** I did not benchmark `Compositor` flatten +
  `Canvas` allocation per frame, nor `MouseModeAllMotion` message volume. Open issue #1481
  (*"Significant Performance Degradation in v2 (2x slower at-least)"*) is unresolved
  `[GH #1481]` and I did not investigate its validity.
- **`teatest` is under `exp/`** and its v2 module requires `charm.land/bubbletea/v2 v2.0.0`
  `[TT exp/teatest/v2/go.mod:5]`, older than the v2.0.9 we would pin. I did not run its
  test suite against v2.0.9 to confirm compatibility.
- **Terminal-specific mouse quirks** beyond what the tracker records (tmux with
  `set -g mouse off`, Herdr's own pane multiplexing, screen, older iTerm2) were not tested.
  Whether Herdr forwards mouse events to hosted panes at all is a question for the Herdr
  research document, not this one.
