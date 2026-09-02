---
title: herdr-cron — Overview
date: 2026-09-02
status: spec (normative)
---

# Overview

Normative. RFC 2119 keywords. This document orients: what herdr-cron is, who it is for, what it
does in v1, and what it deliberately refuses to do. It defines no schema. The schemas are in
[`03-job-model.md`](03-job-model.md), [`04-storage.md`](04-storage.md) and
[`05-cli.md`](05-cli.md); the decisions D1–D8 it implements are recorded in
[`README.md`](README.md).

**One sentence:** herdr-cron is a single cross-platform Go binary that schedules two kinds of
unattended work — shell commands and prompts to coding agents running in Herdr panes — where a
coding agent drives it through a JSON CLI and a human inspects it through a mouse-driven TUI.

---

## 1. Problem

### 1.1 What already exists

The agent-scheduling space is not empty. Five shipping systems solve overlapping parts of this
problem; all five were read from primary sources and are surveyed in
`docs/research/2026-09-02-prior-art-and-domain-model.md` §1.6 (and §1.1 for dagu).

**Claude Code `/loop` scheduled tasks** (`prior-art` §1.6.1). Three model-facing tools —
`CronCreate`, `CronList`, `CronDelete` — with 5-field cron expressions, a cap of 50 tasks per
session, and deterministic task-ID-derived jitter of up to 30 minutes. Its own documentation
states the two limits that disqualify it here: *"No catch-up for missed fires"*, and recurring
tasks *"automatically expire 7 days after creation"*. Tasks live in a Claude Code session and
fire between turns; close the session and nothing fires. It is a loop inside one agent's
conversation, not a scheduler for a machine.

**Claude Desktop scheduled tasks** (`prior-art` §1.6.2). The closest shipping analogue to
herdr-cron in every dimension: local machine, sleeps, fresh agent session per fire, costs money,
mutates a repo. It contributes the missed-run rule this project copies wholesale —
*"Desktop checks whether each task missed any runs in the last seven days... starts exactly one
catch-up run for the most recently missed time and discards anything older"* — plus per-run skip
reasons, deterministic per-task stagger, and an optional git worktree per run. What it is not:
scriptable. It is a desktop GUI product; the only programmatic surface documented is an
`update_scheduled_task` MCP tool a running task can call on itself. It also splits its storage
the other way from a general scheduler — the prompt is a `SKILL.md` on disk, while
*"Schedule, folder, model, and enabled state are not in this file"*. There is no CLI for an agent
to drive, no run-history JSON to read, no Windows/Linux story, and no shell jobs.

**dagu `harness.run`** (`prior-art` §1.1). The honest answer here is that dagu already does most
of what herdr-cron does, and more. It is a single Go binary on Linux/macOS/Windows with no
external DBMS, cron with timezones, `catchup_window` + `overlap_policy` (`skip|all|latest`),
per-step `retry_policy`, `handler_on.failure`, a JSONL run tree under `DataDir`, an audit
package, a REST API, an MCP server, and a `harness.run` action with built-in adapters that
invoke `claude -p "<prompt>"`, `codex exec "<prompt>"`, `gemini -p "<prompt>"` and eleven more.
For a user who wants scheduled agent work and is willing to operate a workflow engine, dagu is a
complete answer today.

Three things remain. First, dagu's harness invocation is fire-and-forget CLI: it shells out and
takes stdout. Its one managed-session integration (OpenCode) documents the failure mode
*"If the owning worker or OpenCode server disappears, the step remains waiting"* — dagu has no
notion of a terminal multiplexer whose panes outlive attachment. Second, dagu's catch-up replays
*every* missed occurrence in the window, capped at 1000 — correct for `ffmpeg` and `dbt`, a bill
for LLM calls (`prior-art` Q3). Third, dagu's surfaces are a Web UI and a REST API; there is no
TUI, and its front door is a server you run. dagu's own README names the cost of adopting it,
and the warning is quoted in `prior-art` Q1(c) from `dagu README.md:58 @ 86fe7e3`:

> *"You wanted to schedule some jobs. Now you operate a second system."*

