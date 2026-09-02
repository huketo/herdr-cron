# herdr-cron research index

Five research documents, all dated **2026-09-02**, all written against primary sources
(pinned repository SHAs, official docs, `--help` from binaries installed on this machine,
and — in `herdr-plugin-integration` §9 — commands actually executed against a disposable
Herdr session). Each document defines its own citation tags at the top and carries a
`## Could not verify` section.

Nothing here is a decision. The last section of this file lists what still has to be
decided by a human.

| Document | Owns | Lines |
| --- | --- | --- |
| [`2026-09-02-gocron-scheduling-engine.md`](2026-09-02-gocron-scheduling-engine.md) | The scheduling engine: gocron v2 API, cron parsing, timers, listeners, what gocron does *not* do | 1293 |
| [`2026-09-02-bubbletea-mouse-tui.md`](2026-09-02-bubbletea-mouse-tui.md) | The TUI: Bubble Tea v2, mouse input, hit testing, Windows, widgets, layout, tests | 1820 |
| [`2026-09-02-herdr-plugin-integration.md`](2026-09-02-herdr-plugin-integration.md) | The host: Herdr plugin contract, events, driving agents from a scheduler, and §9 — headless orchestration **executed** against a disposable session | 1921 |
| [`2026-09-02-agent-skill-and-cli-ux.md`](2026-09-02-agent-skill-and-cli-ux.md) | The interface: `SKILL.md` spec and packaging, CLI framework, JSON output, paths, service managers, persistence, release | 2063 |
| [`2026-09-02-prior-art-and-domain-model.md`](2026-09-02-prior-art-and-domain-model.md) | The problem: what already exists (dagu, supercronic, pueue, river, Claude Desktop tasks), and the job/catch-up/safety model | 992 |

## Read in this order

1. **prior-art-and-domain-model** — establishes what a "job" is and which questions are open.
2. **herdr-plugin-integration** — the host constrains the architecture more than any library does.
3. **gocron-scheduling-engine** — decides what the daemon must own.
4. **agent-skill-and-cli-ux** — the command surface and the on-disk layout.
5. **bubbletea-mouse-tui** — the last layer, and the one with the fewest external constraints.

## Findings that change the plan

These are the results that invalidate an obvious first guess. Each is cited in full in its
own document.

- **Bubble Tea v2 is released and stable** (`charm.land/bubbletea/v2` v2.0.9). Every v1 API
  a reader would reach for is gone: no `tea.WithMouseCellMotion`, no `tea.MouseMsg` struct,
  `View()` returns `tea.View`, and terminal modes are declarative fields on the view. Any
  tutorial found online is v1 and will not compile.
- **`bubbles/table` has no mouse support at all** — no row click, no wheel scroll. Exactly
  one file in the whole `bubbles` module handles a mouse message (`viewport`, wheel only).
  Mouse interaction is code herdr-cron writes, not a feature it enables.
- **Hit testing has a first-party answer now**: `lipgloss.Layer` + `Compositor.Hit` +
  `View.OnMouse`, which removes the reason to take `bubblezone` as a dependency.
- **Enabling mouse reporting breaks native click-drag-to-copy** (bubbletea issue #162, open
  since 2021). The mitigation is a mouse-mode toggle plus explicit copy buttons, and it has
  to be designed in, not bolted on.
- **gocron has zero persistence and zero catch-up.** Every missed tick is discarded by
  design; a job that should have fired 10 times while the machine slept fires once.
  Definitions, run history, and any catch-up logic are herdr-cron's own code.
- **A gocron panic kills the process** unless `AfterJobRunsWithPanic` is registered — the
  recover wrapper is conditional on that listener existing.
- **A daemon is unavoidable in every packaging option.** Herdr `[[startup]]` hooks must
  exit, so even a "plugin-only" herdr-cron has to spawn and supervise its own long-lived
  process. The real question is only *how the daemon gets started*.
- **Herdr drives agents headlessly — verified, not inferred** (`herdr-plugin-integration` §9).
  On a server started with no client ever attached, `agent start` reached
  `interactive_ready: true` in ~4 s, `agent prompt --wait` completed, and `agent read`
  returned the answer. The pane geometry was exactly the documented 120×40. Two caveats came
  out of the same run: *notifications* return `shown: false, reason: no_foreground_client`,
  and a fresh headless server has **zero** workspaces, so the scheduler must build its own.
- **The real unattended failure mode is a trust dialog, not detection** (§9.4). An agent
  started in a `cwd` the agent has never been trusted with returns `agent_not_ready` and sits
  on an approval prompt nobody can answer. herdr-cron needs a pre-flight trust check and must
  treat "blocked" as a terminal outcome that is never retried on a schedule.
