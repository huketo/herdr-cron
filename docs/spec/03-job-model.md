---
title: herdr-cron — Job model
date: 2026-09-02
status: spec (normative)
---

# Job model

Normative. RFC 2119 keywords. Every design choice traces to a decision in
[`README.md`](README.md) or to evidence in `docs/research/`.

A **job** is a declared intent to run something on a schedule. A **run** is one execution of a
job. They are separate records with separate lifetimes and separate storage
([`04-storage.md`](04-storage.md)).

---

## 1. Job record

### 1.1 YAML form — the authored definition

`jobs.yaml` is the source of truth for definitions. It is written by humans and by agents, is
diffable, and is safe to commit to git.

```yaml
version: 1

defaults:
  timezone: local
  timeout: 30m
  concurrency: skip
  jitter: auto
  catchup: latest
  catchup_window: 168h
  retry:
    max_attempts: 1
  limits:
    max_consecutive_failures: 3
  notify:
    on: [failure, blocked, auto_disabled]

jobs:
  - id: nightly-deps
    name: Nightly dependency audit
    description: Check for outdated deps and report.
    enabled: true
    tags: [maintenance, repo:herdr]

    schedule:
      cron: "17 3 * * 1-5"
      timezone: Asia/Seoul
      catchup: latest
      catchup_window: 24h
      jitter: auto

    kind: agent
    agent:
      agent_kind: claude
      prompt: |
        Audit dependencies in this repo. If everything is current,
        reply with exactly HEARTBEAT_OK and stop.
      capture: transcript
      no_op_marker: HEARTBEAT_OK
      session: herdr-cron
      worktree: false

    cwd: ~/src/herdr
    env:
      GIT_AUTHOR_NAME: herdr-cron
    timeout: 45m

    concurrency: skip
    retry:
      max_attempts: 2
      backoff: exponential
      initial: 60s

    limits:
      max_runs_per_day: 4
      max_consecutive_failures: 3

    notify:
      on: [failure, auto_disabled]

  - id: build-smoke
    name: Hourly build smoke
    enabled: true
    schedule:
      every: 30m
    kind: shell
    shell:
      command: go build ./... && go test ./internal/scheduler/...
    cwd: ~/src/herdr
    timeout: 10m
```

### 1.2 Field reference

| Field | Type | Default | Rules |
| --- | --- | --- | --- |
| `version` | int | — | REQUIRED at file top level. `1` for this spec. An unknown version is a load error, not a warning. |
| `defaults` | map | — | Any field below except `id`, `name`, `schedule`, `kind`, and the kind payloads. Applied per job before validation. |
| `id` | string | — | REQUIRED. `^[a-z0-9][a-z0-9._-]{0,127}$`. Unique within the file. Stable: it is the key for state, history, logs, and the gocron `WithIdentifier` UUID derivation. Renaming an id orphans its history. |
| `name` | string | `id` | Display only. |
| `description` | string | `""` | Display only. Shown in the TUI detail pane and in `job get`. |
| `enabled` | bool | `true` | The **declared** value. The effective value may be overridden by state; see §5. |
| `tags` | []string | `[]` | Free-form. `key:value` is conventional but not enforced. Used by `--tag` filters. |
| `schedule` | object | — | REQUIRED. See §2. |
| `kind` | enum | — | REQUIRED. `shell` \| `agent`. v1 supports exactly these two. |
| `shell` | object | — | REQUIRED when `kind: shell`. See §3.1. |
| `agent` | object | — | REQUIRED when `kind: agent`. See §3.2. |
| `cwd` | path | daemon's cwd | `~` and `$VAR` are expanded at load time. MUST be absolute after expansion. MUST exist at run time or the run fails immediately with `cwd_missing`. |
| `env` | map[string]string | `{}` | Merged over the daemon's environment. Values are used verbatim; no shell expansion. |
| `timeout` | duration | `30m` | `0` means inherit the default; `-1` means never. A run exceeding it is killed and recorded `timeout`. |
| `concurrency` | enum | `skip` | `skip` \| `queue` \| `cancel_previous` \| `allow`. See §4.3. |
| `retry` | object | `{max_attempts: 1}` | See §4.4. |
| `limits` | object | see §4.5 | Spend guardrails. |
| `notify` | object | `{on: [failure, blocked, auto_disabled]}` | See §4.6. |

