# Prior art and the herdr-cron domain model

- **Date:** 2026-09-02
- **Scope:** (1) what already exists in the "schedule things, including agents" space, read from source; (2) the domain problems `herdr-cron` must solve that no library solves for it.
- **Sibling documents** (do not duplicate; referenced by name below): `2026-09-02-gocron-scheduling-engine.md` (gocron API, cron parsing, timers), `2026-09-02-bubbletea-mouse-tui.md` (TUI), `2026-09-02-herdr-plugin-integration.md` (Herdr plugin contract and `herdr` CLI), `2026-09-02-agent-skill-and-cli-ux.md` (SKILL.md, cobra/urfave, persistence libs, service managers, base directories).
- **Evidence rule used here:** every factual claim carries an inline citation to a pinned source file, a doc URL, or the exact command run. Design judgements are marked `[INFERENCE]`.

## Method and provenance

All clones made with `git clone --depth 1 <url> /tmp/hc-research/<name>`; SHA recorded with `git -C /tmp/hc-research/<name> rev-parse HEAD`.

| Local path | Upstream | Pinned SHA | Version / date |
|---|---|---|---|
| `/tmp/hc-research/dagu` | `github.com/dagu-org/dagu` (redirects to `dagucloud/dagu`) | `86fe7e34ea86170d54deffa381fbebca0e2c8555` | `main`, 2026-09-01 |
| `/tmp/hc-research/gronx` | `github.com/adhocore/gronx` | `74da1959a9f9d62b3391431d828c65af03e693e2` | tag `v1.20.3`, 2026-08-17 |
| `/tmp/hc-research/supercronic` | `github.com/aptible/supercronic` | `8e0a4a40090de8a22942c9fa573e27885ce18311` | tag `v0.2.49`, 2026-08-14 |
| `/tmp/hc-research/river` | `github.com/riverqueue/river` | `48c0036dcb12b1e2bb65c355593388bb7ff9926e` | tag `v0.47.0`, 2026-09-01 |
| `/tmp/hc-research/pueue` | `github.com/Nukesor/pueue` | `7564e1078ae90e6eff5a09361c7fb3fbbb5f25c2` | crate `pueue` 4.0.4, 2026-08-16 |
| `/tmp/hc-research/agent-cron` | `github.com/T0UGH/agent-cron` | `12f6aeb61799db62bbf6e20bc9e3c7c0ab7ec23b` | — |
| `/tmp/hc-research/ccs-dortort` | `github.com/dortort/claude-code-scheduler` | `89546463466768db1afbc524c9b2ce50d46fa5a7` | `@dortort/scheduler` |
| `/tmp/hc-research/ccs-biosphere` | `github.com/biosphere-labs/claude-code-scheduler` | `5c4a6809fd2f8fb25ac0018a31b44091fac2e2e3` | — |

Docs read as primary sources: `https://code.claude.com/docs/en/scheduled-tasks`, `https://code.claude.com/docs/en/desktop-scheduled-tasks`, `https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows`, `https://www.freedesktop.org/software/systemd/man/latest/systemd.timer.html` (systemd 261.2), `https://docs.temporal.io/encyclopedia/retry-policies`.

`mergestat/timediff` was excluded per assignment.

---

# Part 1 — Prior art

## 1.1 dagu — the closest structural analogue

**What it schedules.** Whole DAGs of steps, on cron, with timezones, overlap policy and a catch-up window. Its own summary: *"Schedule workflows with cron syntax, timezones, overlap policies, and catch-up windows"* (`README.md:31 @ 86fe7e3`). It is a single Go binary with a built-in Web UI, no external DB or broker, on Linux/macOS/Windows (`README.md:19-27 @ 86fe7e3`).

**Job definition format — verbatim** (`README.md:409-421 @ 86fe7e3`):

```yaml
schedule:
  - "0 */6 * * *"          # Every 6 hours
overlap_policy: skip       # Skip if previous run is still active
catchup_window: "5h"       # Catch up missed runs when scheduler is down for up to 5 hours

timeout_sec: 3600
handler_on:
  failure:
    run: notify-team.sh
  exit:
    run: cleanup.sh
```

and a step with retry (`README.md:427-436 @ 86fe7e3`):

```yaml
steps:
  - name: flaky-api-call
    run: curl -f https://api.example.com/data
    retry_policy:
      limit: 3
      interval_sec: 10
    continue_on:
      failure: true
```

The authoritative shape is a JSON Schema shipped in-repo, `internal/cmn/schema/dag.schema.json` (mirrored at `schemas/dag.schema.json`). Verbatim, the two fields that matter most to us (`internal/cmn/schema/dag.schema.json:151-161 @ 86fe7e3`):

```json
"catchup_window": {
  "type": "string",
  "description": "Lookback horizon for replaying missed cron runs when the scheduler restarts. Accepts Go duration syntax with day support (e.g. \"6h\", \"2d12h\", \"30m\"). Must be positive. If omitted, missed runs are not replayed."
},
"overlap_policy": {
  "type": "string",
  "enum": ["skip", "all", "latest"],
  "default": "skip",
  "description": "Controls what happens when a catchup run is ready but the DAG is still running from a previous run. \"skip\" (default) drops the catchup run and moves to the next. \"all\" keeps it in the buffer and retries on the next scheduler tick. \"latest\" discards all but the most recent missed interval."
}
```

Also present: `skip_if_successful` — *"Dagu checks if this DAG has already succeeded since the last scheduled time. If it has, Dagu will skip the current scheduled run"* (`internal/cmn/schema/dag.schema.json:147-149 @ 86fe7e3`); `queue` for cross-DAG concurrency, with `max_active_runs` explicitly deprecated in favour of it (`internal/cmn/schema/dag.schema.json:580-591 @ 86fe7e3`).

**Execution model.** Cron ticks compute missed intervals against a lookback horizon. The replay origin is a three-way max (`internal/service/scheduler/catchup.go:13-29 @ 86fe7e3`, verbatim):

```go
// ComputeReplayFrom computes the earliest timestamp worth replaying for a DAG.
//
//	replayFrom = max(
//	    now - catchupWindow,
//	    lastTick,
//	    lastScheduledTime,
//	)
func ComputeReplayFrom(catchupWindow time.Duration, lastTick, lastScheduledTime, now time.Time) time.Time {
```

with a hard cap (`internal/service/scheduler/catchup.go:31-34 @ 86fe7e3`):

```go
// MaxMissedRuns is the maximum number of missed intervals that will be
// replayed per DAG. Prevents memory explosion for large catchup windows
// with high-frequency schedules (e.g., 30-day window + per-minute cron).
const MaxMissedRuns = 1000
```

and deterministic run IDs for replayed occurrences: `catchupPrefix = "catchup-"`, `GenerateCatchupRunID(dagName, scheduledTime)` is deterministic in both inputs (`internal/service/scheduler/catchup_runid.go:15-33 @ 86fe7e3`; determinism asserted in `catchup_runid_test.go:28-33 @ 86fe7e3`). Trigger provenance is a first-class field: `--trigger-type` ∈ `scheduler | manual | webhook | subdag | retry | catchup` (`skills/dagu/references/cli.md:28 @ 86fe7e3`), surfaced to the workflow as `context.run.scheduled_at` for *"Scheduled, catchup, and one-off scheduled runs only"* (`specs/017-built-in-run-context.md:178 @ 86fe7e3`).

**TUI / UI.** No TUI. A built-in Web UI ("Cockpit") plus a REST API (`README.md:19, 43-50 @ 86fe7e3`) plus a built-in MCP server (`README.md:33 @ 86fe7e3`).

**Persistence.** *Definitions in YAML files, state on the filesystem.* Config splits `DAGsDir`, `DataDir`, `LogDir` (`internal/cmn/config/config.go:408-415 @ 86fe7e3`). Control-plane collections are directories under `DataDir` — queue, dag-state, leases, incidents, notifications, scheduler state (`internal/persis/file/backend.go:16-60 @ 86fe7e3`; `SchedulerStateDir = DataDir/scheduler`, `:29-35`). Run history is a date-sharded tree per DAG: `baseDir/<safe-dag-name>/dag-runs/*/*/*/<prefix>*` (`internal/persis/file/dagrun/dataroot.go:57-70 @ 86fe7e3`), guarded by a directory lock with a 30s stale threshold (`dataroot.go:65-68 @ 86fe7e3`). Each attempt is written as newline-delimited JSON through a buffered `json.Encoder` (`internal/persis/file/dagrun/writer.go:26-48 @ 86fe7e3`). **No SQLite anywhere in the default path.**

**Missed runs.** Opt-in only: omit `catchup_window` and *"missed runs are not replayed"* (schema quote above). When set, every missed occurrence in the window is replayed, deduplicated across multiple schedules, sorted chronologically and truncated to the most recent 1000 (`internal/service/scheduler/catchup.go:41-89 @ 86fe7e3`).

**Agent-relevant surface.** dagu already ships the two things herdr-cron wants. First, `action: harness.run` invokes an external coding-agent CLI, with built-in adapters and exact invocation lines (`skills/dagu/references/harnesses.md:5-27 @ 86fe7e3`) — e.g. `claude` → `claude -p "<prompt>" [flags]`, `codex` → `codex exec "<prompt>" [flags]`, `gemini` → `gemini -p "<prompt>" [flags]`, plus `aider`, `amp`, `cline`, `copilot`, `cursor`, `deepseek`, `droid`, `goose`, `kiro`, `opencode`, `pi`, `qwen`. Second, `type: agent` DAGs where declared steps become a tool catalog and an LLM picks one action per turn until `tasks` are satisfied, with `llm.max_tool_iterations` defaulting to 50 (`specs/032-agent-dag.md:48-107 @ 86fe7e3`). dagu also ships a Claude-style skill at `skills/dagu/SKILL.md` with `name`/`description` frontmatter (`skills/dagu/SKILL.md:1-4 @ 86fe7e3`).

**The one idea worth stealing.** `catchup_window` + `overlap_policy` as *two orthogonal, per-job, declarative strings with safe defaults* (`skip`, and catch-up off), plus a deterministic catch-up run ID so replay is idempotent by construction. That triple is the entire missed-run design, and it is four lines of YAML.

## 1.2 adhocore/gronx and `pkg/tasker` — cron evaluation with no daemon

**What it schedules.** In-process Go functions or shell commands, from a crontab-format file, in a foreground process. The CLI binary is `cmd/tasker`.

**Job definition format — verbatim** the CLI surface (`cmd/tasker/main.go:23-29 @ 74da195`):

```go
flag.StringVar(&opt.File, "file", "", "The task file in crontab format (without user)")
flag.StringVar(&opt.Tz, "tz", "Local", "The timezone to use for tasks")
flag.StringVar(&opt.Shell, "shell", tasker.Shell()[0], "The shell to use for running tasks")
flag.StringVar(&opt.Out, "out", "", "The fullpath to file where output from tasks are sent to")
flag.BoolVar(&opt.Verbose, "verbose", false, "The verbose mode outputs as much as possible")
flag.Int64Var(&opt.Until, "until", 0, "The timeout for task daemon in minutes")
```

The in-memory job record is two strings (`pkg/tasker/tasker.go:34-38 @ 74da195`):

```go
// Task wraps a cron expr and its' command.
type Task struct {
	Expr string
	Cmd  string
}
```

The taskfile is crontab-shaped: blank lines and `#` comments skipped, `@annually|yearly|monthly|weekly|daily|hourly|5minutes|10minutes|15minutes|30minutes|always|everysecond` aliases supported (`pkg/tasker/parser.go:24-46 @ 74da195`).

**Execution model.** A sleep-based tick, not a timer heap: `var tickSec = 60`, wait `tickSec - now.Second()%tickSec`, then poll `time.Sleep(100 * time.Millisecond)` until the tick arrives (`pkg/tasker/tasker.go:285-313 @ 74da195`). Overlap suppression is a per-task atomic CAS (`pkg/tasker/tasker.go:347-350 @ 74da195`):

```go
func (t *Tasker) canRun(ref string) bool {
	lock, ok := t.mutex[ref]
	return !ok || atomic.CompareAndSwapUint32(lock, 0, 1)
}
```

Process spawning is platform-split: `syscall.SysProcAttr{Setpgid: true}` on POSIX (`pkg/tasker/tasker_other.go:22 @ 74da195`) vs `CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP` on Windows (`pkg/tasker/tasker_windows.go:24-26 @ 74da195`).

**TUI / persistence / missed runs.** None, none, and none. There is no state file; the process forgets everything on exit. A missed occurrence is simply never noticed — the tick loop only ever looks at "is this expression due *now*".

