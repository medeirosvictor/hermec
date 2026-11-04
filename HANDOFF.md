# Hermec: session handoff

Brief for the next session. Roadmap: [PLAN.md](PLAN.md). User docs:
[README](README.md), [docs index](docs/README.md),
[getting started](docs/getting-started.md),
[dedicated guide](docs/dedicated-hosting.md), [AWS EC2 from zero](docs/aws-ec2-from-zero.md),
[release signing](docs/release-signing.md), [protocol](docs/protocol.md).

## Current state

- v0.8.0 is released from `main` and signed (`SHA256SUMS.txt.sig`, bound to the
  tag). Every client and host in a group must run the same build; the wire
  rejects any other with `version_mismatch`.
- Updater: Windows/Linux download in the background and offer "Restart to
  update"; a newer server's `version_mismatch` offers "update now". macOS is
  manual (`scripts/build-mac.sh`). Dedicated servers update by hand with the
  installed `/usr/local/lib/hermec/update.sh`, which verifies the signed sums
  by default.
- Dedicated hosting is built (bootstrap, bundles, `hermec-verify`, desktop Add
  and founder authorize/cutover flows). Its remote acceptance has not run, so
  do not claim remote hosting is available.
- Done and unit-tested, not yet exercised on real devices or a real release:
  per-person volume, mic gain, mid-call device switching, auto-rejoin, updater.
- `internal/fsutil`, `internal/ratelimit`, `internal/knownhosts`,
  `server/history`, `server/dedicated`, `server/serverctl`, `server/host` and
  `ui/crt` are extracted; `hermec-server` `main()` and `conn.authenticate` are
  split into steps.

## Next steps, in order

1. Real-call hand test of v0.8.0 with a friend (checklist A).
2. Release v0.8.1 to prove one full self-update cycle on Windows and Linux
   (checklist B).
3. Task 6: remote acceptance on a disposable EC2 instance (checklist C),
   following [docs/aws-ec2-from-zero.md](docs/aws-ec2-from-zero.md). Run the
   rows with Victor; do not provision cloud resources.
4. Second wave: push-to-talk, then echo cancellation (moderation UI and invite
   links are in; invite links still need checklist A below).
5. Design the `ui.game` split with Victor, then do it along with the SFU and
   client splits (PLAN 1b). Follow-ups: `server` exports about 19 methods for
   `server/host` (move the yield sequence behind one call); `cmd/hermec-server`
   `restoreTransition` and `CheckDedicatedState` duplicate a transition-state
   switch (move it into `server/serverctl`).
6. Private networking: decide between real Tailscale/Headscale evidence and
   shipping 1.0 without it; then hosting-method migration if it ships.
7. Call quality, regular-use gate, docs milestone (README split done;
   architecture and code-map guides open), then v1.0.0.

## Open checklists (real devices)

**A. Two PCs or a friend, v0.8.0**
- Right-click a person: 0% and 200% change what you hear live, survive a
  restart, no crackle at 200%.
- Mic gain 50-200% changes the meter and what the peer hears, live.
- Change input and output devices mid-call: audio resumes on the new device; a
  bad device keeps the old one with a status; a Bluetooth headset switch works.
- Kill the server link (or Wi-Fi) mid-call and reconnect: voice rejoins muted,
  camera and share stay off; a banned, left or disconnected session does not
  rejoin; failover takeover still carries the call.
- With a node-shared Tailscale friend host: standby takeover, empty-voice
  handback, roster hosting controls, reachability selection, camera and screen
  transfer, Tailscale grants.
- Moderation with two clients: as founder or admin right-click a member, Ban…
  and confirm: they drop at once, cannot rejoin, and the status line says
  "banned X"; server tile > Banned members… > Unban lets them back. Make admin
  and Remove admin change what the other client's menu shows. A plain user sees
  none of these items.
- Invite links: right-click the hosted tile > Copy invite link; on a second PC
  click + and paste it into the invite link line (Ctrl+V): it joins pinned. A
  garbage link shows the friendly message. For a dedicated server, paste a wss
  link with and without `&pw=`: the Dedicated form is prefilled and nothing
  connects until Validate / Connect. Confirm `servers.toml` never holds the
  password.
