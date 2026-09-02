---
title: Agent Skills and CLI ergonomics for agent-driven Go CLIs
date: 2026-09-02
subject: (a) the Agent Skill format and its distribution tooling; (b) what makes a Go CLI good for an agent to drive
audience: the engineer implementing herdr-cron
status: evidence, not verdict
---

# Agent Skills and CLI ergonomics for agent-driven Go CLIs

This document covers two things herdr-cron needs and nothing else. gocron (the scheduling
engine), Bubble Tea (the TUI), and the Herdr plugin system are each covered by a sibling
document; they are referenced by name here and not researched.

Everything below was read from source, run on this machine, or read from a first-party spec.
Where a doc and the code disagree, the disagreement is called out.

## Citation tags

Definitions used inline throughout. `[$ ...]` means the exact command was run on this machine
(Linux 6.18.33.2-microsoft-standard-WSL2, x86-64, Go 1.26.2) on 2026-09-02.

| Tag | Meaning |
| --- | --- |
| `[SKILLS-CLI]` | `github.com/vercel-labs/skills` cloned to `/tmp/hc-research/skills`, pinned at `435076e78988e1e6ec40d00b0b1d76bdbbc5419a` (committed 2026-08-18T20:28:31Z). Obtained with `git clone --depth 1 https://github.com/vercel-labs/skills`, SHA from `git rev-parse HEAD`. |
| `[CC-SKILLS]` | <https://code.claude.com/docs/en/skills> (served as `https://code.claude.com/docs/en/skills.md`), read 2026-09-02. |
| `[API-SKILLS]` | <https://platform.claude.com/docs/en/agents-and-tools/agent-skills/overview> (redirect target of `docs.claude.com/en/docs/agents-and-tools/agent-skills/overview`), read 2026-09-02. |
| `[AS-SPEC]` | <https://agentskills.io/specification> — the open Agent Skills standard, read 2026-09-02. |
| `[LOCAL-SKILLS]` | Skills installed on this machine under `~/.claude/skills/` and `~/.agents/skills/`. Paths cited individually. |
| `[COBRA]` | `github.com/spf13/cobra` cloned to `/tmp/hc-research/cobra`, pinned at `adbc8813901bba65827259daa8e22ff94ec1f30e` (2026-07-10T20:43:07-04:00). This is `main`, *ahead of* the latest tagged release `v1.10.2` (2025-12-03, from `https://proxy.golang.org/github.com/spf13/cobra/@latest`). |
| `[URFAVE]` | `github.com/urfave/cli` (module path `github.com/urfave/cli/v3`) cloned to `/tmp/hc-research/urfave-cli`, pinned at `1a4deb4f5a35ee12706602698dc0527d44c14b19` (2026-08-25T02:20:51+08:00). Latest tagged release `v3.11.0` (2026-08-16). |
| `[KSERVICE]` | `github.com/kardianos/service` cloned to `/tmp/hc-research/kardianos-service`, pinned at `99070899946d7ab341109f83b7c9fb941a118be0` (2026-08-29T08:13:08-05:00). Latest tagged release `v1.3.0` (2026-07-06). |
| `[XDG]` | `github.com/adrg/xdg`. Read both `main` at `b1241e93d6d49a821dada3ee2ecc09bc91f5e65c` (2026-06-05) and the released `v0.5.3` from the module cache; the base-dir resolution is byte-identical between them (verified by reading `paths_windows.go` in both). Citations are to `v0.5.3` unless noted. |
| `[CONFIGDIR]` | `github.com/kirsle/configdir` cloned to `/tmp/hc-research/kirsle-configdir`, pinned at `e45d2f54772fea5426e7ce417474b62da9457dcc`. `git log -1` reports this is the **initial and only commit**, dated 2017-01-27. No `go.mod`. |
| `[GO-OS]` | Go standard library source on this machine: `/usr/local/go/src/os/file.go`, Go 1.26.2 (`go version`). |
| `[GORELEASER]` | goreleaser documentation, read 2026-09-02: `https://goreleaser.com/getting-started/quick-start/`, `/customization/publish/homebrew_casks/`, `/customization/publish/homebrew_formulas/`, `/customization/publish/scoop/`, `/customization/publish/winget/`. Site banner at read time: "GoReleaser v2.18 is out!". |
| `[GH-SRC]` | `github.com/cli/cli` at branch `trunk`, files read over raw.githubusercontent.com on 2026-09-02: `cmd/gh/main.go`, `internal/ghcmd/cmd.go`. Branch tip, not a tag. |
| `[MSLEARN]` | <https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/schtasks-create>, read 2026-09-02. |
| `[APPLE-LAUNCHD]` | <https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html> (Apple Documentation Archive, doc version 6.3.4, dated 2016-09-13), read 2026-09-02. |
| `[SYSTEMD-MAN]` | `systemd.unit(5)` and `loginctl(1)` man pages installed on this machine; systemd 255 (255.4-1ubuntu8.17) per `[$ systemctl --version]`. |

Module versions resolved via `[$ curl -sS https://proxy.golang.org/<module>/@latest]` on 2026-09-02:
`modernc.org/sqlite v1.57.0` (2026-08-19), `github.com/mattn/go-sqlite3 v1.14.50` (2026-08-17),
`go.etcd.io/bbolt v1.5.0` (2026-06-03).

---

# Part A — Agent Skills

## A1. There are two spec surfaces, and they are not the same size

A skill is a directory with a `SKILL.md` in it. That much is uncontested. But the frontmatter
schema depends on **who is going to load the file**, and this is the single most common source
of confusion.

- The **open standard** is at <https://agentskills.io> and defines six fields
  `[AS-SPEC]`.
- **Claude Code** accepts those six plus about fourteen of its own `[CC-SKILLS]`.
- **claude.ai uploads and the Skills API** accept only the six and *reject* the rest with a hard
  error. `[CC-SKILLS]` quotes the exact error text:

```
Unexpected key(s) in SKILL.md frontmatter: argument-hint. Allowed properties are: allowed-tools, compatibility, description, license, metadata, name
```

`[CC-SKILLS]` states the compatibility rule directly: "Claude Code accepts all six fields, so
frontmatter that follows the spec loads in Claude Code without" modification. The practical
consequence for a shipped skill: **use only the six spec fields unless you have a reason not
to.** A skill bundled with a public CLI will be installed into harnesses you did not test.

There is a second, subtler disagreement between the two sources on whether `name` is required.

- `[AS-SPEC]` frontmatter table: `name` — Required **Yes**; `description` — Required **Yes**.
  It further requires that `name` "must match the parent directory name".
- `[CC-SKILLS]` frontmatter reference: "All fields are optional. Only `description` is
  recommended so Claude knows when to use the skill." Its table marks `name` as Required **No**,
  with the note "Display name shown in skill listings. Defaults to the directory name."
- `[SKILLS-CLI]` — the installer — is stricter than Claude Code and matches the open spec.
  `src/skills.ts` `parseSkillMd()` **skips any skill missing either field**:

```ts
  if (!data.name || !data.description) {
    const missing: string[] = [];
    if (!data.name) missing.push('name');
    if (!data.description) missing.push('description');
    warnSkippedSkill(skillMdPath, `missing required frontmatter field(s): ${missing.join(', ')}`);
    return null;
  }
```

It also rejects non-string values for either field (YAML happily parses `1.0` as a number).

**Resolution: always write both `name` and `description`.** Claude Code will tolerate their
absence; the distribution tooling will not install the skill at all.

## A2. The frontmatter schema and its constraints

### The six spec fields `[AS-SPEC]`

| Field | Required | Constraints (verbatim from `[AS-SPEC]`) |
| --- | --- | --- |
| `name` | Yes | Max 64 characters. Lowercase letters, numbers, and hyphens only. Must not start or end with a hyphen. |
| `description` | Yes | Max 1024 characters. Non-empty. Describes what the skill does and when to use it. |
| `license` | No | License name or reference to a bundled license file. |
| `compatibility` | No | Max 500 characters. Indicates environment requirements (intended product, system packages, network access, etc.). |
| `metadata` | No | Arbitrary key-value mapping for additional metadata (a map from string keys to string values). |
| `allowed-tools` | No | Space-separated string of pre-approved tools the skill may use. (Experimental) |

`[AS-SPEC]` expands `name` into four rules: 1–64 characters; only lowercase alphanumerics and
hyphens; no leading or trailing hyphen; **no consecutive hyphens** (`pdf--processing` is
invalid).

`[API-SKILLS]` adds two constraints the open spec does not state, for skills going through
Anthropic's surfaces: `name` "Cannot contain XML tags" and "Cannot contain reserved words:
'anthropic', 'claude'"; `description` "Cannot contain XML tags".

`[SKILLS-CLI]` independently implements the same name rules in its registry validator
(`src/providers/wellknown.ts`), which is useful corroboration that these are real and enforced:

```ts
  private isValidSkillName(name: unknown): name is string {
    if (typeof name !== 'string') return false;
    if (name.length < 1 || name.length > 64) return false;
    if (!/^[a-z0-9-]+$/.test(name)) return false;
    if (name.startsWith('-') || name.endsWith('-')) return false;
    if (name.includes('--')) return false;
    return true;
  }
```

and the 1024-character description cap: `if (typeof e.description !== 'string' || !e.description || e.description.length > 1024) { return false; }`.

### The Claude Code extensions `[CC-SKILLS]`

Not portable. Listed so you can recognise them in other people's skills and know to strip them
from yours. All optional.

`when_to_use`, `argument-hint`, `arguments`, `disable-model-invocation`, `user-invocable`,
`disallowed-tools`, `model`, `effort`, `context`, `agent`, `background`, `hooks`, `paths`,
`shell`.

Three of these matter for a CLI-driving skill:

- **`disable-model-invocation: true`** — "Set to `true` to prevent Claude from automatically
  loading this skill." The consequence, per the `[CC-SKILLS]` invocation table, is that the
  *description is not in context at all*. That is the opposite of what a CLI skill wants.
- **`allowed-tools`** — "Tools Claude can use without asking permission during the turn that
  invokes this skill. The grant clears when you send your next message." It does **not**
  restrict tools; every tool remains callable. `[CC-SKILLS]` is blunt about the security
  implication: "Workspace trust doesn't gate this field... A skill can grant itself broad tool
  access, so review the `allowed-tools` of skills checked into a repository before you run
  Claude Code there."
- **`paths`** — glob patterns limiting automatic activation. Irrelevant for a scheduler, whose
  triggers are conversational rather than file-shaped.

Two mechanical details from `[CC-SKILLS]` worth knowing before you write the file:

- "Claude Code reads the frontmatter only when the opening `---` is the file's first line.
  Otherwise it treats the whole file, `---` markers included, as skill content." A leading
  blank line or BOM silently turns your frontmatter into prose.
- The frontmatter parser in `[SKILLS-CLI]` (`src/frontmatter.ts`) deliberately supports only
  YAML and refuses `---js` blocks, with a comment naming the reason: "Does NOT support
  `---js` / `---javascript` to avoid eval()-based RCE that exists in gray-matter's built-in JS
  engine."

### Character budgets — two different numbers

There are two truncation limits, and they are not the same.

- `[AS-SPEC]` / `[API-SKILLS]`: `description` max **1024** characters. This is a validation
  limit — exceed it and the skill is rejected.
- `[CC-SKILLS]`: "the combined `description` and `when_to_use` text is truncated at **1,536**
  characters in the skill listing to reduce context usage." This is a display limit inside one
  harness.

`[CC-SKILLS]` also documents a *dynamic* budget that will bite a skill with a long description
on a machine with many skills installed: "Claude Code loads a listing of skill names and
descriptions into context... if you have many skills, Claude Code shortens descriptions to fit
the listing's character budget, which can strip the keywords Claude needs to match your
request. The budget scales at 1% of the model's context window. When the listing overflows,
Claude Code drops descriptions starting with the skills you invoke least."

