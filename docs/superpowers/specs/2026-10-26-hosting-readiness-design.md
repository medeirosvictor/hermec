# Hosting readiness: embedded private networks and dedicated servers

Status: proposed written design for Victor's review. Embedded networking,
Tailscale/Headscale options, self-managed dedicated hosting, and creation/change
UI are agreed directions. This document proposes their detailed behavior;
it does not claim that these features exist.

## Goal and boundaries

Let friends create, join and keep using a HERMEC group without installing a
separate VPN application, or run the same group on an operator-owned dedicated
server. Existing server identity, signed permissions, history, voice, webcams,
screen sharing and standby failover remain the communications model.

HERMEC owns no coordination or media infrastructure. The group owner supplies
a Tailscale network or an existing Headscale service; this is the initial design
assumption pending Victor's confirmation. Headscale installation is an operator
task with a guide. AWS/VPS hosting means download/configure/run/share-address;
the app does not create cloud instances or accept cloud account credentials.

The four UI cleanup fixes are an independent prerequisite. Noise suppression
is a separate experiment at the end of this document. Live hosting migrations,
arbitrary game-host panels, mixed-version bridging, direct peer media and a
HERMEC-operated relay/coordination service are outside this slice.

## User model

Creation distinguishes where the host runs from how friends reach it:

- Host location: this PC or a dedicated machine. The latter offers an operator
  setup guide and a connection/import step, not a fictitious local server.
- Connection method: private Tailscale, private Headscale, public address, or
  the existing LAN/local option. A dedicated machine can also be private.

The primary choices use plain labels: "This PC, private group" and "Dedicated
server". Advanced details expose the connection method. The private path asks
for Tailscale or Headscale; the latter needs its coordination URL. Explain
network-owner and server-host responsibilities at the step where they matter.

HERMEC identity and network enrollment are distinct: possession of a network
account does not grant HERMEC membership/hosting authority. Removing someone
from a group does not automatically remove their network enrollment; the UI
links the owner to the network administration action when needed.

## Embedded network lifecycle

Use a small network-provider boundary with a system provider for current
LAN/public connections and an embedded tsnet provider for private networks.
Tailscale and Headscale share the latter; Headscale supplies ControlURL.
Keep tsnet dependencies in the provider package, outside core protocol types.

Use one persisted network profile per private group on this machine initially,
with its own node state and local profile ID. A group can connect through its
profile while another group remains connected through a different profile.
Do not deduplicate profiles solely by coordination URL: different Tailscale
networks share the same URL. Multiple-node resource and service-limit behavior
must be checked in the probe before this topology is accepted for shipping.

The network profile owns enrollment state, cancellation and lifetime. A session,
friend host and its standby connections borrow that profile; stopping one
borrower cannot close the network for the others. Idle unused profiles stop;
app shutdown closes sockets and workers before stopping the embedded node.
Leaving a HERMEC server is separate from revoking its network node.

Enrollment opens the coordination service's authentication page via an explicit
user action and shows waiting, approval required, connected, expired and failed
states inside HERMEC. An owner may need to approve a new node outside the app.
Do not distribute reusable admin/pre-auth keys in HERMEC invitations or logs.
Network state stays under a separate local configuration subtree, excluded
from exported group configuration, history and diagnostic bundles.

## Signaling and media integration

Current signaling uses websocket.DefaultDialer in client/client.go and
server/linkdial.go. Server TCP and UDP sockets are bound directly in
server/server.go and server/mediaport.go. Pion API construction lives in
client/voice.go and server/sfu.go. Those are the required integration points.

Pass provider-owned dialing/listening operations to client sessions and host
links; do not mutate process-global WebSocket dialers. Supply explicit embedded
TCP listeners and UDP packet connections to the host and ICE mux. On clients,
adapt Pion's network interface/UDP handling so candidate gathering, checks,
media and warm standby data channels actually use the embedded network.
tsnet TCP success is not proof that voice or video works.

Private mode advertises only the selected profile's addresses and restricts
ICE candidates and peer dialing to that profile. Do not silently fall back to
public/LAN candidates or enable subnet routing, exit nodes or Funnel. Retain
the SFU: participants publish to the authorized HERMEC host. Overlay peer
visibility and the network operator's authority must be described accurately;
embedding is not a new end-to-end encryption promise.

The transport probe precedes production integration. It must prove authenticated
chat, bidirectional audio, camera plus selected screen, two concurrent private
profiles, and a warm-standby takeover using disposable network state. Check
direct and overlay-relayed connectivity. Record binary size, memory and CPU.
If Pion adaptation cannot meet the existing call model, revise this design
before choosing a different media protocol or shipping a signaling-only mode.

## Create Server and Join flows

Create follows: choose host location -> choose reachability -> configure/enroll
-> validate -> create/connect -> share connection details. Steps keep a draft
until confirmation. Cancel closes draft resources; no phantom saved tile.

For private this-PC hosting, enroll first, create the normal HERMEC identity
chain and host once the network is ready, then validate signaling and media
reachability. A local check is labeled local; remote verification needs another
endpoint and must never be implied by a successful bind. Show separate states
for host running, network ready, friends able to reach it and standby ready.

Join/import details include the HERMEC server ID, versioned connection mode,
coordination endpoint when applicable, and current host hints. Show network
enrollment before dialing a private host. Preserve current server-ID pinning
and require proof from an authorized host; an invitation is not authority to
change the pinned identity. Reject unsupported or malformed invitation fields.
Network registration and HERMEC admission are both required for private groups.

