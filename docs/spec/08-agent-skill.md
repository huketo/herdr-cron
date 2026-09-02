---
title: herdr-cron — Agent Skill
date: 2026-09-02
status: spec (normative)
---

# Agent Skill

Normative. RFC 2119 keywords.

herdr-cron ships an Agent Skill. The skill is the third front door alongside the Herdr plugin
and the standalone CLI ([`README.md`](README.md) D4): it is what makes a coding agent able to
schedule work correctly on the first attempt instead of discovering the surface by trial.

Every claim traces to `docs/research/2026-09-02-agent-skill-and-cli-ux.md` Part A, which is
primary-source and pinned. The surface the skill teaches is [`05-cli.md`](05-cli.md); the
vocabulary it must use is [`03-job-model.md`](03-job-model.md).

---

## 1. Why a skill and not just `--help`

`herdr-cron --help` is available to any agent that already knows the binary exists. The skill
solves a different problem — **being loaded before the agent knows anything** — at a cost small
enough to pay in every session. The cost model is progressive disclosure, and the numbers are
first-party (research §A4, quoting `[API-SKILLS]`, corroborated by `[AS-SPEC]`):

| Level | When loaded | Token cost | Content |
| --- | --- | --- | --- |
| 1 — metadata | always, at startup | ~100 tokens per skill | `name` + `description` |
| 2 — instructions | when the skill triggers | under 5k tokens | the `SKILL.md` body |
| 3 — resources | when read | none until accessed | bundled files; script output only |

Three consequences, and they are the whole rationale. **Only the description competes for
attention up front**: ~100 tokens buys a permanent standing answer to "can I schedule this?",
where `--help` buys nothing until someone has already decided to run it. **The body is free
until it fires** — `[API-SKILLS]`: "No practical limit on bundled content: Files don't consume
context until accessed" — which is the licence to put the long material in `references/` instead
of cramming it into the body. **Command output is cheaper than reference prose**: only a
command's output enters context, which is why the skill routes to `herdr-cron schema` and
`validate --schedule` instead of reproducing flag tables — the authoritative answer costs one
line of output and cannot be stale.

