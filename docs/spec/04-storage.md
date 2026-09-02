---
title: herdr-cron — Storage and on-disk layout
date: 2026-09-02
status: spec (normative)
---

# Storage and on-disk layout

Normative. Decision: **`jobs.yaml` + JSONL history + `state.json`**, no database
([`README.md`](README.md) D2). Definitions are files because eight of eight surveyed systems put
them in files (`docs/research/2026-09-02-prior-art-and-domain-model.md` Q2); history is JSONL
because it costs zero dependencies and stays greppable.

---

## 1. Roots

Two roots, resolved once at startup and printed by `herdr-cron status`.

| Root | Linux | macOS | Windows |
| --- | --- | --- | --- |
| **config** | `~/.config/herdr-cron` | `~/Library/Application Support/herdr-cron/config` | `%LocalAppData%\herdr-cron\config` |
| **state** | `~/.local/state/herdr-cron` | `~/Library/Application Support/herdr-cron/state` | `%LocalAppData%\herdr-cron\state` |

Rules, each with a reason:

- **Windows uses `LocalAppData`, not `AppData` (Roaming).** `os.UserConfigDir()` returns Roaming
  on Windows; that is wrong here. A roaming job database replicated onto another machine fires
  jobs against absolute paths that do not exist there
  (`docs/research/2026-09-02-agent-skill-and-cli-ux.md` B4).
- **macOS collapses config, data, and state into one directory**, so herdr-cron adds the
  `config/` and `state/` subdirectories itself rather than mixing a job file with a run log tree.
- The resolution follows `adrg/xdg`'s mapping. Whether to take the dependency or copy the ~40
  lines is an implementation call; the resolved paths above are normative either way.

Overrides, highest precedence first:

1. `--config PATH` (file) and `--state-dir PATH` (directory) flags.
2. `HERDR_CRON_CONFIG` and `HERDR_CRON_STATE_DIR`.
3. `HERDR_CRON_HOME` — sets both, as `<home>/config` and `<home>/state`. This is the one to use
   for tests and for the Herdr plugin front door, where `HERDR_PLUGIN_STATE_DIR` is handed to us.
