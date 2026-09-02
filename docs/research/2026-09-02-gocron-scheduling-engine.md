---
title: "gocron as the scheduling core of herdr-cron"
date: 2026-09-02
subject: github.com/go-co-op/gocron
status: research / evidence
sources_pinned:
  gocron_v2_branch: cc444c2a6f2cb66b51ef180940de6398d1f9a10d  # branch v2, committed 2026-07-17
  gocron_latest_release_tag: v2.22.0                          # 10cdd5e5558f8e9b0dfa848df2b0e02933f44163
  gocron_v1_branch: acbaede468675216d905c9969c5d1d27fadc0ff1  # branch v1, unmaintained
  robfig_cron_v3: e843a09e5b2db454d77aad25b1660173445fb2fc    # == tag v3.0.1, committed 2019-07-15
  adhocore_gronx: 74da1959a9f9d62b3391431d828c65af03e693e2    # main, committed 2026-08-17; latest release v1.8.1
  jonboulle_clockwork: 6d8d032a18422c2e3ef651170a8a55012d1f704c  # tag v0.5.0
  go_toolchain: go1.26.2 linux/amd64
---

# gocron as the scheduling core of herdr-cron

Cross-document contract: gocron is the scheduling engine. Bubble Tea is the TUI,
Herdr is the host agents run in, and the Agent Skill is how the agent learns the
CLI — those are researched elsewhere and are only referenced by name here.

## Citation tags

Every claim below carries one of these tags at the claim.

| Tag | Meaning |
| --- | --- |
| `[GC file:lines]` | gocron v2 clone at `/tmp/hc-research/gocron`, pinned SHA `cc444c2a6f2cb66b51ef180940de6398d1f9a10d` (branch `v2`, commit date 2026-07-17). Clone command: `git clone --depth 1 https://github.com/go-co-op/gocron /tmp/hc-research/gocron`; SHA from `git rev-parse HEAD`. |
| `[GC-2220 file:lines]` | gocron at release tag `v2.22.0`, fetched via `curl -sf https://raw.githubusercontent.com/go-co-op/gocron/v2.22.0/<file>`. Used only where the released API differs from the pinned branch tip. |
| `[GC-V1 file:lines]` | gocron `v1` branch clone at `/tmp/hc-research/gocron-v1`, pinned SHA `acbaede468675216d905c9969c5d1d27fadc0ff1`. Clone command: `git clone --depth 1 -b v1 https://github.com/go-co-op/gocron /tmp/hc-research/gocron-v1`. |
| `[RF file:lines]` | robfig/cron v3 clone at `/tmp/hc-research/robfig-cron`, pinned SHA `e843a09e5b2db454d77aad25b1660173445fb2fc`, which is exactly tag `v3.0.1` (`git ls-remote --tags https://github.com/robfig/cron` reports `ccba498c... refs/tags/v3.0.1`; the clone HEAD equals the branch `v3` tip). |
| `[GX file:lines]` | adhocore/gronx clone at `/tmp/hc-research/gronx`, pinned SHA `74da1959a9f9d62b3391431d828c65af03e693e2` (commit date 2026-08-17). Latest release tag is `v1.8.1`. |
| `[CW file:lines]` | jonboulle/clockwork clone at `/tmp/hc-research/clockwork`, tag `v0.5.0`, SHA `6d8d032a18422c2e3ef651170a8a55012d1f704c`. |
| `[RUN <n>]` | A program I wrote and executed with `go run .` under `go1.26.2`; sources at `/tmp/hc-research/probe/p<n>/main.go`. Exact observed stdout is quoted. Dependency provenance per probe, from each `go.mod`: **RUN 1, 2, 3** → gocron `v2.22.0` (released); **RUN 4, 5, 8, 9, 10** → the pinned branch clone via `go mod edit -replace=github.com/go-co-op/gocron/v2=/tmp/hc-research/gocron`; **RUN 6** → gronx `v1.8.1` + robfig `v3.0.1` (both released); **RUN 7** → same but with `-replace=github.com/adhocore/gronx=/tmp/hc-research/gronx`. |
| `[GOSRC file:line]` | Go standard library / runtime source shipped with the local `go1.26.2` toolchain, under `/usr/local/go/src`. |
| `[MAN url]` | Manual page or spec at the given URL. |

---

## 1. v1 vs v2: which to use

**Module paths.** v1 is `module github.com/go-co-op/gocron` with `go 1.16`
`[GC-V1 go.mod:1-3]`. v2 is `module github.com/go-co-op/gocron/v2` with
`go 1.22` `[GC go.mod:1-3]`. They are distinct modules and can coexist in one
build graph.

**Which is maintained.** The v1 branch README opens with, verbatim:

```
| :exclamation: Gocron is officially on [v2](https://github.com/go-co-op/gocron/tree/v2). The v1 branch is no longer maintained. PRs will not be accepted. |
```

`[GC-V1 README.md:3]`. `git ls-remote --heads https://github.com/go-co-op/gocron`
confirms both `refs/heads/v1` and `refs/heads/v2` exist, with `v2` at the SHA
this document pins. The newest v1 tag is `v1.37.0`; v2 tags run to `v2.22.0`
(`git ls-remote --tags`). Treat v1 as archived.

**The API break.** The repository ships its own migration guide
`[GC migration_v1_to_v2.md]`. The load-bearing differences:

- v1 is a **fluent builder** terminated by `Do()`:
  `s.Every(1).Second().Do(taskFunc)` `[GC migration_v1_to_v2.md:70-73]`, and
  `s := gocron.NewScheduler(time.UTC)` takes the location positionally and
  returns no error `[GC migration_v1_to_v2.md:52-56]`.
- v2 is **explicit job definitions plus options**, and almost everything returns
  an error: `s, err := gocron.NewScheduler()`, then
  `j, err := s.NewJob(gocron.DurationJob(1*time.Second), gocron.NewTask(taskFunc))`
  `[GC migration_v1_to_v2.md:58-83]`.
- v1 lifecycle is `StartAsync()` / `StartBlocking()` / `Stop()`
  `[GC-V1 scheduler.go:83,97]`; v2 is `Start()` / `StopJobs()` / `Shutdown()`
  `[GC migration_v1_to_v2.md:120-133]`.
- v2 gives every job a `uuid.UUID` identity `[GC job.go:1639-1640]`; v1 exposed
  `JobsMap() map[uuid.UUID]*Job` `[GC-V1 scheduler.go:146]` but the primary
  handle was the `*Job` pointer.

**Docs/code disagreement, code wins.** The migration guide writes the v2 cron
example as `gocron.CronJob("*/5 * * * *")`
`[GC migration_v1_to_v2.md:96-101]`. The actual signature requires a second
argument: `func CronJob(crontab string, withSeconds bool) JobDefinition`
`[GC job.go:324]`. The guide does not compile. Report upstream if you care;
follow the code.

**Recommendation for herdr-cron: v2.** v1 is explicitly closed to PRs, v2 is
where all the introspection surface a TUI needs lives (`Job.NextRuns(n)`,
`Job.Schedule()`, `SchedulerMonitor`), and v2's error-returning constructors let
the CLI reject a bad crontab at `herdr-cron add` time instead of at run time.

**Version-pinning caveat.** The v2 branch tip carries API that is **not in any
release**. `WithStartAtGrace` exists at the pinned branch SHA
`[GC job.go:913]` but is absent from `v2.22.0`, `v2.21.2`, and `v2.20.0` (checked
with `curl -sf https://raw.githubusercontent.com/go-co-op/gocron/<tag>/job.go |
grep -c "func WithStartAtGrace"` → `0` for each). If herdr-cron wants the grace
window described in §8, it must either wait for a release after v2.22.0 or pin a
pseudo-version on the `v2` branch.

---

## 2. Scheduler lifecycle

The public surface, verbatim:

```go
type Scheduler interface {
	Jobs() []Job
	NewJob(JobDefinition, Task, ...JobOption) (Job, error)
	RemoveByTags(...string)
	RemoveJob(uuid.UUID) error
	Shutdown() error
	ShutdownWithContext(context.Context) error
	Start()
	StopJobs() error
	StopJobsWithContext(context.Context) error
	Update(uuid.UUID, JobDefinition, Task, ...JobOption) (Job, error)
	JobsWaitingInQueue() int
}
```

`[GC scheduler.go:53-105]`

### Architecture

`NewScheduler` spawns **one** goroutine that owns the `jobs map[uuid.UUID]internalJob`
and does nothing but `select` over ~11 channels `[GC scheduler.go:229-275]`. Every
public method is a channel round-trip into that goroutine; there is no mutex on
the job map, the goroutine *is* the lock. The executor is a second goroutine
started on `Start()` `[GC scheduler.go:805]` that receives job IDs and fans work
out to per-run goroutines. Timers are `clock.AfterFunc` callbacks that just push
an ID onto `exec.jobsIn` `[GC scheduler.go:583-595]`.

Consequence for a TUI: **all introspection is a synchronous RPC to a single
goroutine**, bounded by `defaultRequestJobTimeout = time.Second`
`[GC scheduler.go:44-47]`. A blocked scheduler goroutine turns every `NextRun()`
into `ErrSchedulerBusy`.

### `NewScheduler(options ...SchedulerOption) (Scheduler, error)` `[GC scheduler.go:182]`

