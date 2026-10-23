# Call View and Independent Media Lanes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Discord-style call view in which up to ten webcams, one selected screen share, and voice operate simultaneously with independent lifecycles.

**Architecture:** Keep one WebRTC PeerConnection per client and give audio, camera, and screen distinct logical lanes. Modern peers negotiate fixed camera/screen publish tracks plus ten camera receive slots and one screen receive slot; the SFU assigns sources to stable slots, while a compatibility adapter preserves the existing single-screen protocol for old peers.

**Tech Stack:** Go 1.27, Pion WebRTC v4, Pion MediaDevices v0.10.0 capture-only adapter, govpx VP8, Ebiten v2, existing actor-based SFU.

**Spec:** `docs/superpowers/specs/2026-10-23-call-view-media-lanes-design.md`

## Global Constraints

- Keep one WebRTC PeerConnection per participant; do not create per-lane or per-source connections.
- Support at most ten simultaneously active cameras per voice room and one selected incoming screen per viewer.
- Use fixed camera encoding parameters: 640x360, 15 fps, 300 kbps.
- Preserve current screen-share presets and legacy `share_start`, `share_stop`, `watch`, and `video_source` behavior.
- Negotiate `media_lanes_v1` as the intersection of client-offered and server-supported capabilities before using modern media messages or track layout.
- Implement real camera capture on Windows; macOS and Linux must compile and report unsupported.
- Use MediaDevices only for device discovery/raw frames; keep govpx as the VP8 encoder and add no system codec dependency.
- Never open a camera except after an explicit user action, and never persist an active-camera state across app launches.
- Never write captured frames to files, history, or logs.
- Do not touch the user's real HERMEC configuration in tests.
- CI tests use deterministic fake sources and loopback media; do not require hardware, introduce fixed sleeps, or simulate slow external networks.
- Each task follows red-green-refactor and ends with its listed focused tests passing before commit.

## Review Focus

- A peer sends extra, misordered, or undeclared video tracks: Task 4 tests that the SFU discards them and still carries voice.
- Eleven users enable cameras concurrently: Task 2 tests that the eleventh receives `media_full`, no existing camera is displaced, and a retry succeeds after a slot frees.
- Camera is toggled or stopped while a failover is adopting the call: Task 8 tests that generations preserve the latest user choice and never resurrect stopped capture.
- A camera disappears or its reader fails: Task 7 and Task 11 test one clean shutdown, avatar fallback, a visible notice, and no effect on voice/screen.
- A slot is reassigned while old RTP is still arriving: Task 5 and Task 6 test sequence boundaries and source generations so stale frames cannot appear under the new participant.

---

### Task 1: Media protocol and capability negotiation

**Files:**
- Create: `core/proto/media.go`
- Create: `core/proto/media_test.go`
- Modify: `core/proto/proto.go`
- Modify: `core/proto/fuzz_test.go`
- Modify: `core/hshake/hshake.go`
- Modify: `core/hshake/hshake_test.go`
- Modify: `server/conn.go`
- Modify: `server/auth_test.go`
- Modify: `client/client.go`
- Modify: `client/client_test.go`

**Interfaces:**
- Consumes: existing `proto.Encode`, `proto.Decode`, `AuthOK`, and `ErrorMsg`.
- Produces: `proto.MediaKind`, `MediaCamera`, `MediaScreen`, `CapabilityMediaLanesV1`, `MaxCameraSlots`, `MediaStart`, `MediaStop`, `MediaSubscribe`, `MediaSource`, `CodeMediaFull`, `CodeNotPublishing`, negotiated `Auth.Capabilities`/`AuthOK.Capabilities`, and `(*client.Client).SupportsMediaLanes() bool`.

- [ ] **Step 1: Write failing protocol and capability tests**

```go
func TestMediaMessagesRoundTrip(t *testing.T) {
    // Assert kind, fingerprint, slot=9, sequence, and generation survive encode/decode.
}

func TestAuthOKMediaCapability(t *testing.T) {
    // Assert a new client offers media_lanes_v1, a new server accepts it, and the
    // client reports support. Assert either side omitting it yields no support.
}

func TestVoiceMemberMediaOmitEmpty(t *testing.T) {
    // Assert old payloads decode, empty media is omitted, and camera+screen round-trip.
}
```

- [ ] **Step 2: Run focused tests and verify they fail for missing media types**

Run: `go test ./core/proto ./server ./client -run 'Test(Media|AuthOKMedia|VoiceMemberMedia)'`

Expected: FAIL because the constants, structs, capability field, and client accessor do not exist.

- [ ] **Step 3: Add the exact wire contracts**

In `core/proto/media.go`, define:

