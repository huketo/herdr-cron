# 0001 — A `run-once` core with three interchangeable drivers

## Status

Accepted — 2026-09-02. Decision **D1** in [`docs/spec/README.md`](../spec/README.md); specified in [`docs/spec/02-architecture.md`](../spec/02-architecture.md) §2.

## Context

A scheduler has to answer one architectural question before anything else: what holds the clock. The surveyed prior art gives three incompatible answers, and each of them is the *correct* answer to a different question ([`docs/research/2026-09-02-prior-art-and-domain-model.md`](../research/2026-09-02-prior-art-and-domain-model.md) Q7).

**A resident daemon** is the only shape in which an agent's `job add` takes effect immediately. That is not a nicety: the primary caller here is a coding agent that adds a job and then wants to see it fire. The research calls immediacy "the option's decisive advantage" for this option (Q7(a)).

**A foreground process** is the only shape that is trivially correct everywhere — "easiest of the three, it's just a process" (Q7(b)) — and it is the natural fit for a Herdr pane, where the human can watch the log scroll. Its cost is stated by Claude Code's own documentation, quoted in the same section: *"Closing the terminal or letting the session exit stops them firing."*

**OS-scheduler registration** is the only shape that answers "the laptop slept for six hours" correctly and for free, on Linux. systemd's `Persistent=` is `catchup: latest` implemented by the operating system: the unit *"is triggered immediately if it would have been triggered at least once during the time when the timer was inactive"*, and a calendar timer that elapsed repeatedly during a single sleep *"will only result in a single service activation"* (Q3, quoting `systemd.timer(5)`). Nothing in Go gets this for free — `CLOCK_MONOTONIC` "does not count time that the system is suspended", and gocron's `advancePastNow` "walks the schedule forward, discarding every intermediate tick" ([`docs/research/2026-09-02-gocron-scheduling-engine.md`](../research/2026-09-02-gocron-scheduling-engine.md) §8). Its cost is that declared state and OS state can diverge, invisibly, and that `@dortort/scheduler` — the closest analogue — supports *"macOS (launchd) or Linux (crontab)"* and no Windows at all (Q7(c)).

Picking one means being wrong for a third of the users, in a way that cannot be reverted without a rewrite: the choice leaks into every execution path if the executor is written inside the chosen host.

## Decision

**`herdr-cron run-once <job-id>` is the only code path that ever executes a job.** One run, synchronously, in the calling process, with no timer and no daemon. It reads the roots, `jobs.yaml`, `state.json` and the tail of the job's history; it writes the watermark, the `running` record, the log file, the terminal record, the state update, and finally the notifier subprocess. Nothing else executes work.

**Three drivers sit above it, and they differ only in who holds the clock.**

- `daemon` (default) — a long-lived process owning one gocron `Scheduler`, calling the same function in-process.
- `foreground` — `herdr-cron daemon --foreground`: the same code path with logs on stderr instead of the log file, for a Herdr pane.
- `os-scheduler` — no herdr-cron process between runs: `herdr-cron service install --driver os-scheduler` writes one OS entry per enabled job, each exec'ing `run-once` with `HERDR_CRON_TRIGGER=scheduler`.

The default is `daemon`, because catch-up has to exist for it regardless — which makes systemd's `Persistent=true` a Linux-only bonus rather than a portable answer — and because `job add` taking effect now is what an agent notices every time.

**Two invariants make the drivers interchangeable, and they are the load-bearing part of this decision.**

*`run-once` never consults a daemon.* No `daemon.json` read, no `daemon.lock` test, no trigger files, no refusal to run because a daemon is or is not live. The only cross-process coordination is an advisory lock on the job's own `runs/<jobId>.jsonl`.

*The store holds the truth, not any process.* Every decision — effective `enabled`, `runsToday`, `consecutiveFailures`, whether an occurrence was already caught up — is recomputed from `jobs.yaml` + `state.json` + `runs/<jobId>.jsonl`. No fact may live only in memory. This is what lets the `os-scheduler` driver, where several `run-once` processes may be alive at once and none of them is a scheduler, behave identically to a daemon that owns everything.

Overlap is therefore enforced by that per-job file lock rather than by an in-memory set, and `cancel_previous` degrades honestly: when the lock holder is a different pid, `run-once` records `skipped` / `overlap` instead of pretending to cancel a process it does not own.

## Consequences

- Behaviour is defined once. A change to timeout handling, catch-up idempotence, limits, or outcome classification lands in one function and is true under all three drivers.
- `herdr-cron run-once <job-id>` is also the testing and debugging surface: one run, in the foreground, exit code and JSON envelope on the spot, no daemon to restart.
- The `os-scheduler` driver gets Linux catch-up from systemd and still has herdr-cron's own catch-up pass underneath it. Belt and braces, but the two agree because `runId` is deterministic in `(jobId, scheduledAt)` — a duplicate catch-up is a no-op rather than a second LLM invocation.
- Cost: **the largest v1 surface of the four options.** Three service-installation backends to write and test — marker-fenced systemd units, launchd plists, Task Scheduler entries — plus their drift states (`ok` / `stale` / `orphan` / `missing`) surfaced by `herdr-cron service status --driver os-scheduler`. This is the price, and it was accepted deliberately.
- Cost: two schedule forms cannot be translated exactly into `OnCalendar=`, so `service install --driver os-scheduler` **refuses** them rather than approximating. A refusal is a supported outcome of that command.
- Cost: the `os-scheduler` driver's entries are a snapshot. A job added afterwards does not fire until `service install` runs again — the one place where a driver does change observable behaviour, documented in [`docs/spec/02-architecture.md`](../spec/02-architecture.md) §2.3 and mitigated by §4.4's drift detection rather than hidden.
- Cost: macOS and Windows service registration is read from documentation, not executed. Carried as a known risk in [`docs/spec/README.md`](../spec/README.md) rather than claimed.

## Alternatives considered

**A daemon only.** Rejected: it answers "the laptop slept for six hours" with code we write instead of code systemd already ships, and it forces a resident process on a user who wants three jobs a week. It also has no good story for a Herdr pane, where the human wants to *watch*.

**A foreground process only.** Rejected: the schedule dies with the terminal, which is the failure mode of every agent-scheduling tool the research surveyed that chose it (`prior-art` §1.6.4, Q7(b)). A scheduler that stops when you close a window is a loop, not a scheduler.

**OS registration only.** Rejected on two counts: `job add` would not take effect until a second command re-registered everything, and the Windows backend is a different model with a different test matrix — the tool that chose this shape shipped no Windows support at all (Q7(c)). It also makes "is it running now?" a lock-file question in every case.

**A daemon that shells out to `run-once` instead of calling it in-process.** Tempting for symmetry, and rejected: it doubles per-run latency and process count for no behavioural gain, and it makes `cancel_previous` — which needs to cancel a context the scheduler owns — impossible under the default driver.

**A queue with workers (river-style).** Rejected: it imports a lease model, a retry policy calibrated for cheap work (river defaults to 25 attempts; an LLM invocation is not a webhook delivery — `prior-art` §1.5, Q5), and a second system to operate. D5 refuses the orchestration that would justify it.
