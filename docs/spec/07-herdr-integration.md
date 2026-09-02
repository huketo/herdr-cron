---
title: herdr-cron — Herdr integration
date: 2026-09-02
status: spec (normative)
---

# Herdr integration

Normative. RFC 2119 keywords. Specifies the whole boundary between herdr-cron and Herdr: how the `herdr` binary is
found and called, how agent jobs execute inside Herdr panes, how their outcomes map to the run statuses of
[`03-job-model.md`](03-job-model.md) §6, and how herdr-cron ships as a Herdr plugin.

Non-obvious claims are grounded in `docs/research/2026-09-02-herdr-plugin-integration.md`, cited **HPI §n**.
`[BIN 0.8.2]` marks facts verified on 2026-09-02 by running `--help` or a read-only command against `herdr 0.8.2` at
`/home/huke/.local/bin/herdr` while writing this spec. `[UNVERIFIED]` marks claims with no source; all are collected
in §10. `kind: shell` runs are direct child processes of the runner ([`03-job-model.md`](03-job-model.md) §3.1), not
Herdr's business; this document only requires that they keep working when Herdr is absent (§9).

---

## 1. The Herdr adapter

### 1.1 One rule: shell out to the CLI

Every interaction with Herdr MUST be an argv exec of the `herdr` **CLI** with captured stdout/stderr. herdr-cron MUST
NOT open `HERDR_SOCKET_PATH` or speak the newline-delimited JSON protocol. This is Herdr's own recommendation: "Unix
clients connect to a Unix socket path, while Windows clients connect to a named pipe. CLI calls through
`HERDR_BIN_PATH` avoid that transport difference" (HPI §3). Speaking the socket would make that split herdr-cron's to
maintain forever, and with no SDK and no import path (HPI §3) it also means hand-written types for protocol 20. Every
plugin surveyed shells out; herdr-reviewr does it from Rust rather than implement a client (HPI §3). One spawn per
call (~40 ms `[BIN 0.8.2]`) against roughly eight calls per run is not a cost worth optimising.

### 1.2 Binary resolution order

Resolved once per process at first use, then cached:

1. **`HERDR_BIN_PATH`** — injected into every runtime plugin command and authoritative, since Herdr-owned variables
   win over caller `--env` (HPI §2). It names the running server's own binary, so version skew is impossible in
   plugin mode.
2. **`herdr` on `PATH`.**
3. **`<HERDR_PLUGIN_ROOT>/bin/herdr`, then platform install locations** — `~/.local/bin/herdr`,
   `/opt/homebrew/bin/herdr`, `/usr/local/bin/herdr`, `%LocalAppData%\Programs\herdr\herdr.exe`. Required because
   Herdr runs plugin commands with a **minimal `PATH`** (HPI §2; herdr-reviewr prepends
   `/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin`), so step 2 can fail inside a plugin invocation on a machine
   where `herdr` is plainly installed.

All three failing makes the adapter **unavailable**: every `kind: agent` operation fails with error code
`herdr_unavailable` ([`05-cli.md`](05-cli.md) §2.1); `kind: shell` is unaffected (§9). The mirror rule applies
outward — any `git` or notifier subprocess herdr-cron spawns MUST get a `PATH` with the platform bin directories
prepended (HPI §8).

### 1.3 Version gate

**`min_herdr_version = "0.8.2"`.** 0.8.2 is what was probed: the entire headless path of §3 — `herdr server` on a
session that never hosted a TUI, `workspace create`, `tab create`, `agent start`, `agent prompt --wait`, `agent read`,
all with no client attached — is evidence against 0.8.2 and nothing older (HPI §9), and pinning to the probed version
is the only honest floor. Two lower requirements sit underneath: `contexts = ["global"]` (§8) is in the binary's
`PluginActionContext` enum (`{"enum":["global","workspace","tab","pane","selection"]}`, HPI §1), for which
`herdr-hitl` pins `0.8.0`; and `cwd` on `agent list` entries — used by §2.5 — is only verified present from 0.7.5,
which is what herdr-reviewr pins for that field (HPI §1).

Enforced twice. **By Herdr at link time**: the field is required and hard — "the server refuses to link a plugin when
the field is missing, invalid, or newer than the running Herdr binary" (HPI §1). **By the adapter at run time**, since
the standalone CLI has no link step: parse `herdr --version` (`herdr 0.8.2` `[BIN 0.8.2]`) or `.client.version` from
`herdr status --json`. Below the floor the adapter is unavailable with reason `herdr_version_unsupported`; it MUST NOT
degrade silently, or the failure surfaces as an unexplained `agent start` error at 03:00.

### 1.4 Call shape and parsing

```go
// Package herdr is the only package in herdr-cron that knows the herdr CLI exists.
package herdr

// Envelope is herdr's universal CLI response: id, then exactly one of result or error (verified across
// three commands, HPI §5). ErrorBody carries no code enum — codes are open-ended, so callers MUST branch
// on a known set and fall through (§4).
type Envelope[T any] struct {
	ID     string     `json:"id"`
	Result *T         `json:"result"`
	Error  *ErrorBody `json:"error"`
}
type ErrorBody struct{ Code, Message string }

// call execs `<bin> [--session <session>] <args...>`, decodes the envelope, and returns *ErrorBody as the
// error when the response carries one. Every method below is a thin wrapper over it.
func (c *Client) call(ctx context.Context, out any, args ...string) error

func (c *Client) StartAgent(ctx context.Context, req StartAgentRequest) (Agent, error)
func (c *Client) Prompt(ctx context.Context, req PromptRequest) (Agent, error)
func (c *Client) Read(ctx context.Context, target string, src ReadSource, lines int) (string, error)
func (c *Client) Explain(ctx context.Context, target string) (Explain, error)
func (c *Client) ClosePane(ctx context.Context, paneID string) error
```

`Agent` mirrors `.result.agent`; herdr-cron parses `agent`, `agent_status`, `cwd`, `interactive_ready`, `name`,
`pane_id`, `state_change_seq`, `tab_id`, `terminal_title` and `workspace_id`. Two decoding rules: `name`,
`display_agent` and `state_labels` are **omitted entirely until something sets them** (HPI §5), so absence MUST decode
as "unset", never as an empty alias; and `cwd` is the pane's *launch* cwd while `foreground_cwd` is its live one
(HPI §2). `agent_status` is the enum identical everywhere in Herdr:
`{"enum":["idle","working","blocked","done","unknown"]}` (HPI §5). Three mandatory argv rules:

- **`--session` is global and MUST precede the subcommand**: `herdr --session herdr-cron workspace list`, never
  `... workspace list --session herdr-cron` (HPI §9.1).
- **Most subcommands reject `--json`**: `pane list --json` returns `unknown option: --json` and
  `workspace list --json` returns `usage: herdr workspace list` (HPI §9.1) — JSON is simply what they emit. Only
  `herdr status --json` and `herdr agent explain --json` accept it `[BIN 0.8.2]`.