**Design consequence, and it is the most actionable line in Part A: put the trigger keywords
first in the description.** A description whose activation keywords sit in the last sentence
can lose them to truncation on a busy machine, at which point the skill exists but never fires.

## A3. Directory layout

`[AS-SPEC]` gives the canonical layout:

```
skill-name/
├── SKILL.md          # Required: metadata + instructions
├── scripts/          # Optional: executable code
├── references/       # Optional: documentation
├── assets/           # Optional: templates, resources
└── ...               # Any additional files or directories
```

`[CC-SKILLS]` shows a flatter variant and adds the size guidance:

```text
my-skill/
├── SKILL.md (required - overview and navigation)
├── reference.md (detailed API docs - loaded when needed)
├── examples.md (usage examples - loaded when needed)
└── scripts/
    └── helper.py (utility script - executed, not loaded)
```

Both sources give the same two rules of thumb, in nearly the same words: keep `SKILL.md` under
**500 lines** `[AS-SPEC]` `[CC-SKILLS]`, and keep file references **one level deep** from
`SKILL.md` — "Avoid deeply nested reference chains" `[AS-SPEC]`.

The convention is confirmed by a skill installed on this machine. `[LOCAL-SKILLS]`
`~/.claude/skills/lsoffice-cli/` contains exactly `SKILL.md` plus a `references/` directory
with two files:

```
lsoffice-cli/SKILL.md
lsoffice-cli/references/daily-reports.md
lsoffice-cli/references/room-reservations.md
lsoffice-cli/.lsoffice-manifest.json
```

`[$ cd ~/.claude/skills && find -L lsoffice-cli -type f]`

References must be *named from `SKILL.md`* or the agent will never read them `[AS-SPEC]`
`[CC-SKILLS]`:

```markdown
## Additional resources

- For complete API details, see [reference.md](reference.md)
- For usage examples, see [examples.md](examples.md)
```

## A4. Discovery and loading — the key design constraint

This is the part that determines how you write the skill, so it is worth stating precisely.

`[API-SKILLS]` names the model **progressive disclosure** and gives the three levels with
costs:

| Level | When loaded | Token cost | Content |
| --- | --- | --- | --- |
| Level 1: Metadata | Always (at startup) | ~100 tokens per Skill | `name` and `description` from YAML frontmatter |
| Level 2: Instructions | When Skill is triggered | Under 5k tokens | SKILL.md body with instructions and guidance |
| Level 3+: Resources | As needed | None until accessed | Bundled files. Reference files load into context when read. Scripts run through bash, and only their output enters context |

`[AS-SPEC]` states the same three levels with the same numbers (`~100 tokens`, `< 5000 tokens
recommended`).

The mechanism, per `[API-SKILLS]`, is that the agent reads the file with bash: "When a Skill is
triggered, Claude uses bash to read SKILL.md from the filesystem, bringing its instructions into
the context window." Its worked example is worth reproducing because it shows what the agent
actually sees:

1. **Startup:** system prompt includes `pdf-processing - Extract text and tables from PDF files, fill forms, merge documents. Use when working with PDF files or when the user mentions PDFs, forms, or document extraction.`
2. **User request:** "Extract the text from this PDF and summarize it"
3. **Claude invokes:** `bash: cat pdf-processing/SKILL.md` → instructions loaded into context
4. **Claude determines:** form filling is not needed, so `FORMS.md` is not read

So: **only the description competes for attention up front.** The body is free until fired, and
bundled files are free until read. `[API-SKILLS]` spells out the corollary — "No practical
limit on bundled content: Files don't consume context until accessed" — which is the licence to
put a complete CLI reference in `references/` rather than cramming it into `SKILL.md`.

Two lifecycle facts from `[CC-SKILLS]` that change how you phrase instructions:

- "When you or Claude invoke a skill, the rendered `SKILL.md` content enters the conversation as
  a single message and **stays there across later turns**... Claude Code does not re-read the
  skill file on later turns, so write guidance that should apply throughout a task as **standing
  instructions rather than one-time steps**."
- Scripts are strictly cheaper than generated code: "When Claude runs `validate_form.py`, the
  script's code never loads into the context window. Only its output... consumes tokens"
  `[API-SKILLS]`.

For a CLI skill this argues for a specific shape: a short `SKILL.md` that (a) states the
preconditions, (b) tells the agent to get authoritative syntax from `--help` on the installed
binary rather than trusting the skill's own examples, and (c) points at `references/` for the
long stuff. The `herdr` skill on this machine does exactly this `[LOCAL-SKILLS]`
`~/.claude/skills/herdr/SKILL.md`:

````markdown
## Learn the current CLI

The installed binary is the authority for command syntax. Start with:

```bash
herdr --help
```
````

and, further down, `"Most control commands return JSON. Read identifiers and state from those
responses instead of predicting them."`

That is the pattern herdr-cron should copy: the skill is a *router and a set of invariants*, not
a frozen copy of the help text that will drift out of date with the binary.

## A5. On-disk convention, verified on this machine

`[CC-SKILLS]` gives the locations table:

| Location | Path | Applies to |
| --- | --- | --- |
| Enterprise | see managed settings | All users in your organization |
| Personal | `~/.claude/skills/<skill-name>/SKILL.md` | All your projects |
| Project | `.claude/skills/<skill-name>/SKILL.md` | This project only |
| Plugin | `<plugin>/skills/<skill-name>/SKILL.md` | Where plugin is enabled |

Precedence, per `[CC-SKILLS]`: enterprise overrides personal, personal overrides project; any of
these overrides a bundled skill of the same name; plugin skills are namespaced
`plugin-name:skill-name` and therefore cannot conflict.

The thing you cannot learn from the docs is that **installed skills are symlinks, not copies**.
`[$ cd ~/.claude/skills && ls -la]` on this machine:

```
lrwxrwxrwx  1 huke huke   26 Aug 30 18:07 herdr -> ../../.agents/skills/herdr
lrwxrwxrwx  1 huke huke   31 Sep  1 15:55 herdr-hitl -> ../../.agents/skills/herdr-hitl
lrwxrwxrwx  1 huke huke   55 Aug 18 17:45 ask-huke -> /home/huke/lsware-ax/skills/skills/engineering/ask-huke
```

39 entries, all symlinks (`[$ ls ~/.claude/skills/ | wc -l]` → 39).

`[CC-SKILLS]` confirms this is supported and not an accident: "A `<skill-name>` entry in the
enterprise, personal, or project locations can be a symlink to a directory elsewhere on disk.
Claude Code follows the symlink and reads `SKILL.md` from the target directory, and if the same
target is reachable from more than one location, Claude Code loads the skill once."

The symlink target is the *canonical* location that `[SKILLS-CLI]` owns
(`src/installer.ts` and `src/constants.ts`):

```ts
export const AGENTS_DIR = '.agents';
export const SKILLS_SUBDIR = 'skills';
export const UNIVERSAL_SKILLS_DIR = '.agents/skills';
```

```ts
export function getCanonicalSkillsDir(global: boolean, cwd?: string): string {
  const baseDir = global ? homedir() : cwd || process.cwd();
  return join(baseDir, AGENTS_DIR, SKILLS_SUBDIR);
}
```

So: **content lands in `~/.agents/skills/<name>/` and each agent's own skills directory gets a
relative symlink to it.** Verified: `[$ ls -la ~/.agents/]` shows `skills/` with 36 entries and
a lockfile.

Provenance lives in a global lockfile at `~/.agents/.skill-lock.json` (`src/skill-lock.ts`:
`const LOCK_FILE = '.skill-lock.json'` and `return join(homedir(), AGENTS_DIR, LOCK_FILE);`).
Confirmed present with `version: 3`
(`[$ jq -c 'keys' ~/.agents/.skill-lock.json]` → `["dismissed","lastSelectedAgents","skills","version"]`),
one entry being:

```json
{"source":"vercel-labs/skills","sourceType":"github","sourceUrl":"https://github.com/vercel-labs/skills.git","skillPath":"skills/find-skills/SKILL.md","skillFolderHash":"76a98a285cb0434f3d39e1a873823556330e398b","installedAt":"2026-03-25T09:54:06.876Z","updatedAt":"2026-07-27T06:37:51.940Z"}
```

Project-scoped installs use a separate, deliberately merge-friendly file
`skills-lock.json` (`src/local-lock.ts`: `const LOCAL_LOCK_FILE = 'skills-lock.json'`, with the
comment "Intentionally minimal and timestamp-free to minimize merge conflicts").

On Windows, `[SKILLS-CLI]` `src/installer.ts` uses **junctions** rather than symlinks, because
unprivileged symlink creation on Windows is not reliable:

```ts
    const symlinkType = platform() === 'win32' ? 'junction' : undefined;
    const symlinkTarget = symlinkType === 'junction' ? resolvedTarget : relativePath;
```

with a documented `--copy` escape hatch and an automatic copy fallback when linking fails
(`symlinkFailed?: boolean` in the result type).

### Three real frontmatter blocks from this machine

Primary evidence of what people actually write. All three use only spec fields.

`[LOCAL-SKILLS]` `~/.claude/skills/research/SKILL.md`, complete first four lines:

```markdown
---
name: research
description: Investigate a question against high-trust primary sources and capture the findings as a Markdown file in the repo. Use when the user wants a topic researched, docs or API facts gathered, or reading legwork delegated to a background agent.
---
```

`[LOCAL-SKILLS]` `~/.claude/skills/writing-for-agents/SKILL.md`:

```markdown
---
name: writing-for-agents
description: Writing documents for agents. Use when creating or editing skills, or modifying AGENTS.md or CLAUDE.md.
---
```

`[LOCAL-SKILLS]` `~/.claude/skills/lsoffice-cli/SKILL.md` — a CLI-driving skill, and the only
one of the three that uses `allowed-tools`:

```markdown
---
name: lsoffice-cli
description: Operates LSOffice daily reports and meeting-room reservations through the lsoffice CLI. Use when users ask to read or submit daily work reports, inspect rooms, or create and delete room reservations.
allowed-tools: Bash
---
```

Note the shape of every description: **what it does, then "Use when ..." with concrete trigger
phrases.** `[AS-SPEC]` labels the alternative a "Poor example" (`description: Helps with PDFs.`).

The longest description on this machine belongs to `herdr-hitl` and shows how far people push
the field — it lists five distinct trigger conditions in one sentence
(`[LOCAL-SKILLS]` `~/.claude/skills/herdr-hitl/SKILL.md`). Under the 1,536-character listing
budget `[CC-SKILLS]` that is aggressive but legal.

## A6. The `skills` CLI: what it is and what it accepts

`[SKILLS-CLI]` is a TypeScript/Node CLI published for `npx skills`. It is a package manager for
skills, not an agent.

### Commands and aliases

From `src/cli.ts`'s dispatch `switch` (authoritative; the help text omits some aliases):

| Command | Aliases | Purpose (from `showHelp()`) |
| --- | --- | --- |
| `add <package>` | `a`, `i`, `install` | Add a skill package |
| `use <package>@<skill>` | — | Generate a prompt for using one skill without installing it |
| `remove [skills]` | `rm`, `r` | Remove installed skills |
| `list` | `ls` | List installed skills |
| `find [query]` | `search`, `f`, `s` | Search for skills interactively |
| `update [skills...]` | `upgrade`, `check` | Update skills to latest versions |
| `init [name]` | — | Initialize a skill (creates `<name>/SKILL.md` or `./SKILL.md`) |
| `experimental_install` | — | Restore skills from `skills-lock.json` |
| `experimental_sync` | — | Sync skills from `node_modules` into agent directories |
| `--version` | `-v` | Print version |
| `--help` | `-h` | Print help |

Real flags, verbatim from `showHelp()` in `src/cli.ts`:

