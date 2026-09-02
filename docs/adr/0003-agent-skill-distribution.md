# 0003 — The Agent Skill ships inside the binary, hand-written, through both front doors

## Status

Accepted — 2026-09-02. Specified in [`docs/spec/08-agent-skill.md`](../spec/08-agent-skill.md) §2, §6 and §7; the packaging half is decision **D4** in [`docs/spec/README.md`](../spec/README.md).

## Context

herdr-cron's primary caller is a coding agent. A JSON-first CLI is useless if the agent does not know it exists, calls it from a remembered flag list that has drifted, or drives it in an unsafe order — adding six agent jobs at `0 9 * * *` in one repository, or retrying a run that is `blocked` on a human approval dialog. What we need from the agent is mostly *judgement plus a routing habit*: validate a schedule before writing it, read `result` rather than the text rendering, branch on `error.code`, and ask the binary — `herdr-cron schema --command "job add"` — instead of trusting its own memory. None of that is discoverable from `--help`, because `--help` only helps an agent that has already decided to run the tool.

Agent harnesses have converged on a file for exactly this: Markdown with YAML frontmatter, loaded when the `description` matches what the agent is about to do. The token economics are first-party and decide the design ([`docs/research/2026-09-02-agent-skill-and-cli-ux.md`](../research/2026-09-02-agent-skill-and-cli-ux.md) §A4): metadata costs ~100 tokens always, the body under 5k only when it triggers, and bundled files cost nothing until read. So the description is the trigger mechanism, and command output is cheaper *and* more accurate than reference prose.

Two distribution facts constrain the rest. First, `herdr --skill` was verified on this machine to print a file byte-identical to the installed copy — 195 lines, `diff` exit 0 (research §A7) — which is a working precedent for making skill/binary version skew structurally impossible. Second, herdr-cron has two front doors by **D4**: a Herdr plugin and a standalone CLI that works with Herdr absent. That shape was not invented here — both third-party precedents examined converged on it independently, `herdr-hitl` most closely ([`docs/research/2026-09-02-herdr-plugin-integration.md`](../research/2026-09-02-herdr-plugin-integration.md) §8, "The closest precedent is not reviewr"). A plugin skill lives at `<plugin>/skills/<skill-name>/SKILL.md` per the harness's own locations table (research §A5), which is already the repo layout — so the plugin front door can deliver the skill with no second step, while the standalone front door has to deliver it some other way.

Three failure modes to design against:

1. **Drift.** The skill freezes a flag name that the CLI later renames. The agent's command fails, the agent stops scheduling, and the feature silently ceases to exist.
2. **Version skew.** An installed skill from six months ago describing a binary from today, with no signal to the agent that its copy is stale — *confidently* wrong.
3. **A generated skill.** A generator can emit a flag table, which is the least valuable half. It cannot emit "an occurrence that did not run is still a run record, so read the `reason` before assuming a bug".

## Decision

**The skill is hand-written and committed at `skills/herdr-cron/SKILL.md`**, with three bundled files one level below it in `references/` (`job-schema.md`, `json-shapes.md`, `troubleshooting.md`). Not generated, not templated, not assembled at build time. The directory name equals the frontmatter `name`, because the harness requires that and because in Claude Code the directory name determines the slash command (research §A7). A `SKILL.md` at the repo root is **prohibited**: the installer's discovery takes it and returns early, hiding everything under `skills/` (research §A6).

**It is embedded in the binary.** `skills/embed.go` embeds `herdr-cron/SKILL.md` and `herdr-cron/references/*.md` under one `//go:embed` directive and exposes them as `skills.SkillMD()`, `skills.References()`, `skills.Read()` and `skills.FS()`; `herdr-cron --skill` writes those bytes to stdout verbatim — no templating, no version substitution, no newline normalisation. One git tag therefore produces one binary and one skill, and no other combination exists. The skill has no version of its own, which is why its frontmatter carries no `metadata.version`: a second hand-maintained version number would be precisely the drift the embed eliminates.

**The skill is a router and a set of invariants, not a frozen copy of the help text.** It freezes exactly three things, because they are contracts rather than surface: the response envelope and its `result`/`error` split, the exit-code meanings (`0` ok, `1` error, `2` usage, `3` blocked), and the safety invariants of D3. Everything else it attributes to `herdr-cron schema`, `herdr-cron validate --schedule`, and `--help`. That is what makes an older installed skill still correct against a newer binary: a new flag is discovered by asking, not remembered.