- **`agent explain --json` is not enveloped** — a bare object with no `id`/`result` wrapper `[BIN 0.8.2]` (§4.1 quotes
  it), so it needs its own decode path.

Error contract (HPI §5): server errors are JSON on **stderr** with exit 1; CLI syntax errors exit 2. The adapter
decodes from stdout and stderr, preferring whichever parses, and treats exit 2 as a herdr-cron bug (`internal`) —
never a job failure, since a syntax error means the adapter built a bad command line.

---

## 2. Session and topology management

### 2.1 A session of herdr-cron's own

Decision D7: agent jobs run in a dedicated session named **`herdr-cron`**, so every adapter call carries
`--session herdr-cron` unless a job overrides it (§2.6). `--session <name>` is a pure path computation to
`~/.config/herdr/sessions/<name>/herdr.sock` and needs no pre-existing state (HPI §9.1). The extra server process
buys real isolation: scheduled runs create and destroy panes, and doing that in the human's default session means a
03:00 job reshuffling the workspace list they left open.

### 2.2 Probing, and what "not running" looks like

```
$ herdr --session herdr-cron status --json
{"client":{"version":"0.8.2","channel":"stable","protocol":20,"binary":"…/herdr","session":"herdr-cron"},
 "server":{"status":"not_running","running":false,"version":null,"protocol":null,"capabilities":null,
  "compatible":null,"socket":"/home/huke/.config/herdr/sessions/herdr-cron/herdr.sock",
  "session":"herdr-cron","restart_needed":false},"update":{"restart_needed":false}}
```

`[BIN 0.8.2]`, against a session name that had never existed. Three properties make this the correct pre-flight: it
exits **0** even with the server down, it does **not** create the session, and `.server.status` is a clean
discriminator — `"running"` (HPI §9.1) or `"not_running"`. The adapter MUST branch on `.server.status`, and when
running MUST refuse to proceed if `.server.compatible` is `false`.

### 2.3 Starting the server

When `not_running`: `herdr --session herdr-cron server`, launched as a detached child with piped (not inherited)
stdio, then polled with `status --json` every 250 ms until `"running"`, at most 15 s (the probe's server answered
within 2 s, HPI §9.1). The child MUST NOT be waited on and MUST NOT inherit the runner's controlling terminal: what
bare `herdr server` does with a controlling terminal, whether it forks, and where it logs are `[UNVERIFIED]`
(HPI §9.6), and the shape specified here — supervisor, piped stdio, no TTY — is the only one with evidence. Poll
expiry is `failure` / `herdr_unavailable`, not retried within the run. herdr-cron MUST NEVER run bare `herdr`; that
launches or attaches the TUI (HPI, safety note).

### 2.4 Topology: a fresh headless server is empty

On a server started headlessly in a session that never hosted a TUI, all three collections come back empty
(HPI §9.1) — `workspace list` → `{"type":"workspace_list","workspaces":[]}`, `pane list` →
`{"panes":[],"type":"pane_list"}`, `agent list` → `{"agents":[],"type":"agent_list"}`. There is no `w1` to split: the
adapter MUST build its own topology and MUST NOT assume a workspace exists.

**Host workspace — one per session, reused forever.** Look for a workspace labelled `herdr-cron` in `workspace list`;
if absent, `herdr --session herdr-cron workspace create --label herdr-cron --cwd <state-root> --no-focus`, returning
`.result.workspace.workspace_id`, `.result.tab.tab_id` and `.result.root_pane.pane_id` (HPI §5) — `w1`, `w1:t1`,
`w1:p1` with `"scroll":{"viewport_rows":40}` in the probe (HPI §9.2). `--no-focus` is mandatory: a scheduler never
steals focus. The host root pane is **never** used to run a job; it exists so the workspace has an occupant and a
human attaching lands somewhere sane.

**One tab per run, created and destroyed.** Panes are NOT reused across runs:

```
herdr --session herdr-cron tab create --workspace w1 --cwd <job.cwd> --label cron/<jobId> --no-focus
```

Flags verified `[BIN 0.8.2]` (`--workspace`, `--cwd`, `--label`, `--env KEY=VALUE`, `--focus`, `--no-focus`); parse
`.result.tab.tab_id` and `.result.root_pane.pane_id`. Two verified facts force this over pane reuse: **a pane's `cwd`
is fixed at creation and `agent start` has no `--cwd`** (HPI §9.4), so the per-job working directory can only be
chosen at pane creation; and **`agent start` requires a pane at its interactive shell prompt** — the shell owning the
foreground, no command, editor or agent running (HPI §5) — so a reused pane a previous run left in an odd state is an
unattended failure with no diagnosis. A tab also owns its root pane and closes as a unit. `--env` MAY carry the job's
`env` map, but Herdr-owned variables stay authoritative over it (HPI §2). Before hardcoding geometry: the host root
pane reported `viewport_rows: 40` while a later tab reported `39`, and the headless layout rectangle is
`{"height":39,"width":94,"x":26,"y":1}` — 120×40 minus a 26-column sidebar and a 1-row header (HPI §9.2).

### 2.5 Cleanup policy

`herdr --session herdr-cron pane close <pane_id>` `[BIN 0.8.2]`. Plain `pane close` is used deliberately:
`plugin pane close` operates only on the in-memory plugin-pane registry and refuses a still-live pane with
`plugin_pane_not_found` after a restart (HPI §2). Closing the pane is also how the alias is released — **there is no
`herdr agent release` on 0.8.2** `[BIN 0.8.2]` (`herdr agent --help` lists exactly `list get read send-keys prompt
rename focus wait attach start explain`); the alias "is cleared when that agent exits, is released, or is replaced"
(HPI §5).

Not configurable in v1: on `success`, `no_op`, `failure`, `timeout` and `cancelled` the pane is closed; on **`blocked`
it is left open**, because a human is required and the transcript on screen is the evidence. Every run records
`{"session": "herdr-cron", "paneId": "w1:p3", "agentName": "cron-nightly-deps"}` in the run record's `herdr` object
([`03-job-model.md`](03-job-model.md) §6), so a `blocked` run stays addressable. On daemon start a reconciliation
sweep MUST close orphaned run panes: any pane in the `herdr-cron` workspace whose tab label matches `cron/*` and whose
`pane_id` appears in no non-terminal run record, exempting panes of `blocked` runs, which only `job resume` or the
human closes. The sweep follows herdr-reviewr's converge-vs-refuse discipline — "the pane is gone" is a benign race
that converges and exits 0; a failed call refuses loudly (HPI §8).

### 2.6 The per-job `session` override

`agent.session` ([`03-job-model.md`](03-job-model.md) §3.2): `herdr-cron` is the default per D7; `current` means the
session the runner lives in, resolved with Herdr's own order — `HERDR_SESSION` if set, else the session named by
`HERDR_SOCKET_PATH`, else the default session, in which case `--session` is omitted from the argv entirely (HPI §2);
any other string is passed verbatim as `--session <name>`. `current` exists for the `foreground` driver
([`02-architecture.md`](02-architecture.md)), where the daemon runs in a Herdr pane and the human wants the job's
agent beside it. §2.4 still applies inside that session: herdr-cron creates its own labelled workspace there too and
NEVER adopts a workspace it did not create.