Unknown keys are a **load error**, not a warning. A typo in `catchup_window` must not silently
disable catch-up. (Herdr's plugin manifest does the opposite — a misspelled hook name yields
only a `warnings` entry and never fires; `docs/research/2026-09-02-herdr-plugin-integration.md`
§4. That failure mode is exactly what this rule avoids.)

Durations are Go `time.ParseDuration` strings (`45m`, `1h30m`, `10s`). Bare integers are
REJECTED — `timeout: 30` is ambiguous and MUST be written `30s` or `30m`.

### 1.3 JSON form — the canonical resolved record

`job get` and `job list` emit the **resolved** record: defaults applied, `~` expanded, durations
normalised to seconds, and the effective `enabled` merged in. Agents consume this; they never
parse YAML.

```json
{
  "id": "nightly-deps",
  "name": "Nightly dependency audit",
  "description": "Check for outdated deps and report.",
  "enabled": true,
  "enabledSource": "file",
  "tags": ["maintenance", "repo:herdr"],
  "schedule": {
    "type": "cron",
    "expression": "17 3 * * 1-5",
    "timezone": "Asia/Seoul",
    "catchup": "latest",
    "catchupWindowSec": 86400,
    "jitterSec": 743
  },
  "kind": "agent",
  "payload": {
    "agentKind": "claude",
    "prompt": "Audit dependencies in this repo. If everything is current,\nreply with exactly HEARTBEAT_OK and stop.\n",
    "capture": "transcript",
    "noOpMarker": "HEARTBEAT_OK",
    "session": "herdr-cron",
    "worktree": false
  },
  "cwd": "/home/huke/src/herdr",
  "env": {"GIT_AUTHOR_NAME": "herdr-cron"},
  "timeoutSec": 2700,
  "concurrency": "skip",
  "retry": {"maxAttempts": 2, "backoff": "exponential", "initialSec": 60},
  "limits": {"maxRunsPerDay": 4, "maxConsecutiveFailures": 3},
  "notify": {"on": ["failure", "auto_disabled"]},
  "state": {
    "lastRunId": "nightly-deps-20260901T181700Z",
    "lastStatus": "success",
    "lastFinishedAt": "2026-09-02T03:41:12+09:00",
    "consecutiveFailures": 0,
    "runsToday": 1,
    "nextRunAt": "2026-09-03T03:29:23+09:00",
    "nextRuns": ["2026-09-03T03:29:23+09:00", "2026-09-04T03:29:23+09:00"]
  }
}
```

Key names are `lowerCamelCase` in JSON and `snake_case` in YAML. This is deliberate: the YAML is
read by humans next to other YAML tools, the JSON is read by agents next to other JSON APIs. The
mapping is mechanical and total — every YAML key has exactly one JSON key.

`enabledSource` is `file` or `override`; see §5.

---

## 2. Schedule

Exactly one of `cron`, `every`, or `at` MUST be present.

| Form | Example | Semantics |
| --- | --- | --- |
| `cron` | `"17 3 * * 1-5"` | Standard cron. 5 or 6 fields; with 6, the first is seconds. Descriptors `@yearly`, `@annually`, `@monthly`, `@weekly`, `@daily`, `@midnight`, `@hourly` are accepted. `@reboot` is REJECTED. |
| `every` | `30m` | Fixed interval from the daemon's start (or from `start_at` when given). |
| `at` | `2026-12-24T18:00:00+09:00` | One absolute Occurrence. Once it is claimed for execution or recorded as skipped, `completed` is derived from the `state.json` catch-up watermark and it is never scheduled again. `jobs.yaml` is never changed automatically. |

