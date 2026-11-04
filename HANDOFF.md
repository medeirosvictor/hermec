# Hermec: session handoff

User docs: [README](README.md), [friend quickstart](docs/quickstart-friends.md),
[dedicated guide](docs/dedicated-hosting.md), [AWS EC2 from zero](docs/aws-ec2-from-zero.md),
and [protocol](docs/protocol.md).
Roadmap: [PLAN.md](PLAN.md).

## Current work

- **v0.8.0 released 2026-11-04** from `main` (`feature/dedicated-hosting`
  fast-forwarded): first signed release (signature verified independently).
  It refuses any other build, so a group moves to v0.8.0 together; v0.7.0
  users update manually once, then the in-app updater takes over.
- **Road to 1.0.0, in order:** (1) real-call hand test of v0.8.0 with a
  friend (volume, mic gain, device swap, network drop rejoin, failover);
  (2) `v0.8.1` to prove one full in-app update cycle on Windows and Linux;
  (3) Task 6 on a disposable EC2 instance; (4) second wave: moderation UI,
  invite links, push-to-talk; (5) `ui.game` split (design with Victor);
  (6) private networking evidence or an explicit decision to ship 1.0
  without it; (7) regular-use gate with friends, all HANDOFF checklists
  passed; (8) README/architecture/code-map docs; then tag v1.0.0.

- Branch: `feature/dedicated-hosting` in `.worktrees/dedicated-hosting`.
- Dedicated Tasks 1–5 are complete locally (`ca6ed69`, `020492f`, `5df0524`
  plus the founder desktop authorization/cutover flow). Task 6's automated gate
  passed; the evidence is in the dedicated plan.
- The 2026-10-30 security audit's four findings are fixed: public admission is
  validated against the signed chain's effective config; signed config bodies
  are bounds-checked on every append and replay; plaintext-password auth (the
  scrypt exhaustion path) is gone; `update.sh` validates a staged copy as the
  service user in a `systemd-run` sandbox and never runs a candidate as root.
- **Top priority (Victor, 2026-10-31): code organization.** Break the biggest
  files and god types into modular, feature-sized parts where it makes sense.
  Report first, agree the design with Victor, then refactor in small commits
  that each build and pass. Measured offenders (production code only; 35k
  lines in 179 files, plus 36k test lines):
  - `ui.game`: 126 fields, 385 methods spread over 36 files of one flat
    package (`ui` is 11.5k lines in 44 files). Heaviest: `video.go` 33,
    `main_scene.go` 30, `settings.go` 22, `hosts.go`/`callbar.go`/`addpage.go`
    20 each.
  - `server.Server`: 49 fields, 135 methods over 22 files; `server` is 9.8k
    lines in 39 files. `server/sfu.go` (1,026 lines, five types) is the
    largest file in the repo.
  - `client.Client`: 58 fields, 100 methods over 11 files; `client/voice.go`
    (901 lines) and `client/share.go` (776 lines, six types) mix call
    signalling, publishing, receiving and subscription state.
  - Longest functions: `cmd/hermec-server` `main()` 271 lines,
    `conn.authenticate` 177, `hermec-verify` `run` 158, `hshake.handshake` 136,
    `Client.readLoop` 129, `chain.State.Apply` 128, `Server.registerLink` 127.
  - Dedicated lifecycle and config logic lives in `cmd/hermec-server` instead
    of a library package; nine separate temp-file+rename writers.
  - Opus review proposals: extract `server/history`, `internal/ratelimit`,
    `server/dedicated` (records), `server/host`, later `server/sfu`; in `ui`
    move pure logic into `ui/state` and pull out `ui/crt`; one
    `internal/fsutil.WriteFileAtomic`.