```
Add Options:
  -g, --global           Install skill globally (user-level) instead of project-level
  -a, --agent <agents>   Specify agents to install to (use '*' for all agents)
  -s, --skill <skills>   Specify skill names to install (use '*' for all skills)
  -l, --list             List available skills in the repository without installing
  -y, --yes              Skip confirmation prompts
  --copy                 Copy files instead of symlinking to agent directories
  --metadata <json>      Attach valid JSON to the install telemetry event
  --subagent <names>     Install to Eve subagents (use 'root' for the root agent)
  --all                  Shorthand for --skill '*' --agent '*' -y
  --full-depth           Search all subdirectories even when a root SKILL.md exists

List Options:
  -g, --global           List global skills (default: project)
  -a, --agent <agents>   Filter by specific agents
  --json                 Output as JSON (machine-readable, no ANSI codes)
```

Note `list --json` — the *only* `--json` in the whole CLI. Its shape, from `src/list.ts`:

```ts
      return {
        name: skill.name,
        path: skill.canonicalPath,
        scope: skill.scope,
        agents: skill.agents.map((a) => agents[a].displayName),
        source: lockEntry?.source ?? null,
        sourceUrl: lockEntry?.sourceUrl ?? null,
        sourceType: lockEntry?.sourceType ?? null,
      };
```

printed as `JSON.stringify(jsonOutput, null, 2)`. Also note **default scope is project, not
global**: `const scope = options.global === true ? true : false;`.

The CLI detects that it is running inside an agent and suppresses its ASCII-art banner
accordingly (`src/cli.ts`: `const inAgent = await isRunningInAgent();` then
`if (!inAgent) showLogo();`). That is a small but real piece of agent-aware CLI design.

### Accepted source forms

From `parseSource()` in `src/source-parser.ts`, in the order it tries them:

1. **Local path** — absolute, `./`, `../`, `.`, `..`, or a Windows drive path
   (`/^[a-zA-Z]:[/\\]/`). Resolved with `resolve()`; existence is checked later.
2. **`github:owner/repo`** and **`gitlab:owner/repo`** prefixes, including
   `github:owner/repo/subpath` and `github:owner/repo@skill`.
3. **Hosted artifact URLs** downloaded directly rather than cloned: hosts
   `raw.githubusercontent.com`, `codeload.github.com`, `objects.githubusercontent.com`, plus
   `github.com/.../archive/|raw/|releases/download/|releases/latest/download/` and
   `gitlab.com/.../-/archive|raw/`.
4. **GitHub Enterprise URLs** (when `GH_HOST` is set), routed through the generic git path.
5. **GitHub `tree` URLs** with a ref and optional subpath:
   `https://github.com/owner/repo/tree/branch/path/to/skill`.
6. **Plain GitHub repo URLs**, normalised to `https://github.com/owner/repo.git`.
7. **GitLab URLs** on any instance, keyed off the `/-/tree/` marker; subgroups supported.
8. **GitHub shorthand** `owner/repo`, `owner/repo/subpath`, `owner/repo@skill-name`.
9. **Well-known endpoints** — any other HTTP(S) URL, resolved against
   `/.well-known/agent-skills/index.json` (or `/.well-known/skills/`).
10. **Fallback: direct git URL** — anything else, which is how SSH works.

SSH is supported, though not through `parseSource`'s explicit branches: `looksLikeGitSource()`
recognises `git@`, `ssh://...git`, `github:`, and `gitlab:` prefixes, and `getOwnerRepo()` parses
both `git@host:owner/repo.git` and `ssh://git@host:7999/owner/repo.git`. A ref can be pinned
with a URL fragment: `owner/repo#v1.2.3`, and `owner/repo#v1.2.3@skill-name` selects one skill
at a ref (`parseFragmentRef()`).

Path traversal is blocked in two places — `sanitizeSubpath()` rejects any `..` segment, and
`isSubpathSafe()` re-checks after resolution.

### Which agents it targets

`src/agents.ts` is a table of `AgentConfig` records — one per member of the `AgentType` union in
`src/types.ts`, which has **118** members
(`[$ grep -c "^  | '" src/types.ts]`). Each record has a project-relative
`skillsDir` and an absolute `globalSkillsDir`. Two entries, verbatim:

```ts
  'claude-code': {
    name: 'claude-code',
    displayName: 'Claude Code',
    skillsDir: '.claude/skills',
    globalSkillsDir: join(claudeHome, 'skills'),
    detectInstalled: async () => {
      return existsSync(claudeHome);
    },
  },
  cursor: {
    name: 'cursor',
    displayName: 'Cursor',
    skillsDir: '.agents/skills',
    globalSkillsDir: join(home, '.cursor/skills'),
    detectInstalled: async () => {
      return existsSync(join(home, '.cursor'));
    },
  },
```

where `claudeHome = process.env.CLAUDE_CONFIG_DIR?.trim() || join(home, '.claude')`. Several
agents (`amp`, `cline`, `antigravity`) declare `skillsDir: '.agents/skills'` — i.e. they read the
canonical directory directly and need no symlink.

The breadth of the target list is best shown by `AGENT_PROJECT_SKILL_DIRS` in `src/skills.ts`,
the 28 project directories the discovery walk treats as skill containers. Verbatim, in source
order: `.agents/skills`, `.claude/skills`, `.cline/skills`, `.codebuddy/skills`,
`.codex/skills`, `.commandcode/skills`, `.continue/skills`, `.github/skills`, `.goose/skills`,
`.grok/skills`, `.iflow/skills`, `.junie/skills`, `.kilocode/skills`, `.kimchi/skills`,
`.kiro/skills`, `.minimax/skills`, `.mux/skills`, `.neovate/skills`, `.opencode/skills`,
`.openhands/skills`, `.pi/skills`, `.posit/assistant/skills`, `.qoder/skills`, `.roo/skills`,
`.trae/skills`, `.windsurf/skills`, `.zcode/skills`, `.zencoder/skills`.

Note that Cursor's `skillsDir` is `.agents/skills` for project scope but `~/.cursor/skills` for
global scope, so a global install symlinks into `~/.cursor/skills` while a project install writes
straight to the canonical `.agents/skills` — the asymmetry is per-agent, not per-scope.

### How discovery finds your skill in a repo

`discoverSkills()` in `src/skills.ts`:

- If the search root itself has a `SKILL.md`, that skill is taken and the walk **returns early**
  unless `--full-depth` is passed. A repo root `SKILL.md` therefore hides everything below it.
- Otherwise it checks a priority list: the root, `skills/`, `skills/.curated`,
  `skills/.experimental`, `skills/.system`, then each of the `AGENT_PROJECT_SKILL_DIRS`.
- Known container dirs are walked three levels deep (`DEFAULT_SKILL_CONTAINER_DEPTH = 3`); the
  repo root stays at depth 1 so that stray `examples/foo/SKILL.md` files are not picked up.
- Directories `node_modules`, `.git`, `dist`, `build`, `__pycache__` are skipped
  (`SKIP_DIRS`).
- A skill with `metadata.internal === true` is hidden unless `INSTALL_INTERNAL_SKILLS=1` or the
  user asked for it by name.

`[SKILLS-CLI]` ships exactly one skill of its own, at `skills/find-skills/SKILL.md` — the
`skills/<name>/SKILL.md` layout, which is the one that "just works".

## A7. Shipping a skill inside a Go CLI's repo

Combining A5 and A6, the layout that makes `skills add <owner>/<repo>` work with no flags:

```
herdr-cron/
├── go.mod
├── cmd/herdr-cron/
├── skills/
│   └── herdr-cron/
│       ├── SKILL.md
│       └── references/
│           └── COMMANDS.md
└── ...
```

Why this exact shape, with citations:

- `skills/` is second in `discoverSkills()`'s priority list `[SKILLS-CLI]` `src/skills.ts`.
- `skills/<name>/SKILL.md` matches what `[SKILLS-CLI]` does for its own skill.
- **Do not** put a `SKILL.md` at the repo root: `discoverSkills()` returns early on it and hides
  `skills/` unless the user passes `--full-depth` `[SKILLS-CLI]`.
- `references/` one level deep matches `[AS-SPEC]`'s recommended structure and its "Keep file
  references one level deep from `SKILL.md`" rule.
- The skill directory name must equal the frontmatter `name`, per `[AS-SPEC]`'s "Must match the
  parent directory name". It also determines the slash-command in Claude Code: "In a personal or
  project skill, `name` sets only the display label... and the command still comes from the
  directory name" `[CC-SKILLS]`.

Install commands a user (or an agent) can then run, all valid per `parseSource()`
`[SKILLS-CLI]`:

```bash
npx skills add <owner>/herdr-cron              # shorthand, latest default branch
npx skills add <owner>/herdr-cron -g -y        # global, no prompts
npx skills add <owner>/herdr-cron#v0.3.0       # pinned to a tag
npx skills add https://github.com/<owner>/herdr-cron/tree/main/skills/herdr-cron
npx skills add ./skills/herdr-cron             # local path, for development
```

### There is a second distribution channel: embed the skill in the binary

`herdr` on this machine does this, and it is worth copying. `[$ herdr --help]` lists:

```
  --skill             Print the agent skill file and exit
```

And the output is **byte-identical** to the installed skill file:

```
$ herdr --skill > /tmp/herdr-skill.md
$ diff -q /tmp/herdr-skill.md ~/.claude/skills/herdr/SKILL.md
IDENTICAL
```

`[$ herdr --skill | wc -l]` → 195 lines, exit 0.

`herdr` goes further and advertises the flag from its own help output for agents that stumble in
without the skill (`[$ herdr api --help]`):

```
Are you an AI? Use these resources ONLY IF your task specifically asks you to:
  ...
  Control Herdr panes, agents, or workspaces:
    SKIP if a Herdr skill is already in your context. Otherwise run: herdr --skill
```

The Go mechanism is `//go:embed skills/herdr-cron/SKILL.md`, which costs one directive and
guarantees the shipped skill can never be a different version from the shipped binary. This is
a genuinely better property than a separately-versioned skill package, and it costs nothing.

---

# Part B — CLI ergonomics for agents

## B1. cobra vs urfave/cli/v3

Both are mature, both do ~15 subcommands without complaint. The differences that matter are
narrower than the usual framework debate.

### Dependency weight

`[COBRA]` `go.mod`:

```
module github.com/spf13/cobra

go 1.15

require (
	github.com/cpuguy83/go-md2man/v2 v2.0.6
	github.com/inconshreveable/mousetrap v1.1.0
	github.com/spf13/pflag v1.0.9
	go.yaml.in/yaml/v3 v3.0.4
)
```

Four requires look worse than they are. `[$ grep -rln "go-md2man" --include=*.go .]` in the
clone returns exactly `./doc/man_docs.go` — the man-page generator, a separate package. The
root package's only external imports are `github.com/inconshreveable/mousetrap` and
`github.com/spf13/pflag`
(`[$ grep -h '^\s*"github\|^\s*"go\.' *.go | grep -v _test | sort -u]`). So the *compiled*
dependency surface is pflag + mousetrap; `go.sum` still carries md2man and yaml.

`[URFAVE]` `go.mod`:

```
module github.com/urfave/cli/v3

go 1.22

require github.com/stretchr/testify v1.12.1

require go.yaml.in/yaml/v3 v3.0.5 // indirect
```

Its only external imports across all non-test files are testify's `assert` and `require`. So
urfave/cli/v3 has **zero runtime dependencies**. Real difference, small in absolute terms. Note
also `go 1.15` vs `go 1.22`: cobra supports much older toolchains, irrelevant for a greenfield
project but it explains some of its API's shape.

### Command tree ergonomics

`[COBRA]` `command.go` — a `Command` is a struct with `Use`, `Short`, `Long`, `Example`,
`Aliases`, `SuggestFor`, `GroupID`, `Args PositionalArgs`, and a documented five-phase run
sequence (each phase has both a `Run` and an error-returning `RunE` field, so ten fields):

```go
	// The *Run functions are executed in the following order:
	//   * PersistentPreRun()
	//   * PreRun()
	//   * Run()
	//   * PostRun()
	//   * PersistentPostRun()
```

Children are attached imperatively with `AddCommand`. `GroupID` is what produces gh's grouped
help ("CORE COMMANDS" / "ACTIONS COMMANDS").

