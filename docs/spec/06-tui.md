---
title: herdr-cron — TUI specification
date: 2026-09-02
status: spec (normative)
---

# TUI specification

Normative. RFC 2119 keywords. Terminal-stack claims cite
`docs/research/2026-09-02-bubbletea-mouse-tui.md` (**[R]**) by section; job, storage, and command
claims cite [`03-job-model.md`](03-job-model.md), [`04-storage.md`](04-storage.md), or
[`05-cli.md`](05-cli.md), which win any conflict — see §10. The TUI is launched by bare `herdr-cron`
on a TTY (`05-cli.md` §1).

## 1. Principles

### 1.1 The TUI owns no scheduler

**D1.** The TUI MUST NOT construct a gocron scheduler, own timers that execute work, or be required
for a job to run: quitting it — cleanly, by `SIGKILL`, or by closing the terminal — MUST have no
effect on the schedule. Jobs run only in the process holding `daemon.lock` (`04-storage.md` §7) or
in `herdr-cron run-once` (`05-cli.md` §3.5); the TUI can run while nothing is scheduling (§7.4).

### 1.2 The TUI is a client of the store, over the CLI's code paths

**D2.** Reads are file reads of `jobs.yaml`, `state.json`, `overrides.json`, `daemon.json`,
`runs/<jobId>.jsonl`, and `logs/<jobId>/<runId>.log`; writes go through the packages the cobra
commands call, so no second YAML writer, override merge, or `nextRunAt` computation exists, and the
TUI MUST NOT lock to render (`04-storage.md` §2, §9). The `enabled` toggle MUST write
`overrides.json` under `overrides.lock`, never `jobs.yaml` (`03-job-model.md` §5); `job run` and
`job cancel` MUST be trigger files (`04-storage.md` §8), and with no daemon the TUI MUST surface
`daemon_unreachable`.

### 1.3 Keyboard parity is mandatory

Every mouse affordance MUST have a keyboard equivalent listed in the help bar: mouse reporting may
be switched off to recover native copy ([R] §2.4), the terminal may not report mouse, or a Herdr
pane's multiplexer may swallow the events ([R] §8.4 item 1, §9). No mouse-only action; no
keyboard-only *destructive* action.

### 1.4 The stack: Bubble Tea v2 only

`charm.land/bubbletea/v2` (`>= v2.0.9`), `charm.land/lipgloss/v2` (`>= v2.0.6`), and
`charm.land/bubbles/v2` — `table`, `viewport`, `textinput`, `spinner`, `help`, `key` ([R] §8.1, §4).
Hit testing is `lipgloss.NewLayer` + `lipgloss.NewCompositor` + `Compositor.Hit` +
`tea.View.OnMouse` ([R] §2.3 B); mouse mode is `tea.MouseModeCellMotion`, toggleable to
`tea.MouseModeNone` ([R] §2.1); alt screen is on via `tea.View.AltScreen = true` ([R] §6.1).
`bubbles/list` MUST NOT be used (keyboard-only, no wheel path, [R] §4.2) and `lrstanley/bubblezone` MUST NOT be
added ([R] §2.3 C).

These v1 names do not exist in v2 and MUST NOT appear in herdr-cron source, tests, or docs:
`github.com/charmbracelet/bubbletea` (the v1 module path), `tea.WithAltScreen`,
`tea.WithMouseCellMotion`, `tea.EnterAltScreen`, `tea.ExitAltScreen`,
`tea.EnableMouseCellMotion`, `tea.DisableMouse`, `tea.MouseMsg` as a struct with `X`/`Y`, and
`tea.MouseAction` ([R] "Read this first", §2.4, §6.1).

In v2, `View()` returns a `tea.View` **struct** whose terminal modes are declarative fields
(`[BT tea.go:84-186]`); `tea.MouseMsg` is an **interface** whose one method is `Mouse() Mouse`; the
concrete events are `tea.MouseClickMsg`, `tea.MouseReleaseMsg`, `tea.MouseWheelMsg`, and
`tea.MouseMotionMsg`, each defined *as* `tea.Mouse` so `msg.X`, `msg.Y`, `msg.Button`, `msg.Mod` are
promoted; `tea.Mouse` is `struct { X, Y int; Button MouseButton; Mod KeyMod }`, zero-based from the
upper-left cell. There is no `Action` field and no `Alt`/`Ctrl`/`Shift` booleans — modifiers are the
`tea.KeyMod` bitmask tested with `Mod.Contains` — and a scroll is a `tea.MouseWheelMsg` whose
`Button` is `tea.MouseWheelUp`/`tea.MouseWheelDown` ([R] §2.2).

### 1.5 Widget mouse support is code we own

Exactly one non-test file in `bubbles` references any mouse message: `viewport/viewport.go`, so
`viewport` is the only widget that handles the wheel and everything else is code this spec specifies
([R] §2.5). `bubbles/table` handles `tea.KeyPressMsg` only — no row selection, no wheel scroll — and
its `Update` never forwards to its internal `viewport`, so that viewport's wheel handling is
unreachable through the table; `bubbles/list` is keyboard-only with no wheel path at all ([R] §2.5,
§4.1, §4.2). `table` also hard-returns when unfocused, so `Focus()`/`Blur()` MUST be driven from the
hit result. `table.View()` is `m.headersView() + "\n" + m.viewport.View()`, one header line above
the body, and `SetHeight` subtracts that header, so the **total** height is passed and row
arithmetic MUST account for the line: `row = mouse.Y - bounds.Min.Y - 1` then `SetCursor`, and
`MoveUp(3)`/`MoveDown(3)` on a wheel message ([R] §4.1).