One further lifecycle fact decides the *writing style*. Per `[CC-SKILLS]` (research §A4),
rendered `SKILL.md` content "enters the conversation as a single message and stays there across
later turns... Claude Code does not re-read the skill file on later turns, so write guidance
that should apply throughout a task as **standing instructions rather than one-time steps**."
The skill MUST therefore be written as present-tense invariants ("Read `result`, never the text
rendering") and MUST NOT be a numbered procedure ("Step 4: now add the job"): a procedure read
once and forgotten produces an agent that validates the first schedule of a session and none of
the next five. Size caps, from both `[AS-SPEC]` and `[CC-SKILLS]` (research §A3) and normative
here: `SKILL.md` under **500 lines**, references **one level deep** — "Avoid deeply nested
reference chains".

---

## 2. Repo layout

The canonical skill directory is `skills/herdr-cron/`.

```
herdr-cron/
├── cmd/herdr-cron/
├── skills/
│   ├── embed.go                    package skills — the //go:embed directives
│   └── herdr-cron/
│       ├── SKILL.md                the shipped skill (§8)
│       └── references/
│           ├── job-schema.md
│           ├── json-shapes.md
│           └── troubleshooting.md
└── docs/spec/
```

Each rule below is forced by `discoverSkills()` in `[SKILLS-CLI]` (research §A6, §A7):

- `skills/` is second in `discoverSkills()`'s priority list, and `skills/<name>/SKILL.md` is
  exactly the layout `[SKILLS-CLI]` uses for its own skill (`skills/find-skills/SKILL.md`).
- **A `SKILL.md` at the repo root is PROHIBITED.** `discoverSkills()` takes it and returns
  early, hiding everything under `skills/` unless the user passes `--full-depth` (research §A6) —
  so a repo-root `SKILL.md` would silently make `npx skills add <owner>/herdr-cron` install the
  wrong thing.
- The directory name MUST equal the frontmatter `name`: `[AS-SPEC]` requires "Must match the
  parent directory name", and in Claude Code the directory name — not `name` — determines the
  slash-command (research §A7).
- `references/` MUST be exactly one level below `SKILL.md` (research §A3).
- `skills/embed.go` sits beside the skill directory, not inside it: `go:embed` patterns cannot
  escape their own source directory, so the package must be a parent of what it embeds, and
  keeping it out of `skills/herdr-cron/` keeps Go files off every user's machine.

### 2.1 The embed

```go
// Package skills carries the shipped Agent Skill, embedded so that skill and binary cannot skew.
package skills

import "embed"

//go:embed herdr-cron/SKILL.md
var SkillMD string

//go:embed herdr-cron/references
var References embed.FS
```

`herdr-cron --skill` ([`05-cli.md`](05-cli.md) §1) MUST be implemented as a verbatim copy of
that string to stdout, with no templating, no version substitution, and no trailing-newline
normalisation:

```go
func printSkill(w io.Writer) error {
	_, err := io.WriteString(w, skills.SkillMD)
	return err
}
```

This reproduces the property verified for `herdr` on this machine: `herdr --skill > f` then
`diff -q f ~/.claude/skills/herdr/SKILL.md` reports the files identical, 195 lines, exit 0
(research §A7). The directive "guarantees the shipped skill can never be a different version
from the shipped binary... a genuinely better property than a separately-versioned skill
package". `embed` skips names beginning `.` or `_`, so `references/` MUST NOT contain such
files; if that changes the pattern MUST become `all:herdr-cron/references`.

### 2.2 The CI check

Byte identity holds by construction inside one build, but three drift paths remain: a `--skill`
handler that grows a template, a `references/` file that no longer exists, and frontmatter
edited into a state the installer rejects. CI MUST fail on all three. A shell gate, which MUST
run on every push and pull request:

```bash
go build -o "$TMPDIR/herdr-cron" ./cmd/herdr-cron
"$TMPDIR/herdr-cron" --skill > "$TMPDIR/skill-embedded.md"
diff -u skills/herdr-cron/SKILL.md "$TMPDIR/skill-embedded.md"
```

`diff` exiting non-zero fails the build — the exact check the research ran against `herdr`
(research §A7), promoted from an observation to a build gate. Plus a Go test, REQUIRED, in
`skills/embed_test.go`, with these assertions (the last two are the pair that matters most in
practice: an unnamed reference file is dead weight, and a renamed one is a failed `cat` in front
of the user):

| Assertion | Source of the rule |
| --- | --- |
| `skills.SkillMD` equals the on-disk `skills/herdr-cron/SKILL.md` byte for byte | §2.1 |
| The file's first byte begins the `---` frontmatter fence: no BOM, no leading blank line | research §A2, `[CC-SKILLS]`: otherwise "it treats the whole file, `---` markers included, as skill content" |
| Frontmatter keys ⊆ {`name`, `description`, `license`, `compatibility`, `metadata`, `allowed-tools`} | research §A1: claude.ai and the Skills API reject anything else with a hard error |
| `name` and `description` are both present, both YAML strings, and `len(description) <= 1024` | research §A1: `[SKILLS-CLI]` `parseSkillMd()` skips a skill missing either and rejects non-string values; §A2: the validation cap |
| `name` matches `^[a-z0-9-]+$`, 1–64 chars, no leading/trailing hyphen, no `--`, and equals the parent directory name | research §A2, `isValidSkillName()`; §A7 |
| `SKILL.md` body is at most 500 lines | research §A3 |
| Every `[...](...)` relative link in `SKILL.md` resolves to a file that exists in `References` | research §A3 |
| Every file in `References` is named at least once by `SKILL.md` | research §A3, §5 below |

---

## 3. The frontmatter

Shipped verbatim:

```yaml
---
name: herdr-cron
description: "Schedule and inspect automated work with the herdr-cron CLI: cron jobs, recurring shell commands, and scheduled coding-agent prompts. Use when the user asks to schedule, automate, or run something nightly, hourly, or on a cron; to list, add, edit, pause, or delete scheduled jobs; to check why a scheduled job did not run; or to read a job's run history or logs. Requires the herdr-cron binary on PATH."
allowed-tools: Bash
license: MIT
---
```

Five properties, all inside the six-field spec set. `metadata` is deliberately unused; see §7.

**Why only spec fields.** `[CC-SKILLS]` quotes the hard error the Skills API and claude.ai raise
for anything else — `Unexpected key(s) in SKILL.md frontmatter: argument-hint. Allowed
properties are: allowed-tools, compatibility, description, license, metadata, name` — while
Claude Code itself tolerates about fourteen extensions (research §A1). A skill bundled with a
public CLI is installed into harnesses its author never tested, so the fourteen are PROHIBITED.
`disable-model-invocation: true` especially: per the `[CC-SKILLS]` invocation table it removes
the description from context entirely (research §A2), the one thing this skill exists to place.

**Why both `name` and `description`.** The sources disagree — `[AS-SPEC]` marks `name` required,
`[CC-SKILLS]` says "All fields are optional. Only `description` is recommended", `[SKILLS-CLI]`'s
`parseSkillMd()` skips any skill missing either (research §A1). The research's resolution is
binding: **always write both.** Claude Code tolerates absence; the installer skips the skill.

**Why `allowed-tools: Bash`.** The skill's whole mechanism is shelling out to one binary, and
the CLI-driving skill on this machine (`lsoffice-cli`) declares the same (research §A5). Per
`[CC-SKILLS]` the field pre-approves tools for the invoking turn only and "does not restrict
tools" (research §A2): a convenience, not a sandbox, named here so a reviewer sees it.

### 3.1 The description, justified against the budgets

Two different numbers, not interchangeable (research §A2). **1024 characters** is the
`[AS-SPEC]` / `[API-SKILLS]` validation cap, independently enforced by `[SKILLS-CLI]`'s registry
validator: exceed it and the skill is rejected. **1536 characters** is `[CC-SKILLS]`'s listing
truncation for combined `description` + `when_to_use` — a display limit inside one harness, on
top of a *dynamic* budget: "The budget scales at 1% of the model's context window. When the
listing overflows, Claude Code drops descriptions starting with the skills you invoke least."
The shipped description is **402 characters, 68 words**: 39% of the 1024 validation cap and 26%
of the 1536 listing budget. On the machine this was designed against, 39 skills are installed
(research §A5), so the headroom is not decorative. The research calls keyword ordering "the most
actionable line in Part A" — **put the trigger keywords first**, because a description whose
activation keywords sit in the last sentence can lose them to truncation, "at which point the
skill exists but never fires" (research §A2). The draft is ordered accordingly:

| Offsets | Text | Why it is there, and why in that position |
| --- | --- | --- |
| 0–133 | `Schedule and inspect automated work with the herdr-cron CLI: cron jobs, recurring shell commands, and scheduled coding-agent prompts.` | What it does, first, per the `[AS-SPEC]` good/poor example pair. It front-loads five high-value match terms — *schedule* (offset 0), *inspect*, *cron* (offset 51), *shell commands*, *coding-agent prompts* — inside the first sentence, so the survivable prefix alone can fire the skill. Names the binary once, because an agent that has seen `herdr-cron` in a repo needs the string to match. |
| 134–361 | `Use when the user asks to schedule, automate, or run something nightly, hourly, or on a cron; to list, add, edit, pause, or delete scheduled jobs; to check why a scheduled job did not run; or to read a job's run history or logs.` | The `Use when ...` clause with concrete trigger phrases: the exact shape of all three descriptions read off this machine (research §A5), against which `[AS-SPEC]` labels `description: Helps with PDFs.` a "Poor example". Four trigger clusters in falling order of likelihood: create (*nightly*, *hourly*, *cron*), manage (*list/add/edit/pause/delete*), diagnose (*did not run*, offset 342), read (*run history*, *logs*). Diagnosis is included because "why didn't my job run" is a request an agent would otherwise answer by guessing at cron syntax. |
| 363–402 | `Requires the herdr-cron binary on PATH.` | A precondition, last on purpose: it is the one clause that can be truncated without changing when the skill fires, and the body re-checks it anyway (§4). |

Rules the draft obeys, each from a cited constraint:

- No XML tags in `name` or `description`; `[API-SKILLS]` forbids them in both (research §A2).
- `name` contains neither `anthropic` nor `claude`, both reserved by `[API-SKILLS]`
  (research §A2). That is why the skill is not named `claude-cron`.
- The value is quoted: the description contains a colon after `CLI`, which unquoted YAML would
  parse as a mapping, and `[SKILLS-CLI]` rejects a non-string `description` (research §A1) — so
  a YAML accident here is a silent non-install.
- `compatibility` (max 500 chars) is left unset: the PATH requirement is already in the
  description, and the Herdr requirement applies to `kind: agent` jobs, not to the skill.

---

## 4. SKILL.md content plan

Section by section. The complete draft is §8; this section states what each part is *for* and
which rule forces it. **Opening, drafted, and the real text:**

> herdr-cron schedules two kinds of work: `shell` jobs, which run a command in a child process,
> and `agent` jobs, which deliver a prompt to a coding agent running in a Herdr pane. Job
> definitions live in `jobs.yaml`, run history lives in JSONL files, and everything is driven
> through the `herdr-cron` CLI. There is no API and no socket.

Then, in order. Each row is one section of the body; the last column is what MUST survive:

| # | Body section | Content and rule | Forced by |
| --- | --- | --- | --- |
| 1 | Preconditions | `command -v herdr-cron`, then `herdr-cron status`. Both cheap, and `status` never needs a daemon. | [`05-cli.md`](05-cli.md) §3.3 |
| 2 | Is a daemon needed for this? | A condensed copy of the daemon table: reads, `job add/update/rm`, `job pause/resume`, `validate`, `schema` and `run-once` need none; `job run`, `job cancel` and `reload` do, and the substitute is `run-once`. MUST be a table, not prose — it is consulted repeatedly across a task, and standing instructions have to be re-readable in place (§1). It prevents the one failure an agent meets on a fresh machine: `daemon_unreachable` on `job run`. | [`05-cli.md`](05-cli.md) §4 |
| 3 | The binary is the authority | `herdr-cron --help`, `herdr-cron schema`, `herdr-cron schema --command "job add"`, plus the rule that bare `herdr-cron` launches the TUI and MUST NOT be used for discovery. Copied from the `herdr` skill's "Learn the current CLI" section, which the research names as the pattern: "the skill is a *router and a set of invariants*, not a frozen copy of the help text that will drift out of date with the binary". | research §A4; [`05-cli.md`](05-cli.md) §1, §3.5 |
| 4 | JSON only | `-o json` is the default; read `.result`, branch on `.error.code`, never on `.error.message`, never parse `-o text`. Names the single documented exception: `run logs` emits raw text. | [`05-cli.md`](05-cli.md) §5 rules 1–2, §3.2 |
| 5 | `validate --schedule` before every write | With `--next 5`, and read the printed instants back. A mis-typed cron expression is otherwise a job that silently never fires. | [`05-cli.md`](05-cli.md) §5 rule 4, §3.5 |
| 6 | Exit codes | 0 / 1 / 2 / 3, and **exit 3 means stop and ask a human**. MUST say why retrying is wrong: exit 3 is `blocked` — an agent on an approval dialog with nobody present, a terminal outcome that is never retried. | [`05-cli.md`](05-cli.md) §2.2, §5 rule 5; [`03-job-model.md`](03-job-model.md) §6; `herdr-plugin-integration` §9.4 |
| 7 | Adding a job | Two worked command lines, one `shell` and one `agent`, with real flags and real vocabulary. | [`05-cli.md`](05-cli.md) §3.1; [`03-job-model.md`](03-job-model.md) §1.2 |
| 8 | Agent jobs need more care | The `herdr-cron` session default, the prepended scheduler preamble, `no_op_marker`, `herdr_unavailable`, and the trust pre-flight: an agent started in an untrusted `cwd` returns `agent_not_ready` and waits on a trust dialog forever. | [`README.md`](README.md) D7; [`03-job-model.md`](03-job-model.md) §3.2, §3.3; `herdr-plugin-integration` §9.4 |
| 9 | Safety rules that always apply | §4.1, as standing instructions. | [`README.md`](README.md) D3 |
| 10 | Diagnosing a job that did not run | `status.configError`, `job get`'s `enabledSource`, `run list --status all`, the five `skipped` reasons, `run logs`. | [`03-job-model.md`](03-job-model.md) §6; [`04-storage.md`](04-storage.md) §7 |
| 11 | Reference files | One relative link per file in `references/`, or the agent never reads them. | research §A3; §5 |

### 4.1 The safety invariants the skill MUST state

[`README.md`](README.md) D3 and [`03-job-model.md`](03-job-model.md) §4.5 as standing rules, in
the body rather than a reference file because they must hold for the *whole* task.

| Invariant | Ground |
| --- | --- |
| Never set `max_consecutive_failures: 0`. Auto-disable after 3 consecutive `failure`/`timeout`/`blocked` outcomes is a mandatory guardrail; `0` disables it. | `03-job-model.md` §4.5 |
| Keep `max_runs_per_day` at or below 24 for `kind: agent`. An agent run costs money; a shell run does not. | `03-job-model.md` §4.5 |
| Leave `jitter: auto`. It is a safety feature, not a nicety: six agent jobs at `0 9 * * *` would otherwise launch six agents into the same repo in the same second. | `03-job-model.md` §2.1 |
| Leave `catchup: latest` unless the user asks otherwise, and never pair `catchup: all` with a long `catchup_window` on an agent job. | `03-job-model.md` §4.1 |
| Do not raise `retry.max_attempts` for an agent job to work around `blocked`. Retrying burns the daily limit and changes nothing. | `03-job-model.md` §4.4 |
| Never invent a job id; take ids from `job list`. Renaming an id orphans its history. | `05-cli.md` §5 rule 3, `03-job-model.md` §1.2 |
| Never delete a job you did not create, and never pass `--purge` unless the user asked to destroy history. | `05-cli.md` §3.1 |
| Prefer `job pause` to `job rm`. Pausing writes a state override and leaves the user's authored YAML untouched. | `03-job-model.md` §5 |

---

## 5. Bundled references

Three files, all in `references/`, all one level deep (research §A3). The governing rule is the
one the CI check enforces: **a reference file must be named from `SKILL.md` or the agent will
never read it** (research §A3, which shows the "Additional resources" link block both
`[AS-SPEC]` and `[CC-SKILLS]` prescribe). `[LOCAL-SKILLS]` `lsoffice-cli/` is the precedent.

| File | Contents | Length | Read when |
| --- | --- | --- | --- |
| `references/job-schema.md` | The `jobs.yaml` schema: the full field reference of [`03-job-model.md`](03-job-model.md) §1.2, both schedule forms and their extra fields (§2), the two kind payloads (§3), and the `defaults` block. Every enum member spelled out. | 150–250 lines | Hand-editing `jobs.yaml`, or reviewing a file a human wrote. |
| `references/json-shapes.md` | The response envelope, the resolved job JSON of [`03-job-model.md`](03-job-model.md) §1.3, the `job list` payload, the run record of §6, the full `error.code` table of [`05-cli.md`](05-cli.md) §2.1, and one `jq` expression per common question. | 120–200 lines | Parsing a response shape not shown in the body, or mapping an unfamiliar `error.code`. |
| `references/troubleshooting.md` | Symptom → cause → command. `daemon_unreachable`; a job that never fired; a `skipped` run and each of its five reasons; `blocked` and the trust pre-flight; `herdr_unavailable`; `cwd_missing`; an auto-disabled job and how `job resume` clears it; a rejected `jobs.yaml` reload and `status.configError`. | 120–200 lines | Anything went wrong. |

Rules:

- These three are the complete set. A fourth REQUIRES a link to it in `SKILL.md` in the same
  commit; the CI check fails otherwise (§2.2).
- No `scripts/` directory in v1: a script would only wrap the CLI, which is already the thing
  the agent should call — a second surface to sync with [`05-cli.md`](05-cli.md) for no gain.
- Reference files MUST NOT restate flags; flags come from `schema` and `--help` (§7). They carry
  *shapes and diagnoses*, and they link onward to nothing: depth stops here (research §A3).

---

## 6. Distribution

Three installation paths. All three MUST land the same content.

### 6.1 `skills add` — the primary path

Every form below is valid per `parseSource()` in `[SKILLS-CLI]` (research §A6, §A7):

```bash
npx skills add <owner>/herdr-cron                 # GitHub shorthand, default branch
npx skills add <owner>/herdr-cron -g -y           # global (user-level), no prompts
npx skills add <owner>/herdr-cron#v0.3.0          # pinned to a tag
npx skills add https://github.com/<owner>/herdr-cron/tree/main/skills/herdr-cron
npx skills add ./skills/herdr-cron                # local path, for development
```

`skills add` **defaults to project scope**, not global
(`const scope = options.global === true ? true : false;`, research §A6). A user who wants the
skill everywhere MUST pass `-g`; documentation that omits it produces a skill installed into one
repo and mysteriously absent from the next.

The on-disk result, verified on this machine (research §A5): content lands in
`~/.agents/skills/herdr-cron/` and each agent's own skills directory gets a **relative symlink**
to it — `~/.claude/skills/herdr-cron -> ../../.agents/skills/herdr-cron`, the same shape as the
39 existing entries there, all symlinks, including `herdr -> ../../.agents/skills/herdr`.
`[CC-SKILLS]` confirms this is supported and deduplicated: "Claude Code follows the symlink and
reads `SKILL.md` from the target directory... loads the skill once". Provenance is recorded in
`~/.agents/.skill-lock.json` (version 3): `source`, `sourceUrl`, `skillPath`, `skillFolderHash`.

**On Windows**, `[SKILLS-CLI]` creates a **junction** instead, because unprivileged symlink
creation there is unreliable — `const symlinkType = platform() === 'win32' ? 'junction' : undefined;`
— and a junction takes the *resolved absolute* target rather than the relative one. A `--copy`
flag and an automatic copy fallback cover the case where linking fails outright (research §A5).
Consequence: on Windows the installed skill MAY be a copy, so nothing may assume its own path
links back into the repo, and `herdr-cron --skill` remains the way to refresh it.

### 6.2 The Herdr plugin front door

The plugin ships the same directory. Per the `[CC-SKILLS]` locations table (research §A5) a
plugin skill lives at `<plugin>/skills/<skill-name>/SKILL.md`, already the layout of §2, so the
plugin package needs no rearrangement. Plugin skills are namespaced `plugin-name:skill-name` and
therefore **cannot collide** with a personal or project install of the same skill; precedence
otherwise is enterprise > personal > project > bundled. Installing herdr-cron as a Herdr plugin
delivers the skill with no second step; the manifest is specified in
[`07-herdr-integration.md`](07-herdr-integration.md) §8.

### 6.3 `herdr-cron --skill` — the no-network path

```bash
mkdir -p ~/.agents/skills/herdr-cron
herdr-cron --skill > ~/.agents/skills/herdr-cron/SKILL.md
ln -sfn ../../.agents/skills/herdr-cron ~/.claude/skills/herdr-cron
```

This is the path for an air-gapped machine, a machine with no Node, or an agent that has the
binary in hand and wants the instructions now. It also repairs a stale install. Its documented
limitation: `--skill` prints `SKILL.md` only, so an install made this way has no `references/`.
The body MUST therefore be self-sufficient for every common task and MUST attribute detail to
`herdr-cron schema` and `--help` rather than to `references/` alone — the same conclusion §1
reaches from the token budget, arrived at from a second direction.

`herdr` additionally advertises the flag from its own help output for agents that arrive without
the skill (research §A7):

```
Are you an AI? ... SKIP if a Herdr skill is already in your context. Otherwise run: herdr --skill
```

herdr-cron SHOULD copy this: one such block in the root `--help` epilogue, naming
`herdr-cron --skill`.

---

## 7. Versioning and drift

**The skill has no version of its own.** It is versioned by the binary that embeds it: one git
tag produces one binary and one skill, and §2.2's `diff` gate makes any other combination
unbuildable. This is why `metadata` is unused in §3 — a `metadata: {version: "0.1.0"}` line
would be a second, hand-maintained version number, i.e. exactly the drift the embed exists to
eliminate (research §A7). Provenance for an *installed* skill is external and already exists:
`skillFolderHash` plus `updatedAt` in `~/.agents/.skill-lock.json`, or `#v0.3.0` in the install
source (research §A5, §A6).

**When the installed skill is older than the binary**, it MUST still work, and it does:

| Drift | Effect | Why it is survivable |
| --- | --- | --- |
| A new flag on `job add` | The skill does not mention it | The skill routes to `herdr-cron schema --command "job add"`, which prints the flag with its type and default ([`05-cli.md`](05-cli.md) §3.5) |
| A new `error.code` | Unknown code in a response | Codes are additive and never change meaning ([`05-cli.md`](05-cli.md) §2.1); the skill's rule is to report an unrecognised code, not to guess |
| A renamed flag | Would break a frozen example | PROHIBITED by [`05-cli.md`](05-cli.md): `json` output is a stable interface. A rename is a new flag plus a retained old one |
| A changed exit-code meaning | Would break the escalation rule | PROHIBITED. §2.2 has no protection against this and none is possible from the skill side; the contract in [`05-cli.md`](05-cli.md) §2.2 is what holds |

This is the operational reason the skill MUST NOT freeze help text. A skill that reproduces the
flag table is wrong the moment a flag is added and, worse, *confidently* wrong: the agent has no
signal that its copy is stale. A skill that says "ask the binary" is correct for every future
version. The research states the pattern in these terms and names the `herdr` skill as the
working example (research §A4): "a router and a set of invariants, not a frozen copy of the help
text".

Three invariant classes the skill *is* allowed to freeze, because they are contracts rather than
surface: the response envelope and the `result`/`error` split ([`05-cli.md`](05-cli.md) §2); the
exit-code meanings, including exit 3 ([`05-cli.md`](05-cli.md) §2.2); and the safety invariants
of §4.1 ([`README.md`](README.md) D3). A release that changes any of the three MUST update
`SKILL.md` in the same commit. Nothing else needs a release-time edit, which is the point.

---

## 8. The shipped SKILL.md

Normative draft, within the 500-line cap (research §A3). Copied into `skills/herdr-cron/SKILL.md` unchanged.

````markdown
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
| `0` | Success. With `--wait`, the run finished `success` or `no_op` | Continue |
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
  agent job in a new directory, confirm `validate` reports no trust warning for it.
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
````

---

## Open points

1. **`--skill` prints only `SKILL.md`.** [`05-cli.md`](05-cli.md) §1 specifies exactly one skill
   flag, so the §6.3 install path produces a skill with no `references/`. §4 and §6.3 compensate
   by requiring the body to be self-sufficient, which is the right property anyway. A full-bundle
   writer (`herdr-cron --skill-dir DIR`, walking `skills.References`) would be an addition to
   [`05-cli.md`](05-cli.md) §1 and MUST be specified there first, not here.
2. **`validate`'s trust warning is not named in the CLI spec.** §8 tells the agent to look for
   the absence of a trust warning from `validate`; [`03-job-model.md`](03-job-model.md) §7 level
   4 does specify that warning as an environment check, but [`05-cli.md`](05-cli.md) §3.5 does
   not state the JSON field it appears in. `references/troubleshooting.md` needs that field
   name, which [`07-herdr-integration.md`](07-herdr-integration.md) §5 owns.
3. **The description names `jobs.yaml` nowhere.** A user asking "edit my herdr-cron jobs.yaml"
   matches on `herdr-cron` alone. Adding the filename costs ~12 characters of a budget with 61%
   headroom; it is out because "edit ... scheduled jobs" covers the request and keyword count is
   not free at the *listing* level. Revisit if usage shows the miss.
4. **`license: MIT` is asserted, not verified.** The repo has no LICENSE file yet; the
   frontmatter value MUST match the licence the repository ships. Decision D4 in
   [`README.md`](README.md) fixes the distribution shape but not the licence. [UNVERIFIED]
