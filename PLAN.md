# Hermec roadmap

Hermec is a self-hosted voice, screen-share and text-chat app. See
[README.md](README.md), [protocol](docs/protocol.md), and the current
[handoff](HANDOFF.md).

## Principles

- Anyone can self-host; HERMEC operates no infrastructure.
- The server relays media by default, so participants do not expose IPs to one
  another. The host is trusted.
- Identity is an Ed25519 key file; names are not identity.
- Keep the codebase and downloads small; Go/Pion/Ebiten remain the foundation.

## Delivered

Chat, roles, channels, desktop UI, app hosting, LAN/Tailscale discovery, SFU
voice, screen sharing, Windows camera publishing, signed group configuration,
chat history, standby/failover, Windows/Linux releases, and a manual update
banner are shipped. Dedicated-hosting Tasks 1–5 are complete locally, but
remote availability is not yet claimed.

## Roadmap

Priority order as of 2026-10-31 (Victor). Dedicated-hosting remote acceptance
runs in parallel whenever the EC2 instance is ready.

1. **Code organization (top priority).** Break the largest files and the
   god types into feature-sized, well-rounded parts where it makes sense:
   `ui.game` (126 fields, 385 methods over 36 files), `server.Server`
   (135 methods over 22 files), `client.Client` (100 methods), the 1,000-line
   `server/sfu.go`, `client/voice.go`/`share.go`, and logic living in
   `cmd/hermec-server`. The measured offender list is in [HANDOFF.md](HANDOFF.md);
   the design is agreed with Victor before refactoring starts.
2. **Dedicated hosting:** pass remote acceptance/release (Task 6) on a
   disposable EC2 instance. See the
   [dedicated plan](docs/superpowers/plans/2026-10-28-dedicated-hosting.md) and
   [AWS runbook](docs/aws-ec2-from-zero.md).
3. **Regular-use musts, first wave:**
   - Per-person volume (0–200%, saved per fingerprint) and mic gain, with a
     soft limiter. Friends were too quiet; this closes the audio gate.
   - Audio device switching mid-call (today capture/playback open once per
     join, so a change applies only after leave and rejoin).
   - Auto-rejoin voice after a reconnect, and an in-app auto-updater. Builds
     must match exactly, so updating the host forces everyone to update;
     without an updater that is painful. Windows first: verified artifacts,
     explicit restart, atomic replacement and rollback without routine admin
     rights, then Linux/macOS. Settings shows the running version and each
     release's changes and links only to official GitHub Release URLs.
4. **Regular-use musts, second wave:**
   - Moderation UI: kick, ban/unban and role grants (the chain and server
     already enforce them; there is no UI).
   - Invite links: one shareable link carrying address, pinned server ID and
     admission, instead of three separately pasted values.
   - Push-to-talk with a global hotkey (cheap answer to "headphones
     mandatory"), then echo cancellation.
5. **Embedded private networking:** complete real Tailscale/Headscale direct
   and relay evidence, then profile lifecycle and Create/Join flows. See the
   [private-network plan](docs/superpowers/plans/2026-10-28-private-network-lifecycle.md).
6. **Hosting-method migration:** after both destinations exist, implement the
   guarded, voice-empty migration preserving identity, membership and history.
   See the [migration plan](docs/superpowers/plans/2026-10-28-hosting-method-migration.md).
7. **Call quality:** live names, bandwidth/RTT metrics and ping fixes, then a
   measured, default-off noise-suppression trial.
8. **Regular-use gate:** exercise hosting modes with friends and real
   voice/camera/screen/failover (the open checklists in HANDOFF) before calling
   the app ready for regular use.
9. **Documentation:** make README task-oriented and add architecture/code-map
   guides; keep handoff detail linked rather than duplicated.

## Nice to have

- **Graceful low-bandwidth video:** when a connection struggles, send fewer
  pixels instead of fewer frames and upscale with crisp nearest-neighbour
  scaling, so the picture turns deliberately pixel-art (fitting the CRT look)
  while motion stays smooth, with a small "low bandwidth" tag. Needs live
  bitrate adaptation from the bandwidth metrics in item 7; simulcast later.

## Later

macOS/Linux capture; window/shared-audio
sharing; adaptive media priority; optional direct peer media; mobile/web;
encrypted synced history; and the icon editor.
