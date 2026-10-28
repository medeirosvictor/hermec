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

## Done (v0.7.0)

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
- Readiness cleanup: mute changes reconcile safely on send failure and call
  replacement; friend-host startup failures are visible; voice, camera and
  screen errors are friendly while diagnostics stay in logs; the channel
  sidebar scrolls with aligned hit targets.

## Next

1. **Dedicated AWS/VPS hosting**: the next user-facing milestone. Package the
   existing headless server for persistent operator-owned hosting, require TLS
   and admission for public mode, add bootstrap/service/backup/update tooling,
   an AWS EC2 guide and desktop Dedicated Server join/create flow. No cloud
   provisioning or cloud credentials. Availability requires the disposable-VPS
   gate in [the implementation plan](docs/superpowers/plans/2026-10-28-dedicated-hosting.md).
2. **Embedded private networking**: local provider plumbing, tsnet adapter and
   headless media/failover scenario are proven on the development branch. Real
   Tailscale/Headscale direct and relay rows remain NOT RUN and block shipping.
   After that evidence, implement profile lifecycle and Create/Join UI using
   [the private-network plan](docs/superpowers/plans/2026-10-28-private-network-lifecycle.md).
3. **Hosting-method migration**: after both destinations exist, add guarded
   local/private/dedicated changes preserving identity, membership and history,
   with voice-empty commit and restart recovery. See
   [the migration plan](docs/superpowers/plans/2026-10-28-hosting-method-migration.md).
4. **Friends adjustments**: per-person volume and mic gain, live names, audio
   device switching mid-call, bandwidth metrics, ping fixes.
5. **Experimental microphone noise suppression**: evaluate freely redistributable
   algorithms, starting with RNNoise, against SpeexDSP and DeepFilterNet.
   Ship a default-off toggle only after a measured integration probe: local
   processing before Opus, switchable during calls, with no cloud audio service.
   Compare voice quality, noise reduction, CPU, latency, binary size and all
   release platforms. Prefer pure Go or embedded WASM if practical; do not
   introduce an unbundled runtime/library requirement. Echo cancellation remains
   a separate follow-up. See [research notes](docs/research/hosting-and-noise-suppression.md).
6. **Desktop Distribution & Updater**: only after dedicated hosting, embedded
   private networking, and hosting-method migration—including their Create/Join
   flows—work. Friends adjustments and noise suppression are earlier backlog
   items, not prerequisites for distribution. Windows first: a per-user installer
   and in-app download with cryptographic artifact/manifest verification,
   explicit Restart and update,
   atomic replacement, previous-version rollback, and no routine admin need.
   Follow with an appropriate Linux package/update layout; wait for signed,
   notarized distributable builds on macOS. Add Settings > Releases, listing
   what changed in each version with a link to its corresponding official GitHub
   Release. Release links must use trusted, allowlisted GitHub release URLs, and
   the page must clearly show the running version. Current releases only show an
   update banner that opens GitHub; no automatic desktop updater or Releases
   page exists. This is separate from dedicated-server `update.sh`, which
   requires a manually downloaded candidate.
7. **Regular-use/distribution gate**: exercise the selected hosting modes with
   friends, test voice/camera/screen and failover on real devices, and decide
   whether the experimental filter is useful. Victor's intended milestone is an
   app ready for regular use, followed by polishing and quality-of-life work;
   feature completion alone does not pass this gate.
8. **Documentation milestone**: make `README.md` the task-oriented user guide;
   add `docs/architecture.md` for how HERMEC works under the hood and
   `docs/code-map.md` for folders, important files and common development
   entry points. Link rather than duplicate material in `HANDOFF.md`.
9. **Icon editor** (B.3): in-app pixel-art editor for a server icon.

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