Backing implementation: gocron v2 `CronJob(spec, withSeconds=true)`, `DurationJob`, and
`OneTimeJob`. `withSeconds` is always true because robfig's `SecondOptional` parser still accepts
5-field expressions in that mode, so one code path serves both
(`docs/research/2026-09-02-gocron-scheduling-engine.md` §3). A `CRON_TZ=` prefix inside the
expression takes precedence over the `timezone` field; this is robfig behaviour and is documented
rather than suppressed.

Additional schedule fields:

| Field | Default | Meaning |
| --- | --- | --- |
| `timezone` | `local` | IANA name or `local` or `UTC`. Resolved at load; an unknown zone is a load error. |
| `start_at` | unset | Job is not scheduled before this instant. |
| `stop_at` | unset | Job is not scheduled after this instant. Reaching it does not disable the job; it just stops producing occurrences. |
| `catchup` | `latest` | `off` \| `latest` \| `all`. See §4.1. |
| `catchup_window` | `1h` for `at`; `168h` otherwise | How far back `latest`/`all` will look. A one-shot tied to a particular moment becomes unsafe or irrelevant sooner than a recurrence whose next Occurrence is still ahead. |
| `jitter` | `auto` | `auto`, `off`, or a duration. See §2.1. |

### 2.1 Jitter

Jitter is a **safety feature**, not a nicety: six agent jobs at `0 9 * * *` would otherwise launch
six agents into the same repository in the same second.

`auto` computes a per-job deterministic offset:

```
offset = FNV1a64(job.id) mod min(interval/2, 30m)
```

where `interval` is the gap between the two next occurrences. The offset is **stable for a given
job id** — the same job always starts at the same offset, which keeps `next-run` predictions
honest and makes the TUI's countdown stable. This follows Claude Code's task-id-derived jitter,
Claude Desktop's deterministic per-task offset, and systemd's `RandomizedDelaySec=` +
`FixedRandomDelay=` (all cited in `docs/research/2026-09-02-prior-art-and-domain-model.md` Q8).

For recurring schedules, Jitter is applied to `scheduler` and `catchup` triggers. It is NEVER
applied to `manual`. `nextRunAt` in the JSON reports the **jittered** instant, because that is
when a recurring job will actually run.

An `at` schedule is never jittered, regardless of trigger: it names one instant deliberately,
and moving that instant would make both the definition and `nextRunAt` dishonest. Its resolved
`jitterSec` is always `0`; an explicitly supplied `jitter` is ignored and produces a
`jitter_ignored` warning.

---

## 3. Kinds

### 3.1 `kind: shell`

```yaml
kind: shell
shell:
  command: go build ./... && go test ./...
  shell: auto        # auto | none | /bin/sh | pwsh | cmd
```

| Field | Default | Rules |
| --- | --- | --- |
| `command` | — | REQUIRED, non-empty. |
| `shell` | `auto` | `auto` → `/bin/sh -c` on Unix, `powershell -NoProfile -Command` on Windows. `none` → the command is split with shell-like quoting rules and exec'd directly, no interpreter. An explicit path is used as `<path> -c <command>`. |

Execution is a direct child process of the daemon, not a Herdr pane. Rationale: a shell job must
work when Herdr is not installed or its server is down
([`README.md`](README.md), packaging decision). stdout and stderr are captured to the run log,
interleaved, in arrival order, each line prefixed with nothing — the log is the raw stream so it
can be replayed verbatim.

Process group handling: the child is started in its own process group (`Setpgid` on Unix,
`CREATE_NEW_PROCESS_GROUP` on Windows). This two-file split is the pattern `adhocore/gronx` uses
(`docs/research/2026-09-02-prior-art-and-domain-model.md` §1.2).