```go
type MediaKind string
const (
    MediaCamera MediaKind = "camera"
    MediaScreen MediaKind = "screen"
    CapabilityMediaLanesV1 = "media_lanes_v1"
    MaxCameraSlots = 10
)
type MediaStart struct { Kind MediaKind `json:"kind"` }
type MediaStop struct { Kind MediaKind `json:"kind"` }
type MediaSubscribe struct { Kind MediaKind `json:"kind"`; Fingerprint string `json:"fingerprint"`; Gen uint32 `json:"gen,omitempty"` }
type MediaSource struct { Kind MediaKind `json:"kind"`; Fingerprint string `json:"fingerprint"`; Slot int `json:"slot"`; Seq uint16 `json:"seq"`; Gen uint32 `json:"gen,omitempty"` }
```

Add message constants `media_start`, `media_stop`, `media_subscribe`, and `media_source`; add errors `media_full` and `not_publishing`; add optional `Capabilities []string` to both `Auth` and `AuthOK`, and optional `Media []MediaKind` to `VoiceMember`. Extend the protocol fuzz seed map.

- [ ] **Step 4: Advertise and retain the capability**

Offer `media_lanes_v1` from the client handshake. Intersect offered capabilities with server support, retain the accepted set on `conn`, return it in `auth_ok`, copy it into a private client capability set during `DialWith`, and implement `SupportsMediaLanes`. Warm/host handshakes negotiate the same optional field without changing their behavior.

- [ ] **Step 5: Run protocol, handshake, and compatibility tests**

Run: `go test ./core/proto ./core/hshake ./server ./client`

Expected: PASS, including old payload decoding.

- [ ] **Step 6: Commit**

```bash
git add core/proto core/hshake server/conn.go server/auth_test.go client/client.go client/client_test.go
git commit -m "feat: negotiate independent media lanes"
```

### Task 2: Server media control plane and room limits

**Files:**
- Create: `server/media.go`
- Create: `server/media_test.go`
- Modify: `server/conn.go`
- Modify: `server/voice.go`
- Modify: `server/share_test.go`

**Interfaces:**
- Consumes: Task 1 media protocol types and existing `voiceSession.post`.
- Produces: `(*Server).mediaSet(c *conn, kind proto.MediaKind, on bool) string`, `(*Server).mediaSubscribe(c *conn, kind proto.MediaKind, fp string, gen uint32) string`, one canonical per-connection media state used by both modern and legacy handlers, and actor entry points `voiceSession.mediaStarted`, `mediaStopped`, and `setMediaSubscription` that initially retain declaration state and are completed by Task 5.

- [ ] **Step 1: Write failing control-plane tests**

```go
func TestCameraLimitRejectsEleventhAndAllowsRetry(t *testing.T) {
    // Join eleven peers; start ten cameras; assert peer 11 gets media_full.
    // Stop one camera; assert peer 11 can start and no earlier camera was displaced.
}

func TestMediaStateIsIdempotentAndScopedToVoiceRoom(t *testing.T) {
    // Duplicate starts/stops succeed; unknown kind is bad_request; an out-of-room
    // screen subscription is forbidden; an inactive in-room source is not_publishing.
}

func TestLegacyShareMapsToScreenLane(t *testing.T) {
    // Legacy start/watch/stop produces the same canonical screen state and Sharing flag.
}
```

- [ ] **Step 2: Run focused server tests and verify failure**

Run: `go test ./server -run 'Test(CameraLimit|MediaState|LegacyShare)'`

Expected: FAIL because modern handlers and canonical lane state do not exist.

- [ ] **Step 3: Add canonical state and handlers**

Replace `conn.sharing`, `conn.watching`, and `conn.watchGen` with actor-safe membership state guarded by `chMu`:

```go
type mediaSubscription struct { Fingerprint string; Gen uint32 }
// conn fields:
published map[proto.MediaKind]bool
subscriptions map[proto.MediaKind]mediaSubscription
```

Implement `mediaSet` and `mediaSubscribe` with the exact error codes from the spec. Count active cameras under `chMu` before changing state. Duplicate starts and stops are no-ops that return success.

Add the three `voiceSession` actor entry points with minimal declaration-state behavior so this task compiles independently. Preserve the existing legacy screen routing inside those entry points; Task 5 replaces their modern no-forwarding portion with slot assignment.

- [ ] **Step 4: Route modern and legacy messages**

Decode the four modern media messages in `server/voice.go`. Keep legacy handlers as adapters to `MediaScreen`; legacy errors remain `not_sharing`, while modern inactive-source errors use `not_publishing` and echo `Gen`.

- [ ] **Step 5: Broadcast compatible voice state**

Populate `VoiceMember.Media` from canonical state and set `VoiceMember.Sharing` whenever `MediaScreen` is active. Leaving voice clears publications and subscriptions before broadcasting.

- [ ] **Step 6: Run focused and full server tests**

