# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

**Layout: single-context.** One `CONTEXT.md` and one `docs/adr/` at the repo root. There is no `CONTEXT-MAP.md`.

## Before exploring, read these

- **`CONTEXT.md`** at the repo root — the glossary and domain narrative.
- **`docs/adr/`** — read ADRs that touch the area you're about to work in.
- **`docs/spec/README.md`** — this repo also carries a normative specification. `docs/spec/` is the contract the code is written against; `docs/research/` is the primary-source evidence under it, and every spec claim about a library, a binary, or an operating system cites a research section. Neither directory is a design doc to be revised in passing: a change that contradicts `docs/spec/` is a spec change, and the PR must say so.

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates them lazily when terms or decisions actually get resolved.

## File structure

```
/
├── CONTEXT.md
├── docs/adr/
│   ├── 0001-run-once-core-with-three-drivers.md
│   ├── 0002-files-only-ipc.md
│   └── 0003-agent-skill-distribution.md
├── docs/spec/          normative — read README.md first
├── docs/research/      evidence — primary sources, pinned
├── internal/
└── skills/herdr-cron/
```

If this repo ever splits into multiple bounded contexts, promote the root to a `CONTEXT-MAP.md` pointing at one `CONTEXT.md` per context (`internal/<context>/CONTEXT.md`), with context-scoped decisions under `internal/<context>/docs/adr/` and system-wide decisions staying in the root `docs/adr/`. Update this file when that happens.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids — the "Words we do not use" section exists because each of those synonyms already means something else here.

Where `CONTEXT.md` and `docs/spec/03-job-model.md` name the same thing, the spec is authoritative for field names and `CONTEXT.md` for prose.

If the concept you need isn't in the glossary yet, that's a signal — either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0002 (files-only IPC) — but worth reopening because…_

The same rule applies to the decision record in `docs/spec/README.md` (D1–D8), which is where a decision that predates the ADRs lives. Cite it by its D-number.
