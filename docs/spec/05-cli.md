---
title: herdr-cron — CLI specification
date: 2026-09-02
status: spec (normative)
---

# CLI specification

Normative. The CLI is the primary interface: the TUI is a client of the same code paths, and the
Agent Skill ([`08-agent-skill.md`](08-agent-skill.md)) teaches an agent this surface and nothing
else.

Design rules, each traced to evidence in `docs/research/2026-09-02-agent-skill-and-cli-ux.md`:

- **JSON is the default on every data command.** `herdr` itself emits JSON from every control
  command; an agent must never have to parse a table (B2).
- **One response envelope**, copied from herdr: `id` correlates, then exactly one of `result` or
  `error` (B2).
- **Errors are JSON on stderr with a stable `code`.** Prose changes; codes do not.
- **The command tree is introspectable** — `herdr-cron schema` (B1).
- **The skill ships inside the binary** — `herdr-cron --skill`, the pattern verified
  byte-identical for `herdr --skill` (A7).

Framework: **`spf13/cobra`**. It is the boring choice, it is what `gh` and `glab` use, its hidden
`__complete` protocol already gives agents a machine-readable command tree, and it generates
completions for all four shells. `urfave/cli/v3`'s JSON-serialisable command tree would have
made `schema` free, but walking `cmd.Commands()` to produce the same JSON is roughly forty lines
(B1).

---

## 1. Invocation

```
herdr-cron                       Launch the TUI
herdr-cron <command> [flags]
herdr-cron --skill               Print the bundled SKILL.md and exit
herdr-cron --version | -V
herdr-cron --help | -h
```

Bare `herdr-cron` with a TTY on stdout launches the TUI. Bare `herdr-cron` **without** a TTY is a
usage error (exit 2) telling the caller to pick a subcommand — an agent that accidentally
launches a TUI into a pipe is a hang, and hangs are worse than errors. This mirrors the
"never run bare `herdr`" rule that Herdr's own skill has to state in prose.

### 1.1 Global flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--output`, `-o` | `json` | `json` \| `text`. `text` is the human rendering; `json` is the envelope in §2. |
| `--config PATH` | see [`04-storage.md`](04-storage.md) §1 | `jobs.yaml` location. |
| `--state-dir PATH` | see `04-storage.md` §1 | State root. |
| `--quiet`, `-q` | off | Suppress warnings on stderr. Never suppresses errors. |
| `--no-color` | auto | Also honours `NO_COLOR`. Irrelevant with `-o json`, which is never coloured. |

`text` output is not a stable interface and carries no compatibility promise. `json` is.

### 1.2 Environment

| Variable | Meaning |
| --- | --- |
| `HERDR_CRON_CONFIG`, `HERDR_CRON_STATE_DIR`, `HERDR_CRON_HOME` | Path overrides ([`04-storage.md`](04-storage.md) §1). |
| `XDG_CONFIG_HOME`, `XDG_STATE_HOME` | Honoured on all three platforms (`04-storage.md` §1). |
| `HERDR_CRON_TRIGGER` | Sets the run-trigger provenance recorded by `run-once`; one of `scheduler`, `manual`, `catchup`, `retry`, `startup`. Defaults to `manual`. Generated OS-scheduler entries set it to `scheduler` so their runs are not misfiled as hand-invoked ([`02-architecture.md`](02-architecture.md) §2.1, §4.3). |
| `HERDR_BIN_PATH` | Path to the `herdr` binary, checked before `PATH` ([`07-herdr-integration.md`](07-herdr-integration.md) §1). |
| `HERDR_ENV` | Set to `1` by Herdr inside a managed pane; herdr-cron uses it only to decide whether the plugin front door is available. |
| `HERDR_PLUGIN_STATE_DIR` | Ignored. Corrected 2026-09-02: honouring it gave the plugin-started daemon a different state root — and therefore a different `daemon.lock` — from a terminal-started one, so both ran at once and every job fired twice (`04-storage.md` §1). |
| `HERDR_CRON_DEBUG` | `1` adds a stack trace to `internal` errors. |
| `NO_COLOR` | Disables colour in `-o text`. |

