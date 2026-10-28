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

## Delivered (v0.7.0)

Chat, roles, channels, desktop UI, app hosting, LAN/Tailscale discovery, SFU
voice, screen sharing, Windows camera publishing, signed group configuration,
chat history, standby/failover, Windows/Linux releases, and a manual update
banner are shipped. Dedicated-hosting Tasks 1–4 are complete locally, but
remote availability is not yet claimed.

## Roadmap

1. **Dedicated hosting:** authorize/prepare the VPS host (Task 5), then pass
   remote acceptance/release (Task 6). See the
   [dedicated plan](docs/superpowers/plans/2026-10-28-dedicated-hosting.md).
2. **Embedded private networking:** complete real Tailscale/Headscale direct
   and relay evidence, then profile lifecycle and Create/Join flows. See the
   [private-network plan](docs/superpowers/plans/2026-10-28-private-network-lifecycle.md).
3. **Hosting-method migration:** after both destinations exist, implement the
   guarded, voice-empty migration preserving identity, membership and history.
   See the [migration plan](docs/superpowers/plans/2026-10-28-hosting-method-migration.md).
4. **Friends and call quality:** per-person volume/mic gain, live names,
   mid-call device changes, bandwidth/RTT work, then an experimental,
   measured default-off noise-suppression trial.
5. **Desktop distribution and updater:** only after dedicated, private, and
   migration Create/Join flows work. Windows first: verified artifacts,
   explicit restart/update, atomic replacement and rollback without routine
   admin rights; then suitable Linux/macOS approaches. Settings > Releases
   must clearly show the running version, list each version's changes, and link
   only to trusted/allowlisted official GitHub Release URLs. This is not built;
   current behavior is a browser link for manual replacement. Dedicated-server
   `update.sh` still requires a manually downloaded candidate.
6. **Regular-use gate:** exercise selected hosting modes with friends and real
   voice/camera/screen/failover before calling the app ready for regular use.
7. **Documentation:** make README task-oriented and add architecture/code-map
   guides; keep handoff detail linked rather than duplicated.

## Later

Echo cancellation; moderation; macOS/Linux capture; window/shared-audio
sharing; adaptive media priority; optional direct peer media; mobile/web;
encrypted synced history; and the icon editor.