---

## 3. The agent-run sequence

**Step 0 — resolve the adapter** (§1.2, §1.3); failure → `failure` / `herdr_unavailable`, nothing created. herdr-cron
does NOT gate on `HERDR_ENV=1`: that answers "am I inside Herdr" (HPI §2), irrelevant to a daemon driving Herdr from
outside. **Step 1 — ensure the server** (§2.2, §2.3). **Step 2 — ensure the host workspace** (§2.4). **Step 3 — trust
pre-flight** (§5), before any pane exists, so an untrusted job costs one file read and leaves nothing behind.
**Step 4 — create the run tab** (§2.4), recording `.result.root_pane.pane_id` in the run record *before* starting the
agent, so a crash leaves a closable pane id behind. **Step 5 — derive the agent name** (§3.1).

**Step 6 — `agent start`.**

```
$ herdr --session herdr-cron agent start cron-nightly-deps --kind claude --pane w1:p2 --timeout 90000
{"id":"cli:agent:start","result":{"agent":{"agent":"claude","agent_status":"idle",
  "cwd":"/home/huke/huketo/jjalcloud","interactive_ready":true,"name":"probe2","pane_id":"w1:p2",
  "terminal_title":"✳ Claude Code","workspace_id":"w1"},"argv":["claude"],"type":"agent_started"}}
```

(HPI §9.3, which used the name `probe2`; it took 3.973 s.) Required: `.result.type == "agent_started"`,
`.result.agent.agent_status == "idle"`, `.result.agent.interactive_ready == true`, and `.result.agent.pane_id` equal
to the pane passed in; `.result.argv` is logged as the argv Herdr actually launched. `--kind` is `agent.agent_kind`
and MUST be one of the 22 kinds the installed binary accepts `[BIN 0.8.2]`: `pi, claude, codex, gemini, cursor, devin,
agy, cline, omp, mastracode, opencode, copilot, kimi, kiro, droid, amp, grok, hermes, kilo, qodercli, qwen, maki`.
`--timeout` is readiness wait in ms (default 30000, max 300000; the socket schema requires "greater than 3000 and at
most 300000", HPI §5); herdr-cron uses **90000**, what the probe used, and MUST clamp computed values into
`(3000, 300000]`. Native agent args, when a future field supplies them, go after `--`:
`herdr agent start reviewer --kind codex --pane "$review_pane" -- -m gpt-5.4` (HPI §5).

**Step 7 — `agent prompt --wait`.**

```
$ herdr --session herdr-cron agent prompt cron-nightly-deps "<preamble>\n\n<prompt>" --wait --timeout 2700000
{"result":{"agent":{"agent_status":"done","interactive_ready":true,"name":"probe2",
  "terminal_title":"✳ Headless OK","state_change_seq":6},"type":"agent_prompted"}}
```

(HPI §9.3.) The text is the scheduler preamble of [`03-job-model.md`](03-job-model.md) §3.3, a blank line, then the
job's `prompt`, all as **one positional argument**; `--timeout` is `agent.wait_timeout` (default: the job `timeout`)
in milliseconds. **`--until` MUST NOT be passed.** Without it, `--wait` "matches idle, done, or blocked by default"
(HPI §5) — exactly herdr-cron's terminal set. `--until done --until blocked` would make a legitimate `idle` settle
(which happens the moment a human focuses the tab) run to timeout, and `--until unknown` would let an unclassifiable
agent be reported finished. The default set is correct *because* it excludes `unknown`: an agent Herdr cannot classify
overruns into a `timeout`, which is truthful, rather than a false `success`. Prompt and wait are one request on
purpose — it "submits the prompt and starts the wait in one request, avoiding a race between separate calls"
(HPI §5) — so herdr-cron MUST NOT use `agent prompt` followed by a separate `agent wait`.

**Step 8 — `agent read`**, skipped when `agent.capture` is `none`:

```
herdr --session herdr-cron agent read cron-nightly-deps --source recent-unwrapped --lines 200
```

**CORRECTION, verified by implementation on 2026-09-02: `agent read` is NOT enveloped.** It prints the raw terminal
snapshot on stdout; there is no `.result.read.text` and no `--json`. `herdr agent read --help` `[BIN 0.8.2]` offers
`--source`, `--lines`, `--format text|ansi` and `--ansi` — no JSON anywhere. Decoding it as an envelope fails with
"produced no parseable envelope" and loses every transcript, which is exactly what happened on the first live agent
run. An **error** still arrives as a JSON envelope on stderr with exit 1, so the adapter reads stdout as text and only
parses stderr when the exit status is non-zero. `recent-unwrapped` joins soft wraps and is what Herdr's own skill says to
prefer for logs and transcripts; default row count is 80 for recent sources (HPI §5). Fallback chain, because
`--lines N` beyond the visible screen drives the agent's mouse-scroll interface and returns `agent_not_idle` while the
agent is working, blocked or unknown (HPI §5): `recent-unwrapped --lines 200`; on `agent_not_idle` retry `--lines 80`
(no scrolling at 39–40 visible rows); on `agent_not_idle` again use `--source detection`, the plain-text bottom-buffer
snapshot, and record `capture: "partial"` in the run log's header line. The read is safe: **a CLI read does not clear
`done`** — after the probe's read, `agent get probe2` still reported
`{"agent_status":"done","interactive_ready":true,"state_change_seq":6}` (HPI §9.3). herdr-cron therefore archives the
transcript without destroying the human's unseen-work signal, and MUST NOT call `agent focus`, which would consume it
(HPI §5).

**Step 9 — classify** (§4). **Step 10 — clean up** (§2.5, plus worktree teardown, §6).

### 3.1 Agent naming

Herdr names must match `[a-z][a-z0-9_-]{0,31}` and be unique among live agents (HPI §5). Job ids match
`^[a-z0-9][a-z0-9._-]{0,127}$` ([`03-job-model.md`](03-job-model.md) §1.2) — up to 128 bytes and allowed to contain
`.`, which the agent grammar forbids. The derivation is lossy and MUST be deterministic:
`func AgentName(jobID, runID string) string`, whose result always matches `[a-z][a-z0-9_-]{0,31}`.

1. `slug` = `jobID` with every byte outside `[a-z0-9_-]` replaced by `-`, runs of `-` collapsed, leading/trailing `-`
   trimmed (`jobID` is already lowercase by its own pattern).
2. `len(slug) <= 27` → `"cron-" + slug`; so `nightly-deps` → `cron-nightly-deps`, the value in the run-record example
   of [`03-job-model.md`](03-job-model.md) §6. Otherwise `"cron-" + slug[:18] + "-" + hex8`, where `hex8` is the low
   32 bits of `FNV1a64(jobID)` as 8 lowercase hex digits — length exactly 32. The `cron-` prefix satisfies the
   leading-letter rule and makes herdr-cron's agents obvious in `agent list`.
3. Uniqueness is checked against `.result.agents[].name` from `agent list`, where a missing `name` MUST read as "no
   alias", never as an empty-string alias (HPI §5). On collision (a previous run's agent still live, which
   `concurrency: allow` makes legal) the name becomes `"cron-" + slug[:18] + "-" + hex8(runID)`, deterministic per
   run; a further collision is `failure` / `agent_name_collision`, not retried in-run.

