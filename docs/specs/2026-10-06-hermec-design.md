# Hermec — Design Specification

**Date:** 2026-10-06
**Status:** Draft for review
**Companion:** `PLAN.md` (discussion log and decisions table)

## 1. Overview

Hermec is a small, open-source, censorship-resilient communications app — "simple like TeamSpeak." It provides voice channels with screensharing and group text chat, served by lightweight servers that anyone can host. The project owns **zero infrastructure**: Hermec's resilience comes from many small, independently hosted servers rather than any central service.

Hermec is also designed as a reusable base ("boilerplate") for communications apps: a clean Go core behind a role-gated API, with theming and bot integration from day one, aiming to seed open standards above WebRTC.

**Motivating context:** Discord is blocked/censored in Brazil. One official endpoint is trivially blockable; a thousand small self-hosted servers on residential and rented IPs are practically not.

## 2. Goals and non-goals

### Goals
- Anyone can self-host a server on hardware from a Raspberry Pi to a gaming PC.
- Safe between strangers: each **participant** controls their own IP privacy.
- Portable and easy to install: one static-ish binary per OS, run it, done.
- Lightweight: low RAM when active, near-zero when idle.
- Small codebase, fast, open source, easy for contributors.
- Integration-first: bots and AI agents are first-class; theming without code.

### Non-goals (MVP)
- Private/direct messaging (explicitly out of first scope).
- Camera video conferencing (cheap to add later — screenshare builds the pipeline).
- Federation between servers.
- End-to-end encryption (slots reserved in the protocol; see §8).
- Mobile and browser clients (enabled later by the WebRTC wire protocol).
- Client-side local control API (door for MCP/agents to drive the running app; later).
- Device linking / cross-signing between a user's keys (later).
- Community-platform features (discovery, social graph, nitro-style extras).

## 3. Architecture

### 3.1 Topology

```
Client ---signaling/chat (TLS)---> Hermec Server (self-hosted)
Client A ---media (relayed, DEFAULT)---> Server relay ---media---> Client B
Client A ---media (direct P2P, per-participant OPT-IN)---> Client B
```

- **Hermec server:** one lightweight process handling rooms, presence, chat, signaling, and TURN-style media relay. Hostable by anyone; no project-owned components anywhere.
- **Relay by default:** participants' media routes through the server unless they opt into direct P2P. This (a) hides participant IPs from each other and (b) works through CGNAT/strict NATs (mobile networks), since both sides only dial outward.
- **Per-participant privacy:** a relay-choosing participant is relayed on *every* link, even to direct-mode peers. Mixed calls are normal: direct and relayed links coexist per pair.
- **Host consent:** the server shows its host the bandwidth cost of active relayed streams.
- Accepted trade-off: host bandwidth pays for participant privacy (~50–100 kbps per voice stream; substantially more for screenshare).

### 3.2 Ephemeral media sessions

The coordination server is always on and nearly free when idle. A voice channel's media pipeline (relay session) is created when the first participant enters and torn down when the last leaves. An idle Hermec server is a tiny idle process — Pi-friendly by construction.

### 3.3 Wire protocol

Media rides **WebRTC** (DTLS-SRTP, Opus audio, VP8/H.264 video for screenshare) — the open standard — keeping future browser/mobile clients and third-party implementations compatible. Hermec's own protocol is the layer above: signaling, rooms, roles, chat, and API messages. That layer is versioned and documented as a public spec (the "creating standards" deliverable).

## 4. Identity

- **Keypair identity, SSH-style:** on first run the client generates an **Ed25519** keypair stored in a single key file. The public key *is* the identity; the client proves it by signing a server challenge. No accounts, no registration, no central issuer.
- **Per-device keys:** each device (e.g., future mobile) generates its own key. Until cross-signing exists (post-MVP), a user's devices appear as distinct identities.
- **Names vs. keys:** display names are free-text and unverified; the UI shows a short key fingerprint beside names; servers pin first-seen keys to flag changes.
- **Key loss:** no recovery by design (no "forgot password"). First run prompts the user to back up the key file; docs state the trade-off plainly.
- Bots and AI agents hold keypairs exactly like humans.

## 5. Roles and the service layer