Run: `go test ./server`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add server/conn.go server/voice.go server/media.go server/media_test.go server/share_test.go
git commit -m "feat: track media lanes and camera limits"
```

### Task 3: Generalize the VP8 publishing pipeline

**Files:**
- Create: `client/video_publish.go`
- Create: `client/video_publish_test.go`
- Modify: `client/share.go`
- Modify: `client/share_test.go`
- Modify: `client/voice.go`
- Modify: `video/frame.go`

**Interfaces:**
- Consumes: Task 1 `MediaKind` and existing `video.Encoder`.
- Produces: reusable `videoPublisher`, `newVideoPublisher(kind proto.MediaKind, src FrameSource, enc frameEncoder, w, h, fps int, write func([]byte, time.Duration) error) *videoPublisher`, and `video.ErrNoFrame` (with `client.ErrNoFrame` retained as an alias).

- [ ] **Step 1: Write failing independent-publisher tests**

```go
func TestPublishersRunCameraAndScreenIndependently(t *testing.T) {
    // Start two fake sources and assert each writes only to its own lane.
    // Fail camera and assert screen continues writing.
}

func TestPublisherKeepsNewestFrameAndStopsOnce(t *testing.T) {
    // Block encoding, submit multiple frames, assert the newest pending frame wins;
    // concurrent stop calls close encoder and goroutines exactly once.
}
```

- [ ] **Step 2: Run focused client tests and verify failure**

Run: `go test ./client -run 'TestPublisher'`

Expected: FAIL because `videoPublisher` does not exist.

- [ ] **Step 3: Extract the generic publisher**

Move capture polling, newest-frame replacement, encoding, keyframe requests, and shutdown from `shareLoop` into `videoPublisher`. Store publishers in `call.publishers map[proto.MediaKind]*videoPublisher`, guarded by the existing media mutex. Keep each publisher's failure callback lane-specific.

- [ ] **Step 4: Preserve the public screen API**

Keep `StartShare`, `StopShare`, `ShareKeyframe`, and legacy behavior unchanged externally. Internally they call `startPublisher(MediaScreen, ...)`, `stopPublisher(MediaScreen, ...)`, and select legacy or modern control messages from `SupportsMediaLanes`.

- [ ] **Step 5: Run publisher, screen, and race tests**

Run: `go test -race ./client -run 'Test(Publisher|Share|VideoEncode)'`

Expected: PASS with no race reports.

- [ ] **Step 6: Commit**

```bash
git add client/share.go client/share_test.go client/voice.go client/video_publish.go client/video_publish_test.go video/frame.go
git commit -m "refactor: share the VP8 publishing pipeline"
```

### Task 4: Negotiate a stable modern media track layout

**Files:**
- Create: `client/media_tracks.go`
- Create: `client/media_tracks_test.go`
- Create: `server/media_tracks.go`
- Create: `server/media_tracks_test.go`
- Modify: `client/voice.go`
- Modify: `server/sfu.go`

**Interfaces:**
- Consumes: Tasks 1-3 capability, media kinds, and publisher tracks.
- Produces: stable track IDs `camera-publish`, `screen-publish`, `camera-slot-0` through `camera-slot-9`, and `screen-slot-0`; `slotKey{Kind proto.MediaKind, Index int}` and track-ID parsing helpers on client and server.

- [ ] **Step 1: Write failing track-layout tests**

```go
func TestModernOfferHasTwoPublishTracksAndElevenReceiveSlots(t *testing.T) {
    // Assert one audio sender, camera+screen senders, ten camera receivers,
    // and one screen receiver with stable IDs after initial negotiation.
}

func TestServerRejectsExtraOrMisorderedVideoTracksWithoutDroppingVoice(t *testing.T) {
    // Supply an extra/incorrect video transceiver; assert it is drained/discarded,
    // no publication is created, and an audio frame still crosses the call.
}

func TestLegacyOfferKeepsSingleOnDemandScreenTrack(t *testing.T) {
    // Auth without capability and assert the existing negotiation shape remains.
}

func TestModernOfferFitsProtocolAtMaxVoice(t *testing.T) {
    // At max_voice=50 with the retired-line threshold, assert every encoded offer
    // and answer remains at or below proto.MaxMessageSize.
}
```

- [ ] **Step 2: Run focused negotiation tests and verify failure**

Run: `go test ./client ./server -run 'Test(ModernOffer|ServerRejectsExtra|LegacyOffer)'`

Expected: FAIL because fixed modern tracks and slot parsers do not exist.

- [ ] **Step 3: Build the client track layout**

For modern calls, create both local VP8 tracks and the eleven recv-only video transceivers in `call.newPC` before the first offer. Retain the current on-demand single screen sender/receiver path for legacy calls. Store outgoing tracks and senders by `MediaKind`.

- [ ] **Step 4: Bind the server layout safely**

In the SFU, map the first two expected incoming modern video transceivers to camera and screen by the negotiated layout, not by remote track labels. Create eleven stable local RTP tracks/senders per modern viewer before answering. Drain and discard every unexpected video track.

- [ ] **Step 5: Run negotiation and existing voice tests under race detection**

Run: `go test -race ./client ./server -run 'Test(ModernOffer|ServerRejectsExtra|LegacyOffer|Voice|Call)'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add client/media_tracks.go client/media_tracks_test.go client/voice.go server/media_tracks.go server/media_tracks_test.go server/sfu.go
git commit -m "feat: negotiate stable media lane tracks"
```

### Task 5: Route camera slots and the selected screen through the SFU

**Files:**
- Create: `server/media_slots.go`
- Create: `server/media_slots_test.go`
- Modify: `server/sfu.go`
- Modify: `server/share_test.go`
- Modify: `server/videosource_test.go`

**Interfaces:**
- Consumes: Task 2 actor entry points and Task 4 `slotKey` plus stable senders.
- Produces: per-slot `videoSlot`, completed `voiceSession.mediaStarted`, `mediaStopped`, `setMediaSubscription`, and `sendMediaSource` actor operations.

- [ ] **Step 1: Write failing SFU routing tests**

```go
func TestCamerasAutoAssignStableSlotsForEveryModernViewer(t *testing.T) {
    // Start three cameras; assert every other viewer gets unique camera sources
    // in deterministic lowest-free slots, self is excluded, and assignments do not move.
}

