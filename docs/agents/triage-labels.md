# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those roles to the actual label strings used in this repo's issue tracker.

| Label in mattpocock/skills | Label in our tracker | Meaning                                  |
| -------------------------- | -------------------- | ---------------------------------------- |
| `needs-triage`             | `needs-triage`       | Maintainer needs to evaluate this issue  |
| `needs-info`               | `needs-info`         | Waiting on reporter for more information |
| `ready-for-agent`          | `ready-for-agent`    | Fully specified, ready for an AFK agent  |
| `ready-for-human`          | `ready-for-human`    | Requires human implementation            |
| `wontfix`                  | `wontfix`            | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table.

Edit the right-hand column to match whatever vocabulary you actually use.

## What is `ready-for-human` here

Two classes of issue in this repo are never `ready-for-agent`, however well specified:

- Anything that can only be verified on macOS or Windows — service registration, mouse delivery, timer behaviour across suspend. Those claims are read from documentation, not executed (`docs/spec/README.md`, "Known risks carried into implementation"), so closing them needs someone holding the machine.
- Anything requiring a real Herdr session with a live coding agent. The test suite deliberately never starts one (`CONTRIBUTING.md`), so an agent working AFK cannot prove the fix.

A non-goal request (a DAG, a web UI, a remote API, cost accounting — `docs/spec/01-overview.md` §3.2) is `wontfix` with the reason quoted from that section, not left open as a wish.
