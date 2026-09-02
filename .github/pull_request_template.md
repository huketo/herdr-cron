<!--
The PR title becomes the commit subject on main, because merges are squashed.
It MUST be a valid conventional commit:

  <type>(<scope>): <subject>

types:  feat fix perf refactor revert docs test build ci style chore
scopes: cli config daemon herdr model paths runner schedule service store tui
        skill plugin docs ci deps

Examples:
  feat(schedule): honour catchup_window when the daemon starts late
  fix(runner): kill the whole process group when a run times out
  docs(skill): document the run logs streaming envelope
-->

## What

<!-- One or two sentences. What does this change do? -->

## Why

<!-- The problem this solves. Link the issue: Closes #123 -->

## How

<!-- Anything a reviewer needs to follow the diff: design choices, trade-offs,
     alternatives you rejected. Delete if the diff speaks for itself. -->

## Verification

<!-- What you actually ran. Replace the placeholders with real commands and
     real output — "should work" is not verification. -->

- [ ] `make check` passes (`vet` + `lint` + race tests)
- [ ] Exercised the change end to end (paste the commands and what came back):

```
$ herdr-cron validate --schedule "17 3 * * 1-5" --next 5
$ herdr-cron run-once <job-id>
$ herdr-cron status -o text
```

## Notes for reviewers

- [ ] Changes the CLI surface, flags, or exit codes (`docs/spec/05-cli.md` + the bundled skill updated)
- [ ] Changes the `jobs.yaml` schema or the on-disk state layout (migration/compatibility considered)
- [ ] Touches the OS service drivers (verified on the platform it affects; CI cannot register a real service)
- [ ] Touches the Herdr integration (`herdr-plugin.toml`, pane spawning) and was checked against a running Herdr
- [ ] Breaking change (the title uses `!`, e.g. `feat(cli)!: ...`)