Non-blocking. Allocates channels, sets `location: time.Local`,
`clock: clockwork.NewRealClock()`, `stopTimeout: time.Second * 10`
`[GC scheduler.go:185-218]`, applies options (any option error aborts
construction), then starts the scheduler goroutine `[GC scheduler.go:222-277]`.
Jobs added before `Start()` are held but not scheduled — `selectNewJob` only
arms timers when `s.started.Load()` is true `[GC scheduler.go:766-783]`.

### `Start()` `[GC scheduler.go:1111]`

Documented "non-blocking" `[GC scheduler.go:88-92]`, but it **does** block
briefly: it sends on `startCh` and waits on `<-s.startedCh`
`[GC scheduler.go:1121-1122]`, which the scheduler goroutine only sends after
walking every registered job and arming its first timer
`[GC scheduler.go:803-821]`. So `Start()` returns once scheduling is live, not
before. Calling it twice logs a warning and returns `[GC scheduler.go:1112-1115]`.
`Job.NextRun()` returns the zero time before `Start()`, confirmed:
`NextRun BEFORE Start: 0001-01-01 00:00:00 +0000 UTC (zero=true) err=<nil>` `[RUN 3]`.

### `StopJobs() error` / `StopJobsWithContext(ctx) error` `[GC scheduler.go:1129,1140]`

`StopJobs` wraps `StopJobsWithContext` with a deadline of
`s.exec.stopTimeout + 2*time.Second` and maps `context.DeadlineExceeded` to
`ErrStopSchedulerTimedOut` `[GC scheduler.go:1129-1138]`. **Blocking**: sends on
`stopCh`, waits for `stopErrCh`. The stop path `[GC scheduler.go:292-339]`
(1) signals the executor to stop; (2) calls `j.stop()` on every job, which stops
the timer **and cancels the job context** `[GC job.go:102-107]`; (3) waits for
every job context to be `Done()`; (4) waits up to `stopTimeout + 1s` for
`exec.done`, else `ErrStopExecutorTimedOut`; (5) **rebuilds a fresh
`ctx`/`cancel` for every job** so the scheduler is restartable
`[GC scheduler.go:318-331]`; (6) sets `started = false`.

`Start()` after `StopJobs()` works and does **not** replay missed ticks: a 100 ms
`DurationJob` stopped for a full second (ten missed ticks) produced exactly one
run in the 120 ms after restart — `F runs before StopJobs=3, runs 120ms after
restart=4` `[RUN 4]`.

### `Shutdown() error` / `ShutdownWithContext(ctx) error` `[GC scheduler.go:1157,1168]`

`Shutdown` = `ShutdownWithContext` with the same `stopTimeout + 2s` deadline
`[GC scheduler.go:1157-1166]`. It calls `s.shutdownCancel()` then waits on
`stopErrCh` `[GC scheduler.go:1171-1183]`; if the scheduler was never started it
returns `nil` immediately. **Terminal**: "the Scheduler cannot be restarted after
calling Shutdown" `[GC scheduler.go:80-84]`.

In-flight jobs, ordered precisely:

- Job contexts are cancelled *first* `[GC scheduler.go:299-301]`, so a task
  taking `context.Context` as its first parameter observes cancellation
  immediately. Verified: a task selecting on `ctx.Done()` reported
  `ctx cancelled: context canceled` and `Shutdown` returned in `0s` `[RUN 4]`.
- A task that **ignores** its context is *waited on*, up to `stopTimeout`:
  default 10 s timeout with an uncooperative 3 s task →
  `Shutdown blocked 3s err=<nil>` `[RUN 5]`.
- If the timeout wins, `Shutdown` returns `ErrStopJobsTimedOut` and the goroutine
  is abandoned: `WithStopTimeout(500ms)` with a 5 s task →
  `Shutdown blocked 500ms err=gocron: timed out waiting for jobs to finish`
  `[RUN 5]`. The waiter goroutine is documented as potentially leaking — "This
  particular goroutine could leak in the event that some long-running standard
  job doesn't complete" `[GC executor.go:629-630]`.

**Post-shutdown behaviour is a set of quiet traps** `[RUN 4]`:

```
G Jobs() after Shutdown: true 0
G NextRun after Shutdown err: gocron: scheduler did not respond in time
G RunNow after Shutdown err: gocron: Job: RunNow: scheduler unreachable
```

- `Jobs()` returns `nil`, which the interface doc admits is "indistinguishable
  from a scheduler with zero jobs" `[GC scheduler.go:61-66]`.
- `NextRun()` returns `ErrSchedulerBusy` — *not* `ErrJobNotFound`.
- `Start()` after `Shutdown()` silently no-ops (`runs before Shutdown=2, runs
  after re-Start=2`) `[RUN 5]`.
- Worst: **`NewJob` after `Shutdown` returns a non-nil `Job` and a `nil` error**
  while scheduling nothing `[RUN 5]`. The code takes the
  `<-s.shutdownCtx.Done()` branch, cancels, falls through, and returns
  `&out, nil` `[GC scheduler.go:1067-1088]`. herdr-cron must track scheduler
  liveness itself; do not trust a `nil` error from `NewJob`.

### `Update(id, definition, task, options...) (Job, error)` `[GC scheduler.go:1186]`

Literally `addOrUpdateJob(id, ...)` `[GC scheduler.go:1186-1188]`, which
**removes the old job and installs a brand new one under the same UUID**: looks
the job up, sends `removeJobCh`, waits for the old context to be `Done()`, then
builds a fresh `internalJob` `[GC scheduler.go:977-993]`. Blocking — it waits for
the scheduler goroutine to acknowledge the new job via `newJobCancel`
`[GC scheduler.go:1078-1082]`.

Consequence: **`Update` discards all in-memory run history for that job**
`[RUN 5]`:

```
K LastRun before Update: true
K Update err=<nil> sameID=true name="updated" jobsLen=1
K LastRun after Update is zero (history lost): true
```

Decisive for herdr-cron: run history must live in herdr-cron's own store, not be
read back out of gocron (see §7).

### `RemoveJob(id) error` / `RemoveByTags(tags...)` `[GC scheduler.go:1098,1091]`

`RemoveJob` looks the job up first and returns `ErrJobNotFound` for an unknown id
(verified: `L RemoveJob(random): gocron: job not found` `[RUN 5]`), then fires
`removeJobCh` and returns **without waiting** for the removal to land
`[GC scheduler.go:1098-1109]`. Removal stops the timer and cancels the job
context `[GC scheduler.go:395-406]`; an already-running task is *not* waited for.
`RemoveByTags` is fully fire-and-forget — no error, no result count
`[GC scheduler.go:1091-1096]` — and removes every job whose tag set intersects the
argument `[GC scheduler.go:787-801]`.

### `Jobs() []Job` `[GC scheduler.go:886]` and `JobsWaitingInQueue() int` `[GC scheduler.go:1190]`

`Jobs()` is a blocking round-trip returning a fresh `[]Job` snapshot **sorted by
raw UUID bytes** — "a deterministic-but-effectively-random ordering"
`[GC scheduler.go:56-59]`, implemented as `bytes.Compare(aID[:], bID[:])`
`[GC scheduler.go:352-355]`. A TUI must re-sort by whatever the user expects
(name, next-run) itself. Each returned `Job` is a small value carrying id, name,
tags, the schedule descriptor, and the two request channels
`[GC scheduler.go:833-842]` `[GC job.go:1688-1695]`.

`JobsWaitingInQueue()` reads `len(s.exec.limitMode.in)` **without going through
the scheduler goroutine**, and returns `0` unless
`WithLimitConcurrentJobs(_, LimitModeWait)` is set `[GC scheduler.go:1190-1195]`.

---

## 3. Job definitions

```go
type JobDefinition interface {
	setup(j *internalJob, l *time.Location, now time.Time) error
}
```
`[GC job.go:211-213]`

The interface method is unexported, so **herdr-cron cannot implement a custom
`JobDefinition`**. The seven constructors are the whole vocabulary.

### `CronJob(crontab string, withSeconds bool) JobDefinition` `[GC job.go:324-329]`

Doc comment: "defines a new job using the crontab syntax: `* * * * *`. An optional
6th field can be used at the beginning if withSeconds is set to true:
`* * * * * *`" `[GC job.go:318-323]`.

**Parser library: `github.com/robfig/cron/v3 v3.0.1`** `[GC go.mod:8]`, used
directly:

```go
	if c.withSeconds {
		p := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		cronSchedule, err = p.Parse(withLocation)
	} else {
		cronSchedule, err = cron.ParseStandard(withLocation)
	}
```
`[GC job.go:269-274]`

Seconds precision therefore exists **only** for `CronJob(spec, true)`, and because
the flag is `SecondOptional` (not `Second`), a five-field expression still parses
under `withSeconds=true`. **Descriptors work in both modes**, because `Descriptor`
is in both configurations and `ParseStandard` uses
`standardParser = NewParser(Minute | Hour | Dom | Month | Dow | Descriptor)`
`[RF parser.go:217-219]`; the supported set is `@yearly`/`@annually`, `@monthly`,
`@weekly`, `@daily`/`@midnight`, `@hourly`, `@every <duration>`
`[RF parser.go:~380-434]`. `@reboot` is **not** supported. Measured `[RUN 1]`:

```
"@daily"          seconds=false OK   next=2026-09-03T09:00:00+09:00
"@hourly"         seconds=false OK   next=2026-09-02T11:00:00+09:00
"@every 1h30m"    seconds=false OK   next=2026-09-02T11:50:07+09:00
"*/5 * * * * *"   seconds=true  OK   next=2026-09-02T10:20:10+09:00
"* * * * *"       seconds=true  OK   next=2026-09-02T10:21:00+09:00
"* * * * * *"     seconds=false ERR  gocron: CronJob: crontab parse failure
                                     expected exactly 5 fields, found 6
"@reboot"         seconds=false ERR  gocron: CronJob: crontab parse failure
                                     unrecognized descriptor: @reboot
```

