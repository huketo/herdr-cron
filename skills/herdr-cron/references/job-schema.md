# `jobs.yaml` reference

The authored definition file. Written by humans and by agents, diffable, safe to commit.
Location: `herdr-cron status` reports it as `result.roots.jobs`.

Unknown keys are a **load error**, not a warning — a typo in `catchup_window` must not silently
disable catch-up. Durations are Go duration strings (`45m`, `1h30m`, `10s`); a bare number is
rejected because `timeout: 30` is ambiguous.

## Shape

```yaml
version: 1                      # required; 1 is the only supported version

defaults:                       # optional; applied to every job before validation
  timezone: local
  timeout: 30m
  concurrency: skip
  jitter: auto
  catchup: latest
  catchup_window: 168h
  retry: { max_attempts: 1 }
  limits: { max_consecutive_failures: 3 }
  notify: { on: [failure, blocked, auto_disabled] }

jobs:
  - id: nightly-deps            # required, ^[a-z0-9][a-z0-9._-]{0,127}$, unique, stable
    name: Nightly dependency audit
    description: Check for outdated deps and report.
    enabled: true               # the declared value; a pause/resume override can win
    tags: [maintenance, repo:herdr]

    schedule:                   # required; exactly one of cron, every, at
      cron: "17 3 * * 1-5"
      timezone: Asia/Seoul
      start_at: 2026-09-01T00:00:00+09:00
      stop_at: 2027-01-01T00:00:00+09:00
      catchup: latest           # off | latest | all
      catchup_window: 24h
      jitter: auto              # auto | off | a duration

    kind: agent                 # shell | agent
    agent:                      # required when kind: agent
      agent_kind: claude
      prompt: |
        Audit dependencies in this repo. If everything is current,
        reply with exactly HEARTBEAT_OK and stop.
      capture: transcript       # transcript | none
      no_op_marker: HEARTBEAT_OK
      session: herdr-cron       # a Herdr session name, or "current"
      worktree: false           # false, or a branch name
      wait_timeout: 45m

    cwd: ~/src/herdr            # expanded, must be absolute
    env:
      GIT_AUTHOR_NAME: herdr-cron
    timeout: 45m                # 0 inherits the default, -1 means never

    concurrency: skip           # skip | queue | cancel_previous | allow
    retry:
      max_attempts: 2           # total attempts, not extra ones; 1 = no retry
      backoff: exponential      # exponential | fixed
      initial: 60s
      max_interval: 30m

    limits:
      max_runs_per_day: 4       # 0 = unlimited
      max_consecutive_failures: 3

    notify:
      on: [failure, auto_disabled]
      command: ["herdr-hitl", "notify", "-t", "{{.JobName}}", "-m", "{{.Summary}}"]

  - id: build-smoke
    schedule: { every: 30m }
    kind: shell
    shell:
      command: go build ./... && go test ./internal/scheduler/...
      shell: auto               # auto | none | an interpreter path
    cwd: ~/src/herdr
    timeout: 10m
```

## Schedule forms

| Form | Example | Meaning |
| --- | --- | --- |
| `cron` | `"17 3 * * 1-5"` | 5 or 6 fields (6 = seconds first). Descriptors `@yearly`, `@annually`, `@monthly`, `@weekly`, `@daily`, `@midnight`, `@hourly`. `@reboot` is rejected. A `CRON_TZ=` prefix inside the expression beats the `timezone` field. |
| `every` | `30m` | Fixed interval. |
| `at` | `2026-12-24T18:00:00+09:00` | One-time. After it fires the job never runs again. |

## Defaults that matter

| Field | Default | Note |
| --- | --- | --- |
| `enabled` | `true` | Overridden by `job pause`/`job resume`, which write state, not this file |
| `timeout` | `30m` | Exceeding it records `timeout`; the whole process group is killed |
| `concurrency` | `skip` | The skipped occurrence is *recorded* with `reason: "overlap"` |
| `catchup` | `latest` | Exactly one run for the most recently missed occurrence |
| `catchup_window` | `168h` | Older missed occurrences are discarded |
| `jitter` | `auto` | Deterministic per job id, up to `min(interval/2, 30m)` |
| `retry.max_attempts` | `1` | No retry. Agent runs cost money |
| `limits.max_consecutive_failures` | `3` | Then the job auto-disables with `reason: auto_failures` |
| `limits.max_runs_per_day` | `0` shell, `24` agent | `0` is unlimited |

`blocked`, `no_op`, `skipped`, `cancelled`, `cwd_missing` and `limit_exceeded` are **never**
retried.

## Effective `enabled`

`enabled` is declared here and may be overridden in the state store, so a pause never rewrites
this file. The override records what this file said when it was created; edit `enabled` here and
the override is discarded. `job get` reports which won as `enabledSource`: `"file"` or
`"override"`.