The alias is ephemeral by design — it "does not permanently rename the pane" and evaporates when the agent exits
(HPI §5). Job identity is herdr-cron's `jobId`/`runId`; the Herdr name is a handle for one run, recorded for later
correlation.

---

## 4. Outcome classification

Herdr signals mapped to the run statuses of [`03-job-model.md`](03-job-model.md) §6. Normative and total: any signal
not listed is `failure` with reason `herdr_unexpected` and the raw envelope in the run log.

| Signal | Where | Status | `reason` | Retried? |
| --- | --- | --- | --- | --- |
| `agent_status: "done"` | `agent prompt --wait` → `.result.agent.agent_status` | `success`, or `no_op` per §4.2 | — | n/a |
| `agent_status: "idle"` | same | `success`/`no_op`, **subject to the §4.1 cross-check** | — | n/a |
| `agent_status: "blocked"` | same | `blocked` | `agent_blocked` | **Never** |
| `agent_status: "unknown"` | only via `agent get`; never matched by `--wait` (§3 step 7) | `timeout` | `agent_unknown` | Per `retry` |
| error `agent_not_ready` | `agent start` | `blocked` | `agent_startup_dialog` | **Never** |
| error `agent_blocked` | `agent prompt` — already blocked, submission rejected before any input was sent (HPI §5) | `blocked` | `agent_blocked` | **Never** |
| error `agent_prompt_stalled` | `agent prompt --wait` — submission accepted, no state change observed within 5000 ms (HPI §5) | `failure` | `agent_prompt_stalled` | Per `retry` |
| error `timeout` | `agent prompt --wait` with a `--timeout` shorter than the stall window, or the wait budget expiring | `timeout` | `wait_timeout` | Per `retry` |
| error `agent_not_idle` | `agent read` | not terminal — fallback chain, §3 step 8 | — | n/a |
| error `agent_not_running` | any agent command | `failure` | `agent_vanished` | Per `retry` |
| error `pane_not_found` | any pane command | `failure` | `pane_lost` | Per `retry` |
| `server.status: "not_running"`, startup failed | §2.3 | `failure` | `herdr_unavailable` | Per `retry` |
| no `herdr` binary | §1.2 | `failure` | `herdr_unavailable` | Per `retry` |
| version below the floor | §1.3 | `failure` | `herdr_version_unsupported` | **Never** |
| trust pre-flight failed | §5 | `blocked` | `cwd_not_trusted` | **Never** |
| job `timeout` reached with any of the above in flight | the runner's own deadline | `timeout` | `job_timeout` | Per `retry` |

`blocked` is terminal and never retried ([`03-job-model.md`](03-job-model.md) §4.4) for an empirical reason: an agent
on an approval dialog with nobody present never resolves itself (HPI §9.4). A `blocked` run always notifies (§7) and,
with `--wait`, exits 3 ([`05-cli.md`](05-cli.md) §2.2).

### 4.1 Which signals do not prove completion

**`unknown` never does.** Herdr's own skill says so: "`unknown` means an agent is present but Herdr cannot classify it
confidently; it does not prove completion" (HPI §5). herdr-cron's defence is structural, not a check: `--wait` without
`--until` cannot match `unknown`, so an unclassifiable agent burns its wait budget and is recorded `timeout`.

**`idle` is weaker than it looks, because `blocked` is deliberately strict.** Herdr marks `blocked` only when the live
bottom-buffer snapshot matches a *known* approval, question or permission UI; with no matching rule for a known agent,
"Herdr falls back to `idle` and labels that fallback as `default_known_agent_idle_fallback` in explain output"
(HPI §5). A brand-new approval screen Herdr has not learned therefore reads as `idle` — **a stalled job can look
finished.** The cross-check, REQUIRED whenever the settle state is `idle`:

```
$ herdr agent explain w2D:p1 --json
{"agent":"omp","cached_remote_version":null,"evaluated_rules":[],"fallback_reason":null,
 "local_override_shadowing_remote":false,"manifest_source":null,"manifest_version":null,"matched_rule":null,
 "remote_update_error":null,"remote_update_status":null,
 "screen_detection_skip_reason":"full_lifecycle_hook_authority","screen_detection_skipped":true,
 "skip_state_update":false,"skipped_update_reason":null,"state":"idle","visible_blocker":false,
 "visible_idle":false,"visible_working":false,"warning":null}
```

`[BIN 0.8.2]`, against a live `omp` agent — note the bare, unenveloped object (§1.4). Using `.fallback_reason`,
`.matched_rule`, `.state` and `.visible_blocker`: when `.fallback_reason == "default_known_agent_idle_fallback"`
**and** the extracted final assistant text (§4.2) is empty, the run is `failure` with reason
`agent_idle_fallback_unverified`, not `success` — an empty answer from a fallback-classified idle is the exact
signature of a stalled dialog. Any other non-null `.fallback_reason`, or `.visible_blocker == true`, is written into
the run log's header line and the run is otherwise classified normally. `agent explain` failing MUST NOT change the
outcome: the cross-check refines, it does not gate.

That `default_known_agent_idle_fallback` lands specifically in `.fallback_reason` for a screen-detected kind such as
`claude` is `[UNVERIFIED]`: the field name is verified `[BIN 0.8.2]` and the label is documented (HPI §5), but the two
were never observed together — the live sample was an `omp` agent with `screen_detection_skipped: true`.
Implementations MUST therefore also match the literal string anywhere in the `agent explain --json` output.

`done` needs no cross-check and is the expected unattended settle: it is "the same underlying idle state after unseen
background work finishes" (HPI §5), and the probe's unattended run settled to `done`, not `idle` (HPI §9.3).

### 4.2 Extracting the final assistant text (`no_op_marker`)

`agent.no_op_marker` promotes a run to `no_op` when "the captured transcript's final assistant text equals this marker
exactly" ([`03-job-model.md`](03-job-model.md) §3.2). Defined against a real `recent-unwrapped` transcript (HPI §9.3):

```
❯ Reply with exactly the single word HEADLESS-OK and nothing else. Do not use any tools.

● HEADLESS-OK

✻ Sautéed for 1s · done 오전 10:46
```