`@every` is not a cron schedule at all — it produces `ConstantDelaySchedule`
`[RF parser.go:425-431]` `[RF constantdelay.go:11-14]`, a duration relative to the
reference time, which is why `@every 1h30m` resolved to a non-round `11:50:07`.

**Timezone precedence** in `defaultCron.IsValid`:

```go
	if strings.HasPrefix(crontab, "TZ=") || strings.HasPrefix(crontab, "CRON_TZ=") {
		withLocation = crontab
	} else {
		// since the user didn't provide a timezone default to the location
		// passed in by the scheduler. Default: time.Local
		withLocation = fmt.Sprintf("CRON_TZ=%s %s", location.String(), crontab)
	}
```
`[GC job.go:255-262]`

So an inline `TZ=`/`CRON_TZ=` prefix beats `WithLocation` `[GC job.go:241-248]`.
Warning in the same doc block: `location.String()` is interpolated verbatim, so a
`*time.Location` whose name robfig cannot `time.LoadLocation` will fail to parse
`[GC job.go:245-248]` `[RF parser.go:95-103]` — prefer explicit IANA zones. Two
failure modes, distinguishable by `errors.Is`: `ErrCronJobParse` (wrapping the
parser error via `errors.Join`) for syntax, and `ErrCronJobInvalid` when the
expression parses but yields no future run `[GC job.go:275-280]`
`[GC errors.go:9-10]`.

**`NewDefaultCron` is the CLI validator you want.**
`func NewDefaultCron(cronStatementsIncludeSeconds bool) Cron` `[GC job.go:226-230]`
is documented "for use outside the scheduling of a job. For example, validating
crontab syntax before passing to the NewJob function" `[GC job.go:223-225]`, and
returns

```go
type Cron interface {
	IsValid(crontab string, location *time.Location, now time.Time) error
	Next(lastRun time.Time) time.Time
}
```
`[GC job.go:198-201]`

giving herdr-cron **validation and next-run computation with no running
scheduler** — see §10 for how it compares to robfig or gronx directly.

### `DurationJob` and `DurationRandomJob`

`func DurationJob(duration time.Duration) JobDefinition` `[GC job.go:350-354]`
rejects `0` (`ErrDurationJobIntervalZero`) and negatives
(`ErrDurationJobIntervalNegative`) `[GC job.go:337-345]`; `next` is
`lastRun.Add(j.duration)` `[GC job.go:1356-1358]`.

`func DurationRandomJob(minDuration, maxDuration time.Duration) JobDefinition`
`[GC job.go:388-393]` requires `min < max` (`ErrDurationRandomJobMinMax`) and both
`> 0` (`ErrDurationRandomJobPositive`) `[GC job.go:362-369]`; `next` draws
`rand.Int64N(int64(max-min))` from `math/rand/v2`, documented concurrency-safe
because the top-level functions use a per-goroutine generator
`[GC job.go:1366-1373]`. TUI consequence: **`NextRuns(n)` for a random-duration
job is not reproducible** — each call re-rolls the dice past the already-scheduled
entries.

### `DailyJob`, `WeeklyJob`, `MonthlyJob`

```go
func DailyJob(interval uint, atTimes AtTimes) JobDefinition
func WeeklyJob(interval uint, daysOfTheWeek Weekdays, atTimes AtTimes) JobDefinition
func MonthlyJob(interval uint, daysOfTheMonth DaysOfTheMonth, atTimes AtTimes) JobDefinition

func NewAtTime(hours, minutes, seconds uint) AtTime
func NewAtTimes(atTime AtTime, atTimes ...AtTime) AtTimes
func NewWeekdays(weekday time.Weekday, weekdays ...time.Weekday) Weekdays
func NewDaysOfTheMonth(day int, moreDays ...int) DaysOfTheMonth
```
`[GC job.go:401,501,634,601,612,487,574]`

All three reject `interval == 0` (`ErrDailyJobZeroInterval` /
`ErrWeeklyJobZeroInterval` / `ErrMonthlyJobZeroInterval`) and validate at-times:
nil list, nil element, hours outside 0–23, minutes/seconds outside 0–59 map to the
per-type `Err*AtTimesNil` / `Err*AtTimeNil` / `Err*Hours` / `Err*MinutesSeconds`
errors `[GC job.go:415-431,449-480,517-555]` `[GC errors.go:11-44]`. Because
at-times carry seconds, **daily/weekly/monthly jobs have seconds precision
natively**, independent of the cron `withSeconds` flag. Weekly requires non-nil
days (`ErrWeeklyJobDaysOfTheWeekNil`) and sorts them internally
`[GC job.go:456-463]`.

`MonthlyJob` days are `1..31` **or** `-31..-1` counting back from month end
(`-1` = last day); `0` or `|day| > 31` → `ErrMonthlyJobDays`
`[GC job.go:529-538]`, and positive/negative days are kept in separate deduped
slices `[GC job.go:533-543]`.

Two documented drift traps worth surfacing in the CLI: "if you select an interval
greater than 1, your job by default will run X (interval) days from now if there
are no atTimes left in the current day" `[GC job.go:396-401]`; and for monthly,
"If a day of the month is selected that does not exist in all months (e.g. 31st)
any month that does not have that day will be skipped" `[GC job.go:622-623]` —
worked out as "an interval of 2 months on the 31st of each month, starting 12/31
would skip Feb, April, June, and next run would be in August"
`[GC job.go:631-633]`.

### `OneTimeJob(startAt OneTimeJobStartAtOption) JobDefinition` `[GC job.go:715-719]`

```go
func OneTimeJobStartImmediately() OneTimeJobStartAtOption
func OneTimeJobStartDateTime(start time.Time) OneTimeJobStartAtOption
func OneTimeJobStartDateTimes(times ...time.Time) OneTimeJobStartAtOption
```
`[GC job.go:690,699,707]`

Despite the name, `OneTimeJobStartDateTimes` gives a **multi-shot** job: the times
are sorted, deduplicated, and filtered to those strictly in the future; if none
remain and `startImmediately` is false, `setup` returns
`ErrOneTimeJobStartDateTimePast` `[GC job.go:648-673]`. Verified:
`OneTimeJob(past) err: gocron: OneTimeJob: start must not be in the past`
`[RUN 8]`. `next` binary-searches the sorted list and returns the zero time once
exhausted `[GC job.go:1605-1628]`; the scheduler treats a zero `next` as "do not
reschedule" `[GC scheduler.go:538-543]`.

### Task binding

```go
type Task func() task
func NewTask(function any, parameters ...any) Task
```
`[GC job.go:147,154-161]`

Parameter arity and kinds are checked reflectively at `NewJob` time
(`ErrNewJobWrongNumberOfParameters`, `ErrNewJobWrongTypeOfParameters`), including
variadic and interface-element cases `[GC scheduler.go:906-973]`. If the function's
first parameter is `context.Context`, gocron injects the job context
automatically `[GC scheduler.go:1044-1055]`. Because dispatch is
`reflect.Value.Call` `[GC util.go:28]`, herdr-cron gets no compile-time safety on
task signatures — prefer a single `func(context.Context) error` shape and close
over your own payload.

---

## 4. Job options

`type JobOption func(*internalJob, time.Time) error` `[GC job.go:728]`. Options
are applied in order: **global options from `WithGlobalJobOptions` first, then
per-job options, which therefore win** `[GC scheduler.go:1025-1037]`
`[GC scheduler.go:1248-1259]`.

