# Call View and Independent Media Lanes

Date: 2026-10-23

## Purpose

HERMEC will add a Discord-style call view and webcam support without making
camera video and screen sharing mutually exclusive. A participant may use
voice, camera, and screen sharing at the same time. The design gives each
media type an independent lifecycle so a later release can preserve audio,
prefer screen sharing over camera video, and reduce or pause camera video
first when resources are constrained.

This release establishes those media boundaries but does not implement
adaptive bandwidth control, simulcast, or synthetic slow-network behavior.
It targets calls of roughly ten people, matching the existing product scale
target.

## User Experience

Clicking the voice channel that the user has joined opens the call view.
The view contains:

- A tile for every participant, showing their camera or their avatar.
- Speaking indication and mute state on each participant tile.
- Independent microphone, camera, screen-share, and leave controls.
- A presentation area for one selected screen share while camera tiles remain
  visible.
- A clear LIVE indicator for every participant currently sharing a screen.

Camera and screen controls do not affect one another. A camera failure returns
that participant's tile to its avatar without ending voice or screen sharing.
A screen-share failure closes only the presentation surface for that share.

The user may select among multiple advertised screen shares, but each viewer
receives one screen share at a time. Every active camera in a room is delivered
to every other modern client, subject to the ten-camera room cap. The sender's
own tile uses a local preview instead of a server round trip.

## Media Model

One WebRTC peer connection continues to carry all media for a client. Media is
divided into logical lanes:

| Kind | Codec | Publication | Subscription |
| --- | --- | --- | --- |
| `audio` | Opus | Existing voice lifecycle | Every other participant |
| `camera` | VP8 | Independent on/off lifecycle | Every active camera |
| `screen` | VP8 | Independent on/off lifecycle | One selected share |

`MediaKind` is the shared identifier for these lanes. A video source is
identified by `(participant fingerprint, media kind)`, never by fingerprint
alone.

Audio keeps its existing forwarding path. Camera and screen use the existing
VP8 encoder, RTP forwarding, sequence rewriting, source-boundary, PLI, and
frame-decoding mechanisms through generalized video-lane interfaces.

The order `audio`, `screen`, `camera` is recorded as semantic priority
metadata. This release does not claim to enforce transport-level priority.
Future congestion policy can use the separate lanes to reduce camera before
screen and protect audio without another protocol redesign.

## Negotiated Tracks and Slots

Modern clients negotiate stable video transceivers once when a call starts:

- One outgoing camera track.
- One outgoing screen track.
- Ten incoming camera slots.
- One incoming selected-screen slot.

Inactive outgoing tracks send no samples. Empty incoming slots carry no
source. Starting or stopping a lane and assigning a different source use the
already-negotiated sender or receiver rather than adding a new SDP media line.
This prevents camera toggles and participant churn from repeatedly
renegotiating the PeerConnection.

Camera slots are numbered 0 through 9; the screen slot is 0 in the separate
screen namespace. The SFU maintains a continuous outgoing RTP
sequence/timestamp space for each slot. Whenever a source is attached or
changed, the server sends a source boundary and requests a keyframe. The
client discards packets from before the boundary and refuses delta frames
until the first valid keyframe.

Camera slots remain assigned to a participant until that camera stops or the
participant leaves. The single screen slot changes only when the viewer selects
a different screen or stops watching.

## Wire Protocol

`Auth` and `AuthOK` gain `Capabilities []string` encoded as optional JSON field
`capabilities`. A client offers the capabilities it understands in `Auth`; the
server returns the supported intersection in `AuthOK` and retains that result
on the connection before voice negotiation begins. The capability for this
design is `media_lanes_v1`. Clients must not use the new media messages unless
the server accepts it.

The new message family is:

- `media_start { kind }`: declares the sender's lane active.
- `media_stop { kind }`: declares the sender's lane inactive.
- `media_subscribe { kind, fingerprint, generation }`: selects the source for
  a subscription-controlled lane. An empty fingerprint clears it. In this
  version it is used for screen sharing; camera subscriptions are automatic.
- `media_source { kind, fingerprint, slot, sequence, generation }`: identifies
  the source and first RTP sequence number assigned to an incoming slot. An
  empty fingerprint detaches the slot.

