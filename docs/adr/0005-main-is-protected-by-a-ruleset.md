# 0005 — `main` is protected by a repository ruleset that requires a squashed pull request and no status check

## Status

Accepted — 2026-09-02. Amends the second half of [ADR-0004](0004-release-pr-checks-are-never-approved.md)'s decision: `main` is no longer unprotected, but the prohibition on required status checks that ADR-0004 derived stands unchanged and is now enforced by the shape of this ruleset. No `docs/spec/` decision covers it — this is a decision about how this repository accepts changes, not about the product.

## Context

GitHub showed the "Your main branch isn't protected" notice on the repository, and the API agreed: `GET /repos/huketo/herdr-cron/rulesets` returned `[]` and `GET /repos/huketo/herdr-cron/branches/main/protection` returned `404 Branch not protected`. Nothing but convention stopped a force-push that would rewrite released history, or a deletion of the branch every release is cut from.

The convention itself is already written down. [`CONTRIBUTING.md`](../../CONTRIBUTING.md) §"Opening a pull request" says to branch from `main` and open a pull request, and §"The PR title is a commit message" says merges are **squashed** — which is why `commitlint.yml` lints the pull-request title as the subject that will land. Four commits on `main` from the first two days of the repository were pushed directly, before that convention was recorded; the rest arrived as squashed pull requests.

The one thing protection must not do is gate `main` on a check. ADR-0004 established that every workflow run against a `release-please` pull request is created in GitHub's approval-required state and never runs, so a required status check could never report on the release pull request and would deadlock every release.

## Decision

**`main` is protected by a branch ruleset named `main` (id 22095146), targeting `~DEFAULT_BRANCH`, enforcement `active`, with three rules and no bypass actors:**

- `deletion` — the branch cannot be deleted.
- `non_fast_forward` — force-pushes are refused, so released history cannot be rewritten.
- `pull_request` with `required_approving_review_count: 0` and `allowed_merge_methods: ["squash"]` — changes arrive through a pull request, a solo maintainer can still merge their own, and squash is the only way in, so the linted pull-request title is always the subject that lands.

**No `required_status_checks` rule exists, and none may be added while ADR-0004 holds.** Every review knob that could deadlock a solo repository is off deliberately: `require_last_push_approval`, `required_review_thread_resolution`, `require_code_owner_review`, `dismiss_stale_reviews_on_push`, and `require_extra_approval_for_unattributed_changes` — the last of which the API defaults to `true`, and which would demand an approval nobody can give for a commit whose author email is not linked to a GitHub account.

The ruleset was created and is maintained through the API, not the web UI:

```sh
gh api repos/huketo/herdr-cron/rules/branches/main            # what applies to main
gh api --method PUT repos/huketo/herdr-cron/rulesets/22095146 --input ruleset.json
```

## Consequences

- A direct push to `main` is refused by the server. Verified: pushing a commit straight to `main` returns `GH013: Repository rule violations found for refs/heads/main` — "Changes must be made through a pull request" — and `main` stayed at `0faa2d3`.
- The release path is untouched. The ruleset targets `~DEFAULT_BRANCH` alone, so the branch `release-please` force-pushes is not covered:

  ```sh
  git ls-remote origin 'refs/heads/release-please*'   # the release branch, outside the ruleset
  ```

  The tag and the GitHub Release are written to `refs/tags/*`, which a branch ruleset never sees, and merging the release pull request needs neither an approval nor a green check — so the red X that ADR-0004 accepts as noise is still mergeable.
- Merging by rebase or a merge commit is no longer possible in this repository, only squash. A pull request whose title is not a valid conventional commit therefore cannot smuggle a different subject onto `main` through a merge commit.
- There is no bypass actor, including for the owner. That is safe because it is not a lockout: an admin can amend or delete the ruleset through the API in one call, which is a deliberate, logged act rather than an accidental `git push`.
- Wanting a required check on `main` still means switching `release.yml` off `secrets.GITHUB_TOKEN` first, exactly as ADR-0004 says — preferably to a GitHub App installation token.