| Option | Signature | Guarantee |
| --- | --- | --- |
| `WithName` | `func WithName(name string) JobOption` `[GC job.go:783]` | Sets `j.name`; rejects `""` with `ErrWithNameEmpty`. Without it, the name defaults to `runtime.FuncForPC(...).Name()`, e.g. `main.main.func4` `[GC scheduler.go:1009]` — observed in `[RUN 5]`. **The name is also the distributed lock key** `[GC distributed.go:19-20]`. |
| `WithTags` | `func WithTags(tags ...string) JobOption` `[GC job.go:1001]` | Sets `j.tags` verbatim, no validation, no dedup. Only consumer is `RemoveByTags` `[GC scheduler.go:787-801]`; `Job.Tags()` returns a clone made at snapshot time `[GC scheduler.go:837]`. |
| `WithSingletonMode` | `func WithSingletonMode(mode LimitMode) JobOption` `[GC job.go:821]` | Sets `singletonMode`/`singletonLimitMode`. At dispatch, the executor spins one dedicated `singletonModeRunner` goroutine **per job** with a private queue of `defaultSingletonQueueBuffer = 1000` `[GC executor.go:213-222]` `[GC scheduler.go:34-36]`, serialising that job's runs while others run freely `[GC executor.go:380-388]`. |
| `LimitMode` | `LimitModeReschedule = iota + 1`, `LimitModeWait` `[GC scheduler.go:1265-1297]` | `Reschedule` **drops** the overlapping tick (a `cap == 1` limiter channel; on `default` the run is skipped and only rescheduled) `[GC executor.go:227-246]`. `Wait` queues it in the buffered channel. Doc warning on `Wait`: "a job that executes frequently may pile up in the wait queue and be executed many times back to back when the queue opens" `[GC scheduler.go:1276-1279]`. |
| `WithLimitConcurrentJobs` | `func WithLimitConcurrentJobs(limit uint, mode LimitMode) SchedulerOption` `[GC scheduler.go:1310]` | Scheduler-wide cap. Spawns `limit` shared `limitModeRunner` goroutines on first dispatch `[GC executor.go:146-154]`; queue buffer `defaultLimitModeQueueBuffer = 1000` `[GC scheduler.go:30-33]`. `limit == 0` → `ErrWithLimitConcurrentJobsZero`. Interaction warning: with scheduler `(1, LimitModeWait)` plus job `WithSingletonMode(LimitModeReschedule)`, "a single time consuming job can dominate your limit" `[GC scheduler.go:1307-1309]`. |
| `WithStartAt` | `func WithStartAt(option StartAtOption) JobOption` `[GC job.go:870]` | Wraps one of `WithStartImmediately()` `[GC job.go:929]`, `WithStartDateTime(t)` `[GC job.go:938]`, `WithStartDateTimePast(t)` `[GC job.go:958]`. `WithStartDateTime` **rejects the past**: `WithStartDateTime(past) err: gocron: WithStartDateTime: start must not be in the past` `[RUN 8]`; use `WithStartDateTimePast` to backdate `[GC job.go:951-957]`. `WithStartImmediately` fires once at `Start()` and then follows the schedule `[GC scheduler.go:722-726]`. |
| `WithStopAt` | `func WithStopAt(option StopAtOption) JobOption` `[GC job.go:973]` with `WithStopDateTime(end)` `[GC job.go:985]` | Must be in the future and after any start time (`ErrWithStopDateTimePast`, `ErrStopTimeEarlierThanStartTime`). "The job's final run may be at the stop time, but not after" `[GC job.go:982-984]`. Enforced in three places: pre-run `stopTimeReached` `[GC executor.go:439-441]`, on reschedule `[GC scheduler.go:504-507,571-574]`, and by `NextRuns` truncation `[GC job.go:1793-1795]`. Reaching it **removes the job from the scheduler**. |
| `WithEventListeners` | `func WithEventListeners(eventListeners ...EventListener) JobOption` `[GC job.go:755]` | Applies each listener constructor; a nil listener func returns `ErrEventListenerFuncNil`. See §6. |
| `WithContext` | `func WithContext(ctx context.Context) JobOption` `[GC job.go:1028]` | Sets `j.parentCtx`; nil → `ErrWithContextNil`. The job's own ctx is derived from it `[GC scheduler.go:1039-1042]`, so "If you cancel the context the job will no longer be scheduled as well" `[GC job.go:1025-1027]` — `runJob` returns early on `j.ctx.Done()` `[GC executor.go:431-437]`. |
| `WithIdentifier` | `func WithIdentifier(id uuid.UUID) JobOption` `[GC job.go:1011]` | Overwrites the generated `j.id`; `uuid.Nil` → `ErrWithIdentifierNil`. **This is how herdr-cron pins its own persisted job ID onto a gocron job** so that a restart reproduces stable IDs. Nothing checks for collisions — a duplicate id will silently overwrite the map entry in `selectNewJob` `[GC scheduler.go:781]`. |
| `WithLimitedRuns` | `func WithLimitedRuns(limit uint) JobOption` `[GC job.go:768]` | N runs then the job is removed. `0` → `ErrWithLimitedRunsZero`. Removal is deferred until the task function actually finishes `[GC scheduler.go:632-644,666-678]`; skipped runs do not consume a slot `[GC scheduler.go:624-630]`. |
| `WithIntervalFromCompletion` | `func WithIntervalFromCompletion() JobOption` `[GC job.go:861-866]` | Next run measured from completion, not from scheduled start, "Ignored" for time-based jobs `[GC job.go:844-846]`. Implemented by moving the reschedule send to *after* the task returns `[GC executor.go:562-569]` and scheduling from `j.lastRun` `[GC scheduler.go:511-522]`. |
| `WithDaylightSavingsTimePolicy` | `func WithDaylightSavingsTimePolicy(policy DaylightSavingsTimePolicy) JobOption` `[GC job.go:811]` | See §8. |
| `WithStartAtGrace` | `func WithStartAtGrace(grace time.Duration) JobOption` `[GC job.go:913]` | **Branch tip only, not in v2.22.0.** See §8. |
| `WithCronImplementation` | `func WithCronImplementation(c Cron) JobOption` `[GC job.go:795]` | Swap the cron engine per job. Only used by `CronJob` `[GC job.go:297-308]`. Concurrency contract spelled out at `[GC job.go:191-197]`. |

Scheduler-level options not covered above: `WithClock` `[GC scheduler.go:1210]`,
`WithLocation` `[GC scheduler.go:1331]`, `WithLogger` `[GC scheduler.go:1342]`,
`WithStopTimeout` (default `10s`) `[GC scheduler.go:1357]`, `WithMonitor` /
`WithMonitorStatus` / `WithSchedulerMonitor` `[GC scheduler.go:1369,1380,1390]`,
`WithGlobalJobOptions` `[GC scheduler.go:1254]`, `WithDistributedElector` /
`WithDistributedLocker` `[GC scheduler.go:1224,1238]`.

---

## 5. Job introspection: what a TUI can show, and how stale it is

```go
type Job interface {
	ID() uuid.UUID
	IsRunning() (bool, error)
	LastRun() (time.Time, error)              // Deprecated: use LastRunStartedAt instead.
	LastRunCompletedAt() (time.Time, error)
	LastRunStartedAt() (time.Time, error)
	Name() string
	NextRun() (time.Time, error)
	NextRuns(int) ([]time.Time, error)
	RunNow() error
	Schedule() JobSchedule
	Tags() []string
}
```
`[GC job.go:1638-1680]`

**Free / no round-trip:** `ID()`, `Name()`, `Tags()`, `Schedule()` are plain struct
field reads on the snapshot value
`[GC job.go:1697-1699,1748-1750,1802-1808]` — as fresh as the moment you called
`Jobs()`.