**`@dortort/scheduler`** (`prior-art` §1.6.3). A Claude Code plugin that registers jobs with the
OS scheduler — launchd on macOS, marker-fenced crontab blocks on Linux — with a complete job
schema (`id`, `enabled`, `trigger`, `execution`, `tags`, timestamps), JSONL execution history
with 22 fields, a `BLOCKED_ENV_VARS` denylist, and worktree isolation. Its README states its
requirements as *"macOS (launchd) or Linux (crontab)"*: **no Windows at all**. Its `enabled`
lives in the definition file, so any UI toggle rewrites the user's JSON.

**`agent-cron`** (`prior-art` §1.6.4). Markdown files with YAML frontmatter as job definitions —
`cron`, `agent`, `skills`, and the prompt as the body — run by a foreground process through a
global serial queue with slug dedup. It contributes the `HEARTBEAT_OK` no-op protocol this spec
adopts as `no_op_marker`. It has no catch-up, no per-job concurrency, no persistence beyond
per-task per-day log files, and it dies with its terminal. `biosphere-labs/claude-code-scheduler`
(§1.6.5) adds one durable idea — a standing *"you're being controlled by a scheduler and there is
no user"* preamble — and a hardcoded `MAX_CONCURRENT_JOBS = 2`.

### 1.2 The gap

Stated precisely, and only what is actually missing:

1. **No system schedules work into a terminal multiplexer's long-lived panes.** Herdr sessions
   survive detachment, run agents headlessly, and expose them through a CLI. Verified on
   2026-09-02 against herdr 0.8.2 with no client ever attached: `agent start` reaches
   `interactive_ready: true` in ~4 s, `agent prompt --wait` and `agent read` complete the loop,
   and `pane run` + `pane wait-output` cover shell work
   (`docs/research/2026-09-02-herdr-plugin-integration.md` §9.3, §9.5). That is a managed-session
   execution substrate nobody schedules onto.
2. **No system is designed for a coding agent as its primary caller.** dagu has an MCP server and
   a skill; Claude Desktop has one self-modification tool; `@dortort/scheduler` has slash
   commands. None of them is a JSON-first CLI with a stable error-code vocabulary, a
   `validate --schedule` dry run, a self-describing `schema` command, and an embedded
   `SKILL.md` — the surface specified in [`05-cli.md`](05-cli.md) and
   [`08-agent-skill.md`](08-agent-skill.md).
3. **No system pairs that with a mouse-driven TUI.** dagu has a Web UI; the rest have nothing.
   Reading run history is a human act, and a web server is a heavier answer than a terminal
   program in a repo the human already has open.
4. **Cross-platform is claimed rarely and delivered rarely.** dagu delivers it; the
   agent-specific tools do not — `@dortort/scheduler` is macOS/Linux only, Claude Desktop is a
   desktop app, agent-cron inherits Node and launchd/pm2 advice.
5. **The unattended failure mode nobody handles is a trust dialog.** An agent started in a `cwd`
   it has never been trusted with returns `agent_not_ready` and sits on an approval prompt
   forever (`herdr-plugin-integration` §9.4). Claude Desktop documents the same class of stall
   (*"the run stalls until you approve it"*) and offers no automated answer. herdr-cron makes
   `blocked` a first-class terminal outcome that is never retried and always notified
   ([`03-job-model.md`](03-job-model.md) §6).

herdr-cron is therefore not "a scheduler that does not exist". It is the intersection that does
not exist: **Herdr-native execution, agent-first control, human-first inspection, one binary, no
server.**

---

## 2. Two users, one product

herdr-cron has exactly two users and they never share a screen.

**The agent** drives the CLI. It reads `result` objects, branches on `error.code`, never parses a
table, and never sees the TUI. Bare `herdr-cron` with no TTY on stdout is a usage error (exit 2)
precisely so an agent cannot hang a pipe on a full-screen program ([`05-cli.md`](05-cli.md) §1).
Its contract is [`05-cli.md`](05-cli.md) §5, taught by [`08-agent-skill.md`](08-agent-skill.md).

**The human** drives the TUI with a mouse. They review history, read a transcript, toggle a job,
and leave. Their contract is [`06-tui.md`](06-tui.md).

### 2.1 The parity rule

> **Every capability MUST be reachable from both surfaces.** A mutation the TUI can perform MUST
> have a CLI command with the same effect, and a CLI mutation MUST be performable in the TUI. A
> data command's `result` MUST contain everything the corresponding TUI view renders.

Consequences, all normative:

- The TUI MUST NOT own state. It is a client of the same file formats and the same code paths
  ([`04-storage.md`](04-storage.md) §9: read-only clients take no locks).
- `job pause` in the CLI and the pause toggle in the TUI MUST write the same `state.json`
  override, with the same `reason: "manual"` ([`04-storage.md`](04-storage.md) §4).
- `job get` MUST return the full resolved record plus `nextRuns` and `recentRuns` in one call,
  *because* a TUI opening a job must not need four round trips ([`05-cli.md`](05-cli.md) §3.1).
  That requirement exists for the human and is delivered to the agent for free — which is the
  parity rule paying for itself.
- Presentation is exempt. Colour, layout, mouse hit-testing and the log viewport are TUI-only;
  `--follow` streaming and shell completions are CLI-only. Parity is over *capabilities*, not
  over pixels.

Anything that serves neither user is out of scope. That test is what §3.2 is derived from.

---

## 3. Scope

### 3.1 What v1 does

1. **Schedules two job kinds and only two**: `kind: shell` (a direct child process of the
   runner) and `kind: agent` (a prompt driven into a Herdr pane). D5;
   [`03-job-model.md`](03-job-model.md) §3.
2. **Accepts three schedule forms** — `cron` (5 or 6 fields, descriptors, no `@reboot`),
   `every` (fixed interval), and `at` (a one-time instant) — each with a timezone
   ([`03-job-model.md`](03-job-model.md) §2).
3. **Executes through one primitive.** `herdr-cron run-once <job-id>` performs exactly one run,
   synchronously, in the calling process, with no daemon. D1;
   [`02-architecture.md`](02-architecture.md) §2.