---

## 2. Response envelope

Success, on stdout, exit 0:

```json
{"id": "cli:job:list", "result": {"type": "job_list", "jobs": [ … ]}}
```

Failure, on stdout **and** stderr — the envelope goes to stdout so a piped agent gets structured
data, a one-line human summary goes to stderr — exit non-zero:

```json
{"id": "cli:job:get", "error": {"code": "job_not_found", "message": "no job with id \"nightl-deps\"", "hint": "did you mean \"nightly-deps\"?"}}
```

`id` is `cli:<group>:<command>` for grouped commands (`cli:job:list`, `cli:run:get`) and
`cli:<command>` for the ungrouped ones (`cli:status`, `cli:validate`, `cli:schema`,
`cli:reload`, `cli:run-once`). `error.hint` is optional and advisory.

`result.type` names the payload shape and is REQUIRED on every success envelope. The complete
set:

| Command | `result.type` | Payload |
| --- | --- | --- |
| `job list` | `job_list` | `generatedAt`, `daemon`, `jobs[]` (summary records; one-shot records also carry `completed`) |
| `job get` | `job` | `job` (full resolved record), `completed` for a one-shot, `nextRuns[]`, `recentRuns[]` |
| `job add`, `job update` | `job_written` | `job`, `warnings[]` |
| `job rm` | `job_removed` | `id`, `purged` (bool) |
| `job pause`, `job resume` | `job_enabled_changed` | `id`, `enabled`, `enabledSource`, `reason` |
| `job run` | `run_started` | `runId`, `jobId`, `wait` (bool) |
| `job run --wait`, `run-once` | `run` | `run` (full run record) |
| `job cancel` | `run_cancelled` | `runId`, `jobId` |
| `run list` | `run_list` | `runs[]` |
| `run get` | `run` | `run` |
| `run logs -o json` | `log_line` | one envelope per line: `runId`, `line` |
| `status` | `status` | `daemon`, `roots`, `jobCount`, `configError`, `nextRuns[]` |
| `reload` | `reload_requested` | `accepted` (bool) |
| `validate` | `validation` | `valid`, `errors[]`, `warnings[]`; plus `scheduleType` and `nextRuns[]` for `--schedule`, or `jobs[]` of `{id, nextRuns[]}` for a whole-file validation |
| `schema` | `schema` | `commands[]` |
| `service install/uninstall/status` | `service` | `driver`, `installed`, `entries[]` |
| `install-cli` | `install_cli` | `path`, `linked` |
| `daemon` | — | no envelope; it is a long-running process logging to stderr or the log file |

### 2.1 Error codes

Stable. New codes may be added; existing codes never change meaning.

| Code | Meaning |
| --- | --- |
| `usage` | Bad flags or arguments. Exit 2. |
| `config_invalid` | `jobs.yaml` failed validation. `error.details` carries per-job messages. |
| `config_conflict` | The file changed between read and write; retry. |
| `job_not_found` | No such job id. |
| `job_exists` | `job add` with an id already present. |
| `run_not_found` | No such run id. |
| `daemon_unreachable` | No live daemon claimed a trigger within the grace period. |
| `daemon_already_running` | `daemon` started while the lock is held. |
| `herdr_unavailable` | `kind: agent` operation with no `herdr` binary or no reachable server. |
| `agent_blocked` | The agent stopped at an approval or question UI. |
| `cwd_missing` | The job's `cwd` does not exist. |
| `limit_exceeded` | A run was refused by `limits`. |
| `run_failed` | With `--wait` or `run-once`, the run reached a terminal `failure`, `timeout`, or `cancelled`. `error.details.run` carries the full run record. |
| `io_error` | Filesystem failure. |
| `internal` | A bug. Includes a stack trace when `HERDR_CRON_DEBUG=1`. |