## 2. Screens

Four screens; exactly one is active, and the modal overlay composes **over** the active screen. Each
builds a root layer with panes as `.ID(...)` children and affordances as children of panes.
`Layer.AddLayers` recomputes extents from the union of children and `Compositor` computes absolute
bounds during flatten, so nested affordances get correct hit rectangles with no arithmetic ([R]
§5.2). `Compositor.Hit` walks highest-z first and returns the first non-empty-ID layer containing
the point (`[LG layer.go:277-303]`); empty-ID layers are ignored, so decoration MUST be unnamed and
ids exist only for visible rows.

**CORRECTION, verified by implementation on 2026-09-02.** Hit testing does **not** use
`lipgloss.Compositor`, because the design above rests on an assumption that is false: **a layer is
opaque even when its content is only spaces.** Measured directly —
`NewCompositor(NewLayer("ABCDE\nFGHIJ\nKLMNO").AddLayers(NewLayer("  ").X(1).Y(1).Z(1)))` renders
`"ABCDE\nF  IJ\nKLMNO"`, so the two-space overlay erased `GH`. There is no transparent fill, and
therefore no such thing as an invisible hit layer: the first implementation of this section
rendered an entirely blank screen, every pane painted over by its own hit rectangle.

The mechanism is instead a **hit-rectangle table** the layout emits alongside the content:
`hitRect{ID string; Rect image.Rectangle; Z int}`, resolved highest-`Z` first with the smallest
rectangle winning ties, so a child affordance still beats the pane containing it. Every id, z
ordering and precedence rule in the tables below is unchanged — only the data structure differs.
Rendering stays `lipgloss.JoinVertical`/`JoinHorizontal` over fixed-size boxes, which is what
makes the rectangles exact: content and hit geometry are computed from the same numbers.
`lipgloss.Compositor` is still used for the modal, where painting over the screen is the intent.

A second, smaller correction: **`table.SetWidth` is mandatory.** The widget's internal viewport
defaults to zero width, and without it `View()` returns the header line and nothing else — three
rows present, none visible. Columns must also be budgeted at `Width + 2` each, because the default
cell style pads one cell either side; overflow wraps the header and collapses the layout.

### 2.1 Screen 1 — Job list (root, two-pane)

```
┌ herdr-cron ── daemon running · pid 40211 ─────────── mouse: on (m) ─┐
│  ●  job              schedule       next run   last   │ nightly-deps│
│  ●  nightly-deps     17 3 * * 1-5   in 15:41   ok   ▶ │ agent/claude│
│  ○  build-smoke      every 30m      —          ok   ▶ │ enabled     │
│  ●  일일보고 스케줄   0 18 * * 1-5   in 06:20   no-op▶ │   true(file)│
│  ⊘  weekly-audit     0 4 * * 0      —          fail ▼ │ fails 3/3   │
├───────────────────────────────────────────────────────┴─────────────┤
│ ↑/↓ move · enter open · space toggle · r run · m mouse · ? · q quit │
└─────────────────────────────────────────────────────────────────────┘
```

The leading glyph is effective `enabled`: `●` enabled, `○` disabled by override, `⊘` disabled by the
circuit breaker (`consecutiveFailures` reached `limits.maxConsecutiveFailures`, `03-job-model.md`
§4.5). Columns are that glyph, `name` (or `id`), `schedule.expression` or the `every` duration,
`nextRunAt` as a countdown, and `lastRun.status`, from the `job_list` payload (`05-cli.md` §3.1);
narrow widths shed columns via `Column.Width = 0`. A job `id` matches `^[a-z0-9][a-z0-9._-]{0,127}$`
(`03-job-model.md` §1.2), safe as a layer id unescaped.

Layer ids: `pane.header` (row 0, holding `hdr.daemon` and `hdr.mouse`); `pane.jobs` enclosing
`jobs.body` (scrolling rows only, header line excluded — the row-arithmetic origin) and one
`row.<jobID>` per visible row, each with higher-z children `row.<jobID>.toggle` (glyph cell) and
`row.<jobID>.run` (trailing `▶`); `pane.detail`; `pane.help`.

| Interaction | Message | Effect |
| --- | --- | --- |
| Left click `row.<jobID>` | `LayerHitMsg{ID, Mouse: MouseClickMsg}` | `table.SetCursor(row)`; `table.Focus()`; detail pane re-renders |
| Second click on the same row within 400 ms | `LayerHitMsg` | push Screen 2 for that job |
| Left click `row.<jobID>.toggle` | `LayerHitMsg` | `ToggleEnabledCmd(jobID)` — the `job pause`/`job resume` path, writing `overrides.json` under its lock (§1.2) |
| Left click `row.<jobID>.run` | `LayerHitMsg` | `RunNowCmd(jobID)` — trigger file, `action: "run"`, `wait: false` (`04-storage.md` §8); disabled while the latest record is `running` |
| Wheel over `pane.jobs` | `LayerHitMsg{Mouse: MouseWheelMsg}` | `table.MoveUp(3)`/`MoveDown(3)`; `table` will not do it ([R] §2.5) |
| Wheel over `pane.detail` | `LayerHitMsg{Mouse: MouseWheelMsg}` | forward the wheel message to the detail `viewport.Update` — native ([R] §2.5) |
| Left click in `pane.detail` | `LayerHitMsg` | `table.Blur()`; focus the detail viewport |
| Left click `hdr.mouse` / `hdr.daemon` / `pane.help` | `LayerHitMsg` | `MouseModeToggledMsg` (§6) / daemon modal when `stale`/`stopped` / switch the footer between `ShortHelp` and `FullHelp` |