func TestSelectedScreenCoexistsWithCameraSlots(t *testing.T) {
    // Select a screen while cameras flow; assert screen uses screen-slot-0 and
    // every camera slot remains attached and forwarding.
}

func TestSlotReuseAnnouncesBoundaryBeforeNewPackets(t *testing.T) {
    // Stop publisher A, assign B to the freed slot, and assert media_source for B
    // carries the next rewritten sequence before any B packet is delivered.
}
```

- [ ] **Step 2: Run focused SFU tests and verify failure**

Run: `go test ./server -run 'Test(CamerasAutoAssign|SelectedScreenCoexists|SlotReuse)'`

Expected: FAIL because multi-slot routing does not exist.

- [ ] **Step 3: Replace the single modern video watch with slots**

Give each modern peer ten camera `videoSlot` values and one screen `videoSlot`. Each slot owns its stable sender, local output track, `outStream`, current forwarder/rewrite, source fingerprint, and generation. Keep legacy `vsender` state behind the compatibility adapter until legacy tests pass.

- [ ] **Step 4: Implement deterministic actor operations**

On camera start/join, assign the publisher to the lowest free camera slot on every modern viewer. On stop/leave, detach it without moving other cameras. On screen subscription, reassign only screen slot 0. For each attach: retire the prior rewrite, compute the boundary, queue `media_source`, then add the forwarding sink and request a keyframe.

- [ ] **Step 5: Run server media, legacy share, and race suites**

Run: `go test -race ./server -run 'Test(Camera|Media|SelectedScreen|Slot|Share|VideoSource)'`

Expected: PASS with legacy source switching unchanged.

- [ ] **Step 6: Commit**

```bash
git add server/media_slots.go server/media_slots_test.go server/sfu.go server/share_test.go server/videosource_test.go
git commit -m "feat: route simultaneous camera and screen video"
```

### Task 6: Receive and decode independent media slots

**Files:**
- Create: `client/media_receive.go`
- Create: `client/media_receive_test.go`
- Modify: `client/client.go`
- Modify: `client/share.go`
- Modify: `client/voice.go`
- Modify: `client/share_test.go`

**Interfaces:**
- Consumes: Task 4 slot track IDs and Task 5 `MediaSource` boundaries.
- Produces: `client.MediaFrame`, `client.MediaSink`, `(*Client).SetMediaSink(MediaSink)`, and `(*Client).SubscribeScreen(fp string) error`; existing `VideoSink`, `SetVideoSink`, and `Watch` remain legacy-compatible.

- [ ] **Step 1: Write failing receiver tests**

```go
func TestMediaReceiversDeliverConcurrentCameraAndScreenFrames(t *testing.T) {
    // Feed keyframes into two camera slots and the screen slot; assert the sink
    // receives the correct kind, slot, fingerprint, generation, and frame for each.
}

func TestReassignedSlotDropsOldAndPreKeyframePackets(t *testing.T) {
    // Announce B on A's slot; feed late A and B delta packets; assert none render
    // until B's first keyframe at/after the announced sequence.
}

