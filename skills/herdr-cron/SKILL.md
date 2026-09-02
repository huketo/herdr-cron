---
name: herdr-cron
description: "Schedule and inspect automated work with the herdr-cron CLI: cron jobs, recurring shell commands, and scheduled coding-agent prompts. Use when the user asks to schedule, automate, or run something nightly, hourly, or on a cron; to list, add, edit, pause, or delete scheduled jobs; to check why a scheduled job did not run; or to read a job's run history or logs. Requires the herdr-cron binary on PATH."
allowed-tools: Bash
license: MIT
---

# herdr-cron

herdr-cron schedules two kinds of work: `shell` jobs, which run a command in a child process,
and `agent` jobs, which deliver a prompt to a coding agent running in a Herdr pane. Job
definitions live in `jobs.yaml`, run history lives in JSONL files, and everything is driven
through the `herdr-cron` CLI. There is no API and no socket.

The rules below are standing rules: they apply to every command in this task, not just the first.

## Check the preconditions

```bash
command -v herdr-cron && herdr-cron status
```

If the binary is missing, say so and stop; do not install it unasked. `status` never needs a
daemon — it reports daemon liveness, the config and state roots, job counts, any config error,
and the next occurrences. Read `.result` from it before assuming anything about this machine.

## Know whether the daemon is needed

Most work does not need one. Check this table before deciding a failure is your fault:

| Command | Needs a running daemon |
| --- | --- |
| `job list`, `job get`, `run list`, `run get`, `run logs`, `status`, `validate`, `schema` | No — file reads |
| `job add`, `job update`, `job rm` | No — the daemon picks the change up when it next runs |
| `job pause`, `job resume` | No — writes a state override |
| `run-once <job-id>` | No — it *is* the runner |
| `job run`, `job cancel`, `reload` | Yes, or the call fails `daemon_unreachable` |

If `job run` returns `daemon_unreachable`, do not retry it and do not start a daemon on your own
initiative. Run the job in the foreground instead:

```bash
herdr-cron run-once nightly-deps
```

## The installed binary is the authority

Command syntax comes from the binary, never from memory and never from this file:

```bash
herdr-cron --help
herdr-cron schema
herdr-cron schema --command "job add"
```

`schema` prints the whole command tree as JSON: every command, every flag, its type, default,
and whether it is required. Use it instead of guessing a flag name. Do not run bare `herdr-cron`
for discovery: with a TTY it launches the TUI, and without one it is a usage error.

## Read JSON, never the text rendering

`-o json` is the default. Every data command returns one envelope with `id` and exactly one of
`result` or `error`:

```json
{"id": "cli:job:list", "result": {"type": "job_list", "jobs": []}}
{"id": "cli:job:get", "error": {"code": "job_not_found", "message": "no job with id \"nightl-deps\"", "hint": "did you mean \"nightly-deps\"?"}}
```

- Read values from `.result`. `-o text` carries no compatibility promise.
- Branch on `.error.code`, never on `.error.message`.
- Report an `error.code` you do not recognise rather than guessing at its meaning.
- Take job ids from `job list`. Never invent one.

One exception: `run logs` streams raw log text rather than an envelope. With `-o json` each line
is wrapped as `{"type":"log_line","runId":…,"line":…}`.

## Exit codes

| Code | Meaning | What to do |
| --- | --- | --- |
| `0` | Success. With `--wait`, the run finished `success`, `no_op`, or `skipped` | Continue |
| `1` | Failure, or with `--wait` the run finished `failure`, `timeout`, or `cancelled` | Read the error, fix the cause |
| `2` | Usage error | Re-read `schema`; do not retry the same line |
| `3` | With `--wait`, the run finished `blocked` | **Stop and tell the human.** Do not retry |

Exit 3 means a human is required. It is an agent job sitting on an approval or question dialog
that no automated retry can clear. Retrying burns the job's daily run limit and changes nothing.

## Validate a schedule before writing it

Always, on every add and every update. It costs one command and it is the difference between a
job and a job that silently never fires:

```bash
herdr-cron validate --schedule "17 3 * * 1-5" --next 5
```

