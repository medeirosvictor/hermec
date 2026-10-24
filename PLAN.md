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

## Done (released through v0.5.1, plus current main/feature work)

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

1. **Friends adjustments**: per-person volume and mic gain, live names, audio
   device switching mid-call, bandwidth metrics, ping fixes.
2. **Icon editor** (B.3): in-app pixel-art editor for a server icon.

## Later

- Invite links and allowlists in the app, TLS for public hosting (server ID
  pinning is done).
- Noise suppression and echo cancellation (no cgo if possible).
- Moderation: kick and ban by fingerprint.
- Screen share from macOS and Linux, window capture, shared-screen audio.
- Camera capture from macOS and Linux; adaptive enforcement of the recorded
  audio, screen, camera priority order.
- Direct peer-to-peer media opt-in; mobile and browser clients over the same
  WebRTC protocol.
- Headscale as an optional coordination server; encrypted synced chat history.

Details and open checklists live in [HANDOFF.md](HANDOFF.md).