**Drift is guarded by tests, not by generation.** `skills/embed_test.go` asserts that the embedded skill is byte-identical to the file in the repository, that its frontmatter uses only the properties the Skills API accepts, that the description fits its budget, that the body stays under the line cap, and that every bundled reference is linked from the body — an unlinked reference is a file no agent will ever read. `internal/cli/docs_test.go` is the other half: it walks the cobra command tree and asserts that every document naming this surface — `README.md`, `README.ko.md`, `CONTRIBUTING.md`, `CONTEXT.md`, the skill, its references, and every ADR — names only real flags, attaches each flag to a command that actually defines it, and (for the READMEs) mentions every command path. Both check *names*, deliberately not prose: they catch the rename that breaks the agent and stay out of the way of the judgement content a generator would flatten. CI runs them with the rest of the suite, so a flag rename fails the build until the skill and the docs are updated in the same commit.

**Both front doors deliver it, three paths in total, all landing the same content:**

- **The Herdr plugin.** The plugin ships the directory as-is; installing herdr-cron as a plugin delivers the skill with no second step. Plugin skills are namespaced, so they cannot collide with a personal or project install of the same skill.
- **`herdr-cron install-cli --with-skill`.** The standalone path, and also the plugin's `install-cli` action: it links the binary into a directory on `PATH` and writes all four skill files out of the embedded copy. Idempotent — a second run is a no-op.
- **`herdr-cron --skill`.** The no-network, no-Node, air-gapped path, and the way to repair a stale install: redirect it into the harness's skill directory.

## Consequences

- Version skew between the skill and the binary is impossible in practice: they travel in the same git tree, the same tag, the same plugin checkout, and the same process image.
- The skill can hold the part that matters — when to validate, why a `skipped` record is not a failure, why `blocked` must never be retried — in a human voice, because a human wrote it.
- An agent that arrives with the binary but no skill can bootstrap itself: `--skill` in hand, plus one line in the root `--help` epilogue naming it, is enough.
- The drift test makes an agent-facing flag rename a two-file change, enforced. That friction is deliberate — renaming a flag an agent depends on *is* a breaking change.
- Cost: `--skill` prints `SKILL.md` only, so an install made that way has no `references/`. The body must therefore be self-sufficient for every common task, which is also what the token budget wanted; `install-cli --with-skill` is the path that lands all four files.
- Cost: prose can still go stale in ways a name check misses — a flag that changes meaning without changing name. Accepted, because the alternative catches nothing at all.
- Cost: on Windows the installer may produce a junction or an outright copy rather than a symlink (research §A5), so nothing may assume an installed skill links back into the repo. `--skill` remains the way to refresh it.
- Cost: the skill repeats a small amount of what `README.md` also says. Kept, because the audiences differ — an agent reading a skill has no browser — and because the same test covers both.

## Alternatives considered

**Generate `SKILL.md` from the cobra tree at build time.** Rejected: it produces a flag reference, the half that is least valuable and most redundant with `schema`. The judgement content would have to live in a template anyway, so the generator only moves hand-written text somewhere less readable while adding a build step and a generated file in git.

**Ship the skill only on disk, not embedded.** Rejected: it reintroduces skew the moment a binary is copied without its repo, and it makes `--skill` impossible — which is the one path that works with no network and no package manager. Embedding also gives the byte-identity property that the drift test asserts.

**Ship the skill as a separate repository or package.** Rejected: version skew by construction, and a second release process for four Markdown files.

**Freeze the flag tables in the skill so the agent needs no extra call.** Rejected: wrong the moment a flag is added, and confidently wrong — the agent has no signal that its copy is stale. `schema --command "job add"` costs one command's output and cannot be stale (research §A4).

**Rely on `--help` alone and ship no skill.** Rejected: `--help` requires the agent to already know the tool exists and to have already decided to schedule something. The `description` frontmatter *is* the trigger; without it, nothing fires.

**Auto-install the skill into the user's harness directory on first run.** Rejected: writing into a user's agent configuration without being asked is not a scheduler's business, and every harness puts it somewhere different. `install-cli --with-skill` is explicit and idempotent, and the plugin action makes it one click for the case where the user did ask.

**Homebrew and Scoop as additional front doors.** Rejected for now: neither tap repository exists, and publishing to one requires a personal access token only the human can mint — a release that 404s on its first tag is worse than one channel fewer. The standalone front door is `go install` plus the archives goreleaser attaches to each GitHub Release, for six targets.