- **`mattn/go-sqlite3` is disqualified** for a cross-platform tool: it builds with
  `CGO_ENABLED=0` and then fails at first query with a stub error. `modernc.org/sqlite`
  builds clean for all six release targets from one Linux machine, at a ~7 MB binary cost.
- **Job *definitions* live in files, unanimously** — eight of eight prior-art systems, including
  the most feature-complete one (dagu). Only run-history storage is genuinely open.
- **The closest shipping analogue is Claude Desktop scheduled tasks**, and it documents its
  missed-run rule: exactly one catch-up for the most recently missed time, 7-day lookback,
  notified, older occurrences discarded.
- **`herdr --skill` output is byte-identical to the installed `SKILL.md`** — a `//go:embed`
  pattern that makes skill/binary version skew impossible. Copy it.

## Contradictions between the documents

Left unresolved on purpose; each is a real design fork.

1. **Where run history lives.** The gocron document assumes a store the daemon owns and
   proposes the CLI reads it directly; the prior-art document lays out three options (JSONL,
   date-sharded tree, SQLite) and notes no scheduler in its corpus chose SQLite; the CLI-UX
   document sizes `modernc.org/sqlite` as viable and flags that **bbolt takes an exclusive
   process-wide flock**, which forces "daemon owns the DB, CLI talks IPC" if bbolt is chosen.
2. **Naming of the catch-up field.** The CLI-UX document proposes `--on-missed
   skip|run-once|catch-up`; the gocron document proposes `catch_up: skip|once|all`; dagu ships
   `catchup_window` + `overlap_policy`. Pick one vocabulary before writing either the schema
   or the skill.
3. **Default `enabled`.** Every schema in the prior-art corpus defaults to `true`; both hosted
   products (GitHub Actions, Claude Code) bolted on automatic *disable* mechanisms afterwards.
   The safety argument for defaulting agent jobs to `false` has no prior art behind it.

## Decisions required before implementation

From `2026-09-02-prior-art-and-domain-model.md` §"Decisions the human must make", plus the
packaging fork from `2026-09-02-herdr-plugin-integration.md`:

1. **Run-history storage**, and the coupled sub-question of whether `enabled` lives in the
   user's YAML (so the TUI rewrites their file on every toggle) or in the state store.
2. **Architecture**: always-on daemon, foreground process in a Herdr pane, or registration
   with the OS scheduler. Only OS registration answers "the laptop slept for six hours"
   correctly and for free (systemd `Persistent=true`); only the daemon makes an agent's
   `job add` take effect immediately. Making `herdr-cron run-once <id>` the primitive keeps
   the choice reversible but does not remove the need for a shipped default.
3. **Autonomy level**: missed-run policy and default `enabled`, which together decide what
   happens when a laptop lid opens after a weekend.
4. **Packaging**: Herdr plugin, standalone CLI, or one Go binary with both front doors. Both
   real precedents examined (`herdr-hitl`, `herdr-reviewr`) converged independently on both.

## Resolved by experiment on 2026-09-02

What was the top unknown when these documents were written is now executed evidence, recorded
in `2026-09-02-herdr-plugin-integration.md` §9 under the `[PROBE]` tag. It was run in a
disposable `herdr --session hcprobe` server that was stopped and deleted afterwards; the
default session was untouched.

- Unattended `herdr agent start` with **no client attached**: works, ~4 s, `interactive_ready:
  true`. `agent prompt --wait` → `agent read` completed the loop headlessly.
- `herdr server` on a session that never ran the TUI: starts, and creates no workspace at all.
- Headless geometry: 120×40, with the root pane getting 94×39 after the 26-column sidebar.
- `notification show` with no foreground client: `shown: false`.
- Non-agent (shell) jobs headlessly: `pane run` + `pane wait-output` work.

## Highest-risk unknowns that remain

Nothing below was verifiable from this machine; each needs a hands-on experiment before the
design that depends on it is locked.

- What bare `herdr server` does to a **controlling terminal** — whether it forks and where it
  logs. §9 ran it supervised with piped stdio, which does not answer this.
- Whether a mouse-driven TUI works inside a Herdr **plugin pane** — whether Herdr's own mouse
  handling conflicts with the child program's mouse reporting.
- Windows and macOS, empirically: mouse clicks with the alt screen active on Windows Terminal,
  timer/clock behaviour across suspend, and whether headless `agent start` behaves the same.
  Every non-Linux claim in these documents is read from source or issue trackers, not executed.
- Behaviour across a server restart, a live handoff, or machine suspend — the three events a
  scheduler will actually meet in production.
- Whether agent kinds other than `claude` (21 more are accepted by `--kind`) start cleanly
  unattended, and what their own startup dialogs look like.
