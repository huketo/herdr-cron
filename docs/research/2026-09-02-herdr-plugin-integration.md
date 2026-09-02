---
title: Herdr plugin integration for herdr-cron
date: 2026-09-02
status: research — evidence, not a decision
pinned_versions:
  herdr_binary: 0.8.2 (stable channel, protocol 20, schema_version 1)
  herdr_docs: https://herdr.dev/docs/ — "Latest 0.8.2"
  herdr_reviewr: 4c090225af706bf3aaa24b39fea890a72994f40f
  herdr_hitl: 75df41ac9b1b8bb7c0297cd6f7d5d8dbeb811aef (v0.1.4)
  herdr_agent_usage: c0fca9f6ea45a631173e09c094c0caa6ab22ea73 (usagebar 0.5.12)
  herdr_sheep: 0ad12f135f77086bbd1c358c01e12461415aef0d (v0.3.0)
---

# Herdr plugin integration

How `herdr-cron` plugs into Herdr: as a Herdr plugin, and as a scheduler that drives
coding agents inside Herdr panes.

This document covers **Herdr, the host**. gocron (the scheduling engine), Bubble Tea
(the TUI) and the Agent Skill (how an agent learns the CLI) are researched separately;
they are referenced by name here and not duplicated.

## Citation tags

Every claim below carries one of these inline.

