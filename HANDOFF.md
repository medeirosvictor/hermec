# Hermec: session handoff

Compact brief for a fresh development session. User docs: [README.md](README.md),
[protocol](docs/protocol.md), and [friend quickstart](docs/quickstart-friends.md).
The roadmap is [PLAN.md](PLAN.md).

## Current work

- Branch: `feature/dedicated-hosting` in `.worktrees/dedicated-hosting`.
- Dedicated-hosting Tasks 1–2 are `ca6ed69`; Task 3 is `020492f`. Tasks 1–3
  are complete locally but unmerged and unpushed; this working tree has
  uncommitted documentation changes.
- There is no disposable VPS/AWS test. Remote hosting is **not shippable**. Do
  not provision cloud resources.
- Next milestone: **Task 4, Desktop Join/Create Dedicated**. Then do Task 5
  (host authorization/preparation) and Task 6 (remote acceptance/release).
- Private networking cannot ship until real Tailscale/Headscale direct and relay
  evidence exists. After dedicated and private hosting work, implement guarded
  hosting-method migration. Only then begin desktop distribution/updater work;
  Friends adjustments and noise suppression are earlier backlog items, not
  distribution prerequisites.

Active plans and design:

- [Dedicated hosting plan](docs/superpowers/plans/2026-10-28-dedicated-hosting.md)
- [Private-network lifecycle plan](docs/superpowers/plans/2026-10-28-private-network-lifecycle.md)
- [Hosting-method migration plan](docs/superpowers/plans/2026-10-28-hosting-method-migration.md)
- [Hosting-readiness design](docs/superpowers/specs/2026-10-26-hosting-readiness-design.md)
- [Noise-suppression trial](docs/superpowers/plans/2026-10-27-noise-suppression-trial.md)
  is a draft only; no denoiser is in the app.

### Task 4 requirements

Add a Dedicated option to Add a Server. Support an operator-prepared import or
address plus server ID, password entry with proof-key persistence, and no
plaintext password in logs or saved data after successful join. Validate WSS,
pin, admission, and media before saving a tile; do not leave phantom drafts.
Provide shareable, versioned connection details.

### Deferred desktop distribution and updater

Keep this deferred until dedicated hosting, embedded private networking, and
hosting-method migration—including their Create/Join flows—work. The future
Settings > Releases page must show the running version clearly, list what each
version changed, and link each entry only to its trusted/allowlisted official
GitHub Release URL. It is not implemented. Current desktop behavior is an
update banner that opens GitHub in a browser for manual replacement. The
separate dedicated-server `update.sh` requires a manually downloaded candidate.

## Working constraints

- Before v1, do not build mixed-version call/media bridging. Keep negotiation
  safe, but a call expects clients and host on the same build.
- Anyone can self-host; there are no project-operated servers. Public dedicated
  hosting requires TLS and admission. Do not add in-app cloud provisioning or
  cloud credentials.
- The host is trusted. Identity is an Ed25519 key file; names are not identity.
- Do not run the GUI from an agent against Victor's real `%APPDATA%\hermec`.
  There is no `-config-dir` flag; use a separate configuration solution before
  automated GUI work.
- Untracked `primos.server.toml`, `hermec2.key`, and `.claude/` are never
  committed. Leave old `.claude/worktrees/agent-*` alone.
- The controller tags releases on `main` only. Docs should describe the current
  app; remove resolved material rather than leaving historical annotations.

## Run and build

- Run locally: `go run ./cmd/hermec -local`.
- PowerShell live reload: `$env:HERMEC_DEV=1; go run ./cmd/hermec -local`.
- This machine needs `C:\msys64\ucrt64\bin` on `PATH` and `CGO_ENABLED=1` for
  cgo/race work. The server is pure Go and cross-compiles for Pi.
- A stable executable avoids repeated Windows Firewall prompts:
  `go build -o bin\hermec.exe ./cmd/hermec`.

## Open real-device checklists

### Dedicated remote acceptance

- [ ] Run the disposable VPS/AWS row in the dedicated-hosting plan, including
  trusted WSS, pinning, password admission, and reciprocal Opus media.
- [ ] Do not call remote hosting available until that row passes.

### Host failover

Needs a node-shared Tailscale friend running HERMEC.

- [ ] In a call, make the friend the standby, quit the founder, and verify the
  call continues, host/chat notices appear, and camera/screen resume.
- [ ] Restart the founder and verify handback only after voice is empty for two
  minutes; nobody moves during a call.
- [ ] Check Allow hosting, Stop hosting, and Move up in the roster menu and the
  resulting host order.
- [ ] Toggle the friend's hosting choice off/on, confirm the reach notice, and
  confirm it persists after restart.
- [ ] Disable node sharing and confirm no standby is selected when most clients
  cannot reach it.

### Screen share and simultaneous media

- [ ] Share and watch screens with the Brazil friend over Tailscale; test small
  text at 720p and 1080p, switching sharers, logs export, and the Tailscale
  grant.
- [ ] On disposable config with two endpoints, check Windows camera permission
  and that its visible indicator turns off after camera stop, call leave, and
  app exit; selected-device persistence without automatic capture; two cameras
  plus one screen; unplug handling that releases capture while audio/screen
  continue; independent camera/screen/mute controls; and teardown on leave.
- [ ] During that call, fail over between active and standby hosts and verify
  voice, cameras, screen, and selected screen resume.

### Desktop interaction

- [ ] Check stream fullscreen exit (Esc, F11, strip), self-preview, changing a
  shared screen, opening call view by selecting the current voice channel, and
  all voice-connected panel controls.