`[URFAVE]` `command.go` — a `Command` holds `Commands []*Command` **as a field**, so the whole
tree is one nested literal. Hooks are `Before`, `After`, `Action`, plus `ArgValidator`,
`CommandNotFound`, `OnUsageError`, `InvalidFlagAccessHandler`.

The declarative tree is nicer to read for 15 commands; the imperative one is nicer to split
across files. Taste, not correctness.

### The JSON-tagged struct — a real, underrated urfave advantage

Every field of urfave's `Command` carries a JSON tag `[URFAVE]` `command.go`:

```go
type Command struct {
	// The name of the command
	Name string `json:"name"`
	// A list of aliases for the command
	Aliases []string `json:"aliases"`
	// A short description of the usage of this command
	Usage string `json:"usage"`
	...
	// List of child commands
	Commands []*Command `json:"commands"`
	// List of flags to parse
	Flags []Flag `json:"flags"`
	...
	Action ActionFunc `json:"-"`
```

Behaviour fields are `json:"-"`; description fields are serialisable. That means a
`herdr-cron schema` subcommand that emits the entire command tree as JSON is roughly
`json.Marshal(rootCmd)`. cobra has no equivalent — you would walk `cmd.Commands()` and
`cmd.Flags().VisitAll()` yourself. For a CLI whose primary caller is a program, this is the
single most interesting difference between the two libraries.

**Tested, and it works — including the flags.** A three-level tree
(`herdr-cron` → `job` → `list` with a `StringFlag` and a `BoolFlag`) built against
`github.com/urfave/cli/v3` and passed to `json.MarshalIndent` produced, abridged:

```json
{
  "name": "herdr-cron",
  "usage": "schedule tasks",
  "commands": [
    { "name": "job", "usage": "manage jobs", "commands": [
        { "name": "list", "usage": "list jobs", "flags": [
            { "name": "state", "usage": "filter by state", "required": false, "hidden": false,
              "defaultValue": "", "aliases": null, "takesFileArg": false, "onlyOnce": false },
            { "name": "json", "usage": "machine output", "required": false, "hidden": false }
          ] } ] } ]
}
```

`[$ cd /tmp/hc-research/xc-urfave && go mod tidy && go run .]`. `Flags []Flag` is an interface
slice, and it still serialises `name`, `usage`, `required`, `hidden`, `aliases`, `defaultValue`,
`takesFileArg`, and `onlyOnce`. Every string field is emitted even when empty, so the raw output
is verbose — a `herdr-cron schema` command would want its own view struct rather than dumping
`cli.Command` directly, but the data is all there without reflection.

### Generated help quality

Both are template-driven and overridable (`CustomRootCommandHelpTemplate` /
`CustomHelpTemplate` in `[URFAVE]`; `SetHelpTemplate` / `SetUsageTemplate` in `[COBRA]`).

Best available evidence is the installed binaries. `gh` is cobra-based; `[$ gh issue list --help]`
produces:

```
USAGE
  gh issue list [flags]

ALIASES
  ls

FLAGS
      --app string         Filter by GitHub App author
  -a, --assignee string    Filter by assignee
  ...
INHERITED FLAGS
      --help                     Show help for command
  -R, --repo [HOST/]OWNER/REPO   Select another repository using the [HOST/]OWNER/REPO format

EXAMPLES
  $ gh issue list --label "bug" --label "help wanted"
```

The `INHERITED FLAGS` section — persistent flags separated from local ones — is a cobra concept
(`PersistentFlags()`), and it is genuinely useful to a reader, human or machine.

`glab` is also cobra-based but with a heavily customised renderer; `[$ glab issue list --help]`
boxes its section headers and pads with trailing whitespace, which is *worse* for a machine
parser. Neither is a property of the library.

### Shell completion

`[COBRA]` ships four generators as subcommands of a generated `completion` command:
`bash`, `zsh`, `fish`, `powershell` (`completions.go`, `Use:` values at lines 805/840/879/904).
It also exposes a hidden machine protocol:

```go
	ShellCompRequestCmd = "__complete"
	ShellCompNoDescRequestCmd = "__completeNoDesc"
```

with a `ShellCompDirective` bitmask (`ShellCompDirectiveError = 1 << iota`, then `NoSpace`,
`NoFileComp`, `FilterFileExt`, `FilterDirs`, `KeepOrder`; `ShellCompDirectiveDefault = 0`).

**This protocol is directly useful to an agent.** Verified on `gh`:

```
$ gh __complete ""
alias	Create command shortcuts
api	Make an authenticated GitHub API request
auth	Authenticate gh and git with GitHub
...
```

Tab-separated `name<TAB>description`, one per line, terminated by a directive line. And
`[$ gh __complete "issue "]` returns `:4` plus
`Completion ended with directive: ShellCompDirectiveNoFileComp`. `glab __complete ""` behaves
identically. An agent that has a cobra binary and no skill can enumerate the entire command tree
for free — no `--help` parsing required.

`[URFAVE]` ships four too — `bash`, `zsh`, `fish`, `pwsh` — as embedded scripts
(`completion.go`, `//go:embed autocomplete`, `completionShells = []string{"bash", "zsh", "fish", "pwsh"}`)
gated on `EnableShellCompletion bool`. Its protocol flag is
`completionFlag = "--generate-shell-completion"`, documented as "supposed to only be used by the
completion script itself". It is a flag rather than a hidden subcommand, and there is no
directive bitmask.

A downstream detail: goreleaser's Homebrew cask config has a
`generate_completions_from_executable.shell_parameter_format` field whose "Known values" include
`cobra` and `clap` and `click` `[GORELEASER]`. cobra is a first-class citizen of the packaging
ecosystem; urfave is not named.

### Exit-code handling

`[COBRA]`: `Execute()` returns an `error` and does not exit. Error printing is controlled by
`SilenceErrors` / `SilenceUsage` and the message prefix by `SetErrPrefix`. Mapping errors to exit
codes is entirely yours — which is how `gh` gets its five-code scheme (see B3).

`[URFAVE]`: has this built in. `errors.go`:

```go
// ExitCoder is the interface checked by `Command` for a custom exit code.
type ExitCoder interface {
```

```go
func Exit(message any, exitCode int) ExitCoder {
```

```go
func HandleExitCoder(err error) {
	if err == nil {
		return
	}

	if exitErr, ok := err.(ExitCoder); ok {
		if msg := err.Error(); msg != "" {
			if _, ok := exitErr.(ErrorFormatter); ok {
				_, _ = fmt.Fprintf(ErrWriter, "%+v\n", err)
			} else {
				_, _ = fmt.Fprintln(ErrWriter, err)
			}
		}
		OsExiter(exitErr.ExitCode())
		return
	}
```

`var OsExiter = os.Exit` is a package variable, so it is testable. `MultiError` collapses to the
last found code, or 1.

### The tradeoff, stated plainly

| Axis | cobra | urfave/cli/v3 |
| --- | --- | --- |
| Compiled deps | pflag + mousetrap | none |
| Tree definition | imperative `AddCommand` | declarative `Commands []*Command` |
| Command tree as JSON | write it yourself | `json.Marshal` (struct is fully tagged) |
| Completion shells | bash, zsh, fish, powershell | bash, zsh, fish, pwsh |
| Machine completion protocol | `__complete` subcommand + directive bitmask; **agent-usable today** | `--generate-shell-completion` flag, no directives |
| Exit codes | you map errors → codes | `ExitCoder` / `HandleExitCoder` built in |
| Help sections | local vs `INHERITED FLAGS`, command groups via `GroupID` | categories via `Category` |
| Ecosystem recognition | named by goreleaser, used by gh/glab/kubectl | less packaging support |
| `--json` | neither library provides it; both leave it to you | same |

Neither library gives you `--json`. That is application code either way (B2).

The decision hinges on which you value more: **cobra's `__complete` protocol and ecosystem
gravity**, or **urfave's serialisable command tree and built-in exit-code plumbing**. If
herdr-cron plans a `herdr-cron schema --json` that describes its own surface (and for an
agent-facing CLI that is a strong idea), urfave saves you a reflection walk. If herdr-cron wants
agents to be able to discover it with no skill installed at all, cobra's `__complete` already
does that.

## B2. Machine-readable output

Two conventions exist in the wild and they are genuinely different.

### The `gh` convention: `--json <fields>` + `--jq` + `--template`

`[$ gh issue list --help]`:

```
  -q, --jq expression      Filter JSON output using a jq expression
      --json fields        Output JSON with the specified fields
  -t, --template string    Format JSON output using a Go template; see "gh help formatting"
```

`[$ gh help formatting]` gives the semantics verbatim:

> By default, the result of `gh` commands are output in line-based plain text format.
> Some commands support passing the `--json` flag, which converts the output to JSON format.
>
> The `--json` flag requires a comma separated list of fields to fetch. **To view the possible JSON
> field names for a command omit the string argument to the `--json` flag when you run the command.**
> Note that you must pass the `--json` flag and field names to use the `--jq` or `--template` flags.
>
> The `--jq` flag requires a string argument in jq query syntax... **The `jq` utility does not need
> to be installed on the system to use this formatting directive.** When connected to a terminal,
> the output is automatically pretty-printed.

And the before/after from the same doc:

```
  # default output format
  $ gh pr list
  Showing 23 of 23 open pull requests in cli/cli

  #123  A helpful contribution          contribution-branch              about 1 day ago

  # adding the --json flag with a list of field names
  $ gh pr list --json number,title,author
  [
    {
	  "author": {
	    "login": "monalisa"
	  },
	  "number": 123,
	  "title": "A helpful contribution"
    },
```

Properties worth stealing:

- **Field selection is mandatory and self-documenting.** `--json` with no argument lists the
  available field names. That is a discovery mechanism an agent can use without reading docs.
- **`--jq` is embedded**, not shelled out. Worth remembering that `jq` may not be the binary you
  think: on *this* machine `[$ jq --version]` reports `jaq 2.3.0`, a reimplementation shadowing
  `jq` in `PATH`, and its exit codes differ from real jq's (see B3). A CLI that tells an agent
  to "pipe through jq" is depending on an unowned binary.

### The `glab` convention: `-O json`

`[$ glab issue list --help]`:

```
    --jq                Filter JSON output with a jq expression.
    -O --output         Options: 'text' or 'json'. (text)
    -F --output-format  Options: 'details', 'ids', 'urls'. (details)
```

A mode switch rather than a projection. Simpler to implement, no field discovery, and no way to
ask for a subset. Note the two orthogonal flags `--output` and `--output-format`, which is
exactly the naming collision this style invites.

### The `herdr` convention: JSON always, in an envelope

The third option, and the one an agent-first CLI should look at hardest. herdr does not have a
`--json` flag at all; control commands simply return JSON.

`[$ herdr pane list]` — exit 0, stdout:

```json
{"id":"cli:pane:list","result":{"panes":[{"agent":"omp","agent_session":{...},"agent_status":"idle","cwd":"/home/huke","focused":false,"foreground_cwd":"/tmp","pane_id":"w1S:p1","revision":53,...}]}}
```

`[$ herdr pane get w9S:p99]` — exit **1**, stdout empty, stderr:

```json
{"error":{"code":"pane_not_found","message":"pane w9S:p99 not found"},"id":"cli:pane:get"}
```

`[$ herdr bogus-cmd]` — exit **2**, stdout empty, stderr plain text:

```
unknown command: bogus-cmd
run 'herdr --help' for usage
```

Three things make this good for an agent, and they generalise:

1. **One envelope shape.** `{"id": "<command-id>", "result": {...}}` on success,
   `{"id": "<command-id>", "error": {"code": ..., "message": ...}}` on failure. The `id`
   correlates the response to the command that produced it, which matters when an agent has
   several calls in flight or is reading a log.
2. **A stable machine-readable `error.code`.** `pane_not_found` is a string an agent can branch
   on. A prose message is not.
3. **stdout/stderr are cleanly split.** Success on stdout, failure on stderr, never both.