**Costs a scheduler round-trip:** `IsRunning`, `LastRun*`, `NextRun`, `NextRuns`
all go through `requestJob(j.id, j.jobOutRequest)` `[GC job.go:1701-1800]`
`[GC util.go:49-57]`. Failure semantics are documented precisely: no reply within
`defaultRequestJobTimeout = time.Second` → `ErrSchedulerBusy` ("Callers should
surface this to users rather than reporting ErrJobNotFound, so users can
distinguish 'gone' from 'temporarily unreachable'"); unknown id →
`ErrJobNotFound` `[GC util.go:38-48]` `[GC scheduler.go:686-694]`. A TUI polling
every job every frame does one channel round-trip per field per job; read `Jobs()`
once, then one accessor pass, or cache.

**`NextRun()`** returns `nextScheduled[0]` — first element of a slice maintained in
ascending order — or the zero time if empty `[GC job.go:1752-1766]`
`[GC job.go:55-63]`. Only populated after `Start()` `[GC job.go:1660-1663]`,
confirmed in `[RUN 3]`.

**`NextRuns(count)`** returns already-scheduled entries when it can, then
**extrapolates** the rest by calling `ij.next(previous)` in a loop, truncating at
`stopTime`:

```go
	} else if count <= lengthNextScheduled {
		return ij.nextScheduled[:count], nil
	}
	out := make([]time.Time, count)
	for i := range count {
		if i < lengthNextScheduled {
			out[i] = ij.nextScheduled[i]
			continue
		}
		from := out[i-1]
		next := ij.next(from)
		if !ij.stopTime.IsZero() && !next.Before(ij.stopTime) {
			return out[:i], nil
		}
		out[i] = next
	}
```
`[GC job.go:1780-1799]`

Two things follow. The `count <= len` branch returns a **subslice aliasing the
scheduler's backing array**, safe only because `pruneStaleScheduled` deliberately
allocates a fresh array rather than reusing `[:0]` `[GC job.go:116-136]` — so do
not append to what `NextRuns` hands you. And the extrapolation for
`DurationRandomJob` is non-reproducible (§3).

**`LastRun()` vs `LastRunStartedAt()` vs `LastRunCompletedAt()` — read carefully.**
The interface doc claims `LastRunStartedAt` returns the start and
`LastRunCompletedAt` the completion `[GC job.go:1654-1657]`. The implementation
of `LastRunStartedAt` returns `ij.lastRun`, **not** `ij.lastRunStartedAt`:

```go
func (j job) LastRunStartedAt() (time.Time, error) {
	...
	return ij.lastRun, nil
}
```
`[GC job.go:1737-1746]`

which is byte-identical to `LastRun()` `[GC job.go:1715-1724]`. This is also true
at the released tag `[GC-2220 job.go:1673-1682]`. Docs and code disagree; code
wins. Measured on a 2 s job whose task sleeps 900 ms `[RUN 10]`:

```
t+300ms  IsRunning=true  LastRun=+0s   LastRunStartedAt=+0s   LastRunCompletedAt=zero    NextRun=+2s
t+1.2s   IsRunning=false LastRun=+0s   LastRunStartedAt=+0s   LastRunCompletedAt=+900ms  NextRun=+2s
```

Worse, `lastRun` is not even the task's start time in the strict sense: it is set
in `selectExecJobsOutCompleted` `[GC scheduler.go:646]`, which the executor
triggers *before* invoking the task function (`jobsOutCompleted` is sent at
`[GC executor.go:513-517]`, the function call happens at
`[GC executor.go:524-528]`). So `LastRun` is the **dispatch** timestamp. The
genuine start timestamp lives in `lastRunStartedAt`, fed by `jobTimingUpdateCh`
`[GC executor.go:519-523]` `[GC scheduler.go:658-660]`, and is **not reachable
through the public API** — only `IsRunning` consumes it
(`lastRunStartedAt.After(lastRunCompletedAt)` `[GC job.go:1709-1712]`).

Implication for herdr-cron: display `LastRunCompletedAt` for "finished at", and
record your own start timestamp in a `BeforeJobRuns` listener if you want a
truthful "started at".

**`RunNow() error`**

```go
func (j job) RunNow() error
```
`[GC job.go:1810-1833]`

Documented: "runs the job once, now. This does not alter the existing run
schedule, and will respect all job and scheduler limits"
`[GC job.go:1668-1672]`. Implementation sends `runJobRequest` with a
`defaultRunNowSendTimeout = 100ms` send deadline and a
`defaultRunNowResultTimeout = 1s` result deadline `[GC scheduler.go:37-43]`;
either miss yields `ErrJobRunNowFailed`. Crucially the manual dispatch sets
`shouldSendOut: false` `[GC scheduler.go:382-385]`, so a `RunNow` fire does **not**
trigger a reschedule — the regular schedule is untouched. It also returns
`ErrJobNotFound` for an unknown id `[GC scheduler.go:367-375]`. Note the error is
about *dispatch*, not outcome: `RunNow` returns `nil` as soon as the executor
accepts the work, long before the task finishes. Verified `G RunNow err: <nil>`
`[RUN 4]`.

**`Schedule() JobSchedule`** returns a type-assertable descriptor
`[GC job.go:1147-1154]`, one of `CronJobSchedule{Crontab string}`,
`DurationJobSchedule{Duration}`, `DurationRandomJobSchedule{Min, Max}`,
`DailyJobSchedule{Interval, AtTimes}`, `WeeklyJobSchedule{Interval, DaysOfWeek,
AtTimes}`, `MonthlyJobSchedule{Interval, Days, DaysFromEnd, AtTimes}`,
`OneTimeJobSchedule{StartAt}` `[GC job.go:1156-1261]`, built by
`jobScheduleFromInternal` with `slices.Clone` on every slice
`[GC scheduler.go:844-884]`. Each has a `JobType()` returning one of the
`JobType` constants `[GC job.go:1128-1145]`. This is enough for a TUI to render
"every 5m" vs "0 9 * * MON-FRI" without keeping a parallel model — though
herdr-cron will keep one anyway for persistence (§7).

---

## 6. Event listeners and monitoring — how herdr-cron records run history

```go
type EventListener func(*internalJob) error

func BeforeJobRuns(eventListenerFunc func(jobID uuid.UUID, jobName string)) EventListener
func BeforeJobRunsSkipIfBeforeFuncErrors(eventListenerFunc func(jobID uuid.UUID, jobName string) error) EventListener
func AfterJobRuns(eventListenerFunc func(jobID uuid.UUID, jobName string)) EventListener
func AfterJobRunsWithError(eventListenerFunc func(jobID uuid.UUID, jobName string, err error)) EventListener
func AfterJobRunsWithPanic(eventListenerFunc func(jobID uuid.UUID, jobName string, recoverData any)) EventListener
func AfterLockError(eventListenerFunc func(jobID uuid.UUID, jobName string, err error)) EventListener
```
`[GC job.go:1046,1050,1063,1075,1087,1099,1111]`

Each is a setter for a single function field on `internalJob`
`[GC job.go:86-91]` — **one listener per event per job**; registering
`AfterJobRuns` twice keeps the last. A nil func returns `ErrEventListenerFuncNil`.

### Are they synchronous? Yes. On which goroutine?

All of them are invoked from `executor.runJob` via `callJobFuncWithParams`, which
is a plain reflective call with no goroutine and no recover
`[GC util.go:13-36]`. The exact order inside `runJob`:

1. elector / locker check; `afterLockError` on lock failure `[GC executor.go:443-474]`
2. `beforeJobRuns` `[GC executor.go:476]`
3. `SchedulerMonitor.JobStarted` + `JobSchedulingDelay` `[GC executor.go:478-488]`
4. `beforeJobRunsSkipIfBeforeFuncErrors`; on error → reschedule, send
   `jobOutCompleted{skipped: true}`, `JobFailed`, **return** `[GC executor.go:490-502]`
5. `SchedulerMonitor.JobRunning` `[GC executor.go:504-507]`
6. **reschedule + `jobsOutCompleted`** (unless `intervalFromCompletion`) `[GC executor.go:509-517]`
7. `jobTimingUpdateCh{startedAt}` `[GC executor.go:519-523]`
8. the task function `[GC executor.go:524-528]`
9. `Monitor.RecordJobTiming` `[GC executor.go:529]`
10. `afterJobRunsWithError(err)` **or** `afterJobRuns()`, then
    `IncrementJob`, `RecordJobTimingWithStatus`, `jobTimingUpdateCh{completedAt}`,
    and the `SchedulerMonitor` completion notifications `[GC executor.go:530-560]`

The goroutine depends on the job's mode. A **standard** job gets a fresh
goroutine per run: `go func(j internalJob) { e.runJob(j, jIn); ... }`
`[GC executor.go:266-270]`. A **singleton** job runs on its dedicated
`singletonModeRunner` goroutine `[GC executor.go:412]`; a **limit-mode** job runs
on one of the shared `limitModeRunner` goroutines `[GC executor.go:359]`.

### Can a listener block the scheduler?

**Not the scheduler goroutine — but yes, it can block scheduling.** Two hazards,
both measured:

*`AfterJobRuns` is harmless to cadence*, because rescheduling already happened at
step 6. A 500 ms `AfterJobRuns` on a 200 ms `DurationJob` left the interval
untouched — `D task start deltas with 500ms AfterJobRuns on a 200ms job: 200ms
200ms 200ms 200ms (n= 5 )` `[RUN 4]`.

*`BeforeJobRuns` delays the job's own next tick*, because it sits at step 2,
upstream of the reschedule at step 6. A 500 ms `BeforeJobRuns` on a 200 ms job
`[RUN 3]`:

```
   before@10:21:26.364      before@10:21:26.964
   beforeDone@10:21:26.865  beforeDone@10:21:27.465
   task@10:21:26.865        task@10:21:27.465
   after@10:21:26.865       after@10:21:27.465
```

The second tick started 600 ms after the first, not 200 ms: the reschedule ran at
`26.865`, computed `26.564` (already past), and `advancePastNow` walked forward in
200 ms steps to `26.964` `[GC scheduler.go:413-422,545-557]`. Three ticks were
silently swallowed.

Additionally, `jobsOutForRescheduling` and `jobsOutCompleted` are **unbuffered**
channels received by the scheduler goroutine `[GC scheduler.go:192-199]`, so a run
goroutine blocks there whenever the scheduler goroutine is busy — and for
singleton/limit-mode jobs that back-pressure lands on the shared runner goroutine,
stalling that whole queue. Keep listeners non-blocking: enqueue to a buffered
channel and let a herdr-cron writer goroutine own the I/O.

### Panics kill the process unless you opt in

`callJobFuncWithParams` has no `recover`. The recover wrapper is applied **only**
when `AfterJobRunsWithPanic` is registered:

```go
	if j.afterJobRunsWithPanic != nil {
		err = e.callJobWithRecover(j)
	} else {
		err = callJobFuncWithParams(j.function, j.parameters...)
	}
```
`[GC executor.go:524-528]` `[GC executor.go:572-583]`

Verified with two child processes, one with the listener and one without: without
it the child died (`exit=exit status 2`, `panic: boom`); with it the child logged
`panic listener saw: boom` three times and exited `0` with
`CHILD SURVIVED (with AfterJobRunsWithPanic)` `[RUN 8]`.

**herdr-cron must register `AfterJobRunsWithPanic` on every job** (easiest via
`WithGlobalJobOptions`) or a single bad task takes the daemon down. With the
wrapper the panic is also converted into a returned error
`fmt.Errorf("%w from %v", ErrPanicRecovered, recoverData)`
`[GC executor.go:573-579]`, so `afterJobRunsWithError` fires too — a panic
produces **both** the panic listener and the error listener.

### `Monitor` / `MonitorStatus`

```go
type JobStatus string
const (
	Fail                 JobStatus = "fail"
	Success              JobStatus = "success"
	Skip                 JobStatus = "skip"
	SingletonRescheduled JobStatus = "singleton_rescheduled"
)

type Monitor interface {
	IncrementJob(id uuid.UUID, name string, tags []string, status JobStatus)
	RecordJobTiming(startTime, endTime time.Time, id uuid.UUID, name string, tags []string)
}

type MonitorStatus interface {
	Monitor
	RecordJobTimingWithStatus(startTime, endTime time.Time, id uuid.UUID, name string, tags []string, status JobStatus, err error)
}
```
`[GC monitor.go:9-36]`

`MonitorStatus` is the better hook for herdr-cron's history table: **one call per
run carrying id, name, tags, start, end, status, and error**
`[GC executor.go:591-595]`, invoked at step 10 for both success and failure
`[GC executor.go:534,549]`. `Monitor.IncrementJob` additionally fires for
`Skip` (elector/locker refusal) `[GC executor.go:446,459,469]` and
`SingletonRescheduled` (a dropped overlapping tick) `[GC executor.go:240]`, which
are exactly the two "why didn't my job run" cases a user will ask about. Both
are called synchronously on the run goroutine — same blocking caveats.

`SchedulerMonitor` `[GC scheduler_monitor.go:7-50]` is a wider, more granular
interface (`SchedulerStarted/Stopped/Shutdown`, `JobRegistered/Unregistered`,
`JobStarted/Running/Completed/Failed`, `JobExecutionTime`,
`JobSchedulingDelay`, `ConcurrencyLimitReached(limitType string, job Job)`).
It is also called synchronously via thin `notify*` wrappers
`[GC scheduler.go:1400-1482]` — some from the executor goroutine, some from the
scheduler's own methods (`notifySchedulerStarted` from `Start()`
`[GC scheduler.go:1125]`, `notifyJobRegistered` from `addOrUpdateJob`
`[GC scheduler.go:1085-1087]`). The README notes there are no open-source
implementations of `Monitor`, `MonitorStatus`, or `SchedulerMonitor` yet
`[GC README.md:174-227]`, so herdr-cron would be writing the first.

