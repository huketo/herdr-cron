# Domain context

`herdr-cron` decides *when* something unattended should happen, makes it happen exactly once, and records what happened. Everything in this codebase is named after some part of that loop. Use these words; the glossary is the vocabulary of issue titles, test names, log messages, `error.code` values, and user-facing text.

Decisions live in [`docs/adr/`](docs/adr/) and, for anything taken before the ADRs existed, in the D1–D8 record in [`docs/spec/README.md`](docs/spec/README.md). Field names are owned by [`docs/spec/03-job-model.md`](docs/spec/03-job-model.md) and [`docs/spec/04-storage.md`](docs/spec/04-storage.md); where this file and those disagree, they win.

## Glossary

### Job

The unit of declaration: an intent to run one thing on a schedule. Authored in `jobs.yaml`, identified by a stable `id` matching `^[a-z0-9][a-z0-9._-]{0,127}$`. Two kinds and only two — `shell` (a child process) and `agent` (a prompt driven into a Herdr pane). No steps, no dependencies (D5).

Two Go types, deliberately: `model.Job` is the *authored* form, every optional field a pointer so "absent" and "set to the zero value" stay distinguishable; `model.Resolved` is the *canonical* form emitted by `job get` and `job list` and consumed by the runner — defaults applied, `~` expanded, durations normalised to seconds, effective `enabled` merged in. Nothing downstream of `config.Load` sees a `*model.Job`.

### Run

One execution of one Job, and the only thing history is made of. `model.Run`, appended **twice** to `runs/<jobId>.jsonl`: once as `running`, once with a terminal `Status`. `runId` is `model.NewRunID` — `<jobId>-<UTC timestamp>` — deterministic in `(jobId, scheduledAt)`, which is what makes a repeated catch-up pass idempotent instead of expensive.

A Run carries its own `trigger`, `attempt`, timestamps, `status`, `reason`, `logPath`, an `outputExcerpt` (the last 2 KiB; the rest lives in the log file), the `host` that ran it, and for `kind: agent` a `herdr` block naming the session, pane and agent.

### Occurrence

An instant at which a Job's schedule says it should fire. Computed without a scheduler by
`schedule.Schedule.Next` / `NextN`, so `validate --schedule` and the daemon can never disagree —
both parse through gocron's own cron parser. An `at` Job has exactly one Occurrence, and Jitter
never moves it.

An Occurrence is not a Run and a Run is not always an Occurrence: a manual Run has no Occurrence
(`scheduledAt` is null). Every one-time Occurrence produces a Run record even when nothing
executes: a missed or policy-refused Occurrence is `skipped` with a `reason`. Once the
`state.json` watermark reaches that instant, the CLI derives `completed: true`; `jobs.yaml`
remains authored and unchanged.

### Run trigger vs. trigger file

One word, two unrelated objects. Say which.

A **run trigger** is provenance on a Run record: `model.Trigger`, one of `scheduler`, `manual`, `catchup`, `retry`, `startup`. For recurring schedules it decides whether Jitter applies (`scheduler` and `catchup` only), and it decides whether the Run counts against a limit.

A **trigger file** is the CLI→daemon channel: `daemon.Trigger`, one JSON file written into `<state>/triggers/<ulid>.json` by `job run`, `job cancel` or `reload`, claimed by the daemon **renaming** it to `.claimed`, answered by a `daemon.TriggerResult` written to `.result`. The rename is the claim, which is why double-processing is impossible without a lock. `job pause` and `job resume` deliberately do *not* use this channel — see Override. ADR-0002.

### Driver