- **Next steps, in order:**
  1. DONE 2026-10-31 (PLAN 1a): `internal/fsutil`, `internal/ratelimit`,
     `internal/knownhosts`, `server/history`, `server/dedicated`,
     `server/serverctl`, `server/host`, `ui/crt`, `ui/internal/gfxtest`;
     `hermec-server` `main()` and `conn.authenticate` split into steps (Opus
     verified both splits are behavior-equivalent); dedicated manager state
     machine and create/open helpers in `ui/state`. Follow-ups: `server`
     gained ~19 exported methods for `server/host` (could shrink by moving the
     yield sequence behind one call); `restoreTransition` in cmd and
     `CheckDedicatedState` share near-duplicate state switches. Original list: `server/history`,
     `internal/ratelimit`, `server/dedicated` records, `server/host`,
     `internal/fsutil.WriteFileAtomic`, dedicated lifecycle out of
     `cmd/hermec-server` (and split its `main()`), split
     `conn.authenticate`, `ui/crt`, pure `ui` helpers into `ui/state`, one
     `ui/dedicated` feature. One commit each, no behavior change, full gate
     green per commit.
  2. DONE 2026-11-02, untested (PLAN item 3, no tests run by owner request;
     only `go build` and `go vet` passed): per-person volume, mic gain,
     mid-call device switching, auto-rejoin after reconnect. Still open from
     the wave: the in-app updater (now done, see step 4). Hand test with a real call (two PCs or a
     friend, real devices): (a) right-click a person, 0%/200% change what you
     hear live, survive a restart, no crackle at 200%; (b) mic gain 50-200%
     changes the meter and what the peer hears, live; (c) unplug or change
     input and output rows mid-call: audio resumes on the new device, a bad
     device keeps the old one with a status, a Bluetooth headset switch works;
     (d) kill the server link (or the Wi-Fi) mid-call, reconnect: voice
     rejoins muted as before, camera and share stay off, and a banned, left or
     disconnected session does not; failover takeover still carries the call.
  3. Agree the `ui.game` / SFU / client split design with Victor (PLAN 1b),
     then execute it.
  4. DONE 2026-11-03, untested on a real release (PLAN item 3 updater;
     unit tests and loopback httptest only): Windows/Linux auto-updater,
     `cmd/hermec-release` (keygen/sign/verify), signed `SHA256SUMS.txt`
     in `release.yml` (signature bound to the release tag, so replay of an
     older release fails), `update.sh` verifying by default with the installed
     root-owned tool. Security review fixes applied. To go live, Victor
     must (step 0): run `hermec-release keygen`, set `update.PublicKey`
     (`update/verify.go`), add the `HERMEC_RELEASE_KEY` secret
     ([docs/release-signing.md](docs/release-signing.md)); until then the app
     fails closed (link only) and the release workflow's verify step fails.
     Hand test with two real releases: install vN on Windows (and Linux),
     publish vN+1, confirm background download, "restart to update", the
     relaunch with the same args, `.old` kept until the new build has started
     and then removed, a
     read-only install folder falling back to the link, a version_mismatch
     against a vN+1 server offering "update now", and the installed `update.sh` with
     `HERMEC_SUMS`/`HERMEC_ARCHIVE`/`HERMEC_TAG`. Not covered: macOS (manual by design), the real GitHub
     redirect host (`release-assets.githubusercontent.com` or
     `objects.githubusercontent.com`, both allowed), Windows Defender or
     SmartScreen reacting to a renamed/relaunched unsigned exe.
