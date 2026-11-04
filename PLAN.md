# Hermec roadmap

Hermec is a self-hosted voice, screen-share and text-chat app. See
[README.md](README.md), [protocol](docs/protocol.md), and the current
[handoff](HANDOFF.md).

## Principles

- Anyone can self-host; HERMEC operates no infrastructure.
- The server relays media by default, so participants do not expose IPs to one
  another. The host is trusted.
- Identity is an Ed25519 key file; names are not identity.
- Every peer runs the same build; there is no cross-version compatibility
  before 1.0.
- Keep the codebase and downloads small; Go/Pion/Ebiten remain the foundation.

## Delivered

Chat, roles, channels, desktop UI, app hosting, LAN/Tailscale discovery, SFU
voice, screen sharing, Windows camera publishing, signed group configuration,
chat history, standby/failover, dedicated-server tooling, per-person volume, mic
gain, mid-call device switching, auto-rejoin, and a signed in-app updater for
Windows and Linux ship in v0.8.0. Remote dedicated hosting is not yet claimed:
its acceptance run is pending.

## Next, in priority order

1. **Hand test v0.8.0** in a real call (volume, mic gain, device swap, network
   drop rejoin, failover).
2. **v0.8.1** to prove a full self-update cycle on Windows and Linux.
3. **Dedicated hosting acceptance (Task 6)** on a disposable EC2 instance. See
   the [AWS runbook](docs/aws-ec2-from-zero.md) and the
   [dedicated guide](docs/dedicated-hosting.md).
4. **Second-wave regular-use features:**
   - Moderation UI: done (ban/unban and admin grants from the person and
     server-tile menus; there is no separate kick op, a ban disconnects).
   - Invite links: done (`hermec://join?v=1&addr&id[&name][&pw]`, Copy invite
     link on the tile menu, paste field on Add a Server; OS URL-scheme
     registration is later, needs installer work).
   - Push-to-talk: done (Settings voice mode / ptt key; local transmit gate;
     system-wide key on Windows via GetAsyncKeyState, window-focused elsewhere;
     a global hotkey on Linux/macOS is later). Echo cancellation remains later.
5. **Code organization, stage 1b** (design with Victor first): split `ui.game`
   into per-feature state structs with their own update/draw; split
   `server/sfu.go` and `client/voice.go`/`share.go` along publish / receive /
   signalling lines; slim the `server` surface used by `server/host`.
6. **Embedded private networking:** complete real Tailscale/Headscale direct and
   relay evidence, then profile lifecycle and Create/Join flows, or decide to
   ship 1.0 without it. See the
   [private-network plan](docs/superpowers/plans/2026-10-28-private-network-lifecycle.md).
7. **Hosting-method migration:** once both destinations exist, a guarded,
   voice-empty migration preserving identity, membership and history. See the
   [migration plan](docs/superpowers/plans/2026-10-28-hosting-method-migration.md)
   and the [design](docs/superpowers/specs/2026-10-26-hosting-readiness-design.md).
8. **Call quality:** live names, bandwidth/RTT metrics and ping fix, then a
   measured, default-off
   [noise-suppression trial](docs/superpowers/plans/2026-10-27-noise-suppression-trial.md).
9. **Regular-use gate:** exercise hosting modes with friends and real
   voice/camera/screen/failover (the open checklists in HANDOFF).
10. **Documentation:** README split done (see the [docs index](docs/README.md));
    architecture and code-map guides open.

Then tag **v1.0.0**.

## Nice to have

- **Graceful low-bandwidth video:** when a connection struggles, send fewer
  pixels instead of fewer frames and upscale with nearest-neighbour scaling, so
  the picture turns deliberately pixel-art while motion stays smooth, with a
  small "low bandwidth" tag. Needs live bitrate adaptation from item 8.

## Later

macOS/Linux capture; window/shared-audio sharing; adaptive media priority;
optional direct peer media; mobile/web; encrypted synced history; the icon
editor.