**The one idea worth stealing.** The `Setpgid` / `CREATE_NEW_PROCESS_GROUP` split (`tasker_other.go` / `tasker_windows.go`) is the minimal correct cross-platform child-process contract: a spawned job gets its own process group so Ctrl-C in the foreground kills the scheduler and not the children (comment at `supercronic/cron/cron.go:71-73 @ 8e0a4a4` says the same). herdr-cron needs exactly this two-file split, and it is ~20 lines.

## 1.3 supercronic — cron as a foreground process

**What it schedules.** Shell commands from a crontab file, in the foreground, for containers. Usage is `supercronic [OPTIONS] CRONTAB` (`main.go:26-29 @ 8e0a4a4`).

**Job definition format.** Real Vixie crontab, parsed with a field-count ladder (`crontab/crontab.go:19-25 @ 8e0a4a4`, verbatim):

```go
	parameterCounts = []int{
		7, // POSIX + seconds + years
		6, // POSIX + years
		5, // POSIX
		1, // shorthand (e.g. @hourly)
	}
```

`SHELL=`, `USER=` and `CRON_TZ=` env lines are honoured, with Vixie-compatible quote stripping (`crontab/crontab.go:79-108 @ 8e0a4a4`). A real crontab from the README (`README.md:202-204 @ 8e0a4a4`, verbatim):

```
# Sleep for 2 seconds every second. This will take too long.
* * * * * * * sleep 2
```

**Execution model.** One goroutine per job, each running its own `for` loop over `expression.Next()` (`cron/cron.go:158-214 @ 8e0a4a4`). Non-overlap is achieved *structurally* — the run is a synchronous call inside the loop unless `-overlapping` is set (`cron/cron.go:206-210 @ 8e0a4a4`):

```go
			if overlapping {
				go runThisJob(cronIteration, nextRun)
			} else {
				runThisJob(cronIteration, nextRun)
			}
```

with the loop comment stating the guarantee (`cron/cron.go:167-168 @ 8e0a4a4`): *"NOTE: if overlapping is disabled (default), this does not run multiple instances of the job concurrently"*.

**Missed runs — this is the interesting part.** supercronic does **not** catch up. When the next computed occurrence is already in the past it logs and skips forward (`cron/cron.go:173-180 @ 8e0a4a4`, verbatim):

```go
			now := time.Now().In(timezone)

			delay := nextRun.Sub(now)
			if delay < 0 {
				logger.Warningf("job took too long to run: it should have started %v ago", -delay)
				nextRun = now
				continue
			}
```

Separately, a `monitorJob` goroutine ticks the *schedule* while a job is still running and warns once per missed occurrence (`cron/cron.go:125-145 @ 8e0a4a4`), emitting `"not starting"` or `"overlapping jobs"` and incrementing `CronsDeadlineExceededCounter`. Observed in the README output (`README.md:205-212 @ 8e0a4a4`): `WARN[...] job took too long to run: it should have started 1.009438854s ago`.

**Logging model.** Job stdout/stderr are drained line-by-line into structured `logrus` entries tagged `channel=stdout|stderr` with an `iteration` counter (`cron/cron.go:104-114, 199-201 @ 8e0a4a4`), unless `-passthrough-logs` wires the child straight to the parent's fds (`cron/cron.go:85-98 @ 8e0a4a4`). Flags include `-json`, `-split-logs`, `-passthrough-logs`, `-test` ("test crontab (does not run jobs)") and `-prometheus-listen-address` (`main.go:32-56 @ 8e0a4a4`). Per-job Prometheus counters cover exec/success/fail/currently-running/deadline-exceeded (`cron/cron.go:228-257 @ 8e0a4a4`).

**Persistence.** None. Reload is `SIGUSR2` or `-inotify` file watching, the latter triggering on `Write` and `Remove` to survive Kubernetes atomic configmap writes (`README.md:222-237 @ 8e0a4a4`; watcher setup at `main.go:131-147 @ 8e0a4a4`).

**The one idea worth stealing.** `-test` — parse and validate the crontab, print what would run, exit without running anything. For a scheduler that spends money, a validate-only mode is not a nicety; and supercronic proves it costs one flag. Runner-up: emitting a *warning event per missed occurrence* even when the policy is "skip", so "we silently dropped 6 runs" is visible rather than inferred.

## 1.4 pueue — the CLI/daemon split

**What it schedules.** One-shot shell commands in a persistent queue. **There is no cron.** A grep for `cron|crontab|recurring|repeat|interval|periodic` across `pueue/src`, `pueue_lib/src` and `README.md` @ `7564e10` returns only unrelated hits (`"v".repeat()`, log-poll intervals, `std::iter::repeat`). Time scheduling is a one-shot `--delay` only.

**CLI/daemon split.** Two auto-discovered binaries from one crate: `pueue/src/bin/pueue.rs` (client, `#[tokio::main(flavor = "current_thread")]`, `:26`) and `pueue/src/bin/pueued.rs` (daemon, `multi_thread, worker_threads = 4`, `:6`), sharing `pueue_lib` (`Cargo.toml:2-4 @ 7564e10`). Transport is a Unix domain socket by default, TCP+TLS otherwise (`pueue_lib/src/network/socket/mod.rs:55-78 @ 7564e10`); Windows has no unix-socket branch, so Windows is always TCP+TLS. Wire format is length-prefixed CBOR via `ciborium`, `PACKET_SIZE = 1280` (`pueue_lib/src/network/protocol.rs:17, 52, 60-87 @ 7564e10`). The handshake is a raw shared secret with a 10s timeout and a constant 1s delay on failure, and the daemon answers with its `PROTOCOL_VERSION`, so client/daemon skew is a warning rather than a corrupt payload (`pueue/src/daemon/network/socket/mod.rs:41, 90-127 @ 7564e10`; `pueue_lib/src/lib.rs:27 @ 7564e10`).

**Command surface.** 22 subcommands in the clap `SubCommand` enum (`pueue/src/client/cli.rs:11 @ 7564e10`): `add`, `remove`/`rm`, `switch`, `stash`, `enqueue`, `start`, `restart`/`re`, `pause`, `kill`, `send`, `edit`, `env`, `group`, `status`, `log`, `follow`/`fo`, `wait`, `clean`, `reset`, `shutdown`, `parallel`, `completions`. With no subcommand the client defaults to `status` (`pueue/src/bin/pueue.rs:65-69 @ 7564e10`).

**`pueue status --json` surface.** The flag (`pueue/src/client/cli.rs:388-393 @ 7564e10`, verbatim):

```rust
        /// Print the current state as json to stdout.
        /// This does not include the output of tasks.
        /// Use `log -j` if you want everything.
        #[arg(short, long)]
        json: bool,
```

It emits `serde_json::to_string(&state)` of the whole daemon `State` (`pueue/src/client/commands/state/mod.rs:66-73 @ 7564e10`). The serialized job record (`pueue_lib/src/task.rs:55-69 @ 7564e10`, verbatim):

```rust
/// Representation of a task.
#[derive(PartialEq, Eq, Clone, Deserialize, Serialize)]
pub struct Task {
    pub id: usize,
    pub created_at: DateTime<Local>,
    pub original_command: String,
    pub command: String,
    pub path: PathBuf,
    pub envs: HashMap<String, String>,
    pub group: String,
    pub dependencies: Vec<usize>,
    pub priority: i32,
    pub label: Option<String>,
    pub status: TaskStatus,
}
```

and the lifecycle (`pueue_lib/src/task.rs:8-35 @ 7564e10`, condensed to the variant list, which is verbatim): `Locked{previous_status}`, `Stashed{enqueue_at: Option<DateTime<Local>>}`, `Queued{enqueued_at}`, `Running{enqueued_at, start}`, `Paused{enqueued_at, start}`, `Done{enqueued_at, start, end, result}`; with `TaskResult` ∈ `Success | Failed(i32) | FailedToSpawn(String) | Killed | Errored | DependencyFailed` (`pueue_lib/src/task.rs:37-52 @ 7564e10`). No serde attributes, so the JSON is externally-tagged: `"status": {"Queued": {"enqueued_at": "..."}}`.

**Persistence.** A single `state.json` (`state.json.gz` when compressed), written to `.partial` then `std::fs::rename`d — atomic rename, **no fsync, no backup copy** (`pueue/src/daemon/internal_state/state.rs:245-283 @ 7564e10`). Location is `dirs::data_local_dir()/pueue` (`pueue_lib/src/settings.rs:276-286 @ 7564e10`), i.e. `~/.local/share/pueue` on Linux and `%LOCALAPPDATA%\pueue` on Windows, via the `dirs` crate rather than hand-rolled XDG. Task stdout/stderr live in a separate `task_logs/` directory, which is why `--json` deliberately omits output (`pueue/src/daemon/mod.rs:96-128 @ 7564e10`).

**Crash recovery — conservative by design.** On restore, `Running`/`Paused` tasks become `Done{result: Killed}` (`pueue/src/daemon/internal_state/state.rs:326-350 @ 7564e10`), and any surviving `Queued` task force-pauses its group (`:366-375`, verbatim):

```rust
            // If there are any queued tasks, pause the group.
            // This should prevent any unwanted execution of tasks due to a system crash.
            if let TaskStatus::Queued { .. } = task.status {
```

pueue never re-runs an interrupted task automatically.

**Concurrency.** Per-group, stored in *state* (not config), default 1, `0` = unlimited (`pueue/src/daemon/internal_state/state.rs:130-138 @ 7564e10`; `pueue_lib/src/state.rs:22-27 @ 7564e10`; the `parallel_tasks == 0` unlimited branch at `pueue/src/daemon/process_handler/spawn.rs:62-66 @ 7564e10`). Lowering the limit never stops running tasks (`pueue/src/client/cli.rs:502-504 @ 7564e10`).

**Dependencies.** `pueue add --after <ID>...` (`pueue/src/client/cli.rs:70-75 @ 7564e10`), gated at spawn on all dependencies being `Done{Success}` (`spawn.rs:82-88 @ 7564e10`), with failure propagating as `Done{DependencyFailed}` **unless the group is paused**, so a human can fix and restart in place (`pueue/src/daemon/task_handler.rs:148-201 @ 7564e10`).

**Time scheduling.** `--delay` accepts seconds or a date expression, resolved **client-side** to an absolute `DateTime<Local>` before it hits the wire (`pueue/src/client/cli.rs:56-59, 622-637 @ 7564e10`; `pueue/src/client/commands/add.rs:64 @ 7564e10`). Firing is a 300ms poll over the whole task map with `time <= Local::now()` (`pueue/src/daemon/task_handler.rs:124-145, 61 @ 7564e10`) — so a missed window fires late on the next tick after restart, with no misfire semantics.

**The one idea worth stealing.** Two ideas, hard to separate. (a) `--immediate`, an explicit escape hatch that bypasses *both* the parallel limit and dependencies (`pueue/src/client/cli.rs:37-39, 219-222 @ 7564e10`) — the "run this now, I know what I'm doing" verb every scheduler needs. (b) The `pueue status` **query DSL** (`columns=`/`filter`/`order_by`/`limit`, pest grammar, documented inline as clap `long_help` at `pueue/src/client/cli.rs:335-386 @ 7564e10`): one grammar shared by the CLI filter flag and, by extension, a TUI filter bar.

## 1.5 river — what a durable job model looks like

Read only for the durable-model shape, as instructed. river is Go + Postgres (with a SQLite driver present).

**The job row** (`rivertype/river_type.go:50-140 @ 48c0036`, field anchors: `ID:54, Attempt:60, AttemptedAt:64, AttemptedBy:67, CreatedAt:70, EncodedArgs:73, Errors:77, FinalizedAt:82, Kind:87, MaxAttempts:95, Metadata:100, Priority:107, Queue:114, ScheduledAt:121, State:125, Tags:129, UniqueKey:134, UniqueStates:139`). Eight states (`rivertype/river_type.go:161-222 @ 48c0036`, values verbatim): `available`, `cancelled`, `completed`, `discarded`, `pending`, `retryable`, `running`, `scheduled`. The DDL ties terminality to a CHECK constraint (`riverdriver/riverpgxv5/migration/main/004_pending_and_more.up.sql:17-21 @ 48c0036`, verbatim):

```sql
ALTER TABLE /* TEMPLATE: schema */river_job ADD CONSTRAINT finalized_or_finalized_at_null CHECK (
    (finalized_at IS NULL AND state NOT IN ('cancelled', 'completed', 'discarded')) OR
    (finalized_at IS NOT NULL AND state IN ('cancelled', 'completed', 'discarded'))
);
```