- **Roles are the foundation:** every connected identity (human or bot) holds roles granted in per-server configuration. Roles gate which views and endpoints an identity may call — including distinguishing bots from users.
- **The service layer is the server's API:** one role-gated protocol over the same connection clients use, addressing either the server runtime (e.g., list channels, presence) or a specific channel (e.g., send chat, join voice).
- **Integration doors (MVP):**
  1. **Go core packages** — the core ships as importable packages; the official GUI client is their first consumer. Headless bots import the same core.
  2. **Server API** — any identity with permitted roles (bots, AI agents) connects and interacts remotely.
  3. **Theme config** — a minimal declarative theme file (palette, font, shader toggles). Layout configuration comes later.
- **Deferred doors:** a localhost control API on the running client (for MCP/voice-agent control of one's own app) reusing the same role-gated protocol; an MCP server shipped as a separate project acting as an ordinary bot.

## 6. Clients

### 6.1 MVP client
- **Native desktop, minimal GUI** — one lightweight window: channel list, chat pane, video/screenshare area. No browser engine; low idle RAM.
- **Target OSes:** Windows + Linux first; macOS after (signing/notarization deferred).
- **Packaging:** one self-contained binary per OS.

### 6.2 Aesthetic
Old military field-radio / amber phosphor CRT: monochrome amber on black, bitmap fonts, scanline/glow shaders, and runtime Floyd–Steinberg dithering that renders any avatar as 1-bit amber pixel art. The look is part of Hermec's identity: rugged comms equipment, not a social app.

### 6.3 Future clients
Browser (WebRTC is native there) and mobile connect to unchanged servers. Mobile screenshare *capture* is platform-restricted; viewing is fine.

## 7. Technology stack

| Layer | Choice | Rationale |
|---|---|---|
| Language (everything) | **Go** | Pion ecosystem; trivial cross-compilation (incl. `linux/arm64` for Pi); static binaries; contributor pool; strong AI-assisted-development support |
| WebRTC | **Pion** | Battle-tested pure-Go WebRTC (LiveKit's foundation); used by server and client |
| Client rendering | **Ebiten** | Pure-Go 2D engine; full custom drawing for the CRT aesthetic; Kage shaders; video frames render as textures |
| Voice codec | **libopus via CGo** | The one C dependency; no mature pure-Go Opus encoder exists |
| Identity crypto | **Ed25519 (Go stdlib)** | Modern default; tiny keys; no external dependency |

Known caveat: CGo (opus; platform screen-capture APIs) means cross-compiling requires a C toolchain per target — a build-tooling chore, not an architecture problem. Ships as one file regardless.

## 8. Security model (MVP)

- **Scope cut that shapes it:** no private messaging. All MVP content is channel-scoped on a server the user chose to join.
- **Access control:** server entry gated by password/invite and/or public-key allowlist; roles gate everything inside.
- **Transport encryption everywhere:** DTLS-SRTP for media (WebRTC-mandatory), TLS for signaling and chat. Nothing plaintext on any wire. This fully answers the founding adversary (ISP/state observer).
- **Trust boundary:** the room **host is trusted** and documentation says so loudly: the host can see relayed media and chat, the same way a TeamSpeak host can. Choose hosts like you choose group admins.
- **E2EE: reserved, not implemented.** The protocol spec reserves key-exchange message types and per-frame encryption hooks so end-to-end encryption (MLS-style) can be added — first landing where DMs and stranger-scale rooms arrive — without a retrofit.
- **Implementation hardening:** memory-safe Go, deliberately small attack surface, rate limiting, and fuzzing of the protocol parser.
- **Known exposure:** metadata (who connects, when, to which channels) is visible to the host even under future E2EE.

## 9. Testing

- Core protocol and room-state logic: standard Go unit tests (the core packages are pure logic, designed testable).
- Protocol parser: fuzz tests (`go test -fuzz`).
- Media paths: integration tests spinning up a server + two headless clients (the Go core's headless mode is the bot door *and* the test harness) exchanging voice/chat over localhost.
- Relay-vs-direct policies and NAT behavior: integration-tested with simulated topologies where feasible; manual test matrix for real NATs documented.
- GUI: kept thin over the core; smoke tests plus manual checklist for MVP.

## 10. Build process

After this spec is approved: implementation plan via the superpowers **writing-plans** skill, then MVP build in sessions using Sonnet/Haiku subagents (learner-directed, vibe-wise learning loop stays active).

## 11. Open items deliberately deferred to planning

- Exact signaling/API message schema (shape of the versioned protocol spec).
- Screen-capture library choice per OS (Windows DXGI / Linux PipeWire-X11 paths).
- Repo layout for core/server/client packages.
- Theme file format details.