**A substring search is FORBIDDEN.** Look at line 1: the transcript echoes the prompt, and the prompt names the
marker, so `strings.Contains(transcript, marker)` would match the echo on every run of a job whose prompt mentions its
own marker — the normal way to write one (`"reply with exactly HEARTBEAT_OK and stop"`, the example in
[`03-job-model.md`](03-job-model.md) §1.1). The comparison MUST be exact, against a block extracted by
`func FinalAssistantText(transcript, agentKind string) string`, which returns `""` when no assistant block can be
located. Applied after trailing whitespace is trimmed: (0) **cut the agent's own chrome** — scanning backwards, the
first line that is a bare user marker with nothing after it (`❯` followed only by spaces or U+00A0) is the empty input
box, and everything from there on is UI, not conversation; (1) split into lines, drop trailing empty lines; (2) drop
trailing **status lines** — first non-space rune in `{✻, ✳, ⏵, ·, ─, ⚠}`, which in the sample
drops `✻ Sautéed for 1s · done 오전 10:46`; (3) scan backwards for the last line **beginning at column 0** with the
**assistant marker** `●` (U+25CF) — that line, minus the marker and one following space, starts the final block, which
continues forward until a blank line, a line starting with the user marker `❯` (U+276F), or another `●`; (4) with no
`●` line, fall back to the last non-empty, non-status line not starting with `❯`, else `""`; (5) trim the joined
block. For the sample this yields exactly `HEADLESS-OK`. `no_op` is assigned when the result equals
`agent.no_op_marker` **and** the run would otherwise have been `success`; a marker match never rescues a `failure`,
`timeout` or `blocked` run.

**CORRECTION, verified by implementation on 2026-09-02.** Steps 0 and 3 are stricter than originally written, and both
strictnesses are load-bearing. A real `recent-unwrapped` snapshot of claude's full-screen UI ends with a status footer
that carries **its own `●`** — `● high · /effort`, right-aligned near column 60 — so an indentation-tolerant backwards
scan returns `high · /effort` as the assistant's answer. Requiring column 0 fixes it; the input-box cut removes the
rest of the chrome. Both are covered by a test built from the captured transcript.

The glyph set is `claude`'s and verified only for `claude` — HPI §9.3 is a single-kind probe and HPI §9.6 says so. For
the other 21 kinds step 3 finds no `●` and step 4 applies; that is `[UNVERIFIED]`, and it is the documented reason
`no_op_marker` is opt-in and defaults to unset, so a job that does not set it is unaffected by extraction quality.
Where reliable capture matters more than fidelity, the escape hatch is to have the agent write its response to a file
and reply with the path, then read the file (HPI §5) — Herdr's skill calls that an interactive-use fallback, but a
scheduler that must archive output has a standing reason to use it.

---

## 5. Trust pre-flight

**This section is the contract cited by [`03-job-model.md`](03-job-model.md) §3.2 and §7 level 4.**

### 5.1 The failure it prevents

Verified, not hypothesised. `agent start` in a `cwd` the agent has never been trusted with fails in about four seconds
and leaves the agent parked on an approval dialog forever (HPI §9.4):

```
$ herdr --session hcprobe agent start probe --kind claude --pane w1:p1 --timeout 60000
{"error":{"code":"agent_not_ready",
  "message":"agent probe is blocked during startup and is not ready for prompts"},"id":"cli:agent:start"}

real	0m4.070s
```

`agent get probe` then reported `"agent_status":"blocked"` with `"launch_pending":true`, and
`agent read probe --source detection --lines 60` showed the cause verbatim:

```
 Quick safety check: Is this a project you created or one you trust? …

 ❯ No, exit
   Yes, I trust this folder

 Enter to confirm · Esc to cancel
```

With no human attached, nothing ever answers. This — not detection, not headless rendering — is the real unattended
failure mode of an agent scheduler.

### 5.2 Where the check runs

| Point | Severity | Behaviour |
| --- | --- | --- |
| `job add` / `job update` | **Warning** | Level 4 of [`03-job-model.md`](03-job-model.md) §7: printed, returned under `result.warnings`, does not block the write. A job may legitimately be authored for a repo not yet cloned. |
| `validate` | **Warning** | Same, per job, with the §5.5 remediation text. |
| Run time, §3 step 3 | **Hard gate** | `blocked` with reason `cwd_not_trusted`, before any pane is created — one file read, and nothing left behind. |

### 5.3 How trust is checked, per agent kind

Trust is the *agent's* state, not Herdr's, so the check is per `agent_kind` and inspects that agent's own
configuration. The result is three-valued — `CheckTrust(agentKind, cwd string) (TrustVerdict, error)` returning
`TrustUnknown` (no checker for this kind), `TrustTrusted` or `TrustUntrusted`; "no checker" is never an error.

**`claude`** — the mechanism used to make the probe succeed (HPI §9.4). Claude Code records per-project trust in
`~/.claude.json` under `.projects.<abs-path>.hasTrustDialogAccepted`:

```
$ jq -r '.projects | to_entries[] | select(.value.hasTrustDialogAccepted==true) | .key' ~/.claude.json
```

Re-verified on this machine on 2026-09-02: 9 trusted project paths. The check parses `~/.claude.json`
(`%UserProfile%\.claude.json` on Windows), looks up the job's resolved absolute `cwd` as a key of `.projects`, and
requires `hasTrustDialogAccepted == true`, comparing cleaned absolute paths (symlinks resolved, trailing separator
stripped), case-insensitively on Windows and macOS.

**Every other kind** — no verified mechanism. `[UNVERIFIED]`: whether the other kinds start cleanly unattended and what
their startup dialogs look like was never established (HPI §9.6). So the pre-flight returns `TrustUnknown`, which
warns at `job add`/`validate` (`trust pre-flight unavailable for kind <k>`) and does **not** block at run time —
blocking on an unimplementable check would make 21 of 22 kinds unusable. The run-time gate is armed only for kinds
with a real checker; for the rest the protection is §4's classification: `agent_not_ready` → `blocked`, never retried,
always notified — the same terminal outcome, one wasted `agent start` later.

### 5.4 What happens on failure

`TrustUntrusted` at run time: (1) no workspace, tab, pane or agent — nothing is created; (2) the run is `blocked`,
`reason: "cwd_not_trusted"`, with §5.5's text written into the run log; (3) a notification fires, since `blocked` is
in the default `notify.on` set ([`03-job-model.md`](03-job-model.md) §4.6); (4) it is **never retried** — `retry`
excludes `blocked` (§4.4 there) — and it does not nag forever either, since `blocked` increments
`consecutiveFailures` and `max_consecutive_failures` auto-disables the job after 3 occurrences (§4.5 there).

When the pre-flight was `TrustUnknown` and `agent start` then returns `agent_not_ready`, the run additionally captures
the dialog and releases the pane:

```
herdr --session herdr-cron agent read      cron-<job> --source detection --lines 60
herdr --session herdr-cron agent send-keys cron-<job> esc
herdr --session herdr-cron pane close      w1:p2
```