v2 has no double-click message and no configurable threshold; the 400 ms rule is herdr-cron's, held
as `(lastClickID, lastClickTime)` ([R] §8.2, marked an invention). `enter` MUST remain the primary
way to open a job.

### 2.2 Screen 2 — Job detail

```
┌ ‹ jobs / nightly-deps ─────────────────────────── mouse: on (m) ────┐
│ Nightly dependency audit                  [ run ▶ ] [ pause ] [ ✕ ] │
│ kind          agent (claude)     session herdr-cron                ▲│
│ schedule      17 3 * * 1-5  Asia/Seoul   jitter 743s                │
│ enabled       true (source: file)   next  03:29:23 · 09-04 · 09-05  │
│ failures      0 / 3                 runs today   1 / 4             ▼│
├─ recent runs ───────────────────────────────────────────────────────┤
│  2026-09-02 03:29   879s  no_op    scheduler                        │
│  2026-08-31 03:29     4s  failure  scheduler   exit 1               │
├─────────────────────────────────────────────────────────────────────┤
│ esc back · ↑/↓ run · enter output · p pause · r run · d delete · q  │
└─────────────────────────────────────────────────────────────────────┘
```

One `job get` read supplies the resolved record, `nextRuns` (five instants), and `recentRuns` (ten
records) — one round trip per detail screen (`05-cli.md` §3.1). Shown: `kind`, `payload.agentKind`,
`payload.session`, `schedule.*`, `enabled` with `enabledSource`, `state.nextRunAt`,
`state.nextRuns`, `state.consecutiveFailures` vs `limits.maxConsecutiveFailures`, `state.runsToday`
vs `limits.maxRunsPerDay`, `cwd`, `env`, `timeoutSec`, `concurrency`, `retry`, `notify`. Layer ids:
`nav.back`, `pane.definition`, `btn.job.run`, `btn.job.pause`, `btn.job.delete`, `pane.runs`,
`row.run.<runId>`, `pane.help`, `hdr.mouse` (`03-job-model.md` §1.3).

| Interaction | Message | Effect |
| --- | --- | --- |
| Click `nav.back` | `LayerHitMsg` | pop to Screen 1 |
| Wheel over `pane.definition` | `LayerHitMsg{Mouse: MouseWheelMsg}` | forward to the definition `viewport` ([R] §2.5) |
| Click `btn.job.run` / `btn.job.pause` | `LayerHitMsg` | `RunNowCmd(jobID)`, disabled while a run is `running` (`03-job-model.md` §6) / `ToggleEnabledCmd(jobID)`, labelled from effective `enabled` |
| Click `btn.job.delete` | `LayerHitMsg` | open the confirm modal (§2.4); deletion MUST NOT happen on this click |
| Click `row.run.<runId>` | `LayerHitMsg` | push Screen 3 with that run selected |
| Wheel over `pane.runs` | `LayerHitMsg{Mouse: MouseWheelMsg}` | `MoveUp(3)`/`MoveDown(3)` |

### 2.3 Screen 3 — Run history and run output

```
┌ ‹ jobs / nightly-deps / runs ──────────────────── mouse: on (m) ────┐
│ started            dur   status   trigger  exit │ output     [copy] │
│ 2026-09-02 03:29   879s  no_op    scheduler   0 │ HEARTBEAT_OK    ▲ │
│ 2026-08-31 03:29     4s  failure  scheduler   1 │                   │
│ 2026-08-29 03:29    n/a  running  manual      – │                 ▼ │
│                                        [ run ▶ ]│                   │
├─────────────────────────────────────────────────┴───────────────────┤
│ esc back · ↑/↓ run · y copy · r run again · F follow · ? · q quit   │
└─────────────────────────────────────────────────────────────────────┘
```

Columns are `startedAt`, `durationSec`, `status`, `trigger`, `exitCode` from the run record
(`03-job-model.md` §6). `trigger` is the run-trigger enum — `scheduler` | `manual` | `catchup` |
`retry` | `startup` — never a trigger file (`04-storage.md` §8 "Terminology"). The output pane shows
`outputExcerpt` first, then the full `logPath`, and a log containing the elision marker `... <n>
bytes elided by herdr-cron ...` MUST render it verbatim. Layer ids: `nav.back`, `pane.runlist`,
`row.run.<runId>`, `pane.output`, `btn.copy`, `btn.rerun`, `pane.help`, `hdr.mouse`.

| Interaction | Message | Effect |
| --- | --- | --- |
| Click `row.run.<runId>` | `LayerHitMsg` | select the run; load `logPath` asynchronously; `RunLogLoadedMsg` replaces the excerpt |
| Wheel over `pane.output` | `LayerHitMsg{Mouse: MouseWheelMsg}` | forward to the output `viewport` — native ([R] §2.5) |
| Wheel over `pane.runlist` | `LayerHitMsg{Mouse: MouseWheelMsg}` | `MoveUp(3)`/`MoveDown(3)` |
| Click `btn.copy` | `LayerHitMsg` | `tea.SetClipboard(...)`; the designed answer to mouse mode killing native selection — the user MUST NOT have to disable the mouse to copy ([R] §2.4) |
| Click `btn.rerun` | `LayerHitMsg` | `RunNowCmd(jobID)` for the run's `jobId` |

