# 0004 — `release-please` keeps the built-in `GITHUB_TOKEN`, so release-PR checks never run

## Status

Accepted — 2026-09-02. No `docs/spec/` decision covers it: this is a decision about how this repository releases itself, not about the product. It governs [`.github/workflows/release.yml`](../../.github/workflows/release.yml) and is documented for contributors in [`CONTRIBUTING.md`](../../CONTRIBUTING.md) §"How releases work".

## Context

Every release pull request `release-please` has opened so far carries a red X. The natural reading — a lint rule or a test rejected the generated commit — is wrong, and acting on that reading wastes an investigation, so the real cause belongs in writing.

The workflow runs on those pull requests never started. Four release pull requests, one signature:

| Release | `Commitlint` run | `CI` run | Conclusion | Jobs created | Run finished | Pull request merged |
| --- | --- | --- | --- | --- | --- | --- |
| 0.1.0, at open | 33601315048 | 33601315078 | `action_required` | 0 | — | 07:00:44Z |
| 0.1.0, at update | 33601372821 | 33601372824 | `failure` | 0 | 07:00:45Z | 07:00:44Z |
| 0.1.1 | 33602220611 | 33602220461 | `failure` | 0 | 07:10:58Z | 07:10:57Z |
| 0.1.2 | 33602879718 | 33602879752 | `failure` | 0 | 07:19:10Z | 07:19:09Z |
| 0.1.3 | 33613976429 | 33613976276 | `failure` | 0 | 09:28:44Z | 09:28:43Z |

The first row names the state outright: the two runs created when the 0.1.0 release pull request opened are still recorded as `action_required`, GitHub's term for a run held back until someone approves it. Every later pair was created by `release-please` force-pushing an updated release commit, waited the same way, and was finalised as `failure` within a second of the merge that ended the wait. None of them created a single job, so none produced logs or check runs — which is why `gh pr checks` reports "no checks reported" on a release pull request whose merge state is `UNSTABLE`.

GitHub documents exactly this state ([GITHUB_TOKEN](https://docs.github.com/en/actions/concepts/security/github_token), "When `GITHUB_TOKEN` triggers workflow runs"):

> `pull_request` events with the `opened`, `synchronize`, or `reopened` activity types: when a workflow using `GITHUB_TOKEN` creates or updates a pull request, the resulting `pull_request` event creates workflow runs in an **approval-required** state. The pull request displays a banner in the merge box, and a user with write access to the repository can start the runs by selecting **Approve workflows to run**.

`release.yml` passes `secrets.GITHUB_TOKEN` to `release-please-action`, so the release pull request is authored by `github-actions[bot]` and every run against it is born needing a human click that nobody makes. Nothing in the lint configuration is implicated: `commitlint.config.mjs` already ignores subjects matching `^chore\(main\): release `, precisely so the generated title cannot fail `scope-enum`.

The documented escape is a credential: "use a GitHub App installation access token or a personal access token instead of `GITHUB_TOKEN` when creating or updating the pull request." That buys green checks on the release pull request at the cost of a standing credential in the repository — a cost `release.yml` already refuses to pay for artefact publishing, which is why there is no Homebrew tap.

## Decision

**`release-please` keeps `secrets.GITHUB_TOKEN`, and the red X on the release pull request is accepted as noise.**

**No status check on `main` may be made required.** A required check on `main` would deadlock every release: the check can never report on a pull request whose runs are never approved, so the release pull request could never be merged and no tag would ever be pushed. Wanting required checks means switching the token first — a GitHub App installation token in preference to a personal access token, since it is scoped to this repository and rotates itself. `main` was left entirely unprotected when this was written; it is now protected by a ruleset that carries no status-check rule, for this reason ([ADR-0005](0005-main-is-protected-by-a-ruleset.md)).

## Consequences

- A red X on a release pull request means "never ran", not "failed". The tell is that the run carries no jobs and no logs at all; a real failure always has both.
- What the skipped runs would have verified is verified one commit later: the push-event `CI` and `Commitlint` runs on `main` cover the release commit (33614328766 and 33614328733 for 0.1.3, both `success`), and `internal/cli/release_test.go` — the test that asserts `fallbackVersion`, `herdr-plugin.toml` and `.release-please-manifest.json` still agree — runs there. That push starts the `Release` workflow at the same time and does not gate it, so a release whose version literals disagree would still be tagged and published; the failed `CI` run on `main` is what tells you to cut a fixing release.
- Anyone with write access can still get the checks on a specific release pull request by clicking **Approve workflows to run** in the merge box.
- No standing credential exists for CI to leak, and the release path keeps needing nothing beyond the built-in token.