- [ ] With several servers, check calls persist while switching servers,
  Disconnect ends the call, unread indicators, rail DPI at 100/150%, and
  reconnect behavior.
- [ ] Check server rename/Leave/member-list updates. At a narrow window, the
  member list hides, its saved toggle state survives restart at 150% DPI, and
  clicking the dim toggle does not unintentionally change that saved state.
- [ ] Check Add a Server: create, Tailscale join, restart persistence,
  port-in-use error, bad-address retry, discovered-list keyboard selection,
  stop/start hosting, and leaving removes `hosted.toml`.
- [ ] Check Windows title-bar drag/snap/resize/buttons/DPI and theme/CRT visual
  controls.

### Voice, discovery, and audio

- [ ] The audio gate closes after per-person volume ships and works in a real
  call. Headphones remain mandatory without echo cancellation.
- [ ] With two keys on one machine, verify voice, mute, speaking glow,
  leave/rejoin, and removal after a client dies.
- [ ] Verify local, LAN, and Tailscale discovery; refresh, disappearance,
  one-action join, no probe stutter, and `discoverable = false`.

## Known open items

- Friend adjustments: per-person volume and mic gain, live names, mid-call
  device switching, bandwidth metrics, and RTT outlier handling.
- Noise suppression remains experimental and default-off only after a measured
  probe. Echo cancellation is separate.
- The founder menu has hosting controls, but no UI for ban/grant despite server
  support.
- Failover/merge risks: merged chat is not pushed live to winner clients; a
  failed winner append is not reported to the loser; the winner does not
  recheck active state before link-chain append; and concurrent yield has a
  small dual-host window.
- Call-transfer risks: a takeover concurrent with winning redial is only
  code-checked; `carryCall` ends an in-flight voice operation; its goroutine can
  outlive app close for up to 8 seconds; a failed post-detach `joinCall` drops
  to manual rejoin; and a hung old audio read silences new-call outgoing audio.
- Warm/friend-host risks: no cap exists for warm data channels or pings; a
  promoted non-voice connection retains its warm PeerConnection; warm
  offer/demotion can race; startup can probe its own advertised URL; stop during
  start can move the port; and `hostflags` overwrites configured `advertise`.
- Password/config compatibility: no `hermec-server` tool computes
  `password_key`; joined servers have no password field; pre-v0.6 plain-password
  clients impose 32 MiB scrypt work; and pre-fix v0.5 `hosted.toml` limit keys
  are rejected by v0.4.
- Other failover gaps: rollback uses a generic invalid-chain message; a
  `ChainAppend` without echoed `ref` waits for context; and Move up can leave a
  brief priority tie if its second append fails.
- Test gaps: heartbeat tests use real time and `server.SetHeartbeatForTest` is
  exported non-test code; warm-slot cleanup on plain disconnect, 60 KiB
  chain-frame packing, `not_standby` for a non-warm connection, and the GUI
  hosting right-click path remain untested.
- Technical backlog: wire correlation IDs for RTC messages, PeerConnection
  rebuild for long-call SDP growth, RTP loss handling, discovery probe cost,
  per-key limits, config/logging validation, auto-rejoin after reconnect, and
  release-workflow hardening.
- Screen share/camera on macOS and Linux, window capture, shared-screen audio,
  and simulcast remain later work.

## Useful implementation map

- `server`: WebSocket handshake, host/failover, SFU, limits, discovery, and
  media port. `client`: connection, voice/share, RTT. `ui`: Ebiten desktop UI;
  `ui/state` is pure logic. `update`: GitHub release check.
- One UI `session` exists per open server. A lost connection drops that call and
  retries with backoff; `auth_failed` stops automatic retry.
- New tiles remain unsaved until first successful connection. Failed dials show
  an error and retry control.
- App hosting uses `hosted.toml`; `hermec://hosted` maps to localhost. All
  WebRTC media uses one UDP port, normally the server port.
- Current release is v0.7.0. A `v*` tag publishes only from `main` after vet and
  tests. macOS is source-built with `scripts/build-mac.sh`.

## Gotchas

- Recommend Tailscale node sharing for friends rather than tailnet invites.
- Chat history is plain, unsigned JSONL on the host. It is not encrypted.
  History caps are chain config, and old config writers reset missing cap fields
  to defaults. The app has no group-settings UI for changing them. To change
  caps for an app-hosted server, close the app; put `history_days`/`history_mb`
  and `addr` set to the hosted port in `<file>.toml`; then run:
  `hermec-server -config <file>.toml -apply-config -key
  <config>/hermec/identity.key -chain <config>/hermec/chains/<server-id>.jsonl`.
  A wrong `-key` is not the founder; a wrong `-chain` founds a new chain, reports
  appended entries, and leaves the real chain unchanged. Use v0.6+ config
  writers so the full config entry retains the history-cap fields.
- Two clients with the same key intentionally silence each other; use `-key
  <other file>` for the second client.
- No echo cancellation: use headphones.
- Tests use loopback ICE and do not trigger a Windows Firewall prompt. A stable
  built executable avoids repeated prompts when testing the app itself.
- Release executables use the console subsystem, so double-clicking shows a
  terminal window behind the app.
- `scripts/build-mac.sh` leaves a detached HEAD at the tag; return with
  `git checkout main`.
- `vitoserver` is parked; Victor currently hosts from desktop. Its service is
  disabled. The desktop has a HERMEC Windows Firewall rule.
