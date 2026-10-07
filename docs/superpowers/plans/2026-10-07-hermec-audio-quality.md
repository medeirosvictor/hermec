# Hermec Audio Quality & Devices Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the two live-call defects Victor heard on the first Mac↔desktop call — chopped speech (capture over-discarding) and a rhythmic buzzsaw (playback underrun cycling) — and add input/output device selection. Plus one small UI tweak: the chat input sits directly under the chat pane. Victor's repeat listening test is the acceptance gate.

**Architecture:** Capture keeps a slightly deeper ring and only discards stale audio after a genuine pause (never during continuous flow). Playback moves from oto to malgo (one backend for both directions — and malgo selects devices, which oto cannot), with a real per-sender jitter buffer: target depth ~3 frames, Opus PLC conceals single gaps (budget-capped) instead of the unprime→silence→re-prime cycle. Device enumeration surfaces as settings rows persisted in settings.toml.

**Spec:** Victor's live-call report 2026-10-07 (chop + buzzsaw + device pickers; input-under-chat tweak); Task 5 audio review's deferred "Pull-size stutter risk" minor (this is that item come due); docs/footprint.md method for the perf re-check.

## Global Constraints

- Dependency change: REMOVE ebitengine/oto (playback moves to gen2brain/malgo, already a dependency); `go mod tidy`. No new dependencies.
- Audio format unchanged: Opus 48kHz mono 20ms 32kbps. client.AudioSource/AudioSink contracts unchanged (device names are audio-package constructor params).
- The mute contract stays honored: NO backlog burst on unmute (the pause-aware staleness must still discard a muted-period backlog — that's what "after a genuine pause" means), pinned by the existing + updated tests.
- Settings keys: `input_device`, `output_device` (strings, empty = system default); unknown device name at startup → fall back to default + a notice, never crash.
- gofmt/vet clean; TDD for all pure logic (ring/staleness decisions, jitter-buffer depth/PLC budget state machine — extract both as device-free testable units); full `go test -count=1 ./...` + `go test -race -count=1 ./audio/... ./ui/...` per commit (PATH /c/msys64/ucrt64/bin, CGO_ENABLED=1). Commit trailer: `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.
- ACCEPTANCE GATE: after Task 3, Victor repeats the Mac↔desktop call. The plan is not done until he reports clean audio.

## Review Focus

1. Steady-state flow must never drop frames: producer and consumer at nominal 20ms cadence with ±10ms jitter → zero discards over a simulated minute — Task 1 table test with injected timestamps.
2. The unmute case must still not burst: 5s of muted-period frames followed by resume → at most 1 stale frame plays — Task 1 test.
3. A single lost/late frame must produce PLC concealment, not an underrun cycle: jitter-buffer state machine test — gap of 1 → PLC frame inserted, depth recovers, no silence period — Task 2.
4. PLC budget bounded: a 500ms outage → at most N (2-3) PLC frames then silence until real audio resumes, then clean recovery without a burst — Task 2 test.
5. Device selection failure modes: configured device absent (unplugged USB mic) → default + notice at join, not crash; device names with unicode survive settings round-trip — Task 3 tests.

---

### Task 1: Capture — pause-aware staleness

**Files:** Modify: `audio/capture.go`, `audio/opus_test.go` (or the capture test file)

**Interfaces — Produces:**
- Ring depth 4 frames. Staleness: on ReadOpusFrame, discard-to-newest ONLY when the time since the previous Read exceeds `pauseGap` (100ms) — i.e., resuming after mute/stall; during continuous reading (gap < pauseGap), frames are returned strictly in order, none discarded.
- The decision logic (given: now, lastReadAt, queue timestamps → which frames to drop) extracted as a pure function with table tests (injected clock, Review Focus 1 & 2 scenarios).

- [ ] **Step 1: Failing tests (RF1 steady-jitter zero-drop over simulated minute; RF2 unmute no-burst; existing tests updated to the new semantics)**
- [ ] **Step 2: RED → implement → GREEN; -race ./audio/...**
- [ ] **Step 3: Commit** — `fix: capture ring tolerates steady jitter, discards only after pauses`

### Task 2: Playback — malgo + jitter buffer + PLC

**Files:** Modify: `audio/playback.go`, `audio/levels.go` (if touched), tests; `go.mod` (drop oto)

**Interfaces — Produces:**
- Playback via malgo (device-selectable; constructor takes an optional device name — wiring lands in Task 3; default device this task).
- Per-sender jitter buffer replacing prime/unprime: target depth 3 frames (60ms); playback callback pulls 20ms at a time (malgo period = 20ms — match the sip size, killing the gulp/sip mismatch); on empty-with-recent-activity → insert ONE PLC frame (decoder.PLC via WriteOpusFrame-nil path already supported), budget 3 consecutive, then silence; real frame arrival resets budget; initial fill: start playing at depth 2.
- The depth/PLC/budget state machine extracted pure + table-tested (RF3, RF4).
- Meters unchanged. Idle-sender cleanup semantics preserved (existing tests keep passing, adjusted to the new engine).

- [ ] **Step 1: Failing tests (RF3 single-gap PLC no-silence; RF4 outage budget + clean recovery; port existing mixer/cleanup tests)**
- [ ] **Step 2: RED → implement → GREEN; -race; `go mod tidy` (oto gone); full suite**
- [ ] **Step 3: A cautious 2s local capture+playback loopback self-test if devices exist (t.Skip otherwise); note the result**
- [ ] **Step 4: Commit** — `fix: malgo playback with jitter buffer and PLC; drop oto`

### Task 3: Device selection + input-under-chat tweak + acceptance

**Files:** Modify: `audio/` (enumeration), `ui/state/settings.go` (+tests), `ui/settings.go`, `ui/callbar.go`/`ui/app.go` (wiring), `ui/main_scene.go` (input position), README (device rows + the troubleshooting "deny prompts" refinement: the host's server exe must be Allowed)

**Interfaces — Produces:**
- `audio.ListDevices() (inputs, outputs []string, err error)` (malgo enumeration); `Capture(deviceName string)` / `Playback(deviceName string)` ("" = default; absent name → default + returned notice string).
- Settings: `input_device`/`output_device` + two settings-scene rows cycling `default → device1 → device2 …` (enumerate on scene entry), persisting immediately; voice join wiring passes them.
- UI tweak (Victor): the chat input line renders directly under the chat pane (right column width), not as a full-window-width bottom bar; call bar placement adjusts accordingly; hit-testing and geometry stay single-source (the standing lesson).
- Measure: quick CPU sample (footprint method) to confirm no regression from the malgo swap.

- [ ] **Step 1: TDD settings/enumeration-fallback logic; implement; build/vet/full suite + -race ./ui/... ./audio/...**
- [ ] **Step 2: Local smoke (-local, settings page shows device rows, input sits under chat)**
- [ ] **Step 3: Commit** — `feat: audio device selection; input field under chat pane`
- [ ] **Step 4: OWNER ACCEPTANCE: Victor repeats the Mac↔desktop call — clean speech, no buzzsaw, device pickers work. Plan closes only on his report.