What an agent needs in order to never parse a table — synthesising all three CLIs:

- Structured output on **every** command that returns data, not just the list commands.
- **Stable field names** across versions. gh's field-selection model makes this an explicit
  contract; herdr's envelope makes it implicit. Either is better than a table.
- Errors as **structured data on stderr** with a code, so a failed call is as parseable as a
  successful one. Only herdr of the three does this: `[$ gh issue list]` outside a git repo gives
  exit 1 with the plain-text stderr `failed to run git: fatal: not a git repository (or any of the parent directories): .git`.
- **No ANSI escapes in machine mode.** `[SKILLS-CLI]` `src/list.ts` labels its own flag
  "Output as JSON (machine-readable, no ANSI codes)" and returns before any colour codes are
  emitted.
- **Identifiers in the output.** The `herdr` skill's rule — "Parse IDs from JSON responses. Do
  not derive them from sidebar order or examples" `[LOCAL-SKILLS]` — only works because every
  object carries its own `pane_id`.

## B3. Exit codes and error surfaces

Three real schemes, all verified by running the binaries.

### `gh` — five codes, defined in source

`[GH-SRC]` `internal/ghcmd/cmd.go`:

```go
type exitCode int

const (
	exitOK      exitCode = 0
	exitError   exitCode = 1
	exitCancel  exitCode = 2
	exitAuth    exitCode = 4
	exitPending exitCode = 8
)
```

and `cmd/gh/main.go` is just `os.Exit(int(ghcmd.Main()))`.

Observed: `[$ gh issue list --bogus]` → 1; `[$ gh nosuchcmd]` → 1; `[$ gh issue list]` outside a
repo → 1. So in practice `2` means *user cancelled* (not a syntax error), `4` means *not
authenticated*, `8` means *pending*. Usage errors collapse into `1`.

### `glab` — everything is 1

`[$ glab issue list --bogus]` → exit 1, with a boxed `ERROR` banner on stderr.

### `herdr` — the 0/1/2 split

The Herdr skill in this harness documents `[LOCAL-SKILLS]` `~/.claude/skills/herdr/SKILL.md`:

> CLI server errors are JSON on stderr with exit status 1. CLI syntax errors exit with status 2.

**Both claims verified against the installed binary** (see B2 for the exact commands and
output): `herdr pane get w9S:p99` → 1 with a JSON error envelope on stderr; `herdr bogus-cmd`
and `herdr pane read --pane w9S:p99` (an unknown option) → 2 with plain text on stderr. The
documentation and the binary agree.

### `jq` on this machine — a cautionary aside

`[$ jq --version]` → `jaq 2.3.0`. Observed:
`echo '{}' | jq -e '.missing'` → 1; `echo 'bad json' | jq .` → 5 (`Error: failed to parse: value expected`);
`jq --bogus` → 2 (`error: Error: unknown flag: --bogus`). Real jq documents 2 for usage errors
and 5 for `-e` with a false/null result, so the two implementations do not agree on which
category gets 5. Do not build an agent workflow that branches on jq's exit code.

### The convention worth adopting

The 0/1/2 split, herdr-style, for one concrete reason: it lets a caller distinguish *"I wrote
the command wrong"* from *"the command was right and the system said no"* **without parsing any
text**. That distinction drives completely different agent behaviour — reread the help versus
retry, back off, or report. gh's scheme is richer but spends its extra codes on categories
(auth, cancel, pending) that a local scheduler does not have, while folding syntax errors into
the generic `1`. glab's single code forces text parsing for everything.

Proposed for herdr-cron: `0` success; `1` operation failed (JSON error envelope on stderr);
`2` usage/syntax error (plain text on stderr, plus usage). If a third category is genuinely
needed later — say "scheduler daemon not running" — it deserves a distinct code rather than a
message an agent has to regex.

## B4. Config, state, and a database on disk

### `os.UserConfigDir` and `os.UserCacheDir` — the stdlib answer

`[GO-OS]` `/usr/local/go/src/os/file.go`, verbatim (error branches elided with `...`):

```go
func UserConfigDir() (string, error) {
	var dir string

	switch runtime.GOOS {
	case "windows":
		dir = Getenv("AppData")
		...
	case "darwin", "ios":
		dir = Getenv("HOME")
		...
		dir += "/Library/Application Support"
	...
	default: // Unix
		dir = Getenv("XDG_CONFIG_HOME")
		if dir == "" {
			dir = Getenv("HOME")
			...
			dir += "/.config"
		} else if !filepathlite.IsAbs(dir) {
			return "", errors.New("path in $XDG_CONFIG_HOME is relative")
		}
	}

	return dir, nil
}
```

`UserCacheDir` is the same shape with `LocalAppData` / `~/Library/Caches` / `$XDG_CACHE_HOME`
or `~/.cache`.

**There is no `os.UserStateDir`.** `[$ go doc os | grep "func User"]` returns exactly three:
`UserCacheDir`, `UserConfigDir`, `UserHomeDir`. Confirmed by
`[$ grep -c "func UserStateDir" /usr/local/go/src/os/*.go]` → no match. For a scheduler this is
the gap that matters, because a job database is neither config nor cache.

### `adrg/xdg` — adds `StateHome`, disagrees with stdlib on Windows

`[XDG]` exposes `DataHome`, `ConfigHome`, `StateHome`, `CacheHome`, `RuntimeDir` as package
variables plus `ConfigFile()`, `StateFile()`, `CacheFile()`, `DataFile()` helpers that create the
parent directory.

`paths_unix.go` (build-tagged `linux || freebsd || openbsd || ...`):

```go
	baseDirs.dataHome = pathutil.EnvPath(envDataHome, filepath.Join(home, ".local", "share"))
	baseDirs.configHome = pathutil.EnvPath(envConfigHome, filepath.Join(home, ".config"))
	baseDirs.stateHome = pathutil.EnvPath(envStateHome, filepath.Join(home, ".local", "state"))
	baseDirs.cacheHome = pathutil.EnvPath(envCacheHome, filepath.Join(home, ".cache"))
	baseDirs.runtime = pathutil.EnvPath(envRuntimeDir, filepath.Join("/run/user", strconv.Itoa(os.Getuid())))
```

`paths_darwin.go`:

```go
	homeAppSupport := filepath.Join(home, "Library", "Application Support")
	baseDirs.dataHome = pathutil.EnvPath(envDataHome, homeAppSupport)
	baseDirs.configHome = pathutil.EnvPath(envConfigHome, homeAppSupport)
	baseDirs.stateHome = pathutil.EnvPath(envStateHome, homeAppSupport)
	baseDirs.cacheHome = pathutil.EnvPath(envCacheHome, filepath.Join(home, "Library", "Caches"))
```

`paths_windows.go`:

```go
	baseDirs.dataHome = pathutil.EnvPath(envDataHome, kf.localAppData)
	baseDirs.configHome = pathutil.EnvPath(envConfigHome, kf.localAppData)
	baseDirs.stateHome = pathutil.EnvPath(envStateHome, kf.localAppData)
	baseDirs.cacheHome = pathutil.EnvPath(envCacheHome, filepath.Join(kf.localAppData, "cache"))
```

**The disagreement:** on Windows, `os.UserConfigDir()` returns `%AppData%` (Roaming) `[GO-OS]`,
while `xdg.ConfigHome` returns `LocalAppData` `[XDG]`. Roaming means the file follows the user
across domain-joined machines; Local does not. For a scheduler with a machine-local job database
and machine-local absolute paths in job commands, **Local is the correct choice and stdlib is
wrong for this use case** — a roaming job DB replicated onto a different machine would fire jobs
against paths that do not exist there.

`[XDG]` also honours `XDG_*` environment variables on **all** platforms, including Windows and
macOS, whereas `[GO-OS]` honours them only on Unix. That is a feature for testing (point
`XDG_STATE_HOME` at a temp dir) and a hazard in production (a stray env var relocates your DB).

### `kirsle/configdir` — do not use

`[CONFIGDIR]` is a single commit from 2017-01-27 with `const VERSION = "0.1.0"` and **no
`go.mod`**. It exposes `SystemConfig()`, `LocalConfig()`, `LocalCache()`, `MakePath()` — and
that is all. There is no state directory. Its Windows mapping is:

```go
func findPaths() {
	systemConfig = []string{os.Getenv("PROGRAMDATA")}
	localConfig = os.Getenv("APPDATA")
	localCache = os.Getenv("LOCALAPPDATA")
}
```

i.e. Roaming for config, matching stdlib and not xdg. It uses only the pre-Go-1.17
`// +build !windows,!darwin` constraint syntax with no `//go:build` line. It reads
`os.Getenv("HOME")` directly rather than `os.UserHomeDir()`. Nine years unmaintained, strictly
less capable than either alternative.

### Concrete resolved paths, all three OSes

Assuming an application name `herdr-cron` and no `XDG_*` overrides.

| | `os.UserConfigDir()` + app | `xdg.ConfigHome` + app | `xdg.StateHome` + app | `os.UserCacheDir()` + app |
| --- | --- | --- | --- | --- |
| **Linux** | `~/.config/herdr-cron` | `~/.config/herdr-cron` | `~/.local/state/herdr-cron` | `~/.cache/herdr-cron` |
| **macOS** | `~/Library/Application Support/herdr-cron` | `~/Library/Application Support/herdr-cron` | `~/Library/Application Support/herdr-cron` | `~/Library/Caches/herdr-cron` |
| **Windows** | `%AppData%\herdr-cron` i.e. `C:\Users\<u>\AppData\Roaming\herdr-cron` | `%LocalAppData%\herdr-cron` i.e. `C:\Users\<u>\AppData\Local\herdr-cron` | `%LocalAppData%\herdr-cron` | `%LocalAppData%\herdr-cron` |

Derived directly from the source excerpts above (`[GO-OS]`, `[XDG]`); the Windows and macOS rows
were not executed on those platforms — see "Could not verify".

For reference, herdr itself puts its config at `~/.config/herdr/config.toml` on Linux
(`[$ herdr --help]`, last line: `Config: /home/huke/.config/herdr/config.toml`).

Note that on macOS all three of config, data, and state collapse to the same directory. If you
want them separable on macOS you must add your own subdirectories (`config/`, `state/`).

## B5. Running as a background service, cross-platform

A scheduler that dies with the terminal is not a scheduler. This section is the hardest
cross-platform problem in the project.

### `kardianos/service` — still maintained, still covers all three

`[KSERVICE]` `go.mod` is one dependency:

```
module github.com/kardianos/service

go 1.23.0

require golang.org/x/sys v0.34.0
```

Its package doc claims: "Currently supports Windows, Linux/(systemd | Upstart | SysV | OpenRC),
and OSX/Launchd." The file list is broader than the doc
(`[$ ls /tmp/hc-research/kardianos-service/*.go]`): `service_systemd_linux.go`,
`service_upstart_linux.go`, `service_sysv_linux.go`, `service_openrc_linux.go`,
`service_procd_linux.go`, `service_rcs_linux.go`, `service_darwin.go`, `service_windows.go`,
`service_freebsd.go`, `service_aix.go`, `service_solaris.go`. So the doc undercounts by three
Linux init systems (procd, rcs) and three OSes.

The API is small. `service.go`:

```go
type Interface interface {
	// Start provides a place to initiate the service. The service doesn't
	// signal a completed start until after this function returns, so the
	// Start function must not take more then a few seconds at most.
	Start(s Service) error

	// Stop provides a place to clean up program execution before it is terminated.
	// It should not take more then a few seconds to execute.
	// Stop should not call os.Exit directly in the function.
	Stop(s Service) error
}
```

```go
type Config struct {
	Name        string   // Required name of the service. No spaces suggested.
	DisplayName string   // Display name, spaces allowed.
	Description string   // Long description of service.
	UserName    string   // Run as username.
	Arguments   []string // Run with arguments.
	Executable string
	Dependencies []string
	WorkingDirectory string // Initial working directory.
	ChRoot           string
	Option KeyValue
	EnvVars map[string]string
}

// ControlAction list valid string texts to use in Control.
var ControlAction = [5]string{"start", "stop", "restart", "install", "uninstall"}
```

