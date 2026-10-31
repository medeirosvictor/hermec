# Hermec: session handoff

User docs: [README](README.md), [friend quickstart](docs/quickstart-friends.md),
[dedicated guide](docs/dedicated-hosting.md), [AWS EC2 from zero](docs/aws-ec2-from-zero.md),
and [protocol](docs/protocol.md).
Roadmap: [PLAN.md](PLAN.md).

## Current work

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
  1. Easy, low-risk extractions (PLAN item 1a): `server/history`,
     `internal/ratelimit`, `server/dedicated` records, `server/host`,
     `internal/fsutil.WriteFileAtomic`, dedicated lifecycle out of
     `cmd/hermec-server` (and split its `main()`), split
     `conn.authenticate`, `ui/crt`, pure `ui` helpers into `ui/state`, one
     `ui/dedicated` feature. One commit each, no behavior change, full gate
     green per commit.
  2. Regular-use musts, first wave (below).
  3. Agree the `ui.game` / SFU / client split design with Victor (PLAN 1b),
     then execute it.
- **Regular-use musts, first wave** (PLAN item 3): per-person
  volume and mic gain; mid-call audio device switching; auto-rejoin voice after
  a reconnect plus an in-app updater (now required: builds must match exactly).
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
