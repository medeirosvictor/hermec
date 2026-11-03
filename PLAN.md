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

1. **Code organization (top priority).** Two stages:
   - **1a. Easy, low-risk extractions (next up).** Each is its own commit that
     builds and passes on its own, with no behavior change:
     - `server/history`: move `History` (self-contained type) out of `server`.
     - `internal/ratelimit`: `ratelimit.go` and `readpace.go`.
     - `server/dedicated`: transition records, readiness lock, URL helper
       (now in `core/proto`), bootstrap record logic.
     - `server/host`: `Host` orchestration and `hostmerge.go`, behind a narrow
       interface to `Server`; one known-hosts store shared with the app.
     - `internal/fsutil.WriteFileAtomic`, replacing the nine temp+rename
       writers (fixes the weak `writeFile0600`).
     - Move dedicated lifecycle/config validation out of `cmd/hermec-server`
       into the library; break `main()` (271 lines) and
       `conn.authenticate` (177 lines) into named steps.
     - `ui/crt` for the CRT shader; pure helpers from `ui` into `ui/state`;
       merge the three `ui/dedicated*` files into one feature;
       `ui/state/watch.go` already renamed.
   - **1b. Bigger splits (design with Victor first).** `ui.game` (126 fields,
     385 methods) into per-feature state structs (call, video, hosting,
     settings, …) with their own update/draw; split `server/sfu.go` and
     `client/voice.go`/`share.go` along publish / receive / signalling lines.
     The measured offender list is in [HANDOFF.md](HANDOFF.md).
2. **Dedicated hosting:** pass remote acceptance/release (Task 6) on a
   disposable EC2 instance. See the
   [dedicated plan](docs/superpowers/plans/2026-10-28-dedicated-hosting.md) and
   [AWS runbook](docs/aws-ec2-from-zero.md).
3. **Regular-use musts, first wave:**
   - DONE (untested): per-person volume (0–200%, saved per fingerprint) and mic
     gain, with a soft limiter. Friends were too quiet; this closes the audio
     gate.
   - DONE (untested): audio device switching mid-call.
   - DONE (untested): auto-rejoin voice after a reconnect.
   - DONE (untested on a real release): in-app auto-updater for Windows and
     Linux. Builds must match exactly, so updating the host forces everyone
     to update. CI signs `SHA256SUMS.txt` (Ed25519, secret
     `HERMEC_RELEASE_KEY`); the app downloads in the background, verifies
     signature (bound to the tag; the tag must be newer) then SHA-256, stages
     `<exe>.new` under an update lock, and "Restart to update" re-hashes it and
     swaps it in (rolling back on failure). A version_mismatch from a newer server
     offers "update now". macOS and dedicated servers stay manual
     (the installed, root-owned `update.sh` verifies the signed release by default). Fails closed
     until `update.PublicKey` is set; see docs/release-signing.md.
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