**Attempts and retries.** `MaxAttemptsDefault = 25` (`internal/rivercommon/river_common.go:16 @ 48c0036`). The backoff (`retry_policy.go:31-36 @ 48c0036`, verbatim doc comment):

> *"Reschedules using a basic exponential backoff of `ATTEMPT^4`, so after the first failure a new try will be scheduled in 1 seconds, 16 seconds after the second, 1 minute and 21 seconds after the third, etc."*

Implementation (`internal/retrypolicy/default.go:37, 63, 74-77 @ 48c0036`): `errorCount := len(job.Errors) + 1`, `math.Pow(float64(attempt), 4)`, then `retrySeconds += retrySeconds * (rand.Float64()*0.2 - 0.1)` — i.e. ±10% jitter. Attempt accounting is deliberately asymmetric: claims increment, snoozes and graceful-shutdown interrupts decrement (`internal/jobexecutor/job_executor.go:417-419, 486 @ 48c0036`), and the retry clock keys off `len(Errors)` so voluntary reschedules can't inflate backoff.

**Leases.** There are none. Exclusivity is one atomic CTE (`riverdriver/riverpgxv5/internal/dbsqlc/river_job.sql:200-237 @ 48c0036`): `SELECT ... WHERE state = 'available' ... FOR UPDATE SKIP LOCKED` feeding an `UPDATE ... SET state = 'running', attempt = river_job.attempt + 1, attempted_at = ...`. Terminal writes go through `JobSetStateIfRunning` (`river_job.sql:620 @ 48c0036`), a no-op if the row left `running` — that is what stops a zombie first runner clobbering a rescued rerun. Liveness is *inferred from `attempted_at` age*, not a renewed lease (`river_job.sql:257-264 @ 48c0036`), with `JobRescuerRescueAfterDefault = time.Hour` and `JobRescuerIntervalDefault = 30 * time.Second` (`internal/maintenance/job_rescuer.go:29-32 @ 48c0036`).

**Idempotency.** SHA-256 of a canonical `&k=v` string over the enabled unique dimensions, stored in `unique_key`, enforced by a partial unique index gated on a state bitmask (`internal/dbunique/db_unique.go:47-54 @ 48c0036`; `migration/main/006_bulk_unique.up.sql:25-33 @ 48c0036`). The dimension that matters for cron (`insert_opts.go:161-169 @ 48c0036`, verbatim):

```go
	// ByPeriod defines uniqueness within a given period. On an insert time is
	// rounded down to the nearest multiple of the given period, and a job is
	// only inserted if there isn't an existing job that will run between then
	// and the next multiple of the period.
	ByPeriod time.Duration
```

Duplicates are not errors: `JobInsertResult.UniqueSkippedAsDuplicate` is `true` and the caller gets the pre-existing job (`rivertype/river_type.go:36-46 @ 48c0036`).

**Periodic jobs.** Schedule is an interface, not a string (`periodic_job.go:14-19 @ 48c0036`): `Next(current time.Time) time.Time`. Cron support is satisfied by any third-party parser — the shipped example uses `robfig/cron/v3`'s `cron.ParseStandard("30 * * * *")` (`example_cron_job_test.go:46, 52-59 @ 48c0036`). Options are exactly two fields (`periodic_job.go:36-52 @ 48c0036`): `ID string` and `RunOnStart bool`. **No catch-up**, stated outright (`periodic_job.go:64-72 @ 48c0036`, verbatim):

> *"The periodic job scheduler is approximate and doesn't guarantee strong durability. It's started by the elected leader in a River cluster, and each periodic job is assigned an initial run time when that occurs. ... each scheduler only retains in-memory state, so anytime a process quits or a new leader is elected, the whole process starts over without regard for the state of the last scheduler. The RunOnStart option can be used as a hedge to make sure that jobs with long run durations are guaranteed to occasionally run."*

The enqueuer advances `periodicJob.nextRunAt = periodicJob.ScheduleFunc(periodicJob.nextRunAt)` from the *scheduled* time, one insert per tick regardless of how many periods elapsed — no catch-up, no drift (`internal/maintenance/periodic_job_enqueuer.go:460, 475-479 @ 48c0036`).

One correction to that doc comment, from the code: at v0.47.0 the enqueuer *does* persist `nextRunAt` for periodic jobs that carry an explicit `ID`, and restores it on leader start via `initialPeriodicJobsMap` — only jobs without an ID (or without a stored row) get `nextRunAt = ScheduleFunc(now)`, i.e. next-occurrence-from-now with missed windows dropped (`internal/maintenance/periodic_job_enqueuer.go:380, 409-413 @ 48c0036`). The restored value is still a single next-fire time, not a backlog, so the "no catch-up" conclusion holds; the "whole process starts over" wording in the doc comment is stronger than the current code.

**Timeouts.** Tri-valued and cooperative (`worker.go:52-57 @ 48c0036`): `0` inherits the client default, `-1` means never. `JobTimeoutDefault = 1 * time.Minute` (`client.go:53 @ 48c0036`). `-1` jobs are also exempt from rescue (`internal/maintenance/job_rescuer.go:358 @ 48c0036`: `if timeout < 0 || ... { return jobRetryDecisionIgnore }`). The docs are explicit that a worker ignoring context cancellation cannot be stopped (`worker.go:65-73 @ 48c0036`).

**Error recording** (`rivertype/river_type.go:262-281 @ 48c0036`, verbatim):

```go
type AttemptError struct {
	At time.Time `json:"at"`
	Attempt int `json:"attempt"`
	Error string `json:"error"`
	Trace string `json:"trace"`
}
```

One element per attempt, appended oldest-first to a `jsonb[]` column. Note `At: e.start` — the attempt's *start* time (`internal/jobexecutor/job_executor.go:496-501 @ 48c0036`).

**Temporal, one paragraph for contrast.** Temporal's default Activity retry policy is `Initial Interval = 1 second`, `Backoff Coefficient = 2.0`, `Maximum Interval = 100 × Initial Interval`, `Maximum Attempts = ∞`, `Non-Retryable Errors = []` (https://docs.temporal.io/encyclopedia/retry-policies, "Default values for Retry Policy"). Workflows, by contrast, are **not** retried by default, and the docs argue against it: *"Retrying an entire Workflow Execution is not recommended due to the deterministic nature of Workflow replay. Since Workflows replay the same sequence of events to reach the same state, retrying the whole Workflow would repeat the same logic without resolving the underlying issue"* (ibid.). The same page names LLM invocations as exactly the kind of non-deterministic operation that belongs in a retryable Activity rather than in replayable Workflow code.

**The one idea worth stealing.** The lease-free concurrency triple: increment the attempt at *claim* time, write terminal state only `IF still running`, and infer death from the age of `attempted_at`. It needs no heartbeat, no lock table, and ports to SQLite or to a file lock unchanged. Runner-up: `UniqueOpts.ByPeriod`, which makes "at most one run per hour" a *data* invariant rather than a scheduler invariant — the only mechanism here that survives two schedulers racing.

## 1.6 Agent-scheduling tools that already exist

### 1.6.1 Claude Code `/loop` — session-scoped, no catch-up

Official docs: https://code.claude.com/docs/en/scheduled-tasks. The mechanism is three model-facing tools — `CronCreate` (5-field cron expression, prompt, recurring or one-shot), `CronList`, `CronDelete` — with an 8-character task ID and a cap of **50 scheduled tasks per session** (ibid., "Manage scheduled tasks").

Semantics worth copying or rejecting, all verbatim from that page:

- **Firing model:** *"The scheduler checks every second for due tasks and enqueues them at low priority. A scheduled prompt fires between your turns, not while Claude is mid-response."*
- **Timezone:** *"All times are interpreted in your local timezone. A cron expression like `0 9 * * *` means 9am wherever you're running Claude Code, not UTC."*
- **Jitter:** *"Recurring tasks fire up to 30 minutes after the scheduled time (or up to half the interval, for tasks that run more often than hourly)... The offset is derived from the task ID, so the same task always gets the same offset."*
- **No catch-up:** *"No catch-up for missed fires. If a task's scheduled time passes while Claude is busy on a long-running request, it fires once when Claude becomes idle, not once per missed interval."*
- **Forced expiry:** *"Recurring tasks automatically expire 7 days after creation. The task fires one final time, then deletes itself. This bounds how long a forgotten loop can run."*
- **Kill switch:** *"Set `CLAUDE_CODE_DISABLE_CRON=1` in your environment to disable the scheduler entirely."*
- **Extended cron rejected:** *"Extended syntax like `L`, `W`, `?`, and name aliases such as `MON` or `JAN` is not supported."* Day-of-month/day-of-week uses vixie OR-semantics.

### 1.6.2 Claude Code Desktop scheduled tasks — local, one catch-up run

Official docs: https://code.claude.com/docs/en/desktop-scheduled-tasks. This is the closest published product to herdr-cron's problem, and its missed-run rule is the single most transferable piece of evidence in this document (verbatim, "Missed runs"):

> *"When the app starts or your computer wakes, Desktop checks whether each task missed any runs in the last seven days. If it did, Desktop starts exactly one catch-up run for the most recently missed time and discards anything older. A daily task that missed six days runs once on wake. Desktop shows a notification when a catch-up run starts."*

Followed immediately by the prompt-authoring warning (verbatim):

> *"A task scheduled for 9am might run at 11pm if your computer was asleep all day. If timing matters, add guardrails to the prompt itself, for example: 'Only review today's commits. If it's after 5pm, skip the review and just post a summary of what was missed.'"*

Other load-bearing facts from that page:

- Sleep behaviour: *"Tasks only run while the desktop app is running and your computer is awake. If your computer sleeps through a scheduled time, the run is skipped."*
- Staggering: *"Each task gets a small delay of a few minutes after the scheduled time to stagger API traffic. The delay is deterministic: the same task always starts at the same offset."*
- Skip reasons are recorded and inspectable: *"Hover a skipped entry to see why: your computer was asleep, the previous run was still in progress, or other scheduled tasks were already running."* — three distinct overlap/skip causes, surfaced per run.
- Isolation: *"By default, scheduled tasks run against whatever state your working directory is in, including uncommitted changes. Enable the worktree toggle when creating the task to give each run its own isolated Git worktree."*
- Permissions: per-task permission mode; *"If a task runs in Manual mode and needs to run a tool it doesn't have permission for, the run stalls until you approve it."*
- Storage: the prompt is a `SKILL.md` at `~/.claude/scheduled-tasks/<task-name>/SKILL.md` with YAML frontmatter for `name` and `description`; *"Schedule, folder, model, and enabled state are not in this file"* — i.e. a **deliberate split between a human-editable prompt file and machine-owned schedule state.**
- Self-modification: *"A scheduled task can also modify its own schedule or prompt from within a running session using the `update_scheduled_task` MCP tool."*
- Comparison table (both pages): Cloud minimum interval **1 hour**; Desktop and `/loop` minimum interval **1 minute**.

### 1.6.3 `@dortort/scheduler` — no daemon, register with the OS

`/tmp/hc-research/ccs-dortort @ 8954646`. A Claude Code plugin that installs jobs into launchd (macOS) or crontab (Linux): *"powered by native OS schedulers (launchd / crontab)"* (`README.md:11`). Requirements are Node ≥18, the `claude` CLI, and *"macOS (launchd) or Linux (crontab)"* (`README.md:32-34`) — **no Windows**.

Its job record is a complete answer to "what fields does a job need", and is worth quoting in full (`src/types.ts:96-109 @ 8954646`, verbatim):

```ts
export const ScheduledTaskSchema = z.object({
  id: z.string()
    .min(1)
    .max(128)
    .regex(TASK_ID_PATTERN, 'Task ID must start with alphanumeric and contain only alphanumeric, dots, hyphens, underscores'),
  name: z.string().min(1),
  description: z.string().optional(),
  enabled: z.boolean().default(true),
  trigger: TriggerSchema,
  execution: ExecutionConfigSchema,
  tags: z.array(z.string()).default([]),
  createdAt: z.string().datetime(),
  updatedAt: z.string().datetime(),
});
```

with `TriggerSchema` a discriminated union of `{type:'cron', expression, timezone}` and `{type:'once', timestamp, timezone}` (`src/types.ts:38-53 @ 8954646`), and the execution half (`src/types.ts:75-87 @ 8954646`, verbatim):

