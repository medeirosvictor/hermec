# Hermec — Application Plan

> Working document for planning the Hermec application. Nothing here is final — this is where we discuss and shape the idea before writing code.

## What is Hermec?

A small, open-source, censorship-resilient comms app — "simple like TeamSpeak." Voice chat, screensharing, and video conferencing on PC and mobile. Motivated by Discord being blocked/censored in Brazil. Intended as an open base other comms apps can build on, with an eye toward creating standards.

## Goals

- Decentralized: anyone can self-host; **zero project-owned infrastructure**
- Safe between strangers — privacy (incl. IP privacy) controlled by each participant
- Portable, easy to install, straightforward to use
- Lightweight, small codebase, fast
- Open source, with an easy integration API (moddability)

## Non-goals

- Communities/social platform features (Discord-style nitro, discovery, etc.)
- Federation between servers (for now — small codebase wins)

## Architecture (agreed so far)

**Connectivity model** — hybrid of TeamSpeak-style hosting and P2P:

- A lightweight **Hermec server** anyone can host: handles rooms, presence, signaling, *and* acts as a TURN-style media relay.
- **Media is relayed by default** (privacy-safe, works through CGNAT/mobile); **direct P2P is a per-participant opt-in** optimization.
- A relay-choosing participant is relayed on *every* link, even against direct-mode peers — their choice protects them regardless of others.
- Mixed calls: direct and relayed links coexist per pair; no whole-room downgrade.
- Host sees the bandwidth cost of active relayed streams (hosting stays informed-consent).
- Accepted trade-off: host bandwidth pays for participant privacy.

## Core features

**MVP (first ship):**
- Voice chat
- Screensharing
- Text chat
- Abstraction service layer — core behind clean interfaces so Hermec works as an agnostic boilerplate for comms apps (integration/modding from day one)

**Later:** camera video conferencing (cheap once screenshare works — same pipeline), mobile and browser clients (same WebRTC protocol, same servers).

## Platform (agreed)

- MVP client: **native desktop app, minimal GUI** (one lightweight window: channels, chat, video area). Low RAM; no browser engine.
- Packaging goal: **single static binary per OS** — download one file, run it.
- Target OSes: **Windows + Linux** first; macOS after (signing/notarization deferred).
- Mobile/browser: deferred, not abandoned — WebRTC wire protocol keeps them compatible later.

## Tech stack (agreed)

- **Go everywhere** — core/service layer, server, desktop client. One language, one repo; cross-compiles to Pi (`GOARCH=arm64`) and every desktop OS.
- **Pion** — pure-Go WebRTC for server and client transport.
- **Ebiten** — pure-Go 2D engine rendering the client; custom widgets, Kage shaders for CRT effects.
- **libopus via CGo** — the one C dependency (voice codec). Caveat: CGo means cross-compiling needs a C toolchain per target; still ships as one file.
- **Client aesthetic:** old military field-radio / amber phosphor CRT — monochrome amber on black, bitmap fonts, scanline/glow shaders, runtime Floyd–Steinberg-dithered 1-bit avatars.

## Service layer & integration (agreed)

- **Roles & signed identities** are the foundation: every connected thing (human or bot) is an identity; per-server config grants roles; roles gate which views/endpoints may be called.
- **The service layer is the server's API** — one role-gated protocol over the same connection clients use, addressing the server runtime or a specific channel.
- **Ephemeral media sessions:** a voice channel's relay pipeline spins up when the first user enters and tears down when the last leaves. Idle server ≈ tiny idle process (Pi-friendly).
- **Doors:** (1) importable Go core packages — ship in MVP, our GUI is the first consumer; (2) server API for bots/agents — MVP; local client-control API deferred (will reuse the role-gated protocol); (3) theming config — minimal theme file in MVP (palette, font, shader toggles), layout config later.
- **MCP server:** later, as a separate project that connects as an ordinary bot identity.

## Identity (agreed)

- **Ed25519 keypair identity, SSH-style single key file**; public key = identity, proven by signing a server challenge. No accounts, no issuer.
- **Per-device keys** (mobile gets its own); device linking/cross-signing deferred post-MVP — until then a user's devices are distinct identities.
- Display names are free-text; UI shows key fingerprints; servers pin first-seen keys. Key loss is unrecoverable by design; first-run backup prompt.

## Security model (agreed, MVP)

- **No private messaging in MVP** — voice channels (w/ screenshare) + group chat only; all content is channel-scoped.
- **Access control:** password/invite and/or key allowlist gate server entry; roles gate everything inside.
- **Transport encryption everywhere** (DTLS-SRTP media, TLS signaling/chat) — answers the ISP/state-blocking adversary fully.
- **Host is trusted** and documented as such (can see relayed media + chat). E2EE not in MVP; protocol reserves key-exchange message types and frame-encryption hooks for later (DMs/stranger-scale).
- Hardening: Go memory safety, small attack surface, parser fuzzing.

## Spec

Full design spec: **`docs/specs/2026-10-06-hermec-design.md`** (approved 2026-10-06). Repo: https://github.com/medeirosvictor/hermec

## Implementation roadmap

Four sequential plans, each ending in working software:

1. **Foundation** — identity, wire protocol, roles, server + headless client with group chat. Plan: `docs/superpowers/plans/2026-10-06-hermec-foundation.md` — ✅ **merged to main 2026-10-06 (PR #1)**
2. **GUI chat client** *(reordered before voice — Victor wants to see the app take shape incrementally)* — Ebiten window, connect screen, channel list + chat, amber CRT theme v1, and a single run-everything dev command (`-local` spawns an in-process server). Plan: `docs/superpowers/plans/2026-10-06-hermec-gui-chat.md`. Deferred to a later pass: `--headless` client mode with structured logging for dev/agent debugging (Victor's explicit intent, when debugging needs it).
3. **Voice** — ✅ **merged to main 2026-10-07** (16 commits; final review clean; Victor's live-call listening checklist still to run — see .vibe-wise notes). Post-merge hardening queue, in priority order: wire correlation IDs for rtc messages (retires the 300ms glare heuristic, stale-answer residual, and error-misattribution warts at once); SFU PeerConnection rebuild past N dead m-lines (long-call SDP growth); client-side packet-loss concealment on RTP sequence gaps; per-peer rtc_offer debounce. Original design notes: Discord-style channel types (text as today + voice channels); joining a voice channel = being in the call, one call at a time; open mic default, mute button + icon (M key), mute state visible to others; voice occupancy + mute icons shown in the channel pane before joining; SFU relay via Pion + Opus, ephemeral sessions, **relay-only** (per-participant direct-P2P opt-in moves to its own later plan); client-side speaking indicator; **rate limiting** (spec §8) included. Requires cgo (malgo mic capture + libopus) → one-time MinGW toolchain install, which also enables local `-race`. Plan: `docs/superpowers/plans/2026-10-07-hermec-voice.md`
4. **Server discovery (Hamachi level 2)** — ✅ **merged to main 2026-10-07** (10 commits; final review clean; Victor's LAN/tailnet/visual checklist outstanding). Connect screen lists live Hermec servers on the LAN and tailnet (DISCOVERED section, one-click join, Ctrl+R). Footprint snapshot taken same day: see `docs/footprint.md`. **Spike verdict (2026-10-07): level 3 deferred.** tsnet+Headscale works in principle (BSD-3, pure Go, ~150-300 lines of glue, paste-a-key join) but costs +~19MB in BOTH binaries, a huge dependency tree + Go toolchain coupling, and — decisive — moves hard infrastructure onto hosts (public Linux box, domain, TLS, own DERP relay to avoid depending on Tailscale Inc.). Conflicts with "anyone can host". Revisit as an OPT-IN tsnet listener behind a build tag for hosts who already run a tailnet/Headscale. (Spike code was throwaway; discarded.)
5. **Release & update** — ✅ **merged + v0.1.0 tagged 2026-10-07** (9 commits; final review clean; pipeline proven by the v0.1.0-rc1 dry run, first try). The "send the exe to friends" gate is OPEN: friends install from the Releases page and get the in-app update banner (Ctrl+U) on new versions. Bonus landed: GUI idle CPU 34% → ~6.5% of one core (render cache). Follow-up filed: extract RenderKey to ui/state with tests. Original scope: CI release workflow on git tags (hermec-windows.zip with DLLs, Linux server binaries); version stamped via ldflags; startup update check against GitHub Releases (opt-out in settings) showing an in-app update banner with download link; optional: server advertises known-latest version in auth_ok (censorship-resilient nudge); self-update (download-and-swap) is v2.
6. **Moderation** *(design settled: kick + ban by fingerprint; server-owned state dir for bans/grants; interface TBD — likely chat commands first)* — plus the ban-evasion barrier menu to choose from: invite gating (exists), proof-of-work key minting, invite trees. (MAC-derived keys considered and rejected 2026-10-07: spoofable, low-entropy, breaks key backup.)
7. **GUI polish + identity trust** — dithered avatars, fingerprint display + first-seen key pinning, key backup prompt flow.
8. **Screenshare + direct-P2P opt-in** — capture per OS, VP8, per-participant direct mode.

**Standing follow-ups** (from the foundation final review): Linux CI with `-race` and a fuzz budget (race detector can't run on the Windows dev box — cgo disabled); decide auth-signature domain separation (`"hermec-auth-v1" || nonce`) before protocol v1 is frozen as a public spec.

## Decisions log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-10-06 | Hybrid connectivity: self-hosted lightweight server (signaling + relay), P2P as opt-in | Zero infra cost for the project; censorship resistance via many small hosts |
| 2026-10-06 | Relay by default; each **participant** (not host) controls direct vs. relayed | Privacy-first, power to users; also solves mobile CGNAT via the same mechanism |
| 2026-10-06 | MVP = voice + screenshare + text chat + abstraction service layer | Smallest slice proving the idea; screenshare implies most of the video pipeline anyway |
| 2026-10-06 | MVP client: native desktop, minimal GUI, single-binary, Win+Linux first | Lightweight TeamSpeak feel; terminal ruled out (can't render screenshare); browser/mobile later via WebRTC |
| 2026-10-06 | Stack: Go everywhere + Pion + Ebiten client + libopus (CGo) | Pion maturity beat Odin/Rust/Zig on ecosystem & AI-build velocity; Ebiten gives total control for the CRT aesthetic |
| 2026-10-06 | Service layer: roles + signed identities, role-gated server API, ephemeral media sessions; no client control API in MVP | Bots/agents are first-class identities; idle servers cost ~nothing; door (b) deferrable without loss |
| 2026-10-06 | Identity: Ed25519 keypairs, SSH-style file, per-device | No central issuer possible (zero infra); TeamSpeak/Nostr precedent; stolen device revokes one key only |
| 2026-10-06 | Security: access gating + transport encryption + documented host-trust; E2EE slots reserved; **no DMs in MVP** | Threats disentangled: gating & memory safety answer the real concerns; DM cut removes main E2EE pressure consistently |