**Recommended wiring for herdr-cron history**: `MonitorStatus` for the run
record (it is the only hook with start, end, status, and error in one place), a
non-blocking `BeforeJobRuns` for the true start timestamp (§5), and
`AfterJobRunsWithPanic` globally for survival. Every implementation method must
be a channel send, never a disk write.

---

## 7. Persistence: gocron has none

I grepped for it explicitly:

```
cd /tmp/hc-research/gocron && grep -rni -E "persist|sqlite|database|storage|serializ|marshal" \
  --include="*.go" --include="*.md" . | grep -v _test.go
```

Every hit is a comment or a sponsor logo URL — `./executor.go:382` ("serializes"),
`./util.go:155` ("serializes"), and four `README.md` image URLs
`[GC README.md:248-259]`. There is **no storage layer, no serialization, no
snapshot API**. Corroborating structural evidence: `internalJob` holds
`function any`, `parameters []any`, live `context.Context` values, a
`clockwork.Timer`, and a `cancel func` `[GC job.go:43-95]` — a struct that cannot
be serialised even in principle. The whole state is one in-memory map owned by one
goroutine `[GC scheduler.go:120-121]`.

The public `JobSchedule` descriptors (§5) are the only serialisable projection,
and they are lossy: they carry the schedule shape but not the options
(`WithStartAt`, `WithStopAt`, tags beyond `Job.Tags()`, limits, listeners).

### What herdr-cron must own

1. **Job definitions.** The source of truth is herdr-cron's own store (a JSON/TOML
   file or SQLite). At daemon start, read every record and call `NewJob` with
   `WithIdentifier(storedID)` `[GC job.go:1011]` so IDs survive restarts, and
   `WithName`/`WithTags` reconstructed from the record. Because
   `Update` destroys in-scheduler history (§2), "edit a job" in herdr-cron must
   mean "write the store, then `Update`", never the reverse.
2. **Run history.** gocron keeps exactly three timestamps per job — `lastRun`,
   `lastRunStartedAt`, `lastRunCompletedAt` `[GC job.go:65-67]` — one run deep,
   with no exit status and no output. Everything a user would want from
   `herdr-cron log <job>` must be captured through `MonitorStatus` +
   listeners (§6) and written by herdr-cron.
3. **Next-run recovery after restart.** `Job.NextRun()` is zero until `Start()`
   `[GC job.go:1660-1663]`, and after `Start()` it is recomputed from the schedule
   and the current time — **not** from any record of the previous process. Two
   consequences: (a) `herdr-cron next-run` in a CLI process with no daemon cannot
   ask gocron for a schedule it never registered — use `NewDefaultCron(...)` (§3)
   or a standalone parser (§10); (b) if herdr-cron wants "missed while you were
   offline" semantics, it must store the last successful run per job and compute
   the gap itself. gocron will not do it (§8).
4. **Catch-up policy.** Entirely herdr-cron's decision and code. gocron's only
   related knob is `WithStartAtGrace`, which fires **at most one** run and is
   explicit about it: "No catch-up is performed for recurring schedules. If the
   scheduler was down long enough to miss multiple cron ticks, at most one
   grace-triggered fire happens on startup, and the schedule resumes from
   schedule.Next(now)" `[GC job.go:906-909]`.

### `Elector` and `Locker` — not relevant to a single-machine daemon

```go
type Elector interface {
	IsLeader(context.Context) error
}
type Locker interface {
	Lock(ctx context.Context, key string) (Lock, error)
}
type Lock interface {
	Unlock(ctx context.Context) error
}
```
`[GC distributed.go:10-45]`

