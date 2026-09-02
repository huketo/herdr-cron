# 0002 — Files only: no socket between the CLI and the daemon

## Status

Accepted — 2026-09-02. Decision **D6** in [`docs/spec/README.md`](../spec/README.md); specified in [`docs/spec/04-storage.md`](../spec/04-storage.md) §3.1, §7 and §8.

## Context

Three commands need to reach a running daemon: `herdr-cron job run`, `herdr-cron job cancel`, and `herdr-cron reload`. Two more questions need answering about it: is it alive, and what is it? Everything else — `job list`, `job get`, `run list`, `run get`, `run logs`, `validate`, `run-once` — is a file read or a file write and needs no daemon at all ([`docs/spec/05-cli.md`](../spec/05-cli.md) §4).

The default answer for a local CLI-to-daemon channel is a unix socket, and it is a good answer on exactly one of our three platforms. Windows has no unix sockets in the sense a Go program can rely on across the versions we support; the portable equivalent is a named pipe, with different creation semantics, different permissions, different error codes and a different listener type. Taking a socket means writing and testing two transports, on a product whose entire claim is that one codebase runs on Linux, macOS and Windows.

This is not a hypothetical cost. `pueue` — a local task queue with the same shape of problem — resolved the split by moving to TCP with TLS, which is a listening port on a developer's laptop and a certificate to manage ([`docs/research/2026-09-02-prior-art-and-domain-model.md`](../research/2026-09-02-prior-art-and-domain-model.md) Q7(a)).

The host answers the same question the same way. Herdr's own plugin documentation pushes plugins toward shelling out to its CLI rather than speaking its socket protocol, precisely because of the unix-socket-versus-named-pipe split ([`docs/research/2026-09-02-herdr-plugin-integration.md`](../research/2026-09-02-herdr-plugin-integration.md) §2) — and herdr-cron obeys that for its outbound calls (`internal/herdr` is an argv exec of `herdr`, never a socket client). Importing the split *inbound*, for three commands, would be the same mistake we are already avoiding outbound.

A second constraint comes from ADR-0001. Under the `os-scheduler` driver there is no daemon at all, and several `run-once` processes may be alive at once. Whatever channel exists must be optional: a design where liveness or coordination depends on a socket owner cannot express "nobody is resident and everything still works".

## Decision

**The only channel is the filesystem.** No sockets, no named pipes, no TCP, not even locally. Four mechanisms, all files:

**Requests are trigger files.** `job run`, `job cancel` and `reload` write one JSON object to `<state>/triggers/<ulid>.json` — staged in `tmp/` and renamed in, so a half-written request is never readable. The daemon watches that directory, reads the file, **renames it to `<ulid>.claimed`**, acts, then deletes it. *The rename is the claim*: it is atomic on all three platforms, so double-processing is impossible without any lock. A `daemon.TriggerResult` written to `<ulid>.result` carries the resulting `runId` back.

**Waiting is polling.** `job run --wait` polls the job's `runs/<jobId>.jsonl` at 100 ms for a terminal record matching the `runId` the daemon reported. Triggers older than five minutes are garbage-collected by the next daemon start, and a client whose trigger nobody claims within a 3-second grace period reports `daemon_unreachable` instead of hanging.

**Configuration changes arrive by watch.** `fsnotify` on `<config>` with a 200 ms debounce (editors write-then-rename, producing two events, and rename replaces the inode — so the directory is watched, not the file) plus a 5-second stat-poll fallback for the filesystems where notification does not work.

**Liveness is a heartbeat plus a lock, and both are required.** `daemon.json` is rewritten every 15 s by atomic rename; `daemon.lock` holds an advisory lock (`flock` / `LockFileEx`) for the daemon's lifetime. A fresh heartbeat alone proves nothing — a `kill -9` leaves one that stays fresh for a minute, during which `herdr-cron status` lied and `daemon --detach` was a silent no-op. The kernel releases the lock when the process dies, so the conjunction cannot be wrong in that direction. This correction came out of running the thing, and is recorded in [`docs/spec/README.md`](../spec/README.md).

**`job pause` and `job resume` deliberately do not use this channel.** They write `overrides.json` under its own lock, which is what makes them work with no daemon running and identical between the CLI and the TUI.

## Consequences

- One implementation for three operating systems. No named-pipe branch, no TCP, no port, no TLS, no bind address, no authentication story beyond filesystem permissions on the user's own state directory.
- The `os-scheduler` driver needs no special case: there is nothing to connect to, and nothing tries.
- The TUI is a client of the same files by the same rules — an fsnotify watch plus a tick — so it can be killed at any moment without affecting a run, and it takes no locks to read.
- Every request is inspectable and replayable. A stuck `job run` leaves a JSON file on disk with a timestamp in it, which is a better debugging surface than a closed socket.
- Cost: **latency.** `job run --wait` notices completion roughly 100–300 ms after it happens, versus a socket's immediate return. Measured against writing and testing three transports, that is cheap — and it is the only cost this decision has that a user can observe.
- Cost: no push cancellation. `job cancel` is a request the daemon picks up on its next poll, not an interrupt.
- Cost: a client cannot distinguish "no daemon" from "a daemon too busy to poll" except by timeout, which is why `daemon_unreachable` carries a hint naming both `service install` and `run-once` rather than asserting the daemon is down.
- Cost: correctness rests on rename being atomic and on advisory locks behaving. Both hold on local filesystems on all three platforms; neither is guaranteed over NFS, so a state root on a network filesystem is unsupported.

## Alternatives considered

**A unix socket, with a named pipe on Windows.** The default choice, and rejected: two transports, two test matrices, and two classes of platform-specific bug, bought for three commands whose latency requirement is "before the human notices". `herdr-hitl` takes a socket and is right to — it needs client EOF to cancel a pending question, a semantic no filesystem gives you. herdr-cron has no such requirement: a run's lifetime is not a client's lifetime, and a cancelled agent must *not* cancel a scheduled run.

**TCP on localhost.** Rejected: a listening port on a laptop is an attack surface and a firewall prompt, and doing it properly means TLS and a token — which is where pueue ended up, and it is more machinery than the problem has (`prior-art` Q7(a)).

**gRPC or an HTTP API.** Rejected, and refused as a non-goal in [`docs/spec/01-overview.md`](../spec/01-overview.md) §3.2. It is a server: a port, an auth story, a schema, a second surface to keep in parity with the CLI. dagu ships one, and its own README names the price of becoming that: *"You wanted to schedule some jobs. Now you operate a second system."*

**A SQLite or bbolt table as the queue.** Rejected with evidence: no scheduler in the surveyed corpus chose an embedded database for this, `modernc.org/sqlite` costs roughly 7 MB of binary, and bbolt takes a process-wide exclusive `flock` with a default infinite timeout — which would have *forced* an IPC design on us to serialise access ([`docs/research/2026-09-02-agent-skill-and-cli-ux.md`](../research/2026-09-02-agent-skill-and-cli-ux.md) B6). The cure would have created the disease.

**Signals to the daemon pid.** Rejected: no payload, so `job run <id>` cannot say which job; and Windows has no equivalent worth the name.

**Polling `jobs.yaml` only, with no trigger files at all.** Rejected: it can express "reload" but not "run this now" or "cancel this", and encoding a one-shot request as an edit to the user's authored, git-committable YAML is exactly the kind of file-mutation D2 exists to prevent.