and `func Interactive() bool` — "returns false if running under the OS service manager and true
otherwise", which is how one binary serves both `herdr-cron daemon` in a terminal and
`herdr-cron` under systemd.

**The critical option is `UserService`.** `service.go`:

```go
	optionUserService          = "UserService"
	optionUserServiceDefault   = false
```

With it set:

- **Linux/systemd** (`service_systemd_linux.go`): the unit is written to
  `filepath.Join(homeDir, ".config/systemd/user")` and every `systemctl` invocation gains
  `--user`:

```go
	if s.isUserService() {
		return run("systemctl", append([]string{action, "--user"}, args...)...)
	}
	return run("systemctl", append([]string{action}, args...)...)
```

- **macOS/launchd** (`service_darwin.go`): the plist goes to
  `homeDir + "/Library/LaunchAgents/" + s.Name + ".plist"` instead of `/Library/LaunchDaemons/`.

- **Windows** (`service_windows.go`): **there is no user-service path.** `Install()` calls
  `mgr.Connect()`, which opens the Service Control Manager and requires Administrator. There is
  no `UserService` branch in the file at all. This is the asymmetry that shapes the whole
  design.

The generated systemd unit is a hand-rolled mini-template (`systemdScript` in
`service_systemd_linux.go`), and its `[Install]` section is hardcoded:

```
{{end}}RestartSec=120
EnvironmentFile=-/etc/sysconfig/{{Name}}

{{range EnvVars}}{{.}}
{{end}}[Install]
WantedBy=multi-user.target
```

`WantedBy=multi-user.target` is a **system** target. For a `--user` unit the correct target is
`default.target`. This is a real bug for user services: `systemctl --user enable` against a unit
wanting `multi-user.target` will not produce the intended boot-time symlink. Also note the
unconditional `RestartSec=120` — a two-minute restart delay, hardcoded, for a scheduler.

### Plain systemd user units — Linux, no admin

`[SYSTEMD-MAN]` `systemd.unit(5)`, "Table 2. Load path when running in user mode (--user)"
lists, among others:

```
│ $XDG_CONFIG_HOME/systemd/user or         │ User configuration               │
│ $HOME/.config/systemd/user               │ ($XDG_CONFIG_HOME is used if     │
│                                          │ set, ~/.config otherwise)        │
```

So the install is:

```bash
mkdir -p ~/.config/systemd/user
cat > ~/.config/systemd/user/herdr-cron.service <<'EOF'
[Unit]
Description=herdr-cron scheduler

[Service]
ExecStart=%h/.local/bin/herdr-cron daemon
Restart=on-failure

[Install]
WantedBy=default.target
EOF
systemctl --user daemon-reload
systemctl --user enable --now herdr-cron.service
```

No admin. But a user manager normally dies at logout, so for a scheduler you also need lingering
— `[SYSTEMD-MAN]` `loginctl(1)`:

> **enable-linger** [USER...], **disable-linger** [USER...]
> Enable/disable user lingering for one or more users. If enabled for a specific user, a user
> manager is spawned for the user at boot and kept around after logouts. **This allows users who
> are not logged in to run long-running services.** ... Added in version 233.

```bash
loginctl enable-linger "$USER"
```

`enable-linger` for *your own* user is normally permitted by polkit without a password;
enabling it for another user is not. systemd 255 is installed here
(`[$ systemctl --version]`) and `[$ which loginctl]` → `/usr/bin/loginctl`.

### launchd — macOS, no admin for a LaunchAgent

`[APPLE-LAUNCHD]` states the placement rule: "Property list files describing daemons are
installed in `/Library/LaunchDaemons`, and those describing agents are installed in
`/Library/LaunchAgents` or in the `LaunchAgents` subdirectory of an individual user's `Library`
directory."

And the lifecycle: "When a user logs in, a per-user `launchd` is started. It ... loads the
parameters for each launch-on-demand user agent from the property list files found in
`/System/Library/LaunchAgents`, `/Library/LaunchAgents`, and the user's individual
`Library/LaunchAgents` directory. ... When the user logs out, it sends a `SIGTERM` signal to all
of the user agents that it started."

**A LaunchAgent under `~/Library/LaunchAgents` needs no admin, but it dies at logout.** There is
no launchd equivalent of `enable-linger`; surviving logout on macOS means a LaunchDaemon in
`/Library/LaunchDaemons`, which needs admin.

The required keys are `Label` and `ProgramArguments`; `KeepAlive` makes it a resident process
`[APPLE-LAUNCHD]`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
<key>Label</key>
<string>com.example.hello</string>
<key>ProgramArguments</key>
<array>
<string>hello</string>
<string>world</string>
</array>
<key>KeepAlive</key>
<true/>
</dict>
</plist>
```

Install: `launchctl load -w ~/Library/LaunchAgents/dev.herdr.cron.plist` (or the modern
`launchctl bootstrap gui/$UID <plist>`; the archived guide predates `bootstrap`, so treat the
exact subcommand as unverified — see "Could not verify").

Interesting aside: launchd can *be* the scheduler. `[APPLE-LAUNCHD]` documents `StartInterval`
(seconds) and `StartCalendarInterval` (a cron-like dict where "any missing key ... is treated as
a wildcard"). herdr-cron should not delegate to it — the point is one portable scheduling model
across three OSes — but it is worth knowing the platform already has one.

### Windows Task Scheduler / `schtasks` — no admin, and the pragmatic answer

`[MSLEARN]` gives the full syntax. The relevant schedule types:

> - **ONSTART** - Specifies that the task runs every time the system starts. ...
> - **ONLOGON** - Specifies that the task runs whenever a user (any user) logs on. ...

and the privilege flag:

> `/rl <level>` — Specifies the Run Level for the job. Acceptable values are **LIMITED**
> (scheduled tasks will be ran with the least level of privileges, such as Standard User
> accounts) and **HIGHEST** (scheduled tasks will be ran with the highest level of privileges,
> such as Superuser accounts). **The default value is Limited.**

So:

```cmd
schtasks /create /tn "herdr-cron" /tr "%LOCALAPPDATA%\herdr-cron\herdr-cron.exe daemon" /sc ONLOGON /rl LIMITED /f
```

`/f` "Specifies to create the task and suppress warnings if the specified task already exists" —
i.e. idempotent install. `/delete /tn "herdr-cron" /f` removes it.

Creating a task that runs as the current user requires no elevation; `/ru SYSTEM`, `/rl HIGHEST`,
and `/s <remote-computer>` do. `[MSLEARN]` notes `/np` ("No password is stored. The task runs
non-interactively as the given user. Only local resources are available.") for the
no-stored-credential case.

`ONLOGON` still means "not running before anyone logs in" — the Windows analogue of the launchd
logout problem, inverted. A true boot-time Windows service means the SCM and admin rights.

### The do-nothing option

Run only while the TUI or an explicit `herdr-cron daemon` is in the foreground. Zero install,
zero privileges, zero platform-specific code, and no possibility of a stale service pointing at
a deleted binary. Missed jobs are handled by catch-up-on-start logic — which the scheduling
engine (gocron, per the sibling document) has to have anyway for the suspend/resume case.

For a tool whose users are coding agents inside Herdr, this is more defensible than it sounds:
Herdr is itself a persistent session host, and a `herdr-cron daemon` pane inside a Herdr session
already survives terminal exit. The cost is that jobs do not fire when Herdr is not running.

### Comparison

| Mechanism | Admin needed | Survives logout | Survives reboot | Install command |
| --- | --- | --- | --- | --- |
| systemd user unit + linger | No (`enable-linger` for self is polkit-allowed) | Yes, with linger | Yes, with linger | `systemctl --user enable --now herdr-cron` |
| systemd system unit | Yes | Yes | Yes | `sudo systemctl enable --now herdr-cron` |
| launchd LaunchAgent (`~/Library/LaunchAgents`) | No | **No** (`SIGTERM` at logout `[APPLE-LAUNCHD]`) | Yes, at next login | `launchctl load -w <plist>` |
| launchd LaunchDaemon (`/Library/LaunchDaemons`) | Yes | Yes | Yes | `sudo launchctl load -w <plist>` |
| Windows Service (SCM) | **Yes** (`mgr.Connect()` `[KSERVICE]`) | Yes | Yes | `herdr-cron service install` |
| Windows `schtasks /sc ONLOGON /rl LIMITED` | No | No | Yes, at next logon | `schtasks /create ... /f` |
| Foreground only | No | No | No | none |

`kardianos/service` gives you rows 1–5 behind one API, at the cost of the `WantedBy` bug for
user units and a hardcoded `RestartSec=120`. It does **not** cover the `schtasks` row, which is
the only no-admin Windows option. A complete story therefore needs `kardianos/service` *plus*
a `schtasks` path, or hand-rolled installers for all four.

## B6. Embedded persistence, and the cgo question

The deciding factor is Windows cross-compilation. It was tested, not reasoned about.

### `modernc.org/sqlite` v1.57.0 — pure Go, verified

No cgo anywhere:
`[$ grep -rln 'import "C"' $(go env GOMODCACHE)/modernc.org/sqlite@v1.57.0 --include=*.go]`
returns nothing.

Hard proof of the property that matters. From this Linux machine:

```
$ cd /tmp/hc-research/xcompile && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o hcxc.exe .
exit=0
$ file hcxc.exe
hcxc.exe: PE32+ executable (console) x86-64, for MS Windows, 16 sections
```

for a program whose entire body is `sql.Open("sqlite", "file:test.db")` with
`_ "modernc.org/sqlite"` imported and `require modernc.org/sqlite v1.57.0` pinned in `go.mod`.

The cost is binary size. Measured with identical flags:

| Program | Windows/amd64 binary |
| --- | --- |
| `fmt.Println` only (baseline) | 2,459,648 bytes |
| `+ github.com/mattn/go-sqlite3` (cgo disabled → stub) | 3,010,560 bytes |
| `+ go.etcd.io/bbolt` | 3,498,496 bytes |
| `+ modernc.org/sqlite` | **9,519,104 bytes** |

`[$ CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o out-<name>.exe .]` in four throwaway
modules under `/tmp/hc-research/`. So modernc costs about **7 MB** over baseline — SQLite's C
source transpiled to Go.

Platform coverage is finite but wider than the filenames suggest. From
`[$ ls $(go env GOMODCACHE)/modernc.org/sqlite@v1.57.0/lib/ | grep -E '^sqlite_(aix|darwin|freebsd|linux|netbsd|openbsd|windows)']`:

```
darwin_amd64  darwin_arm64
freebsd_386   freebsd_amd64  freebsd_arm  freebsd_arm64
linux_386     linux_amd64    linux_arm    linux_arm64
linux_loong64 linux_ppc64le  linux_riscv64  linux_s390x
netbsd_amd64  openbsd_amd64  openbsd_arm64
windows       windows_386
```

There is no `sqlite_windows_arm64.go`, but **`windows/arm64` builds anyway** — the build tag on
`lib/sqlite_windows.go` is:

```go
//go:build windows && (amd64 || arm64)
```

even though its own header line says `// Code generated for windows/amd64 by 'generator ...'`.
Verified:

```
$ CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -o hcxc-arm64.exe .
exit=0
$ file hcxc-arm64.exe
hcxc-arm64.exe: PE32+ executable (console) Aarch64, for MS Windows, 14 sections
```

9,161,728 bytes. So the release matrix can include `windows/arm64`; note only that the arm64
build reuses translation output generated against amd64, so it is the least-exercised
configuration. `darwin/arm64` and `linux/arm64` have dedicated generated files.

Its own dependency tree is not small — `modernc.org/libc`, `modernc.org/memory`,
`modernc.org/mathutil`, `modernc.org/fileutil`, plus indirects including `github.com/google/pprof`
and `github.com/mattn/go-isatty`. Its `go.mod` also carries **seven `retract` directives**
(`[$ grep -c "^retract" .../modernc.org/sqlite@v1.57.0/go.mod]` → 7),
including `retract v1.42.0 // Accidentaly broken, reverting to v1.41.0 state`. Pin exactly and
read the retractions before bumping.