4. **Ships three interchangeable drivers over that primitive**: `daemon` (default, a long-lived
   process owning a gocron scheduler), `foreground` (`herdr-cron daemon --foreground` in a Herdr
   pane), and `os-scheduler` (one systemd user timer / launchd LaunchAgent / Windows Scheduled
   Task per job, each exec'ing `run-once`). D1; [`02-architecture.md`](02-architecture.md) §4.
5. **Stores definitions in `jobs.yaml`** — authored, comment-preserving, diffable,
   git-committable — with run history in `runs/<jobId>.jsonl` and mutable state in `state.json`.
   No database. D2; [`04-storage.md`](04-storage.md) §2.
6. **Catches up conservatively.** Default `catchup: latest`: exactly one run for the most
   recently missed occurrence within a 7-day window, `off` and `all` available per job. D3;
   [`03-job-model.md`](03-job-model.md) §4.1.
7. **Guards spend by default**: deterministic per-job jitter, `max_runs_per_day` (24 for agent
   jobs, 0 for shell), and `max_consecutive_failures: 3` auto-disable. D3;
   [`03-job-model.md`](03-job-model.md) §2.1, §4.5.
8. **Records every occurrence, including the ones that did not run.** `skipped` with a reason of
   `overlap`, `limit_exceeded`, `disabled`, `catchup_capped` or `superseded` is a real run record
   ([`03-job-model.md`](03-job-model.md) §6). "Why did my job not run at 03:00" is answerable —
   the single most common complaint about cron (`prior-art` Q8).
9. **Talks to itself with files only.** `jobs.yaml` and `<state>/triggers/` watched by fsnotify
   with a 5-second stat-poll fallback; liveness is the `daemon.json` heartbeat plus
   `daemon.lock`. No sockets, no named pipes, no TCP. D6;
   [`04-storage.md`](04-storage.md) §3.1, §7, §8.
10. **Runs agent jobs in a dedicated Herdr session**, `herdr-cron` by default, overridable per
    job via `agent.session`, because a fresh headless server has zero workspaces and the
    scheduler must build its own topology (`herdr-plugin-integration` §9.1). D7;
    [`07-herdr-integration.md`](07-herdr-integration.md).
11. **Always writes a log file and a JSONL record for every run**, and *additionally* fires a
    best-effort `notify.command` whose failure never changes the run's outcome. On a headless
    server `herdr notification show` provably returns
    `{"shown": false, "reason": "no_foreground_client"}` (`herdr-plugin-integration` §9.5), so
    the notifier can never be the record of truth. D8;
    [`03-job-model.md`](03-job-model.md) §4.6.
12. **Presents two front doors from one binary**: a Herdr plugin (`herdr-plugin.toml`,
    marketplace install, a one-shot `[[startup]]` hook, `[[actions]]`, `[[panes]]`, and an
    `install-cli` action symlinking the binary onto `PATH`) and a standalone CLI
    (`go install`, goreleaser archives for six targets, a Homebrew cask, a Scoop manifest) that
    works with Herdr absent. `herdr-hitl` ships exactly this shape and is the precedent
    (`herdr-plugin-integration` §8, "The closest precedent is not reviewr"). D4.
13. **Ships its own Agent Skill inside the binary.** `herdr-cron --skill` prints the bundled
    `SKILL.md`, byte-identical to the installed copy — the pattern verified for `herdr --skill`
    (`docs/research/2026-09-02-agent-skill-and-cli-ux.md` A7), which makes skill/binary version
    skew impossible.
14. **Runs on Linux, macOS and Windows** from one codebase, with per-OS behaviour confined to
    root resolution ([`04-storage.md`](04-storage.md) §1), the child-process group split, and the
    `os-scheduler` driver's three backends.

### 3.2 Non-goals

Each with the reason it is refused. A non-goal is a commitment, not an omission: v1 MUST NOT
ship a partial version of any of these.

**No DAG or pipeline orchestration.** No `steps:`, no `depends:`, no `--after`, no fan-out. A job
is one shell command or one prompt. The moment a scheduler grows dependencies it becomes a
workflow engine with a queue, a lease model and a failure-propagation policy, and dagu — which
did grow all of them, well — states the price in its own README (`prior-art` Q1(c), quoting
`dagu README.md:58 @ 86fe7e3`): *"You wanted to schedule some jobs. Now you operate a second
system."* Users who need a DAG should run dagu; herdr-cron's `shell` kind can invoke it.
(D5.)

**No distributed or multi-machine scheduling.** Every root is machine-local
([`04-storage.md`](04-storage.md) §1), the single-instance guard is a local advisory file lock
(§7), and the trigger protocol claims work by `rename` on one filesystem (§8). Distribution
would require a lock service, clock agreement, and a way to run an agent on a machine whose Herdr
session you do not own. The run record carries `host` so history stays honest when a `jobs.yaml`
is shared over git and two machines each run their own scheduler — but they do not coordinate,
and v1 does not pretend they do.

**No cost or token accounting.** No `cost`, no `usd`, no token counts, anywhere. Only
`agent-cron` in the surveyed corpus models it (`RunResult{cost, inputTokens, outputTokens}`,
`prior-art` §1.6.4), and even there the research could not verify it is populated for all runners
(`prior-art` "Could not verify"). herdr-cron's agent output arrives as a pane transcript via
`agent read --source recent-unwrapped` — text, not a usage record — so the number is not
observable through the surface this product has
([`03-job-model.md`](03-job-model.md) §6). A field the tool cannot populate is worse than a
missing field, because a dashboard will show it as zero.

**No web UI.** dagu ships one ("Cockpit") plus a REST API; that is a server, a port, a bind
address, an auth story, and an asset pipeline. herdr-cron's two users are an agent that wants
JSON on stdout and a human who already has a terminal open. A web UI serves neither better than
what §2 specifies.

**No remote API.** No HTTP endpoint, no gRPC, no socket, not even a local one. This is D6 stated
as a boundary rather than as an implementation detail: the entire CLI-to-daemon channel is a file
written into `<state>/triggers/` and claimed by `rename`
([`04-storage.md`](04-storage.md) §8). The cost is real and is documented there — roughly
100–300 ms of polling latency for `job run --wait` — and it is paid to get one implementation for
three operating systems instead of a Unix-socket branch and a named-pipe branch. Herdr's own
plugin documentation pushes plugins toward shelling out to the CLI for the same reason
(`herdr-plugin-integration` §2).

---

## 4. Glossary

Consistent with [`03-job-model.md`](03-job-model.md); that document is authoritative for every
field name used here.

**Job** — a declared intent to run something on a schedule, identified by a stable `id` matching
`^[a-z0-9][a-z0-9._-]{0,127}$` and authored in `jobs.yaml`.

**Run** — one execution of a job, recorded as its own JSON object with its own `runId`, appended
twice to `runs/<jobId>.jsonl`: once as `running`, once with a terminal status.

**Occurrence** — an instant at which a job's schedule says it should fire. Every occurrence
produces a run record even when nothing executes; an occurrence that is dropped is a run with
status `skipped` and a `reason`.

**Trigger** — the provenance of a run, recorded in the run record as `scheduler`, `manual`,
`catchup`, `retry` or `startup`. (The same word names the trigger *file* the CLI writes into
`<state>/triggers/` to ask the daemon for work — [`04-storage.md`](04-storage.md) §8. Two
meanings, one word; see §7.)

**Driver** — how `run-once` gets invoked: `daemon`, `foreground`, or `os-scheduler`. Drivers are
interchangeable and change no job semantics ([`02-architecture.md`](02-architecture.md)).

**Catch-up** — what happens to occurrences that passed while nothing was running. Per job:
`off`, `latest` (default — exactly one run for the most recently missed occurrence), or `all`,
each bounded by `catchup_window` (default 168h).

**Overlap** — the previous run of a job is still going when the next occurrence arrives. Resolved
per job by `concurrency`: `skip` (default), `queue`, `cancel_previous`, or `allow`.

**Terminal outcome** — the run's final `status`: `success`, `no_op`, `failure`, `timeout`,
`blocked`, `skipped`, or `cancelled`. `running` is the only non-terminal status. The terminal
outcome is a different field from the run's captured output, and the two are never conflated.

**Blocked** — terminal, and specific: the agent is sitting on an approval or question UI that
nobody will answer. It is never retried, it always notifies, it increments
`consecutiveFailures`, and it maps to CLI exit code 3 ([`05-cli.md`](05-cli.md) §2.2).

**No-op** — terminal and successful: the run completed and correctly did nothing, detected by the
captured output equalling the job's `no_op_marker` exactly. Without it, a healthy week of agent
history is 300 identical green rows.

**Front door** — one of the two supported ways to obtain and launch herdr-cron: the Herdr plugin
or the standalone CLI. Both are the same binary and the same on-disk state (D4).

---

## 5. The safety position

D3 as a product stance:

> An unattended scheduler that prompts a coding agent spends money and mutates repositories while
> nobody is watching. herdr-cron therefore ships **balanced autonomy**: it behaves like every
> other scheduler on the happy path — `enabled` defaults to `true`, a job you add runs — and it
> bounds the unhappy path with guardrails that are on by default and cannot be forgotten.

The shape is Claude Desktop's, because Claude Desktop is the only shipping product with the same
risk profile (`prior-art` §1.6.2, Q3). The money argument is stated in `prior-art` Q8 and Q5, and
it is asymmetric: with `catchup: all` and `enabled: true`, opening a laptop lid after a weekend
can start N agent runs in one repository; with `catchup: off` and `enabled: false`, a user files
a bug saying the scheduler does not work. `off` under-delivers quietly; `all` over-delivers
expensively. Defaulting to `latest` is the position between them, and it is the one the evidence
supports.

Consequences that fall out of the stance, each already normative elsewhere:

- **`max_attempts` defaults to 1, not 25.** river's default of 25 is calibrated for a webhook
  delivery that costs nothing; an LLM invocation is not that (`prior-art` §1.5, Q5;
  [`03-job-model.md`](03-job-model.md) §4.4).
- **Retries are refused outright** for `blocked`, `no_op`, `skipped`, `cancelled`, `cwd_missing`
  and `limit_exceeded`. Retrying a job stuck on a human approval dialog burns the daily limit and
  changes nothing.
- **Dropped occurrences are recorded, never silently discarded** — supercronic warns per missed
  occurrence, Claude Desktop stores per-run skip reasons; herdr-cron writes a run record
  (`prior-art` Q3, Q8).

### 5.1 The three mechanisms

1. **Deterministic per-job jitter** ([`03-job-model.md`](03-job-model.md) §2.1).
   `offset = FNV1a64(job.id) mod min(interval/2, 30m)`, stable for a job id, applied to
   `scheduler` and `catchup` triggers and never to `manual`. It exists so that six agent jobs at
   `0 9 * * *` do not launch six agents into the same repository in the same second. Grounded
   three times over: Claude Code's task-ID-derived offset, Claude Desktop's deterministic stagger,
   and systemd's `RandomizedDelaySec=` with `FixedRandomDelay=` (`prior-art` Q8).
2. **`max_runs_per_day`** ([`03-job-model.md`](03-job-model.md) §4.5). Default 24 for
   `kind: agent`, 0 (unlimited) for `kind: shell` — a shell job is nearly free, an agent job is
   not. It is the only guard that holds across a catch-up storm, a manual `job run`, and a retry
   loop *simultaneously*; the closest prior art is river's `UniqueOpts.ByPeriod`, which makes
   at-most-one-per-period a data invariant rather than a scheduler invariant (`prior-art` Q8).
3. **`max_consecutive_failures` auto-disable** ([`03-job-model.md`](03-job-model.md) §4.5).
   Default 3, counting `failure`, `timeout` and `blocked`. On trip it sets an `enabled` override
   to `false` with `disabledReason: auto_failures`, writes a run record saying so, and notifies;
   `job resume` clears it. This has **no direct prior art** and is marked in `03-job-model.md` as
   a deliberate invention. The nearest analogues are GitHub Actions auto-disabling scheduled
   workflows in **public** repositories after 60 days with no repository activity, and Claude
   Code `/loop`'s 7-day expiry
   (`prior-art` §1.7, Q8) — the same instinct, that an unattended recurring thing must eventually
   stop itself, applied to a different trigger.

### 5.2 The fourth mechanism: the preamble

Every `kind: agent` run has this prepended verbatim, not configurable, not optional
([`03-job-model.md`](03-job-model.md) §3.3):

```
You are being run by herdr-cron on a schedule. There is no human watching this session.
Do not ask questions; if a required detail is missing, make the safest reasonable assumption
or stop and explain what was missing. Do not wait for approval. When you are done, state the
outcome in one line.
```

It is a product feature rather than each user's responsibility because its absence is the
documented cause of a scheduled agent stalling forever on a question. `ccs-biosphere` makes
exactly this its first prompt step (`prior-art` §1.6.5); Claude Desktop documents the failure it
prevents (*"the run stalls until you approve it"*, `prior-art` §1.6.2).

---

## 6. A worked example

One story, end to end, with the commands and envelopes of [`05-cli.md`](05-cli.md). Payload
shapes marked *(illustrative)* are consistent with §2's envelope but are not fixed by that
document; see §7.

### 6.1 The agent sets up a weekday dependency audit

The human says: *"audit our dependencies every weekday before I get in."* The agent's first move
is to check the schedule before writing anything — [`05-cli.md`](05-cli.md) §5 rule 4.

```console
$ herdr-cron validate --schedule "17 3 * * 1-5" --next 5
```

```json
{"id": "cli:validate",
 "result": {"type": "schedule_validation", "valid": true, "form": "cron",
            "expression": "17 3 * * 1-5", "timezone": "Asia/Seoul",
            "nextRuns": ["2026-09-03T03:17:00+09:00", "2026-09-04T03:17:00+09:00",
                         "2026-09-07T03:17:00+09:00", "2026-09-08T03:17:00+09:00",
                         "2026-09-09T03:17:00+09:00"]}}
```

*(illustrative payload)* Exit 0. Note `:17` rather than `:00`, and note that the five instants
are the raw occurrences — jitter is applied at fire time and reported separately as `nextRunAt`.

Then a dry run of the write:

```console
$ herdr-cron job add --id nightly-deps --name "Nightly dependency audit" \
    --schedule "17 3 * * 1-5" --timezone Asia/Seoul \
    --prompt "Audit dependencies in this repo. If everything is current, reply with exactly HEARTBEAT_OK and stop." \
    --no-op-marker HEARTBEAT_OK --cwd ~/src/herdr --tag maintenance \
    --timeout 45m --max-runs-per-day 4 --dry-run
```

`--dry-run` validates, prints the resolved job plus its next five fire times, and writes nothing
([`05-cli.md`](05-cli.md) §3.1). `--prompt` implies `kind: agent`; supplying `--command` as well
would be a `usage` error, exit 2. The agent re-runs the same line without `--dry-run`, and
`jobs.yaml` gains the job with its comments and key order intact
([`04-storage.md`](04-storage.md) §3).

### 6.2 Testing it once

```console
$ herdr-cron job run nightly-deps --wait
```

```json
{"id": "cli:job:run",
 "error": {"code": "daemon_unreachable",
           "message": "no daemon claimed the trigger within 3s",
           "hint": "run `herdr-cron service install --driver daemon --now`, or use `run-once`"}}
```

Exit 1. `job run` is one of the three commands that need a daemon
([`05-cli.md`](05-cli.md) §4); nothing has installed one yet. The agent branches on
`error.code`, not on the message:

```console
$ herdr-cron service install --driver daemon --now
$ herdr-cron job run nightly-deps --wait
```

```json
{"id": "cli:job:run",
 "result": {"type": "run",
   "run": {"runId": "nightly-deps-20260902T024107Z-m", "jobId": "nightly-deps",
           "trigger": "manual", "attempt": 1, "scheduledAt": null,
           "startedAt": "2026-09-02T11:41:07+09:00", "finishedAt": "2026-09-02T11:47:55+09:00",
           "durationSec": 408, "status": "no_op", "exitCode": 0, "reason": null,
           "logPath": "logs/nightly-deps/nightly-deps-20260902T024107Z-m.log",
           "outputExcerpt": "HEARTBEAT_OK", "host": "huke-desktop",
           "herdr": {"session": "herdr-cron", "paneId": "w1:p2",
                     "agentName": "cron-nightly-deps"}}}}
```

*(illustrative payload; the run record itself is normative —
[`03-job-model.md`](03-job-model.md) §6.)* Exit **0**: with `--wait`, exit 0 covers `success`
*and* `no_op` ([`05-cli.md`](05-cli.md) §2.2). The run id ends in `-m` because a manual run has no
scheduled instant and uses its invocation time. `status: "no_op"` is the useful part — the agent
learns the dependencies were already current, the marker round-tripped, and the job is wired
correctly. Had the run come back `blocked`, the exit code would have been **3**, and rule 5 of
[`05-cli.md`](05-cli.md) §5 tells the agent to stop and escalate rather than retry.

The agent confirms the schedule is live and stops:

```console
$ herdr-cron job list --tag maintenance
```

```json
{"id": "cli:job:list",
 "result": {"type": "job_list", "generatedAt": "2026-09-02T11:48:10+09:00",
   "daemon": {"status": "running", "pid": 40211},
   "jobs": [{"id": "nightly-deps", "name": "Nightly dependency audit", "kind": "agent",
             "enabled": true, "enabledSource": "file",
             "schedule": {"type": "cron", "expression": "17 3 * * 1-5",
                          "timezone": "Asia/Seoul"},
             "tags": ["maintenance"], "nextRunAt": "2026-09-03T03:29:23+09:00",
             "lastRun": {"runId": "nightly-deps-20260902T024107Z-m", "status": "no_op",
                         "finishedAt": "2026-09-02T11:47:55+09:00", "durationSec": 408},
             "consecutiveFailures": 0}]}}
```

This payload is verbatim the shape of [`05-cli.md`](05-cli.md) §3.1. `nextRunAt` is
`03:29:23`, not `03:17:00`: 743 seconds of deterministic jitter derived from the job id
([`03-job-model.md`](03-job-model.md) §2.1). It will be the same offset tomorrow and next month.

### 6.3 Two days later, the human

Thursday's run failed. Friday's timed out at 45 minutes. Both wrote a log file and a JSONL
record; both fired the best-effort notifier, which on this headless box returned
`{"shown": false, "reason": "no_foreground_client"}` and changed nothing about either outcome
(D8; [`03-job-model.md`](03-job-model.md) §4.6). The record is the record.

Friday mid-morning the human runs `herdr-cron` with a TTY, which launches the TUI
([`06-tui.md`](06-tui.md)). The job list shows `nightly-deps` with two red rows and
`consecutiveFailures: 2`. Clicking the job opens the detail pane — one `job get` behind the
scenes, one round trip ([`05-cli.md`](05-cli.md) §3.1). The same data from the CLI side:

```console
$ herdr-cron run list --job nightly-deps --status failed --limit 5
```

```json
{"id": "cli:run:list",
 "result": {"type": "run_list", "runs": [
   {"runId": "nightly-deps-20260902T181700Z", "jobId": "nightly-deps", "trigger": "scheduler",
    "attempt": 1, "scheduledAt": "2026-09-03T03:17:00+09:00",
    "startedAt": "2026-09-03T03:29:23+09:00", "finishedAt": "2026-09-03T03:31:02+09:00",
    "durationSec": 99, "status": "failure", "exitCode": 1, "reason": null,
    "logPath": "logs/nightly-deps/nightly-deps-20260902T181700Z.log",
    "outputExcerpt": "go.mod is not readable: permission denied", "host": "huke-desktop"},
   {"runId": "nightly-deps-20260903T181700Z", "jobId": "nightly-deps", "trigger": "scheduler",
    "attempt": 1, "scheduledAt": "2026-09-04T03:17:00+09:00",
    "startedAt": "2026-09-04T03:29:23+09:00", "finishedAt": "2026-09-04T04:14:23+09:00",
    "durationSec": 2700, "status": "timeout", "exitCode": null, "reason": null,
    "logPath": "logs/nightly-deps/nightly-deps-20260903T181700Z.log",
    "outputExcerpt": "…still waiting on `npm audit`", "host": "huke-desktop"}]}}
```

*(illustrative envelope; run records normative.)* The run ids are UTC renderings of the local
occurrences — `2026-09-03T03:17:00+09:00` is `20260902T181700Z`
([`03-job-model.md`](03-job-model.md) §6; see §7 below). The human clicks a row and the TUI
streams the log file, the same bytes `herdr-cron run logs nightly-deps-20260903T181700Z --tail 50`
would print as raw text — the one documented exception to the JSON envelope
([`05-cli.md`](05-cli.md) §3.2).

Two failures out of three. One more and the circuit breaker trips on its own, disabling the job
with `disabledReason: auto_failures`. The human gets there first and clicks pause, which is
exactly:

```console
$ herdr-cron job pause nightly-deps
```

```json
{"id": "cli:job:pause",
 "result": {"type": "job_state", "id": "nightly-deps", "enabled": false,
            "enabledSource": "override", "reason": "manual",
            "at": "2026-09-04T10:12:44+09:00"}}
```

*(illustrative payload.)* The pause is an override in `state.json`, not an edit to `jobs.yaml`
([`03-job-model.md`](03-job-model.md) §5) — the human's authored YAML, comments and all, is
untouched, and `git status` stays clean. The override records `declaredEnabled: true`; if someone
later edits `enabled: false` into the file by hand, the override is discarded and the file wins,
because editing the file is always the stronger act.

Nothing in this story required a socket, a database, a server, or a second system.

---

## 7. Open points

Six discrepancies were found in the normative documents while writing this overview. Five were
resolved by the lead on 2026-09-02 and are recorded here so the resolution is traceable; one
remains.

| # | Discrepancy | Resolution |
| --- | --- | --- |
| 1 | `runId` samples used local-time digits with a `Z` suffix, contradicting the stated UTC rule. | Resolved. Samples in [`03-job-model.md`](03-job-model.md) §6, [`04-storage.md`](04-storage.md) §4 and [`05-cli.md`](05-cli.md) §3.1 now follow the rule (`nightly-deps-20260902T181700Z`). |
| 2 | Who writes the `enabled` override — [`05-cli.md`](05-cli.md) said the CLI writes it with no daemon; [`04-storage.md`](04-storage.md) said `state.json` has a single daemon writer. | Resolved by splitting the file. Overrides live in `overrides.json`, guarded by `overrides.lock`, with three legitimate writers ([`04-storage.md`](04-storage.md) §4, §9). |
| 3 | "Trigger" named both the run-provenance enum and the CLI→daemon request file. | Resolved by naming, not renaming: "run trigger" versus "trigger file", defined in [`04-storage.md`](04-storage.md) §8. |
| 4 | `result.type` was specified for `job_list` only. | Resolved. [`05-cli.md`](05-cli.md) §2 now carries the complete payload-type table. |
| 5 | Envelope `id` was undefined for ungrouped commands. | Resolved. `cli:<command>` for ungrouped commands ([`05-cli.md`](05-cli.md) §2). |
| 6 | `docs/spec/README.md` was referenced but absent. | Resolved. It exists and carries decisions D1–D8. |

Still open, and owned by this document: everything marked *(illustrative)* in §6 is an inference
from the envelope rules rather than a quotation of a fixed shape. Where §6 and
[`05-cli.md`](05-cli.md) §2's payload table now disagree, the table wins.
