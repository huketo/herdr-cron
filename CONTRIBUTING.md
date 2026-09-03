# Contributing to herdr-cron

`herdr-cron` is a [Herdr](https://herdr.dev) plugin and a standalone CLI that runs
shell commands and coding-agent prompts on a schedule, inside Herdr panes. It is a Go
module (`github.com/huketo/herdr-cron`) that builds a single binary, `herdr-cron`, from
`./cmd/herdr-cron`.

`docs/spec/` is normative and `docs/research/` is the evidence under it. Read
`docs/spec/README.md` first: it is the index, it records decisions D1–D8, and it says
what is shipped and what is not. When the code and the spec disagree, one of them is a
bug — say which in the PR.

## Prerequisites

- Go — the version is pinned in `go.mod`; CI uses exactly that toolchain.
- `make`.
- Herdr `0.8.2` or newer, if you want to exercise the plugin integration or a
  `kind: agent` job. That floor is the version the headless orchestration path was
  probed against (`docs/spec/07-herdr-integration.md` §1.3), not a guess.
- A coding-agent CLI (`claude`, `codex`, …) only if you want to watch a `kind: agent`
  job run end to end. Tests never start an agent, never talk to Herdr, and never touch
  the network.

Nothing else is required. `make lint` and `make fmt` fetch their tools on demand
through `go run`, pinned to gofumpt `v0.9.1` and golangci-lint `v2.13.2` — the same
versions CI uses — so there is no separate install step. The build is pure Go with
`CGO_ENABLED=0` — no C toolchain, no cgo, no SQLite.

## Build and test

```sh
make build     # -> bin/herdr-cron
make test      # go test -race -count=1 ./...
make cover     # writes coverage.out and prints total coverage
make fmt       # gofumpt -w .
make vet       # go vet ./...
make lint      # golangci-lint run
make check     # vet + lint + test — run this before opening a PR
make tidy      # go mod tidy
make clean
```

`make check` is the gate. CI runs the same checks plus a `gofmt` diff, a `go mod tidy`
diff, the test suite on Linux, macOS and Windows, and `goreleaser build --snapshot`,
which cross-compiles every release target.

Two things fail CI that are easy to miss locally:

- **Formatting.** CI fails if `gofmt -l .` prints anything. `make fmt` runs gofumpt,
  which is a strict superset, so a gofumpt-clean tree is gofmt-clean.
- **An untidy module.** CI runs `go mod tidy` and then
  `git diff --exit-code go.mod go.sum`. Run `make tidy` and commit the result.

### Test conventions

- Table-driven, with `t.Parallel()` wherever the test is safe to run in parallel.
- No network, no live Herdr, no live agent. The Herdr adapter is tested against
  captured transcripts (`internal/herdr/transcript_test.go`) rather than a running
  server, because what broke on the first live run was a parsing rule — claude's status
  footer carries its own `●` — not a socket.
- Filesystem tests use `t.TempDir()` and drive the real store. Roots come from
  `paths.Resolve`, which is also what `--state-dir` feeds, so a test overrides them the
  same way a user does instead of reaching for a global.
- Test observable behaviour — the exit code, the run record's `status` and `reason`,
  the bytes `jobs.yaml` holds after a write — not internal plumbing. The two writes
  worth guarding hardest are the ones a user notices: an edit that would produce an
  invalid `jobs.yaml` must leave the original byte-identical, and a comment must
  survive a `job update`.
- **Two drift guards will fail your build, and both are meant to.**
  `skills/embed_test.go` asserts that `herdr-cron --skill` is byte-identical to
  `skills/herdr-cron/SKILL.md`, that the frontmatter and description stay inside their
  budgets, and that every bundled reference is linked from the body.
  `internal/cli/docs_test.go` walks the cobra command tree and asserts that
  `README.md`, `README.ko.md`, `CONTRIBUTING.md`, `CONTEXT.md`, the skill, its
  references and every ADR name only real flags, attached to a command that actually
  defines them — and that the READMEs mention every command path. Rename an
  agent-facing flag and the docs must move in the same commit. That friction is the
  feature: an agent that runs a flag we deleted gets exit 2.

## Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/). This is not
cosmetic: `release-please` parses the history to decide the next version and to write
`CHANGELOG.md`. A commit that does not parse is a commit that cannot be released.

```
<type>(<scope>): <subject>

[optional body]

[optional footer]
```

**Types**

| Type       | Meaning                                      | Release effect        |
| ---------- | -------------------------------------------- | --------------------- |
| `feat`     | New user-visible capability                  | minor bump            |
| `fix`      | Bug fix                                      | patch bump            |
| `perf`     | Performance improvement, no behaviour change | patch bump            |
| `refactor` | Internal restructuring, no behaviour change  | patch bump            |
| `revert`   | Reverts an earlier commit                    | patch bump            |
| `docs`     | Documentation only                           | in changelog, no bump |
| `test`     | Tests only                                   | hidden                |
| `build`    | Build system, Makefile, goreleaser           | hidden                |
| `ci`       | Workflows, dependabot, commitlint            | hidden                |
| `style`    | Formatting only                              | hidden                |
| `chore`    | Everything else, including dependency bumps  | hidden                |

Append `!` for a breaking change — `feat(cli)!: make -o text the default`. Before
`1.0.0` a breaking change is a minor bump (`bump-minor-pre-major`), not a major one.

**Scopes** — one of:

`cli`, `config`, `daemon`, `herdr`, `model`, `paths`, `runner`, `schedule`, `service`,
`store`, `tui`, `skill`, `plugin`, `docs`, `ci`, `deps`

The first eleven are the packages under `internal/`, so the scope names the directory a
commit touched. `skill` is `skills/`; `plugin` is `herdr-plugin.toml`. The scope is
optional, but if you set one it must be from that list. `commitlint.config.mjs` is the
source of truth; keep it and this list in sync.

Examples:

```
feat(schedule): accept 6-field cron expressions with seconds
fix(daemon): close orphaned running records left by a killed process
fix(runner): kill the process group so a shell's grandchild cannot survive
feat(cli)!: make --state active the default for job list
docs(skill): document exit code 3 as blocked
chore(deps): bump github.com/go-co-op/gocron/v2 to v2.16.0
```

### The PR title is a commit message

Merges are **squashed**, so the PR title becomes the subject of the single commit that
lands on `main`. It must itself be a valid conventional commit. A dedicated workflow
lints the PR title on every edit, and `commitlint` also lints the individual commits in
the branch — so the commits inside the branch should be conventional too, even though
only the title survives the merge.

To check a message before pushing:

```sh
npm install --no-save @commitlint/cli@19 @commitlint/config-conventional@19
echo 'feat(store): record runsToday per calendar day' | npx commitlint --config commitlint.config.mjs
```

## How releases work

Releases are fully automated; nobody tags by hand.

1. Conventional commits land on `main`.
2. `release-please` keeps a **release PR** open, titled `chore(main): release X.Y.Z`.
   It holds the version bump and the generated `CHANGELOG.md` entry, and it updates
   itself as more commits land.
3. Merging that PR is the release decision. `release-please` then pushes the `vX.Y.Z`
   tag and creates the GitHub Release with the changelog as its notes.
4. In the same workflow run, `goreleaser` builds the six release targets — Linux, macOS
   and Windows for `amd64` and `arm64` — packages each archive with `README.md`,
   `LICENSE`, `herdr-plugin.toml` and `skills/`, and appends the archives plus
   `checksums.txt` to that release.

The release workflow needs no secret beyond the built-in `GITHUB_TOKEN`. There is no
Homebrew tap and no Scoop bucket: the archives on the Releases page, `go install`, and
the Herdr plugin are the three distribution channels.

### The release PR's checks are red, and that is expected

`CI` and `Commitlint` never run on a release PR. `release-please` authors it with the
built-in `GITHUB_TOKEN`, and GitHub creates the resulting `pull_request` runs in an
**approval-required** state that nobody approves, so they sit until the merge finalises
them as failures with zero jobs and no logs. A red X there means *never ran*, not
*failed*; the absence of logs is how to tell them apart. Do not go looking for the lint
rule that rejected `chore(main): release X.Y.Z` — `commitlint.config.mjs` ignores that
subject by design.

Merge the release PR anyway. The push-event `CI` and `Commitlint` runs on `main` cover
the release commit one commit later, including `internal/cli/release_test.go`, which is
what would catch a version bump that left the three `release-please`-owned version
literals disagreeing. See
[ADR-0004](docs/adr/0004-release-pr-checks-are-never-approved.md) for the evidence and
for why `main` must stay free of required status checks while this holds.

### Where the version lives

Four places, and only one of them is hand-editable:

- `.release-please-manifest.json` — the current version. **Owned by `release-please`.**
  Do not edit it, or `CHANGELOG.md`, by hand.
- `internal/cli/buildinfo.go` — `const fallbackVersion`, carrying the
  `// x-release-please-version` marker. `release-please` rewrites the literal, so the
  marker comment is load-bearing: do not reflow that line. This is what
  `herdr-cron --version` prints for a `go install` build, where no linker flags were
  passed.
- `herdr-plugin.toml` — `[plugin] version`, what Herdr's marketplace shows.
- `cmd/herdr-cron/main.go` — the three package-level variables `version`, `commit` and
  `date`, stamped by `-ldflags -X main.<name>=…` for a release build. `Makefile` and
  `.goreleaser.yaml` set the same three names; **rename one, rename all three**, or a
  release binary silently reports the fallback version instead of its tag.

## Developing the plugin locally

Herdr can load the plugin straight from your working copy, so you do not have to
reinstall after every build:

```sh
make build                      # bin/herdr-cron
herdr plugin link .             # register this checkout as plugin huketo.herdr-cron
herdr plugin list               # confirm huketo.herdr-cron is present
```

Linking runs the manifest's `[[startup]]` hook (`daemon --detach`), and Herdr re-runs
that hook on every server start and every live handoff — so it has to be, and is,
idempotent: it returns as soon as some daemon holds `daemon.lock` and has written a
fresh heartbeat, and does nothing when one already does.

To watch the scheduler work, run the `foreground` driver instead and read its log on
stderr:

```sh
bin/herdr-cron daemon --foreground
```

Edit `jobs.yaml` in another terminal and it reloads by itself — fsnotify, with a
5-second stat poll behind it. `bin/herdr-cron reload` forces the same pass when you
want to be certain, or when the watch missed your editor's write-then-rename.

Exercising one job without waiting for its schedule:

```sh
bin/herdr-cron validate --schedule "17 3 * * 1-5" --next 5   # no daemon needed
bin/herdr-cron run-once nightly-deps                         # one run, in this process
bin/herdr-cron job run nightly-deps --wait                   # through the daemon
bin/herdr-cron run logs <run-id> --follow
```

**A rebuilt binary needs the daemon restarted.** `reload` re-reads `jobs.yaml`; it does
not re-exec. A daemon started before `make build` keeps serving the old code, which is
the most confusing way to lose an afternoon here. Stop it — Ctrl-C in the
`--foreground` pane, or terminate the detached process — then start it again with
`bin/herdr-cron daemon --detach`. `bin/herdr-cron status` reports which pid holds the
lock and what version it claims to be.

When you are done:

```sh
herdr plugin unlink huketo.herdr-cron
```

## Opening a pull request

`main` is protected by a repository ruleset: a direct push is refused with `GH013`, the
branch cannot be force-pushed or deleted, and **squash is the only merge method**. No
approval and no status check is required — a solo maintainer can merge their own pull
request, and a required check would deadlock every release
([ADR-0005](docs/adr/0005-main-is-protected-by-a-ruleset.md)).

- Branch from `main`.
- Keep the change focused; one logical change per PR.
- Run `make check`.
- Give the PR a conventional-commit title.
- Fill in the PR template, including what you actually ran to verify the change. For
  anything touching the runner, the daemon or the TUI, "the tests pass" is not
  verification — say what you ran and what you saw.
- Update `docs/spec/`, `README.md`, `README.ko.md` and `skills/herdr-cron/SKILL.md` in
  the same PR whenever you touch the CLI surface, the flags, the exit codes or the JSON
  envelope. The code, the spec, the docs and the skill must agree; the drift guards
  enforce the part of that a machine can check, and the rest is on the reviewer.

## Reporting bugs and proposing changes

Use the issue templates. Bug reports should include `herdr-cron --version` and
`herdr-cron status` output, plus the failing run's record from
`herdr-cron run get <run-id>`. Redact anything private before pasting a `jobs.yaml`: a
prompt and an `env` map are user content, and nothing in herdr-cron scrubs them for
you.