### `github.com/mattn/go-sqlite3` v1.14.50 — cgo, and it fails silently

This is the trap, and it is worse than "cross-compilation doesn't work".

**With `CGO_ENABLED=0` the build succeeds.** There is a stub, `static_mock.go`. The load-bearing
lines, verbatim:

```go
//go:build !cgo
// +build !cgo

package sqlite3

var errorMsg = errors.New("Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work. This is a stub")

func init() {
	sql.Register("sqlite3", &SQLiteDriver{})
}

func (SQLiteDriver) Open(s string) (driver.Conn, error)                        { return nil, errorMsg }
```

The driver *registers*, so `sql.Open` returns a non-nil `*sql.DB` and a nil error. The failure
surfaces only at first connection:

```
$ CGO_ENABLED=0 go build -o mattn-nocgo . && ./mattn-nocgo
ping err: Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work. This is a stub
run exit=0
```

A CI job that builds and runs `--version` will pass. Users get a runtime error on first use.

And with cgo on, cross-compiling needs a Windows toolchain you probably do not have:

```
$ CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -o mattn-win.exe .
# runtime/cgo
gcc: error: unrecognized command-line option ‘-mthreads’; did you mean ‘-pthread’?
exit=141
```

So `mattn/go-sqlite3` means either per-OS native build runners or a mingw-w64 cross toolchain in
CI. For a project whose whole premise is "cross-platform Windows/macOS/Linux", this is the
disqualifying finding.

### `go.etcd.io/bbolt` v1.5.0 — pure Go, tiny, key/value only

No cgo:
`[$ grep -rln 'import "C"' $(go env GOMODCACHE)/go.etcd.io/bbolt@v1.5.0 --include=*.go]`
returns nothing. Cross-compiles clean, +1 MB over baseline (table above).

Its own doc states the model and the caveats (`doc.go`):

> package bbolt implements a low-level key/value store in **pure Go**. It supports fully
> serializable transactions, ACID semantics, and lock-free MVCC with multiple readers and a
> single writer. ... Bolt currently works on Windows, Mac OS X, and Linux.
>
> **Only one read-write transaction is allowed at a time.**
>
> The database uses a read-only, memory-mapped data file ... this means that keys and values
> returned from Bolt cannot be changed. Writing to a read-only byte slice will cause Go to
> panic. Keys and values retrieved from the database are only valid for the life of the
> transaction.

And the process-level constraint, `db.go`:

```go
	// The database file is locked exclusively (only one process can grab the lock)
	...
	if err = flock(db, !db.readOnly, options.Timeout); err != nil {
```

with `Options.Timeout` — "the amount of time to wait to obtain a file lock" — defaulting to `0`,
i.e. block forever.

**Single-writer-process is the design consequence for herdr-cron.** If a background daemon holds
the bbolt file open read-write, a concurrently launched `herdr-cron list` cannot open it
read-write — it must either open read-only (shared `flock`) or ask the daemon over an IPC
channel. That is not a defect; it is a forced architectural decision, and it needs to be made
before the storage layer is written.

Note that bbolt's `go.mod` requires `github.com/spf13/cobra v1.10.2` (for its `bbolt` CLI
subpackage) — importing the library does not compile cobra in, but it does land in `go.sum`.

### Plain JSON files

Zero dependencies, zero bytes of binary, trivially inspectable and hand-editable — a real
advantage for a scheduler whose users are agents that may want to read the job list without
running the tool. Costs: no atomicity beyond write-temp-then-`os.Rename` (and `os.Rename` over an
existing file has historically been the sharp edge on Windows), no concurrent access story at
all, no queries, and full-file rewrite on every mutation.

For "a few hundred job definitions plus last-run timestamps" this is genuinely sufficient. For
"an append-only run history with retention" it is not.

### Summary

| Option | cgo | Windows cross-compile from Linux | Size over baseline | Model | Failure mode if misconfigured |
| --- | --- | --- | --- | --- | --- |
| `modernc.org/sqlite` v1.57.0 | **No** (verified) | Works for amd64 **and arm64** (verified, PE32+ produced for both) | ~7.0 MB | SQL, `database/sql` | 7 `retract`ed versions — pin exactly |
| `mattn/go-sqlite3` v1.14.50 | **Yes** | Fails (needs mingw-w64) | ~0.55 MB (stub only) | SQL, `database/sql` | **builds, then errors at first `Ping`** |
| `go.etcd.io/bbolt` v1.5.0 | **No** (verified) | Works (verified) | ~1.0 MB | K/V, single writer *process* | second process blocks on `flock` forever (`Timeout: 0`) |
| JSON files | No | Works | 0 | whole-file | lost writes, no concurrency story |

## B7. Release and distribution

### `go install` — free, and the weakest link

`[$ go help install]`:

> Executables are installed in the directory named by the GOBIN environment variable, which
> defaults to `$GOPATH/bin` or `$HOME/go/bin` if the GOPATH environment variable is not set.
>
> If the arguments have version suffixes (like `@latest` or `@v1.0.0`), "go install" builds
> packages in module-aware mode, ignoring the go.mod file in the current directory...

So `go install github.com/<owner>/herdr-cron/cmd/herdr-cron@latest` works from day one with no
release infrastructure. Two caveats for herdr-cron specifically: it requires a Go toolchain on
the user's machine, and version strings injected via `-ldflags` are absent — `go install` does
not run your goreleaser build flags. (Go embeds VCS info in the binary, readable via
`runtime/debug.ReadBuildInfo`, which is the usual workaround; not verified here.)

### goreleaser — multi-OS binaries plus package manifests

`[GORELEASER]` quick start:

```
goreleaser init                       # create an example .goreleaser.yaml
goreleaser check                      # validate config
goreleaser healthcheck                # verify required tooling is present
goreleaser build --single-target      # build one target for local dev
goreleaser release --snapshot --clean # full local dry run, no publish
goreleaser release --skip=publish
goreleaser release                    # real release, driven by the latest git tag
```

> To release to GitHub, export a `GITHUB_TOKEN` environment variable containing a GitHub token
> that can create releases in your repository. A classic personal access token needs the `repo`
> scope; a fine-grained token needs `contents: write` permission on the repository.
>
> GoReleaser will use the latest Git tag of your repository.

goreleaser is **not installed on this machine** (`[$ which goreleaser]` → no output), so none of
the above was executed. Cited from docs only.

### Homebrew

`[GORELEASER]` documents `homebrew_casks` ("Since v2.10"): "After releasing to GitHub, GitLab,
or Gitea, GoReleaser can generate and publish a *Homebrew Cask* into a repository (*Tap*) that
you have access to." Key fields: `name`, `binaries`, `repository.owner`/`name`, `directory`
(default `Casks`), `completions` (per-shell paths), and — usefully —
`generate_completions_from_executable`, which runs your binary at package build time:

```yaml
    generate_completions_from_executable:
      executable: "bin/myapp"
      args:
        - completions
      shell_parameter_format: cobra
      shells:
        - bash
        - zsh
        - fish
        - pwsh
```

"Known values (rendered as Ruby symbols): arg, clap, click, cobra, flag, none, typer" and
"Default: [bash, zsh, fish]. **cobra and typer also include pwsh by default**." Another small
point for cobra in B1.

**Casks vs formulae.** The old formula pipe still exists as `brews:` at
<https://goreleaser.com/customization/publish/homebrew_formulas/>, titled "Homebrew Formulas
(deprecated)", and the page opens with: "Deprecated in v2.10. Homebrew Casks should be used
instead." (The plural spelling matters: `.../homebrew_formulae/` returns **404**,
`.../homebrew_formulas/` returns **200** — `[$ curl -sS -o /dev/null -w "%{http_code}" ...]`.)

So for a new project the answer is `homebrew_casks`, not `brews`. The cask pipe is not
app-bundle-only — its config takes `binaries:` and `completions:` directly, which is what a bare
CLI needs. What you give up by not using `brews:` are the formula-only fields: `test:`
("So you can `brew test` your formula"), `install:`/`extra_install:`/`post_install:` Ruby
snippets, `dependencies:` with per-OS scoping (`os: mac` / `os: linux`), and `conflicts:` on
arbitrary formula names. None of those is load-bearing for a single static Go binary.

### Scoop — Windows

`[GORELEASER]` `scoops:` generates "a Scoop App Manifest into a repository that you have access
to". Two details relevant to a scheduler:

- `directory` — "Note that while scoop works if the manifests are in a directory, `scoop bucket
  list` will show 0 manifests if they are not in the root directory. In short, it's generally
  better to leave this empty."
- **`persist`** — "Persist data between application updates":

```yaml
    persist:
      - "data"
      - "config.toml"
```

For a tool with a job database, `persist` is not optional. Without it a Scoop upgrade orphans
the user's jobs.

The output is `<project>.json` in the bucket repo root: "Assuming that the project name is
`drumroll`, and the current tag is `v1.2.3`, the above configuration will generate a
`drumroll.json` manifest in the root of the repository specified in the bucket section."

Note that the non-`archive` formats (`msi`, `nsis`) are GoReleaser Pro only.

### winget — Windows

`[GORELEASER]` `winget:` "can generate and publish a winget manifest and commit to a git
repository, and open a pull request to `winget-pkgs` if instructed to." Required fields:
`publisher`, `short_description`, `license`. Default manifest path:
`manifests/<lowercased first char of publisher>/<publisher>/<name>/<version>`.

Publishing to the public `winget-pkgs` repository means a pull request into a
Microsoft-maintained repo with its own review process — a slower channel than a private Scoop
bucket, and not one you control the latency of.

### Distribution shape for herdr-cron

The cheapest complete story, in order of effort:

1. `go install ...@latest` — works immediately, zero infrastructure.
2. goreleaser building `linux/{amd64,arm64}`, `darwin/{amd64,arm64}`,
   `windows/{amd64,arm64}` archives attached to GitHub releases, with `goreleaser check` in CI.
   **All six targets were built successfully from this Linux machine with `CGO_ENABLED=0`**
   against `modernc.org/sqlite v1.57.0`
   (`[$ for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do CGO_ENABLED=0 GOOS=... GOARCH=... go build; done]`
   → all `exit=0`, 9.0–9.5 MB each; the two Windows targets are shown in B6). No cgo toolchain,
   no per-OS runners.
3. `homebrew_casks` into an own tap, with `generate_completions_from_executable`.
4. `scoops` into an own bucket — **with `persist`** naming the state directory.
5. `winget` last, if at all.

(What `herdr-reviewr` does for distribution is covered by the Herdr-plugin document; not
duplicated here.)

---

# Implications for herdr-cron

**Everything in this section is inference, not evidence.** It is a proposal derived from the
findings above, offered so the next reader has a concrete starting point to argue with.

## Proposed command surface

Design rules taken from the evidence: JSON by default on data commands (B2, herdr); the 0/1/2
exit split (B3, verified against herdr); machine-readable errors with a stable `code` (B2); and
a self-describing surface an agent can enumerate (B1, `gh __complete`).

```
herdr-cron                                     Launch the TUI (mouse-driven)
herdr-cron --skill                             Print the bundled SKILL.md and exit
herdr-cron --version | -V

Jobs
  herdr-cron job list      [--state active|paused|all] [--tag T]...
  herdr-cron job get       <job-id>
  herdr-cron job add       --name N --schedule EXPR --command CMD
                           [--cwd PATH] [--env K=V]... [--tag T]...
                           [--pane PANE_ID] [--timeout DUR]
                           [--on-missed skip|run-once|catch-up]
                           [--max-retries N] [--paused]
  herdr-cron job update    <job-id> [same flags as add]
  herdr-cron job rm        <job-id> [--yes]
  herdr-cron job pause     <job-id>
  herdr-cron job resume    <job-id>
  herdr-cron job run       <job-id> [--wait] [--timeout DUR]

