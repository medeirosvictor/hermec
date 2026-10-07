# Hermec Voice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Working voice calls: Discord-style voice channels with open-mic audio relayed through the self-hosted server (SFU), mute with visible state, occupancy shown before joining, and server rate limiting — on both the headless client and the GUI.

**Architecture:** The existing WebSocket protocol gains voice membership + WebRTC negotiation messages; the server becomes a Pion WebRTC peer per voice participant and forwards Opus RTP between them (SFU, ephemeral per-channel sessions — spec §3.1/§3.2). The client publishes one Opus track and plays back received tracks. Audio hardware is isolated behind `AudioSource`/`AudioSink` interfaces so everything below the speaker/mic is testable without devices. Relay-only: direct P2P opt-in is a later plan.

**Tech Stack:** Pion (`pion/webrtc/v4`), `hraban/opus` (CGo libopus), `gen2brain/malgo` (CGo miniaudio, capture), `ebitengine/oto/v3` (pure-Go playback). CGo required → MinGW toolchain (Task 0).

**Spec:** `docs/specs/2026-10-06-hermec-design.md` §3.1–3.3 (relay, ephemeral sessions, WebRTC/Opus), §5 (roles gate actions), §8 (rate limiting). Design decisions of 2026-10-07 recorded in PLAN.md §roadmap item 3.

## Global Constraints