- Visual, input, device-permission, firewall, discovery, voice, screen-share and
  camera checks on real desktops; headless tests do not replace them.

**B. Self-update, vN to vN+1 (v0.8.1)**
- Install vN on Windows and Linux, publish vN+1: background download, "Restart
  to update", relaunch with the same args, `.old` kept until the new build has
  started and then removed.
- Read-only install folder falls back to the Releases link.
- `version_mismatch` against a vN+1 server offers "update now".
- Installed `update.sh` with `HERMEC_SUMS`, `HERMEC_ARCHIVE`, `HERMEC_TAG`
  on the VPS, including the `systemd-run` validation branch (no automated
  coverage) and rollback.
- Not covered by tests: the real GitHub redirect host
  (`release-assets.githubusercontent.com` or `objects.githubusercontent.com`),
  Windows Defender or SmartScreen reacting to a renamed, relaunched unsigned exe.

**C. Task 6, disposable VPS and AWS**
- Fresh install; DNS and trusted TLS; join; two-way text and audio; camera and
  selected screen; restart persistence; backup and restore; update and
  rollback; blocked-UDP diagnosis; host failover and recovery.
- AWS security group permits TCP and UDP 7697; a real remote friend connects;
  no project infrastructure is used.
- `hermec-verify` on two independently administered remote machines (see
  [the verifier](docs/dedicated-hosting.md#remote-reachability-verifier)); keep
  only its safe reports.
- Only after all rows pass may docs describe availability.

**D. Private networking** (needed only if it ships): real Tailscale and
Headscale direct and relay media and standby evidence
([research](docs/research/embedded-network-proof.md)).

## Constraints

- No backward compatibility before 1.0.0: no code, config key, protocol path or
  test supports a different build.
- Anyone can self-host; no project servers, in-app cloud provisioning, cloud
  credentials, or claim that public hosting is safe without TLS and admission.
- The host is trusted; Ed25519 keys, not names, are identity. Never transfer
  founder or member private keys to a VPS.
- Do not run GUI automation against Victor's real `%APPDATA%\hermec`; there is
  no `-config-dir`. Use isolated configuration.
- Never commit untracked `primos.server.toml`, `hermec2.key` or `.claude/`;
  leave `.claude/worktrees/agent-*` alone. Releases are tagged from `main`.
- Docs describe the app as it is; finished plans are deleted and open items
  move here.
- Testing: run only the tests for code that changed. Known flakes:
  `TestYieldAbortsWhenHistoryFails` (`server/host_fix_test.go`, fixed sleep;
  replace it with an explicit wait for the probe hook) and ui `TestHostedByLine`.
- Deferred: update provenance hardening beyond the signed sums.

## Risks and gotchas

- Failover merge, call-transfer and warm-host races and cleanup gaps remain;
  read tests and logs before changing host state. Merged chat is not pushed
  live to winner clients, and some takeover or redial failures need a manual
  rejoin.
- Preserve host proof before authentication
  ([protocol §3.1](docs/protocol.md#31-handshake)). Chat history is plain
  unsigned JSONL on the host.
- No echo cancellation: use headphones. Noise suppression is not in the app.
- Moderation UI (ban, unban, make or remove admin) is built; kick is only a ban.
  macOS/Linux camera and share publishing, window capture, shared-screen audio
  and simulcast are not built.
- Two clients using one key silence each other; pass `-key` with another file
  for a second client. `scripts/build-mac.sh` leaves a detached HEAD; return
  with `git checkout main`.

## Commands

```sh
go run ./cmd/hermec -local
go vet ./...
go test ./server/...     # only the packages you changed
go test -race ./...      # needs cgo
```

This machine needs `C:\msys64\ucrt64\bin` on `PATH` and `CGO_ENABLED=1` for
cgo and race work. A stable executable avoids repeated Windows Firewall
prompts: `go build -o bin\hermec.exe ./cmd/hermec`.