Runs
  herdr-cron run list      [--job JOB_ID] [--status ok|failed|running] [--limit N] [--since T]
  herdr-cron run get       <run-id>
  herdr-cron run logs      <run-id> [--stream stdout|stderr|both] [--tail N] [--follow]

Scheduler
  herdr-cron daemon        [--foreground]
  herdr-cron status
  herdr-cron service install|uninstall|start|stop   [--user] [--scheduler auto|systemd|launchd|schtasks|windows-service]

Introspection
  herdr-cron schema                          Print the full command tree as JSON
  herdr-cron validate --schedule EXPR        Parse a schedule and print the next N fire times
  herdr-cron completion bash|zsh|fish|powershell
```

Fifteen data/control subcommands plus five meta commands. Global flags: `--json` /
`--output json|text` (JSON is the default for every `list`/`get`; `text` is opt-in for humans),
`--config PATH`, `--state-dir PATH`, `--quiet`.

Notes on specific choices:

- **`herdr-cron validate --schedule` exists because agents get cron expressions wrong.** A
  dry-run that prints the next five fire times converts a class of silent misconfiguration into
  an immediate, checkable answer. This is the single highest-value command on the list for an
  agent caller.
- **`--skill` mirrors `herdr --skill`** (A7, verified byte-identical to the installed file). One
  `//go:embed`, and the skill can never drift from the binary.
- **`schema`** is the urfave/cli argument from B1 cashed in: if the command tree is a
  JSON-tagged struct, this command is nearly free.
- **`job run --wait`** gives an agent a way to test a job definition without waiting for its
  schedule.
- **`--on-missed`** exists because the do-nothing service option (B5) makes missed runs the
  normal case, not the exception.

## Proposed JSON shapes

Envelope copied from herdr (B2, verified): `id` correlates the response to the command;
`result` or `error`, never both; success to stdout, failure to stderr.

`herdr-cron job list`:

```json
{
  "id": "cli:job:list",
  "result": {
    "jobs": [
      {
        "job_id": "j_01K5Q7XV",
        "name": "nightly-deps",
        "schedule": {"kind": "cron", "expr": "0 3 * * *", "tz": "Asia/Seoul"},
        "command": ["bash", "-lc", "go get -u ./... && go mod tidy"],
        "cwd": "/home/huke/huketo/herdr-cron",
        "env": {"CI": "1"},
        "tags": ["deps"],
        "state": "active",
        "pane": null,
        "timeout_ms": 900000,
        "on_missed": "run-once",
        "max_retries": 0,
        "created_at": "2026-08-30T11:02:14Z",
        "updated_at": "2026-09-01T04:41:00Z",
        "last_run": {"run_id": "r_01K5R2AA", "started_at": "2026-09-02T03:00:00Z", "status": "ok", "exit_code": 0, "duration_ms": 41230},
        "next_run_at": "2026-09-03T03:00:00Z"
      }
    ],
    "count": 1
  }
}
```

`herdr-cron run get r_01K5R2AA`:

```json
{
  "id": "cli:run:get",
  "result": {
    "run_id": "r_01K5R2AA",
    "job_id": "j_01K5Q7XV",
    "trigger": "schedule",
    "status": "ok",
    "exit_code": 0,
    "started_at": "2026-09-02T03:00:00Z",
    "finished_at": "2026-09-02T03:00:41Z",
    "duration_ms": 41230,
    "attempt": 1,
    "stdout_bytes": 2048,
    "stderr_bytes": 0,
    "truncated": false
  }
}
```

`herdr-cron validate --schedule '0 3 * * *' --count 3`:

```json
{
  "id": "cli:validate",
  "result": {
    "schedule": {"kind": "cron", "expr": "0 3 * * *", "tz": "Asia/Seoul"},
    "valid": true,
    "next": ["2026-09-03T03:00:00+09:00", "2026-09-04T03:00:00+09:00", "2026-09-05T03:00:00+09:00"]
  }
}
```

Errors, exit 1, on **stderr**:

```json
{"id":"cli:job:get","error":{"code":"job_not_found","message":"job j_nope not found"}}
```

```json
{"id":"cli:validate","error":{"code":"invalid_schedule","message":"cron expression has 6 fields, expected 5","field":"--schedule"}}
```

Proposed initial `error.code` vocabulary, kept small and stable: `job_not_found`,
`run_not_found`, `invalid_schedule`, `invalid_command`, `duplicate_name`, `daemon_not_running`,
`daemon_already_running`, `storage_locked`, `permission_denied`, `service_unsupported`.
`storage_locked` exists specifically because of bbolt's `flock` behaviour (B6).

Exit codes: `0` ok; `1` operation failed, JSON envelope on stderr; `2` usage error, plain text
on stderr plus usage. Same split as herdr, verified in B3.

## Draft `SKILL.md` frontmatter for the bundled skill

Spec-only fields (A1: the six-field set is the only portable one), trigger keywords first
(A2: the listing budget truncates from the tail), `name` matching the directory
`skills/herdr-cron/` (A3, A7).

```yaml
---
name: herdr-cron
description: Schedule, inspect, and manage recurring and one-off automated tasks with the herdr-cron CLI. Use when the user wants to run something on a schedule, asks about cron jobs, wants a task to repeat nightly or hourly, wants to see what is scheduled or when a job last ran, wants to pause, resume, or delete a scheduled job, or asks why a scheduled task did not fire. Requires the herdr-cron binary on PATH.
compatibility: Requires the herdr-cron binary on PATH. Optional integration with a running Herdr session for pane-targeted jobs.
license: Apache-2.0
metadata:
  source: herdr-cron
  homepage: https://herdr.dev
---
```

Rationale, field by field:

- `name: herdr-cron` — 11 chars, lowercase-and-hyphen, no consecutive hyphens, matches the
  directory. Satisfies every `[AS-SPEC]` rule.
- `description` — 500 characters, well inside the 1024 validation cap `[AS-SPEC]` and the 1,536
  listing budget `[CC-SKILLS]`. Opens with what it does, then a run of concrete trigger phrases,
  and closes with the precondition. Modelled on `lsoffice-cli`'s and `herdr-hitl`'s descriptions
  from `[LOCAL-SKILLS]`.
- `compatibility` — 143 chars, inside the 500 cap `[AS-SPEC]`. `[AS-SPEC]` says "Most skills do
  not need the `compatibility` field", but a skill that is useless without a specific binary is
  precisely the case it is for.
- `license` / `metadata` — spec fields; `metadata` is "a map from string keys to string values"
  `[AS-SPEC]`, so keep the values strings.
- **No `allowed-tools`.** `lsoffice-cli` on this machine uses `allowed-tools: Bash`
  `[LOCAL-SKILLS]`, and it is in the six-field spec set, so it *is* portable. But `[CC-SKILLS]`
  warns that "A skill can grant itself broad tool access, so review the `allowed-tools` of skills
  checked into a repository before you run Claude Code there", and a bare `Bash` grant from a
  third-party skill is a large ask. If it is added later, scope it —
  `allowed-tools: Bash(herdr-cron *)` — and note that scoping only works in harnesses that
  implement the pattern syntax; `[AS-SPEC]` calls the whole field "Experimental. Support for
  this field may vary between agent implementations."
- **No `disable-model-invocation`.** It removes the description from context entirely
  `[CC-SKILLS]`, which defeats the purpose.

Body shape, following the `herdr` skill's pattern (A4) and the 500-line ceiling (A3):

```
skills/herdr-cron/
├── SKILL.md              # preconditions; "the binary is the authority, run --help";
│                         #   the JSON envelope contract; the 0/1/2 exit contract;
│                         #   pointer to references/COMMANDS.md
└── references/
    └── COMMANDS.md       # full subcommand reference and JSON schemas
```

The invariants that belong in `SKILL.md` rather than in `--help`, because they are what an agent
gets wrong: parse IDs out of JSON responses instead of predicting them; treat exit 2 as "reread
the help" and exit 1 as "read `error.code`"; always `herdr-cron validate --schedule EXPR` before
`job add`; never `job rm` a job you did not create.

---

## Could not verify

- **The full 118-entry `agents` table was not enumerated.** `src/agents.ts` is 881 lines
  (`[$ wc -l /tmp/hc-research/skills/src/agents.ts]`) and I read lines 1–200 plus a targeted
  grep for `cursor` (found at line 264, quoted in A6). The `skillsDir`/`globalSkillsDir` values
  quoted are accurate for the entries shown; the other ~110 agents' install directories were not
  read. If herdr-cron's skill needs to work with a specific harness not named in this document,
  grep that file for it.
- **Windows and macOS path resolution was derived from source, not executed.** Every
  Windows/macOS row in the B4 table comes from reading `[GO-OS]` and `[XDG]` platform files on
  Linux. No Windows or macOS machine was available. In particular
  `os.UserConfigDir() == %AppData%` and `xdg.ConfigHome == %LocalAppData%` are read off the
  source, not observed.
- **`kardianos/service` was not run.** No `Install()` was executed on any platform. The
  `WantedBy=multi-user.target` problem for `--user` systemd units, and the absence of a Windows
  user-service path, are read off `service_systemd_linux.go` and `service_windows.go`. The
  `WantedBy` claim is a strong inference from the systemd load-path table `[SYSTEMD-MAN]`, not an
  observed failure. There may be an `Option` key that overrides the target which I did not find;
  I grepped `service.go` for option constants but did not read the full `KeyValue` documentation
  block.
- **`launchctl` subcommand syntax.** `[APPLE-LAUNCHD]` is Apple's Documentation Archive, doc
  version 6.3.4 dated 2016-09-13. It documents plist keys and directories, which are stable, but
  it predates `launchctl bootstrap`/`bootout`. Whether `launchctl load -w` is still the correct
  invocation on current macOS was not verified — no macOS machine, and the modern `launchctl`
  man page was not read.
- **`schtasks` was not run.** No Windows machine. The syntax and the `/rl LIMITED` default come
  from `[MSLEARN]`. The specific claim that creating a current-user task needs no elevation is an
  inference from `/rl`'s documented default of `Limited` plus the fact that `/u`/`/p` are
  documented as "valid only when you use `/s`" (remote); `[MSLEARN]` does not state the local
  elevation requirement explicitly.
- **goreleaser was not run.** `[$ which goreleaser]` returned nothing. All of B7 is from docs.
  `goreleaser check` / `healthcheck` / `release --snapshot` were not executed against any
  config.
- **`brews:` field-level detail.** The deprecation is verified (B7), but I read only the top of
  the `homebrew_formulas` page's YAML block, not all 573 lines. The formula-only fields I list as
  "given up" are the ones visible in that excerpt; there may be others.
- **cobra's `main` vs `v1.10.2`.** The clone is `main` at 2026-07-10, seven months after the
  v1.10.2 tag. Everything quoted (the `Command` struct fields, `ShellCompRequestCmd`, the
  directive bitmask, `SetErrPrefix`) is long-standing API, but the `go.mod` require list and
  exact line numbers are from `main`. Same caveat for `[URFAVE]` (`main` at 2026-08-25 vs
  v3.11.0) and `[KSERVICE]` (`main` at 2026-08-29 vs v1.3.0).
- **`os.Rename` atomicity on Windows** for the JSON-files option (B6) is asserted from general
  knowledge, not from reading `os/file_windows.go`. Not cited, and should be checked if JSON
  storage is chosen.
- **Herdr's plugin system and how a plugin registers itself** — deliberately out of scope,
  covered by the Herdr-plugin document. The only Herdr facts used here are the CLI's observable
  behaviour (`herdr --help`, `herdr --skill`, `herdr pane list`, `herdr pane get`,
  `herdr bogus-cmd`) and its installed `SKILL.md`.
- **gocron's schedule expression syntax** — out of scope, covered by the gocron document. The
  `{"kind": "cron", "expr": "...", "tz": "..."}` shape proposed above is a placeholder that must
  be reconciled with whatever gocron actually accepts.
