---
title: herdr-cron — Specification index and decision record
date: 2026-09-02
status: spec (normative index)
---

# herdr-cron specification

`herdr-cron` schedules automated work for coding agents: shell commands and prompts to coding
agents running inside [Herdr](https://herdr.dev) panes. One Go binary, a JSON-first CLI for
agents, a mouse-driven TUI for humans, Linux/macOS/Windows.

This directory is normative. `docs/research/` is the evidence it rests on — every document there
is primary-source, pinned to a commit or a URL, and carries its own "Could not verify" section.
Where a spec makes a claim about a library, a binary, or an operating system, it cites the
research section that established it.

## Implementation status

The whole specification is implemented in this repository as of 2026-09-02. It was built to
prove the spec against a compiler, a filesystem, a live Herdr and a real terminal rather than
only against a reader.

| Shipped | Not yet |
| --- | --- |
| `validate` — all three schedule forms, timezones, whole-file validation with jitter applied, and the agent environment warnings of §7 level 4 | worktree isolation (`agent.worktree`) |
| `run-once <id>` for **both kinds** — timeout, overlap skip, daily limits, state watermark, JSONL history, log files | the `/` filter on the job list |
| `kind: agent`: adapter resolution, version gate, trust pre-flight, dedicated session, headless server start, host workspace, one tab per run, `agent start` / `prompt --wait` / `read`, outcome classification, pane cleanup | `job add --tag` filters beyond exact match |
| `job list / get / add / update / rm / pause / resume / run / cancel`, with `--dry-run`, `--purge`, `--wait` | retry backoff (`retry.max_attempts` is honoured as 1; multi-attempt scheduling is not wired) |
| `run list / get / logs`, with `--follow` and the raw-vs-`log_line` split | |
| `daemon [--foreground\|--detach]`, `status`, `reload` — gocron scheduler, catch-up pass, fsnotify + poll reload, trigger files, heartbeat, single-instance lock, panic listener, auto-disable, best-effort notifier | |
| The **mouse TUI** — Bubble Tea v2, alt screen, cell-motion mouse with an `m` toggle, three screens plus a confirm modal, hit rectangles for rows and per-row toggle and run affordances, wheel scrolling, clipboard copy, keyboard parity throughout | |
| `schema`, `completion`, `--skill`, `--version`, `install-cli --with-skill` | |
| `service install / uninstall / status` for both drivers, with marker-fenced systemd units, launchd plists and Task Scheduler entries, plus drift states `ok` / `stale` / `orphan` / `missing` | |
| The bundled Agent Skill: `skills/herdr-cron/SKILL.md` plus three references, `//go:embed`, and a test that fails on drift | |
| `herdr-plugin.toml` (build, idempotent `[[startup]]`, four global `[[actions]]`, a `[[panes]]` TUI surface) and `.goreleaser.yaml` for six targets, brew and scoop | |
| The JSON envelope, error codes, and the 0/1/2/3 exit split | |
| Comment-preserving, atomic, validated `jobs.yaml` writes; `overrides.json` under its lock | |
| Path resolution for all three OSes; all six release targets cross-compile with `CGO_ENABLED=0` | |

Verified by running, not by reading: a Korean comment and an inline `# :17, not :00` survive a
`job update`; an edit that would produce an invalid file leaves the original byte-identical; a
2-second timeout kills `sleep 30` with no orphan; two concurrent runs produce one execution and
one `skipped` / `overlap` record; `job resume` deletes the override rather than inverting it;
a job added while the daemon runs starts firing without a restart, and `job pause` stops it
live; twenty seconds of downtime on a five-second job produced **exactly one** `catchup` run,
not four; `kill -9` mid-run leaves a `running` record that the next start closes as
`failure` / `daemon_died`; two consecutive failures auto-disabled a job with reason
`auto_failures`. And the whole `kind: agent` path ran for real against a live Herdr in a
disposable session: server started headlessly, workspace and tab created, `claude` started and
prompted, transcript captured, pane closed, run recorded `success` in 7 s — while a job pointed
at an untrusted `cwd` was gated to `blocked` / `cwd_not_trusted` in 4 ms with nothing created.

The TUI was driven over a real PTY at 110×32 with real SGR mouse bytes, not only in tests: the
first frame requests the alt screen and cell-motion mouse (`ESC[?1049h`, `ESC[?1002h`,
`ESC[?1006h`); a click on a row's status glyph flipped `●` to `○` and wrote `overrides.json`
without touching `jobs.yaml`; two clicks on the same row within 400 ms pushed the detail screen;
`esc` popped back; `m` emitted `ESC[?1002l ESC[?1006l` and changed the badge to `mouse: off(m)`
while the footer kept advertising the key. A Korean job name renders at its correct cell width.

The distribution surface was exercised the same way. `herdr-cron --skill` is byte-identical to
`skills/herdr-cron/SKILL.md` (`diff` reports no difference, and a test fails the build if that
ever stops being true). `schema` returns 28 commands with root-relative paths, so
`schema --command "job add"` answers with that command's flags. `install-cli --dir … --with-skill`
symlinked the binary, installed all four skill files, and was a no-op on the second run.
`service install --driver os-scheduler` generated a marker-fenced systemd timer carrying
`OnCalendar=Mon..Fri *-*-* 03:17:00` and `Persistent=true`, **refused** the two schedules that
cannot be translated exactly, reported `stale` after a hand edit and `orphan` after the job left
`jobs.yaml`, refused to overwrite an unfenced file of the same name, and swept every owned unit
on uninstall. `herdr-plugin.toml` parses. A `-X main.version` build reports the stamped version,
and an unstamped build falls back to the release constant in `internal/cli/buildinfo.go` — which
is the case for every Herdr `[[build]]`, because Herdr clones without tags.

Nine spec corrections came out of writing it, and are already applied: the two distinct per-job
lock files ([`04-storage.md`](04-storage.md) §9), `validate --timezone`, the `validation`
payload's `scheduleType` / `jobs[]` split ([`05-cli.md`](05-cli.md) §2, §3.5), the `daemon
--detach` flag, `install-cli`, and the four contract errors below.

Six things the spec got wrong or did not say, learned by running:

1. **Daemon liveness cannot rest on the heartbeat alone.** A `kill -9` leaves a heartbeat that
   stays "fresh" for a minute, during which `status` lied and `daemon --detach` was a silent
   no-op. Liveness is now heartbeat freshness **and** `daemon.lock` being held; the kernel
   releases the lock when the process dies. Absorbed into `04-storage.md` §7.
2. **Cancelling a run must kill the process group, not the child.** `sh -c "sleep 20; echo x"`
   cannot exec in place, so the shell stays and `sleep` becomes a grandchild that survives the
   kill and holds the output pipe open — leaving the run recorded as `running` forever. The
   runner now kills the group and sets `WaitDelay`. Absorbed into `03-job-model.md` §3.1.
3. **`herdr agent read` is not enveloped.** The spec said the text is at `.result.read.text`; it
   is not. The command prints the raw snapshot on stdout and has no `--json` at all, so envelope
   decoding lost every transcript on the first live run. Corrected in
   [`07-herdr-integration.md`](07-herdr-integration.md) §3 step 8.
4. **The assistant marker must be matched at column 0.** claude's status footer carries its own
   `●` (`● high · /effort`, right-aligned), so the specified indentation-tolerant backwards scan
   returned the status bar as the agent's answer. Corrected in
   [`07-herdr-integration.md`](07-herdr-integration.md) §4.2, with a regression test built from
   the captured transcript.
5. **A lipgloss layer is opaque even when its content is only spaces**, so the specified
   "invisible hit layer" does not exist: the first TUI build rendered a completely blank screen,
   every pane painted over by its own hit rectangle. Hit testing is now a rectangle table the
   layout emits alongside the content — same ids, same z precedence, different data structure.
   Corrected in [`06-tui.md`](06-tui.md) §2.
6. **`bubbles/table` needs `SetWidth`.** Its internal viewport defaults to zero width, so
   `View()` returns the header line and nothing else — rows present, none visible — and columns
   must be budgeted at `Width + 2` each for the default cell padding. Corrected in
   [`06-tui.md`](06-tui.md) §2.

## Documents

| # | Document | Contents |
| --- | --- | --- |
| 01 | [`01-overview.md`](01-overview.md) | Problem, users, scope and non-goals, glossary, worked example |
| 02 | [`02-architecture.md`](02-architecture.md) | Components, the `run-once` core and three drivers, daemon lifecycle, OS-scheduler registration, failure model, sequences |
| 03 | [`03-job-model.md`](03-job-model.md) | Job and run schema, schedule syntax, catch-up, overlap, retry, limits, effective `enabled` |
| 04 | [`04-storage.md`](04-storage.md) | Roots per OS, on-disk layout, `jobs.yaml` write rules, `state.json`, JSONL history, logs, locking, the trigger protocol |
| 05 | [`05-cli.md`](05-cli.md) | Command surface, JSON envelope, error codes, exit codes, what works without a daemon |
| 06 | [`06-tui.md`](06-tui.md) | Screens, mouse affordances, message vocabulary, keymap, rendering, tests |
| 07 | [`07-herdr-integration.md`](07-herdr-integration.md) | Herdr adapter, session topology, agent-run sequence, outcome classification, trust pre-flight, worktrees, plugin manifest |
| 08 | [`08-agent-skill.md`](08-agent-skill.md) | The bundled Agent Skill: frontmatter, content, packaging, distribution, drift |

Read 01 first. 03, 04, and 05 are the contracts everything else is written against; when two
documents disagree, those three win, in that order.

## Decision record

Eight decisions, taken with the human on 2026-09-02 against the evidence in `docs/research/`.
Each records the alternatives and what choosing this one costs. None of them is a default that
fell out of an implementation.

### D1 — Architecture: `run-once` core with three interchangeable drivers

`herdr-cron run-once <job-id>` is the only thing that ever executes a job. Above it sit three
drivers: **`daemon`** (the default; a long-lived process owning a gocron scheduler),
**`foreground`** (`herdr-cron daemon --foreground`, typically in a Herdr pane), and
**`os-scheduler`** (systemd user timer / launchd LaunchAgent / Windows Task Scheduler, one entry
per job).

*Rejected:* any single one of the three on its own. The prior art ships all three and they
optimise different things — only OS registration answers "the laptop slept for six hours"
correctly and for free via systemd `Persistent=true`; only a daemon makes an agent's `job add`
take effect immediately; only the foreground process is trivially correct on Windows
(`docs/research/2026-09-02-prior-art-and-domain-model.md` Q7).

*Cost:* the largest v1 surface of the four options. Three service-installation paths to write and
test. Accepted because the alternative is picking a default that is wrong for a third of users
and unrevertible.

### D2 — Storage: `jobs.yaml` + JSONL history + `state.json`

Definitions in an authored, comment-preserving, git-committable YAML file. Run history in
append-only `runs/<jobId>.jsonl`. Mutable state — including the `enabled` override — in
`state.json`. No database.

*Evidence:* eight of eight surveyed systems keep definitions in files, including the most
feature-complete one (`prior-art` Q2). *Rejected:* SQLite for history — no scheduler in the
surveyed corpus chose it, and `modernc.org/sqlite` costs ~7 MB of binary
(`docs/research/2026-09-02-agent-skill-and-cli-ux.md` B6); bbolt — its exclusive process-wide
`flock` with a default infinite timeout would have forced an IPC design on us (ibid.).

*Cost:* a TUI filter across all history is a linear scan of a few hundred KiB. Acceptable at the
default retention; the fix, if ever needed, is an index file rather than a database.

The coupled sub-decision: **`enabled` is declared in YAML and overridden in state**
([`03-job-model.md`](03-job-model.md) §5), so a TUI toggle never rewrites a user's file, and a
hand edit to the file always wins back.

### D3 — Autonomy: balanced, Claude-Desktop-shaped

Catch-up defaults to `latest` — exactly one run for the most recently missed occurrence, within a
7-day window. `enabled` defaults to `true`. Three guardrails are mandatory rather than optional:
deterministic per-job jitter, `max_runs_per_day` (24 for agent jobs), and
`max_consecutive_failures: 3` auto-disable.

*Evidence:* the closest shipping analogue chose exactly this catch-up rule
(`prior-art` §1.6.2). Every schema in the corpus defaults `enabled: true`, while both hosted
products bolted on automatic *disable* mechanisms afterwards — so the guardrails, not the
default, are where the safety lives (`prior-art` Q8).

*Rejected:* `catchup: off` with `enabled: false` (a user files "the scheduler doesn't work");
`catchup: all` by default (opening a laptop after a weekend launches N agents into one repo).

### D4 — Packaging: one binary, two front doors

A Herdr plugin (`herdr-plugin.toml`, marketplace install, `[[startup]]` hook, `[[actions]]`,
`[[panes]]`, an `install-cli` action) **and** a standalone CLI (`go install`, goreleaser, brew,
scoop) that works with Herdr absent.

*Evidence:* both real third-party precedents examined converged on this independently —
`herdr-hitl` ships exactly this shape, and `herdr-reviewr` does the softer version
(`docs/research/2026-09-02-herdr-plugin-integration.md` "Implications"). A daemon is unavoidable
in every option anyway, because `[[startup]]` hooks must exit.

*Cost:* two installation stories to document and test, and a `[[startup]]` hook that must be
idempotent because it re-runs on every server start and every live handoff.

### D5 — v1 kinds: `shell` and `agent`

No chains, no DAGs, no pipelines. dagu's own README names the trap: *"You wanted to schedule some
jobs. Now you operate a second system"* (`prior-art` §1.1).

### D6 — IPC: files only

The daemon watches `jobs.yaml` and `<state>/triggers/` with fsnotify plus a 5-second stat-poll
fallback. Liveness is the `daemon.json` heartbeat plus `daemon.lock`. No sockets, no named pipes,
no TCP.

*Evidence:* Herdr's own documentation pushes plugins toward shelling out to its CLI rather than
speaking its socket, precisely because of the Unix-socket-vs-Windows-named-pipe split
(`herdr-plugin-integration` §2). Taking a socket would import that split into a project whose
whole point is that it runs on three platforms.

*Cost:* `job run --wait` notices completion by polling, roughly 100–300 ms later than a socket
would deliver it. Measured against one implementation instead of three, that is cheap.

### D7 — Agent jobs run in a dedicated Herdr session

Default `herdr --session herdr-cron`, overridable per job via `agent.session` (including
`current`).

*Evidence:* verified on 2026-09-02 that a headless server with no client attached runs the full
`agent start` → `prompt --wait` → `read` loop, and that a fresh session starts with zero
workspaces (`herdr-plugin-integration` §9). A dedicated session keeps 03:00 tabs out of the
human's workspace and guarantees the 120×40 headless geometry.

*Cost:* watching a run live requires `herdr session attach herdr-cron`.

### D8 — Reporting: always log, notify best-effort

Every run writes a log file and a JSONL record, always. On top of that a pluggable
`notify.command` — defaulting to `herdr notification show` — fires for the configured events and
its failure never changes a run's outcome.

*Evidence:* `notification show` provably returns
`{"shown": false, "reason": "no_foreground_client"}` on a headless server
(`herdr-plugin-integration` §9.5). A scheduler whose reporting depends on it would be silent
exactly when it matters.

## Deliberate inventions

Three things in this spec have no prior art behind them and are marked as inventions rather than
patterns, so a future reader knows to question them rather than assume they were copied:

1. **The money circuit breaker** (`max_consecutive_failures` auto-disable). The nearest analogues
   are GitHub's 60-day idle auto-disable and Claude Code `/loop`'s 7-day expiry
   (`prior-art` Q8).
2. **`blocked` as a terminal, never-retried outcome.** It follows from a verified failure mode
   (`herdr-plugin-integration` §9.4) but no surveyed scheduler models it.
3. **The trust pre-flight** ([`07-herdr-integration.md`](07-herdr-integration.md) §5).

## Known risks carried into implementation

From the research documents' "Could not verify" sections; none is resolved by this spec.

- **Windows and macOS are unverified.** Every non-Linux claim — service registration, mouse
  behaviour, headless `agent start`, timer behaviour across suspend — is read from source or
  documentation, not executed. Each must be exercised on the real platform before v1 ships.
- **A mouse TUI inside a Herdr `[[panes]]` surface** may conflict with Herdr's own mouse
  handling. Untested. [`06-tui.md`](06-tui.md) specifies the required fallback.
- **Bare `herdr server` semantics** with a controlling terminal (does it fork, where does it log)
  are unknown; the probe ran it supervised with piped stdio.
- **Agent kinds other than `claude`** were never started unattended. Their startup dialogs are
  unknown, and the trust pre-flight is specified per kind for that reason.

## Where the remaining open points live

Each document ends with its own open points; they are not duplicated here. This is the map.

| Document | Open points concern |
| --- | --- |
| [`01-overview.md`](01-overview.md) §7 | Six cross-document discrepancies found while writing it; five resolved on 2026-09-02 and recorded with their resolution, one left with the payload table as the tie-breaker. |
| [`02-architecture.md`](02-architecture.md) | systemd `OnCalendar=` grammar, launchd catch-up after wake, and the whole Windows Task Scheduler artefact are `[UNVERIFIED]` — outside the research corpus. Also: do not depend on gocron's `WithStartAtGrace`, which exists only at branch tip. |
| [`06-tui.md`](06-tui.md) §10 | Herdr pane mouse forwarding, Windows mouse delivery, the invented 400 ms double-click threshold, and whether `teatest` v2 works against the pinned Bubble Tea. |
| [`07-herdr-integration.md`](07-herdr-integration.md) §10 | Nine unverified Herdr behaviours, most importantly bare `herdr server` semantics and non-`claude` agent kinds unattended. |
| [`08-agent-skill.md`](08-agent-skill.md) | `--skill` ships no `references/`; the licence value is asserted, not chosen. |

### Reconciliations already applied

Contract-level items raised by those documents were absorbed into the normative three on
2026-09-02 rather than left open:

- `overrides.json` + `overrides.lock` split out of `state.json`, so `job pause` works with no
  daemon and a TUI toggle never rewrites `jobs.yaml` (`04-storage.md` §4, §9).
- "Run trigger" versus "trigger file" disambiguated (`04-storage.md` §8).
- Complete `result.type` payload table and the `cli:<command>` envelope id for ungrouped
  commands (`05-cli.md` §2).
- `run_failed` error code; exit 0 for `skipped`, so an `overlap` skip does not mark a systemd
  unit failed (`05-cli.md` §2.1, §2.2).
- `herdr-cron daemon --detach` for the Herdr `[[startup]]` hook, which must exit and must be
  idempotent across live handoff (`05-cli.md` §3.3).
- `herdr-cron install-cli`, the second front door of D4 (`05-cli.md` §3.4).
- `HERDR_CRON_TRIGGER` and the rest of the environment contract (`05-cli.md` §1.2).
- The single `reason` union across all run statuses (`03-job-model.md` §6).
- Writer attribution corrected from "daemon only" to "the executing scheduler process", because
  under the `os-scheduler` driver several `run-once` processes write concurrently
  (`04-storage.md` §5, §9).