4. `XDG_CONFIG_HOME` / `XDG_STATE_HOME`, honoured **on all three platforms** (that is `xdg`'s
   behaviour, not the stdlib's, and it is what makes a temp-dir test possible on Windows).
5. The table above.

When herdr-cron runs as a Herdr plugin, `HERDR_PLUGIN_STATE_DIR` MUST be used as the state root:
`HERDR_PLUGIN_ROOT` is replaced wholesale on every plugin update, so nothing durable may live
under it (`docs/research/2026-09-02-herdr-plugin-integration.md` §1).

---

## 2. Layout

```
<config>/
  jobs.yaml                     definitions — authored, git-committable
  config.toml                   daemon/TUI/notifier settings (optional)

<state>/
  state.json                    mutable per-job state; owned by the daemon
  overrides.json                enabled overrides; written by CLI, TUI, and daemon
  overrides.lock                advisory lock for overrides.json
  daemon.json                   heartbeat: pid, started_at, driver, socketless status
  daemon.lock                   single-instance lock
  triggers/
    <ulid>.json                 one-shot request files from CLI/TUI to the daemon
  runs/
    <jobId>.jsonl               append-only run history, one JSON object per line
    <jobId>.jsonl.lock          advisory lock serialising appends
    <jobId>.jsonl.run.lock      advisory lock held for the duration of a run
  logs/
    <jobId>/
      <runId>.log               captured stdout+stderr, or agent transcript
  tmp/                          atomic-write staging; same filesystem as the target
```

Every path under `<state>` is machine-local and disposable in the sense that deleting it loses
history but never loses a job definition. That asymmetry is the point of the split.

---

## 3. `jobs.yaml`

Schema in [`03-job-model.md`](03-job-model.md). Storage rules:

- **The daemon never writes it.** Only `job add`, `job update`, and `job rm` do.
- **Writes preserve comments and key order.** Implemented by round-tripping through
  `yaml.Node` (gopkg.in/yaml.v3) and editing the node tree, not by unmarshalling into a struct
  and re-marshalling. A user's `# runs after the nightly backup` comment surviving `job update`
  is a hard requirement: this file is meant to be committed and reviewed.
- **Writes are atomic**: render to `<state>/tmp/jobs.yaml.<ulid>`, `fsync`, then `rename` over
  the target. On Windows `rename` over an existing file requires `MoveFileEx` semantics; use
  `os.Rename` and, on failure, retry once after a 50 ms backoff (antivirus and indexers hold
  transient handles).
- **Writes are guarded by an advisory lock** on `<config>/jobs.yaml.lock` held for the duration
  of read-modify-write. Two concurrent `job add` calls from two agents MUST NOT interleave.
- **A write is preceded by a re-read.** The CLI never edits from a cached parse; the file may
  have been edited by hand or by git between commands.
- **A failed validation aborts before the rename.** An invalid `jobs.yaml` is never written.

### 3.1 Reload

The daemon watches `<config>/jobs.yaml` with `fsnotify` and reloads on change, debounced 200 ms
(editors write-then-rename, producing two events). Because rename replaces the inode, the watch
is placed on the **directory**, not the file — watching the file alone silently stops working
after the first atomic write.

A reload that fails validation is **rejected wholesale**: the daemon keeps the previous schedule,
logs the error, and surfaces it in `status` as `configError`. A scheduler must not half-apply a
broken file.

fsnotify does not fire reliably on every filesystem (network mounts, some WSL paths, containers
with bind mounts). The daemon therefore also stats the file every 5 seconds and reloads on
mtime+size change. The watcher is the fast path, the poll is the guarantee.

---

## 4. `state.json`

`state.json` is written only by the executing scheduler process (§9): atomic replace, and under
the `daemon`/`foreground` drivers a single writer with no locking needed against itself. Readers
tolerate a torn read by retrying once. The `enabled` override
lives in a **separate** file, `overrides.json`, because it has three writers (CLI, TUI, daemon)
and must be writable with no daemon running at all
([`05-cli.md`](05-cli.md) §4).

```json
{
  "version": 1,
  "updatedAt": "2026-09-02T11:32:04+09:00",
  "jobs": {
    "nightly-deps": {
      "lastScheduledAt": "2026-09-02T03:17:00+09:00",
      "lastRunId": "nightly-deps-20260901T181700Z",
      "lastStatus": "success",
      "lastFinishedAt": "2026-09-02T03:41:12+09:00",
      "consecutiveFailures": 0,
      "runsToday": {"date": "2026-09-02", "count": 1}
    }
  }
}
```

`overrides.json`, in the same directory:

```json
{
  "version": 1,
  "overrides": {
    "build-smoke": {
      "enabled": false,
      "declaredEnabled": true,
      "reason": "manual",
      "at": "2026-09-01T22:10:00+09:00"
    }
  }
}
```

- `lastScheduledAt` is the catch-up watermark and is written **before** a run executes
  ([`03-job-model.md`](03-job-model.md) §4.2).
- `overrides[].declaredEnabled` records what `jobs.yaml` said when the override was created, so
  a later edit to the file invalidates it (`03-job-model.md` §5).
- `reason` is `manual` or `auto_failures`.
- Entries for job ids no longer present in `jobs.yaml` are retained for 30 days and then pruned,
  so that renaming a job away and back does not silently resurrect a stale override, while a
  temporary git checkout does not destroy state.
- `overrides.json` is guarded by an advisory lock on `overrides.lock` for the duration of every
  read-modify-write, held by whichever of the CLI, the TUI, or the daemon is writing. It is the
  only state file with more than one writer, and the lock is what makes `job pause` work while
  the daemon is running and while it is not. The daemon watches it and applies a change within
  one debounce interval.

Written with the same atomic temp+rename+fsync sequence as `jobs.yaml`, at most once per second
(coalesced), and always immediately before a run starts and after a run finishes.

---

## 5. Run history — `runs/<jobId>.jsonl`

One JSON object per line, append-only, newest last. Schema in
[`03-job-model.md`](03-job-model.md) §6.

- A run appends a `running` record at start and a **second, terminal** record at finish. Readers
  reduce by `runId`, last write wins. Appending rather than rewriting is what makes the file
  crash-safe with no lock and no fsync-per-field: a killed daemon leaves a `running` record with
  no terminal partner, which is exactly the truth.
- On daemon start, any `running` record without a terminal partner and without a live process is
  closed out as `status: "failure"`, `reason: "daemon_died"`. This is river's age-inferred death,
  minus the database (`docs/research/2026-09-02-prior-art-and-domain-model.md` §1.5).
- Appends use `O_APPEND` with a single `write` syscall per line, which is atomic for lines under
  `PIPE_BUF` on Unix, and is serialised across concurrent `run-once` processes by a per-job
  advisory lock on the file ([`02-architecture.md`](02-architecture.md) §2.1).
- **Retention**: `history.max_runs_per_job` (default 500) and `history.max_age` (default 90d),
  whichever is hit first. Compaction rewrites the file atomically and runs at most once per day
  per job, at daemon start and after each run that crosses the threshold.

Full-history queries (`run list` with no `--job`) read every `<jobId>.jsonl` and merge. With the
default retention that is a few hundred KiB — a linear scan is correct and fast enough. If a
future TUI filter makes this the bottleneck, the fix is an index file, not a database; that is
dagu's path and it is a known one (`prior-art` Q2 Option B).

---

## 6. Logs

`logs/<jobId>/<runId>.log` holds the raw captured stream: interleaved stdout+stderr for
`kind: shell`, the `agent read --source recent-unwrapped` transcript for `kind: agent`.

- Written incrementally as output arrives, so `run logs --follow` works while the run is live.
- Capped at `logs.max_bytes` per run (default 8 MiB). On overflow the middle is elided and a
  single marker line `... <n> bytes elided by herdr-cron ...` is written; the head (4 MiB) and
  tail (4 MiB) are kept. Truncating the tail would throw away the error; truncating the head
  would throw away what caused it.
- Deleted together with their run record by retention (§5). Log deletion is driven by the JSONL
  compaction so the two can never disagree.

---

## 7. Single instance and the daemon heartbeat

`daemon.lock` is created with `O_CREAT|O_EXCL` and holds an advisory file lock
(`flock` on Unix, `LockFileEx` on Windows) for the daemon's lifetime. gocron's `Locker` is a
per-run distributed lock and is explicitly *not* this
(`docs/research/2026-09-02-gocron-scheduling-engine.md` §7).

`daemon.json`, rewritten every 15 seconds:

```json
{
  "pid": 40211,
  "startedAt": "2026-09-02T09:00:11+09:00",
  "heartbeatAt": "2026-09-02T11:32:00+09:00",
  "version": "0.1.0",
  "driver": "daemon",
  "configPath": "/home/huke/.config/herdr-cron/jobs.yaml",
  "jobCount": 7,
  "configError": null
}
```

`herdr-cron status` reads it and reports the daemon as:

- **running** when `daemon.lock` is held by another process **and** `heartbeatAt` is under 60 s
  old,
- **stale** when `daemon.json` exists but either test fails — this is a crash, and `status` says
  so with the last heartbeat time,
- **stopped** when `daemon.json` does not exist.

**Both tests are required, and the lock is the authoritative one.** A `kill -9` leaves a
heartbeat that stays under 60 s old for a full minute; trusting the timestamp alone reports a
dead daemon as running, and — worse — makes `daemon --detach` a silent no-op during that window.
A client tests the lock by attempting to take it: success means nobody is home, and the client
releases it immediately. The kernel releases the lock when the holder dies, which is exactly the
property the heartbeat lacks.

Because there is no socket ([`02-architecture.md`](02-architecture.md)), these two files are the
entire liveness protocol. `daemon.json` is therefore written with the same atomic replace as
everything else; a half-written heartbeat that parses as "stopped" would make the CLI lie.

---

## 8. Trigger files — the CLI → daemon channel

**Terminology.** "Trigger" is used in two unrelated senses in this spec. A *run trigger* is the
provenance enum on a run record (`scheduler` | `manual` | `catchup` | `retry` | `startup`,
[`03-job-model.md`](03-job-model.md) §6). A *trigger file*, specified here, is a request from a
client to the daemon. They are never the same object; where ambiguity is possible, the spec says
"run trigger" or "trigger file".

A one-shot request is a file. `job run`, `job cancel`, and `reload` write
`<state>/triggers/<ulid>.json` and the daemon consumes it. `job pause` and `job resume` do NOT
use this channel — they write `overrides.json` directly under its lock (§4), which is what makes
them work with no daemon running.

```json
{
  "id": "01K4E7Q0YB3T5S2M8N9P",
  "createdAt": "2026-09-02T11:33:41+09:00",
  "action": "run",
  "jobId": "build-smoke",
  "requestedBy": "cli",
  "wait": true
}
```

Protocol:

1. The client writes the file atomically into `triggers/` (staged in `tmp/`, then renamed — a
   half-written trigger must never be readable).
2. The daemon, watching `triggers/` with fsnotify plus the same 5-second poll fallback, reads it,
   **renames it to `triggers/<ulid>.claimed`**, acts, then deletes it. The rename is the claim;
   it makes double-processing impossible without a lock.
3. For `wait: true`, the client polls the job's `runs/<jobId>.jsonl` for a terminal record whose
   `runId` matches the one the daemon writes back into `triggers/<ulid>.result`. Poll interval
   100 ms.
4. Triggers older than 5 minutes with no daemon to claim them are garbage-collected by the next
   daemon start, and the client reports `daemon_unreachable` after a 3-second grace period rather
   than hanging.

The cost of this design is the latency in step 3 — roughly 100–300 ms for `job run --wait` to
notice completion, versus a socket's immediate return. The benefit is that there is exactly one
implementation for Linux, macOS, and Windows, with no named-pipe branch. Herdr's own docs push
plugins toward shelling out to the CLI precisely to avoid that Unix-socket-vs-named-pipe split
(`herdr-plugin-integration` §2), which is the same tradeoff resolved the same way.

---

## 9. Concurrency summary

| File | Writers | Mechanism |
| --- | --- | --- |
| `jobs.yaml` | CLI, TUI, humans, git | `jobs.yaml.lock` advisory lock + atomic rename |
| `state.json` | the executing scheduler process | atomic rename, coalesced |
| `overrides.json` | CLI, TUI, daemon | `overrides.lock` advisory lock + atomic rename |
| `runs/<jobId>.jsonl` | the executing scheduler process | `O_APPEND`, serialised by `runs/<jobId>.jsonl.lock` |
| `logs/**` | the executing scheduler process | streaming append |
| `daemon.json` | daemon only | atomic rename every 15 s |
| `triggers/*` | CLI, TUI (create); daemon (claim/delete) | atomic rename to create, rename to claim |

"The executing scheduler process" is the daemon under the `daemon` and `foreground` drivers, and
a short-lived `herdr-cron run-once` process under the `os-scheduler` driver
([`02-architecture.md`](02-architecture.md) §2). Under `os-scheduler` several `run-once`
processes can be alive at once, which is why the run-history append takes a per-job advisory
lock rather than relying on a single writer.

Two locks, not one, and they are not interchangeable. `<jobId>.jsonl.run.lock` is held for the
**whole run** and is what `concurrency: skip` observes: a second process that cannot take it
records a `skipped` run with reason `overlap` and exits 0. `<jobId>.jsonl.lock` is taken and
released around **each append**, which happens twice inside a run — once for the `running`
record and once for the terminal one. Merging them would make the second append deadlock
against the run's own lock. Both are advisory `flock`/`LockFileEx` locks on sidecar files rather
than on the JSONL itself, because a lock taken on the data file cannot be re-entered by the
process already holding it.

Read-only clients (`job list`, the TUI) take no locks at all. A torn read is handled by parse
failure + one retry after 50 ms, which is cheaper and simpler than a reader lock and is
sufficient given that every writer uses atomic rename.
