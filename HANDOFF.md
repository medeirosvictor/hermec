# Hermec: session handoff

User docs: [README](README.md), [friend quickstart](docs/quickstart-friends.md),
[dedicated guide](docs/dedicated-hosting.md), [AWS EC2 from zero](docs/aws-ec2-from-zero.md),
and [protocol](docs/protocol.md).
Roadmap: [PLAN.md](PLAN.md).

## Current work

- Branch: `feature/dedicated-hosting` in `.worktrees/dedicated-hosting`.
- Dedicated Tasks 1–5 are complete locally (`ca6ed69`, `020492f`, `5df0524`
  plus the founder desktop authorization/cutover flow). The documentation
  changes are committed with the dedicated-hosting work.
- **Next work: Task 6 — remote acceptance/release.** There is no disposable
  VPS/AWS test yet: do not claim remote hosting is available or provision cloud
  resources.
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
  client and host in a group runs the same build; anything older is rejected,
  not bridged.
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