**Cancellation and timeout MUST kill the whole group, not the leader**, and the runner MUST set
a bounded `WaitDelay`. Killing only the direct child looks like it works and then does not:
`sh -c "sleep 30"` execs `sleep` in place, so one kill is enough, but
`sh -c "sleep 20; echo done"` cannot exec, so the shell stays, `sleep` becomes a grandchild, and
that grandchild both survives the kill and keeps the inherited stdout pipe open. `Wait` then
blocks on the pipe, the terminal record is never appended, and the run stays `running` until the
grandchild happens to exit. On Unix this means signalling the negative pid; on Windows it means
`taskkill /T /F`.

Terminal outcome: `success` when the exit code is 0, `failure` otherwise. `exitCode` is recorded.

### 3.2 `kind: agent`

```yaml
kind: agent
agent:
  agent_kind: claude
  prompt: |
    …
  capture: transcript      # transcript | none
  no_op_marker: HEARTBEAT_OK
  session: herdr-cron      # Herdr session name, or "current"
  worktree: false          # or a branch name
  wait_timeout: 45m
```

| Field | Default | Rules |
| --- | --- | --- |
| `agent_kind` | `claude` | MUST be one of the kinds the installed `herdr` accepts for `agent start --kind`. Validated against `herdr agent start --help` at load time when Herdr is present, otherwise at run time. |
| `prompt` | — | REQUIRED, non-empty. A scheduler preamble is prepended; see §3.3. |
| `capture` | `transcript` | `transcript` records `herdr agent read --source recent-unwrapped` output into the run log. `none` records only the outcome. |
| `no_op_marker` | unset | When set and the captured transcript's final assistant text equals this marker exactly, the run's status is `no_op` rather than `success`. |
| `session` | `herdr-cron` | Herdr session name. `current` means the session the daemon itself lives in. |
| `worktree` | `false` | `false` runs in `cwd`. A string creates/reuses a Herdr worktree on that branch and runs there. |
| `wait_timeout` | job `timeout` | Passed to `agent prompt --wait --timeout`. |

The full execution sequence, its failure taxonomy, and the trust pre-flight are specified in
[`07-herdr-integration.md`](07-herdr-integration.md). The domain-level contract here is only:

- A run has a **captured output** and a **terminal outcome**, and they are different fields.
- `blocked` is a terminal outcome, not a retryable failure. An agent sitting on an approval
  dialog with nobody present will never resolve itself
  (`docs/research/2026-09-02-herdr-plugin-integration.md` §9.4).

### 3.3 The scheduler preamble

Every `kind: agent` run has this prepended to the prompt, verbatim, before any user text:

```
You are being run by herdr-cron on a schedule. There is no human watching this session.
Do not ask questions; if a required detail is missing, make the safest reasonable assumption
or stop and explain what was missing. Do not wait for approval. When you are done, state the
outcome in one line.
```

It is not configurable per job in v1, and it is not optional. Precedent: `ccs-biosphere`'s recipe
template makes exactly this its first prompt step
(`docs/research/2026-09-02-prior-art-and-domain-model.md` §1.6.5, Q8). Its absence is the
documented cause of a scheduled agent stalling forever on a question.

---

## 4. Run semantics

### 4.1 Catch-up (missed runs)

The daemon was off, or the machine slept. On start, and on every wake, the reconciliation pass
(§4.2) computes what should have fired since `state.lastScheduledAt` and applies the job's policy:

| `catchup` | Behaviour |
| --- | --- |
| `off` | No recurring Occurrence is replayed. A passed one-time Occurrence is still recorded as `skipped` with reason `catchup_off`, so the refusal is visible and is not reconsidered on every reload. |
| `latest` | **Default.** Exactly one run, for the most recently missed occurrence, discarding older ones. Only if that occurrence is within `catchup_window`. |
| `all` | Every missed occurrence within `catchup_window`, in chronological order, serialised (never in parallel), each subject to `limits`. Capped at 100 runs per job per pass; the overflow is recorded once as a `skipped` run with reason `catchup_capped`. |