### 2.4 Screen 4 — Modal overlay

Used for confirm-delete, the daemon-state explainer from `hdr.daemon`, the `configError` explainer
(§7.4), and a schedule helper backed by `validate --schedule … --next 5` (`05-cli.md` §3.5).

```
        ┌ delete job ──────────────────────────────────┐
        │  Delete "nightly-deps" from jobs.yaml?       │
        │  [x] also purge 47 runs and 47 logs          │
        │            [ cancel ]   [ delete ]           │
        └──────────────────────────────────────────────┘
```

`modal.scrim` sits at `.Z(99)` covering the terminal and `modal.body` at `.Z(100)`, with children
`modal.confirm`, `modal.cancel`, `modal.purge`. Because `Hit` scans highest-z first the modal
swallows clicks meant for the screen underneath and a click on `modal.scrim` is unambiguously
"outside" — the concrete reason layers beat zones ([R] §2.3, §8.2).

| Interaction | Message | Effect |
| --- | --- | --- |
| Click `modal.confirm` | `LayerHitMsg` | perform the action — for delete, the `job rm` path with `--purge` iff `modal.purge` is checked (`05-cli.md` §3.1) |
| Click `modal.cancel` or `modal.scrim`, or `esc` | `LayerHitMsg` / `tea.KeyPressMsg` | dismiss, no side effect |
| Click `modal.purge` | `LayerHitMsg` | toggle the checkbox |

While a modal is open the underlying screen's bindings MUST be disabled with `key.WithDisabled()`,
which also drops them from the help bar, since `key.Binding.Enabled()` is false for them
(`[BB key/key.go:103-108]`). `MouseMode` is the one exception (§6.2).

## 3. Message vocabulary

`tea.Msg` is an alias for `uv.Event`, not a struct, and `tea.Cmd` is `func() tea.Msg` ([R] §1.1).
Payloads are `05-cli.md` §3.1 and `03-job-model.md` §1.3/§6.

```go
// Derived mouse message — the ONLY mouse entry point in Update; shape copied
// from the in-tree clickable example, [R] §2.3 Option B.
type LayerHitMsg struct{ ID string; Mouse tea.MouseMsg }

// Store reads. Daemon is running|stale|stopped, ConfigError is daemon.json's
// configError field (04-storage.md §7).
type JobsLoadedMsg struct{ Jobs []jobsummary.Job; Daemon daemonstatus.Status; ConfigError *string; GeneratedAt time.Time }
type JobDetailLoadedMsg struct{ Job jobmodel.Resolved; NextRuns []time.Time; RecentRuns []jobmodel.Run }
type RunsLoadedMsg struct{ JobID string; Runs []jobmodel.Run }
type RunLogLoadedMsg struct{ RunID, Content string; Truncated bool }

// Change notification and cadence (§4).
type StoreChangedMsg struct{ Paths []string } // debounced fsnotify batch
type ClockTickMsg time.Time                   // tea.Every(time.Second)
type PollTickMsg time.Time                    // tea.Tick(5*time.Second)

// Local UI state.
type MouseModeToggledMsg struct{ On bool }
type ScreenPushedMsg struct{ Screen screenID }; type ScreenPoppedMsg struct{}
type ModalOpenedMsg struct{ Modal modalID; JobID string }; type ModalClosedMsg struct{}

// Outcomes: never a panic, always a message. Op is
// toggle_enabled|run_now|cancel|delete|reload|watch; Err carries a 05-cli.md §2.1 code.
type WriteResultMsg struct{ Op, JobID string; Err *clierr.Error }
type ClipboardCopiedMsg struct{ Bytes int }
type TornReadMsg struct{ Path string; Attempt int } // §4.3
```

`View()` MUST set `OnMouse` to a closure capturing the frame's compositor **by value** and returning
a command producing a `LayerHitMsg`, as the in-tree example does (`[BT examples/clickable/main.go:234-256]`):

```go
func (m model) View() tea.View {
	comp := lipgloss.NewCompositor(m.rootLayer()) // §2 layer tree
	v := tea.NewView(comp.Render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeNone
	if m.mouseOn {
		v.MouseMode = tea.MouseModeCellMotion
	}
	v.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
		return func() tea.Msg {
			if id := comp.Hit(msg.Mouse().X, msg.Mouse().Y).ID(); id != "" {
				return LayerHitMsg{ID: id, Mouse: msg}
			}
			return nil
		}
	}
	return v
}
```

`Update`'s cases for `tea.MouseClickMsg`, `tea.MouseReleaseMsg`, `tea.MouseWheelMsg`, and
`tea.MouseMotionMsg` MUST remain empty; all mouse behaviour lives in the `LayerHitMsg` case, which
discriminates on `msg.Mouse.(type)` and, for wheel events, `msg.Mouse.Mouse().Button`.