func TestStaleMediaSourceGenerationCannotReplaceCurrentScreen(t *testing.T) {
    // Subscribe A then B; deliver A's late response; assert B remains selected.
}
```

- [ ] **Step 2: Run focused client tests and verify failure**

Run: `go test ./client -run 'Test(MediaReceivers|ReassignedSlot|StaleMediaSource)'`

Expected: FAIL because keyed receivers and modern subscription state do not exist.

- [ ] **Step 3: Add keyed sink and slot state**

Define:

```go
type MediaFrame struct { Kind proto.MediaKind; Fingerprint string; Slot int; Gen uint32; Frame *video.Frame }
type MediaSink interface { WriteMediaFrame(MediaFrame) }
```

Maintain one `srcGate`, decoder, newest-frame handoff, PLI limiter, and cancellation scope per stable slot. Parse the track ID once and reject tracks outside the negotiated layout.

- [ ] **Step 4: Implement modern screen subscription**

`SubscribeScreen` serializes requests, sends `MediaSubscribe{Kind: MediaScreen}`, waits for its matching generation, restores the prior selection on a timely refusal, and ignores stale replies. `Watch` delegates to it on modern servers and preserves the old code path otherwise.

- [ ] **Step 5: Run client media and race suites**

Run: `go test -race ./client -run 'Test(Media|Reassigned|Stale|Watch|Video)'`

Expected: PASS without cross-slot frame delivery.

- [ ] **Step 6: Commit**

```bash
git add client/media_receive.go client/media_receive_test.go client/client.go client/share.go client/share_test.go client/voice.go
git commit -m "feat: receive independent media slots"
```

### Task 7: Windows camera capture adapter

**Files:**
- Create: `cameracap/cameracap.go`
- Create: `cameracap/source.go`
- Create: `cameracap/source_test.go`
- Create: `cameracap/media_windows.go`
- Create: `cameracap/stub_other.go`
- Create: `cameracap/convert_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: `video.Frame` and `video.ErrNoFrame` from Task 3.
- Produces: `type Device struct { ID, Name string }`, `type Source interface { Next(*video.Frame) error; Close() error }`, `Devices() ([]Device, error)`, `Open(deviceID string) (Source, error)`, `Supported`, and `ErrUnsupported`.

- [ ] **Step 1: Write failing capture-boundary tests**

```go
func TestLatestFrameSourceIsNonBlockingAndKeepsNewest(t *testing.T) {
    // Feed three synthetic images; assert Next returns the newest as 640x360 I420,
    // returns video.ErrNoFrame when unchanged, and Close unblocks the reader once.
}

func TestReaderFailureIsReportedOnce(t *testing.T) {
    // Make the fake reader fail; assert Next returns that failure and repeated Close is safe.
}

func TestUnsupportedPlatformContract(t *testing.T) {
    // On the stub build, assert Supported=false and Devices/Open return ErrUnsupported.
}
```

- [ ] **Step 2: Run package tests and verify failure**

Run: `go test ./cameracap`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Add the capture-only dependency and portable source core**

Add `github.com/pion/mediadevices v0.10.0`. Implement a background reader that owns MediaDevices frame-release callbacks, converts the newest frame to 640x360 I420, and exposes non-blocking `Next`. Do not import any MediaDevices codec package.

- [ ] **Step 4: Add Windows backend and non-Windows stub**

The Windows file imports only the camera driver, enumerates video inputs, selects the saved ID or system default, and requests the nearest available raw format to 640x360 at 15 fps. The stub keeps cross-builds working and never prompts for permission.

- [ ] **Step 5: Run unit and cross-build checks**

Run: `go test -race ./cameracap`

Run: `$env:GOOS='linux'; go build ./cameracap; $env:GOOS='darwin'; go build ./cameracap; Remove-Item Env:GOOS`

Expected: PASS without opening a physical device.

- [ ] **Step 6: Commit**

```bash
git add cameracap go.mod go.sum video/frame.go
git commit -m "feat: add Windows camera capture source"
```

### Task 8: Camera API and failover-safe publisher adoption

**Files:**
- Create: `client/camera.go`
- Create: `client/camera_test.go`
- Modify: `client/video_publish.go`
- Modify: `client/voice.go`
- Modify: `client/warmpeer.go`
- Modify: `client/failover_test.go`

**Interfaces:**
- Consumes: Tasks 3-7 publisher, camera lane, and stable camera track.
- Produces: `(*Client).StartCamera(src FrameSource) error`, `StopCamera() error`, `CameraKeyframe()`, `ErrAlreadyCamera`, `ErrCameraUnsupported`, and publisher transfer inside `AdoptCall`.

- [ ] **Step 1: Write failing camera and adoption tests**

```go
func TestCameraAndScreenStartStopIndependently(t *testing.T) {
    // Start both; stop camera; assert screen publisher and declaration remain active.
}

func TestAdoptCallCarriesActivePublishersAndScreenSubscription(t *testing.T) {
    // Adopt onto a new client; assert camera+screen sources stay open, publishers
    // rebind to new tracks, no old frames queue, and selected screen is restored.
}

func TestStopDuringAdoptionWinsOverSavedCameraState(t *testing.T) {
    // Pause adoption after snapshot, stop camera, resume, and assert the new client
    // does not redeclare or transmit camera media.
}
```

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./client -run 'Test(CameraAndScreen|AdoptCallCarries|StopDuringAdoption)'`

Expected: FAIL because camera API and publisher transfer do not exist.

- [ ] **Step 3: Add fixed-profile camera API**

`StartCamera` calls the generic publisher with 640, 360, 15, and 300. It requires `SupportsMediaLanes`, reports `ErrCameraUnsupported` otherwise, and sends modern camera start/stop declarations. Source or encode failure stops only camera and emits a lane-specific event.

- [ ] **Step 4: Make publishers rebindable during adoption**

Replace publisher write closures tied to one `call.vtrack` with a synchronized lane destination that can be set to nil during switching and rebound to the new call track. Transfer active publisher ownership once; use per-lane generations so concurrent stop wins. Restore only the current screen subscription.

- [ ] **Step 5: Run camera, failover, voice, and race tests**

Run: `go test -race ./client -run 'Test(Camera|Adopt|Takeover|Failover|Share|Voice)'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add client/camera.go client/camera_test.go client/video_publish.go client/voice.go client/warmpeer.go client/failover_test.go
git commit -m "feat: publish cameras across host failover"
```

### Task 9: Headless call-view state and navigation

**Files:**
- Create: `ui/state/callview.go`
- Create: `ui/state/callview_test.go`
- Modify: `ui/state/state.go`
- Modify: `ui/state/voice.go`
- Modify: `ui/state/voice_test.go`

**Interfaces:**
- Consumes: `VoiceMember.Media` and existing speaking/mute inputs.
- Produces: `state.CallParticipant`, `(*State).CallParticipants() []CallParticipant`, `CallViewOpen bool`, `OpenCallView`, `CloseCallView`, and `VoiceClickAction` values `join`/`open`.

- [ ] **Step 1: Write failing state tests**

```go
func TestCurrentVoiceClickOpensCallViewWithoutLeaving(t *testing.T) {
    // Joined channel returns open; OpenCallView preserves InCall and membership.
}