`latest` is the default because it is what the closest shipping analogue chose: Claude Desktop
runs exactly one catch-up for the most recently missed time with a 7-day lookback and discards
anything older (`docs/research/2026-09-02-prior-art-and-domain-model.md` §1.6.2, Q3). gocron
itself provides none of this — it discards every missed tick by design, so all of §4.1 is
herdr-cron's own code (`docs/research/2026-09-02-gocron-scheduling-engine.md` §8).

Catch-up runs carry `trigger: "catchup"` and a deterministic `runId` derived from
`(jobId, scheduledAt)`, so replaying the same pass twice cannot double-run: a run record that
already exists for that id is skipped. This is dagu's `GenerateCatchupRunID` idea
(`prior-art` §1.1).

An `at` schedule has exactly one Occurrence, so reconciliation applies a total rule to it. If
the pending instant is within `catchup_window`, it executes with `trigger: "catchup"`. If it is
older than the window, reconciliation records `skipped` / `missed_window`; if `catchup` is
`off`, it records `skipped` / `catchup_off` instead. `latest` and `all` are indistinguishable
for one Occurrence. Being claimed for execution and both skipped outcomes advance
`state.lastScheduledAt`, which is also the source of the one-shot's derived `completed` field.

Reconciliation runs at scheduler start, after a `jobs.yaml` reload, and when a wall-clock jump
is detected. It first checks the watermark, so repeating any of those passes neither executes
nor records the same one-time Occurrence twice.

### 4.2 Reconciliation pass

Runs at daemon start, after a `jobs.yaml` reload, and when a wall-clock jump larger than 90
seconds is observed between two 30-second ticks (the sleep/resume detector; Go's monotonic clock
does not advance across suspend, so wall-clock comparison is the only signal —
`gocron` doc §8).

For each enabled recurring job: read `state.lastScheduledAt`, enumerate occurrences in
`(lastScheduledAt, now]` bounded by `catchup_window`, apply the policy, enqueue, and write the new
`lastScheduledAt` **before** executing anything. One-time jobs follow the total rule in §4.1,
including the skipped record for an Occurrence outside the window or disabled catch-up. A crash
mid-pass therefore re-runs at most the occurrences it had not yet claimed.

### 4.3 Overlap

The previous run of a job is still going when the next fires:

| `concurrency` | Behaviour |
| --- | --- |
| `skip` | **Default.** The new occurrence is recorded as a `skipped` run with reason `overlap` and does not execute. Recording it — rather than dropping it — is what makes "why did this not run at 03:00" answerable. |
| `queue` | Enqueued, executed after the running one, bounded to 1 waiting run per job. A second waiting occurrence displaces the first with reason `superseded`. |
| `cancel_previous` | The running one is cancelled (`cancelled`, reason `superseded`) and the new one starts. |
| `allow` | Both run. |

Implemented with gocron `WithSingletonMode` for `skip`/`queue`, and herdr-cron's own guard for
`cancel_previous` (`gocron` doc §4).

### 4.4 Retry

```yaml
retry:
  max_attempts: 2       # total attempts, not extra attempts. 1 = no retry.
  backoff: exponential  # exponential | fixed
  initial: 60s
  max_interval: 30m
```

Default is `max_attempts: 1` — **no retry**. This deliberately departs from river's default of 25
attempts, which is calibrated for free webhook deliveries and is the wrong default when one
attempt is an LLM invocation that costs money (`prior-art` §1.5, Q5).

Exponential backoff is `initial * 2^(attempt-1)` with ±10% deterministic jitter, clamped to
`max_interval` (default `30m`).

Retries are NEVER attempted for these terminal outcomes: `blocked`, `no_op`, `skipped`,
`cancelled`, `cwd_missing`, and `limit_exceeded`. Retrying a job that is blocked on a human
approval dialog burns the daily limit and changes nothing.