How `run-once` gets invoked. Three of them, interchangeable, changing no job semantics: `daemon` (default — a resident process owning a gocron scheduler), `foreground` (the same code path with logs on stderr, for a Herdr pane), and `os-scheduler` (one systemd timer / launchd LaunchAgent / Windows Scheduled Task per job, each exec'ing `run-once`). ADR-0001.

`service.Driver` has only two values — `DriverDaemon` and `DriverOSScheduler` — because `foreground` is nothing to register: it is a process a human starts. That asymmetry is intentional, not an oversight.

### Catch-up

What happens to Occurrences that passed while nothing was running — the daemon was down, or the
machine slept. `model.Catchup`: `off`, `latest` (default), or `all`, each bounded by
`catchup_window` (default 1h for a one-time Job and 168h otherwise).

For recurring Jobs, `latest` means **exactly one** Run for the most recently missed Occurrence,
and everything older is discarded. Twenty seconds of downtime on a five-second Job produces
one `catchup` Run, not four. For a one-time Job, `latest` and `all` are identical: its single
Occurrence runs with trigger `catchup` inside the window, becomes `skipped` / `missed_window`
outside it, or becomes `skipped` / `catchup_off` when catch-up is disabled. Every outcome
advances the watermark, so the same Occurrence is never reconsidered.

### Overlap

The previous Run of a Job is still going when the next Occurrence arrives. Resolved per Job by `model.Concurrency`: `skip` (default), `queue`, `cancel_previous`, or `allow`.

Enforced by an advisory lock on the job's own history file — `store.TryLockRun` — not by an in-memory set, because under the `os-scheduler` Driver the contending runs are separate processes. Held across the whole run including the terminal write, so a crash releases it with the OS. An Overlap under `skip` is a real Run record: `skipped` / `overlap`, exit code 0. Exit 0 because the command did what it was asked; a skip must not mark a systemd unit failed.

### Terminal outcome

A Run's final `model.Status`: `success`, `no_op`, `failure`, `timeout`, `blocked`, `skipped`, or `cancelled`. `running` is the only non-terminal one, and `Status.Terminal()` is that single sentence in code.

The Terminal outcome is a different field from the Run's captured output, and the two are never conflated: output is evidence, status is the verdict. `reason` is one union across every status — `overlap`, `limit_exceeded`, `disabled`, `catchup_capped`, `superseded`, `missed_window`, `catchup_off`, `cwd_not_trusted`, `daemon_died`, `auto_failures`, and the rest — so a reader parses one enum rather than one per status. A one-time Job's watermark is spent when its Occurrence is claimed, before the Terminal outcome may exist; `completed` therefore means accounted for, not terminal or successful.

### Blocked

Terminal, and specific: the agent is sitting on an approval or question UI that nobody will answer. `model.StatusBlocked`, CLI exit code 3.

It is never retried, it always notifies, and it increments `consecutiveFailures`. Retrying a run stuck on a human approval dialog burns the daily limit and changes nothing. The failure is verified, not hypothesised — an agent started in a `cwd` it has never been trusted with parks on a dialog forever — and `herdr.CheckTrust` is the pre-flight that turns it into a 4 ms `blocked` / `cwd_not_trusted` record with nothing created. No surveyed scheduler models this status; it is a deliberate invention.

### No-op

Terminal and successful: the Run completed and correctly did nothing. `model.StatusNoOp`, detected by the captured output equalling the Job's `no_op_marker` exactly (`AgentSpec.NoOpMarker`).

It exists so that a healthy week of agent history is not 300 identical green rows. The protocol is borrowed from `agent-cron`'s `HEARTBEAT_OK`. For a caller, `no_op` is a success: `job run --wait` exits 0 for both.

### Front door

One of the two supported ways to obtain and launch herdr-cron (D4): the **Herdr plugin** — marketplace install or `herdr plugin link .`, with a `[[startup]]` hook, `[[actions]]` and a `[[panes]]` surface — and the **standalone CLI**, which works with Herdr absent and arrives either by `go install` or as a prebuilt archive from the Releases page. Both are the same binary and the same on-disk state. `install-cli --with-skill` then puts the binary on `PATH` and installs the Agent Skill, which is the part an agent cannot arrange for itself.

There is no Homebrew tap and no Scoop bucket. A Front door is not a Driver: the first is how the binary arrives, the second is who holds the clock.

### Jitter

A deterministic per-job offset added to a recurring Occurrence before it fires:
`offset = FNV1a64(job.id) mod min(interval/2, 30m)`, surfaced as
`ResolvedSchedule.JitterSec` and applied by `schedule.NextN`'s jitter argument.

Deterministic, not random, so the same Job always fires at the same offset and `validate` can
tell the truth about it. It is a safety feature, not a nicety: six agent Jobs at `0 9 * * *`
would otherwise launch six agents into the same repository in the same second. Applied to
`scheduler` and `catchup` Run triggers on recurring Jobs, never to `manual` or an `at` Job.

### Override

The mutable half of `enabled`. Declared in `jobs.yaml`, overridden in `overrides.json`: `store.Override` records the new value, the `declaredEnabled` it was recorded against, a `reason` (`manual` or `auto_failures`) and a timestamp; `store.EffectiveEnabled` merges the two and reports which won as `enabledSource`.

The split exists so that clicking the toggle in the TUI never rewrites a user's authored YAML — comments and key order are not ours to lose — and so `job pause` works with no daemon running. An Override recorded against a different declared value is **discarded**, which is what makes a hand edit to `jobs.yaml` always win back. `job resume` deletes the Override rather than inverting it (D2).

### Heartbeat

`daemon.Heartbeat`, the contents of `daemon.json`: pid, start time, `heartbeatAt`, version, driver, config path, job count, and the current config error if any. Rewritten every 15 s by atomic rename.

**Liveness is the Heartbeat plus the lock, never the Heartbeat alone.** A `kill -9` leaves a heartbeat that stays fresh for a minute, during which `status` lied and `daemon --detach` was a silent no-op; `daemon.lock` is held by the kernel and released when the process dies, so the two together cannot both be wrong. This was learned by running, not by reading.

### Adapter / Session

The **adapter** is `herdr.Client`, the only type in the codebase that knows the `herdr` CLI exists. Every interaction is an argv exec of that CLI with captured stdout, decoded through `herdr.Envelope`; herdr-cron never opens Herdr's socket. Behind it sit the version gate (`CheckVersion`, floor `0.8.2`) and the trust pre-flight.

The **session** is the Herdr session Jobs run in — `herdr-cron` by default, resolved by `herdr.ResolveSession`, overridable per job via `agent.session` (including `current`). A dedicated session keeps 03:00 tabs out of the human's workspace and guarantees the headless geometry; a fresh headless server starts with zero workspaces, so the runner builds its own topology — host workspace, one tab per Run, pane closed afterwards (D7).

### Agent Skill

The Markdown document at `skills/herdr-cron/SKILL.md` that teaches an agent *how to drive this CLI correctly on the first attempt* — read `result`, branch on `error.code`, validate a schedule before writing it, ask the binary rather than trusting a remembered flag. Hand-written, embedded by `skills/embed.go` under one `//go:embed` directive, read back through `skills.SkillMD()` and `skills.References()`, printed verbatim by `herdr-cron --skill` (`skills.Print`), with three bundled files under `references/`.

It is a router and a set of invariants, not a frozen copy of the help text. It has no version of its own: the binary that embeds it is its version. ADR-0003.

### Plugin manifest

`herdr-plugin.toml` at the repo root: the contract between Herdr and this plugin. Declares the plugin id `huketo.herdr-cron`, the version floor `min_herdr_version = "0.8.2"`, one `[[build]]`, one `[[startup]]` hook (`daemon --detach`, idempotent because Herdr re-runs it on every server start and every live handoff), four global `[[actions]]`, and one `[[panes]]` surface for the TUI.

It is not a config file. User configuration is `jobs.yaml` under the config root.

## Words we do not use

- **"Task."** Overloaded three ways in this neighbourhood — a Claude Code scheduled task, a Windows *Scheduled Task*, a queue's work item. Ours is a **Job**, and one occurrence of it is a **Run**. "Task" is reserved for the OS-level artefact the `os-scheduler` Driver creates on Windows.
- **"Pipeline," "workflow," "DAG."** We do not have them and will not: a Job is one command or one prompt (D5). Using the words in an issue title invites the feature. dagu, which did grow all of them and did it well, states the price in its own README: *"You wanted to schedule some jobs. Now you operate a second system."*
- **"Cron"** is not a synonym for this product. It is one of the three schedule *forms* (`cron`, `every`, `at`), and it is the Unix daemon we are compared to. The product is **herdr-cron**, and the thing that holds the clock is a **Driver**.
- **"Job failed"** for a `skipped` Run. A skip is not a failure — different status, different `reason`, exit code 0, and it does not touch `consecutiveFailures`.
- **"Notification"** as the record of a Run. Logs and the JSONL record are always written; the notifier is best-effort and its failure never changes an outcome. On a headless server `herdr notification show` provably returns `{"shown": false}`, so a scheduler that reported through it would be silent exactly when it mattered (D8).

## The scheduled-run lifecycle

One `kind: agent` Job, from Occurrence to Run record, under the default `daemon` Driver.

1. The daemon holds `daemon.lock`, writes a Heartbeat every 15 s, and owns one gocron scheduler built from the enabled Jobs in `jobs.yaml`. An edit to that file, or a `reload` trigger file, rebuilds it live.
2. An Occurrence arrives — the Job's next fire time plus its Jitter. gocron calls `Daemon.fire(jobID)`, which re-reads the Job from the loaded config rather than trusting a closure, because the file may have changed since the timer was set.
3. Guards, in order: effective `enabled` (declared value merged with its Override), `max_runs_per_day` against `runsToday`, and the Overlap lock. Any of them refusing writes a `skipped` Run with the matching `reason` and stops here — a refusal is still history.
4. `runner.RunOnce` is called with `Options{Trigger: scheduler, ScheduledAt: <the Occurrence>}`. It writes `state.lastScheduledAt` **before** executing (so a crash mid-run cannot cause the same Occurrence to be caught up), appends the `running` Run record, and opens `logs/<jobId>/<runId>.log`.
5. For `kind: agent` the adapter resolves the `herdr` binary, checks the version floor, and runs the trust pre-flight. An untrusted `cwd` ends the Run here as `blocked` / `cwd_not_trusted` with nothing created — no server started, no pane opened, no money spent.
6. Otherwise: ensure a headless server, ensure the host workspace for the Job's `cwd`, create one tab for this Run, start the agent, and prompt it with the scheduler preamble (`runner.Preamble`) prepended verbatim to the Job's prompt. The preamble is a product feature, not each user's responsibility: its absence is the documented cause of a scheduled agent stalling forever on a question.
7. The transcript is captured with `herdr agent read --source recent-unwrapped` and streamed into the log file as it arrives. The pane is closed when the Run ends, whatever the outcome.
8. Classification produces the Terminal outcome: the final assistant text equal to `no_op_marker` is `no_op`; the timeout firing is `timeout` with the process group killed, not just the child; a recognised Herdr signal maps per the integration spec; anything unrecognised is `failure` with `herdr_unexpected` and the raw envelope in the log.
9. `finish` appends the terminal Run record and updates `state.json` — `lastRunId`, `lastStatus`, `lastFinishedAt`, `consecutiveFailures`, `runsToday`. On the third consecutive `failure`/`timeout`/`blocked` it writes an `enabled: false` Override with reason `auto_failures`, records a Run saying so, and notifies. `job resume` is how a human undoes that.
10. The notifier fires last, for the statuses in `notify.on`, and its failure is logged and ignored.
11. Everyone reads the same files afterwards: `run list`, `run get`, `run logs --follow` for the agent; the TUI for the human, over the same code paths and taking no locks. The store holds the truth — no fact lives only in a process.