```ts
export const ExecutionConfigSchema = z.object({
  command: z.string().min(1, 'Command must not be empty'),
  workingDirectory: z.string().min(1),
  timeout: z.number().int().positive().default(300),
  env: z.record(z.string(), z.string()).optional().refine(
    (env) => env === undefined || validateEnvVars(env),
    { message: `Environment variables must not include blocked keys: ${BLOCKED_ENV_VARS.join(', ')}` },
  ),
  skipPermissions: z.boolean().default(false),
  worktree: WorktreeConfigSchema.optional(),
  memory: MemoryConfigSchema.optional(),
  projectPath: z.string().optional(),
});
```

Note `sensitiveFilePolicy: z.enum(['block', 'warn', 'allow']).default('block')` on the worktree config (`src/types.ts:64 @ 8954646`) and a `BLOCKED_ENV_VARS` denylist enforced at schema level.

Run history is a separate JSONL record with its own status enum `['success','failure','timeout','skipped','running']` and 22 fields including `worktreePath`, `worktreeBranch`, `worktreePushed`, `exitCode`, `duration` (`src/types.ts:115-136 @ 8954646`). Settings: `logRetentionDays` default 30, `maxExecutionHistory` default 100 (`src/types.ts:142-147 @ 8954646`).

Storage is two JSON files — *"Global: `~/.claude/schedules.json` — your personal tasks / Project: `<project>/.claude/schedules.json` — shared team tasks"*, with *"Global config takes precedence on ID collision. Project configs cannot set `skipPermissions`"* (`README.md:106-111 @ 8954646`).

OS registration uses marker-fenced crontab blocks so foreign entries survive (`src/schedulers/linux.ts:13-38 @ 8954646`):

```ts
const MARKER_PREFIX = 'claude-scheduler';
...
  lines.push(`# ${MARKER_PREFIX}:${task.id}:begin`);
  lines.push(`PATH=${task.userPath}`);
  ...
  cronLine += `${task.cronExpression} /bin/bash ${task.wrapperScriptPath}`;
  lines.push(cronLine);
  lines.push(`# ${MARKER_PREFIX}:${task.id}:end`);
```

Log rotation is deliberately primitive (`src/logs/index.ts:66-79 @ 8954646`): *"Rotate a log file if it exceeds maxBytes. Renames current to .1, creates fresh empty file. Keeps only 1 rotated copy."* Paths are platform-split — `<taskId>.out.log` / `<taskId>.err.log` on darwin (launchd writes two streams), a single `<taskId>.log` elsewhere (`src/logs/index.ts:25-34 @ 8954646`).

**The one idea worth stealing.** Marker-fenced OS-scheduler entries (`# tool:id:begin` … `:end`) — it makes "register with the OS" reversible and safe to run repeatedly, which is the only thing that makes architecture (c) tolerable.

### 1.6.4 `agent-cron` — markdown prompts, strictly serial queue

`/tmp/hc-research/agent-cron @ 12f6aeb`. *"Run Claude Agent SDK tasks on a cron schedule. Tasks are plain `.md` files — write a prompt, set a cron expression. That's it."* (`README.md:5`).

Job definition is one Markdown file with YAML frontmatter (`README.md:124-135 @ 12f6aeb`, verbatim):

```markdown
---
name: Daily AI News          # display name (default: filename slug)
cron: "0 9 * * *"            # cron expression (Asia/Shanghai timezone)
agent: claude                # agent runner (default: claude)
skills: true                 # load ~/.claude/ skills (set false to isolate)
---

Today is {date}. Search for the latest AI news...

If nothing to report, output exactly: HEARTBEAT_OK
```

The in-memory record (`src/types.ts:3-11 @ 12f6aeb`, verbatim):

```ts
export interface Task {
  slug: string
  name: string
  cron: string
  agent?: string
  skills?: boolean | string[]
  prompt: string
  [key: string]: unknown
}
```

Concurrency is a *global* serial queue with slug dedup, not per-job (`README.md:114-118 @ 12f6aeb`): *"Tasks triggered at the same cron time are executed **serially** in filename (slug) order — not concurrently. This prevents silent failures from parallel execution."* Implementation dedups against both the running slug and the pending list (`src/queue.ts:16-21 @ 12f6aeb`). `agent-cron run` bypasses the queue entirely (`README.md:118 @ 12f6aeb`).

Cost is a first-class result field (`src/types.ts:13-18 @ 12f6aeb`):

```ts
export interface RunResult {
  result: string;
  cost?: number;
  inputTokens?: number;
  outputTokens?: number;
}
```

Logs are per-task per-day structured lines (`README.md:96-107 @ 12f6aeb`): `[2026-03-07 09:00:01.123] [START] task=daily-ai-news`, `[TOOL] name=web_search input={...}`, `[END] status=ok duration=8877ms`. `agent-cron start` is a long-running foreground process; the README defers durability to launchd/systemd/pm2 (`README.md:84 @ 12f6aeb`).

**The one idea worth stealing.** The **`HEARTBEAT_OK` protocol** (`README.md:180 @ 12f6aeb`): *"If the agent returns exactly `HEARTBEAT_OK` (trimmed), the task is considered a no-op — logged as `heartbeat` status. Use this when a task should only act when there is genuinely new content."* A three-valued run outcome — succeeded-and-did-something / succeeded-and-did-nothing / failed — is exactly what a nightly agent job needs so the history isn't 300 identical green rows. Runner-up: the prompt *is* the file, so `git diff` on a job is a diff of the instruction.

### 1.6.5 `biosphere-labs/claude-code-scheduler` — recipes with a concurrency cap

`/tmp/hc-research/ccs-biosphere @ 5c4a680`. Drives the `claude` CLI (subscription, not API credits) from JSON "recipes" with multi-turn session continuity (`README.md:3-12`). A real recipe template (`recipe-templates/epic.template.json @ 5c4a680`, verbatim):

```json
{
  "schedule": "{{TIME}} once",
  "status": "unprocessed",
  "cwd": "/home/justin/Documents/dev/workspaces/YouTube-Studdy-Buddy-App",
  "steps": [
    {
      "type": "claude_session_send",
      "sessionId": "epic{{NUMBER}}",
      "userPrompt": "Just be aware that for this session you're being controlled by a scheduler and there is no user, so you can't ask them questions. If you encounter issues, you'll have to either best guess them or abort if you can't best guess them. But try and complete the implementation at the very least."
    },
    {
      "type": "claude_session_send",
      "sessionId": "epic{{NUMBER}}",
      "userPrompt": "/pm:epic-all {{EPIC_NAME}}"
    }
  ]
}
```

Concurrency is a hardcoded global cap `const MAX_CONCURRENT_JOBS = 2;` with an in-memory queue (`src/cli.ts:8, 198-210 @ 5c4a680`) and `MAX_LOG_LINES = 1000` ring-trimming (`src/cli.ts:52, 230-231 @ 5c4a680`). Recipes carry their own `status` (`unprocessed`/`in_progress`/`processed`) so state lives in the definition file (`README.md:12`).

**The one idea worth stealing.** That first prompt step. Prepending a standing *"you are being run by a scheduler, there is no user, do not ask questions, abort if you cannot proceed"* preamble to every agent job is a product feature, not a user's responsibility. Its absence is how unattended agent runs stall forever waiting for input — which is exactly the failure mode Claude Desktop documents ("the run stalls until you approve it").

### 1.6.6 Codex

`codex exec` is the documented non-interactive primitive and is already an adapter target in dagu (`codex exec "<prompt>" [flags]`, `dagu skills/dagu/references/harnesses.md:16 @ 86fe7e3`). Beyond that, see **Could not verify** — I found no official OpenAI documentation page for Codex "automations" scheduling semantics; the search results were blog posts and open feature-request issues, which this document does not cite as evidence.

## 1.7 GitHub Actions `on.schedule` — the documented "delayed or dropped" behaviour

Source: https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows, section `schedule`. Verbatim notes:

> *"The `schedule` event can be delayed during periods of high loads of GitHub Actions workflow runs. High load times include the start of every hour. If the load is sufficiently high enough, some queued jobs may be dropped. To decrease the chance of delay, schedule your workflow to run at a different time of the hour."*

> *"In a public repository, scheduled workflows are automatically disabled when no repository activity has occurred in 60 days."*

> *"By default, scheduled workflows run in UTC. ... The shortest interval you can run scheduled workflows is once every 5 minutes."*

> *"For schedules that set `timezone` to a time zone that observes daylight saving time (DST), during DST spring-forward transitions, scheduled workflows in skipped hours advance to the next valid time. For example, a 2:30 AM schedule advances to 3:00 AM."*

Three transferable lessons. **(a)** A scheduler under contention is allowed to be late and allowed to *drop*, and the honest move is to document it rather than pretend otherwise. **(b)** The mitigation offered to users is "don't schedule at `:00`" — the same insight Claude Code implements automatically as deterministic per-task jitter. **(c)** GitHub auto-disables idle scheduled workflows after 60 days: an unattended recurring job that nobody is watching is treated as a liability with an expiry, exactly like Claude Code's 7-day `/loop` expiry.

## 1.8 Comparison

| | schedules | definition format | execution | UI | persistence | missed runs |
|---|---|---|---|---|---|---|
| dagu | DAGs, cron+tz | YAML files | in-proc, queues, workers | Web UI + REST + MCP | YAML defs + file/JSONL state tree | `catchup_window`, replay all, cap 1000 |
| gronx/tasker | funcs/commands | crontab file | goroutine per tick, CAS overlap lock | none | none | never noticed |
| supercronic | commands | crontab file | goroutine per job, synchronous | none | none | skip + warn per occurrence |
| pueue | one-shot commands | CLI args → `state.json` | daemon, per-group slots | none (`--json` + query DSL) | `state.json` atomic-rename | n/a (absolute `enqueue_at`, fires late) |
| river | queued jobs, periodic | Go structs | Postgres/SQLite, `SKIP LOCKED` | separate `cmd/river` CLI | RDBMS | none; `RunOnStart` hedge |
| Claude Code `/loop` | prompts | tool calls, session-scoped | between turns | in-session | none (session, `--resume` restores) | none |
| Claude Desktop tasks | prompts | `SKILL.md` + app state | fresh session per fire | desktop app | `~/.claude/scheduled-tasks/` + app state | exactly one, most recent, 7-day lookback |
| `@dortort/scheduler` | claude commands | `schedules.json` | OS scheduler execs CLI | Claude slash-commands | JSON + JSONL history | whatever cron/launchd does |
| agent-cron | prompts | `.md` + frontmatter | foreground, global serial queue | `status`/`logs` CLI | log files + status | none |
| GitHub Actions | workflows | YAML in repo | hosted runners | web | hosted | delayed or dropped, documented |

---

# Part 2 — Domain model for herdr-cron

Each question states options, cites prior art that solved it, and names the consequence. Where evidence is one-sided I say so; where it isn't, both branches stay open and reappear in the closing section.

## Q1. What is a job, exactly?

### The three candidate kinds

**(a) Shell command.** Universally supported: `Task{Expr, Cmd}` in gronx (`pkg/tasker/tasker.go:34-38 @ 74da195`), `command: z.string().min(1)` in `@dortort/scheduler` (`src/types.ts:76 @ 8954646`), `original_command` + `command` (post-alias) in pueue (`pueue_lib/src/task.rs:60-61 @ 7564e10`). Note pueue keeps *both* the user's text and the resolved command — cheap, and it makes `list` honest.

**(b) Prompt an agent in a Herdr pane and capture its answer.** Prior art splits into two sub-shapes:
- *Fire-and-forget CLI invocation*: dagu's `harness.run` shells out to `claude -p "<prompt>"` / `codex exec "<prompt>"` and takes stdout (`skills/dagu/references/harnesses.md:5-27 @ 86fe7e3`); agent-cron does the same through the Agent SDK.
- *Managed session*: dagu's OpenCode integration keeps a long-lived conversation, suspends the step durably when the agent asks a question, and resumes it on the owning host — *"A waiting answer suspends the step durably; Dagu resumes the same OpenCode session on the host that owns it. The final assistant text becomes step stdout"* (`skills/dagu/references/harnesses.md:52-54 @ 86fe7e3`). It also documents the failure mode: *"If the owning worker or OpenCode server disappears, the step remains waiting and the run page offers a clean-session restart"* (ibid.:60).

Herdr panes are the managed-session shape, not the one-shot shape. The consequence is that "capture its answer" needs a defined terminator. dagu's answer is "the final assistant text"; agent-cron's is "the agent's returned string, with the exact literal `HEARTBEAT_OK` meaning no-op" (`agent-cron README.md:180 @ 12f6aeb`). See `2026-09-02-herdr-plugin-integration.md` for what the pane contract actually exposes; the domain requirement is that a run has a **captured output** and a **terminal outcome**, and that the two are not the same field.