### 4.5 Limits and the circuit breaker

```yaml
limits:
  max_runs_per_day: 24          # 0 = unlimited
  max_consecutive_failures: 3   # 0 = never auto-disable
```

Defaults: `max_consecutive_failures: 3` for every kind; `max_runs_per_day` is `0` for
`kind: shell` and `24` for `kind: agent`. The asymmetry is intentional — a shell job is nearly
free, an agent job is not.

- `max_runs_per_day` counts runs whose `startedAt` falls in the current day in the job's
  timezone, excluding `skipped`. Exceeding it records a `skipped` run with reason
  `limit_exceeded`.
- `max_consecutive_failures` counts consecutive terminal outcomes in `{failure, timeout}`.
  `blocked` also increments it — an agent that cannot start is not a transient fault. Reaching
  the limit sets an `enabled` override to `false` with `disabledReason: auto_failures` and emits
  a `auto_disabled` notification. `job resume` clears it.

There is no prior art for a money circuit breaker; the nearest analogues are GitHub's 60-day
idle auto-disable and Claude Code `/loop`'s 7-day expiry (`prior-art` Q8). This design is
therefore marked as a deliberate invention, not a copied pattern.

### 4.6 Notifications

```yaml
notify:
  on: [failure, blocked, auto_disabled]   # any of: success, no_op, failure, timeout, blocked, auto_disabled, catchup
  command: ["herdr-hitl", "notify", "-t", "{{.JobName}}", "-m", "{{.Summary}}"]
```

`command` defaults to the built-in Herdr notifier, equivalent to
`herdr notification show "<job name>" --body "<summary>"`. Delivery is **best effort**: a
notifier that fails, is missing, or returns `shown: false` is logged at warn level and does not
change the run's outcome. This is not defensive coding — `notification show` provably returns
`{"shown": false, "reason": "no_foreground_client"}` on a headless server
(`herdr-plugin-integration` §9.5), which is the normal case for a scheduler.

---

## 5. Effective `enabled`

`enabled` is declared in `jobs.yaml` and may be overridden in `state.json`. This split exists so
that clicking the toggle in the TUI never rewrites a user's authored YAML — comment loss and
formatting churn on every click is the cost the alternative pays
(`prior-art` Q2, Claude Desktop precedent).

Resolution:

1. If `state.overrides[id]` exists **and** `state.overrides[id].declaredEnabled` equals the
   current `jobs.yaml` value, the override wins. `enabledSource: "override"`.
2. Otherwise the file value wins and any stale override is discarded.
   `enabledSource: "file"`.

Rule 1's second clause is what makes the file authoritative again after a human edits it: change
`enabled: true` → `false` in YAML and the TUI's old "resume" override evaporates. Editing the
file is always the stronger act.

`job pause` / `job resume` write the override. `job add --paused` writes `enabled: false` into
the YAML instead, because a job that has never run has no state worth overriding.

---

## 6. Run record

```json
{
  "runId": "nightly-deps-20260902T181700Z",
  "jobId": "nightly-deps",
  "trigger": "scheduler",
  "attempt": 1,
  "scheduledAt": "2026-09-03T03:17:00+09:00",
  "startedAt": "2026-09-03T03:29:23+09:00",
  "finishedAt": "2026-09-03T03:44:02+09:00",
  "durationSec": 879,
  "status": "no_op",
  "exitCode": 0,
  "reason": null,
  "logPath": "logs/nightly-deps/nightly-deps-20260902T181700Z.log",
  "outputExcerpt": "HEARTBEAT_OK",
  "host": "huke-desktop",
  "herdr": {"session": "herdr-cron", "paneId": "w1:p3", "agentName": "cron-nightly-deps"}
}
```

`trigger`: `scheduler` | `manual` | `catchup` | `retry` | `startup`.

`status`, the terminal outcome:

| Status | Meaning |
| --- | --- |
| `running` | Not terminal. Written at start, replaced on completion. |
| `success` | Shell exit 0, or agent run completed. |
| `no_op` | Completed, and the output equalled `no_op_marker`. "Ran and correctly did nothing" is a distinct and common outcome for an agent job; without it a week of history is 300 identical green rows (`prior-art` §1.6, agent-cron's `HEARTBEAT_OK`). |
| `failure` | Non-zero exit, or the agent run could not complete. |
| `timeout` | Killed at `timeout`. |
| `blocked` | The agent is sitting on an approval or question UI. Terminal, never retried, always notified. |
| `skipped` | Never executed. `reason` is `overlap`, `limit_exceeded`, `disabled`, `catchup_capped`, `superseded`, `missed_window`, or `catchup_off`. |
| `cancelled` | Killed by `cancel_previous`, by shutdown, or by `job cancel`. |

`reason` is a stable machine-readable string, or `null` when there is nothing to add beyond the
status. It is a single union across all statuses:

| Status | Legal `reason` values |
| --- | --- |
| `skipped` | `overlap`, `limit_exceeded`, `disabled`, `catchup_capped`, `superseded`, `missed_window`, `catchup_off` |
| `failure` | `daemon_died`, `cwd_missing`, `herdr_unavailable`, `herdr_version_unsupported`, `herdr_unexpected`, `agent_vanished`, `agent_name_collision`, `pane_lost`, `agent_unknown`, `notifier_failed` (never on its own — a notifier failure never sets the status), or `null` for an ordinary non-zero exit |
| `timeout` | `job_timeout`, `wait_timeout`, `agent_prompt_stalled` |
| `blocked` | `agent_blocked`, `agent_startup_dialog`, `cwd_not_trusted` |
| `cancelled` | `superseded`, `shutdown`, `user` |
| `success`, `no_op`, `running` | `null` |

The agent-specific members are specified, with the Herdr signal each is derived from, in
[`07-herdr-integration.md`](07-herdr-integration.md) §4. An unrecognised Herdr condition maps to
`failure` / `herdr_unexpected` with the raw response logged — the table is total by fallback,
because Herdr's own error codes have no published enum.

`runId` is `<jobId>-<scheduledAt in UTC as 20060102T150405Z>`, deterministic in
`(jobId, scheduledAt)`. Manual runs, which have no scheduled instant, use the invocation time and
append `-m`. Retries reuse the base id and append `-r<attempt>`. Determinism is what makes a
repeated catch-up pass idempotent (§4.1).

`outputExcerpt` is the last 2 KiB of captured output, or less. The full output is in `logPath`.
Run records are never allowed to grow without bound; that is what the log file is for.

No `cost` field in v1. Only `agent-cron` in the surveyed corpus models token cost, and it is not
observable through the Herdr pane surface — the transcript is text, not a usage record
(`prior-art` "Could not verify"). Inventing a field herdr-cron cannot populate would be worse
than omitting it.

---

## 7. Validation

`herdr-cron validate` and every write command apply, in order:

1. **Syntax** — YAML parses, `version` is known, no unknown keys.
2. **Schema** — required fields, enum members, id pattern, id uniqueness, duration format.
3. **Semantics** — schedule parses (through the same `NewDefaultCron` gocron uses, so the CLI's
   verdict and the daemon's behaviour cannot diverge — `gocron` doc §10); timezone resolves;
   `cwd` is absolute; exactly one kind payload matches `kind`.
4. **Environment** (warnings, not errors) — `cwd` exists; for `kind: agent`, Herdr is on `PATH`,
   the agent kind is supported, and the `cwd` passes the trust pre-flight
   ([`07-herdr-integration.md`](07-herdr-integration.md) §5).

Levels 1–3 are errors and block the write. Level 4 produces warnings that are printed and
returned in JSON but do not block: a job may legitimately be authored on a machine where its
target repo has not been cloned yet.