**The asynchrony caveat is normative.** The event loop calls `onMouse`, dispatches its result with
`go p.Send(cmd())`, then delivers the raw mouse message to `Update` unconditionally
(`[BT tea.go:808-816, 879-880]`, [R] §2.3 Option B). For one physical click `Update` sees the raw
`tea.MouseClickMsg` **first** and the derived `LayerHitMsg` **later, from another goroutine**, so no
logic may assume they arrive adjacently, and the 400 ms rule of §2.1 MUST use `LayerHitMsg`
timestamps only. `OnMouse` runs against the **previous** frame (`s.lastView.OnMouse`, [R] §2.3),
which is why the closure MUST capture by value.

Shift-click MUST NOT be required — some emulators do not send the shift modifier for mouse actions
(`[BB viewport/viewport.go:701-702]`). Only `tea.MouseLeft` is load-bearing; `tea.MouseRight` and
`tea.MouseMiddle` MUST be ignored in v1.

## 4. Data flow

### 4.1 No socket; the watcher goroutine

**D6.** There is no socket, no named pipe, no TCP (`README.md` D6). Change reaches the TUI from two
sources only: an fsnotify watch on the state directory, and a periodic tick. The watcher lives
outside the model tree, so it uses the `p.Send()`-from-a-goroutine pattern — the case [R] §1.5 names
explicitly, "a file watcher on the job store" — shaped after the `send-msg`
example (`[BT examples/send-msg/main.go:104-124]`):

```go
p := tea.NewProgram(newModel(store))

go func() { // errors are reported as WriteResultMsg{Op:"watch"}
	w, _ := fsnotify.NewWatcher()
	defer w.Close()
	// Watch directories, never files: rename replaces the inode (04-storage.md §3.1).
	for _, d := range []string{stateDir, filepath.Dir(cfgPath), filepath.Join(stateDir, "runs")} {
		_ = w.Add(d)
	}
	var pending []string
	debounce := time.NewTimer(time.Hour)
	defer debounce.Stop()
	for {
		select {
		case ev := <-w.Events:
			pending = append(pending, ev.Name)
			debounce.Reset(200 * time.Millisecond)
		case <-debounce.C:
			if len(pending) > 0 {
				batch := pending
				pending = nil
				// Send blocks until the event loop is ready; hold no lock here.
				p.Send(StoreChangedMsg{Paths: batch})
			}
		}
	}
}()

if _, err := p.Run(); err != nil { /* report and exit non-zero */ }
```

`p.Send` blocks until the event loop is ready ([R] §1.5), so the watcher MUST NOT hold a lock across
it; it is safe after shutdown, dropping the message on a closed context (`[BT tea.go:1192-1197]`).
The 200 ms debounce matches the daemon's, since editors write-then-rename and produce two events
(`04-storage.md` §3.1). Pattern (b) of [R] §1.5 is **not** used: per §1.1 there is no in-process
scheduler to drain.

### 4.2 Cadence

| Source | Interval | Primitive | Purpose |
| --- | --- | --- | --- |
| fsnotify batch | event-driven, 200 ms debounce | `p.Send` | authoritative change notification |
| `ClockTickMsg` | 1 s, clock-aligned | `tea.Every(time.Second, …)` | re-render countdowns and relative times only |
| `PollTickMsg` | 5 s | `tea.Tick(5*time.Second, …)` | watcher-failure fallback: re-stat, re-read if mtimes moved |

`tea.Every` truncates to the system clock so its first tick lands on the next wall-clock boundary
(`[BT commands.go:101-103]`); that is why it, not `tea.Tick`, drives the countdown. Both are
**one-shot**: the handler MUST return a fresh command or the cadence stops ([R] §1.4). A
`ClockTickMsg` MUST NOT trigger a file read.

### 4.3 What is re-read, what is cached, and torn reads

| Data | Re-read on | Cached |
| --- | --- | --- |
| `daemon.json` | `StoreChangedMsg` naming it; `PollTickMsg` | yes — the header renders from cache |
| `jobs.yaml` | `StoreChangedMsg` touching the config dir | yes — parsed and resolved once per change |
| `state.json`, `overrides.json` | `StoreChangedMsg` naming either | yes; re-resolving effective `enabled` (`03-job-model.md` §5) needs both plus `jobs.yaml` |
| `runs/<jobId>.jsonl` | `StoreChangedMsg` naming that file; entering Screen 2 or 3 | yes, per job, reduced by `runId`, last write wins (`04-storage.md` §5) |
| `logs/<jobId>/<runId>.log` | selecting a run; while following a live run | only the selected run |

A `StoreChangedMsg` MUST re-read only the files its `Paths` implicate: a change to
`runs/gitlab-poll.jsonl` MUST NOT reparse `jobs.yaml`. A run record is written twice, `running` then
terminal, so the TUI MUST reduce by `runId` and MUST render an unpartnered `running` record as
running (`04-storage.md` §5).

Because the TUI takes no locks a torn read is possible; `04-storage.md` §9 specifies the handling —
**parse failure, then one retry after 50 ms**. If the retry fails the model keeps its last good
snapshot, emits `TornReadMsg{Path, Attempt: 2}`, and the header shows a transient
`store unreadable: <basename>`. It MUST NOT render an empty list or open a modal.

## 5. Keymap

One `KeyMap` struct of `key.Binding` values built with
`key.NewBinding(key.WithKeys(...), key.WithHelp(...))` ([R] §4), implementing `ShortHelp()` and
`FullHelp()` and rendered by `help.Model.View(km)`; `table.KeyMap` is the worked example ([R] §4.2).