**(c) Chain / pipeline.** Three shapes exist in prior art, in ascending cost: pueue's `--after <ID>` id-list with `DependencyFailed` propagation (`pueue/src/client/cli.rs:70-75 @ 7564e10`; `pueue/src/daemon/task_handler.rs:148-201 @ 7564e10`); dagu's `depends:` DAG with `continue_on` (`dagu README.md:398-403, 427-436 @ 86fe7e3`); dagu's `type: agent`, where the LLM chooses the order and `tasks:` states the termination condition (`specs/032-agent-dag.md:48-107 @ 86fe7e3`). **Consequence:** (c) is where a scheduler becomes an orchestrator. dagu's own README frames this as the trap: *"You wanted to schedule some jobs. Now you operate a second system"* (`README.md:58 @ 86fe7e3`). `[INFERENCE]` A `steps:` list executed in order, with `continue_on_failure` per step and no fan-out, buys ~90% of the value of (c) at ~5% of the cost; a real DAG is a v2 decision, not a v1 one.

### Proposed job record

Fields, each with its prior-art warrant:

| field | warrant |
|---|---|
| `id` | `ScheduledTaskSchema.id` with `^[a-zA-Z0-9][a-zA-Z0-9._-]*$`, max 128 (`ccs-dortort src/types.ts:96-100`); river caps periodic IDs at 128 too |
| `name` | `name: z.string().min(1)` (`ccs-dortort src/types.ts:101`); dagu prefers `id` and treats `name` as display-only (`skills/dagu/SKILL.md:14`) |
| `description` | optional (`ccs-dortort src/types.ts:102`); dagu makes it the tool description in agent DAGs |
| `schedule` | discriminated union `cron`/`once`, each with `timezone` (`ccs-dortort src/types.ts:38-53`); river makes it an interface, `Next(time.Time) time.Time` (`periodic_job.go:14-19`) |
| `kind` + `payload` | dagu discriminates on `action:`/`type:`; keeping the payload in a `kind`-tagged sub-object is what allows adding kinds without touching the outer record |
| `cwd` | `workingDirectory` (`ccs-dortort src/types.ts:77`); pueue stores `path: PathBuf` per task (`pueue_lib/src/task.rs:62`) |
| `env` | `envs: HashMap<String,String>` (`pueue_lib/src/task.rs:63`); with a denylist (`ccs-dortort src/types.ts:79-82`) |
| `timeout` | `timeout: z.number().int().positive().default(300)` (`ccs-dortort src/types.ts:78`); river's tri-valued `0 = inherit, -1 = never` (`worker.go:52-57`) is strictly better for agent runs that legitimately take hours |
| `enabled` | `enabled: z.boolean().default(true)` (`ccs-dortort src/types.ts:103`) — but see Q8 for why the default is contested |
| `tags` | `tags: z.array(z.string()).default([])` (`ccs-dortort src/types.ts:106`); river `Tags []string`; dagu `labels` |
| `retry` | dagu `retry_policy: {limit, interval_sec}` (`README.md:431-433`); river `MaxAttempts` + policy interface |
| `concurrency` | dagu `overlap_policy: skip|all|latest` (`dag.schema.json:155-161`); pueue per-group `parallel_tasks` (`state.rs:22-27`) |
| `limits` | *no direct prior art for money caps* — see Q8, marked `[INFERENCE]` |
| `notify` | dagu `handler_on: {failure, exit}` (`README.md:415-421`); Desktop fires an OS notification per run and per catch-up |
| `createdAt`/`updatedAt` | `ccs-dortort src/types.ts:107-108` |

**YAML form** (the human/agent-editable definition):

```yaml
version: 1

defaults:
  timezone: local
  timeout: 30m
  concurrency: skip
  enabled: false

jobs:
  - id: nightly-deps
    name: Nightly dependency audit
    description: Check for outdated deps and open a PR if anything is stale.
    enabled: true
    tags: [maintenance, repo:herdr]

    schedule:
      cron: "17 3 * * 1-5"      # :17, not :00 — see Q3 on jitter
      timezone: Asia/Seoul
      catchup: latest           # off | latest | all
      catchup_window: 6h

    kind: agent                 # shell | agent | chain
    agent:
      target: pane:reviewer     # resolved by the Herdr plugin; see sibling doc
      prompt: |
        Audit dependencies in this repo. If everything is current,
        reply with exactly HEARTBEAT_OK and stop.
      capture: final-message
      no_op_marker: HEARTBEAT_OK

    cwd: ~/src/herdr
    env:
      GIT_AUTHOR_NAME: herdr-cron
    timeout: 45m

    concurrency: skip           # skip | queue | cancel-previous | allow
    retry:
      max_attempts: 2
      backoff: exponential
      initial: 60s

    limits:
      max_runs_per_day: 4
      max_consecutive_failures: 3   # then auto-disable

    notify:
      on: [failure, auto_disabled]

  - id: build-smoke
    name: Hourly build smoke
    enabled: true
    schedule: { cron: "*/30 * * * *" }
    kind: shell
    shell:
      command: go build ./... && go test ./internal/scheduler/...
    cwd: ~/src/herdr
    timeout: 10m
    concurrency: skip
```