- New dependencies: pion/webrtc/v4 (+ transitive pion/*), hraban/opus, gen2brain/malgo, ebitengine/oto/v3 — nothing else.
- CGo becomes required for `cmd/hermec` and the `audio` package only; `server`, `core/*`, `client` (with injected source/sink) must still build and test with `CGO_ENABLED=0` (CI cross-build must stay green). Guard hardware audio behind the `audio` package; never import it from `client` or `server`.
- Protocol stays envelope v1; new message types are additive and documented; an old server answers voice messages with `error bad_request` → client shows "server has no voice support".
- Voice channels come from new TOML key `voice_channels` (array, default `[]`); existing `channels` stays text-only. `AuthOK.Channels []string` is REPLACED by `AuthOK.Channels []ChannelInfo{Name, Type string}` (types "text"/"voice") — pre-1.0 breaking change, protocol.md updated, client/GUI updated in the same tasks that touch them.
- Audio format: Opus, 48kHz mono, 20ms frames, target bitrate 32kbps.
- One voice channel per identity-connection at a time; `voice_join` while in a call moves the participant (implicit leave + state broadcasts for both channels).
- Mute is signaling + the client stops sending frames; muted participants stay subscribed (they hear everything).
- Rate limit (spec §8): per-connection token bucket on inbound ws messages — 30 msgs/sec sustained, burst 60; violation → `error{code:"rate_limited"}` then close. Media/RTP is NOT counted (it rides the PeerConnection, not the ws).
- gofmt/vet clean; TDD wherever hardware isn't involved; full `go test -count=1 ./...` before each commit; after Task 0, `go test -race -count=1 ./server/... ./client/... ./integration/...` must also pass locally. Commits end with trailer `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

## Review Focus

1. A participant whose TCP drops mid-call (no voice_leave) must be removed from the session, its PeerConnection closed, remaining members re-broadcast, and the ephemeral session destroyed when last-out — Task 3 integration test (force-close).
2. voice_join to a text channel, a nonexistent channel, or without `join_channel` permission → proper error codes, no session side effects — Task 2 tests.
3. The SFU must not leak forwarding goroutines when a reader or writer PC dies mid-stream — Task 3 (goroutine-count assertion around session create/destroy in the integration test).
4. A client that joins voice, gets no rtc_answer (server died mid-negotiation), then reconnects must not deadlock JoinVoice — ctx timeout path tested in Task 4.
5. Rate limiter must not starve legitimate traffic: a client receiving a 200-message chat flood while sending presence/pings stays connected (outbound is uncounted; only inbound counted) — Task 7 test proves a normal client under load survives while a spammer is cut.

---

### Task 0: CGo toolchain

**Files:** none in-repo (system setup + verification); Modify: `README.md` (build prerequisites)

- [ ] **Step 1: Install MinGW-w64** — `winget install --id MartinStorsjo.LLVM-MinGW --silent` (or `BrechtSanders.WinLibs.POSIX.UCRT` if unavailable); ensure `gcc`/`cc` on PATH for Go (new shells).
- [ ] **Step 2: Verify** — `go env CGO_ENABLED` with CC found; `CGO_ENABLED=1 go build ./...` succeeds; `go test -race -count=1 ./core/... ./server/...` runs and PASSES (first-ever local race run).
- [ ] **Step 3: README prerequisites note** — building `cmd/hermec` with voice needs a C compiler on Windows (one line, with the winget command); server/headless stay pure-Go.
- [ ] **Step 4: Commit** — `docs: cgo build prerequisites for voice`

### Task 1: Voice protocol (`core/proto`)

**Files:** Modify: `core/proto/proto.go`, `core/proto/proto_test.go`, `core/proto/fuzz_test.go`, `docs/protocol.md`

**Interfaces — Produces (exact):**
- Type constants: `TypeVoiceJoin="voice_join"`, `TypeVoiceLeave="voice_leave"`, `TypeVoiceState="voice_state"`, `TypeVoiceMute="voice_mute"`, `TypeRTCOffer="rtc_offer"`, `TypeRTCAnswer="rtc_answer"`, `TypeRTCCandidate="rtc_candidate"`
- Payloads: `VoiceJoin{Channel string}`, `VoiceLeave{}`, `VoiceMute{Muted bool}`, `VoiceMember{Fingerprint, Name string; Muted bool}`, `VoiceState{Channel string; Members []VoiceMember}`, `RTCOffer{SDP string}`, `RTCAnswer{SDP string}`, `RTCCandidate{Candidate string}`
- `ChannelInfo{Name, Type string}`; `AuthOK.Channels` becomes `[]ChannelInfo`; error code `"rate_limited"` added to the documented codes.
- protocol.md: voice section (join/leave/state/mute semantics, one-call-at-a-time, negotiation flow server-as-SFU with server-initiated renegotiation on topology change), the AuthOK change, rate-limit behavior, and an honest "no acoustic echo cancellation — headsets recommended" note.

- [ ] **Step 1: Failing tests** — round-trip for each new payload; AuthOK with typed channels; fuzz seeds extended with one of each new message.
- [ ] **Step 2: RED → implement → GREEN; 30s fuzz, no crashers**
- [ ] **Step 3: Update protocol.md**
- [ ] **Step 4: Commit** — `feat: voice signaling protocol and typed channels`

### Task 2: Voice membership on the server (`server`) — no media yet

**Files:** Modify: `server/config.go`, `server/server.go`, `server/conn.go`, `server/channels.go`; Create: `server/voice.go`; Test: `server/voice_test.go` (+config test additions)

**Interfaces:**
- Consumes: Task 1 messages; existing roles (`join_channel` gates voice_join), channel registry, broadcast machinery.
- Produces:
  - Config: `voice_channels` TOML key (array, default empty); `LoadConfig` validates no name collision with `channels`; `AuthOK` now sends typed ChannelInfo for both kinds; `channelType(name)` internal lookup.
  - `voice_join`: requires existing voice channel (`bad_request` for unknown/text) + `join_channel` perm (`forbidden`); success → participant added, implicit leave of any previous voice channel, `voice_state` broadcast to ALL connected clients (occupancy is visible before joining, like presence) for every affected channel.
  - `voice_leave`, disconnect, and keepalive-close all remove the participant and broadcast; `voice_mute` updates the member's Muted and re-broadcasts that channel's voice_state.
  - voice_state broadcasts go to every authenticated connection (not just channel members) — the pane shows occupancy server-wide.

- [ ] **Step 1: Failing tests** — join happy path (two conns see voice_state with both members); join unknown/text channel → bad_request; join without perm → forbidden; move between voice channels (both channels' states broadcast); mute flag round-trip; disconnect removes + broadcasts.
- [ ] **Step 2: RED → implement → GREEN (full suite + -race now available)**
- [ ] **Step 3: Commit** — `feat: voice channel membership, occupancy, and mute signaling`

### Task 3: SFU media relay (`server`)

**Files:** Modify: `server/voice.go`, `server/conn.go` (route rtc_* messages); Create: `server/sfu.go`; Test: `integration/voice_test.go` (uses Task 4's client — write the test in Task 4 if sequencing demands; otherwise a package-internal pion client here)

**Interfaces:**
- Consumes: pion/webrtc/v4; Task 2 membership events as the lifecycle driver.
- Produces:
  - Ephemeral `voiceSession` per occupied voice channel: created on first join, destroyed (all PCs closed, goroutines joined) on last leave — spec §3.2.
  - Per participant: one server-side PeerConnection negotiated over ws (client sends rtc_offer after voice_join's ack... exact flow: server sends rtc_offer? DECISION: the CLIENT creates the PC and sends `rtc_offer` immediately after receiving its first voice_state as a member; server answers with `rtc_answer`; ICE candidates flow both ways via rtc_candidate; on later topology changes the SERVER sends rtc_offer (renegotiation) and the client answers).
  - Forwarding: each publisher's Opus track is fanned out via `TrackLocalStaticRTP` added to every other participant's PC; one copy goroutine per publisher track, exiting on session destroy or publisher death; a dead subscriber PC is pruned without killing the publisher loop.
  - Host visibility (spec §3.1): the server logs, once per minute per active session, participant count and approximate relayed kbps.
  - ICE config: host candidates only by default; optional `public_ip` TOML key sets NAT 1:1 mapping for internet-facing hosts (documented in example config).

- [ ] **Step 1: Implement sfu.go + negotiation routing (this task is design-heavy; tests land as the Task 4 integration suite — acceptable because no pure unit seam exists above pion)**
- [ ] **Step 2: Build/vet/full suite + -race green; no goroutine-leak regression in existing tests**
- [ ] **Step 3: Commit** — `feat: SFU media relay with ephemeral voice sessions`

### Task 4: Headless client voice (`client`) + end-to-end integration tests

**Files:** Modify: `client/client.go`; Create: `client/voice.go`; Test: `client/voice_test.go`, `integration/voice_test.go`

**Interfaces:**
- Produces:
  - `type AudioSource interface { ReadOpusFrame() ([]byte, error) }` (blocking, 20ms frames; io.EOF ends the stream)
  - `type AudioSink interface { WriteOpusFrame(fromFP string, frame []byte) }` (called per received frame; mixing is the sink's concern)
  - `(c *Client) JoinVoice(ctx, channel string, src AudioSource, sink AudioSink) error` — voice_join, waits for member voice_state, then negotiates the PC (rtc_offer out, rtc_answer in, candidates); ctx timeout tears down cleanly (Review Focus 4)
  - `(c *Client) LeaveVoice(ctx) error`; `(c *Client) SetMuted(m bool) error` (signaling + pauses src reads); `(c *Client) VoiceChannel() string`
  - `Event` gains `Voice *proto.VoiceState` (still exactly-one-field-non-nil)
  - `Channels()` returns `[]proto.ChannelInfo`
- Integration tests (the heart of the plan): server + 2 headless clients with synthetic sources (deterministic opus-silence frames with per-client marker bytes) and recording sinks — assert A's frames arrive at B's sink (and not at A's), mute stops A's frames within a bound, B leaving destroys nothing for C... (3-client case), force-closed C is pruned (Review Focus 1), goroutine counts return to baseline after last leave (Review Focus 3).

- [ ] **Step 1: Failing client unit tests (JoinVoice state machine against a stub ws server; ctx-timeout path) — RED**
- [ ] **Step 2: Implement client/voice.go — GREEN**
- [ ] **Step 3: Failing integration tests — RED against current server if Task 3 stubs anything; then GREEN**
- [ ] **Step 4: Full suite + -race; commit** — `feat: headless voice client with SFU integration tests`

### Task 5: Hardware audio (`audio` package, CGo)

**Files:** Create: `audio/capture.go` (malgo + opus encode → AudioSource), `audio/playback.go` (opus decode + int16 mixer → oto → AudioSink), `audio/levels.go` (RMS meters); Test: `audio/mixer_test.go` (pure mixing/RMS math only)

**Interfaces:**
- Produces: `Capture() (client.AudioSource, *Meter, error)`; `Playback() (client.AudioSink, *Meters, error)`; `Meter.Level() float64` (own mic RMS 0..1), `Meters.Level(fp string) float64` (per-speaker); mixer clips with saturation; decoder tolerates lost frames (opus PLC).
- Only this package imports malgo/oto/opus. 48kHz mono 20ms, bitrate 32kbps.

- [ ] **Step 1: TDD the pure parts (mixer saturation, RMS decay)**
- [ ] **Step 2: Implement capture/playback; a `cmd/hermec-audiocheck` throwaway is NOT wanted — verification is Task 6's manual call**
- [ ] **Step 3: Full suite (audio tests must skip gracefully when no devices: guard with t.Skip on device-open failure); commit** — `feat: hardware audio capture and playback`

### Task 6: Voice in the GUI (`ui`)

**Files:** Modify: `ui/main_scene.go`, `ui/app.go`, `ui/state/state.go` (+tests); Create: `ui/callbar.go`

**Interfaces:**
- state: `State.Voice map[string][]proto.VoiceMember` (reducer applies VoiceState events; tests); `State.InCall string` maintained from events + own actions.
- Channel pane: voice channels listed under a `── voice ──` divider with a speaker glyph; occupants (name + mute icon, speaking glow via Meters) rendered indented under each voice channel — visible WITHOUT joining; click a voice channel = JoinVoice with hardware source/sink (leave previous; clicking your current voice channel = leave — toggle semantics); text channel behavior unchanged (voice join does NOT change the focused text channel).
- Call bar (bottom, above input): channel name, elapsed time, mute button + icon (M key toggles; icon turns dim/bright; own mute mirrored to others via SetMuted), leave button; own-mic RMS as a tiny level meter.
- Old-server handling: voice_join answered bad_request → status "server has no voice support".
- Manual checklist (the real verification): two instances on one machine (HEADPHONES — no AEC), one `hermec-server` with a voice channel; join from both, hear each other, mute propagates icons both ways, speaking glow tracks, leave/rejoin, kill one instance mid-call → other sees it vanish; `-local` config gains a default voice channel ("voice").

- [ ] **Step 1: TDD state reducer additions; implement pane + callbar + wiring**
- [ ] **Step 2: Build/vet/full suite + -race (non-audio packages); brief -local smoke**
- [ ] **Step 3: Commit** — `feat: voice channels, call bar, and speaking indicators in the GUI`

### Task 7: Rate limiting (`server`)

**Files:** Modify: `server/conn.go`, `server/config.go`; Create: `server/ratelimit.go`; Test: `server/ratelimit_test.go`

**Interfaces:**
- Produces: token bucket per connection (30 msgs/sec, burst 60; TOML `msg_rate`/`msg_burst` overrides, 0 disables); applies to inbound ws messages post-auth AND pre-auth (pre-auth bucket smaller: 5/sec burst 10); violation → `error{code:"rate_limited"}` + close; pings/pongs (control frames) uncounted.
- Tests: spammer cut at the documented threshold; normal client under inbound flood survives (Review Focus 5); pre-auth flood cut.

- [ ] **Step 1: TDD → implement → GREEN (+ -race)**
- [ ] **Step 2: Commit** — `feat: per-connection message rate limiting`

### Task 8: Docs & examples

**Files:** Modify: `README.md`, `example.server.toml`, `docs/protocol.md` (final consistency pass)

- [ ] **Step 1: README — voice quickstart (server voice_channels config, M key, call bar), the no-AEC/headphones note, cgo prerequisite cross-link; example.server.toml gains `voice_channels = ["voice"]` and commented `public_ip`/`msg_rate`; verify example still loads via the existing test.**
- [ ] **Step 2: Full suite + -race + cross-builds (`CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./server/... ./cmd/hermec-server` must stay green — the Pi server never needs cgo).**
- [ ] **Step 3: Commit** — `docs: voice setup, limits, and examples`