| Binding | Keys | Help text | Screens | Mouse equivalent |
| --- | --- | --- | --- | --- |
| `Up` / `Down` | `up`,`k` / `down`,`j` | `↑/↓ move` | 1, 2, 3 | wheel over the focused pane |
| `PageUp` / `PageDown` | `pgup`,`b` / `pgdown`,`f` | `pgup/pgdn page` | 1, 2, 3 | — |
| `Top` / `Bottom` | `home`,`g` / `end`,`G` | `g/G top/bottom` | 1, 2, 3 | — |
| `Open` | `enter`, `l`, `right` | `enter open` | 1, 2 | double-click a row |
| `Back` / `NextPane` | `esc`,`h`,`left` / `tab` | `esc back · tab pane` | 2, 3, modal / 1, 2, 3 | click `nav.back` or `modal.scrim` / click the pane |
| `ToggleEnabled` | `space` | `space enable/disable` | 1, 2 | click `row.<id>.toggle` / `btn.job.pause` |
| `RunNow` | `r` | `r run now` | 1, 2, 3 | click `row.<id>.run` / `btn.job.run` / `btn.rerun` |
| `Cancel` | `x` | `x cancel run` | 1, 2, 3 | — (destructive; no button) |
| `Delete` | `d` | `d delete` | 2 | click `btn.job.delete` |
| `Copy` / `Follow` | `y` / `F` | `y copy · F follow` | 3 | click `btn.copy` / — |
| `Filter` / `Reload` | `/` / `ctrl+r` | `/ filter · ctrl+r reload` | 1 / 1, 2, 3 | click the filter field / — |
| `MouseMode` | `m` | `m mouse: on` / `m mouse: off` | all | click `hdr.mouse`, only while mouse is on (§6.2) |
| `Help` | `?` | `? help` | all | click `pane.help` |
| `Confirm` | `enter`, `y` | `enter confirm` | modal | click `modal.confirm` |
| `Quit` | `q`, `ctrl+c` | `q quit` | all | — |

`ctrl+c` MUST be bound explicitly — in raw mode `^C` arrives as a key event, not a signal — and a
suspend binding MUST NOT be advertised, since `suspendSupported` is `false` on Windows ([R] §6.2).

**Help-bar rendering.** `pane.help` renders `help.Model.View(km)` after `SetWidth(m.width)`; short
help is one line, `?` expands to full help. Inapplicable bindings MUST be disabled with
`key.WithDisabled()` rather than omitted from `ShortHelp`, since `Enabled()` already excludes them
([R] §4.2). `Quit`, `Help`, and `MouseMode` always show.

## 6. Mouse mode toggle and copy

Enabling mouse reporting takes click-drag away from the terminal, so native selection-and-copy stops
working. This is not a Bubble Tea bug and not fixed: issue #162, opened 2021-11-25, is **still
open**, the maintainer calling it "a limitation of pretty much all terminals" (`[GH #162]`); alt
screen compounds it, with no scrollback to select from ([R] §2.4, §6.1).

### 6.1 Required behaviour

1. The model holds `mouseOn bool`, default `true`.
2. `View()` sets `v.MouseMode = tea.MouseModeCellMotion` when on and `tea.MouseModeNone` when off
   (§3). No command, no program option — that is the whole mechanism in v2 ([R] §2.4).
3. The renderer emits mode changes only when the field changes, so toggling every frame is cheap and
   idempotent, and both 1002 and 1003 are reset on exit so a mid-session change cannot leak
   (`[BT cursed_renderer.go:384-401]`, [R] §2.1).
4. Mode is `CellMotion`, never `AllMotion`: `AllMotion` delivers a message per crossed cell, each
   running `Update` → `View` → diff *and* the hit-test closure, and buys only hover ([R] §2.1).
5. The press-hold heuristic from the #162 thread MUST NOT be implemented: it consumes a drag's first
   click to decide it is a drag, making every interaction ambiguous ([R] §2.4).

### 6.2 The trap: an unclickable toggle

Once mouse mode is off, `hdr.mouse` cannot be clicked — no mouse events are reported, so no
`LayerHitMsg` is ever produced for it. The `m` binding is therefore **mandatory**; the help bar MUST
render state and key in **both** states (`m mouse: on`, `m mouse: off`), the off-state text being
the only thing that tells a user how to get back; and `MouseMode` MUST NOT be disabled in any screen
or modal state. It is the one binding always available.

### 6.3 Copy

Copying MUST NOT depend on the user disabling the mouse. v2 has first-class clipboard commands —
`tea.SetClipboard` (`[BT clipboard.go]`, [R] §2.4) — which `btn.copy` and `y` MUST use. Screen 3
copies the run's full log as loaded, or `outputExcerpt` if unread; Screen 2 copies the resolved job
record as JSON, byte-identical to `job get`'s `result.job`; Screen 1 copies the job `id`. Success
emits `ClipboardCopiedMsg{Bytes}`. Windows Terminal also restores selection under a held modifier
(`[MS]`, [R] §3.3), but that exists nowhere else and MUST NOT be presented as the answer.

## 7. Rendering rules

### 7.1 Measurement, CJK width, and resize

