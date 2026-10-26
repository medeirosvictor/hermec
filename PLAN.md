# Hermec: roadmap

A small, open-source, censorship-resilient comms app, "simple like TeamSpeak":
voice, screen share and text chat on servers anyone can host. The project owns
no infrastructure. How the app works is in [README.md](README.md) and
[docs/protocol.md](docs/protocol.md). The working brief for the next session is
[HANDOFF.md](HANDOFF.md).

## Principles that still hold

- Anyone can self-host. No project-owned servers.
- Media is relayed by the server by default, so participants never see each
  other's IPs. Direct peer-to-peer media may come later as a per-participant
  opt-in.
- The host is trusted and the docs say so. No private messages and no
  federation for now.
- Identity is an Ed25519 key file, no accounts. Names are free text; the
  fingerprint is the identity.
- Go everywhere: Pion (WebRTC), Ebiten (client), gopus (Opus, pure Go), govpx
  (VP8, pure Go). malgo (miniaudio) is the only cgo dependency.
- Small codebase, one file per platform to download, idle server near zero cost.

## Done (v0.6.0)

- Chat server and headless client, Ed25519 auth, roles, channels, presence.
- Desktop GUI: CRT themes, fonts, settings, title bar, server rail, several
  servers at once, member list, server names, Add a Server page, hosting from
  the app.
- Voice (SFU relay), audio device choice, jitter buffer, connection quality.
- Server discovery on the LAN and on Tailscale.
- Releases for Windows and Linux (x64 and ARM), update banner, macOS from
  source.
- Hardening: connection and input limits, paste and caret editing.
- Screen share (Windows to everyone), one media UDP port, session log files.
- Call and stream controls: fullscreen exit, self-preview, change screen,
  "voice connected" panel.
- Signed server identity/config chain, retained chat history, host links, warm
  standby and in-call failover.
- Independent audio, camera and screen lanes; Windows webcam capture; a
  multi-participant call view; up to ten simultaneous camera publications and
  one selected screen per viewer. Camera is fixed at 640x360/15 fps/300 kbps.

## Next

1. **Genuine-defect cleanup**: finish simple, user-facing failure handling;
   make the channel sidebar scroll; fix mute-state reconciliation; surface
   friend-host startup failures; keep technical detail in session logs.
2. **Friends adjustments**: per-person volume and mic gain, live names, audio
   device switching mid-call, bandwidth metrics, ping fixes.
3. **Hosting options** (Victor, 2026-10-26; architecture discussion next):
   private groups using Tailscale or self-hosted Headscale, plus a portable
   dedicated server on AWS or another VPS, operated like a Minecraft server
   (download, configure, run, share an address). No in-app cloud provisioning.
   Embedded networking is the selected direction (Victor, 2026-10-26): one
   private-network integration with selectable coordination service, so friends
   do not need a separate Tailscale installation. Validate tsnet integration.
   Prove both signaling and UDP media, group isolation, and standby reachability.
   Include Create Server flows for each hosting method and a Server Settings
   flow to change an existing server's method. Show enrollment/setup progress,
   reachability, invite/share details and actionable errors. For changes, preview
   affected clients/standby hosts and any interruption; validate the destination,
   preserve server identity, membership and history, and provide recovery on
   failure. Reissue connection details when needed. Design permissions, live-call
   behavior and migration/recovery before implementation.
   Public hosting includes TLS, admission controls/invites, persistence, updates,
   firewall guidance, and backup/restore. Hosts own infrastructure and costs.
   Proposed written design:
   [hosting readiness](docs/superpowers/specs/2026-10-26-hosting-readiness-design.md)
   (awaiting review).
4. **Experimental microphone noise suppression**: evaluate freely redistributable
   algorithms, starting with RNNoise, against SpeexDSP and DeepFilterNet.
   Ship a default-off toggle only after a measured integration probe: local
   processing before Opus, switchable during calls, with no cloud audio service.
   Compare voice quality, noise reduction, CPU, latency, binary size and all
   release platforms. Prefer pure Go or embedded WASM if practical; do not
   introduce an unbundled runtime/library requirement. Echo cancellation remains
   a separate follow-up. See [research notes](docs/research/hosting-and-noise-suppression.md).
5. **Usability gate**: finish the four cleanup fixes, exercise the selected
   hosting modes with friends, test voice/camera/screen and failover on real
   devices, and decide whether the experimental filter is useful. Victor's
   intended milestone is an app ready for regular use, followed by polishing
   and quality-of-life work; feature completion alone does not pass this gate.
6. **Documentation milestone**: make `README.md` the task-oriented user guide;
   add `docs/architecture.md` for how HERMEC works under the hood and
   `docs/code-map.md` for folders, important files and common development
   entry points. Link rather than duplicate material in `HANDOFF.md`.
7. **Icon editor** (B.3): in-app pixel-art editor for a server icon.

## Later

- Echo cancellation (no cgo if possible); noise suppression is in the next round.
- Moderation: kick and ban by fingerprint.
- Screen share from macOS and Linux, window capture, shared-screen audio.
- Camera capture from macOS and Linux; adaptive enforcement of the recorded
  audio, screen, camera priority order.
- Direct peer-to-peer media opt-in; mobile and browser clients over the same
  WebRTC protocol.
- Encrypted synced chat history.

Details and open checklists live in [HANDOFF.md](HANDOFF.md).
