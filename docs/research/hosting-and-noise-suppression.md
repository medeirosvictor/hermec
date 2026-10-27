# Hosting options and microphone noise suppression

Research notes, 2026-10-26. The
[hosting design](../superpowers/specs/2026-10-26-hosting-readiness-design.md) and
first [transport-proof plan](../superpowers/plans/2026-10-26-embedded-network-proof.md)
are approved. This note records candidate algorithms and the original UI
discussion; it does not claim they are implemented. Current behavior remains
documented in README.md and docs/protocol.md.

## Intended outcome

Victor wants private groups using Tailscale or Headscale, and a self-managed
dedicated server on AWS or another VPS, operated like a Minecraft server:
download, configure, run, share an address. He confirmed that first scope;
in-app cloud provisioning is excluded. The project continues to own no
infrastructure. After the cleanup and hosting work, real-use checks should
establish a baseline for regular use, followed by polish and quality of life.

Victor selected embedded private networking so friends do not need a separate
Tailscale installation. tsnet is the candidate integration; its media path still
needs validation. He also explicitly requires UI flows for creating a server
with each method and changing the setup of an existing server.

He also wants a free noise-suppression algorithm behind a toggle for testing
in real calls. No processor has been selected or benchmarked in HERMEC yet.

## Hosting approach to discuss

- Preserve the existing server identity, signed group configuration and host
  failover model across connection modes. Keep host addresses and credentials
  local; review what group connection policy belongs in the signed chain.
- Private networking: embed the network client. tsnet has a configurable
  ControlURL for alternate coordination and
  TCP/UDP APIs. Headscale is a self-hosted alternative control server, not a
  replacement HERMEC media server. Enrollment, per-group isolation, logout,
  credential storage and host reachability need explicit design.
- Embedded networking needs an end-to-end proof with Pion: signaling alone is
  insufficient. Verify ICE addresses, UDP packet paths, direct and relayed
  overlay connections, simultaneous media, and standby takeover. Re-measure
  the binary/dependency cost from the historical tsnet spike.
- Dedicated server: extend the existing hermec-server packaging and guide for
  an operator-owned Linux VPS, including an AWS example. Provide persistent
  state, service/container lifecycle, logs, backups and upgrades. Public mode
  requires trusted TLS for signaling, admission controls, and reachable media
  UDP; a WebSocket reverse proxy alone does not carry the media path.
- Cloud instances and game-hosting panels vary: the host must permit arbitrary
  HERMEC binaries/containers and required TCP/UDP listeners. A Minecraft-only
  service cannot be assumed compatible. No cloud resources are provisioned
  by this research or roadmap edit.

Official references:

- [tsnet server API](https://tailscale.com/docs/reference/tsnet-server-api):
  ControlURL, Listen, ListenPacket and Dial.
- [Headscale features](https://headscale.net/stable/about/features/).
- [AWS EC2 security groups](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-security-groups.html):
  instance traffic is controlled by operator-configured inbound/outbound rules.

## Server creation and configuration UI

Both flows belong to the hosting milestone, rather than a later polish round.
The following is a design outline, not approved screen behavior.

- Create Server: choose private Tailscale, private Headscale, or a dedicated
  server. Explain who operates the network/server and what the owner must
  supply. Private setup handles enrollment, progress and reachability inside
  HERMEC; a dedicated-server flow guides the owner through preparing the host
  and connecting to it, without provisioning a VM. End with validated connection
  details and an invite/share action. Retain the existing LAN/local path.
- Existing server: expose its current hosting method and health in Server
  Settings and offer a permission-controlled change flow. Distinguish changes
  to group policy from this PC's host address, credentials and hosting toggle.
- Preview the consequences: who will need enrollment or a new address, whether
  standby hosts are reachable, and whether an active call must finish first.
  Do not silently change networking underneath a call.
- Prepare and validate the destination before finalizing. Keep the server ID,
  signed membership/configuration and chat history. Handle desktop-to-VPS state
  transfer without copying member private keys; the destination must prove its
  own authorized host identity. Define recovery when the destination cannot be
  reached or the original host disappears during the change.
- Success refreshes connection-sharing information and clearly reports any
  action friends still need to take. Failure leaves an understandable state,
  a retry/recovery action and technical diagnostics in logs.

The approved design chooses owner-supplied coordination, an initial embedded
profile per HERMEC group, guided enrollment and founder-authorized method
changes while voice is empty. Validate profile resource costs in the proof.
Offline friends may need refreshed connection details. See the design for
the complete migration and recovery behavior.

## Noise-suppression candidates

| Candidate | Evidence | HERMEC evaluation |
| --- | --- | --- |
| RNNoise | Xiph neural noise suppression; BSD redistribution terms; upstream C and a browser-oriented WASM wrapper exist. | First candidate. Compare a portable embedded build with a statically bundled native build; neither route is selected. |
| SpeexDSP | Official preprocessor API supports denoising, gain control and voice activity detection. | Traditional DSP baseline. Check exact revision's license, packaging, voice artifacts and CPU rather than assuming it is sufficient. |
| DeepFilterNet | Full-band 48 kHz enhancement; repository code is MIT/Apache-2.0; Rust inference tooling. | Alternative if RNNoise falls short. Check model redistribution separately and measure runtime/build complexity. |

Additional candidate found during implementation planning:
[VoiceBlender/rnnoise-go](https://github.com/VoiceBlender/rnnoise-go) is a
pure-Go RNNoise port with BSD-3-Clause terms. Its upstream documents 10 ms
frames, an embedded model package and 48 kHz operation. This is a promising
first integration route before adding a WASM runtime or another native build.
These are upstream claims, not HERMEC measurements; verify the exact revision,
model provenance, output quality and native platform performance in the trial.
Do not select a runtime from the older C/WASM comparison without evaluating
this option.

Sources:

- [RNNoise upstream](https://github.com/xiph/rnnoise) and
  [redistribution terms](https://raw.githubusercontent.com/xiph/rnnoise/main/COPYING).
- [Shiguredo RNNoise WASM](https://github.com/shiguredo/rnnoise-wasm): a
  browser/JavaScript integration, not a ready-to-use Go binding. A Go WASM
  runtime integration needs a separate feasibility and performance check.
- [Speex preprocessor API](https://www.speex.org/docs/manual/speex-manual/node7.html).
- [DeepFilterNet upstream and code license](https://github.com/Rikorose/DeepFilterNet).

## Proposed experiment and acceptance checks

HERMEC captures 48 kHz mono PCM and encodes 20 ms frames in
audio/capture.go. Process PCM on the reader/encoder path before Opus; keep
the device callback free of filtering work. Adapt processor frame sizes
explicitly and measure buffering rather than assuming zero additional delay.

Start with a default-off Settings toggle labeled experimental. Switching must
work during a call without rejoining. Keep processor state owned by the audio
worker and reset it appropriately after bypass/mute gaps. Preserve the user's
mute and gain settings. On processor initialization/failure, visibly disable
filtering, keep the call usable, and log the reason. Bundle required assets and
notices; send no audio to an external service and record no raw mic audio by
default.

Compare off/on in normal conversation, with a fan, keyboard, and competing
speech; listen for clipped syllables and altered voice. Measure CPU, memory,
processing time, added delay and artifact size on supported OS/architectures.
Verify toggle changes, mute/unmute, device changes, leave/rejoin and failover.
No quality or performance claim is made before those checks.

Noise suppression targets background sound. Echo cancellation also needs the
playback reference and timing alignment; it is separate work. Headphones remain
the current call guidance until echo cancellation is implemented and verified.