func TestCallParticipantsCarryCameraScreenMuteAndStableOrder(t *testing.T) {
    // Assert cleaned names, self marker, camera/screen flags, mute state, and
    // deterministic name/fingerprint order.
}

func TestDisconnectAndLeaveCloseCallView(t *testing.T) {
    // Assert call view closes when InCall clears but not on unrelated text navigation.
}
```

- [ ] **Step 2: Run state tests and verify failure**

Run: `go test ./ui/state -run 'Test(CurrentVoiceClick|CallParticipants|DisconnectAndLeave)'`

Expected: FAIL because call-view state does not exist.

- [ ] **Step 3: Implement the pure state model**

Add `Camera` and `Sharing` to `CallParticipant`, derive them from `VoiceMember.Media` with `Sharing` fallback, and expose call-view transitions. Joining successfully opens the view; clicking the already joined channel opens it; Esc/back closes the view without leaving voice.

- [ ] **Step 4: Run all headless UI state tests**

Run: `go test ./ui/state`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ui/state/callview.go ui/state/callview_test.go ui/state/state.go ui/state/voice.go ui/state/voice_test.go
git commit -m "feat: model the call view and media states"
```

### Task 10: Multi-source UI frame store and presentation selection

**Files:**
- Create: `ui/mediaframes.go`
- Create: `ui/mediaframes_test.go`
- Modify: `ui/app.go`
- Modify: `ui/video.go`
- Modify: `ui/media.go`
- Modify: `ui/media_test.go`

**Interfaces:**
- Consumes: Task 6 `client.MediaSink` and Task 9 call participant state.
- Produces: keyed `mediaFrames`, per-source `videoView`, `selectScreen(fp string)`, and screen selection through `Client.SubscribeScreen`.

- [ ] **Step 1: Write failing frame-store tests**

```go
func TestMediaFramesKeepNewestPerSourceWithoutCrossTalk(t *testing.T) {
    // Write two cameras and one screen concurrently; assert independent newest frames.
}

func TestFrameFromRetiredGenerationCannotReplaceReassignedTile(t *testing.T) {
    // Change a slot source then deliver an old-generation frame; assert it is dropped.
}

func TestSelectingScreenDoesNotClearCameraFrames(t *testing.T) {
    // Select/switch/close a screen and assert camera views remain populated.
}
```

- [ ] **Step 2: Run focused UI tests and verify failure**

Run: `go test ./ui -run 'Test(MediaFrames|FrameFromRetired|SelectingScreen)'`

Expected: FAIL because the UI has one global sink/view.

- [ ] **Step 3: Replace the global modern video sink**

Store newest decoded frames by `(kind, slot, fingerprint, generation)` and maintain one uploadable `videoView` per current source. Keep the legacy `videoSink` only for old-server screen mode. Reset/deallocate a view only when its exact source retires.

- [ ] **Step 4: Generalize screen selection**

Route LIVE clicks to `SubscribeScreen` for modern calls and `Watch` for legacy calls. A selected screen no longer replaces or clears camera state. Preserve self screen preview behavior.

- [ ] **Step 5: Run media UI and race tests**

Run: `go test -race ./ui -run 'Test(Media|Video|Share|SelfPreview|Call)'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ui/mediaframes.go ui/mediaframes_test.go ui/app.go ui/video.go ui/media.go ui/media_test.go
git commit -m "feat: retain concurrent camera and screen frames"
```

### Task 11: Camera devices, controls, and user-visible failures

**Files:**
- Create: `ui/camera.go`
- Create: `ui/camera_test.go`
- Modify: `ui/app.go`
- Modify: `ui/callbar.go`
- Modify: `ui/voicepanel.go`
- Modify: `ui/state/hits.go`
- Modify: `ui/state/settings.go`
- Modify: `ui/state/settings_test.go`
- Modify: `ui/settings.go`
- Modify: `ui/settings_test.go`
- Modify: `ui/session.go`
- Modify: `ui/hosts.go`

