# Response shapes and error codes

Every command emits one envelope: `id`, then exactly one of `result` or `error`. `result.type`
names the payload. `-o json` is the default.

## Payload types

| Command | `result.type` | Payload |
| --- | --- | --- |
| `job list` | `job_list` | `generatedAt`, `daemon`, `jobs[]` |
| `job get` | `job` | `job`, `nextRuns[]`, `recentRuns[]` |
| `job add`, `job update` | `job_written` | `job`, `nextRuns[]`, `warnings[]`, `dryRun` |
| `job rm` | `job_removed` | `id`, `purged` |
| `job pause`, `job resume` | `job_enabled_changed` | `id`, `enabled`, `enabledSource`, `reason` |
| `job run` | `run_started` | `runId`, `jobId`, `wait` |
| `job run --wait`, `run-once` | `run` | `run` |
| `job cancel` | `run_cancelled` | `jobId` |
| `run list` | `run_list` | `runs[]` |
| `run get` | `run` | `run` |
| `run logs -o json` | `log_line` | one object per line: `runId`, `line` |
| `status` | `status` | `daemon`, `roots`, `jobCount`, `configError`, `nextRuns[]` |
| `reload` | `reload_requested` | `accepted` |
| `validate` | `validation` | `valid`, `errors[]`, `warnings[]`, `scheduleType`, `nextRuns[]`, `jobs[]` |
| `schema` | `schema` | `commands[]` |
| `service …` | `service` | `driver`, `installed`, `entries[]` |
| `install-cli` | `install_cli` | `path`, `linked` |

## A resolved job

`job get` returns the canonical record: defaults applied, `~` expanded, durations in seconds,
keys in `lowerCamelCase`.

```json
{
  "id": "nightly-deps",
  "name": "Nightly dependency audit",
  "enabled": true,
  "enabledSource": "file",
  "tags": ["maintenance"],
  "schedule": {
    "type": "cron", "expression": "17 3 * * 1-5", "timezone": "Asia/Seoul",
    "catchup": "latest", "catchupWindowSec": 86400, "jitterSec": 743
  },
  "kind": "agent",
  "payload": {
    "agentKind": "claude", "prompt": "…", "capture": "transcript",
    "noOpMarker": "HEARTBEAT_OK", "session": "herdr-cron"
  },
  "cwd": "/home/huke/src/herdr",
  "timeoutSec": 2700,
  "concurrency": "skip",
  "retry": {"maxAttempts": 2, "backoff": "exponential", "initialSec": 60, "maxIntervalSec": 1800},
  "limits": {"maxRunsPerDay": 4, "maxConsecutiveFailures": 3},
  "notify": {"on": ["failure", "auto_disabled"]}
}
```

## A run record

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

`runId` is `<jobId>-<scheduledAt in UTC as 20060102T150405Z>`; a manual run appends `-m` and a
retry appends `-r<attempt>`.

`trigger`: `scheduler` | `manual` | `catchup` | `retry` | `startup`.

| `status` | Meaning |
| --- | --- |
| `running` | Not terminal; replaced by a second record when the run ends |
| `success` | Shell exit 0, or the agent run completed |
| `no_op` | Completed, and the output equalled `no_op_marker` |
| `failure` | Non-zero exit, or the agent run could not complete |
| `timeout` | Killed at `timeout` |
| `blocked` | The agent is on an approval or question UI. Terminal, never retried |
| `skipped` | Never executed; `reason` says why |
| `cancelled` | Killed by `cancel_previous`, shutdown, or `job cancel` |

`reason` values, by status:

| Status | Values |
| --- | --- |
| `skipped` | `overlap`, `limit_exceeded`, `disabled`, `catchup_capped`, `superseded` |
| `failure` | `daemon_died`, `cwd_missing`, `herdr_unavailable`, `herdr_version_unsupported`, `herdr_unexpected`, `agent_vanished`, `agent_name_collision`, `pane_lost`, `agent_prompt_stalled`, `agent_idle_fallback_unverified`, or `null` for an ordinary non-zero exit |
| `timeout` | `job_timeout`, `wait_timeout`, `agent_unknown` |
| `blocked` | `agent_blocked`, `agent_startup_dialog`, `cwd_not_trusted` |
| `cancelled` | `superseded`, `shutdown`, `user` |

## Error codes

Stable. Branch on these, never on `message`.

| Code | Meaning |
| --- | --- |
| `usage` | Bad flags or arguments (exit 2) |
| `config_invalid` | `jobs.yaml` failed validation; `error.details` lists the problems |
| `config_conflict` | The file changed between read and write; re-read and retry |
| `job_not_found` | No such job id |
| `job_exists` | `job add` with an id already present |
| `run_not_found` | No such run id |
| `daemon_unreachable` | No live daemon claimed the request; use `run-once` |
| `daemon_already_running` | `daemon` started while the lock is held |
| `herdr_unavailable` | No `herdr` binary, or no reachable server |
| `agent_blocked` | The agent stopped at an approval or question UI (exit 3) |
| `cwd_missing` | The job's `cwd` does not exist |
| `limit_exceeded` | A run was refused by `limits` |
| `run_failed` | With `--wait` or `run-once`, the run ended `failure`/`timeout`/`cancelled` |
| `io_error` | Filesystem failure |
| `internal` | A bug; set `HERDR_CRON_DEBUG=1` for a stack trace |

## Validate warnings

`validate` warnings are objects with `code`, `message`, `jobId`, `field`. Codes: `cwd_missing`,
`cwd_not_trusted`, `herdr_unavailable`, `trust_unknown`. They never block a write.

## Environment

| Variable | Effect |
| --- | --- |
| `HERDR_CRON_CONFIG`, `HERDR_CRON_STATE_DIR`, `HERDR_CRON_HOME` | Path overrides |
| `XDG_CONFIG_HOME`, `XDG_STATE_HOME` | Honoured on all three platforms |
| `HERDR_CRON_TRIGGER` | Run provenance recorded by `run-once`; OS-scheduler entries set `scheduler` |
| `HERDR_BIN_PATH` | Path to the `herdr` binary, checked before `PATH` |
| `HERDR_CRON_DEBUG` | `1` adds a stack trace to `internal` errors |