### 2.2 Exit codes

| Code | Meaning |
| --- | --- |
| `0` | The command succeeded. With `--wait` or `run-once`, the run finished `success`, `no_op`, or `skipped`. |
| `1` | The command failed, **or** with `--wait` / `run-once` the run finished `failure`, `timeout`, or `cancelled` (`error.code` = `run_failed`). |
| `2` | Usage error. |
| `3` | With `--wait` or `run-once`, the run finished `blocked` — a human is required. |

`skipped` exits 0 deliberately. Under the `os-scheduler` driver a skip caused by `overlap` or
`limit_exceeded` is the scheduler working as designed, and marking the systemd unit or the
scheduled task failed would train the user to ignore it
([`02-architecture.md`](02-architecture.md) §2.1).

`3` is separate from `1` because it is the one outcome where retrying is pointless and escalating
is correct. Herdr's own CLI uses 1 for server errors and 2 for syntax errors, which this
follows.

---

## 3. Commands

Twenty-one commands in six groups. Every `list`/`get` is read-only and needs no daemon.

### 3.1 Jobs

```
herdr-cron job list   [--state active|paused|all] [--tag T]... [--kind shell|agent]
herdr-cron job get    <job-id>
herdr-cron job add    --id ID --schedule EXPR (--command CMD | --prompt TEXT)
                      [--name N] [--description D] [--cwd PATH] [--env K=V]... [--tag T]...
                      [--timeout DUR] [--timezone TZ] [--catchup off|latest|all]
                      [--concurrency skip|queue|cancel_previous|allow]
                      [--agent-kind KIND] [--session NAME] [--no-op-marker TEXT]
                      [--max-attempts N] [--max-runs-per-day N] [--paused] [--dry-run]
herdr-cron job update <job-id> [same flags as add]
herdr-cron job rm     <job-id> [--yes] [--purge]
herdr-cron job pause  <job-id>
herdr-cron job resume <job-id>
herdr-cron job run    <job-id> [--wait] [--timeout DUR]
herdr-cron job cancel <job-id>
```

- `--schedule` accepts everything §2 of [`03-job-model.md`](03-job-model.md) accepts: a cron
  expression, a descriptor (`@daily`), a duration (`30m` → `every`), an RFC 3339 instant
  (→ `at`), or a `+`-prefixed relative instant (`+2h` → `at`). A relative instant is resolved
  once and stored as RFC 3339, never as `+2h`, so reloading cannot move it forward. A bare `2h`
  remains a repeating `every` schedule. `job add` and `job update` reject an `at` instant that
  is already past as a `usage` error (exit 2). One flag is disambiguated by shape because an
  agent that has to choose between `--cron`, `--every`, and `--at` will choose wrong.
- `--command` implies `kind: shell`; `--prompt` implies `kind: agent`. Supplying both is a usage
  error.
- `--dry-run` validates and prints the resolved job plus its next five fire times, and writes
  nothing.
- `job rm --purge` also deletes the job's history and logs. Without it they are retained and a
  re-added job with the same id inherits them.
- `job run` without `--wait` returns as soon as the trigger is claimed, with the `runId` the
  caller can poll. With `--wait` it blocks until the run is terminal
  ([`04-storage.md`](04-storage.md) §8).
- `job cancel` cancels the job's currently running execution.

`job list` result:

```json
{
  "id": "cli:job:list",
  "result": {
    "type": "job_list",
    "generatedAt": "2026-09-02T11:40:02+09:00",
    "daemon": {"status": "running", "pid": 40211},
    "jobs": [
      {
        "id": "nightly-deps",
        "name": "Nightly dependency audit",
        "kind": "agent",
        "enabled": true,
        "enabledSource": "file",
        "schedule": {"type": "cron", "expression": "17 3 * * 1-5", "timezone": "Asia/Seoul"},
        "tags": ["maintenance"],
        "nextRunAt": "2026-09-03T03:29:23+09:00",
        "lastRun": {
          "runId": "nightly-deps-20260901T181700Z",
          "status": "success",
          "finishedAt": "2026-09-02T03:41:12+09:00",
          "durationSec": 1461
        },
        "consecutiveFailures": 0
      },
      {
        "id": "demo-backup",
        "name": "demo-backup",
        "kind": "shell",
        "enabled": true,
        "enabledSource": "file",
        "schedule": {"type": "at", "at": "2026-12-24T18:00:00+09:00", "timezone": "Asia/Seoul"},
        "tags": [],
        "nextRunAt": "2026-12-24T18:00:00+09:00",
        "lastRun": null,
        "consecutiveFailures": 0,
        "completed": false
      }
    ]
  }
}
```

`job get` returns the full resolved record of `03-job-model.md` §1.3 under
`result.job`, plus `result.nextRuns` (five instants) and `result.recentRuns` (ten run records).
For a one-shot it also returns `result.completed`: `false` while its single Occurrence is pending
and `true` once the `state.json` catch-up watermark has spent that Occurrence by claiming it for
execution or recording why it was skipped. Each one-shot summary in `job list` carries the same
field.
Recurring jobs omit `completed` at both levels. One call is enough to render a whole detail
screen; this is deliberate, because a TUI opening a job should not need four round trips.

### 3.2 Runs

```
herdr-cron run list  [--job JOB_ID] [--status ok|failed|running|all] [--limit N] [--since T]
herdr-cron run get   <run-id>
herdr-cron run logs  <run-id> [--tail N] [--follow]
```

`--status ok` means `{success, no_op}`; `--status failed` means
`{failure, timeout, blocked, cancelled}`. The raw statuses are also accepted verbatim.

`run logs` emits **raw log text**, not an envelope, because it is a stream — this is the single
documented exception to §2 and `-o json` wraps each line as
`{"type":"log_line","runId":…,"line":…}` for callers that need framing.

### 3.3 Scheduler

```
herdr-cron daemon  [--foreground | --detach]
herdr-cron status  [--watch]
herdr-cron reload
```

- `daemon` acquires the lock and runs the schedule in the foreground of the calling process.
  `--foreground` is the same thing with logs on stderr instead of the log file; it is the
  Herdr-pane driver ([`02-architecture.md`](02-architecture.md) §2). `--detach` starts the
  daemon as a detached background process and returns once the lock is held and the first
  heartbeat is written, exiting 0; it exists for the Herdr plugin `[[startup]]` hook, which MUST
  exit rather than stay supervised ([`07-herdr-integration.md`](07-herdr-integration.md) §8).
  `--detach` on an already-running daemon is a no-op that exits 0, because the startup hook
  re-runs on every server start and every live handoff and must therefore be idempotent.
- `status` reports daemon liveness, roots, job counts, config errors, and the next three
  occurrences across all jobs. It never requires a daemon.
- `reload` writes a reload trigger; it exists for filesystems where the watcher is unreliable.

### 3.4 Service

```
herdr-cron service install   [--driver daemon|os-scheduler] [--user] [--now]
herdr-cron service uninstall [--yes]
herdr-cron service status
herdr-cron install-cli       [--dir PATH] [--force]
```

`--driver daemon` installs a systemd user unit / launchd LaunchAgent / Windows Scheduled Task
that runs `herdr-cron daemon`. `--driver os-scheduler` instead registers **one OS entry per job**
that execs `herdr-cron run-once <id>`; the mapping and its per-OS commands are specified in
[`02-architecture.md`](02-architecture.md) §4. `service status` reports which driver is
installed and whether the OS agrees.