**Interfaces:**
- Consumes: Tasks 7-8 camera source/API and Task 10 media frame store.
- Produces: persisted `Settings.CameraDevice string`, camera device settings row, `HitBarCamera`, `HitVoiceCamera`, and UI-owned `cameraState` lifecycle.

- [ ] **Step 1: Write failing camera UI tests**

```go
func TestCameraOpensOnlyAfterExplicitToggleAndPersistsDeviceChoice(t *testing.T) {
    // Join voice: opener count stays zero. Toggle camera: open once with saved ID.
    // Cycle device: save ID, but never persist camera-on state. Assert the local
    // participant tile receives preview frames without an SFU self-subscription.
}

func TestCameraFailureFallsBackToAvatarWithoutStoppingOtherMedia(t *testing.T) {
    // Inject open/read failure; assert camera off, source closed once, visible notice,
    // call+screen still active, and participant tile falls back to avatar.
}

func TestLeavingCallClosesCameraAndScreenSources(t *testing.T) {
    // Assert both capture sources close and both controls reset on leave/disconnect.
}
```

- [ ] **Step 2: Run focused UI tests and verify failure**

Run: `go test ./ui ./ui/state -run 'Test(CameraOpens|CameraFailure|LeavingCallCloses|CameraDevice)'`

Expected: FAIL because camera settings and controls do not exist.

- [ ] **Step 3: Add settings and device enumeration**

Add `camera_device` to `Settings`, enumerate devices only when the settings page requests refresh, and save changes through the existing atomic settings writer. Unsupported platforms show `camera: unsupported` without a permission prompt.

- [ ] **Step 4: Add independent camera controls**

Queue camera open/start/stop work on `mediaWorker`, never the Ebiten goroutine. Keep the source in UI ownership so leaving, disconnecting, app shutdown, or a terminal camera event closes it exactly once. Show persistent active-capture indication in both call bar and call view.

- [ ] **Step 5: Map actionable errors**

Map permission/device open failure, device read/disconnect, `media_full`, server unsupported, and failover restoration errors to the exact user-facing categories in the spec. Do not retry a failed device automatically.

- [ ] **Step 6: Run UI, settings, failover, and race tests**

Run: `go test -race ./ui ./ui/state -run 'Test(Camera|LeavingCall|Settings|Failover|Promotion)'`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add ui/camera.go ui/camera_test.go ui/app.go ui/callbar.go ui/voicepanel.go ui/state/hits.go ui/state/settings.go ui/state/settings_test.go ui/settings.go ui/settings_test.go ui/session.go ui/hosts.go
git commit -m "feat: add safe webcam controls and settings"
```

### Task 12: Render and operate the call view

**Files:**
- Create: `ui/callview.go`
- Create: `ui/callview_test.go`
- Modify: `ui/app.go`
- Modify: `ui/main_scene.go`
- Modify: `ui/callbar.go`
- Modify: `ui/voicepanel.go`
- Modify: `ui/state/hits.go`
- Modify: `ui/hits_test.go`

**Interfaces:**
- Consumes: Tasks 9-11 participant state, keyed video views, screen selection, and camera controls.
- Produces: call-view geometry, `HitCallBack`, `HitCallParticipant`, `HitCallScreen`, participant tile hits, selected-screen presentation area, close/back behavior, and integrated mute/camera/share/leave controls.

- [ ] **Step 1: Write failing layout and interaction tests**

```go
func TestCallViewShowsAllParticipantsAndSelectedScreenTogether(t *testing.T) {
    // Ten participants with cameras plus selected screen: assert ten non-overlapping
    // tile rects, a presentation rect, and camera views are not hidden/cleared.
}

func TestCallTileUsesAvatarMuteAndSpeakingStates(t *testing.T) {
    // Assert camera frame wins over avatar, missing frame uses avatar, mute badge is
    // present, and speaking glow follows the existing envelope for the same fingerprint.
}

func TestCallViewControlsAreIndependent(t *testing.T) {
    // Toggle camera, share, mute, and close view; assert each action changes only its
    // own state and closing the view leaves the call active.
}
```

- [ ] **Step 2: Run focused call-view tests and verify failure**

Run: `go test ./ui -run 'Test(CallViewShows|CallTileUses|CallViewControls)'`

Expected: FAIL because the scene and hit targets do not exist.

- [ ] **Step 3: Implement responsive call-view geometry**

Use the existing chat pane. With no selected screen, tile participants in the largest balanced grid that fits. With a selected screen, reserve the main presentation area and place compact camera tiles in the remaining strip. At tiny sizes, keep controls reachable and clip excess decoration rather than creating overlap.

- [ ] **Step 4: Render tiles and source indicators**

Draw the newest camera frame or deterministic initials avatar, participant name, mute badge, speaking glow, and LIVE indicator. Render the selected screen with existing letterboxing/fullscreen behavior. Do not make drawing mutate subscriptions or media state.

- [ ] **Step 5: Wire navigation and hits**

Opening a newly joined or already joined voice channel enters the call view. Esc or the back control returns to chat without leaving. Camera, share, mute, and leave controls call the existing independent actions.

- [ ] **Step 6: Run UI and headless-state suites**

Run: `go test ./ui ./ui/state`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add ui/callview.go ui/callview_test.go ui/app.go ui/main_scene.go ui/callbar.go ui/voicepanel.go ui/state/hits.go ui/hits_test.go
git commit -m "feat: add the multi-participant call view"
```

