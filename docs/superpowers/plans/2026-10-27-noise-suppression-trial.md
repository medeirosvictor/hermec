# Microphone Noise Suppression Trial Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** Not started. Awaiting approval; it follows the second-wave
regular-use features in [PLAN.md](../../../PLAN.md) (call quality).

**Goal:** Evaluate a freely redistributable local denoiser and, if useful, expose
it behind a default-off experimental toggle usable during calls.

**Architecture:** Trial the pure-Go RNNoise port first, then integrate a small
PCM processor on the capture reader/encoder path. UI changes publish desired
state; the reader owns initialization, filtering and resets. A processing failure
bypasses filtering and preserves the call.

**Tech Stack:** Existing 48 kHz mono/20 ms capture and gopus encoder;
candidate `github.com/VoiceBlender/rnnoise-go v0.3.0`. Version resolved with
`go list -m -json github.com/VoiceBlender/rnnoise-go@latest` on 2026-10-27;
the result requires Go 1.26 and is compatible with HERMEC's Go 1.27 floor.

**Research:** [noise-suppression candidates and evaluation gate](../../research/hosting-and-noise-suppression.md).

## Global Constraints

- Default off; switching works during a call without rejoining.
- Process microphone PCM locally before Opus, never in the device callback.
- No cloud processing, raw microphone logging or external runtime installation.
- Keep mute semantics, mic gain, bounded capture backlog and device teardown behavior.
- Do not use Victor's real config or launch an agent GUI for the trial.
- Initialization/failure disables filtering visibly and preserves the call.
- Noise suppression does not implement acoustic echo cancellation.
- Verify code and model redistribution terms for the exact bundled revision.

## Review Focus

1. Cold-start silence stays silent, and subframe processing does not repeat,
   omit or reorder input samples.
2. Off/on during speech or after a mute gap must not replay stale filter state.
3. An initialization/processing error must not terminate microphone delivery.
4. Rapid setting changes must not share a denoiser across reader goroutines.
5. A saved preference from a previous call must apply to a newly opened source.

Tests for these belong to Tasks 1-3 below. Listening quality and real hardware
performance remain manual checks, reported separately from automated tests.

## Files and interfaces

- `tools/noise-probe/`: isolated candidate/benchmark module, initially outside
  the root dependency graph; own `go.mod`, `main.go`, tests and license notices.
- `audio/noise.go`, `audio/noise_test.go`: processor and immutable status snapshots.
- `audio/capture.go`: apply processing to 960-sample frames before encoding.
- `ui/state/settings.go`, its tests, `ui/settings.go`, `ui/callbar.go` and UI tests:
  saved default-off preference, toggle and failure reporting.
- `docs/research/noise-suppression-trial.md`: exact measurements and listening
  checklist; distribution notices accompany any shipped model/library.

Proposed audio control interface (Task 2 produces, Task 3 consumes):

```go
type NoiseStatus struct {
    Wanted, Active bool
    FailureID uint64
    Err error
}
type NoiseControl interface {
    SetNoiseSuppression(bool)
    NoiseStatus() NoiseStatus
}
```

SetNoiseSuppression publishes state only; NoiseStatus returns a synchronized
copy. Neither initializes or calls the denoiser on the UI thread. The capture
reader is the only owner of mutable denoiser state.

### Task 1: Validate the pinned pure-Go candidate independently

**Files:** Create the nested `tools/noise-probe/` module and
`docs/research/noise-suppression-trial.md`.

**Interfaces:** A 48 kHz mono `Process20ms(dst, src []int16) error` trial adapter
whose input/output lengths are exactly 960. Use two candidate-sized subframes
per HERMEC frame, deriving their size from `FrameSize()` and rejecting mismatch.

- [ ] Pin v0.3.0 in the nested module. Inspect that tag's LICENSE, NOTICE,
  model/LICENSE.weights and model hash records. Record the module checksum and
  model provenance; do not run upstream build scripts without inspection.
- [ ] Write `TestProcess20msRejectsWrongFrameSizes` and
  `TestProcess20msSubframeOrder` with a recording processor: use distinct input
  ramps for samples 0-479 and 480-959 and independently expected output markers
  to catch missing/repeated/reversed halves. Observe RED before implementation.
  Add an actual-candidate silence regression, not a mock-only denoising claim.
- [ ] Implement the adapter using the candidate's `New`, `FrameSize` and
  `ProcessInt16` API. Keep the model embedded for a standalone binary and use
  upstream default processing options initially.
