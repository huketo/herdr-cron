# 0006 — One-shot jobs are `at` jobs with a lifecycle

## Status

Accepted — 2026-09-03. Specified in [`docs/spec/03-job-model.md`](../spec/03-job-model.md) §2, §2.1, §4.1 and §6, and [`docs/spec/05-cli.md`](../spec/05-cli.md) §3.1.

## Context

The Job model already has the concept required for work that should happen once: an `at` schedule names one absolute instant and gocron represents it as a `OneTimeJob`. The missing work was not another scheduling concept. It was an honest lifecycle for that schedule across exact timing, downtime, history, and inspection.

Two measurements exposed the gap. A one-shot that named **10:14:26** reported `nextRunAt` as **10:44:22**, because the resolved schedule carried `jitterSec: 1796`. The safety offset that keeps recurring Jobs from arriving together had moved an instant the author chose deliberately by 29 minutes and 56 seconds. Separately, a one-shot whose instant passed while the daemon was down produced no Run record. Every reload then repeated `cannot add job ... start must not be in the past`, while history gave no answer to whether the work ran or why it did not.

The authored definition also had no trustworthy lifecycle projection. Earlier specification text said the Job would be "recorded `completed`" after firing, but no such mutation or stored status had ever existed. Silently editing `jobs.yaml` would contradict D2: that file belongs to its author, while mutable execution state belongs in `state.json` and Run history.

A separate pending-work file and a new command could avoid overloading `at`, but only by creating a second path through the daemon, TUI, CLI and store. It would also need a second translation beside the existing systemd, launchd and Windows Task Scheduler paths. The same command or prompt would then behave differently depending on which scheduling vocabulary created it.

## Decision

**First, an `at` Job owns one exact Occurrence.** Its resolved `jitterSec` is always zero, because herd protection cannot move a deliberately named instant without making `nextRunAt` dishonest. An explicit `jitter` is ignored with a `jitter_ignored` warning. The CLI accepts either an RFC 3339 instant or a `+`-prefixed relative value such as `+2h`; it resolves the latter once and stores the resulting absolute instant so a reload cannot move it forward again. A bare duration such as `2h` remains an `every` schedule. `job add` and `job update` reject an instant that is already past as a usage error, because a newly authored Job must still have an Occurrence ahead of it.

**Second, reconciliation accounts for the Occurrence exactly once.** When an unspent instant is already past, a Job within its `catchup_window` runs with `trigger: catchup`. Outside the window it produces a `skipped` Run with reason `missed_window`; with `catchup: off` it produces a `skipped` Run with reason `catchup_off`. `latest` and `all` are indistinguishable when only one Occurrence exists. Being claimed for execution and both refusals advance `state.json`'s `lastScheduledAt` watermark, so daemon start, a wall-clock jump, and a `jobs.yaml` reload cannot record or execute the same Occurrence twice. A one-shot defaults to a one-hour window rather than the recurring default of 168 hours: replaying a Job created for a particular moment days later is more likely to be harmful than useful.

**Third, `completed` is a projection, not an authored state.** `jobs.yaml` is never changed when the Occurrence is claimed or skipped. `job list` and `job get` expose `completed` only for one-shot Jobs, derived by comparing the `state.json` catch-up watermark with the Job's instant. `completed: true` therefore means the one Occurrence is spent, not that its Run is terminal or successful. The Job remains available for inspection until its author explicitly removes it with `job rm`.

## Consequences

- The time shown before a one-shot fires is the time at which it will fire. Recurring Jobs retain deterministic Jitter, where spreading a herd is the intended trade-off.
- Downtime never makes a one-shot disappear without history. A Run records either execution or the policy that prevented it, and the watermark makes that decision idempotent.
- Authored definitions remain stable and diffable. Completion cannot reorder or rewrite a user's YAML, and removing a completed Job remains an explicit act.
- Every surface continues to consume the same Job, Occurrence and Run model. The daemon and all three OS translations do not gain a second scheduling path.
- A one-shot left in `jobs.yaml` remains visible with `completed: true` but is never scheduled again.

## Alternatives considered

**A separate pending queue file and a new scheduling command.** Rejected: it duplicates scheduling and lifecycle logic across the daemon, TUI, CLI and store, then duplicates the systemd, launchd and Windows Task Scheduler translations. It is a second system for an Occurrence the existing `at` form already represents.

**Delete or mark the Job in `jobs.yaml` after it fires.** Rejected: execution would mutate an authored, comment-preserving file and create surprising diffs. `state.json` already has the watermark needed to derive completion without taking ownership of the definition.

**Drop a missed one-shot silently.** Rejected: the observed failure left no Run to inspect and repeated the same scheduler error on every reload. A refusal is still an outcome and must be recorded.

**Apply deterministic Jitter unless the author turns it off.** Rejected: the default `auto` setting caused the measured 10:14:26 instant to be reported as 10:44:22. Exact timing is intrinsic to `at`, not an optional override an author must discover.

## Follow-up work

Two adjacent capabilities remain outside this decision. A future `job run` option named `at` could schedule a delayed execution of an existing Job without authoring another Job, but no such flag or lifecycle exists yet. `start_at` and `stop_at` are declared as YAML fields, but their interpretation and enforcement are not implemented; this decision does not make either field operational.