`install-cli` symlinks (or, on Windows, hard-links with a copy fallback) the running binary into
a directory on `PATH`, defaulting to `~/.local/bin` on Unix and `%LocalAppData%\Microsoft\WindowsApps`
on Windows. It exists so a Herdr plugin install turns into a standalone CLI an agent can call
directly — the `herdr-hitl` precedent behind decision D4 — and is exposed as a plugin action in
[`07-herdr-integration.md`](07-herdr-integration.md) §8. `result.type` is `install_cli` with
`path` and `linked` (bool).

### 3.5 Introspection

```
herdr-cron validate  [--schedule EXPR] [--timezone TZ] [--file PATH] [--next N]
herdr-cron schema    [--command PATH]
herdr-cron completion bash|zsh|fish|powershell
herdr-cron run-once  <job-id>
```

Every `validate` warning is an object with `code`, `message`, `jobId` (nullable), and `field`
(a dotted path such as `agent.cwd`). The codes are the environment-check set of
[`03-job-model.md`](03-job-model.md) §7 level 4: `cwd_missing`, `cwd_not_trusted`,
`herdr_unavailable`, `agent_kind_unsupported`. An agent checks for the absence of
`cwd_not_trusted` before enabling a `kind: agent` job
([`07-herdr-integration.md`](07-herdr-integration.md) §5).

- **`validate --schedule "17 3 * * 1-5" --next 5`** is the highest-value command on this list for
  an agent caller. It parses through the same `NewDefaultCron` the daemon uses and prints the
  next five fire times, turning a class of silent misconfiguration into an immediate checkable
  answer (`gocron` research §10). `--schedule` takes any of the three forms of
  [`03-job-model.md`](03-job-model.md) §2, disambiguated by shape: a leading `@` or an
  expression containing whitespace is cron, an RFC 3339 instant is `at`, and anything else that
  parses as a Go duration is `every`. `--timezone` applies only to `--schedule`; a whole-file
  validation uses each job's own timezone. With no `--schedule` it validates the whole
  `jobs.yaml` and reports each job's next fire times **with jitter applied**, because that is
  when the job will actually run.
- **`schema`** prints the full command tree as JSON: name, path, usage, every flag with its type,
  default, and required-ness. An agent can discover the surface without scraping `--help`.
- **`run-once <job-id>`** executes exactly one run of a job in the calling process, synchronously,
  and exits with §2.2 semantics. It is the execution primitive every driver shares
  ([`02-architecture.md`](02-architecture.md) §2) and it works with no daemon at all.

---

## 4. What the CLI does without a daemon

| Command | Needs a daemon? |
| --- | --- |
| `job list`, `job get`, `run list`, `run get`, `run logs`, `status`, `validate`, `schema` | No — pure file reads. |
| `job add/update/rm` | No. The daemon picks the change up via the watcher; with no daemon, the change simply takes effect the next time one starts. |
| `job pause/resume` | No. Writes `overrides.json` under `overrides.lock` ([`04-storage.md`](04-storage.md) §4) — the one state file with more than one writer, which exists precisely so this works with no daemon. A running daemon observes the change through its watcher. |
| `job run`, `job cancel`, `reload` | Yes, or `daemon_unreachable`. Use `run-once` instead when there is no daemon. |
| `run-once` | No. It *is* the runner. |

That table is the practical payoff of the file-based design: an agent can add a job, validate it,
and test it with `run-once` on a machine where the daemon has never been installed.

---

## 5. Conventions for agent callers

Stated here because the skill will repeat them, and they are contracts, not advice:

1. **Read `result`, never the text rendering.** `-o text` may change in any release.
2. **Branch on `error.code`, never on `error.message`.**
3. **Never invent a job id.** Take ids from `job list`.
4. **Validate a schedule before writing it** — `validate --schedule` costs nothing and prevents a
   job that silently never fires.
5. **Treat exit 3 as "stop and ask a human".** It means an agent job is blocked on an approval
   dialog no automated retry can clear.