Only `camera` and `screen` are accepted as video kinds in this version.
Malformed messages and unknown kinds return `bad_request`; an eleventh active
camera returns `media_full`; duplicate starts and stops are idempotent;
subscriptions outside the current voice channel return `forbidden`; and an
inactive source returns `not_publishing`. Errors caused by a numbered
subscription echo its generation.

`VoiceMember` gains `Media []MediaKind` encoded as optional JSON field `media`
while retaining the existing `Sharing` field. A new server includes `screen`
in `Media` and sets `Sharing` for screen sharing; it includes `camera` only in
`Media`. Old clients therefore retain their screen indicator and ignore camera
state safely.

Generation numbers make rapid toggle and selection operations ordered. A late
response for an older generation cannot reactivate a stopped lane, restore an
old source, or overwrite the current selection.

## Mixed-Version Compatibility

- New client with new server: uses `media_lanes_v1` and the independent lanes.
- New client with old server: keeps the existing `share_start`, `share_stop`,
  `watch`, and single-video-track screen path. Camera controls are disabled with
  a server-upgrade explanation.
- Old client with new server: offers no capability and continues using the
  legacy screen messages. The server maps them to the participant's screen
  lane and returns the legacy `video_source` boundary on the client's single
  video track.
- Old clients are not assigned camera tracks or sent camera state they need to
  understand.

The legacy path remains isolated behind an adapter. New media state is not
stored twice in the SFU.

## Client Components

### Generic video publisher

The current screen-share encoder loop becomes a reusable video publisher. It
owns capture polling, newest-frame replacement, VP8 encoding, forced
keyframes, sender RTCP handling, statistics, and shutdown. It accepts a frame
source and a lane kind. Separate publisher instances may run concurrently for
camera and screen.

There is no unbounded frame queue. If capture outpaces encoding, a newer frame
replaces the pending frame, preserving the current low-latency behavior.

### Camera capture

A `CameraSource` boundary provides device enumeration, opening, raw frames,
and close. This release supplies a Windows backend using Pion MediaDevices for
camera discovery and raw capture, plus unsupported-platform stubs so existing
macOS and Linux builds remain valid. The backend converts frames into HERMEC's
existing I420 representation and uses the existing `govpx` encoder. HERMEC
does not adopt MediaDevices' codec layer or require system VP8 libraries.

Camera capture is opened only after the user enables it. Device selection is
remembered as an application preference, but camera-active state is not
persisted across application launches.

CI uses fake camera sources. Physical-device permission and enumeration are
covered by the release manual checklist.

### Video receiver collection

The single global video sink becomes a keyed collection of receive slots. Each
slot independently owns its RTP assembler, source gate, decoder, newest-frame
handoff, PLI limiter, statistics, and cancellation. Frames are delivered with
their media kind, slot, fingerprint, and source generation so the UI cannot
place a stale frame in a newly reassigned tile.

### Failover

The client snapshots which local lanes were active and which screen was
selected immediately before switching hosts. Once the replacement
PeerConnection is established, it redeclares only those active lanes and
restores the screen subscription. Local capture may remain open during the
short switch, but encoded frames are not queued for later transmission.

A restoration failure affects only its lane and produces a visible notice. It
does not end the voice call or silently enable a lane that the user stopped
during the switch.

## Server Components

The voice session remains an actor. Only its actor goroutine mutates peers,
tracks, slots, subscriptions, senders, and negotiation state. RTP forwarders
remain independent hot-path goroutines whose sink lists are synchronized.

Incoming video transceivers are bound to camera and screen roles by the
server-created negotiation layout, not by an untrusted track label. Extra or
misordered tracks are closed and never forwarded.

Each peer owns:

- At most one declared camera publisher and one declared screen publisher.
- A set of viewers for each published lane.
- Ten camera receive slots and one selected-screen receive slot.
- Independent output stream and rewrite state per receive slot.

When a camera starts, the actor assigns it to one free camera slot on every
other modern peer. The publisher's own client supplies its tile from local
preview. When a camera stops, the actor detaches those assignments and
announces empty sources. Joining and leaving follow the same assignment rules.

The room accepts at most ten simultaneously active cameras. An eleventh start
is rejected before a publisher is exposed or forwarding resources are
created. No existing camera is displaced. Voice membership may still use the
configured `max_voice` limit, including values above ten.