`Elector` gates *all* jobs on one instance being leader — `runJob` calls
`IsLeader` and, on error, reschedules and counts a `Skip`
`[GC executor.go:443-448]`. `Locker` takes a per-run lock **keyed on the job
name** ("The lock key passed is the job's name - which, if not set, defaults to
the go function's name" `[GC distributed.go:19-20]`), held for the run's duration
via `defer lock.Unlock` `[GC executor.go:449-474]`. Both are configured by
`WithDistributedElector` / `WithDistributedLocker` `[GC scheduler.go:1224,1238]`,
with per-job overrides `WithDistributedJobLocker` /
`WithDisabledDistributedJobLocker` `[GC job.go:730-751]`.

For a single-machine daemon: **not needed.** The doc itself warns the design does
not synchronise run times across instances and is "vulnerable to clockskew
between scheduler instances" `[GC distributed.go:22-32]`. The one adjacent idea
worth stealing is *single-instance* mutual exclusion: herdr-cron should hold an OS
lock (lockfile / named mutex) so two daemons on the same machine cannot both fire
the same job. That is herdr-cron's problem, not gocron's, and `Locker` is the
wrong tool for it (it locks per run, not per process).

---

## 8. Timezones, clocks, sleep, and missed runs

### `WithLocation` `[GC scheduler.go:1331]` and `WithClock` `[GC scheduler.go:1210]`

`WithLocation(location *time.Location) SchedulerOption` defaults to `time.Local`
`[GC scheduler.go:205]`; nil → `ErrWithLocationNil`. Every internal "now" flows
through

```go
func (s *scheduler) now() time.Time {
	return s.exec.clock.Now().In(s.location)
}
```
`[GC scheduler.go:829-831]`

and the location is threaded into `definition.setup(&j, s.location, ...)`
`[GC scheduler.go:1062]`, where `convertAtTimesToDateTime` builds the at-times in
that zone `[GC util.go:84-103]` `[GC job.go:584-586]`. For cron it becomes a
`CRON_TZ=` prefix unless the crontab carries its own (§3) — verified that an
inline prefix wins: `"TZ=America/New_York 0 9 * * *"` resolved to
`2026-09-02T22:00:00+09:00` = 09:00 New York `[RUN 1]`.

`WithClock(clock clockwork.Clock) SchedulerOption` (nil → `ErrWithClockNil`) is
used for *everything* time-related: `now()`, `AfterFunc` timer arming
`[GC scheduler.go:583,753]`, the shutdown timer `[GC scheduler.go:310]`, the
executor's stop timer `[GC executor.go:664]`, and all recorded timestamps
`[GC executor.go:519,529,533,548]`. The interface:

```go
type Clock interface {
	After(d time.Duration) <-chan time.Time
	Sleep(d time.Duration)
	Now() time.Time
	Since(t time.Time) time.Duration
	Until(t time.Time) time.Duration
	NewTicker(d time.Duration) Ticker
	NewTimer(d time.Duration) Timer
	AfterFunc(d time.Duration, f func()) Timer
}
```
`[CW clockwork.go:14-23]`

### DST

```go
const (
	DaylightSavingsTimeDefault DaylightSavingsTimePolicy = iota
	DaylightSavingsTimeSkip
	DaylightSavingsTimeRunAfterTransition
)
```
`[GC job.go:23-39]`

`Default` preserves per-type legacy behaviour: "CronJob skips to the next valid
occurrence; DailyJob, WeeklyJob, and MonthlyJob run at the clock-adjusted time
after the transition" `[GC job.go:24-27]`. `Skip` drops any occurrence landing in
a spring-forward gap; `RunAfterTransition` runs it at the adjusted
post-transition time `[GC job.go:29-38]`. Duration jobs are unaffected — they
never consult wall-clock fields `[GC job.go:808-810]`.

Gap detection is a round-trip through `time.Date`: if the normalised
hour/minute/second differ from what was requested, the wall-clock time did not
exist `[GC job.go:1416-1426]` `[GC job.go:1273-1295]`. Fall-back (the
*duplicated* hour) is handled separately in `cronJob.next`, which detects an
identical wall-clock date+time and skips forward, because "cron.Next always
advances at least one second in absolute time, [so] identical wall-clock
date+time can only happen during a Daylight Saving Time fall-back"
`[GC job.go:1311-1325]`. Without that guard, robfig would fire twice at e.g. 01:30.

### Sleep, suspend, and missed runs — this is the important part

The executor and scheduler code answer this directly. Rescheduling flows through
`selectExecJobsOutForRescheduling`, which contains this comment verbatim:

```go
	if next.Before(s.now()) {
		// in some cases the next run time can be in the past, for example:
		// - the time on the machine was incorrect and has been synced with ntp
		// - the machine went to sleep, and woke up some time later
		// in those cases, we want to increment to the next run in the future
		// and schedule the job for that time.
		var ok bool
		next, ok = s.advancePastNow(j, next)
```
`[GC scheduler.go:545-552]`

and `advancePastNow` walks the schedule forward, discarding every intermediate
tick, until it is no longer in the past:

```go
func (s *scheduler) advancePastNow(j internalJob, next time.Time) (time.Time, bool) {
	for next.Before(s.now()) {
		n := j.next(next)
		if n.IsZero() || !n.After(next) {
			return time.Time{}, false
		}
		next = n
	}
	return next, true
}
```
`[GC scheduler.go:413-422]`

The same logic guards the first run via `firstRunOrGrace`
`[GC scheduler.go:473-485]` and `prepareFirstRun` `[GC scheduler.go:721-760]`. So:

- **Missed ticks are dropped, never replayed.** At most the one timer that expired
  during the gap fires late (Go's `time.AfterFunc` fires as soon as the runtime
  notices it is overdue), and then the schedule resumes from `next(now)`.
- **Ticks missed while the process was stopped are dropped entirely.** `StopJobs`
  for 1 s over a 100 ms job (10 ticks) produced exactly one run after restart
  `[RUN 4]`.
- **Ticks missed while the process was not running at all are dropped, and no
  immediate fire happens either.** A cron job registered with
  `WithStartDateTimePast(now-1h)` produced **zero** runs on `Start()`:

  ```
  fires within 1.5s of Start (backdated 1h): 0
  NextRun after Start: 2026-09-02T10:22:00+09:00
  ```
  `[RUN 3]`

  That is `firstRunOrGrace` taking the `advancePastNow` branch because
  `startAtGrace` defaults to `0` — "strict — any past first-run time at dispatch
  is treated as exhausted" `[GC job.go:911-912]`.
- **`WithStartAtGrace` buys exactly one catch-up fire.** Same job, plus
  `WithStartAtGrace(2*time.Hour)`:

  ```
  C fires with 2h grace on a 1h-backdated job: 1
  ```
  `[RUN 4]`

  One, not sixty — matching the documented "at most one grace-triggered fire
  happens on startup" `[GC job.go:906-909]`. Remember this option is **not in
  v2.22.0** (§1).
- **A schedule that can never advance is treated as exhausted and the job is
  removed.** `advancePastNow` returning `ok=false` leads to `selectRemoveJob`
  `[GC scheduler.go:552-556]`, with the rationale documented against issue #943
  `[GC scheduler.go:424-441]`.

### Laptop suspend, specifically

gocron's `clock.AfterFunc` on a real clock is `time.AfterFunc`
`[CW clockwork.go:61-63]`, whose deadline is measured on Go's monotonic clock.
On Linux `amd64`, the Go runtime's `nanotime` is `clock_gettime(CLOCK_MONOTONIC)`
— `MOVL $1, DI // CLOCK_MONOTONIC` `[GOSRC runtime/time_linux_amd64.s:59,92]`.
`CLOCK_MONOTONIC` "does not count time that the system is suspended"
`[MAN https://man7.org/linux/man-pages/man2/clock_gettime.2.html]`.

The consequence, which I mark as `[INFERENCE]` because I did not suspend a
machine to test it: on a Linux laptop that sleeps for an hour, a timer armed for
10 minutes from now fires roughly 10 minutes of *awake* time later, i.e. about
1h10m of wall time later — and the reschedule path then jumps to
`next(wall-clock now)`, so all intermediate ticks vanish. Windows and macOS use
different `nanotime` backends and may or may not include suspend; I did not verify
either. This behaviour is not gocron's to fix, and it is a strong argument for
herdr-cron owning a wall-clock-based reconciliation pass at wake/startup: read the
last successful run per job from the store, compare against a schedule evaluated
with `NewDefaultCron`/gronx/robfig, and decide per job whether to fire, log a
miss, or ignore.

---

## 9. Testing with `WithClock` / clockwork

The README states the intent: "Time can be mocked by passing in a FakeClock to
WithClock" `[GC README.md:231-235]`, and mocks for the `Scheduler`, `Job`,
`Logger`, and distributed interfaces are generated with gomock into a separate
`mocks` module `[GC README.md:230-232]`, present in the tree at
`/tmp/hc-research/gocron/mocks/{scheduler,job,logger,distributed}.go` with its own
`go.mod`.

The canonical pattern, copied verbatim from the package examples:

```go
func ExampleWithClock() {
	fakeClock := clockwork.NewFakeClock()
	s, _ := gocron.NewScheduler(
		gocron.WithClock(fakeClock),
	)
	var wg sync.WaitGroup
	wg.Add(1)
	_, _ = s.NewJob(
		gocron.DurationJob(
			time.Second*5,
		),
		gocron.NewTask(
			func(one string, two int) {
				fmt.Printf("%s, %d\n", one, two)
				wg.Done()
			},
			"one", 2,
		),
	)
	s.Start()
	_ = fakeClock.BlockUntilContext(context.Background(), 1)
	fakeClock.Advance(time.Second * 5)
	wg.Wait()
	_ = s.StopJobs()
	// Output:
	// one, 2
}
```
`[GC example_test.go:868-895]`

Three parts make this deterministic. `NewFakeClockAt(t)` pins the start instant
(the doc is explicit: "Tests that require a deterministic time must use
NewFakeClockAt") `[CW clockwork.go:81-95]`. `BlockUntilContext(ctx, n)` waits
until the clock has `n` registered waiters — i.e. until the scheduler has actually
armed its timer — closing the classic start-up race `[CW clockwork.go:238]`.
`Advance(d)` then fires the timers.

What this enables for herdr-cron: assert that a given crontab fires at the
expected wall-clock instants, that `WithStopAt` removes the job, that
`WithLimitedRuns` stops after N, and that a `MonitorStatus` implementation records
the right rows — all without sleeping.

**A real trap: do not `Advance` across a long span in one call.** `Advance` fires
the earliest waiter repeatedly and **sets the clock to each waiter's expiration as
it goes** (`now := w.expiration(); fc.time = now`), so it steps *through*
intermediate times rather than jumping, and it holds `fc.l` for the whole loop
`[CW clockwork.go:202-224]`. Meanwhile `fakeTimer.expire` dispatches the callback
on a **new goroutine** (`go f.afterFunc()`) `[CW timer.go:63-67]`. The result is a
race between the fired callbacks and the scheduler's reschedule logic. Measured on
a `CronJob("* * * * *", false)` under a fake clock `[RUN 9]`:

```
one Advance(1h)              runs=9962   nextRun=11:01:00 pendingNextRuns=3
60x Advance(1m)              runs=60     nextRun=11:01:00 pendingNextRuns=3
```

An earlier run of the same jump produced `7750` `[RUN 2]` — it is
non-deterministic. Stepping the clock produces the correct `60`. So: **step the
fake clock in schedule-sized increments, and never use a single large `Advance`
to model suspend/downtime.** For downtime semantics, use the real-clock probes in
§8 (backdated start, `StopJobs` gap) instead.

---

## 10. Alternatives, briefly

### `github.com/robfig/cron/v3` (v3.0.1)

This is gocron's own cron parser `[GC go.mod:8]`, and it is also a complete
scheduler in its own right: `New(opts ...Option) *Cron`
`[RF cron.go:108-126]`, `AddFunc(spec, cmd) (EntryID, error)`
`[RF cron.go:136]`, `Schedule(schedule Schedule, cmd Job) EntryID`
`[RF cron.go:153]`, `Entries() []Entry` / `Entry(id) Entry`
`[RF cron.go:172,189]`, `Remove(id)` `[RF cron.go:199]`, `Start()` /
`Run()` / `Stop() context.Context` `[RF cron.go:210,221,318]`. Its `Entry`
carries `Next` and `Prev` timestamps `[RF cron.go:44-68]`, and it ships
`JobWrapper`s that gocron does not have: `Recover(logger)`,
`DelayIfStillRunning(logger)`, `SkipIfStillRunning(logger)`
`[RF chain.go:38,61,78]` — the third is roughly `WithSingletonMode(LimitModeReschedule)`,
the second roughly `LimitModeWait` with depth 1. Its run loop fires every entry
whose `Next <= now` exactly once on wake and then recomputes from `now`
`[RF cron.go:257-272]`, i.e. the same "one late run, no catch-up" semantics as
gocron. What it does **not** have: daily/weekly/monthly/random-duration job
types, `NextRuns(n)`, tags, per-job contexts, an error-returning constructor, or
any metrics interface — and the pinned HEAD is from **2019-07-15**, so it is
effectively frozen. The genuinely interesting piece for a CLI is the *parser
without the scheduler*: `cron.ParseStandard(spec) (Schedule, error)`
`[RF parser.go:229-231]` and `NewParser(options).Parse(spec)`
`[RF parser.go:71-88]` give a `Schedule` whose only method is
`Next(time.Time) time.Time` `[RF cron.go:35-39]`, so
`herdr-cron next-run --spec "0 9 * * MON-FRI"` can be answered in a short-lived
process with no daemon. Note that gocron already re-exports this capability as
`NewDefaultCron(withSeconds).IsValid/.Next` (§3), so herdr-cron probably does not
need a second dependency for it — using `NewDefaultCron` guarantees the CLI's
`next-run` and the daemon's actual firing agree bit-for-bit, including the
`CRON_TZ=` prefixing rules.

### `github.com/adhocore/gronx`

Zero-dependency (`module github.com/adhocore/gronx`, `go 1.13`, no `require`
block `[GX go.mod]`), and its whole point is *stateless* evaluation: package-level
`NextTick(expr string, inclRefTime bool) (time.Time, error)`,
`NextTickAfter(expr, start, inclRefTime)`, `PrevTick`, `PrevTickBefore`
`[GX next.go:19,24]` `[GX prev.go:9,14]`, plus `Gronx.IsDue(expr, ref ...time.Time)`
and `BatchDue(exprs, ref ...time.Time)` for evaluating many expressions against
one instant `[GX gronx.go:77,117]` `[GX batch.go:17]`. **`PrevTick` has no
equivalent anywhere in gocron or robfig**, and it is exactly the primitive a
catch-up pass wants: "what should have run between the last recorded run and
now". Its expression grammar is also richer — 5, 6, or 7 fields
(`<second> <minute> <hour> <day> <month> <weekday> <year>`)
`[GX README.md:279-294]`, plus `L`/`W` day-of-month and `L`/`#` day-of-week
modifiers `[GX README.md:339-345]` and tags like `@5minutes`, `@30minutes`,
`@everysecond` `[GX README.md:316-330]`. Verified against robfig on the same
reference instant `[RUN 6]`:

```
"0 0 L * *"        gronx next=2026-09-30T00:00:00Z | robfig ERR failed to parse int from L
"0 0 * * 5#3"      gronx next=2026-09-18T00:00:00Z | robfig ERR failed to parse int from 5#3
"0 0 1 1 * 2027"   gronx next=2027-01-01T00:00:00Z | robfig ERR expected exactly 5 fields, found 6
"@every 90s"       gronx ERR expr should contain 5-7 segments | robfig next=2026-09-02T10:31:30Z
```

Two cautions, both measured. gronx does **not** understand `@every`. And the
**latest released version has a next-run correctness bug** that the pinned main
branch fixes: at `v1.8.1`, `NextTickAfter("0 0 31 * *", 2026-09-02, false)`
returned `2026-10-03` where robfig returns `2026-10-31`, and
`"0 0 29 2 *"` returned `2027-03-04` (a date that is not Feb 29) where robfig
returns `2028-02-29` `[RUN 6]`. Against the pinned clone
(`74da1959`, 2026-08-17) both are correct `[RUN 7]`:

```
ref=2026-09-02 gronx=2028-02-29T00:00:00Z  robfig=2028-02-29T00:00:00Z
"0 0 31 * *" gronx=2026-10-31T00:00:00Z robfig=2026-10-31T00:00:00Z
```

So gronx is attractive for a daemon-free `next-run`/`missed-runs` command and for
`L`/`#`/year syntax, but herdr-cron would have to pin a post-`v1.8.1`
pseudo-version and accept that gronx's grammar is a **superset** of what
`CronJob` will actually accept — a CLI that validates with gronx and schedules
with gocron will happily accept `0 0 L * *` and then fail at `NewJob`. If
herdr-cron wants the richer grammar it must supply gronx to gocron through
`WithCronImplementation` `[GC job.go:795]`, implementing
`IsValid`/`Next` `[GC job.go:198-201]` over `gronx`, and honour the concurrency
contract documented at `[GC job.go:191-197]`.

---

## Implications for herdr-cron `[INFERENCE]`

Everything in this section is my reasoning from the evidence above, not a claim
about any source.

**gocron is the right engine, and it is a thin one.** It gives you seven schedule
shapes with careful DST handling, a well-behaved single-goroutine state machine, a
clean introspection surface for a TUI, and a metrics hook. That is genuinely
valuable and hard to get right. It gives you nothing else.

**What herdr-cron must build on top:**

1. **A store, and it is the source of truth.** Job definitions (schedule kind +
   parameters + options + the command to run), stable IDs replayed into gocron via
   `WithIdentifier`, and a run-history table. `Update`'s history wipe (§2) and the
   unserialisable `internalJob` (§7) make this non-optional.
2. **A history writer fed by `MonitorStatus` + `AfterJobRunsWithPanic` + a
   non-blocking `BeforeJobRuns`.** Never do I/O inside a listener; the reschedule
   path is upstream of `BeforeJobRuns` and the notification channels are
   unbuffered (§6).
3. **A wake/startup reconciliation pass.** gocron drops every missed tick and
   fires at most one late run (§8). herdr-cron should, on start and ideally on
   wake, walk its store, compute what should have fired since the last recorded
   run, and apply a per-job policy: `catch_up: skip | once | all`. `once` maps
   naturally onto `WithStartAtGrace` when it ships; `all` and `skip` need
   herdr-cron's own code plus a `PrevTick`-style backward iterator.
4. **A global panic listener.** One line via `WithGlobalJobOptions`, and without
   it a single bad agent task kills the daemon (§6).
5. **Its own single-instance lock.** `Locker` is per-run, not per-process (§7).

**Does a gocron scheduler belong in the CLI process?** I think **no**, with one
carve-out.

The argument against: `NewScheduler` starts a goroutine and `Start()` arms timers;
neither survives process exit. A one-shot CLI invocation that constructs a
scheduler just to answer "when does this run next" pays for a goroutine, a
`context`, and a channel handshake, and then throws away the answer's own
prerequisite — `NextRun()` is zero until `Start()` (§5) and after `Start()` it is
computed from *now*, not from history. And `NewJob` returning `(job, nil)` on a
shut-down scheduler (§2) is a trap a short-lived process is likely to hit.

The carve-out: `NewDefaultCron(withSeconds)` is deliberately exported "for use
outside the scheduling of a job" `[GC job.go:223-225]` and needs no scheduler at
all. So the split I would draw is:

- **CLI process**: `NewDefaultCron` for `validate` and `next-run` (guaranteeing
  the CLI's answer matches the daemon's behaviour bit-for-bit, including
  `CRON_TZ=` precedence), plus reads/writes against the store, plus IPC to the
  daemon for `run-now` / `enable` / `disable`. No `Scheduler`.
- **Daemon process**: the single `Scheduler`, the single-instance lock, the
  listeners, the reconciliation pass, and an IPC endpoint. `Jobs()` +
  `NextRuns(n)` + `LastRunCompletedAt` render the TUI (with the freshness and
  ordering caveats from §5).
- **TUI**: a client of the daemon over IPC, not an owner of a `Scheduler`.
  Owning one would mean the schedule stops the moment the user quits the TUI.

The one place this gets genuinely debatable is a `herdr-cron run --once` / `--fg`
mode, where a foreground process *is* the daemon for its lifetime. That is fine —
same code path, different lifetime — as long as the store, not the process, holds
the truth.

---

## Could not verify

- **Actual laptop-suspend behaviour.** I did not suspend a machine. The
  `CLOCK_MONOTONIC`-does-not-count-suspend claim is from the Linux man page
  `[MAN clock_gettime]` and the Go runtime's use of `CLOCK_MONOTONIC` is from
  `[GOSRC runtime/time_linux_amd64.s:59,92]`; the *combined* consequence for a
  sleeping laptop is marked `[INFERENCE]` in §8.
- **Windows and macOS timer/clock behaviour across suspend.** Not investigated.
  herdr-cron is cross-platform, so this needs its own experiment on each target
  before the catch-up policy is finalised — the answer may differ per OS.
- **The exact mechanism behind the fake-clock duplicate-run storm** (§9). I
  established that it happens, that it is non-deterministic (`7750` then `9962`
  runs for the same 1-hour `Advance`), and that stepping the clock avoids it, but
  I did not root-cause the interleaving between `Advance` holding `fc.l`, the
  `go f.afterFunc()` dispatch, and `advancePastNow` blocking on `fc.l.RLock()`.
  I did not check whether upstream considers this a gocron bug, a clockwork bug,
  or expected.
- **Whether the released `v2.22.0` behaves identically to the pinned branch tip**
  on anything other than the presence of `WithStartAtGrace`. The commit at the
  pinned SHA is a scheduler refactor
  ("extract shared first-run logic from selectNewJob/selectStart (#947)",
  `git log -1`), so first-run semantics specifically may differ between the two.
  I ran probes on both — `[RUN 1,2,3]` against released `v2.22.0` and
  `[RUN 4,5,8,9,10]` against the branch tip (see the `[RUN]` tag definition) — and
  saw no contradiction between them, but I did not re-run every probe on both
  versions, so I cannot claim equivalence. The findings that exist **only** on the
  branch tip are `WithStartAtGrace`'s single catch-up fire `[RUN 4]`; the findings
  that hold on the **released** version include the zero-catch-up backdated start
  and the `BeforeJobRuns` cadence stall `[RUN 3]`.
- **The `v2.22.0` release date.** `curl https://api.github.com/repos/go-co-op/gocron/releases/tags/v2.22.0`
  returned nothing usable (unauthenticated request); the tag SHA
  `10cdd5e5558f8e9b0dfa848df2b0e02933f44163` is from `git ls-remote --tags`.
- **Whether `LastRunStartedAt` returning `lastRun` is a known upstream bug.** I
  confirmed the behaviour in the code at both the branch tip
  `[GC job.go:1737-1746]` and the release tag `[GC-2220 job.go:1673-1682]` and
  measured it `[RUN 10]`, but did not search the issue tracker.
- **`gocron-ui`** (referenced at `[GC README.md:8-10]`, URL returns HTTP 200) as a
  possible reference implementation for the TUI's data model. Not read; the
  Bubble Tea research document is the right home for that question.
- **Behaviour under `WithDistributedElector`/`WithDistributedLocker`.** Read but
  not exercised; judged out of scope for a single-machine daemon (§7).
- **`Monitor`/`MonitorStatus`/`SchedulerMonitor` under load.** I read every call
  site and established the synchronous-on-run-goroutine property from the code,
  but did not benchmark a slow monitor implementation the way I did for event
  listeners.