`agent send-keys probe esc` returned `{"type":"ok"}` and the pane fell back to its shell prompt in the probe
(HPI §9.4). The `detection` snapshot goes into the run log verbatim: it is the only thing that tells a human at 09:00
which dialog was on screen at 03:00.

### 5.5 Remediation text

Printed by `validate`, carried in `error.hint`, written to the run log:

```
herdr-cron: job "nightly-deps" cannot run unattended.
  The agent kind "claude" has never been trusted for this directory:
    /home/huke/src/herdr
  A scheduled `agent start` there returns agent_not_ready and parks the agent on
  "Quick safety check: Is this a project you created or one you trust?" — a dialog
  no scheduler can answer.

  Fix it once, interactively:
    cd /home/huke/src/herdr && claude
    answer "Yes, I trust this folder", then exit the agent.

  Verify:  herdr-cron validate
```

---

## 6. Worktrees

`agent.worktree` ([`03-job-model.md`](03-job-model.md) §3.2) is `false` (run in `cwd`) or a branch name; a branch name
gives the run its own Git worktree. The reason is not tidiness: a scheduled agent mutates a repository with no human
watching, and `cwd` is usually a checkout the human also uses, so without isolation a 03:00 job stages files into
their index, leaves a dirty tree they find at 09:00, and races any other job pointed at the same repo. Worktrees are
"normal Herdr workspaces with Git checkout provenance" (HPI §7), so isolation costs one command and stays visible in
Herdr's UI. Exact signatures `[BIN 0.8.2]`, matching HPI §7:

```
herdr worktree list   [--workspace ID | --cwd PATH]
herdr worktree create [--workspace ID | --cwd PATH] [--branch NAME] [--base REF] [--path PATH]
                      [--label TEXT] [--focus] [--no-focus]
herdr worktree open   [--workspace ID | --cwd PATH] (--path PATH | --branch NAME) [--label TEXT] [--focus] [--no-focus]
herdr worktree remove --workspace ID [--force]
```

At most one of `--workspace` / `--cwd` for `list`/`create`/`open`; exactly one of `--path` / `--branch` for `open`
(HPI §7).

**Create or reuse.** `worktree list --cwd <job.cwd>` returns `.result.worktrees[]` with `branch`, `path`,
`open_workspace_id`, `is_linked_worktree`, `is_prunable`, plus `.result.source.repo_root` (HPI §7, live sample). An
entry whose `branch` equals `agent.worktree` is **reused** — use `open_workspace_id` when set, else
`worktree open --cwd <job.cwd> --branch <b> --no-focus`. Otherwise
`herdr --session herdr-cron worktree create --cwd <job.cwd> --branch <agent.worktree> --base HEAD --label
cron/<jobId> --no-focus`, whose response fields are `.result.workspace`, `.result.tab`, `.result.root_pane` and
`.result.worktree`, with `.result.type == "worktree_created"` (HPI §5). Without `--path`, Herdr puts the checkout
under `<worktrees.directory>/<repo>/<branch-slug>`, default `~/.herdr/worktrees` (HPI §7). Reuse rather than
create-per-run is deliberate: a per-run branch accumulates one branch per night forever, and `worktree remove`
**never deletes the branch** (HPI §7), so herdr-cron would own unbounded branch GC; one stable branch per job is
bounded. `--no-focus` is mandatory, and `focus` already defaults to `false` at the socket level, which is what a
scheduler wants (HPI §7).

**Run.** `worktree create`/`open` already yield a workspace with a tab and a root pane, so the agent runs in *that*
root pane and §2.4's `tab create` is skipped; the worktree workspace replaces the host workspace for this run only.
Trust (§5) is checked against the worktree's `checkout_path`, not the job's `cwd` — a different directory, and Claude
Code trusts directories, not repositories.

**Clean up.** On `success`/`no_op`: `worktree remove --workspace <id>`, retried once with `--force` on failure. On
`failure`/`timeout`/`cancelled`: left in place, workspace left open, `workspace_id` recorded in the run record,
removed by the next successful run of the same job or by `job rm --purge`. On `blocked`: left in place (§2.5).
`--force` is REQUIRED when Git refuses a dirty checkout and MUST NOT be passed by default — a dirty worktree after a
failed agent run is evidence. `workspace close` closes only Herdr state and does not delete the checkout; only
`worktree remove` does (HPI §7), and it never takes the branch with it. The lifecycle is observable to other
plugins — `worktree.create` emits `workspace.created`, `tab.created`, `pane.created`, `worktree.created`;
`worktree.remove` emits `worktree.removed` plus `workspace.closed` when the linked workspace was open (HPI §4).
herdr-cron subscribes to none of them, but a herdr-reviewr-shaped plugin will react, which is a feature.

---

## 7. Notifications

Decision D8: every run **always** writes a log file and a JSONL record ([`04-storage.md`](04-storage.md) §5, §6);
notification is a layer on top, never a substitute. `notify.command` ([`03-job-model.md`](03-job-model.md) §4.6)
defaults to the Herdr notifier, `herdr --session <session> notification show "<job name>" --body "<summary>" --sound
done`. Full surface `[BIN 0.8.2]`, matching HPI §6:

```
herdr notification show [OPTIONS] <TITLE>
      --body <TEXT>
      --position <POSITION>   [top-left, top-right, bottom-left, bottom-right]
      --sound <SOUND>         [none, done, request]
```

The title is **positional** and the body is `--body`; `--message` is not a flag (HPI §9.5), and only `title` is
required (HPI §6). Constraints herdr-cron MUST respect (all HPI §6): `title` is trimmed to **80 characters**, `body`
to **240**, with newlines, tabs, CRs and repeated whitespace collapsed to single spaces; an empty sanitized title
returns `invalid_params`, so herdr-cron falls back to the job id when the job `name` sanitizes to nothing; `position`
applies only when `ui.toast.delivery = "herdr"` and is ignored otherwise, so herdr-cron does not set it; `sound`
defaults to `none`, and herdr-cron uses `done` for `success`/`no_op` and `request` for `blocked` — the existing
finished / needs-attention sounds, which play only when the notification is shown. Adding `--sound` refines
[`03-job-model.md`](03-job-model.md) §4.6, which quotes the default as `--body`-only; the flag is additive and
changes nothing else. 80/240 makes the toast a pointer, not a report, so the composition is fixed:
`title: "<job name>: <status>"`, `body: "<first line of outputExcerpt> — herdr-cron run <runId>"`. The `runId` is
what makes `herdr-cron run logs <runId>` the next action.

**Best-effort, verified.** On a headless server the notifier does not deliver, and that is the *normal* case for a
scheduler (HPI §9.5, reproduced rather than assumed):

```
$ herdr --session hcprobe notification show "headless probe" --body "from hcprobe"
{"id":"cli:notification:show","result":{"reason":"no_foreground_client","shown":false,"type":"notification_show"}}
```