- [ ] Run adapter tests, the pinned candidate's own tests and a benchmark with
  `-benchmem`, including impulse/full-scale inputs. Integer output being within
  int16 bounds is a property of its type, not evidence against distortion; use
  upstream reference checks and the listening checklist for quality. Record
  process time per 20 ms, allocations, initialization cost and compiled size
  against an otherwise equivalent bypass binary. Do not equate upstream
  benchmarks with HERMEC measurements or claim listening quality from noise
  amplitude alone.
- [ ] Build supported OS/architecture targets where available. Mark native
  performance/listening not run where no device exists. Record algorithmic
  delay separately from application buffering; the upstream CLI compensates
  delay and is not evidence that a live call has zero added delay.
- [ ] Commit trial and evidence. Review the measured result before promoting
  the dependency into the app; if unsuitable, retain the report and evaluate
  another candidate in a separate trial.

### Task 2: Add capture-side processing and safe live control

**Files:** `audio/noise.go`, `audio/noise_test.go`, `audio/capture.go`, capture
tests; root `go.mod`/`go.sum` only after the candidate trial is accepted.

**Interfaces:** Produce NoiseControl/NoiseStatus above. Preserve the current
`Capture(deviceName string) (client.AudioSource, *Meter, error)` signature; its
source additionally implements NoiseControl. Default Wanted/Active are false.

- [ ] Write a reader-path regression proving bypass preserves pre-encoder PCM,
  enable processes exactly two subframes, and disable immediately bypasses on
  the next reader frame. Use an injected processor factory below the real
  capture logic; test behavior, not mock presence.
- [ ] Add `TestNoiseFailureBypassesWithoutEndingCapture` for constructor and
  processing failures, checking that frames remain available, Active becomes
  false, and the cause is retained once with a new FailureID.
- [ ] Add `TestNoiseToggleAndMuteGapResetState` and a race test for rapid desired
  changes. A filter is never used concurrently; after a pause longer than the
  existing capture pauseGap, reset/recreate it before processing the next frame.
- [ ] Observe RED, then implement lazy reader-owned processing before EncodeInt16.
  Keep the hardware callback and its ring behavior unchanged. Initialization
  and processing failure publish a snapshot, bypass the current frame and
  clear Wanted; UI handles user notice/logging. Avoid repeated failure retries.
- [ ] Run focused audio tests and race tests, then root suite; commit the
  integration and exact dependency pin. Preserve required notices in any
  distribution containing code or model weights.

### Task 3: Settings toggle and real-call evaluation

**Files:** `ui/state/settings.go`, settings tests, `ui/settings.go`,
`ui/callbar.go`, focused UI tests; trial report and release notices/packaging.

**Interfaces:** Add `NoiseSuppression bool` with TOML key
`noise_suppression,omitempty`. UI type-asserts the current source as
audio.NoiseControl; a new source receives the saved desired value.

- [ ] Test absent/false settings remain off, true round-trips, a toggle updates
  an existing call without join/leave, and a new call inherits the preference.
  Existing short-window settings drawing/hit tests include the new row.
- [ ] Test one processing FailureID produces one friendly notice and one raw
  log entry for the current call. Stale snapshots from a retired source cannot
  alter a replacement call. Persist off after a failure to avoid repeated
  initialization attempts on the next call.
- [ ] Observe RED, implement the row `noise suppression (experimental)` and
  desired-state update, and poll status on the existing UI update path.
  Processing and model loading never run on the UI goroutine.
- [ ] Run focused UI/state/audio tests, root vet/race suite, packaging checks
  and `git diff --check`. Include required code/model notices in release
  archives; do not publish a release as part of this task.
- [ ] Provide a manual A/B checklist: speech in quiet, fan, keyboard and
  competing speech; clipping/voice coloration; delay/CPU; toggle during a
  call, mute/unmute, leave/rejoin, device change and host takeover. Use test
  configuration; no automatic recording. Report actual listening outcomes
  separately and retain the experimental/default-off label.
- [ ] Commit and update roadmap/handoff with measured behavior and open
  listening checks. This does not close the echo-cancellation work item.

## Sources and self-review

- [Pinned candidate README/API](https://github.com/VoiceBlender/rnnoise-go/tree/v0.3.0).
- [Pinned model provenance](https://github.com/VoiceBlender/rnnoise-go/blob/v0.3.0/model/LICENSE.weights).
- [Pinned code license](https://github.com/VoiceBlender/rnnoise-go/blob/v0.3.0/LICENSE).

Task 1 settles redistribution/runtime suitability and costs before app changes.
Task 2 owns processing, resets, bounded failure and concurrency. Task 3 owns
persisted defaults, mid-call controls, stale-result handling and user feedback.
All five review-focus cases have an owning test step. Manual listening and
platform performance not available locally remain explicit open checks.