### Task 13: End-to-end compatibility, failover, and documentation

**Files:**
- Create: `server/media_e2e_test.go`
- Modify: `client/failover_test.go`
- Modify: `ui/failover_test.go`
- Modify: `docs/protocol.md`
- Modify: `README.md`
- Modify: `HANDOFF.md`
- Modify: `PLAN.md`

**Interfaces:**
- Consumes: the complete vertical slice.
- Produces: end-to-end proof, protocol documentation, user instructions, and an updated project handoff.

- [ ] **Step 1: Write failing end-to-end tests before final integration fixes**

```go
func TestE2ETwoCamerasAndScreenFlowSimultaneously(t *testing.T) {
    // Three loopback clients with deterministic VP8 sources: assert both remote
    // cameras and the selected screen produce correctly identified decoded frames.
}

func TestE2EMediaLanesSurviveTakeover(t *testing.T) {
    // Active+standby hosts: camera and screen active during takeover; assert voice,
    // both publications, and selected screen resume without a reconnect screen.
}

func TestE2EMixedVersionScreenCompatibility(t *testing.T) {
    // Modern server with legacy-mode client and old-capability server with modern
    // client: assert screen still works and camera is explicitly unavailable.
}
```

- [ ] **Step 2: Run end-to-end tests and make only integration fixes needed for them**

Run: `go test -race ./server ./client ./ui -run 'TestE2E'`

Expected: PASS without physical devices or timing sleeps.

- [ ] **Step 3: Update project documentation**

Document `media_lanes_v1`, messages, errors, fixed slots, legacy translation, 640x360/15 fps/300 kbps camera profile, ten-camera limit, Windows camera support, camera settings/control, and the manual two-client camera+screen+failover checklist. Mark webcam/call view complete in `HANDOFF.md` and `PLAN.md`; retain macOS/Linux camera capture and adaptive priority as future work.

- [ ] **Step 4: Run formatting, static analysis, full race suite, cross-builds, and focused flake check**

Run: `gofmt -w <all changed .go files>`

Run: `go vet ./...`

Run: `go test -race -p 1 ./...`

Run: `go test -race ./server ./client ./ui -run 'TestE2E(TwoCamerasAndScreenFlowSimultaneously|MediaLanesSurviveTakeover)' -count=5`

Run: `$env:GOOS='linux'; go build ./cmd/...; $env:GOOS='darwin'; go build ./cmd/...; Remove-Item Env:GOOS`

Expected: every command exits 0; the repeated test passes five times.

- [ ] **Step 5: Complete the real-device release checklist**

On Windows with a disposable test server/config: verify permission/open, device selection, two webcams plus one selected screen, camera unplug fallback, independent controls, visible capture indicators, and active/standby takeover. Record observations in `HANDOFF.md`; do not automate or modify the user's real configuration. If the required hardware or second endpoint is unavailable, record that exact unchecked item rather than substituting a synthetic test or claiming it passed.

- [ ] **Step 6: Commit**

```bash
git add server/media_e2e_test.go client/failover_test.go ui/failover_test.go docs/protocol.md README.md HANDOFF.md PLAN.md
git commit -m "test: verify simultaneous media lanes end to end"
```

### Task 14: Whole-branch verification and review fixes

**Files:**
- Modify: only files required by verified review findings.

**Interfaces:**
- Consumes: all prior tasks.
- Produces: a release-ready branch with no Critical or Important correctness findings.

- [ ] **Step 1: Review the full branch against the approved spec**

Focus on lane isolation, fixed-slot negotiation, untrusted track handling, room limits, source-boundary ordering, teardown ownership, rapid toggle/failover races, legacy compatibility, camera privacy, and UI state/render separation. Reject speculative slow-network work unless it exposes an application bug independent of the external condition.

- [ ] **Step 2: Add a failing regression test for each accepted correctness finding**

Run the narrow test and verify it fails for the reported behavior before changing production code.

- [ ] **Step 3: Apply the smallest fixes and rerun their owning packages**

Expected: each new regression test passes and no unrelated refactor is introduced.

- [ ] **Step 4: Run final verification from a clean worktree**

Run: `go vet ./...`

Run: `go test -race -p 1 ./...`

Run: `git diff --check main...HEAD`

Expected: all commands exit 0 and `git status --short` is empty.

- [ ] **Step 5: Commit review fixes if any**

```bash
git add <only reviewed fix files>
git commit -m "fix: address call view review findings"
```