`reason` ∈ `shown | disabled | rate_limited | no_foreground_client | busy` (HPI §6). Rules: notification failure
**never** changes a run's outcome — a notifier that exits non-zero, is missing, or returns `shown: false` is
warn-logged and nothing else (§4.6 there); `shown: false` MUST NOT be retried, since `no_foreground_client`,
`rate_limited` and `busy` are all non-exceptional (HPI §6); the `reason` MUST be recorded in the run log
(`notify: shown=false reason=no_foreground_client`) so "I never got told" is answerable; herdr-cron MUST NEVER treat
"notified" as "reported"; and a custom `notify.command` gets §1.2's PATH-prepending rule and a hard 10-second
deadline — `["herdr-hitl", "notify", …]` is the interesting case precisely because it delivers out of band and
therefore works with no client attached.

---

## 8. The plugin front door

Decision D4: one Go binary, two front doors. The manifest below is complete and valid, modelled on `herdr-hitl` — Go,
GUI-free, `contexts = ["global"]` throughout, an `install-cli` action, and a background daemon started from a startup
hook (HPI §8): structurally herdr-cron's problem exactly.

```toml
# Herdr plugin manifest for herdr-cron. User-editable config does NOT live here: HERDR_PLUGIN_ROOT is a
# managed checkout that `herdr plugin install` replaces wholesale. jobs.yaml and all state live at the
# machine-wide roots of docs/spec/04-storage.md §1, which are already outside HERDR_PLUGIN_ROOT.
# HERDR_PLUGIN_CONFIG_DIR and HERDR_PLUGIN_STATE_DIR are deliberately NOT used as roots: a state root
# that varies by launcher gave the [[startup]] daemon its own daemon.lock and ran every job twice.
id = "huketo.cron"
name = "herdr-cron"
version = "0.1.0"

# The version the whole headless execution path was verified against (§1.3). Herdr refuses to link a
# plugin whose min_herdr_version exceeds the running binary: a hard gate, not documentation.
min_herdr_version = "0.8.2"

platforms = ["linux", "macos", "windows"]
description = "Schedule shell commands and coding-agent prompts, and inspect their runs."

# Runs on `herdr plugin install` only, not on `plugin link`. Build commands get no runtime or socket env,
# so this must not call back into herdr. Needs the Go toolchain on PATH.
[[build]]
command = ["go", "build", "-trimpath", "-ldflags", "-s -w", "-o", "bin/herdr-cron", "./cmd/herdr-cron"]

# One-shot hook: ensure the daemon is up, then exit — a startup hook is an initialisation command, never a
# supervised daemon, and it re-runs on every server start and live handoff, so it must be idempotent.
# `daemon --detach` re-execs with detached stdio, waits for a fresh heartbeat in <state>/daemon.json, and
# exits 0, including as a no-op when daemon.lock is already held (docs/spec/02-architecture.md §3.1).
[[startup]]
command = ["bin/herdr-cron", "daemon", "--detach"]

# Actions run with the plugin directory as cwd, so relative "bin/herdr-cron" resolves.
[[actions]]
id = "list"
title = "herdr-cron: list jobs"
description = "Print every scheduled job with its next fire time and last run."
contexts = ["global"]
command = ["bin/herdr-cron", "job", "list", "-o", "text"]

[[actions]]
id = "status"
title = "herdr-cron: scheduler status"
description = "Daemon liveness, driver, roots, config errors, next three occurrences."
contexts = ["global"]
command = ["bin/herdr-cron", "status", "-o", "text"]

[[actions]]
id = "reload"
title = "herdr-cron: reload jobs.yaml"
description = "Ask the daemon to re-read jobs.yaml now instead of waiting for the watcher."
contexts = ["global"]
command = ["bin/herdr-cron", "reload"]

[[actions]]
id = "install-cli"
title = "herdr-cron: install on PATH"
description = "Symlink the plugin binary into a PATH directory so agents can call it directly."
contexts = ["global"]
command = ["bin/herdr-cron", "install-cli"]

# The mouse-driven TUI (docs/spec/06-tui.md). A pane command's cwd is the PANE's cwd, not the plugin root,
# so the binary is invoked by absolute path under $HERDR_PLUGIN_ROOT — relative "bin/herdr-cron" fails
# here even though it works in [[actions]] — and pane commands get no PATHEXT resolution on Windows.
[[panes]]
id = "tui"
title = "herdr-cron"
placement = "overlay"
command = ["sh", "-c", "exec \"$HERDR_PLUGIN_ROOT/bin/herdr-cron\""]

# No [[events]]: herdr-cron schedules by wall-clock time, which no Herdr event announces, and it learns of
# agent completion from its own server-owned `agent prompt --wait`. No [[link_handlers]]: nothing it prints
# is a clickable URL.
```

Documented versus merely observed, because the difference decides what may be relied on:

- **Documented** (`herdr.dev/docs/plugins/`, HPI §1–§2): file name and root location; required `id`, `name`,
  `version`, `min_herdr_version`; optional `description`, `platforms`; all six section kinds; `platforms` on every
  section with item-level override; `command` as an argv array run without a shell; runtime commands using the plugin
  directory as cwd **except panes**; build commands receiving no runtime or socket env; startup hooks being one-shot
  and re-running on live handoff; Windows PATHEXT resolution for build/action/event but **not** pane commands.
- **Observed, not documented**: `contexts = ["global"]` is absent from the plugins docs page but is in the binary's
  schema enum and used by two installed plugins (HPI §1); a `description` on an individual `[[actions]]` entry is used
  by `herdr-hitl` (HPI §8) but never documented — decorative, and harmless if ignored.
- **Narrower than documented**: `plugin pane open --placement` accepts exactly `overlay, split, tab, zoomed` on 0.8.2
  `[BIN 0.8.2]` while the docs also describe a `popup` placement; whether a manifest may declare
  `placement = "popup"` when the CLI flag rejects it is `[UNVERIFIED]`, so `overlay` stays inside the verified set.
- **`plugin list --json` names differ from the docs' prose**: `plugin_id` and `plugin_root`, not `id`/`root`, and
  `events`, `panes`, `link_handlers`, `config_dir`, `state_dir` and `warnings` are absent when empty (HPI §2) — the
  installer MUST NOT assume they exist. It MUST, however, read `herdr plugin list --plugin huketo.cron --json` back
  after install and print any `warnings`: Herdr validates `[[events]] on =` values softly, so a typo'd hook silently
  never fires and only adds a warning (HPI §4).
- **Lifecycle**: update is uninstall + install (there is no `plugin update`) and the root is replaced wholesale, but
  nothing durable lives under the root — the config and state roots of [`04-storage.md`](04-storage.md) §1 are
  machine-wide and launcher-independent, so reinstalling preserves every job and run. Plugin scope is per-user and
  global, not per-session (HPI §1), and there is exactly one daemon per user
  ([`04-storage.md`](04-storage.md) §7) — so the hook firing once per session is harmless: every invocation after the
  first takes the no-op branch of `daemon --detach`. Marketplace listing is automatic for a public repo tagged
  `herdr-plugin` with a parseable manifest, refreshed every 30 minutes (HPI §1).