Public dedicated hosting uses trusted wss:// signaling, a reachable media UDP
port and configured HERMEC admission controls. Support the existing password
proof/allowlist with a usable join/setup flow as the first admission gate;
unique signed invitations and invite trees can follow separately. Public mode
must not present unauthenticated open hosting as a safe default.

The dedicated guide covers a service/container, persistent state, DNS/TLS,
TCP/UDP firewall rules, logs, updates, backups and restore. An AWS example
explains security-group configuration and that media egress is operator-paid.
The app reports actual connection/media checks and offers copyable connection
details. It cannot verify a provider's firewall by inspecting only local state.

## Existing Server Settings and changing method

Show current host and connection method, network status, standby readiness,
connection-sharing action and "Change hosting setup". Local device settings
(port, credentials, node state, let-this-PC-host) stay separate from signed
group policy. Initially the founder authorizes group-wide method changes;
other authorized hosts may inspect and prepare their own hosting endpoint.

Change follows: inspect -> draft destination -> prepare -> validate -> review
impact -> switch -> verify. Selecting a different method changes no live state.
Show who needs new enrollment/details and which standby hosts are unready.
Require voice to be empty at the final switch and recheck atomically with
the transition; no silent method switch during a call. If that guard loses
to a new voice join, cancel the switch and leave the current host active.

Keep the permanent server ID, signed membership and retained history. A new
machine creates its own member key and is explicitly authorized as a host;
never copy the founder/member private key to a VPS. Destination preparation
uses the existing host-link and history synchronization model while restricted
from acting as active. Reconcile chain/history before commit and coordinate
with the host state machine so the prepared host cannot cold-start into a
second active server. Do not implement a second failover authority beside it.

Group-wide connection policy and committed endpoint hints need a bounded,
signed representation and a persisted transition identifier. Exact wire fields
belong in the implementation plan/protocol update after this design is approved.
Do not put private credentials or machine paths in the chain. Same-build peers
are expected; existing protocol safety checks still apply.

Before commit, failure releases preparation and leaves the current setup active.
During commit, persist an explicit transition record so restart can distinguish
prepared from committed. On a lost acknowledgement, verify committed chain and
active-host state before retrying. After commit, do not independently reactivate
the old host; use verified failover/recovery and, when necessary, a founder-
authorized corrective change. Surface the exact recovery action in Settings.

Where practical retain the old endpoint temporarily to deliver verified move
information to connected clients. It is not a guarantee for offline friends:
show refreshed share details and state that unreachable/offline clients may
need to import them. A host switch through an unchanged network can retain
the current standby behavior; changing networks has an explicit reconnect.

## Diagnostics and verification

Use friendly actionable UI messages for enrollment, missing approval, failed
host start, untrusted TLS, unreachable media and failed migration. Keep technical
details in session logs, excluding credentials, authentication URLs containing
tokens and raw audio. Progress operations are cancelable and bounded; workers
post results back to the UI and ignore stale results after cancellation.

Use disposable test config and loopback/fake network providers for most tests.
Add transition/restart tests for every failure boundary and stale async result.
Real-node acceptance covers both coordination providers, no system Tailscale
installed, two groups concurrently, private host takeover, private-to-dedicated
and dedicated-to-private changes, denied enrollment, revoked nodes, app restart,
offline friend recovery and UDP-blocked hosting. Run supported native builds
and the normal Go vet/race suite. Never touch Victor's real HERMEC config.

## Separate experiment: microphone noise suppression

Start by probing RNNoise with a redistributable model/build. Compare embedded
WASM and statically bundled native integration; neither is selected yet.
SpeexDSP and DeepFilterNet are alternatives if the measured result warrants
them. Verify code and model licenses for the exact revisions being bundled.

The proposed user feature is "Noise suppression (experimental)", default off,
switchable during a call. Process local 48 kHz mono PCM before Opus encoding,
on the encoder worker rather than the device callback. Processor state stays
with that worker; handle mute/bypass gaps and frame-size adaptation explicitly.
Initialization/failure disables filtering visibly and preserves the call.

Compare speech with a fan, typing and competing speech; measure clipping,
voice coloration, CPU, allocations, latency and shipped size on Windows/Linux
x64/ARM and macOS from source. Check toggle, mute, device switch and host
takeover. No cloud processing or raw audio logging. Echo cancellation needs
the playback reference and remains separate; headphones remain recommended.

## Recommended delivery order

1. Finish and verify the four bounded cleanup fixes.
2. Prove embedded signaling/media and profile topology with disposable nodes.
3. Implement private Create/Join, profile lifecycle and host/standby integration.
4. Package dedicated hosting and its setup/import UI with TLS/admission flows.
5. Implement Server Settings and guarded method changes with recovery tests.
6. Probe RNNoise; ship the experimental toggle if results justify it.
7. Verify real use with friends and close the readiness checklist before
   treating the remaining work as polish and quality of life.

## Technical references

- [tsnet API](https://tailscale.com/docs/reference/tsnet-server-api): configurable
  coordination, TCP dialing/listening and UDP packet listeners.
- [Pion network customization](https://pkg.go.dev/github.com/pion/webrtc/v4#SettingEngine.SetNet).
- [Headscale](https://headscale.net/stable/about/features/).
- [AWS security groups](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-security-groups.html).
- [Audio candidates and license sources](../../research/hosting-and-noise-suppression.md).