- **Status 2026-11-03:** first wave is implemented and its unit tests pass:
  per-person volume + soft limiter, mic gain, mid-call device switching,
  auto-rejoin after reconnect, and the desktop auto-updater (signed,
  tag-bound `SHA256SUMS.txt.sig`, background download, "Restart to update",
  "update now" on a newer server's version_mismatch). The release public key
  is embedded (`update/verify.go`); the private key was generated outside the
  repo and must be added by Victor as GitHub secret `HERMEC_RELEASE_KEY` (see
  `docs/release-signing.md`). Until then tagged releases fail at the verify
  step. Dedicated servers stay manual: `update.sh` verifies the signed sums
  with the installed `/usr/local/lib/hermec/hermec-release`.
  Still needs real-device/real-release checks: volume 0/200%, mic gain heard
  by a peer, device swap incl. Bluetooth/unplug, network drop rejoining muted,
  failover still carrying the call, one full update cycle on Windows and Linux
  (incl. SmartScreen/Defender), `update.sh` signed mode on the VPS.
  Testing convention (Victor): run only the tests for code that changed, not
  the full suite every time.
- **Regular-use musts, first wave** (PLAN item 3): per-person
  volume and mic gain, mid-call audio device switching and auto-rejoin voice
  after a reconnect are done but untested (see Next steps 2); the in-app
  updater (builds must match exactly) is done but untested (Next steps 4).
  Second wave: moderation UI, invite links, push-to-talk then echo
  cancellation. Nice to have: pixel-art low-bandwidth video (PLAN.md).
- **In parallel: Task 6 — remote acceptance/release.** Victor is setting up the
  AWS account and disposable EC2 instance following
  [docs/aws-ec2-from-zero.md](docs/aws-ec2-from-zero.md); run the
  disposable-VPS and AWS rows with him. Do not claim remote hosting is
  available or provision cloud resources. The `systemd-run` validation branch
  of `update.sh` has no automated coverage; exercise it on the VPS
  (update/rollback row).
- Flaky test: `server/host_fix_test.go` `TestYieldAbortsWhenHistoryFails` fails
  occasionally under full-suite load (fixed `time.Sleep(2 * probeEvery)`); it
  passes alone. Replace the sleep with an explicit wait for the probe/merge hook.
- Package refactor proposals (Opus review, 2026-10-31), not started: extract
  `server/history`, `internal/ratelimit`, dedicated records (`server/dedicated`)
  and `server/host`; move dedicated lifecycle out of `cmd/hermec-server`; later
  `server/sfu`. In `ui`, move pure logic into `ui/state` and pull `ui/crt` out.
  One `internal/fsutil.WriteFileAtomic` should replace the nine temp+rename
  writers (`cmd/hermec-server/hostflags.go` `writeFile0600` is the weakest).
- Deferred audit suggestions: signed release manifests (Sigstore/cosign or an
  offline minisign key) for update provenance, and one shared secure
  atomic-file helper for the several private-file writers.
- Private networking cannot ship before real Tailscale/Headscale direct and
  relay evidence. Migration follows dedicated/private hosting; desktop
  distribution/updater follows migration.

Active references: [dedicated plan](docs/superpowers/plans/2026-10-28-dedicated-hosting.md),
[AWS disposable-VPS runbook](docs/aws-ec2-from-zero.md), [private-network plan](docs/superpowers/plans/2026-10-28-private-network-lifecycle.md),
[migration plan](docs/superpowers/plans/2026-10-28-hosting-method-migration.md),
[approved design](docs/superpowers/specs/2026-10-26-hosting-readiness-design.md),
and [noise trial](docs/superpowers/plans/2026-10-27-noise-suppression-trial.md).

## Non-negotiable constraints

- No backward compatibility before 1.0.0 (Victor, 2026-10-30): no code, config
  key, protocol path or test exists to support an earlier HERMEC build. Every
  client and host in a group runs the same build; anything older or different
  is rejected (`version_mismatch`), not bridged.
- Anyone can self-host; no project servers, in-app cloud provisioning, cloud
  credentials, or claim that public hosting is safe without TLS and admission.
- The host is trusted; Ed25519 keys, not names, are identity. Never transfer
  founder/member private keys to a VPS.
- Do not run GUI automation against Victor's real `%APPDATA%\hermec`; there is
  no `-config-dir`. Use isolated configuration first.
- Never commit untracked `primos.server.toml`, `hermec2.key`, or `.claude/`;
  leave old `.claude/worktrees/agent-*` alone. Releases are tagged from `main`.

## Essential manual gates

- **Dedicated:** pass the disposable-VPS plan row: trusted WSS, pinning,
  password admission, reciprocal Opus media, persistence, recovery and real
  remote friend/client evidence before availability/release claims.
- **Private networking:** real Tailscale/Headscale direct *and relay* media and
  standby evidence remains required.
- **Failover/media:** with a node-shared Tailscale friend, verify standby
  takeover, empty-voice handback, roster hosting controls, reachability
  selection, camera/screen transfer, and Tailscale grants.
- **Desktop/real devices:** retain visual/input/device-permission, firewall,
  discovery, voice, screen-share and camera checks; headless coverage does not
  replace these.

## Open risks and gotchas

- Failover merge/call-transfer/warm-host races and cleanup gaps remain; consult
  tests and logs before changing host state. Merged chat is not pushed live to
  winner clients, and some takeover/redial failures still require manual rejoin.
- Pre-1.0 builds and plaintext-password configurations are unsupported and
  rejected; there is no protocol or authentication bridge. Preserve host proof
  before authentication ([protocol §3.1](docs/protocol.md#31-handshake)). Chat
  history is plain unsigned JSONL on the host, not encrypted.
- No echo cancellation: use headphones. Noise suppression is experimental only
  after a measured probe; it is not in the app.
- Founder hosting controls exist, but no UI ban/grant controls. macOS/Linux
  camera/share publishing, window capture, shared-screen audio and simulcast
  remain later work.
- Two clients using one key intentionally silence each other; use `-key` with a
  different file for a second client. `scripts/build-mac.sh` leaves detached
  HEAD; return with `git checkout main`.

## Useful commands

```sh
go run ./cmd/hermec -local
go vet ./...
go test ./...
go test -race ./... # needs cgo
```

This machine needs `C:\msys64\ucrt64\bin` on `PATH` and `CGO_ENABLED=1` for
cgo/race work. A stable built executable avoids repeated Windows Firewall
prompts: `go build -o bin\hermec.exe ./cmd/hermec`.