This parses through the same code the daemon uses and prints the next five fire times; read them
back and confirm they are what the user asked for. With no `--schedule` it validates the whole
`jobs.yaml`. `--schedule` accepts all three shapes: a cron expression (`"17 3 * * 1-5"`, or a
descriptor such as `@daily`), a duration for a fixed interval (`30m`), or an RFC 3339 instant
for a one-time run (`2026-12-24T18:00:00+09:00`). `@reboot` is rejected.

## Add a job

```bash
# a shell job
herdr-cron job add --id build-smoke --schedule 30m \
  --command 'go build ./... && go test ./internal/scheduler/...' \
  --cwd ~/src/herdr --timeout 10m
```

```bash
# an agent job
herdr-cron job add --id nightly-deps --schedule "17 3 * * 1-5" --timezone Asia/Seoul \
  --prompt 'Audit dependencies in this repo. If everything is current, reply with exactly HEARTBEAT_OK and stop.' \
  --cwd ~/src/herdr --timeout 45m --no-op-marker HEARTBEAT_OK --max-runs-per-day 4
```

`--command` implies `kind: shell`; `--prompt` implies `kind: agent`, and passing both is a usage
error. `--dry-run` prints the resolved job and its next five fire times without writing
anything; `--paused` authors a job that does not start firing yet.

## Agent jobs need more care than shell jobs

- They run in the Herdr session named `herdr-cron` by default. Override with `--session` only
  when the user asks for a specific session.
- A scheduler preamble is prepended to every prompt, telling the agent no human is watching and
  not to ask questions. Write the prompt assuming that preamble is there.
- Give the job a `--no-op-marker` when "ran and correctly did nothing" is a likely outcome; the
  run is then recorded `no_op` instead of `success`, which keeps the history readable.
- An agent started in a directory it has never been trusted with returns `agent_not_ready` and
  waits on a trust dialog forever. This is the normal unattended failure. Before scheduling an
  agent job in a new directory, confirm `validate` reports no `cwd_not_trusted` warning for it.
- `herdr_unavailable` means no `herdr` binary or no reachable server. An agent job cannot run
  without one; a shell job can.

## Safety rules that always apply

- Leave `jitter` on `auto`. It staggers jobs that share a fire time; six agent jobs at
  `0 9 * * *` would otherwise start six agents in the same repository in the same second.
- Never set `max_consecutive_failures: 0`. Three consecutive `failure`, `timeout`, or `blocked`
  outcomes auto-disable a job on purpose; `job resume` clears it once the cause is fixed.
- Keep `max_runs_per_day` at 24 or below for agent jobs. An agent run costs money.
- Leave `catchup` on `latest` unless asked: it replays exactly the most recently missed
  occurrence after downtime, where `all` can replay many at once.
- Do not raise `retry.max_attempts` to work around a `blocked` run. Blocked is terminal.
- Prefer `job pause` to `job rm`; pausing leaves the user's authored YAML untouched. Never
  delete a job you did not create, and never pass `--purge` — which destroys run history and
  logs — unless the user explicitly asked for that.

## Diagnose a job that did not run

In this order:

```bash
herdr-cron status
herdr-cron job get <job-id>
herdr-cron run list --job <job-id> --status all --limit 20
herdr-cron run logs <run-id> --tail 200
```

- `status.result.configError` non-null means the whole file was rejected and nothing reloaded.
- `job get` shows the effective `enabled`, `enabledSource`, `nextRunAt`, and
  `state.consecutiveFailures`. `enabledSource: "override"` means someone paused it or the
  circuit breaker fired.
- A `skipped` run always carries a `reason`: `overlap`, `limit_exceeded`, `disabled`,
  `catchup_capped`, or `superseded`. That field answers "why did this not run at 03:00".
- No run record at all means the schedule never fired; re-check it with `validate --schedule`.

## Reference files

- Full `jobs.yaml` schema, every field and enum: [references/job-schema.md](references/job-schema.md)
- Response shapes, run records, and the complete error-code table: [references/json-shapes.md](references/json-shapes.md)
- Symptom-to-cause diagnosis: [references/troubleshooting.md](references/troubleshooting.md)