Screen publishers are advertised to the room but forwarded only to viewers
who select them. The legacy watch adapter and modern screen subscription both
drive this same internal subscription state.

## Call View State and Rendering

Call-view state is maintained outside Ebiten drawing code. It contains
participant identity, mute state, speaking level, camera activity, camera slot,
screen activity, and whether the screen is selected. Rendering consumes a
snapshot of this state.

Participant tiles use camera frames when available and avatars otherwise.
Speaking glow derives from the existing audio level signal and remains visible
with either representation. A selected screen receives the main presentation
area; the participant grid remains present in a compact layout.

Opening and closing the call view does not join, leave, start, stop, subscribe,
or unsubscribe by itself. Media changes happen only through explicit controls
or participant/server lifecycle events.

All capture, encoding, client media calls, and device enumeration run outside
the game/render goroutine. The UI receives immutable state updates and newest
frames only.

## Safety and Resource Boundaries

- Camera permission is requested only in response to the camera control.
- No reconnect, failover, call join, or app launch opens a previously stopped
  camera.
- Persistent on-screen indicators show every locally active capture lane.
- Leaving voice, disconnecting, and app shutdown request both publishers to
  stop and close their capture sources.
- Input dimensions, frame rate, and bitrate are validated before encoder
  allocation. Camera uses a fixed 640x360, 15 fps, 300 kbps call-tile profile;
  screen retains the existing share presets.
- The server ignores undeclared RTP video and accepts at most one publisher
  track for each supported kind per peer.
- The ten-camera cap is checked atomically in the voice-session actor before
  state is broadcast.
- Unknown media kinds and out-of-room fingerprints are rejected rather than
  creating state.
- Source generations and RTP boundaries prevent late control messages or
  packets from crossing assignments.
- Camera, screen, and audio teardown paths are independent and idempotent.
- Camera frames are transient. They are never written to logs, history, or
  disk by HERMEC.
- Errors and logs name the lane and device but never include frame contents.

## Errors Visible to Users

The UI distinguishes these actionable outcomes:

- Camera permission denied.
- Camera device unavailable or disconnected.
- Camera limit reached for the room.
- Server does not support webcams.
- Camera or screen failed to resume after host failover.
- Selected screen is no longer available.

A device read failure stops and closes that camera publisher once, returns its
tile to the avatar, updates room state, and displays the error. It does not
enter an automatic reopen loop.

## Verification Strategy

Automated tests cover behavior introduced by HERMEC itself:

- Camera and screen publishing concurrently without either stopping the
  other.
- Independent camera and screen start, stop, teardown, and error paths.
- Correct assignment of multiple camera slots to participant identities.
- Slot reuse without stale frames or source confusion.
- Automatic camera assignment for join, leave, start, and stop.
- The ten-camera cap and a successful retry after a slot becomes free.
- Rejection of duplicate, malformed, unknown-kind, and out-of-room requests.
- Camera permission, open, read, and disconnect failures through a fake source.
- Call-view participant tiles, speaking state, mute state, controls, and
  selected-screen layout state.
- Failover restoring only active lanes and the current screen subscription.
- New/new, new/old, and old/new client/server compatibility behavior.
- Race-enabled tests for concurrent capture, media events, failover, and UI
  frame delivery.

Tests use deterministic fake frame sources and protocol/SFU events. They do
not require a physical webcam, sleep to imitate slow links, or attempt to
predict congestion policy that is outside this release.

The manual release check uses two real clients and covers camera permission,
device selection, camera plus screen at the same time, multiple visible
cameras, selecting a screen without hiding cameras, device unplug, and host
failover while camera and screen are active.

## Explicitly Deferred

- Transport-level media priority or bandwidth estimation.
- Simulcast, scalable video coding, or per-viewer quality layers.
- Viewport-based camera subscription and camera pagination beyond the
  ten-camera target.
- More than one received screen share at a time.
- Camera capture backends for macOS and Linux.
- Automatic camera reopening after device failure.
- Background blur, replacement, recording, virtual cameras, and camera
  controls such as exposure or zoom.
- Synthetic slow-network, packet-loss, and long-duration soak suites.

These items can build on the lane interfaces without changing the identity,
lifecycle, or compatibility model defined here.