| Tag | Meaning |
| --- | --- |
| `[BIN]` | Learned by running the installed binary. `herdr 0.8.2` at `/home/huke/.local/bin/herdr` (`command -v herdr`; `herdr --version` → `herdr 0.8.2`). The exact command is quoted at the claim. |
| `[SCHEMA]` | The socket protocol schema bundled *with that binary*, dumped via `herdr api schema --json` (255 484 bytes; `.protocol = 20`, `.schema_version = 1`, `.title = "Herdr API"`). Queried with `jq`; the jq path is quoted at the claim. |
| `[LIVE]` | A live read-only call against the running server on this machine (`herdr status --json` → `server.status = "running"`, version 0.8.2). The exact command is quoted at the claim. |
| `[DOC:plugins]` | <https://herdr.dev/docs/plugins/> |
| `[DOC:socket]` | <https://herdr.dev/docs/socket-api/> |
| `[DOC:agents]` | <https://herdr.dev/docs/agents/> |
| `[DOC:automation]` | <https://herdr.dev/docs/agent-automation/> |
| `[DOC:config]` | <https://herdr.dev/docs/configuration/> |
| `[DOC:cli]` | `docs/next/website/src/content/docs/cli-reference.mdx` at tag `v0.8.2`, raw from `https://raw.githubusercontent.com/herdrdev/herdr/v0.8.2/...` (the canonical source behind <https://herdr.dev/docs/cli-reference/>, named by <https://herdr.dev/llms.txt>) |
| `[DOC:persist]` | `docs/next/website/src/content/docs/persistence-remote.mdx` at tag `v0.8.2`, same raw source |
| `[SKILL]` | The agent skill the binary itself prints: `herdr --skill` (195 lines) |
| `[REVIEWR]` | `git clone --depth 1 https://github.com/persiyanov/herdr-reviewr /tmp/hc-research/herdr-reviewr`; `git rev-parse HEAD` → `4c090225af706bf3aaa24b39fea890a72994f40f` |
| `[HITL]` | Third-party **Go** plugin installed on this machine: `huketo/herdr-hitl` v0.1.4, resolved commit `75df41ac9b1b8bb7c0297cd6f7d5d8dbeb811aef`, manifest read at `/home/huke/.config/herdr/plugins/github/huketo.hitl-cd696300a29f/herdr-plugin.toml` |
| `[USAGEBAR]` | Third-party plugin installed on this machine: `huketo/herdr-agent-usage` 0.5.12, resolved commit `c0fca9f6ea45a631173e09c094c0caa6ab22ea73` |
| `[SHEEP]` | Third-party plugin installed on this machine: `huketo/herdr-sheep` v0.3.0, resolved commit `0ad12f135f77086bbd1c358c01e12461415aef0d` |
| `[PROBE]` | Learned by **executing** a mutating command inside an isolated, disposable named session (`herdr --session hcprobe …`) that had never hosted a TUI client, on 2026-09-02. Confined to §9. The session was stopped and deleted afterwards; the default session was never touched. The exact command is quoted at the claim. |
| `[INFERENCE]` | My reasoning, not a source. |

Provenance of the installed plugin commits: `herdr plugin list --json | jq -r '.result.plugins[] | "\(.plugin_id)\t\(.version)\t\(.source.owner)/\(.source.repo)\t\(.source.requested_ref)\t\(.source.resolved_commit)"'` `[LIVE]`.

**Safety note on method.** Bare `herdr` launches or attaches the TUI, so it was never run
`[SKILL]`. §1–§8 ran **no** mutating command: no `workspace create`, `pane split`,
`agent start`, `plugin link`, or `server stop`. Everything in those sections is `--help`
output, `api schema`, read-only list/get calls, docs, and source.

§9 is different by design, and was added after the rest of the document: it executes the
headless orchestration path that §5 could only infer. Every mutation there happened in a
throwaway session (`--session hcprobe`) with its own socket at
`~/.config/herdr/sessions/hcprobe/herdr.sock`, and was torn down with `herdr session stop`
plus `herdr session delete`. `herdr session list --json` afterwards showed only `default`,
still running.

---

## 1. What a Herdr plugin actually is

### Definition

> A plugin is a directory with a `herdr-plugin.toml` manifest and commands Herdr
> can launch. Herdr validates the manifest, injects runtime context, starts the
> declared commands, and records logs. The commands call back into Herdr through
> the CLI or socket when they need to do more work. `[DOC:plugins]`

It is **not** a shared library, not a WASM module, not an in-process extension. It is a
manifest plus argv commands:

> `command` values are argv arrays. Herdr does not run them through a shell, so
> there is no shell expansion unless your command starts a shell itself. `[DOC:plugins]`

### Manifest file name and schema

The file is always `herdr-plugin.toml`, at the plugin directory root (or in a subdirectory
for multi-plugin repos) `[DOC:plugins]`.

Required top-level keys: `id`, `name`, `version`, `min_herdr_version` `[DOC:plugins]`.
`description` and `platforms` are optional, with a caveat:

> `min_herdr_version` is required. The server refuses to link a plugin when the
> field is missing, invalid, or newer than the running Herdr binary. `[DOC:socket]`

> Local plugins without top-level `platforms` link with a warning. `[DOC:plugins]`

Id rules `[DOC:plugins]`:

- Plugin ids may use ASCII letters, digits, dot, colon, underscore, hyphen.
- Action / pane / link-handler ids may use letters, digits, colon, underscore, hyphen — **not dots**. Each id type must be unique within a plugin.
- Herdr qualifies action ids as `plugin.id.action` globally. Because local ids cannot contain dots, qualified ids stay unambiguous even when the plugin id itself contains dots `[DOC:cli]`.

Manifest sections, all optional: `[[build]]`, `[[startup]]`, `[[actions]]`, `[[events]]`,
`[[panes]]`, `[[link_handlers]]` `[DOC:plugins]`. Every one of them, plus the top level,
accepts `platforms`; item-level overrides top-level `[DOC:plugins]`.

`contexts` on an action is an enum. From the binary's own schema:

```
$ jq -c '.schemas.success_response["$defs"].PluginActionContext' herdr-api-schema.json
{"enum":["global","workspace","tab","pane","selection"],"type":"string"}
```
`[SCHEMA]`

The docs page only ever demonstrates `contexts = ["workspace"]` and `["pane", "workspace"]`
`[DOC:plugins]`, but `global` is real and used in the wild `[HITL]`, `[SHEEP]`. **This is a
docs gap, not a disagreement** — the schema is a superset of the documented examples.

### herdr-reviewr's manifest, verbatim

`/tmp/hc-research/herdr-reviewr/herdr-plugin.toml` at `4c09022` `[REVIEWR]`:

```toml
id = "persiyanov.reviewr"
name = "reviewr"
version = "0.36.2"
# Turn tracking resolves each agent's `cwd` from `herdr agent list` to decide worktree
# membership, and `cwd` is only verified present from 0.7.5 (docs/herdr-api-notes.md). On an
# older herdr every agent would parse with no `cwd`, and `last turn` would never track.
min_herdr_version = "0.7.5"
platforms = ["macos", "linux"]
description = "Review agent-written diffs beside the chat and add line comments to the agent input."

# On `herdr plugin install`, download the prebuilt `herdr-reviewr` binary for this platform from
# the matching GitHub Release into $HERDR_PLUGIN_ROOT/bin (no Rust toolchain needed). Skipped by
# `herdr plugin link` — for a local checkout, build it yourself with `cargo install --path .`.
[[build]]
command = ["bash", "herdr/install.sh"]

# The plugin pane runs the downloaded binary by absolute path under the plugin root, since the
# pane's cwd is the repo under review (not the plugin root) and the binary isn't on PATH. The
# toggle action's placement is configurable (default: right split); see herdr/pane.sh.
[[panes]]
id = "pane"
title = "reviewr"
placement = "split"
command = ["sh", "-c", "exec \"$HERDR_PLUGIN_ROOT/bin/herdr-reviewr\""]

[[actions]]
id = "toggle"
title = "reviewr: toggle pane"
contexts = ["pane", "workspace"]
command = ["bash", "herdr/pane.sh", "toggle"]

[[actions]]
id = "open"
title = "reviewr: open pane"
contexts = ["pane", "workspace"]
command = ["bash", "herdr/pane.sh", "open"]

[[actions]]
id = "close"
title = "reviewr: close pane"
contexts = ["pane", "workspace"]
command = ["bash", "herdr/pane.sh", "close"]

# Auto-open a reviewr pane when a worktree workspace is born (gated by auto_open; see herdr/pane.sh).
[[events]]
on = "worktree.created"
command = ["bash", "herdr/pane.sh", "auto-open"]

[[events]]
on = "worktree.opened"
command = ["bash", "herdr/pane.sh", "auto-open"]
```

### Where plugins live on disk

Observed on this Linux machine `[LIVE]`
(`find ~/.config/herdr -maxdepth 3 -type d`, `find ~/.local/state/herdr -maxdepth 3`):

```
/home/huke/.config/herdr/plugins.json                          # registry
/home/huke/.config/herdr/plugins/github/<plugin_id>-<hash>/    # managed GitHub checkout = HERDR_PLUGIN_ROOT
/home/huke/.config/herdr/plugins/config/<plugin_id>/           # HERDR_PLUGIN_CONFIG_DIR
/home/huke/.local/state/herdr/plugins/<plugin_id>/             # HERDR_PLUGIN_STATE_DIR
```

Concretely: `herdr plugin config-dir usagebar` → `/home/huke/.config/herdr/plugins/config/usagebar`
`[LIVE]`. The managed checkout for `huketo.hitl` is
`/home/huke/.config/herdr/plugins/github/huketo.hitl-cd696300a29f` `[LIVE]`.

The base config directory is the documented per-OS one:

```
Linux and macOS: ~/.config/herdr/config.toml
Windows:          %APPDATA%\herdr\config.toml
```
`[DOC:config]`. `herdr --help` prints the resolved path — on this machine
`Config: /home/huke/.config/herdr/config.toml` `[BIN]`, and `HERDR_CONFIG_PATH` overrides it `[BIN]`.

`HERDR_PLUGIN_STATE_DIR` resolves to `~/.local/state/herdr/plugins/<plugin_id>/`
(reviewr observed this on 0.7.1) `[REVIEWR]` `docs/herdr-api-notes.md`, and the directory
listing above confirms it still holds on 0.8.2 `[LIVE]`. **The macOS and Windows spellings of
the state directory are not verified** — see *Could not verify*.

Roles, from the docs:

> `HERDR_PLUGIN_ROOT` is the installed or linked plugin directory. Do not store
> user credentials or durable state there, because GitHub-installed plugin roots
> are managed source checkouts. Put user-editable config such as `.env` files
> under `HERDR_PLUGIN_CONFIG_DIR`, and put local runtime state under
> `HERDR_PLUGIN_STATE_DIR`. `[DOC:plugins]`

### Discovery and registration

There is no directory scan. Registration is explicit and recorded in a registry file:

> Installed and linked plugins persist across restarts. Herdr writes a
> `plugins.json` registry file alongside `session.json` on `plugin.link`,
> `plugin.unlink`, `plugin.enable`, and `plugin.disable`. The `herdr plugin
> install` and `herdr plugin link` CLIs also write the same registry when Herdr
> is not running, then startup loads it automatically. On startup, Herdr
> re-reads each manifest from its original path; if the file is missing or
> unparseable, the entry is kept with a `warnings` field so `plugin.list`
> surfaces it. `[DOC:socket]`

Confirmed on disk: `/home/huke/.config/herdr/plugins.json` (8 731 bytes) exists next to
`/home/huke/.config/herdr/session.json` `[LIVE]`. Its records carry the full parsed manifest,
e.g. `{"plugin_id":"huketo.hitl","name":"Herdr HITL","version":"0.1.4","min_herdr_version":"0.8.0",...,"manifest_path":"...","plugin_root":"...","enabled":true,"platforms":[...],"build":[...],"startup":[...],"actions":[...]}` `[LIVE]`.

**Scope is per-user and global, not per-session:**

> Plugin installation and enabled state are global to the current user. A plugin
> installed, linked, enabled, or disabled through one Herdr session is
> immediately available with the same state in every session. `[DOC:cli]`

> Both `plugin install` and `plugin link` can register plugins while no Herdr
> server is running. `[DOC:plugins]`

### Distribution and installation

`herdr plugin --help` `[BIN]`:

```
Install and run workflow plugins

Usage: herdr plugin [COMMAND]

Commands:
  install     Install a plugin from GitHub
  uninstall   Uninstall a plugin
  link        Link a local plugin
  unlink      Unlink a local plugin
  enable      Enable a plugin
  disable     Disable a plugin
  list        List installed plugins
  config-dir  Print a plugin config directory
  action      List or invoke plugin actions
  log         Inspect plugin command logs [aliases: logs]
  pane        Manage plugin-owned panes
```

Exact signatures `[BIN]` (`herdr plugin install --help`, `herdr plugin link --help`, …):

```
herdr plugin install [OPTIONS] <OWNER/REPO[/SUBDIR]>   # --ref <REF>, -y/--yes
herdr plugin link    [OPTIONS] <PATH>                  # --disabled | --enabled
herdr plugin list    [OPTIONS]                         # --plugin <ID>, --json
herdr plugin uninstall <PLUGIN>                        # plugin id OR owner/repo[/subdir]
herdr plugin unlink  <PLUGIN_ID>
herdr plugin enable  <PLUGIN_ID>
herdr plugin disable <PLUGIN_ID>
herdr plugin config-dir <PLUGIN_ID>
```

Distribution is **GitHub-shorthand only**:

> `plugin install` accepts GitHub shorthand only, such as `owner/repo/subdir`. It
> clones with `git`, shows a preview in interactive terminals, runs supported
> build commands, then stores the checkout under Herdr-managed plugin data and
> registers it. `[DOC:plugins]`

There is **no `plugin update`**:

> There is no separate `plugin update` in v1; reinstall from GitHub to refresh a
> managed plugin. `[DOC:plugins]`

Discovery for humans is an automatic index:

> Community plugins are discoverable in the marketplace, an automatic index of
> public GitHub repositories tagged with `herdr-plugin` that contain one or more
> `herdr-plugin.toml` files whose required metadata can be parsed. … The index
> refreshes every 30 minutes. `[DOC:plugins]`

**Implications for herdr-cron `[INFERENCE]`:** shipping as a plugin means a one-line install
(`herdr plugin install <owner>/herdr-cron`), automatic marketplace listing via the
`herdr-plugin` topic, and Herdr-managed config/state directories for free. It also means
"update" is "uninstall + install", so the plugin must tolerate its `HERDR_PLUGIN_ROOT` being
replaced wholesale — schedule state must live under `HERDR_PLUGIN_STATE_DIR`, never in the root.

---

## 2. The plugin contract

### What Herdr invokes

Herdr spawns **argv commands**, directly, without a shell `[DOC:plugins]`. There are five
invocation kinds, all declared in the manifest — nothing is registered at runtime:

> Runtime action registration and native non-terminal plugin UI are not part of
> plugin v1. Actions, event hooks, panes, and link handlers are all declared in
> the manifest. `[DOC:plugins]`

| Kind | Manifest | When it runs | Lifetime |
| --- | --- | --- | --- |
| Build | `[[build]]` | during `plugin install`, after preview, before registration | run to completion; failure aborts install `[DOC:plugins]` |
| Startup | `[[startup]]` | once per enabled plugin after session restore + socket ready; again on live handoff | one-shot; must exit `[DOC:plugins]` |
| Action | `[[actions]]` | keybinding, `plugin action invoke`, link handler | one-shot `[DOC:plugins]` |
| Event hook | `[[events]]` | when a matching event fires | one-shot `[DOC:plugins]` |
| Pane | `[[panes]]` | `plugin pane open` | long-running terminal process `[DOC:plugins]` |

### Working directory

> Runtime commands run with the plugin directory as their working directory. `[DOC:plugins]`

Except panes. reviewr found this the hard way and wrote it down:

> **Pane command resolves against the pane's cwd (`--cwd`, the repo), not the plugin
> root** — a relative `./target/...` path fails, so the manifest invokes the binary by
> absolute path under `$HERDR_PLUGIN_ROOT`. `[REVIEWR]` `docs/herdr-api-notes.md`

That is why the reviewr manifest reads
`command = ["sh", "-c", "exec \"$HERDR_PLUGIN_ROOT/bin/herdr-reviewr\""]` `[REVIEWR]`, and
sheep does the identical thing `[SHEEP]`.

Build commands are also special:

> Build commands are plain argv commands too, but they do not receive runtime
> plugin context or Herdr socket env. `[DOC:plugins]`

reviewr's build script works around exactly that:

```bash
# The build runs with the plugin checkout as the working directory, so we resolve the plugin root
# from this script's location rather than $HERDR_PLUGIN_ROOT (build commands may not receive the
# runtime env). At runtime the pane command reads $HERDR_PLUGIN_ROOT/bin/herdr-reviewr.
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
```
`[REVIEWR]` `herdr/install.sh`

### Environment variables

Always injected into runtime commands `[DOC:plugins]`, `[DOC:socket]`:

| Variable | Notes |
| --- | --- |
| `HERDR_SOCKET_PATH` | raw socket / named pipe; OS-specific transport |
| `HERDR_BIN_PATH` | the running Herdr binary — **the portable way to call back** |
| `HERDR_ENV=1` | "am I inside Herdr" flag; the agent skill gates on `test "${HERDR_ENV:-}" = 1` `[SKILL]` |
| `HERDR_PLUGIN_ID` | |
| `HERDR_PLUGIN_ROOT` | installed/linked directory |
| `HERDR_PLUGIN_CONFIG_DIR` | user-editable config |
| `HERDR_PLUGIN_STATE_DIR` | durable runtime state |
| `HERDR_PLUGIN_CONTEXT_JSON` | full invocation context |
| `HERDR_WORKSPACE_ID`, `HERDR_TAB_ID`, `HERDR_PANE_ID` | when available |

Kind-specific `[DOC:plugins]`:

- actions also get `HERDR_PLUGIN_ACTION_ID`
- startup hooks get `HERDR_PLUGIN_EVENT=startup`
- event hooks get `HERDR_PLUGIN_EVENT` and `HERDR_PLUGIN_EVENT_JSON`
- pane commands get `HERDR_PLUGIN_ENTRYPOINT_ID`
- link-handler actions also get `HERDR_PLUGIN_CLICKED_URL` and `HERDR_PLUGIN_LINK_HANDLER_ID`

Herdr-owned variables win over caller `--env`:

> Herdr-managed variables such as `HERDR_SOCKET_PATH`, `HERDR_BIN_PATH`, `HERDR_ENV`,
> `HERDR_WORKSPACE_ID`, … stay authoritative when they conflict with caller-provided env.
> `[DOC:cli]`

`HERDR_PLUGIN_CONTEXT_JSON` shape, from the binary's schema
(`jq -c '.schemas.request["$defs"].PluginInvocationContext'`) `[SCHEMA]`:

```
clicked_url, correlation_id, focused_pane_agent, focused_pane_cwd, focused_pane_id,
focused_pane_status, invocation_source, link_handler_id, selected_text, tab_id, tab_label,
workspace_cwd, workspace_id, workspace_label, worktree
```

All nullable. `focused_pane_status` is an `AgentStatus`. `worktree` is a `WorkspaceWorktreeInfo`.

Two sharp edges reviewr documented and both are still worth honouring:

> herdr runs plugin commands with a minimal `PATH`; prepend common bin dirs for `jq`/`git`.
> `[REVIEWR]` `docs/herdr-api-notes.md`

reviewr's own mitigation, verbatim `[REVIEWR]` `herdr/pane.sh`:

```bash
# herdr runs plugin commands with a minimal PATH; ensure jq/git resolve on common installs.
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:${PATH:-}"
```

and:

> **`focused_pane_cwd` is the pane's *launch* cwd, not its live one** (observed, 0.7.5: a pane
> running `claude -w <worktree>` reported the main checkout it was launched from, while the
> agent process had chdir'd into the worktree). `herdr pane get <id>` carries both:
> `.result.pane.cwd` (launch) and `.result.pane.foreground_cwd` (live foreground process).
> `[REVIEWR]` `docs/herdr-api-notes.md`

I confirmed both fields still exist on 0.8.2: a `pane list` entry carries `"cwd":"/home/huke"`
and `"foreground_cwd":"/tmp"` on the same pane `[LIVE]` (`herdr pane list | jq -c '.result.panes[0]'`).

### stdin / stdout / exit codes

There is **no stdin/stdout protocol between Herdr and the plugin**. Herdr captures the
streams for its log and does not interpret them:

> Build failures show the plugin id, build index, working directory, command,
> exit status or spawn error, and capped stdout/stderr **without interpreting tool
> output**. `[DOC:plugins]` (emphasis mine)

The captured record is visible through `plugin log list`. Live example `[LIVE]`
(`herdr plugin log list --limit 2 | jq -c '.result.logs[0]'`):

```json
{"command":["bash","bin/run-status.sh"],"event":"pane.focused","exit_code":0,
 "finished_unix_ms":1788311935565,"log_id":"plugin-log-1516","plugin_id":"usagebar",
 "started_unix_ms":1788311935047,"status":"succeeded","stderr":"","stdout":""}
```

So exit code and both streams are recorded, keyed by `log_id`, with `status` and
millisecond timestamps. Exit code is meaningful for **build** commands (non-zero aborts the
install `[DOC:plugins]`). For actions and event hooks I found no documented behaviour tied to
exit code beyond the log record — see *Could not verify*. reviewr treats it as advisory and
designs for the log:

> Actions refuse loudly (exit 1, one stderr line) and report successes on stdout; a refused
> event reports its config error through stderr for herdr's plugin log. `[REVIEWR]` `herdr/pane.sh`

Startup failure is explicitly non-fatal:

> Herdr starts them asynchronously and records their completion in the normal
> plugin command log. A startup failure does not stop the server. `[DOC:plugins]`

### Is there an RPC/IPC channel?

Yes — but it is the *same* one every CLI user gets, in the reverse direction. The plugin is
a client of Herdr, never a server Herdr calls into.

> There is no separate plugin SDK or restricted command set. **The entire Herdr CLI is
> the plugin API.** Every command in the CLI reference is available to a plugin, and a
> plugin can run anything you can run yourself as `herdr ...`. Most plugins should call
> Herdr through `HERDR_BIN_PATH` … Use the socket API when you want to send raw JSON
> requests yourself. `[DOC:plugins]` (emphasis mine)

Transport `[DOC:socket]`:

> Herdr uses newline-delimited JSON over a local socket. On Unix, that socket is a
> Unix domain socket. On Windows, it is a named pipe.

```
{"id":"req_1","method":"ping","params":{}}
{"id":"req_1","result":{"type":"pong"}}
```

Socket resolution order `[DOC:socket]`: explicit `--session <name>` → `HERDR_SOCKET_PATH` →
`HERDR_SESSION=<name>` → default session socket. Paths:
`~/.config/herdr/herdr.sock` and `~/.config/herdr/sessions/<name>/herdr.sock`. Confirmed live:
`herdr session list --json` → `{"sessions":[{"default":true,"name":"default","running":true,"session_dir":"/home/huke/.config/herdr","socket_path":"/home/huke/.config/herdr/herdr.sock"}]}` `[LIVE]`.

Long-lived subscriptions keep the connection open:

> Event subscriptions keep the connection open after the initial response. `[DOC:socket]`

### Docs vs. code disagreements found

1. **`contexts = ["global"]` is undocumented on the plugins page** but is in the binary's
   schema enum `[SCHEMA]` and used by two installed plugins `[HITL]`, `[SHEEP]`. Docs
   understate; code wins.
2. **`plugin pane close` is narrower than its name suggests.** reviewr:
   > **`plugin pane close` only closes panes in the in-memory plugin-pane registry** — after a
   > herdr restart it refuses a still-live pane with `plugin_pane_not_found` (observed, 0.7.1),
   > and a layout-launched pane was never registered at all. Plain `herdr pane close <pane_id>`
   > closes any pane by id; `pane.sh` sweeps with it. `[REVIEWR]` `docs/herdr-api-notes.md`

   `[DOC:socket]` says only "`plugin.pane.focus` and `plugin.pane.close` continue to operate on
   those panes" and does not mention the restart hole. reviewr's code follows its own finding
   and uses plain `pane close` `[REVIEWR]` `herdr/pane.sh`.
3. **`--workspace` alone is insufficient for a split plugin pane.** reviewr:
   > A `split` (or `zoomed`) pane **must** pass `--target-pane` (it implies the workspace);
   > `--workspace` alone errors. `[REVIEWR]` `docs/herdr-api-notes.md`

   `[DOC:socket]` says "Split and zoomed panes target an existing pane; tab panes can target a
   workspace" — consistent in substance, but the CLI's own `--help` lists `--workspace` and
   `--target-pane` as peers with no such constraint `[BIN]`.
4. **`plugin list --json` field names differ from the docs' prose.** The live record uses
   `plugin_id` and `plugin_root`, not `id`/`root` `[LIVE]`
   (`herdr plugin list --json | jq -r '.result.plugins[0]|keys[]'` →
   `actions build description enabled manifest_path min_herdr_version name platforms plugin_id plugin_root source startup version`).
   Note `events`, `panes`, `link_handlers`, `config_dir`, `state_dir`, and `warnings` are
   **absent** from that key set for the plugins installed here — `warnings` is documented to
   appear only when there is a warning `[DOC:socket]`, and the others are apparently omitted
   when empty. Do not assume they are present.

---

## 3. Language: is Go first-class?

**Yes. Every language is equally first-class, and Go specifically is proven on this machine.**

> A plugin can be a Bash script, JavaScript app, Lua script, Rust binary, or any
> other argv command your machine can run. Herdr owns the host surface … The
> plugin owns its implementation language, dependencies, files, and durable state.
> `[DOC:plugins]`

> This example uses Node, but nothing about plugins requires Node. The manifest
> could launch Bash, PowerShell, Python, Rust, Go, Lua, Bun, or any other command
> available on the user's machine. `[DOC:plugins]`

**herdr-reviewr is Rust, not Go.** `Cargo.toml` declares `herdr-reviewr` with 13 dependencies
including `ratatui 0.30`, `syntect 5.3.0`, `pulldown-cmark 0.13.4` `[REVIEWR]`. There is no
`go.mod`; the tree is `src/*.rs`, `Cargo.lock`, `rust-toolchain.toml`, `clippy.toml`,
`rustfmt.toml` `[REVIEWR]`.

**A Go plugin exists and is installed here.** `huketo.hitl` builds with the Go toolchain
directly from its manifest `[HITL]`:

```toml
# Build commands run on `herdr plugin install` (after the preview is confirmed,
# before the plugin is registered). They do NOT run on `herdr plugin link` —
# local authors build their own working tree, e.g. with `make build`.
# Requires the Go toolchain on PATH.
[[build]]
command = ["go", "build", "-trimpath", "-ldflags", "-s -w", "-o", "bin/herdr-hitl", "./cmd/herdr-hitl"]
```

`usagebar` likewise resolves a Go binary in its build step (`bash bin/ensure-binary.sh --in-tree`,
whose comment reads "If the hook fails (offline, **no Go**, no gh/curl) the install aborts")
`[USAGEBAR]`.

**Is there an official SDK or client library?** No.

> There is no separate plugin SDK or restricted command set. The entire Herdr CLI
> is the plugin API. `[DOC:plugins]`

There is therefore **no import path** to depend on. What exists instead:

- `herdr api schema --json` — the full JSON Schema for requests, success responses, error
  responses, emitted events, and subscription events, bundled with the binary you are talking
  to `[DOC:socket]`, `[BIN]`. `herdr api schema --output PATH` writes it to a file `[BIN]`.
- `herdr api snapshot` — the live `session.snapshot` response as JSON `[BIN]`, `[DOC:socket]`.

**Implications for herdr-cron `[INFERENCE]`:** Go is viable with no friction. The
`herdr api schema --output` path is a real advantage — you can codegen Go request/response
types from the schema of the exact binary you support, and pin `min_herdr_version` to the
protocol you generated against. The cost of "no SDK" is that you own the socket client (or
shell out to `HERDR_BIN_PATH`, which is what the docs recommend and what every plugin read
here actually does — reviewr shells out from Rust rather than speaking the socket `[REVIEWR]`
`src/herdr.rs`).

reviewr's callback shim, verbatim `[REVIEWR]` `src/herdr.rs`:

```rust
fn herdr_bin() -> String {
    env::var("HERDR_BIN_PATH").unwrap_or_else(|_| "herdr".to_string())
}
```

The docs push the same way, for a concrete portability reason:

> The raw socket transport behind `HERDR_SOCKET_PATH` is OS-specific: Unix clients
> connect to a Unix socket path, while Windows clients connect to a named pipe.
> CLI calls through `HERDR_BIN_PATH` avoid that transport difference. `[DOC:plugins]`

Since herdr-cron is cross-platform including Windows, that argument lands hard `[INFERENCE]`.

---

## 4. Hooks and events

### The manifest hook surface (what a plugin can declare)

| Surface | Fires when | Payload |
| --- | --- | --- |
| `[[build]]` | during `plugin install` only; skipped by `plugin link` | **no** runtime env, **no** socket env `[DOC:plugins]` |
| `[[startup]]` | once per enabled plugin after session restore and socket ready; again on live-handoff server takeover; **not** on client attach, config reload, link, or enable | runtime env + `HERDR_PLUGIN_EVENT=startup` `[DOC:plugins]` |
| `[[actions]]` | keybinding, `plugin action invoke`, or a link handler | runtime env + `HERDR_PLUGIN_ACTION_ID` + `HERDR_PLUGIN_CONTEXT_JSON` `[DOC:plugins]` |
| `[[events]]` | a matching Herdr event | runtime env + `HERDR_PLUGIN_EVENT` + `HERDR_PLUGIN_EVENT_JSON` `[DOC:plugins]` |
| `[[panes]]` | `plugin pane open` | runtime env + `HERDR_PLUGIN_ENTRYPOINT_ID` `[DOC:plugins]` |
| `[[link_handlers]]` | Ctrl-modified click on a terminal URL matching a Rust regex | routes to a named action; adds `clicked_url`, `link_handler_id`, `invocation_source = "link_click"` `[DOC:plugins]` |
| `[[keys.command]]` (user config, **not** the manifest) | keypress | `type = "plugin_action"`, `command = "<plugin_id>.<action_id>"` `[DOC:plugins]` |

The keybinding is declared by the *user*, not the plugin. reviewr spells out the trap:

```toml
[[keys.command]]
key = "cmd+r"
type = "plugin_action"
command = "persiyanov.reviewr.toggle"   # <plugin_id>.<action_id> — plugin_id is the manifest `id`, not `name`
```
`[REVIEWR]` `docs/herdr-api-notes.md`

### The complete event name list

Two spellings exist and they matter. The **snake_case** list is `EventKind` — what the server
emits internally. The **dotted** list is what subscriptions and plugin `on =` values use.

`EventKind`, all 26, from the binary
(`jq -r '.schemas.event["$defs"].EventKind.enum[]' herdr-api-schema.json`) `[SCHEMA]`:

```
workspace_created            tab_created           pane_created
workspace_updated            tab_closed            pane_closed
workspace_metadata_updated   tab_renamed           pane_updated
workspace_closed             tab_moved             pane_focused
workspace_renamed            tab_focused           pane_moved
workspace_moved                                    pane_output_changed
workspace_reordered                                pane_exited
workspace_focused                                  pane_agent_detected
worktree_created                                   pane_agent_status_changed
worktree_opened                                    layout_updated
worktree_removed
```

The dotted subscription list, 27 entries, from `.schemas.request["$defs"].Subscription`
`[SCHEMA]` — with the parameters each accepts:

| Dotted type | Params | Required |
| --- | --- | --- |
| `workspace.created` / `.updated` / `.metadata_updated` / `.renamed` / `.moved` / `.reordered` / `.closed` / `.focused` | — | `type` |
| `worktree.created` / `.opened` / `.removed` | — | `type` |
| `tab.created` / `.closed` / `.focused` / `.renamed` / `.moved` | — | `type` |
| `pane.created` / `.closed` / `.updated` / `.focused` / `.moved` / `.exited` / `.agent_detected` | — | `type` |
| `pane.output_matched` | `pane_id`, `source`, `match`, `lines`, `strip_ansi` | `type`, `pane_id`, `source`, `match` |
| `pane.agent_status_changed` | `pane_id`, `agent_status` | `type`, `pane_id` |
| `pane.scroll_changed` | `pane_id` | `type`, `pane_id` |
| `layout.updated` | — | `type` |

The dotted spelling is confirmed as the plugin-hook spelling three ways: reviewr's manifest
uses `on = "worktree.created"` `[REVIEWR]`; usagebar's uses
`on = "pane.agent_status_changed"` and `on = "pane.focused"` `[USAGEBAR]`; and a live plugin
log record reads `"event":"pane.focused"` `[LIVE]`.

Validation is soft:

> Herdr validates event hook `on` values against known event names at link time.
> An unrecognised name does not block the link, but the returned plugin info
> includes a warning (e.g. `"unknown event 'worktree.craeted'"`). Check the
> `warnings` field in the `plugin.link` and `plugin.list` responses. `[DOC:plugins]`,
> `[DOC:socket]`

That is a trap `[INFERENCE]`: a typo'd hook silently never fires. herdr-cron's installer
should read `warnings` back from `plugin list --json` after install.

### Payloads, in detail

`worktree.opened`, captured live by reviewr on 0.7.5 — the raw `HERDR_PLUGIN_EVENT_JSON`
`[REVIEWR]` `docs/herdr-api-notes.md`:

```json
{
  "event": "worktree_opened",
  "data": {
    "type": "worktree_opened",
    "workspace": {
      "workspace_id": "w3W",
      "number": 3,
      "label": "branch-name",
      "focused": false,
      "pane_count": 1,
      "tab_count": 1,
      "active_tab_id": "w3W:t1",
      "agent_status": "idle",
      "worktree": {
        "repo_key": "repo-key",
        "repo_name": "repo",
        "repo_root": "/repo",
        "checkout_path": "/repo/.herdr/worktrees/branch-name",
        "is_linked_worktree": true
      }
    },
    "worktree": {
      "path": "/repo/.herdr/worktrees/branch-name",
      "branch": "branch-name",
      "is_bare": false,
      "is_detached": false,
      "is_prunable": false,
      "is_linked_worktree": true,
      "open_workspace_id": "w3W",
      "label": "repo"
    },
    "already_open": false
  }
}
```

Note the envelope: `event` carries the **snake_case** name even though the manifest hook is
dotted `[REVIEWR]`. The schema agrees — `EventEnvelope` requires `{event, data}` where
`event` is an `EventKind` `[SCHEMA]`.

Other payload shapes, from the docs `[DOC:socket]`:

- `worktree.created` — the opened `workspace` and created `worktree`
- `worktree.removed` — `workspace_id`, removed `worktree`, `forced`
- `pane.agent_status_changed` — requires `pane_id`; carries `workspace_id`, `pane_id`, `agent_status` `[SCHEMA]`
- `workspace.moved` — moved `workspace_id`, requested `insert_index`, updated ordered `workspaces`
- `workspace.reordered` — moved `workspace_ids`, optional `before_workspace_id`, authoritative ordered `workspaces`
- `workspace.closed` — final `workspace` snapshot when Herdr can still identify it
- `tab.moved` — `tab_id`, `workspace_id`, `insert_index`, updated ordered `tabs`
- `pane.scroll_changed` — `pane_id`, `workspace_id`, current `scroll` metrics
- `layout.updated` — the updated `PaneLayoutSnapshot` for one tab

Which lifecycle events a worktree command produces `[DOC:socket]`:

> `worktree.create` emits `workspace.created`, `tab.created`, `pane.created`, and
> `worktree.created`. `worktree.open` emits `worktree.opened`, and it also emits
> workspace/tab/pane creation events when it opens a new Herdr workspace.
> `worktree.remove` emits `worktree.removed`; if the linked workspace is still
> open, it also emits `workspace.closed`.

### Two documented exclusions

> `workspace.metadata_updated` reports token changes and TTL expiry **without invoking
> plugin event hooks**. `[DOC:socket]` (emphasis mine)

and

> This metadata event is available to API subscribers but does not invoke plugin event hooks.
> `[DOC:socket]`

**Can herdr-cron be event-driven or must it poll? `[INFERENCE]`**

For *scheduling by wall-clock time*, events are irrelevant — that is gocron's job and it needs
a process that stays alive.

For *reacting to agent completion*, there are three tiers, in decreasing preference:

1. **Server-owned blocking wait.** `agent.wait` / `agent prompt --wait` is
   > server-owned and event-driven. It pins the resolved pane occupant so a replacement
   > cannot satisfy the wait. `[DOC:socket]`

   This is the correct primitive for "run this job and tell me when it finished". No polling.
2. **A long-lived `events.subscribe` connection**, filtered to
   `pane.agent_status_changed` for the panes herdr-cron owns `[SCHEMA]`, `[DOC:socket]`.
   Needs a resident process holding the socket open.
3. **Manifest event hooks** — process-per-event, no filtering beyond the event name for the
   parameterless kinds. `pane.agent_status_changed` requires `pane_id` in a subscription
   `[SCHEMA]`, but a manifest `[[events]] on = ...` has nowhere to put one, and usagebar
   nevertheless uses `on = "pane.agent_status_changed"` successfully `[USAGEBAR]` with live
   log records to prove it fires `[LIVE]`. **Whether the hook receives every pane's transition
   or only the focused pane's is not established** — see *Could not verify*. usagebar's own
   manifest comment hints at the latter: "event hooks only see the focused pane" `[USAGEBAR]`.

Nothing in the surface requires polling for agent state. Polling would only be needed to
detect *pane disappearance* outside a wait, or to reconcile after a server restart `[INFERENCE]`.

---

## 5. Driving agents from a scheduler

### The exact command sequence

From `[SKILL]`, `[DOC:automation]`, and the binary's own `--help` `[BIN]`.

**Step 0 — confirm the environment.** The skill's own gate `[SKILL]`:

```bash
test "${HERDR_ENV:-}" = 1
```

**Step 1 — find or create an available shell pane.**

> `agent start` requires an existing available shell pane and never creates, splits,
> or moves layout. `[SKILL]`

> An available shell pane is at its interactive shell prompt: the shell itself owns
> the foreground, with no foreground command, editor, or agent running. `[DOC:automation]`

Discovery `[SKILL]`:

```bash
herdr workspace list
herdr tab list --workspace "$HERDR_WORKSPACE_ID"
herdr pane current --current
herdr pane list --workspace "$HERDR_WORKSPACE_ID"
herdr agent list
```

Creation. `herdr pane split --help` `[BIN]`:

```
Usage: herdr pane split [OPTIONS] [PANE_ID]

Options:
      --pane <ID>
      --current
      --direction <DIRECTION>   [possible values: right, down]
      --ratio <FLOAT>
      --cwd <PATH>
      --env <KEY=VALUE>         Set an environment variable for the launched process
      --right-click <TARGET>    [possible values: herdr, pane]
      --focus
      --no-focus
```

```bash
split=$(herdr pane split --current --direction right --cwd "$PWD" --no-focus)
review_pane=$(printf '%s\n' "$split" | jq -r '.result.pane.pane_id')
```
`[DOC:automation]`

ID fields to parse after creation `[SKILL]`, `[DOC:automation]`:

| Command | Fields |
| --- | --- |
| `workspace create` | `.result.workspace`, `.result.tab`, `.result.root_pane` |
| `tab create` | `.result.tab`, `.result.root_pane` |
| `pane split` | `.result.pane` (so `.result.pane.pane_id`) |
| `pane move` | `.result.move_result.pane.pane_id`, and `.result.move_result.previous_pane_id` |
| `plugin pane open` | `.result.plugin_pane.pane.pane_id` `[REVIEWR]` |
| `worktree create` | `.result.workspace`, `.result.tab`, `.result.root_pane`, `.result.worktree` `[DOC:socket]` |

ID format is `w1` / `w1:t1` / `w1:p1`; closed ids are never reused; a cross-workspace pane move
mints a new pane id `[SKILL]`.

`herdr workspace create --help` `[BIN]`:

```
Usage: herdr workspace create [OPTIONS]

Options:
      --cwd <PATH>
      --label <TEXT>
      --env <KEY=VALUE>   Set an environment variable for the launched process
      --focus
      --no-focus
```

**Step 2 — `agent start`.** `herdr agent start --help`, verbatim `[BIN]`:

```
Start a supported interactive agent in an existing pane

Usage: herdr agent start <NAME> --kind <KIND> --pane <ID> [OPTIONS] [-- [AGENT_ARG]...]

Arguments:
  <NAME>
  [AGENT_ARG]...

Options:
      --kind <KIND>
          Supported agent kind and canonical executable

          [possible values: pi, claude, codex, gemini, cursor, devin, agy, cline, omp,
           mastracode, opencode, copilot, kimi, kiro, droid, amp, grok, hermes, kilo,
           qodercli, qwen, maki]

      --pane <ID>
          Existing pane at an interactive shell prompt

      --timeout <MS>
          Wait for interactive readiness (default: 30000; max: 300000)

The pane must be at its interactive shell prompt. Success means the expected agent was
detected in the same terminal and is ready for input.

next: herdr agent prompt <TARGET> <TEXT> --wait
```

22 kinds. The socket schema agrees exactly — `AgentStartParams` requires `name`, `kind`,
`pane_id`, with optional `args` and `timeout_ms` ("must be greater than 3000 and at most
300000") (`jq -c '.schemas.request["$defs"].AgentStartParams'`) `[SCHEMA]`.

**Naming rules.** `[a-z][a-z0-9_-]{0,31}`, unique among live agents `[SKILL]`, `[DOC:automation]`:

> Names must match `[a-z][a-z0-9_-]{0,31}` and be unique among live agents. The alias is
> cleared when that agent exits, is released, or is replaced; it does not permanently rename
> the pane. `[DOC:automation]`

**A herdr-cron consequence `[INFERENCE]`:** a job name cannot be reused while a previous run's
agent is still live, and the name silently evaporates when the agent exits. Job identity must
be herdr-cron's own (job id + run id), with the Herdr name derived and treated as ephemeral.

Startup failure mode:

> If detection reports `blocked` during startup, the command returns `agent_not_ready`
> immediately. The name remains available for `agent read` and `agent send-keys`, and becomes
> ready for prompts after detection reports `idle`. `[DOC:automation]`

Native agent args go after `--` `[BIN]`, `[DOC:automation]`:

```bash
herdr agent start reviewer --kind codex --pane "$review_pane" -- -m gpt-5.4
```

**Step 3 — `agent prompt --wait --timeout`.** `herdr agent prompt --help`, verbatim `[BIN]`:

```
Submit a prompt to an agent

Usage: herdr agent prompt <TARGET> <TEXT> [OPTIONS]

Options:
      --wait
          Wait for the first matching state observed after submission
      --until <STATUS>
          State to match after --wait; repeat for more than one state
          [possible values: idle, working, blocked, done, unknown]
      --timeout <MS>
          Fail after this many milliseconds

If the agent is already blocked, submission is rejected with agent_blocked before any input
is sent. When an accepted submission starts from another non-working state, --wait first
requires an observed state change within 5000ms; otherwise it returns agent_prompt_stalled.
A shorter --timeout returns timeout instead. It then matches idle, done, or blocked by
default, or any exact --until state. It does not track turns: if the agent is already
working, that active turn's completion may match. Without --timeout, the settled-state wait
is indefinite.
```

`--until` requires `--wait` `[DOC:automation]`. At the socket level, prompt+wait is one
request, deliberately:

> `agent.prompt` accepts an optional `wait` object with `until` and `timeout_ms`; this submits
> the prompt and starts the wait in one request, avoiding a race between separate calls.
> `[DOC:socket]`

**Step 4 — `agent wait --until`.** `herdr agent wait --help`, verbatim `[BIN]`:

```
Wait until an agent reaches one of the requested states

Usage: herdr agent wait <TARGET> [OPTIONS]

Options:
      --until <STATUS>   [possible values: idle, working, blocked, done, unknown]
      --timeout <MS>

Without --until, matches idle, done, or blocked. Use --until unknown explicitly when needed.
Without --timeout, waits indefinitely.
```

> Standalone `agent wait` observes the current agent and returns immediately if its status
> already matches. `[DOC:automation]`

**Step 5 — `agent read --source`.** `herdr agent read --help`, verbatim `[BIN]`:

```
Read agent terminal output

Usage: herdr agent read <TARGET> [OPTIONS]

Options:
      --source <SOURCE>   Terminal snapshot source (default: recent)
                          [possible values: visible, recent, recent-unwrapped, detection]
      --lines <N>
      --format <FORMAT>   [possible values: text, ansi]
      --ansi
```

Source semantics `[SKILL]`:

- `visible` — the currently rendered viewport
- `recent` — recent rendered output, including soft wraps
- `recent-unwrapped` — soft wraps joined; **prefer for logs and transcripts**
- `detection` — the plain-text bottom-buffer snapshot used for agent detection

The socket returns the text at `.result.read.text` `[DOC:automation]`. Default row count is 80
for recent sources `[DOC:automation]`.

**The alternate-screen problem, which will bite a scheduler `[INFERENCE]`:**

> Full-screen agents such as Claude Code and OpenCode render transcript history in the
> terminal's alternate screen instead of Herdr's host scrollback. For an idle, recognized
> agent at the bottom of its transcript, text reads from `recent` or `recent-unwrapped`
> automatically use the agent's mouse-scroll interface when `--lines` requests more than the
> visible screen. … An explicit `agent read --lines N` that needs alternate-screen history
> returns `agent_not_idle` while the agent is working, blocked, or unknown. `[DOC:automation]`

The documented fallback, from both the skill and the automation page:

> ask the agent to write its complete response as Markdown in a temporary directory and reply
> only with the file path, then read the file directly. Use this only as a fallback; do not
> request file output in the initial prompt. `[SKILL]`

For a *scheduled* job, "capture the output reliably" is a hard requirement, and the caveat
"do not request file output in the initial prompt" is advice aimed at interactive use
`[INFERENCE]`. A scheduler that must archive run output has a real reason to ask for a file
up front.

### Detecting completion vs. blocked vs. unknown

The five states are the same enum everywhere
(`jq -c '.schemas.request["$defs"].AgentStatus'` → `{"enum":["idle","working","blocked","done","unknown"],"type":"string"}`) `[SCHEMA]`.

Semantics, verbatim `[SKILL]`:

> `idle` means the agent is ready for input and its tab has been seen in the focused Herdr UI.
> `done` is the same underlying idle state after unseen background work finishes. Focusing the
> tab or targeting the pane or agent with a focus command marks it seen. **CLI reads do not
> mark it seen.** `blocked` means Herdr recognized an approval or question UI. `unknown` means
> an agent is present but Herdr cannot classify it confidently; **it does not prove
> completion.** (emphasis mine)

This matters enormously for a scheduler `[INFERENCE]`:

- `done` is the natural terminal state for unattended work — it means "finished and nobody has
  looked at it". `herdr agent read` does **not** consume it. So a scheduled job can complete,
  be read and archived by herdr-cron, and still show `done` in the sidebar for the human.
  Calling `agent focus` would destroy that signal.
- `blocked` is a *stalled* job needing a human. It is deliberately conservative:
  > Herdr only marks `blocked` when the live bottom-buffer snapshot matches known visible
  > approval, question, or permission UI. If no manifest rule matches for a known agent, Herdr
  > falls back to `idle` and labels that fallback as `default_known_agent_idle_fallback` in
  > explain output. `[DOC:agents]`

  A new approval screen Herdr hasn't learned will read as `idle`, i.e. **a stalled job can look
  finished.** Diagnose with `herdr agent explain <target> --json` `[BIN]`, `[DOC:agents]`.
- `unknown` must never be treated as success — the skill says so outright `[SKILL]`.

Recommended discrimination for herdr-cron `[INFERENCE]`: prompt with
`--wait --until done --until blocked --timeout <budget>`, then branch on
`.result.agent.agent_status` from the response; treat a `timeout` error as a distinct
"overran" outcome, and cross-check a claimed-idle finish against the transcript.

### Response fields to parse

> Successful `agent start`, `agent prompt`, and `agent wait` commands return the current agent
> at `.result.agent`. `pane wait-output` returns `.result.pane_id`, `.result.matched_line`, and
> the matched snapshot at `.result.read`. `[DOC:automation]`

Response type constants and their fields, from the binary's schema `[SCHEMA]`:

```
agent_started  -> {type, agent, argv}
agent_prompted -> {type, agent}
agent_info     -> {type, agent}
notification_show -> {type, shown, reason}
plugin_pane_opened -> {type, plugin_pane}
worktree_created -> {type, workspace, worktree, tab, root_pane}
```

A live `agent get` returns the envelope
`{"id":"cli:agent:get","result":{"type":"agent_info","agent":{…}}}` `[LIVE]`
(`herdr agent get w1S:p1`). The `agent` object's fields are exactly the `agent list` entry
fields below, including `agent_session` (`{agent, kind, source, value}`) and the display-only
`tokens` map (observed: `{"context":"⛁ 5% (46k)","limit":"7d 68%","provider":"codex","title":"huke"}`).

The full key set of an `agent list` entry on 0.8.2 `[LIVE]`
(`herdr agent list | jq -r '.result.agents[0]|keys[]'`):

```
agent  agent_session  agent_status  cwd  focused  foreground_cwd  pane_id  revision
screen_detection_skipped  state_change_seq  tab_id  terminal_id  terminal_title
terminal_title_stripped  tokens  workspace_id
```

Note `name`, `display_agent`, and `state_labels` are **absent** here — reviewr documented
exactly this:

> `name`, `display_agent`, and `state_labels` are omitted entirely until something sets them.
> `herdr agent rename <pane> <name>` makes `name` appear; `--clear` leaves it present and null.
> `[REVIEWR]` `docs/herdr-api-notes.md`

`agent list` takes no flags at all (`herdr agent list --help` → `Usage: herdr agent list`)
`[BIN]`, so all filtering is the caller's job `[REVIEWR]`.

### Error handling

> CLI server errors are JSON on stderr with exit status 1. CLI syntax errors exit with
> status 2. `[SKILL]`

> Wait commands have no default timeout and can wait indefinitely. `[DOC:automation]`

The envelope, verified live by reviewr across three different commands `[REVIEWR]`
`docs/herdr-api-notes.md`:

```
{"error":{"code":"pane_not_found","message":"pane w8:p2 not found"},"id":"cli:request"}
```

The schema defines `ErrorBody` as `{code: string, message: string}` with **no enum**
(`jq -c '.schemas.error_response'`) `[SCHEMA]` — codes are open-ended. Codes named in prose
across the sources, useful for a scheduler's branch table:

`agent_blocked`, `agent_prompt_stalled`, `agent_not_ready`, `agent_not_running`,
`agent_not_idle`, `timeout` `[BIN]`, `[DOC:automation]`; `pane_not_found`,
`plugin_pane_not_found` `[REVIEWR]`; `plugin_disabled`, `platform_unsupported`, `ui_busy`,
`popup_not_open`, `stream_conflict`, `feature_disabled`, `invalid_params`, `not_found`
`[DOC:socket]`; `confirmation_required` `[DOC:cli]`.

### The headless question — can a background scheduler work with no UI attached?

**Yes for pane/agent orchestration. Partially for notifications. This is the single most
important finding in this document.**

Evidence, strongest first.

**(a) Herdr is server/client by construction, and the server owns the panes.**

> Herdr keeps panes running in a background server. Your terminal client can detach and
> reconnect later. `[DOC:persist]`

> Detach the client with `ctrl+b q`; panes and agents keep running. `[DOC:persist]`

**(b) There is an explicit headless server command.**

`herdr server --help` `[BIN]`:

```
Run or control the headless server

Usage: herdr server [COMMAND]

Commands:
  stop                    Stop the running server
  reload-config           Reload config in the running server
  agent-manifests         Show active agent detection manifests
  update-agent-manifests  Fetch and reload agent detection manifests
  reload-agent-manifests  Reload local agent detection manifest overrides
```

and `herdr --help` lists it under *Advanced commands*: `herdr server  Run as headless server`
`[BIN]`. The reference is explicit about intent:

> `herdr server` runs the headless server explicitly. Use it for supervised or service-style
> setups. `[DOC:cli]`

**(c) Herdr has a documented no-client geometry fallback — proof that pane creation with no UI
is a supported path, not an accident.**

> When no client is attached, the server uses a 120×40 virtual terminal for layout and newly
> created panes. Change that fallback **for headless orchestration** with:
>
> ```
> [server]
> headless_cols = 160
> headless_rows = 50
> ```
>
> An attached client remains authoritative for the shared runtime size. After it detaches,
> existing pane PTYs retain their last attached size while new headless layout uses the
> configured fallback. `[DOC:config]` (emphasis mine)

**(d) The running server on this machine advertises a detached-daemon capability.**

`herdr status --json` `[LIVE]`:

```json
{"client":{"version":"0.8.2","channel":"stable","protocol":20,
 "binary":"/home/huke/.local/bin/herdr","session":null},
 "server":{"status":"running","running":true,"version":"0.8.2","protocol":20,
 "capabilities":{"live_handoff":true,"detached_server_daemon":true},
 "compatible":true,"socket":"/home/huke/.config/herdr/herdr.sock","session":null,
 "restart_needed":false},"update":{"restart_needed":false}}
```

**(e) Plugin registration does not need a running server at all.**

> Both `plugin install` and `plugin link` can register plugins while no Herdr server is
> running. `[DOC:plugins]`

**What does *not* work headless.** Notifications degrade:

> Possible reasons are `shown`, `disabled`, `rate_limited`, **`no_foreground_client`**, and
> `busy`. … Terminal and system delivery are best-effort through the current foreground
> attached Herdr client. `[DOC:socket]` (emphasis mine)

So with nothing attached, `notification.show` returns `{"shown": false, "reason":
"no_foreground_client"}`. Same for `client.window_title.set` `[DOC:socket]`.

**Plain statement for the herdr-cron engineer `[INFERENCE]`:** a background scheduler daemon
*can* create workspaces/tabs/panes, start agents, prompt them, wait on their lifecycle, read
their output, and create worktrees with no human attached — the server does all of it. What it
*cannot* do is guarantee the human sees a toast; `notification show` is fire-and-hope, and its
`shown`/`reason` fields must be checked and a durable fallback used when `shown` is false.
There is also a real prerequisite: **something must have started the server.** Herdr's server
lifecycle is normally driven by a human running `herdr`; `herdr server` exists for
service-style setups but I did not run it (mutating), so its exact foreground/daemonize
behaviour is unverified — see *Could not verify*.

---

## 6. Notifications

The entire command group `[BIN]` (`herdr notification --help`):

```
Show Herdr notifications

Usage: herdr notification [COMMAND]

Commands:
  show  Show a notification
```

`herdr notification show --help`, verbatim `[BIN]`:

```
Show a notification

Usage: herdr notification show [OPTIONS] <TITLE>

Arguments:
  <TITLE>

Options:
      --body <TEXT>
      --position <POSITION>   [possible values: top-left, top-right, bottom-left, bottom-right]
      --sound <SOUND>         [possible values: none, done, request]
```

Documented example `[DOC:socket]`:

```bash
herdr notification show "build failed" --body "api workspace" --position top-left --sound request
```

Raw request and response `[DOC:socket]`:

```json
{"id":"req_notify","method":"notification.show","params":{"title":"build failed","body":"api workspace","position":"top-left","sound":"request"}}
{"id":"req_notify","result":{"type":"notification_show","shown":true,"reason":"shown"}}
```

Params schema (`jq -c '.schemas.request["$defs"].NotificationShowParams'`) — only `title` is
required `[SCHEMA]`.

Constraints, all from `[DOC:socket]`:

- `title` must contain visible text after control characters and repeated whitespace are removed; an empty sanitized title returns `invalid_params`.
- Newlines, tabs, CRs and repeated whitespace collapse to single spaces.
- `title` is trimmed to **80 characters**, `body` to **240 characters**.
- `position` applies only when `ui.toast.delivery = "herdr"`; terminal, system, and off delivery ignore it.
- `sound` defaults to `none`; `done` and `request` reuse the existing finished / needs-attention sounds and play only when the notification is actually shown `[DOC:cli]`.
- Response `reason` ∈ `shown | disabled | rate_limited | no_foreground_client | busy`.

**Implications for herdr-cron `[INFERENCE]`:** 80/240 characters is very tight for a job
report — the toast can carry "job X: 3 findings" and nothing more. The real result must land
somewhere durable (a run log under `HERDR_PLUGIN_STATE_DIR`, or a file the toast names). And
because `shown:false / reason:no_foreground_client` is the *expected* case for an unattended
overnight run, herdr-cron must never treat "notified" as "reported"; the toast is a
best-effort nudge layered on top of a durable record. `rate_limited` and `busy` are equally
non-exceptional and must not be retried blindly.

---

## 7. Worktrees

Group `[BIN]` (`herdr worktree --help`):

```
Manage Git worktree-backed workspaces

Usage: herdr worktree [COMMAND]

Commands:
  list    List worktree workspaces
  create  Create and open a Git worktree
  open    Open an existing Git worktree
  remove  Remove a worktree checkout
```

Exact signatures `[BIN]` (`herdr worktree <cmd> --help`), matching `[DOC:cli]`:

```
herdr worktree list   [--workspace ID | --cwd PATH]
herdr worktree create [--workspace ID | --cwd PATH] [--branch NAME] [--base REF] [--path PATH]
                      [--label TEXT] [--focus] [--no-focus]
herdr worktree open   [--workspace ID | --cwd PATH] (--path PATH | --branch NAME)
                      [--label TEXT] [--focus] [--no-focus]
herdr worktree remove --workspace ID [--force]
```

Semantics `[DOC:cli]`, `[DOC:socket]`:

> Worktrees are normal Herdr workspaces with Git checkout provenance. `worktree create`
> creates a Git worktree checkout, opens it as a workspace, and groups it with the parent repo
> workspace. If `--branch` names an existing local branch, Herdr checks it out; otherwise it
> creates the branch from `--base` or `HEAD`. Without `--path`, Herdr creates the checkout
> under `<worktrees.directory>/<repo>/<branch-slug>`. `[DOC:cli]`

> `worktree.remove` runs `git worktree remove` against a linked child workspace and **never
> deletes the branch**. `[DOC:socket]` (emphasis mine)

> `workspace close` closes only Herdr state. To delete the checkout, run `worktree remove`. It
> runs `git worktree remove`, never deletes the branch, and requires `--force` when Git refuses
> a dirty checkout. `[DOC:cli]`

Argument exclusivity `[DOC:socket]`:

> Use at most one of `workspace_id` or `cwd` for `worktree.list`, `worktree.create`, and
> `worktree.open`; omit both to use the active workspace. Use exactly one of `path` or `branch`
> for `worktree.open`. Raw socket `cwd` and `path` values must be absolute; the CLI expands
> relative `--cwd` and `--path` values before sending requests.

Default root `[DOC:config]`:

```toml
[worktrees]
directory = "~/.herdr/worktrees"
```

A live `worktree list` against this very repo `[LIVE]`
(`herdr worktree list --cwd /home/huke/huketo/herdr-cron`):

```json
{"id":"cli:worktree:list","result":{"source":{"repo_key":"/home/huke/huketo/herdr-cron/.git",
"repo_name":"herdr-cron","repo_root":"/home/huke/huketo/herdr-cron",
"source_checkout_path":"/home/huke/huketo/herdr-cron","source_workspace_id":"w2H"},
"type":"worktree_list","worktrees":[{"branch":"main","is_bare":false,"is_detached":false,
"is_linked_worktree":false,"is_prunable":false,"label":"herdr-cron","open_workspace_id":"w2H",
"path":"/home/huke/huketo/herdr-cron"}]}}
```

Raw request forms `[DOC:socket]`:

```json
{"id":"req_1","method":"worktree.create","params":{"workspace_id":"w1","branch":"worktree/api","focus":false}}
{"id":"req_2","method":"worktree.open","params":{"workspace_id":"w1","branch":"worktree/api","focus":true}}
{"id":"req_3","method":"worktree.remove","params":{"workspace_id":"2","force":false}}
```

`WorktreeCreateParams` fields, from the binary `[SCHEMA]`: `base`, `branch`, `cwd`, `focus`
(default `false`), `label`, `path`, `workspace_id` — **all optional**.

**Implications for herdr-cron `[INFERENCE]`:** worktrees are the natural isolation unit for a
scheduled job that writes code — one checkout per run, `--branch cron/<job>/<run>`, `--base main`,
`--no-focus`. The pattern reviewr already relies on (`worktree.created` / `worktree.opened` hooks)
proves the lifecycle is observable. Two cautions: `focus` defaults to `false` at the socket level
`[SCHEMA]` which is what a scheduler wants, and cleanup is a two-step (`worktree remove` deletes
the checkout, never the branch), so herdr-cron owns branch GC itself.

---

## 8. herdr-reviewr as a template

### It is Rust, not Go

`Cargo.toml`, `Cargo.lock`, `rust-toolchain.toml`, `clippy.toml`, `rustfmt.toml`, `src/*.rs`,
`tests/*.rs`, `examples/bench_latency.rs` `[REVIEWR]`. Dependencies include `ratatui 0.30`
(the Rust analogue of the role Bubble Tea plays for herdr-cron), `syntect`, `similar`,
`pulldown-cmark`, `toml`, `serde` `[REVIEWR]`.

None of that transfers directly. **The structural lessons do**, and they are unusually good.

### Repo layout

```
herdr-plugin.toml          # the manifest, at repo root
herdr/install.sh           # the [[build]] step
herdr/pane.sh              # the action + event-hook dispatcher
src/*.rs                   # the binary
docs/herdr-api-notes.md    # verified host-API notes, version-stamped
docs/specs/<date>-<slug>/{spec.md,plan.md}
docs/RELEASING.md
docs/qa-install.md
policies/ux-responsiveness.md
scripts/qa-install.sh, scripts/swap-binary.sh, scripts/bench_tui.py
justfile                   # fmt / fmt-check / lint / test
.github/workflows/{ci,release,audit}.yml
CHANGELOG.md               # Keep a Changelog + SemVer
AGENTS.md  CONTRIBUTING.md  SECURITY.md  CODE_OF_CONDUCT.md  LICENSE
assets/demo.tape, assets/demo.gif
```
`[REVIEWR]`

Note what is **not** there: no goreleaser, no npm package, no curl-pipe-sh installer for
end users. Distribution is `herdr plugin install` and nothing else `[REVIEWR]` `README.md`.

### Build and release

`.github/workflows/release.yml`, three jobs, verbatim structure `[REVIEWR]`:

1. `create-release` — `taiki-e/create-gh-release-action@v1` with `changelog: CHANGELOG.md`,
   `draft: true`. The comment explains why:

   > The action parses the Keep-a-Changelog format and fails when the tag has no section, so a
   > forgotten changelog entry fails the release instead of shipping the bare compare link. …
   > the repo publishes immutable releases, which refuse asset uploads after publish — so
   > publish is the pipeline's last step, not its first.

2. `binaries` — matrix over four targets:

   ```yaml
   - { target: aarch64-apple-darwin, os: macos-latest }
   - { target: x86_64-apple-darwin, os: macos-latest }
   - { target: x86_64-unknown-linux-musl, os: ubuntu-latest }
   - { target: aarch64-unknown-linux-musl, os: ubuntu-latest }
   ```

   built by `taiki-e/upload-rust-binary-action@v1` with `archive: $bin-$target` and
   `checksum: sha256`, then `actions/attest-build-provenance@v4` for a Sigstore attestation
   (`gh attestation verify <asset> --repo persiyanov/herdr-reviewr`).

3. `publish` — `gh release edit "$GITHUB_REF_NAME" --draft=false`, last, so a failed target
   leaves a draft instead of a release that installs 404.

Trigger is `on: push: tags: ["v*"]` `[REVIEWR]`.

**No Windows target.** The manifest declares `platforms = ["macos", "linux"]` `[REVIEWR]`.
herdr-cron is cross-platform, so it needs a `windows` target and a `windows`-aware
`[[build]]` step. Herdr helps a little:

> On Windows, build commands, action commands, and event commands resolve common `PATHEXT`
> shims such as `npm.cmd`, `bun.cmd`, and `pnpm.cmd` when the bare command is on `PATH`. Pane
> commands use Herdr's normal Windows pane launcher and must still be valid Windows argv
> commands. `[DOC:plugins]`

Note the asymmetry: pane commands get **no** PATHEXT resolution `[DOC:plugins]`. A Go binary
invoked by absolute path sidesteps this entirely `[INFERENCE]`.

### The `[[build]]` step: download, don't compile

`herdr/install.sh`, the key mechanics `[REVIEWR]`:

```bash
NAME="herdr-reviewr"
REPO="persiyanov/herdr-reviewr"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN_DIR="$ROOT/bin"

# The release tag matches the manifest version, so a checkout always pulls its own release.
VERSION="$(grep -m1 '^version' "$ROOT/herdr-plugin.toml" | sed -E 's/.*"([^"]+)".*/\1/')"
TAG="v${VERSION}"
```

Then a `uname -s`/`uname -m` → target-triple table, a SHA-256 verification against a sidecar,
`install -m 0755`, and a retry policy with a documented reason:

```bash
# Release-asset downloads are eventually-consistent: GitHub's CDN can 404 for a few minutes
# after a release publishes, even though the asset exists. Retry (incl. on 404) so an install
# right after a release doesn't fail spuriously.
dl() { curl -fsSL --retry 5 --retry-delay 3 --retry-all-errors --retry-connrefused "$1" -o "$2"; }
```

And a symlink step whose comment captures a subtlety worth stealing outright:

```bash
# This build may run in a staging checkout herdr
# renames afterwards, so the links aim at the runtime root when herdr provides one, and
# every action re-points them at the live root regardless (herdr/pane.sh).
LINK_ROOT="${HERDR_PLUGIN_ROOT:-$ROOT}"
```

`[HITL]` takes the other route — compile at install time with `go build` — and pays for it
with a Go-toolchain prerequisite it documents in the manifest comment `[HITL]`. `[USAGEBAR]`
hedges with a script that can do either `[USAGEBAR]`.

### Config file format and location

Manifest-adjacent config is explicitly *not* the manifest `[REVIEWR]` `README.md`:

```text
~/.config/herdr/plugins/config/persiyanov.reviewr/config.toml
```

> Create it if missing. It is reviewr's file. Settings in herdr's `~/.config/herdr/config.toml`
> never reach it. reviewr re-reads it on every refresh and toggle, so edits apply without a
> relaunch. `[REVIEWR]` `README.md`

Format is flat TOML with one nested table:

```toml
theme = "tokyo-night"
default_scope = "branch"
navigator_position = "right"
toggle_placement = "overlay"
toggle_direction = "down"
auto_open = false
github_host = "github.example.com"
editor = "code -g {file}:{line}"

[keybindings]
comment = ["c", "ㅊ"]
select  = ["v", "ㅍ"]
```
`[REVIEWR]` `README.md`

> A missing file or omitted key uses its default. **An invalid file is rejected whole** — the
> pane shows the error and recovers on the next refresh after you fix it. `[REVIEWR]` `README.md`

And the update-survives-config property is designed for and advertised:

```bash
herdr plugin uninstall persiyanov.reviewr && herdr plugin install persiyanov/herdr-reviewr
```
> **To update**, reinstall. Your config is keyed by plugin id and survives. `[REVIEWR]` `README.md`

`[HITL]` puts the same rule in its manifest header comment `[HITL]`:

> User-editable configuration does NOT live here. It lives in the plugin config
> directory, which you can print with: `herdr-hitl config path`

### Things herdr-cron should copy

1. **One config contract, one parser, every entry point.** reviewr's shell dispatcher does not
   parse TOML — it shells back into the Rust binary and consumes JSON `[REVIEWR]` `herdr/pane.sh`:

   ```bash
   # Validate the whole plugin config before reading workspace state or taking any action. The Rust
   # binary owns TOML parsing and defaults, so every plugin entry point shares exactly one contract.
   config_json=$("$REVIEWR" --resolve-plugin-config 2>&1)
   ```

   For a Go plugin this collapses further: make every manifest entry point invoke the **same Go
   binary** with a different subcommand. No shell dispatcher at all `[INFERENCE]`.

2. **`export PATH=...` at the top of anything Herdr launches.** Minimal PATH is real
   `[REVIEWR]`. A single static Go binary invoked by absolute path is immune, but any helper
   it shells out to (`git`) is not `[INFERENCE]`.

3. **A version-stamped `docs/herdr-api-notes.md`.** reviewr's is titled
   "herdr API notes (verified against herdr 0.7.5) … last sweep 2026-07-31" `[REVIEWR]` and it
   records *which* behaviours were confirmed live, with sample sizes ("verified across 10 live
   plugin panes", "matched on every entry of a 10-agent sample"). This is the single best
   practice in the repo and it is exactly the discipline a scheduler needs, because it is
   coupling to a host whose contract is a moving target.

4. **`min_herdr_version` chosen for a *reason*, and commented.** reviewr pins `0.7.5` and says
   why in the manifest itself (`cwd` on `agent list`) `[REVIEWR]`. Herdr refuses to link when
   the requirement exceeds the running binary `[DOC:socket]`, so this is a hard gate, not
   documentation.

5. **Draft-then-publish releases with checksums and provenance.** Directly portable to Go via
   `taiki-e/upload-rust-binary-action`'s Go equivalents or a hand-rolled matrix `[INFERENCE]`.

6. **Idempotent, converging actions.** reviewr's `toggle`/`open`/`close` re-derive state from a
   live `pane list` + per-pane `process-info` sweep rather than a state file:
   > There is no state file. `[REVIEWR]` `herdr/pane.sh`

   And it distinguishes "the pane is gone" (benign race, converge, exit 0) from "the call
   failed" (refuse loudly). For a scheduler that may be interrupted mid-run, that
   converge-vs-refuse discipline is directly applicable `[INFERENCE]`.

7. **Work without Herdr too.** reviewr runs as a plain terminal app against a repo path, losing
   only the Herdr-dependent features `[REVIEWR]` `README.md`. A herdr-cron that degrades to a
   plain CLI when `HERDR_ENV` is unset is testable in CI and usable outside Herdr `[INFERENCE]`.

### The closest precedent is not reviewr

`[HITL]` is a better structural template for herdr-cron than reviewr is: it is **Go**, it is
**GUI-free**, it has **no `[[panes]]` and no `[[link_handlers]]`**, all its actions are
`contexts = ["global"]`, and — decisively — **it runs a background daemon from a startup hook**
`[HITL]`:

```toml
# One-shot startup hook: spawn the detached daemon and exit immediately.
# `daemon start` returns as soon as the daemon answers a probe, which matches
# Herdr's startup-hook contract (initialize, then exit — not a supervised
# long-running process).
[[startup]]
command = ["bin/herdr-hitl", "daemon", "start"]
```

with `daemon start | status | restart | stop` exposed as global actions, and an
`install-cli` action that symlinks the binary onto `PATH` so agents can call it directly
`[HITL]`. That is very close to the shape herdr-cron needs: a scheduler daemon that must
outlive any single hook invocation, plus a CLI the agent drives.

The contract it is respecting is real:

> Startup hooks are one-shot initialization commands rather than supervised daemons. A hook
> should restore plugin-owned state, call any required Herdr APIs, and exit. `[DOC:plugins]`

> They run again when a new server takes over during live handoff, but not when a client
> attaches, config reloads, or a plugin is linked or enabled. `[DOC:plugins]`

**So the daemon must be idempotent under repeated `start`** — it will be invoked again on every
server start and every live handoff `[INFERENCE]`.

## 9. Headless orchestration, executed rather than inferred `[PROBE]`

Everything in this section was **run** on 2026-09-02 against `herdr 0.8.2` (protocol 20) on
Linux/WSL2, in an isolated named session `hcprobe` that had never hosted a TUI client. Tag
`[PROBE]` means: the exact command is quoted, and the JSON is the response received. The
session was stopped and deleted afterwards (`herdr session stop hcprobe`,
`herdr session delete hcprobe`); the default session was never touched.

This section exists because §5 could establish only that headless orchestration *should*
work. It resolves the four highest-risk unknowns that the rest of this document had to leave
open.

### 9.1 `herdr server` starts a session that never ran the TUI, and it starts empty

`[$ herdr --session hcprobe status]` before starting anything reports
`status: not running` and names the socket it would use —
`/home/huke/.config/herdr/sessions/hcprobe/herdr.sock`. So `--session <name>` resolves to
`~/.config/herdr/sessions/<name>/` and is a pure path computation; no session state has to
exist first `[PROBE]`.

`herdr --session hcprobe server` was launched as a background process with **no client ever
attached, and no TTY** (stdio pipes, `pty: false`). Two seconds later
`[$ herdr --session hcprobe status --json]` returned:

```json
{"server":{"status":"running","running":true,"version":"0.8.2","protocol":20,
  "capabilities":{"live_handoff":true,"detached_server_daemon":true},
  "compatible":true,
  "socket":"/home/huke/.config/herdr/sessions/hcprobe/herdr.sock",
  "session":"hcprobe","restart_needed":false}}
```

`[PROBE]`. The three list commands all returned empty collections `[PROBE]`:

```
$ herdr --session hcprobe workspace list
{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[]}}
$ herdr --session hcprobe pane list
{"id":"cli:pane:list","result":{"panes":[],"type":"pane_list"}}
$ herdr --session hcprobe agent list
{"id":"cli:agent:list","result":{"agents":[],"type":"agent_list"}}
```

**Consequence for herdr-cron:** a scheduler cannot assume a workspace exists. On a
freshly-started headless server there is no `w1` to split; the first scheduled run must
create its own topology. `herdr --session` also gives herdr-cron a clean way to run its jobs
in a session of its own rather than in the human's.

Note the CLI flag surface: `--session` is a **global** flag and must precede the subcommand
(`herdr --session hcprobe workspace list`). Also, these subcommands do not accept `--json` —
`[$ herdr --session hcprobe pane list --json]` returns `unknown option: --json` and
`[$ herdr --session hcprobe workspace list --json]` returns `usage: herdr workspace list`.
JSON is simply what they emit `[PROBE]`.

### 9.2 The documented headless geometry is real: 120×40

`[$ herdr --session hcprobe workspace create --label hcprobe-ws --cwd /tmp --no-focus]`
succeeded with no client attached, returning `workspace_id: w1`, `tab_id: w1:t1`,
`pane_id: w1:p1`, and `"scroll":{"viewport_rows":40}` `[PROBE]`.

`[$ herdr --session hcprobe pane layout --pane w1:p1]` then returned:

```json
{"layout":{"area":{"height":39,"width":94,"x":26,"y":1},"focused_pane_id":"w1:p1",
  "panes":[{"focused":true,"pane_id":"w1:p1","rect":{"height":39,"width":94,"x":26,"y":1}}],
  "splits":[],"tab_id":"w1:t1","workspace_id":"w1","zoomed":false}}
```

`[PROBE]`. `x + width = 26 + 94 = 120` and `y + height = 1 + 39 = 40`: the pane occupies the
documented `headless_cols = 120` / `headless_rows = 40` virtual terminal minus a 26-column
sidebar and a 1-row header. This confirms `[DOC:config]` empirically and gives the exact usable
content rectangle a scheduled TUI or agent gets when nobody is watching. A second tab created
later reported `viewport_rows: 39` `[PROBE]` — one row less than the root pane, worth noting
before hardcoding either number.

### 9.3 `agent start` **does** succeed with no client attached

This was named in the original "Could not verify" list as *the single highest-risk unknown for
herdr-cron*. It is resolved: **it works**, and it takes about four seconds.

```
$ herdr --session hcprobe agent start probe2 --kind claude --pane w1:p2 --timeout 90000
{"id":"cli:agent:start","result":{"agent":{"agent":"claude","agent_status":"idle",
  "cwd":"/home/huke/huketo/jjalcloud","interactive_ready":true,"name":"probe2",
  "pane_id":"w1:p2","terminal_title":"✳ Claude Code","workspace_id":"w1"},
  "argv":["claude"],"type":"agent_started"}}

real	0m3.973s
```

`[PROBE]`. Screen-manifest detection therefore functions against a 120×40 virtual terminal with
no client rendering it. The full loop then completed headlessly `[PROBE]`:

```
$ herdr --session hcprobe agent prompt probe2 "Reply with exactly the single word \
  HEADLESS-OK and nothing else. Do not use any tools." --wait --timeout 120000
{"result":{"agent":{"agent_status":"done","interactive_ready":true,"name":"probe2",
  "terminal_title":"✳ Headless OK","state_change_seq":6},"type":"agent_prompted"}}

real	0m3.241s
```

and `[$ herdr --session hcprobe agent read probe2 --source recent-unwrapped --lines 40]`
returned the transcript including the model's answer:

```
❯ Reply with exactly the single word HEADLESS-OK and nothing else. Do not use any tools.

● HEADLESS-OK

✻ Sautéed for 1s · done 오전 10:46
```

`[PROBE]`. Two secondary confirmations fall out of the same run:

- **`done` is what an unattended run settles to**, not `idle` — matching §5's account of `done`
  as idle-after-unseen-work.
- **A CLI read does not clear it.** `[$ herdr --session hcprobe agent get probe2]` issued
  *after* the read still reported `{"agent_status":"done","interactive_ready":true,
  "state_change_seq":6}` `[PROBE]`. herdr-cron can archive a run's output without destroying
  the human's unseen-work signal.

### 9.4 The real headless failure mode is a startup dialog, not detection

The first attempt failed, and its failure is more useful than its success. The pane had
`cwd = /tmp`, which Claude Code does not trust:

```
$ herdr --session hcprobe agent start probe --kind claude --pane w1:p1 --timeout 60000
{"error":{"code":"agent_not_ready",
  "message":"agent probe is blocked during startup and is not ready for prompts"},
  "id":"cli:agent:start"}

real	0m4.070s
```

`[PROBE]`. It returned in 4s rather than burning the 60s timeout, and the name stayed usable
exactly as §5 describes. `[$ herdr --session hcprobe agent get probe]` reported
`"agent_status":"blocked"` with `"launch_pending":true`, and
`[$ herdr --session hcprobe agent read probe --source detection --lines 60]` showed the cause
verbatim `[PROBE]`:

```
 Quick safety check: Is this a project you created or one you trust? …

 ❯ No, exit
   Yes, I trust this folder

 Enter to confirm · Esc to cancel
```

`[$ herdr --session hcprobe agent send-keys probe esc]` returned `{"type":"ok"}` and the pane
fell back to its shell prompt `[PROBE]`.

**Consequence for herdr-cron, and it is a product requirement, not a detail:** an agent job
whose `cwd` has never been approved *blocks forever with no human present*. Herdr classifies
this correctly and fast, so the scheduler can detect it — but it must then treat
`agent_not_ready` / `agent_status: blocked` as a first-class terminal outcome ("needs human
approval"), report it, and not retry it on a schedule. The mitigation used for this probe was
to pick a cwd already listed with `hasTrustDialogAccepted: true` in `~/.claude.json`
`[$ jq -r '.projects | to_entries[] | select(.value.hasTrustDialogAccepted==true) | .key' ~/.claude.json]`
`[PROBE]`. A pre-flight trust check is cheap; discovering this at 03:00 is not.

Note also that pane `cwd` is fixed at pane creation — `agent start` has no `--cwd`
`[$ herdr --session hcprobe agent start --help]` `[PROBE]`. The per-job working directory is
chosen when herdr-cron creates the pane/tab (`tab create --workspace w1 --cwd <path>` was used
here and reported the requested cwd back `[PROBE]`).

The same help output enumerates the installed agent kinds `[PROBE]`:

```
[possible values: pi, claude, codex, gemini, cursor, devin, agy, cline, omp, mastracode,
 opencode, copilot, kimi, kiro, droid, amp, grok, hermes, kilo, qodercli, qwen, maki]
```

### 9.5 Shell jobs work headlessly; notifications do not

A non-agent job — the other job kind herdr-cron must support — ran end to end with no client
`[PROBE]`:

```
$ herdr --session hcprobe pane run w1:p1 "echo SHELLJOB-\$((6*7))"
$ herdr --session hcprobe pane wait-output w1:p1 --match "SHELLJOB-42" --timeout 10000
{"result":{"matched_line":"SHELLJOB-42","pane_id":"w1:p1", …}}
```

And the documented notification limitation was reproduced rather than assumed `[PROBE]`:

```
$ herdr --session hcprobe notification show "headless probe" --body "from hcprobe"
{"id":"cli:notification:show",
 "result":{"reason":"no_foreground_client","shown":false,"type":"notification_show"}}
```

`shown: false`. A scheduler running against a headless server gets **no** notification
delivery. Result reporting must go somewhere else — a log, a file, a git commit, or an
out-of-band transport such as the `herdr-hitl` plugin already installed here. (`notification
show` takes the title as a positional argument and the body as `--body`; `--message` is not a
flag `[PROBE]`.)

### 9.6 What this section does not prove

- Linux/WSL2 only. Nothing here was run on macOS or Windows.
- One agent kind (`claude`). Detection latency and startup dialogs will differ per kind.
- The server was started in the foreground of a supervised background process, not via
  systemd/launchd/Task Scheduler, and not with `detached: true`. What bare `herdr server`
  does to a controlling terminal is still unverified.
- Nothing was tested across a server restart, a live handoff, or machine suspend.

---

## Implications for herdr-cron `[INFERENCE]`

*This whole section is inference. It lays out options; it does not settle them.*

### The three shapes

**Option A — Herdr plugin only.**

Evidence for: one-command install and marketplace listing `[DOC:plugins]`; Herdr-managed
config/state dirs it does not have to invent; `[[startup]]` gives it a server-lifecycle hook
that fires on boot *and* live handoff `[DOC:plugins]`; `[[events]]` gives it worktree and
agent-status triggers for free; `[[actions]]` with `contexts = ["global"]` puts
`herdr-cron list/run-now/pause` in the command surface and on a keybinding `[SCHEMA]`,
`[HITL]`; `[[panes]]` hosts the Bubble Tea TUI as a real Herdr pane with `placement`
`overlay | popup | split | tab | zoomed` `[DOC:plugins]` — a mouse-driven TUI in an overlay is
exactly the shape `[[panes]]` was designed for.

Consequences: it only exists inside Herdr, so it cannot schedule anything for a user who has
not installed Herdr; `HERDR_PLUGIN_ROOT` is replaced wholesale on every update `[DOC:plugins]`
so all durable state must be under `HERDR_PLUGIN_STATE_DIR`; there is no `plugin update` so
upgrades are uninstall+install `[DOC:plugins]`; a `[[startup]]` hook must exit, so the actual
gocron loop still has to be a self-managed daemon `[DOC:plugins]` — meaning even "plugin only"
implies a daemon, exactly as `[HITL]` does it.

**Option B — standalone CLI that shells out to `herdr`.**

Evidence for: `HERDR_BIN_PATH` is the *documented preferred* callback mechanism, explicitly to
avoid the Unix-socket-vs-named-pipe split `[DOC:plugins]` — which matters because herdr-cron
targets Windows; every plugin read here shells out rather than speaking the socket
`[REVIEWR]`, `[USAGEBAR]`, `[SHEEP]`; "the entire Herdr CLI is the plugin API" means a
standalone CLI has *the same* API surface a plugin does `[DOC:plugins]`; the scheduler can then
also run under systemd/launchd/Task Scheduler with no Herdr involvement, and degrade to
non-Herdr jobs when Herdr is absent.

Consequences: the user installs it themselves (brew/scoop/go install/release binary), so no
marketplace discovery; herdr-cron owns config/state path resolution on three OSes instead of
reading two env vars; no `[[events]]` hooks, so agent-status reactions need a persistent
`events.subscribe` connection or `agent wait` per job; no keybinding surface; the TUI is
launched by the user rather than being a Herdr pane.

**Option C — both: one Go binary, two front doors.**

Evidence for: this is precisely what `[HITL]` ships. The manifest's `install-cli` action
symlinks the plugin binary onto `PATH` "so agents can call it directly", and the plugin
config dir doubles as the standalone config dir `[HITL]`. reviewr does the softer version —
plugin-first, but "Without herdr, reviewr runs as a plain terminal app" `[REVIEWR]` `README.md`
— and its `install.sh` symlinks into `~/.local/bin` when that directory exists `[REVIEWR]`.
The env contract makes the dual mode cheap: `HERDR_ENV=1` tells the binary which mode it is in
`[DOC:plugins]`, `[SKILL]`, and reviewr's fallback pattern (`HERDR_PLUGIN_CONFIG_DIR`, else ask
`herdr plugin config-dir`, else a default path) is already proven `[REVIEWR]`
`docs/herdr-api-notes.md`.

Consequences: two installation stories to document and test; two config-resolution paths (though
they can converge on one directory); the manifest becomes a thin wrapper over subcommands the
CLI already has, which is cheap; and the `[[startup]]` hook and a system service unit are two
ways to start the *same* daemon, which must therefore be idempotent under both.

### The facts that most constrain the choice

- **A daemon is unavoidable in every option.** gocron needs a live process; `[[startup]]` hooks
  must exit `[DOC:plugins]`. So the "plugin vs. standalone" question is really "how does the
  daemon get started", not "is there a daemon".
- **Headless orchestration works** `[DOC:config]`, `[DOC:persist]`, `[DOC:cli]`, `[LIVE]`. The
  daemon does not need a UI to run jobs. But *something* must have started the Herdr server,
  and herdr-cron cannot assume it is up — every run must handle "no server" as a first-class
  outcome.
- **Notifications are best-effort and tiny** (80/240 chars, `no_foreground_client`)
  `[DOC:socket]`. Reporting cannot rest on them in either option.
- **Windows pushes toward `HERDR_BIN_PATH`** over raw sockets `[DOC:plugins]`, which is
  option-neutral — it argues for shelling out regardless of packaging.
- **The plugin surface is genuinely richer**: keybindings, panes with five placements, event
  hooks, marketplace `[DOC:plugins]`. A standalone CLI gets none of it.
- **`min_herdr_version` is a hard gate** `[DOC:socket]`. A plugin that wants `contexts =
  ["global"]` should look at what `[HITL]` pinned (`0.8.0`) `[HITL]` versus what reviewr
  pinned for `agent list.cwd` (`0.7.5`) `[REVIEWR]` and choose deliberately.

I am not settling this. The honest summary is that Option C costs one extra install path and
buys both surfaces from one Go binary, and that both real precedents examined here converged on
it independently.

---

## Could not verify

- **`herdr server` foreground/daemonize semantics.** `herdr server --help` documents its
  subcommands but not what bare `herdr server` does to the terminal, whether it forks, its exit
  behaviour, or its logging destination `[BIN]`. `[DOC:cli]` says only "runs the headless server
  explicitly. Use it for supervised or service-style setups."
  **Partially resolved in §9**: `herdr --session hcprobe server` was run under a supervisor with
  piped stdio and no TTY, and it served the socket until `herdr session stop` ended it with exit
  code 0 `[PROBE]`. Whether it daemonises when given a controlling terminal, and where it logs,
  is still unverified.
- ~~**Whether `herdr server` can start a server on a machine that has never run the TUI**, and
  what session it creates.~~ **Resolved in §9.1**: it starts, and it creates *nothing* —
  `workspace list`, `pane list`, and `agent list` all return empty collections `[PROBE]`.
- ~~**Whether an unattended `agent start` succeeds with no client attached.**~~ **Resolved in
  §9.3: yes**, in ~4 s, returning `agent_status: "idle"` and `interactive_ready: true`, followed
  by a successful `agent prompt --wait` and `agent read` `[PROBE]`. Screen-manifest detection
  works against the 120×40 headless terminal. The real unattended failure mode turned out to be
  different and is documented in §9.4: an agent whose `cwd` has never been trusted returns
  `agent_not_ready` and sits on an approval dialog no one can answer.
- **Whether a manifest `[[events]] on = "pane.agent_status_changed"` hook fires for every pane
  or only the focused one.** The subscription form requires `pane_id` `[SCHEMA]`; the manifest
  form has nowhere to put one; `[USAGEBAR]` uses it successfully and its own comment says "event
  hooks only see the focused pane" `[USAGEBAR]`, which is a plugin author's claim, not a Herdr
  document. Same open question for `pane.output_matched` and `pane.scroll_changed`, whose
  subscription forms have required params a manifest cannot supply.
- **The authoritative list of legal `on =` values.** I derived it from `SubscriptionEventKind` +
  the dotted `Subscription` type constants `[SCHEMA]` and corroborated three names against real
  manifests and a live log record `[REVIEWR]`, `[USAGEBAR]`, `[LIVE]`. No source states "these
  are the plugin hook names" as a list. `workspace.metadata_updated` is documented as explicitly
  *not* invoking hooks `[DOC:socket]`, proving the hook set is a strict subset — but the exact
  subset is not published.
- **Exit-code semantics for action and event-hook commands.** Only build-command failure has
  documented consequences (aborts install) `[DOC:plugins]`. Whether a non-zero action exit does
  anything beyond `status: "failed"` in the log is unstated.
- **Plugin directory paths on macOS and Windows.** Only Linux was observed `[LIVE]`. `[DOC:config]`
  gives the *config file* per OS (`%APPDATA%\herdr\config.toml`), and `[DOC:plugins]` never gives
  literal plugin/state paths for any OS. `herdr plugin config-dir <id>` is the supported way to
  ask, and `[DOC:cli]` recommends exactly that. **Do not hardcode.**
- **`herdr integration status` output shape.** The command exists `[BIN]` but returned
  `Error: failed to parse: value expected` when piped through `jq` on this machine `[LIVE]`,
  suggesting non-JSON output. Not pursued — integrations are adjacent to, not part of, the
  scheduler's path.
- **Enumerated error codes.** `ErrorBody` is `{code: string, message: string}` with no enum
  `[SCHEMA]`. The code list in §5 is assembled from prose across four sources and is certainly
  incomplete.
- **`herdr --skill` and Agent Skill packaging.** The binary prints a skill file `[BIN]`, `[SKILL]`
  and there is a docs page "Agent skill file" named by `llms.txt`. That page was not read — it is
  the sibling Agent-Skill document's territory, deliberately left there.
- **`herdr-hitl`, `herdr-agent-usage`, and `herdr-sheep` source code.** Only their installed
  manifests were read `[HITL]`, `[USAGEBAR]`, `[SHEEP]`, not their repositories. The claim that
  `herdr-hitl` is Go rests on its `[[build]]` command invoking `go build ./cmd/herdr-hitl`, which
  is conclusive for the language but says nothing about its internal structure.
- **Live behaviour of `plugin pane open` for a Bubble Tea TUI** — placement, resize, mouse
  reporting inside a Herdr plugin pane. Nothing was launched. `[DOC:plugins]` documents
  `placement`, `width`, `height`, and popup clamping, and notes a popup "receives all terminal
  input, including Escape" and "has no pane ID". Whether Herdr's own mouse handling conflicts
  with a mouse-driven TUI in a plugin pane is unverified and matters for the TUI design.
- **`herdr.dev/docs/marketplace/` and `herdr.dev/docs/integrations/`** were not fetched; the
  marketplace mechanics quoted here come from `[DOC:plugins]`, which summarises them.