The standalone front door (`go install`, goreleaser, brew, scoop) is [`02-architecture.md`](02-architecture.md)'s and
needs nothing from here except §1.2's resolution order — which is what lets the same binary find `herdr` with no
plugin env at all.

---

## 9. Degradation

| Feature | No `herdr` binary | Server not running | Version < 0.8.2 |
| --- | --- | --- | --- |
| `kind: shell` jobs | **Fully functional** — a direct child process ([`03-job-model.md`](03-job-model.md) §3.1) | Fully functional | Fully functional |
| `kind: agent` jobs | `failure` / `herdr_unavailable` | Server started (§2.3), then normal; on startup failure `failure` / `herdr_unavailable` | `failure` / `herdr_version_unsupported` |
| Trust pre-flight | Runs anyway — it reads the agent's own config, not Herdr (§5.3) | Runs anyway | Runs anyway |
| Default `notify.command` | Skipped, warn-logged, outcome unchanged | Attempted, returns `shown: false`, outcome unchanged (§7) | Attempted |
| Custom `notify.command` | Runs — an ordinary subprocess | Runs | Runs |
| Worktrees | Unavailable: a job with `agent.worktree` set is `failure` / `herdr_unavailable`; herdr-cron does NOT fall back to raw `git worktree` | Server started, then normal | `failure` / `herdr_version_unsupported` |
| `job list/get/add/update/rm/pause/resume`, `run list/get/logs`, `status`, `validate`, `schema`, `completion` | **All work** — pure file operations ([`05-cli.md`](05-cli.md) §4) | All work | All work |
| `run-once` on a `shell` job | Works | Works | Works |
| `daemon`, `service install` | Work; only agent jobs degrade | Work | Work |
| TUI | Works as a plain terminal program; Herdr-specific columns render `—` | Works | Works |
| `validate` level 4 | Warning: `herdr not found on PATH` | Warning: `herdr server not running` | Warning naming both versions |
| Plugin front door | N/A — no Herdr, no plugin | `plugin install` / `plugin link` work with no server running (HPI §5) | Herdr **refuses to link** (HPI §1); the standalone CLI still installs and runs |

1. **`kind: shell` MUST be fully functional with no Herdr at all.** A shell job is a child process, never a Herdr
   pane, precisely so herdr-cron is a usable scheduler on a machine that has never heard of Herdr:
   `herdr-cron job add --command … && herdr-cron run-once <id>` MUST work there.
2. **Degradation is loud and typed, never silent.** Every row produces a specific `reason` and a specific
   `error.code` from [`05-cli.md`](05-cli.md) §2.1. herdr-cron MUST NOT downgrade an agent job to a shell job, MUST
   NOT substitute raw `git worktree`, and MUST NOT report `success` for anything it did not observe.
3. **Availability failure is retried; version failure is not.** A missing binary or stopped server can fix itself
   before the next tick; a too-old Herdr will not, so it goes straight to the `max_consecutive_failures` circuit
   breaker ([`03-job-model.md`](03-job-model.md) §4.5), which auto-disables and notifies once instead of failing
   nightly forever.

---

## 10. Open points

**Unverified, and load-bearing here:**

1. **What bare `herdr server` does to a controlling terminal** — fork, exit behaviour, log destination (HPI §9.6).
   §2.3 specifies the only shape with evidence; a server that daemonises or dies with its parent changes §2.3's
   implementation, not its contract.
2. **Non-`claude` agent kinds, unattended** — clean startup, startup dialogs, transcript decoration (HPI §9.6). §5.3
   returns `TrustUnknown` for every other kind and §4.2's extraction falls back to "last non-empty line"; both
   degrade to §4's `agent_not_ready` → `blocked` net, not to a wrong answer.
3. **Whether `default_known_agent_idle_fallback` appears in `agent explain --json`'s `.fallback_reason`** — field
   verified `[BIN 0.8.2]`, label documented (HPI §5), never observed together; §4.1 mandates a literal-substring
   fallback because of this.
4. **macOS and Windows, empirically** (HPI §9.6). `HERDR_PLUGIN_STATE_DIR`'s macOS and Windows spellings are
   unverified — HPI §1 says ask `herdr plugin config-dir <id>`, never hardcode — and headless `agent start` on either
   platform is untested.
5. **A mouse-driven Bubble Tea TUI inside a Herdr plugin pane** — whether Herdr's own mouse handling conflicts with a
   child program's mouse reporting was never tested (HPI "Could not verify"). §8's `[[panes]]` entry assumes it
   works; the risk is [`06-tui.md`](06-tui.md)'s.
6. **`placement = "popup"` in a manifest** — rejected by the CLI flag on 0.8.2 `[BIN 0.8.2]` while documented in
   prose; §8 uses `overlay`.
7. **Exit-code semantics for action and event-hook commands** — only build-command failure has documented
   consequences (HPI §2). §8's actions exit non-zero with one stderr line, assuming nothing but the plugin log
   consumes it.
8. **Behaviour across a server restart, a live handoff, or machine suspend** (HPI §9.6). §2.5's reconciliation sweep
   is designed for it but was not tested against it.
9. **`herdr` error codes are open-ended** — no enum, and the list is assembled from prose across four sources and is
   "certainly incomplete" (HPI §5). §4's table is total by fallback: an unlisted code is `failure` /
   `herdr_unexpected` with the raw envelope logged.

**Discrepancies with the normative siblings:**

10. **`herdr-cron daemon --detach` is not in [`05-cli.md`](05-cli.md) §3.3**, which lists
    `herdr-cron daemon [--foreground]`. §8's `[[startup]]` hook requires a spawn-and-exit form, because a Herdr
    startup hook "should restore plugin-owned state, call any required Herdr APIs, and exit" and is not a supervised
    daemon (HPI §8). Semantics are specified in [`02-architecture.md`](02-architecture.md) §3.1 and agreed with that
    document's author; `05-cli.md` §3.3 should absorb the flag.
11. **`herdr-cron install-cli` is not in [`05-cli.md`](05-cli.md) §3.** §8 declares it as an action because it is what
    turns a plugin install into a standalone CLI — the `herdr-hitl` precedent decision D4 names (HPI §8). It belongs
    in `05-cli.md` §3.4 beside `service install`.
12. **Failure `reason` values are defined here, not centrally.** [`03-job-model.md`](03-job-model.md) §6 enumerates
    `reason` only for `skipped`. This document introduces, for non-`skipped` statuses: `agent_blocked`,
    `agent_startup_dialog`, `agent_prompt_stalled`, `agent_unknown`, `agent_idle_fallback_unverified`,
    `agent_vanished`, `agent_name_collision`, `pane_lost`, `wait_timeout`, `job_timeout`, `cwd_not_trusted`,
    `herdr_unavailable`, `herdr_version_unsupported`, `herdr_unexpected`. Consistent with §6's example
    (`"reason": null` when there is nothing to say), but the union should eventually live in `03-job-model.md` §6.
