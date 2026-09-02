# Troubleshooting: symptom to cause

Work down the table. Every row names the command that confirms the cause.

## The job never ran

| Symptom | Cause | Confirm | Fix |
| --- | --- | --- | --- |
| No run record at all | The schedule never fires what the user meant | `herdr-cron validate --schedule "<expr>" --next 5` | Correct the expression; day-of-month plus day-of-week is an OR in cron |
| No run record, schedule is right | Nothing is scheduling | `herdr-cron status` → `result.daemon.status` | `running` is required. `stopped` means no daemon; `stale` means one crashed |
| `status.result.configError` is non-null | The whole file was rejected, so nothing reloaded | `herdr-cron validate` | Fix the reported job; a broken file keeps the previous schedule, it does not half-apply |
| `job get` shows `enabled: false`, `enabledSource: "override"` | Paused, or the circuit breaker fired | `job get <id>` → `state.consecutiveFailures` | `job resume <id>` once the cause is fixed |
| `skipped` run with `reason: "overlap"` | The previous run was still going | `run list --job <id> --status all` | Raise `timeout`, or set `concurrency: queue` |
| `skipped` with `reason: "limit_exceeded"` | `max_runs_per_day` reached | `job get <id>` → `limits`, `state.runsToday` | Raise the limit deliberately; it exists to bound spend |
| `skipped` with `reason: "catchup_capped"` | More than 100 occurrences were missed | — | Expected after long downtime with `catchup: all` |

## The job ran and failed

| Symptom | Cause | Confirm |
| --- | --- | --- |
| `failure`, `exitCode` non-zero, `reason: null` | The command itself failed | `run logs <run-id> --tail 200` |
| `failure`, `reason: "cwd_missing"` | The working directory does not exist on this machine | `job get <id>` → `cwd` |
| `failure`, `reason: "daemon_died"` | The daemon was killed mid-run; the next start closed the record | `status` |
| `timeout`, `reason: "job_timeout"` | The run exceeded `timeout`; the whole process group was killed | `job get <id>` → `timeoutSec` |
| `failure`, `reason: "herdr_unavailable"` | No `herdr` binary, or its server would not start | `command -v herdr && herdr --version` |
| `failure`, `reason: "herdr_version_unsupported"` | Herdr is older than 0.8.2 | `herdr --version` |

## The agent job is blocked

`blocked` is terminal. It is never retried, and retrying by hand does nothing.

| `reason` | Cause | Fix |
| --- | --- | --- |
| `cwd_not_trusted` | The agent has never been trusted for that directory, so a scheduled start would park on a safety dialog | Once, interactively: `cd <cwd> && claude`, answer "Yes, I trust this folder", exit. Then `herdr-cron validate` must report no `cwd_not_trusted` warning |
| `agent_startup_dialog` | The agent showed a dialog during startup; the pane was left open on purpose | Attach and read it: `herdr session attach herdr-cron`, then look at the pane named in `run.herdr.paneId` |
| `agent_blocked` | The agent asked a question mid-run | Same. Then make the prompt self-sufficient — the scheduler preamble already forbids questions, so a blocked run usually means the prompt lacked a required detail |

`failure` with `reason: "agent_idle_fallback_unverified"` is the near miss: the agent settled to
an idle state Herdr could not classify **and** produced no output. Treat it as a probable stalled
dialog, not a successful empty run.

## The CLI itself failed

| Exit / code | Meaning | What to do |
| --- | --- | --- |
| exit 2, `usage` | The command line was wrong | Re-read `herdr-cron schema --command "<path>"`. Do not retry the same line |
| exit 1, `daemon_unreachable` | No daemon claimed the request within the grace period | Use `run-once <id>` instead. Do not start a daemon unasked |
| exit 1, `config_conflict` | `jobs.yaml` changed between read and write | Re-read and retry once |
| exit 1, `job_exists` | The id is taken | `job get <id>` first; use `job update` |
| exit 3 | The run is `blocked` | Stop and tell the human |
| exit 1, `internal` | A bug in herdr-cron | Re-run with `HERDR_CRON_DEBUG=1` and report the trace |

## Things that look broken and are not

- **A `no_op` run.** It means the job ran and correctly did nothing — the agent's answer matched
  `no_op_marker`. A week of `no_op` is a healthy heartbeat, not a stuck job.
- **`nextRunAt` is later than the cron expression suggests.** Deterministic jitter is added on
  purpose, up to `min(interval/2, 30m)`, and it is stable for a given job id.
- **Exactly one `catchup` run after long downtime.** `catchup: latest` is the default: it replays
  the most recently missed occurrence and discards the rest.
- **`herdr notification` reported nothing.** Notifications are best effort and return
  `shown: false` on a headless server. The log file and the run record are the durable record.
- **The TUI shows a job the daemon is not running.** The TUI reads files and owns no scheduler;
  check the header badge for daemon liveness.