All measurement MUST use `lipgloss.Width`, `lipgloss.Height`, or `lipgloss.Size`; `len()`,
`len([]rune(s))`, and `fmt.Sprintf("%-20s", …)` over any string that can contain user text are
PROHIBITED, and padding MUST go through `Style.Width(n)` ([R] §5.3). `lipgloss.Width` ignores ANSI
sequences and measures grapheme clusters via `ansi.StringWidth`. For a Korean job name:

```
"일일보고 스케줄"  ->  runes: 8   bytes: 22   terminal cells: 15
```

([R] §5.3, computed there with `unicodedata.east_asian_width`.) `len` overstates by 14 and rune
count understates by 7, and herdr-cron's users write Korean job names. Layer bounds come from
`lipgloss.Width`/`Height`, so hit rectangles are CJK-correct for free; an X coordinate MUST NOT be
sliced into a string index — walk grapheme clusters. Chrome height MUST use `lipgloss.Height(...)`.

`tea.WindowSizeMsg` is `struct { Width, Height int }`, delivered at startup and on every resize **on
all three platforms**, and MUST NOT be special-cased for Windows, where `SIGWINCH` is absent but
ultraviolet converts `WINDOW_BUFFER_SIZE_EVENT` into a VT window-op sequence ([R] §3.4: "delivered
on all three platforms; do not special-case"). On it the model MUST recompute geometry — body height
is total minus measured chrome, `SetHeight` receiving the total including the header line (§1.5) —
and below 80 columns MUST collapse to one pane via `Column.Width = 0` ([R] §5.2).

### 7.2 Status colours

Every terminal status of `03-job-model.md` §6, plus the non-terminal `running`:

| Status | Colour role | Glyph | Note |
| --- | --- | --- | --- |
| `running` | accent (cyan), with a `spinner` | `◌` | the only animated element |
| `success` | success (green) | `✔` | — |
| `no_op` | muted (dim grey-green) | `·` | MUST be visually distinct from `success`, or a week of an agent job is 300 identical green rows (`03-job-model.md` §6) |
| `failure` | error (red) | `✖` | — |
| `timeout` | error (red), italic | `⏲` | a distinct cause of the same class of outcome |
| `blocked` | warning (yellow), bold | `⚑` | terminal, never retried, always notified (`03-job-model.md` §4.4, §4.6); MUST be the most prominent non-error state — the one outcome requiring a human |
| `skipped` | muted (grey) | `–` | `reason` (`overlap`, `limit_exceeded`, `disabled`, `catchup_capped`, `superseded`) MUST be shown adjacent, since answering "why did this not run at 03:00" is why these records exist (`03-job-model.md` §4.3) |
| `cancelled` | muted (grey), strikethrough | `⊘` | — |

Colour MUST never be the only carrier of meaning: each row also shows the status word as text, so
`--no-color`/`NO_COLOR` (`05-cli.md` §1.1) degrades cleanly. Emoji MUST NOT be used for status or
the §2.1 enabled glyphs — emoji cell width is terminal-dependent ([R] §5.3). A job at
`limits.maxConsecutiveFailures` renders `⊘` with `disabledReason: auto_failures` (`03-job-model.md`
§4.5).

### 7.3 Empty states

| Situation | Required rendering |
| --- | --- |
| `jobs.yaml` absent, or present with zero jobs | `no jobs.yaml at <path>` / `no jobs defined`, the resolved config root (`04-storage.md` §1), and the exact `herdr-cron job add` invocation |
| Filter matches nothing; job has no runs | `no jobs match "<query>"` with the filter still editable; `no runs yet` plus `first run <countdown>` when `nextRunAt` is set |
| Selected run has an empty log | `no output captured` — distinct from a log not yet read, which shows the `spinner` |

An empty state MUST NOT be an empty pane: a blank list is indistinguishable from a failed read
(§4.3).

### 7.4 Error states in the header — mandatory

**`configError`.** When `daemon.json`'s `configError` (`04-storage.md` §7) is non-null the header
MUST render, on its own line, in the error role:

```
! config error — jobs.yaml is not being scheduled.  [enter] details
```

Clicking it or pressing `enter` opens the modal (§2.4) with the message verbatim. The job list MUST
still render from the last parseable `jobs.yaml`, marked possibly stale.

**Stale or stopped daemon.** `herdr-cron status` classifies the daemon **running** (lock held,
`heartbeatAt` under 60 s old), **stale** (file exists, heartbeat older or pid gone — a crash), or
**stopped** (no lock) (`04-storage.md` §7). All three MUST render distinctly and MUST NOT be
collapsed, since a crash and a deliberate stop demand different actions: muted
`daemon running · pid <pid>`, `daemon STALE · last heartbeat <relative>` in the error role, and
`daemon stopped · nothing is scheduled` in the warning role. In both non-running states every
`nextRunAt` countdown MUST be dimmed (§1.1), and clicking `hdr.daemon` opens a modal naming the state
directory, the last heartbeat, and the `service install` / `daemon` invocations (`05-cli.md` §3.3).

## 8. Testing

Harness: `github.com/charmbracelet/x/exp/teatest/v2`, with `teatest.WithInitialTermSize(w, h)`,
`tea.WithWindowSize(w, h)`, and `tea.WithColorProfile(p)` as determinism levers. Mouse is testable
with no terminal: the concrete types are plain structs over `tea.Mouse` ([R] §7):

```go
tm := teatest.NewTestModel(t, newModel(fixtureStore(t)),
	teatest.WithInitialTermSize(100, 30))
tm.Send(tea.MouseClickMsg{X: 12, Y: 4, Button: tea.MouseLeft})
tm.Send(tea.MouseWheelMsg{X: 12, Y: 4, Button: tea.MouseWheelDown})
```

Each region below MUST have at least one test sending a synthetic mouse message at a cell inside it
and asserting on `tm.FinalModel(t)`; hit-test arithmetic is the highest-risk code here ([R] §8.4).

| Region | Assertion |
| --- | --- |
| `row.<jobID>`, and `row.run.<runId>` on Screens 2–3 | the table cursor moves to that row, and only that row |
| `row.<jobID>.toggle` | the override write is issued for that job; `jobs.yaml` is byte-identical afterwards |
| `row.<jobID>.run` | a trigger file with `action: "run"` and the correct `jobId`; none while the job is already `running` |
| `pane.jobs` / `pane.detail` wheel and click | the cursor moves by 3 and clamps; over the detail pane the viewport offset changes and the cursor does not; a click moves focus and the blurred `table` stops consuming `Up`/`Down` (§1.5) |
| `hdr.mouse` | `mouseOn` flips and the rendered view's `MouseMode` changes |
| `modal.scrim` vs `modal.body` | a click covered by both resolves to the modal; a click outside `modal.body` dismisses |
| `modal.confirm` / `btn.copy` / `nav.back` | the destructive action fires exactly once; `ClipboardCopiedMsg` carries the expected byte count; the stack pops one level |
| a row containing Korean text | the toggle and run hit rectangles are still correct — the §7.1 regression test |

Also required: row arithmetic with a scroll offset (scroll, then click; the resolved job MUST be the
visible one); a torn-read fixture failing once then succeeding MUST yield the populated list (§4.3);
three `daemon.json` fixtures MUST yield the three headers of §7.4; and a build-failing check for any
prohibited §1.4 identifier. Goldens record raw ANSI mode sequences, so they MUST never cover a view
that toggles `MouseMode`, and `teatest.WaitFor`'s 1 s default needs `teatest.WithDuration` ([R] §7).

## 9. Herdr plugin pane

**D4.** herdr-cron ships as a Herdr plugin as well as a standalone CLI (`README.md` D4), and the
manifest MAY declare the TUI as a `[[panes]]` surface. The pane command MUST be bare `herdr-cron`,
which launches the TUI on a TTY (`05-cli.md` §1), and MUST NOT be `herdr-cron daemon --foreground`,
which per §1.1 is the only surface that schedules. The TUI in a pane MUST behave identically to the
TUI in a terminal, nothing in §2–§7 being conditional on the host, and closing the pane MUST NOT
stop the schedule.

**UNVERIFIED RISK.** Whether Herdr forwards mouse events to a hosted pane at all is untested, and
Herdr's own mouse handling may conflict with a child program's mouse reporting. The research states
this was not tested and belongs to the Herdr document, not the Bubble Tea one ([R] "Could not
verify"); `README.md` carries the same risk and points here, as does any multiplexer in between,
including tmux with `set -g mouse off`.

**Required fallback.** herdr-cron MUST NOT depend on mouse events being delivered.

- Keyboard parity (§1.3) is what makes the fallback exist: with zero mouse events every affordance is
  still reachable and the help bar still lists every one.
- `HERDR_CRON_MOUSE=off` MUST start with `mouseOn = false`; the `[[panes]]` entry MAY set it.
- If no mouse message has arrived within 10 seconds of the first `tea.WindowSizeMsg` **and** the user
  has pressed at least one key, the footer MUST append `mouse: no events (m to retry)` — a hint,
  never a mode change. The manifest entry MUST document the toggle key and the variable, because a
  user whose pane swallows mouse events has no clickable way to learn either (§6.2).

## 10. Open points

1. **Herdr pane mouse forwarding is unverified** (§9). Until tested on a real Herdr pane on all three
   platforms the `[[panes]]` surface ships with the §9 fallback, documented as keyboard-first.
2. **Windows mouse delivery is inferred, not measured**: no byte stream was observed on conhost or
   Windows Terminal, and which of ultraviolet's two reader paths runs was never traced ([R] §3.5).
3. **The 400 ms double-click threshold is an invention** (§2.1); v2 has no double-click message and no
   configurable threshold ([R] "Could not verify"). If wrong it becomes a `config.toml` setting.
4. **`teatest` v2 requires an older Bubble Tea than we pin** — `charm.land/bubbletea/v2 v2.0.0`
   against v2.0.9, unverified ([R] "Could not verify"). The fallback is driving `Update` directly
   with synthetic messages, which covers every §8 region without the harness.
5. **In-TUI editing is deferred.** Open issue #1424 means mouse and alt screen may not return after
   `tea.ExecProcess` ([R] §3.3), so the TUI MUST NOT shell out to `$EDITOR`.
6. **`bubbles` signatures were read from `main`, not a tagged release** ([R] "Could not verify"); the
   pinned dependency MUST be the release, so drift is a build error rather than silent.
7. **Filter matching on Screen 1 is unspecified.** `bubbles/list`'s fuzzy filter is rejected (§1.4),
   so `/` is a `textinput` over the job table, matching however it likes but measuring with
   `lipgloss.Width` (§7.1).
8. **A cross-job history view is a linear scan** of every `runs/<jobId>.jsonl` (`04-storage.md` §5);
   the documented fix is an index file, not a database.