**JSON form** — the same job record, canonicalised (this is what `herdr-cron list --json` would emit; `defaults` are resolved, durations normalised to seconds, following pueue's precedent of serialising the resolved state rather than the source text):

```json
{
  "id": "nightly-deps",
  "name": "Nightly dependency audit",
  "description": "Check for outdated deps and open a PR if anything is stale.",
  "enabled": true,
  "tags": ["maintenance", "repo:herdr"],
  "schedule": {
    "type": "cron",
    "expression": "17 3 * * 1-5",
    "timezone": "Asia/Seoul",
    "catchup": "latest",
    "catchupWindowSec": 21600
  },
  "kind": "agent",
  "payload": {
    "target": "pane:reviewer",
    "prompt": "Audit dependencies in this repo. If everything is current,\nreply with exactly HEARTBEAT_OK and stop.\n",
    "capture": "final-message",
    "noOpMarker": "HEARTBEAT_OK"
  },
  "cwd": "/home/huke/src/herdr",
  "env": { "GIT_AUTHOR_NAME": "herdr-cron" },
  "timeoutSec": 2700,
  "concurrency": "skip",
  "retry": { "maxAttempts": 2, "backoff": "exponential", "initialSec": 60 },
  "limits": { "maxRunsPerDay": 4, "maxConsecutiveFailures": 3 },
  "notify": { "on": ["failure", "auto_disabled"] },
  "createdAt": "2026-09-02T11:04:00+09:00",
  "updatedAt": "2026-09-02T11:04:00+09:00"
}
```

And the **run record**, which is a separate type in every system that has one (`ExecutionHistoryRecordSchema`, `ccs-dortort src/types.ts:115-136`; `JobRow`, `river rivertype/river_type.go:50-140`):

```json
{
  "runId": "sched-nightly-deps-20260903T031700",
  "jobId": "nightly-deps",
  "trigger": "scheduler",
  "scheduledAt": "2026-09-03T03:17:00+09:00",
  "startedAt": "2026-09-03T03:19:41+09:00",
  "finishedAt": "2026-09-03T03:44:02+09:00",
  "attempt": 1,
  "status": "no_op",
  "exitCode": 0,
  "durationSec": 1461,
  "logPath": "runs/2026/09/03/nightly-deps-031700/run.log",
  "outputExcerpt": "HEARTBEAT_OK",
  "cost": { "inputTokens": 41233, "outputTokens": 812, "usd": 0.19 },
  "errors": []
}
```

Three deliberate choices, each with a citation. `trigger` is an enum, following dagu's `scheduler|manual|webhook|subdag|retry|catchup` (`skills/dagu/references/cli.md:28 @ 86fe7e3`) — without it you cannot tell a catch-up run from a real one in the history. `runId` is deterministic in `(jobId, scheduledAt)`, following dagu's `GenerateCatchupRunID` (`catchup_runid.go:31-33 @ 86fe7e3`), which makes replay idempotent. `status` has a `no_op` member beyond `success|failure|timeout|skipped|running` (`ccs-dortort src/types.ts:122`), because agent-cron's `HEARTBEAT_OK` proves that "ran and correctly did nothing" is a distinct and common outcome (`agent-cron README.md:180 @ 12f6aeb`). `cost` has no prior art outside agent-cron's `RunResult{cost, inputTokens, outputTokens}` (`src/types.ts:13-18 @ 12f6aeb`); its inclusion is `[INFERENCE]` driven by Q5 and Q8.

## Q2. Storage format — the most consequential decision

### What the prior art actually chose

| system | definitions | run state / history |
|---|---|---|
| dagu | **YAML files** in `DAGsDir` | filesystem tree under `DataDir`, JSONL per attempt (`internal/persis/file/backend.go:38-60`, `dagrun/writer.go:26-48`, `dagrun/dataroot.go:57-70 @ 86fe7e3`) |
| supercronic | **crontab file** | none |
| gronx/tasker | **crontab file** | none |
| pueue | none (CLI args) | **one `state.json`**, atomic rename, `dirs::data_local_dir()/pueue` (`internal_state/state.rs:245-283`; `settings.rs:276-286 @ 7564e10`) |
| river | Go code | **RDBMS** (Postgres, or the shipped SQLite driver) |
| `@dortort/scheduler` | **`schedules.json`** ×2 (global + project) | **JSONL** history + rotated log files (`README.md:106-111`; `src/types.ts:115-136 @ 8954646`) |
| agent-cron | **`.md` files**, one per job | per-task per-day log files |
| Claude Desktop | **`SKILL.md`** per task (prompt only) | app-owned state, *not* in the file |

The pattern is unanimous on one axis and split on the other. **Every single system stores job definitions in files, not in a database.** dagu, the most feature-complete system here — worker fleets, queues, leases, distributed execution — still keeps DAGs as YAML on disk and explicitly advertises *"Self-contained: no external DBMS or message broker required"* (`README.md:26 @ 86fe7e3`). Not one of the eight puts definitions in SQLite.

Run history is where they split: file tree (dagu), single JSON blob (pueue), JSONL (`@dortort/scheduler`), or RDBMS (river).

### The three options for herdr-cron

**(a) One file for everything.** Definitions and history in one YAML/TOML. *Consequence:* history growth corrupts the ergonomics of the definition file within days. `@dortort/scheduler` caps history at 100 records precisely because it lives in a JSON file (`maxExecutionHistory: z.number().int().positive().default(100)`, `src/types.ts:145 @ 8954646`). Rejected on evidence.

**(b) SQLite for everything.** *Consequence:* you lose `git diff` on a job, you lose "the agent edits the file with the Edit tool", and you lose the human's ability to fix a broken cron expression in vim. The single strongest counter-evidence is Claude Desktop, which had every reason to keep prompts in app state and instead put them in `~/.claude/scheduled-tasks/<task-name>/SKILL.md` with a documented editing workflow: *"To edit a task's prompt on disk, open `~/.claude/scheduled-tasks/<task-name>/SKILL.md`... Changes take effect on the next run"* (https://code.claude.com/docs/en/desktop-scheduled-tasks). For a tool whose *primary user is a coding agent*, a text file is an API and a database is not.

**(c) Split: definitions in a file, run history in a store.** This is dagu's shape (YAML defs + file-backed run tree), `@dortort/scheduler`'s shape (JSON defs + JSONL history), and Claude Desktop's shape (SKILL.md prompt + app-owned schedule/enabled state).

Note the *third* thing Claude Desktop's split reveals, which is easy to miss: it puts the **prompt** in the file but keeps **schedule, folder, model, and enabled state** out of it — *"Schedule, folder, model, and enabled state are not in this file: change them through the Edit form or ask Claude"* (ibid.). That is a different cut than dagu's, and it exists because a UI toggle for `enabled` should not require rewriting a user's text file.

### The genuinely open sub-question

Given (c), the run-history store is still undecided, and the evidence is not one-sided:

- **JSONL append-only** (`@dortort/scheduler`): trivial, greppable, no dependency, no schema migrations; but "show me the last 20 failures of job X across 90 days" is a full scan, and concurrent appenders need care.
- **File tree, date-sharded** (dagu, `dag-runs/YYYY/MM/DD/<run>` at `dagrun/dataroot.go:57-70 @ 86fe7e3`): scales, prunes by directory, needs a directory lock (dagu uses one with a 30s stale threshold) and an index for cross-cutting queries (dagu builds `dagrunindex`).
- **SQLite**: real queries for the TUI's filter/sort, transactional run-state updates, one file; costs a CGo-or-pure-Go dependency decision and schema migrations. See `2026-09-02-agent-skill-and-cli-ux.md` for the concrete Go library options.

`[INFERENCE]` The TUI is the tiebreaker. A mouse-driven TUI that filters and sorts run history is a query workload; pueue felt this pressure hard enough to build a **pest grammar** for `pueue status` filtering over an in-memory `BTreeMap` (`pueue/src/client/cli.rs:335-386 @ 7564e10`). If the history is going to be queried interactively, a store that can answer queries is worth its dependency; if the TUI only ever shows "last N runs of the selected job", JSONL is enough. That is a product decision, not a technical one, and it is listed in the closing section.

## Q3. Missed runs and catch-up

### What the four reference systems do

**cron (vixie):** nothing. A missed occurrence is not represented; the daemon only asks "is this due now". `anacron` exists as a separate program precisely because cron has no answer here. supercronic reproduces this faithfully — past-due occurrences are skipped forward with a warning (`cron/cron.go:173-180 @ 8e0a4a4`).

**systemd timers:** opt-in persistence, coalesced to **one** run. `Persistent=` (https://www.freedesktop.org/software/systemd/man/latest/systemd.timer.html, systemd 261.2), verbatim:

> *"Takes a boolean argument. If true, the time when the service unit was last triggered is stored on disk. When the timer is activated, the service unit is triggered immediately if it would have been triggered at least once during the time when the timer was inactive. Such triggering is nonetheless subject to the delay imposed by `RandomizedDelaySec=`. This is useful to catch up on missed runs of the service when the system was powered down. Note that this setting only has an effect on timers configured with `OnCalendar=`. Defaults to false."*

And specifically for sleep, on the same page:

> *"When a system is temporarily put to sleep (i.e. system suspend or hibernation) the realtime clock does not pause. When a calendar timer elapses while the system is sleeping it will not be acted on immediately, but once the system is later resumed it will catch up and process all timers that triggered while the system was sleeping. Note that if a calendar timer elapsed more than once while the system was continuously sleeping the timer will only result in a single service activation."*

Two more directly relevant knobs from that page: `WakeSystem=` (*"If true, an elapsing timer will cause the system to resume from suspend"*, requires privileges, and switches monotonic timers from `CLOCK_MONOTONIC` to `CLOCK_BOOTTIME`), and `RandomizedDelaySec=` / `FixedRandomDelay=` — the latter deriving a stable offset *"from the machine ID, the manager's user identifier, and the timer unit's name"*, which is the same trick Claude Code uses ("derived from the task ID").

**GitHub Actions:** may be late, may be dropped, no catch-up, and auto-disables idle scheduled workflows after 60 days (quotes in §1.7).

**Claude Desktop:** exactly one catch-up run, most recent missed time, 7-day lookback, notified (quote in §1.6.2).

**dagu:** off by default; when `catchup_window` is set, *every* missed occurrence in the window is replayed, capped at 1000 (`internal/cmn/schema/dag.schema.json:151-154`; `internal/service/scheduler/catchup.go:31-34, 41-89 @ 86fe7e3`).

**river:** none, by explicit design; `RunOnStart` is the documented hedge (`periodic_job.go:64-72 @ 48c0036`).

### The option space, and which fits

| option | who does it | consequence for an agent scheduler |
|---|---|---|
| **skip** | cron, supercronic, river, GH Actions, Claude `/loop` | zero surprise cost; a laptop closed over a weekend simply produces no runs. Silent unless you log the skip, which supercronic does (`cron.go:177`) |
| **run-once-on-startup** | systemd `Persistent=true`, Claude Desktop | one bounded cost per job per wake. The failure mode is *temporal confusion*: the 9am job runs at 11pm — which is exactly why Claude Desktop's own docs tell you to put the guardrail in the prompt |
| **run-every-missed-occurrence** | dagu `catchup_window` (capped 1000) | for an agent job this is a bill. A `*/30` agent job with a 6h window replays 12 runs the instant you open the lid, concurrently competing for the same repo |
| **grace window** | dagu's `catchup_window` *is* the grace window; systemd has no equivalent | bounded, per-job, declarative |

**Which fits an agent-driven scheduler where a run may cost money and mutate a repo.** The evidence here is *nearly* one-sided and it is worth being precise about why. Three independent systems that ship agent scheduling to real users — Claude Code `/loop`, Claude Desktop, and river's periodic jobs — all refuse to replay more than one occurrence, and the one that does replay everything (dagu) is a general workflow engine whose jobs are `ffmpeg` and `dbt`, not LLM calls. Claude Desktop is the closest match in every dimension (local machine, sleeps, agent runs, costs money, mutates a repo) and it chose *exactly one catch-up run for the most recently missed time, discarding anything older, with a 7-day lookback and a notification.*

`[INFERENCE]` The defensible design is therefore a **three-valued per-job policy** — `off` (default) | `latest` | `all` — with `latest` bounded by a `catchup_window`, and `all` refusing to run without an explicit `max_catchup_runs`. Two mechanisms should be non-optional regardless of policy: (1) every dropped occurrence produces a `skipped` run record with a reason, because supercronic warns per occurrence and Claude Desktop stores per-run skip reasons (*"your computer was asleep, the previous run was still in progress, or other scheduled tasks were already running"*), and (2) a catch-up run carries `trigger: catchup` and its original `scheduledAt`, so the *prompt* can see it is late — the guardrail Claude Desktop's docs push onto the user should be a template variable the product provides.

Two second-order issues that the systemd page forces into view and that no agent scheduler seems to have solved: **DST**. GitHub documents spring-forward as "advance to the next valid time"; systemd notes calendar timers may fire at unexpected times if the RTC is wrong and orders `OnCalendar=` units after `time-sync.target`. A laptop that wakes with a stale clock and immediately fires catch-up runs is a real, cheap-to-hit failure. `[INFERENCE]` A clock-sanity check before firing catch-up (refuse if `now` moved backwards, or if the delta since last tick is implausible) is worth its handful of lines.

## Q4. Concurrency and overlap

gocron's singleton mode is covered in `2026-09-02-gocron-scheduling-engine.md` and is not re-researched here. What the *other* prior art names:

| name in prior art | semantics | citation |
|---|---|---|
| `overlap_policy: skip` (default) | drop the new occurrence | `dagu internal/cmn/schema/dag.schema.json:155-161 @ 86fe7e3` |
| `overlap_policy: all` | buffer it, retry on the next tick | ibid. |
| `overlap_policy: latest` | keep only the most recent missed occurrence | ibid. |
| `-overlapping` flag (off by default) | run concurrently anyway | `supercronic main.go:55`, `cron/cron.go:206-210 @ 8e0a4a4` |
| per-task CAS lock | atomic compare-and-swap on a per-task mutex word | `gronx pkg/tasker/tasker.go:347-350 @ 74da195` |
| per-group `parallel_tasks` | N slots per group; `0` = unlimited; default 1 | `pueue pueue_lib/src/state.rs:22-27`, `internal_state/state.rs:130-138`, `spawn.rs:57-80 @ 7564e10` |
| global serial queue with slug dedup | one at a time across *all* jobs, dedup by slug | `agent-cron README.md:114-118`, `src/queue.ts:16-21 @ 12f6aeb` |
| global cap | `MAX_CONCURRENT_JOBS = 2` | `ccs-biosphere src/cli.ts:8 @ 5c4a680` |
| named `queue` | cross-job concurrency group; supersedes per-DAG `max_active_runs` | `dagu dag.schema.json:580-591 @ 86fe7e3` |
| unit already active ⇒ not restarted | the timer simply doesn't re-trigger | systemd.timer, *"Note that in case the unit to activate is already active at the time the timer elapses it is not restarted, but simply left running. There is no concept of spawning new service instances in this case."* |
| `DeferReactivation=` | schedule the next elapse from *completion*, not from the last trigger | systemd.timer (added v257) |

Three observations that matter for herdr-cron.

1. **Every system defaults to non-overlapping**, and the two that let you overlap make it an explicit opt-in flag (supercronic `-overlapping`) or an explicit policy value (dagu `all`). There is no dissent in the evidence.
2. **There are two independent axes**, and prior art conflates them at its peril: *self-overlap* (job X vs. job X) and *global concurrency* (how many jobs at once). dagu separates them (`overlap_policy` vs. `queue`); pueue only has the second (per-group slots) and relies on one-shot tasks having no self-overlap; agent-cron only has the first, aggressively (global serial). For agent jobs the global axis is the one that protects the wallet and the repo — three agents in one worktree is a merge conflict, not a schedule.
3. **`DeferReactivation=`** is the option nobody else has and agent jobs need most. A 45-minute agent run on a `*/30` schedule with `skip` will fire, skip, fire, skip forever; with defer-reactivation the next elapse is computed from completion. `[INFERENCE]` For `kind: agent`, defer-from-completion is arguably the right *default*, and cron-from-schedule the exception — but this contradicts every cron user's expectation, so it should be a per-job field rather than a silent behaviour change.

Also worth recording as a hazard: pueue counts running tasks from the **actual child-process map**, not from task statuses (`pueue/src/daemon/process_handler/spawn.rs:66-77 @ 7564e10`). Any implementation that counts "how many are running" from a persisted status field will over-count after a crash. river's equivalent guard is that terminal writes are conditional on the row still being `running` (`river_job.sql:620 @ 48c0036`).

## Q5. Failure, retry, and the money problem

### What river does

Covered in §1.5; the numbers restated for decision-making: `MaxAttemptsDefault = 25` (`internal/rivercommon/river_common.go:16 @ 48c0036`); backoff `attempt^4` seconds, documented as *"1 seconds, 16 seconds after the second, 1 minute and 21 seconds after the third"* (`retry_policy.go:31-36 @ 48c0036`); ±10% jitter (`internal/retrypolicy/default.go:63 @ 48c0036`); the retry clock keyed on `len(job.Errors)`, not `Attempt`, so snoozes cannot inflate backoff (`internal/retrypolicy/default.go:31-39 @ 48c0036`); a per-attempt `AttemptError{At, Attempt, Error, Trace}` appended oldest-first (`rivertype/river_type.go:262-281 @ 48c0036`); terminal state `discarded` when `Attempt >= MaxAttempts` (`internal/jobexecutor/job_executor.go:513-517 @ 48c0036`), which requires *"Manual user intervention... for them to be tried again"* (`rivertype/river_type.go:183 @ 48c0036`); and an explicit `river.JobCancel(err)` that *"Regardless of whether or not the job has any remaining attempts... will ensure the job does not execute again"* (`river/error.go:18-23 @ 48c0036`).

**river has no circuit breaker and no auto-disable.** I looked; the nearest constructs are the per-job `discarded` terminal state and `JobCancel`. Both are per-job-instance, not per-job-definition.

### What everyone else does

| system | retry | auto-disable / circuit breaker |
|---|---|---|
| dagu | `retry_policy: {limit, interval_sec}` per step, plus `repeat_policy` with `backoff` multiplier defaulting to 2.0 (`schemas/dag.schema.json:1863-1900 @ 86fe7e3`) | none found; `handler_on.failure` runs a script |
| Temporal | default Activity policy: 1s initial, 2.0 coefficient, 100× cap, ∞ attempts (docs.temporal.io/encyclopedia/retry-policies) | `Non-Retryable Errors` by error type; explicitly recommends *against* retrying whole Workflows |
| pueue | none — a killed task stays `Done{Killed}` until `pueue restart` | `daemon.pause_group_on_failure` / `pause_all_on_failure`, both default `false` (`pueue_lib/src/settings.rs:131-137 @ 7564e10`) |
| supercronic / gronx | none | none |
| `@dortort/scheduler` | none in the schema | none |
| GitHub Actions | n/a | **auto-disables scheduled workflows after 60 days of no repository activity** (public repos) |
| Claude Code `/loop` | n/a | **recurring tasks expire 7 days after creation** |

### The money problem, stated precisely

Retry is the wrong default primitive for an agent run, and the evidence says so from two directions. Temporal's docs argue that retrying a whole deterministic replay is pointless (*"retrying the whole Workflow would repeat the same logic without resolving the underlying issue"*) — an agent run is the *opposite*, non-deterministic, so a retry genuinely might succeed, but it also costs full price again and may re-apply half-finished mutations. river's `discarded` state exists because *some* failures must stop and wait for a human. Meanwhile pueue's `pause_group_on_failure` is the only "one failure stops the queue" primitive in the corpus, and it is off by default because pueue's tasks are cheap.

`[INFERENCE]` Three mechanisms, in order of importance:

1. **`max_attempts` default of 1, not 25.** river's 25 is calibrated for a webhook delivery costing nothing. An agent run's default should be "try once, record the failure, tell me". Retry stays available per-job.
2. **`max_consecutive_failures` → auto-disable.** This is the circuit breaker, and it has *no direct prior art* in the schedulers read — the closest analogues are GitHub's 60-day idle auto-disable and Claude Code's 7-day expiry, both of which are the same instinct (an unattended recurring thing must eventually stop itself) applied to a different trigger. The auto-disable must (a) flip `enabled` to false, (b) write a run record explaining why, (c) notify. Making it observable is what distinguishes a circuit breaker from a mystery.
3. **Distinguish "the harness failed" from "the agent said no".** A non-zero exit from `claude -p` and an agent replying "I couldn't do this" are different failures with different retry answers, and only the first is worth an automatic retry. agent-cron's `HEARTBEAT_OK` is the positive-case version of this distinction (`agent-cron README.md:180 @ 12f6aeb`); the negative case has no prior art I found.

A fourth, cheaper mechanism with real precedent: **budget the schedule, not the retry.** `UniqueOpts.ByPeriod` makes "at most one run per hour" a database invariant (`river insert_opts.go:161-169 @ 48c0036`). A per-job `max_runs_per_day` counter checked before spawn is the same idea and is the only guard that survives a catch-up storm, a manual `run`, and a retry loop simultaneously.

## Q6. Where does a scheduled run's output go?

Base directories are established in `2026-09-02-agent-skill-and-cli-ux.md`; this section only fixes the *layout under* them and cites what prior art puts where.

### What prior art does

- **dagu** splits three roots — `DAGsDir`, `DataDir`, `LogDir` (`internal/cmn/config/config.go:408-415 @ 86fe7e3`) — and shards run data by date under a per-DAG prefix: `<baseDir>/<safe-dag-name>/dag-runs/YYYY/MM/DD/<runPrefix><id>` (`internal/persis/file/dagrun/dataroot.go:57-70 @ 86fe7e3`), directory-locked (30s stale threshold), with attempts written as JSONL (`dagrun/writer.go:26-48`). It also caps output: `max_output_size` and `hist_retention_runs` / `hist_retention_days` (mutually exclusive) are schema fields (`schemas/dag.schema.json:575-600 @ 86fe7e3`).
- **pueue** puts state in `dirs::data_local_dir()/pueue` and task output in a sibling `task_logs/` directory created at daemon start (`pueue_lib/src/settings.rs:276-286`; `pueue/src/daemon/mod.rs:96-128 @ 7564e10`) — and `status --json` deliberately omits output, with `log -j` as the opt-in (`pueue/src/client/cli.rs:388-393 @ 7564e10`).
- **`@dortort/scheduler`** rotates at a byte cap keeping exactly one copy — *"Renames current to .1, creates fresh empty file. Keeps only 1 rotated copy"* (`src/logs/index.ts:66-79 @ 8954646`) — deletes by mtime after `logRetentionDays` (default 30, `src/types.ts:144`), and splits filenames per platform: `<taskId>.out.log`/`<taskId>.err.log` on darwin, `<taskId>.log` elsewhere (`src/logs/index.ts:25-34 @ 8954646`).
- **supercronic** doesn't write files at all: it drains child stdout/stderr line-by-line into structured log records tagged `channel` and `iteration` (`cron/cron.go:104-114, 199-201 @ 8e0a4a4`), with `-passthrough-logs` to bypass.
- **agent-cron** writes per-task per-day logs with a structured line format (`[ts] [START] task=…`, `[TOOL] name=…`, `[END] status=ok duration=8877ms`) and exposes `logs <slug> [date]` (`README.md:71-107 @ 12f6aeb`).
- **river** stores no logs; output is a `metadata.output` JSON key with a size story left to the user (`rivertype/river_type.go:14, 145 @ 48c0036`). dagu's equivalent — and the better idea for large outputs — is `stdout.artifact`, redirecting a stream straight to a run artifact file (`skills/dagu/references/harnesses.md`, `skills/dagu/SKILL.md:20 @ 86fe7e3`).

### Proposed layout

`[INFERENCE]` throughout; base dirs per the sibling CLI-UX doc. Three roots, following dagu, because they have different lifetimes and different backup value:

| root | Linux | macOS | Windows | contents |
|---|---|---|---|---|
| **config** | `$XDG_CONFIG_HOME/herdr-cron` → `~/.config/herdr-cron` | `~/Library/Application Support/herdr-cron` | `%APPDATA%\herdr-cron` | `jobs.yaml` (or `jobs.d/*.yaml`), `config.yaml` — the human/agent-editable, git-committable half |
| **state** | `$XDG_STATE_HOME/herdr-cron` → `~/.local/state/herdr-cron` | `~/Library/Application Support/herdr-cron/state` | `%LOCALAPPDATA%\herdr-cron\state` | `runs.db` or `history.jsonl`, `scheduler.json` (last-tick per job), `daemon.pid`, socket |
| **logs** | `$XDG_STATE_HOME/herdr-cron/logs` | `~/Library/Logs/herdr-cron` | `%LOCALAPPDATA%\herdr-cron\logs` | per-run output |

Note the deliberate divergence from pueue: pueue puts state in `data_local_dir` (`~/.local/share`), but the XDG spec's `state` directory is the better fit for run history and last-tick data, and `~/Library/Logs` is the macOS convention `@dortort/scheduler` implicitly acknowledges by splitting darwin filenames. Confirm against the sibling doc before implementing.

Per-run log path, date-sharded like dagu so pruning is a directory operation:

```
<logs>/runs/2026/09/03/<jobId>-<HHMMSS>/
    run.log          # combined, line-oriented, with the same [ts][LEVEL] shape agent-cron uses
    stdout.log       # only when the kind produces distinct streams
    stderr.log
    meta.json        # the run record (Q1), written last, atomically
```

Caps, each with a warrant: a per-run byte cap that truncates with an explicit marker (dagu's `max_output_size`, and its agent-DAG precedent `llm.observation_max_bytes` defaulting to 524288 bytes with *"A truncated result uses an explicit marker"*, `specs/032-agent-dag.md:104 @ 86fe7e3`); rotation at a byte cap keeping one copy (`@dortort/scheduler`); retention by *count* and by *days*, mutually exclusive (dagu's `hist_retention_runs` / `hist_retention_days`); and — the one nobody implements — a **total disk budget** across all runs, because 300 agent runs × a 512 KiB transcript is not obviously bounded to a user who set `logRetentionDays: 30`.

The three-surface split, following pueue's precedent exactly (`status --json` omits output; `log -j` includes it):

- **run-history record** — small, structured, always loaded, holds `outputExcerpt` only (first/last N bytes).
- **log file on disk** — full transcript, rotated, capped, retained.
- **TUI** — reads the record for the list, streams the file for the detail pane. See `2026-09-02-bubbletea-mouse-tui.md` for the viewport component; the domain requirement is that the list view must never need to open a log file.

## Q7. Daemon vs foreground vs no-daemon

### (a) Always-on background daemon owning the schedule

Prior art: pueue (`pueued`, Unix socket, `--daemonize` via a hand-rolled self-fork at `pueue/src/bin/pueued.rs:76-124 @ 7564e10`), dagu (`dagu start-all`), Claude Desktop (the app *is* the daemon).

- **Laptop that sleeps:** the daemon survives sleep and sees a large clock jump on wake. This is where the whole of Q3 becomes live. systemd's `WakeSystem=` can even resume the machine for a timer, but *"this functionality requires privileges and is thus generally only available in the system service manager"* — not available to a user-level Go daemon.
- **Windows:** works, but the transport must change. pueue's Unix-socket branch is `cfg`-gated off on Windows, forcing TCP+TLS there (`pueue_lib/src/network/socket/mod.rs:55-63 @ 7564e10`). Named pipes are the alternative. Auto-start needs a service or a Startup entry; see the sibling CLI-UX doc.
- **Agent adds a job and wants it live now:** trivially solved — the CLI tells the daemon. This is the option's decisive advantage. supercronic's fallback (`SIGUSR2` or `-inotify`, `README.md:222-237 @ 8e0a4a4`) shows the file-watching alternative when there's no socket.
- **Cost:** IPC protocol, lifecycle management, PID/socket cleanup (pueue installs a panic hook to clean both up, `pueue/src/daemon/mod.rs:145-166 @ 7564e10`), version skew between CLI and daemon (pueue answers this with a version string in the handshake, `:124`), and "is it running?" as a support question.

### (b) Foreground `herdr-cron run` in a Herdr pane

Prior art: supercronic (its entire reason to exist), gronx `tasker`, agent-cron (`agent-cron start`), Claude Code `/loop` (tasks live in the session).

- **Laptop that sleeps:** the process survives sleep with the pane; the tick loop just wakes late. gronx's 100ms sleep-poll and pueue's 300ms poll are both trivially correct across suspend, where a computed `time.After(delay)` may not be.
- **Windows:** easiest of the three — it's just a process, and the child-spawn split is 20 lines (`gronx tasker_windows.go:24-26 @ 74da195`).
- **Agent adds a job and wants it live now:** needs a reload path. supercronic's `-inotify` watches for `Write` *and* `Remove* to survive atomic writes (`README.md:232 @ 8e0a4a4`) — which is exactly what an agent's Edit tool does. This works, and it is much less machinery than a socket.
- **Cost:** it dies when the pane dies, the terminal closes, or the user reboots. Claude Code documents this bluntly for `/loop`: *"Tasks only fire while Claude Code is running and idle. Closing the terminal or letting the session exit stops them firing."* For a *Herdr* tool this cost is smaller than usual — Herdr is a multiplexer whose panes are supposed to outlive attachment — but it is not zero, and the sibling Herdr doc should be consulted on pane persistence guarantees.

### (c) No daemon; register with the OS scheduler

Prior art: `@dortort/scheduler` (launchd + crontab with marker fences, `src/schedulers/linux.ts:13-38 @ 8954646`), and systemd timers as the general mechanism.

- **Laptop that sleeps:** *this is the only option with a real answer.* `Persistent=true` gives you exactly-one catch-up on resume, for free, correctly, including across a power-off (quoted in Q3). No user-space daemon can match that without reimplementing it.
- **Windows:** the option collapses. `@dortort/scheduler` requires *"macOS (launchd) or Linux (crontab)"* (`README.md:32-34 @ 8954646`) and has a `schedulers/darwin.ts` and `schedulers/linux.ts` and nothing else. Task Scheduler is a third implementation with a genuinely different model (XML task definitions, `schtasks.exe` or COM). Three backends, three test matrices, and Task Scheduler's `StartWhenAvailable` is the nearest analogue to `Persistent=`.
- **Agent adds a job and wants it live now:** this is the option's real weakness, and it is worse than it looks. Adding a job means mutating the user's crontab or writing a plist/unit and reloading it — an operation that can fail on permissions, that is invisible in `jobs.yaml`, and that leaves the tool's declared state and the OS's actual state able to diverge. Marker fences (`# claude-scheduler:<id>:begin`) make it *reversible*, which is necessary but not sufficient; you also need a `sync`/`doctor` command, which `@dortort/scheduler` has (`/scheduler:status` — "Health check for the scheduling system", plus a `sync` CLI subcommand, `README.md:64, 82 @ 8954646`).
- **Cost:** three OS backends, a drift problem, and no place for a run to live while it runs — each fire is a fresh process, so "is it running now?" needs a lock file, and overlap prevention becomes the CLI's job rather than the scheduler's.

### The shape the evidence actually suggests

`[INFERENCE]` These are not mutually exclusive, and the systems that solved this best did not choose. Claude Code ships all three tiers side by side and publishes a comparison table (cloud / desktop / `/loop`, at https://code.claude.com/docs/en/scheduled-tasks) precisely because the tradeoff is irreducible. dagu ships `dagu start-all` (daemon) and `dagu start` (one-shot exec) from one binary.

The cheap version of that insight: **make `herdr-cron run-once <job-id>` the execution primitive**, so (b) is a loop around it and (c) is the OS invoking it. Then (a) vs (b) is a deployment question rather than an architecture question, and (c) becomes an optional `herdr-cron install-timer` that generates a unit/plist/task calling the same primitive. That ordering also means the missed-run policy of Q3 has one implementation, not three.

## Q8. Safety

An unattended scheduler that prompts a coding agent can burn tokens and mutate repos. What is actually grounded:

**Dry-run / validate.** Grounded. supercronic's `-test`: *"test crontab (does not run jobs)"* (`main.go:36 @ 8e0a4a4`). dagu's `dagu validate` and `dagu schema`, which its own skill file instructs the model to prefer over guessing (`skills/dagu/SKILL.md:16 @ 86fe7e3`). Both are validate-only, not simulate-the-run; a true "show me what would fire in the next 24h" has partial prior art in `@dortort/scheduler`'s `cron/parser.ts` "next runs" and its `/scheduler:list` showing next run times (`README.md:59, 64 @ 8954646`).

**Max runs per day.** Not grounded as such. The nearest real mechanisms are river's `UniqueOpts.ByPeriod`, which enforces at-most-one-per-period as a unique-index invariant (`insert_opts.go:161-169 @ 48c0036`), and Claude Code's 1-hour minimum interval for cloud routines vs. 1-minute for local. `[INFERENCE]` A per-job daily counter is the direct expression of the same intent and is the only guard that holds across catch-up storms, manual runs and retries simultaneously.

**`enabled` default of false.** *Contested by the evidence, and this matters.* Every schema I read defaults to enabled: `enabled: z.boolean().default(true)` (`ccs-dortort src/types.ts:103 @ 8954646`); dagu DAGs are live once the file is in `DAGsDir`; supercronic runs every line in the crontab; Claude Desktop tasks are Active on creation with a Paused toggle. Not one defaults to off. The counter-evidence is indirect but real: both GitHub and Claude Code decided that a recurring unattended job needs an *automatic* off switch (60-day idle auto-disable; 7-day expiry) — i.e. the industry's expressed concern is about jobs that stay on too long, not about jobs starting on. `[INFERENCE]` `enabled: false` by default for `kind: agent` and `true` for `kind: shell` is defensible but violates least-surprise; the alternative — default on, plus a mandatory expiry or a `max_consecutive_failures` auto-disable — matches prior art more closely. This is genuinely undecided and appears in the closing section.

**Confirmation for destructive kinds.** Partially grounded. `@dortort/scheduler` has `sensitiveFilePolicy: z.enum(['block','warn','allow']).default('block')` on worktree operations and a `BLOCKED_ENV_VARS` denylist enforced in the Zod schema (`src/types.ts:64, 79-82 @ 8954646`), plus the rule that *"Project configs cannot set `skipPermissions`"* (`README.md:111 @ 8954646`) — a trust boundary between a repo-checked-in config and the user's own. Claude Desktop's model is per-task permission modes with saved always-allow grants that are reviewable and revocable from the task's detail page, and the honest failure mode is documented: *"If a task runs in Manual mode and needs to run a tool it doesn't have permission for, the run stalls until you approve it."*

**Isolation.** Grounded and, I think, underrated. Claude Desktop: *"By default, scheduled tasks run against whatever state your working directory is in, including uncommitted changes. Enable the worktree toggle when creating the task to give each run its own isolated Git worktree."* `@dortort/scheduler` has the same feature as a first-class config block. dagu has `git.worktree.add` / `git.worktree.remove` actions (`skills/dagu/SKILL.md:19 @ 86fe7e3`). Three independent implementations converging on git-worktree-per-run is the strongest safety signal in this entire document, and it costs nothing at schedule time.

**Audit log.** Grounded in shape. dagu has an `internal/audit` package and an `audit` file-store collection (`internal/persis/file/audit/`, `internal/audit/` @ 86fe7e3). `@dortort/scheduler` has JSONL execution history with `triggeredBy`, `executedCommand`, `worktreePushed` (`src/types.ts:115-136 @ 8954646`). Claude Desktop keeps *"every past run, including skipped runs"* with hoverable skip reasons. The transferable requirement: the history must record **skipped and never-run occurrences**, not just executions — otherwise "why didn't my job run" is unanswerable, which is the single most common complaint about cron.

**Kill switch.** Grounded, and cheap: `CLAUDE_CODE_DISABLE_CRON=1` disables the whole scheduler including already-scheduled tasks (https://code.claude.com/docs/en/scheduled-tasks). One environment variable that stops everything is worth more than any per-job guard when something goes wrong at 3am.

**The scheduler preamble.** `[INFERENCE]`, but with a concrete precedent. `ccs-biosphere`'s recipe template makes its *first* prompt step a standing notice (`recipe-templates/epic.template.json @ 5c4a680`, quoted in full in §1.6.5): *"you're being controlled by a scheduler and there is no user, so you can't ask them questions... abort if you can't best guess them."* Claude Desktop's stall behaviour is the documented failure this prevents. This belongs in the product, prepended to every `kind: agent` run, not left to each user's prompt.

**Jitter as a safety feature.** Grounded three times over: Claude Code (task-ID-derived, up to 30 min, or half the interval for sub-hourly), Claude Desktop (*"a small delay of a few minutes... The delay is deterministic: the same task always starts at the same offset"*), and systemd (`RandomizedDelaySec=` + `FixedRandomDelay=` derived *"from the machine ID, the manager's user identifier, and the timer unit's name"*). GitHub's version is advice rather than mechanism (*"schedule your workflow to run at a different time of the hour"*). For herdr-cron, deterministic per-job jitter prevents six jobs at `0 9 * * *` from launching six agents into the same repo simultaneously.

---

## Could not verify

- **Herdr's actual pane/plugin contract.** Everything in Q1(b) about "prompt an agent in a pane and capture its answer" is stated as a domain requirement, not as a verified capability. `2026-09-02-herdr-plugin-integration.md` owns that evidence; I did not read the Herdr source or CLI. Whether a pane can be addressed, whether an agent's "final message" is observable, and whether a pane survives detach are all unverified here.
- **OpenAI Codex "automations" scheduling semantics.** Web search returned only blog posts and open feature-request issues (`openai/codex#22310`, `#25466`). I found no official OpenAI documentation page defining Codex automation schedule semantics, missed-run behaviour, or storage location. The only Codex fact cited above is `codex exec "<prompt>"` as the non-interactive invocation, sourced from dagu's harness adapter table rather than from OpenAI.
- **Claude Code Routines (cloud).** Referenced by the two Claude Code docs pages I read, but I did not fetch `https://code.claude.com/docs/en/routines` itself. The only Routines facts cited are those restated in the comparison table on the two pages I did read (cloud, no machine required, 1-hour minimum interval, triggerable by schedule/API/GitHub events).
- **dagu's `harness.run` behaviour under a scheduler**, specifically what happens to a managed OpenCode session when a *scheduled* (not manual) run's host sleeps. The harness reference documents worker-disappearance recovery but not sleep.
- **Whether dagu's catch-up interacts with `skip_if_successful`.** Both fields exist; I did not read the tick planner closely enough to state the combined semantics.
- **Windows behaviour for anything.** No claim in this document about Windows was executed or tested; all Windows statements are read from source `cfg` gates (pueue's Unix-socket exclusion, gronx's `CREATE_NEW_PROCESS_GROUP`) or from a README's stated requirements (`@dortort/scheduler`: macOS/Linux only). Task Scheduler was not researched at all.
- **Exact base directories per OS.** Q6's table is a proposal that must be reconciled with `2026-09-02-agent-skill-and-cli-ux.md`, which owns that research. I verified only pueue's choice (`dirs::data_local_dir()`) and dagu's three-root split.
- **`hist_retention_runs` / `max_output_size` semantics in dagu** were read from the JSON Schema descriptions only, not from the implementing Go code.
- **Cost/token accounting.** Only agent-cron models it (`RunResult{cost, inputTokens, outputTokens}`), and I did not verify whether it is populated for all runners or only the Agent SDK path. No other system in the corpus tracks money.

---

## Decisions the human must make before implementation starts

Three, and none of them is resolved by the evidence above.

### Decision 1 — Storage: where does run history live? (Q2)

The *definitions* question is settled by unanimous prior art: **files, YAML, human- and agent-editable, git-committable.** Eight of eight systems do this, including the most feature-complete one. Do not put job definitions in SQLite.

What is open is the history store, and the second-order question of *which fields live in the file at all*.

- **Option A — JSONL append-only.** Warrant: `@dortort/scheduler` (`src/types.ts:115-136 @ 8954646`), capped at 100 records. Zero dependencies, greppable, trivially correct. Consequence: interactive filtering in the TUI is a full scan; concurrent writers need care; long history is a linear cost.
- **Option B — date-sharded file tree.** Warrant: dagu (`internal/persis/file/dagrun/dataroot.go:57-70 @ 86fe7e3`), with a directory lock and a separate index package. Consequence: prunes by directory, scales, but you will end up writing dagu's `dagrunindex` yourself the first time the TUI needs "all failures across all jobs".
- **Option C — SQLite.** Warrant: river's driver exists; no *scheduler* in this corpus chose it. Consequence: real queries for the TUI, transactional run-state updates, one file to back up; costs a dependency choice (CGo vs pure Go — see the sibling CLI-UX doc) and schema migrations forever.

The sub-decision that must be made at the same time, because it constrains the others: **does `enabled` live in the YAML file or in the state store?** Claude Desktop deliberately keeps the prompt in `SKILL.md` and *"Schedule, folder, model, and enabled state"* outside it, so a UI toggle never rewrites a user's text file. dagu and `@dortort/scheduler` put `enabled` in the definition. If the TUI has an enable/disable toggle — and it will — option "in the file" means the TUI rewrites user-authored YAML on every click, with comment and formatting loss as the consequence.

### Decision 2 — Architecture: daemon, foreground, or OS registration? (Q7)

Not resolvable from evidence because the three options optimise different things and the prior art ships all three.

- **Daemon (a).** Warrant: pueue, dagu, Claude Desktop. Wins on "agent adds a job, it's live immediately". Loses on Windows transport (pueue is forced to TCP+TLS there, `pueue_lib/src/network/socket/mod.rs:55-63 @ 7564e10`), on lifecycle complexity, and on "is it running?".
- **Foreground in a Herdr pane (b).** Warrant: supercronic, gronx `tasker`, agent-cron, Claude Code `/loop`. Wins on simplicity and on Windows. Loses when the pane dies — Claude Code documents this cost explicitly. Reload solved cheaply by `-inotify`-style file watching (`supercronic README.md:230 @ 8e0a4a4`). *This is the option most aligned with a tool named `herdr-cron`, and its viability depends entirely on Herdr's pane-persistence guarantees, which this document could not verify.*
- **OS registration (c).** Warrant: `@dortort/scheduler`, systemd timers. **The only option with a correct answer to "the laptop was asleep for six hours"** — `Persistent=true`, quoted in Q3, gives exactly-one catch-up including across power-off, for free. Loses hard on Windows (three backends, and `@dortort/scheduler` simply doesn't support it) and on state drift between `jobs.yaml` and the OS.

The mitigating move, if the human wants to defer: make `herdr-cron run-once <id>` the primitive and treat (a)/(b)/(c) as three drivers over it. That does not make the decision go away — a default must still ship — but it makes it reversible.

### Decision 3 — Missed runs and the default `enabled` state: how autonomous is this thing? (Q3 + Q8)

These are one decision wearing two hats, because both answer "how much can this spend while nobody is watching".

- **Missed runs.** The evidence *leans* one way — Claude Desktop, the closest analogue in existence, chose exactly one catch-up run for the most recently missed time, 7-day lookback, notified. river and Claude Code `/loop` choose none at all. dagu, the only system that replays everything, is a workflow engine for cheap deterministic jobs and caps replay at 1000. Options: `off` (river, `/loop`, cron, GH Actions) | `latest` (Claude Desktop, systemd `Persistent=`) | `all` within a window (dagu). The consequence of getting this wrong is measured in dollars and in unattended repo mutations, and it is asymmetric: `off` under-delivers quietly, `all` over-delivers expensively.
- **Default `enabled`.** Genuinely contested, as documented in Q8: every schema in the corpus defaults to `true`, while the two hosted products both bolted on automatic *disable* mechanisms (GitHub's 60-day idle, Claude Code's 7-day expiry). Options: (i) `enabled: true` like all prior art, plus a mandatory `max_consecutive_failures` auto-disable; (ii) `enabled: false` by default for `kind: agent` only, so a scheduled agent job requires one deliberate act before it can ever spend money; (iii) `enabled: true` plus a mandatory expiry date on every job, copying Claude Code's 7-day rule.

The reason these are one decision: with `catchup: all` and `enabled: true` by default, opening a laptop lid after a weekend can start N agent runs in one repo. With `catchup: off` and `enabled: false`, a user will file a bug saying the scheduler doesn't work. The product's position on that spectrum is a human call, and every downstream default — jitter, `max_runs_per_day`, worktree isolation on or off, whether a scheduler preamble is prepended — follows from it.
