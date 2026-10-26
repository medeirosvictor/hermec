# Embedded Networking Proof Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove HERMEC signaling, media and host takeover through embedded
Tailscale/Headscale before building the hosting screens and migration workflow.

**Architecture:** Add explicit per-instance networking hooks with system
networking as the unchanged default. Exercise them with an isolated experimental
tsnet adapter and headless harness. Keep the experimental dependency in a nested
module until measured results justify adding it to release builds.

**Tech Stack:** Go 1.27, existing Gorilla WebSocket/Pion WebRTC v4.2.23,
Pion transport/v5 v5.1.1, candidate tailscale.com v1.104.2 in the probe module.
Verify the candidate's module/toolchain compatibility when resolving it;
record any necessary version change before implementation.

**Spec:** [approved hosting readiness design](../specs/2026-10-26-hosting-readiness-design.md),
approved by Victor on 2026-10-26. This plan implements its transport proof;
production enrollment/settings/migration and the independent RNNoise experiment
have separate follow-up plans after the proof settles transport interfaces.

## Global Constraints

- HERMEC owns no coordination or media infrastructure.
- The group owner supplies a Tailscale network or an existing Headscale service.
- AWS/VPS hosting means download/configure/run/share-address; the app does not
  create cloud instances or accept cloud account credentials.
- Never copy the founder/member private key to a VPS.
- Do not distribute reusable admin/pre-auth keys in HERMEC invitations or logs.
- Private mode advertises only the selected profile's addresses and restricts
  ICE candidates and peer dialing to that profile.
- Do not silently fall back to public/LAN candidates or enable subnet routing,
  exit nodes or Funnel.
- Never touch Victor's real HERMEC config.
- Do not mutate process-global WebSocket dialers.
- Keep the existing signed identity checks, SFU media model and warm-standby
  behavior. No protocol version bridging or new user-facing feature claims.
- Tests use disposable state. Real enrollment is explicit, operator-assisted
  and uses a disposable network; absence of credentials is a pending manual
  acceptance item, not permission to enroll Victor's real devices.

## Review Focus

1. An embedded node not ready yet must fail closed, never use system sockets.
2. Two groups on the same coordination URL must keep distinct node state.
3. A late dial or media result after shutdown must release its resources.
4. An unavailable standby must not be labeled ready after signaling alone.
5. Capture-free test frames must prove actual bidirectional media delivery.

Each condition has a test in the task that owns it below. Root tests continue
to run without the experimental tsnet module or network credentials.

## File map and shared interfaces

- `netpath/network.go`: small per-instance boundary; no tsnet import.
- `client/network.go`: network-aware dialing, retaining current Dial APIs.
- `client/voice.go`: per-client ICE network selection, including warm peers.
- `server/network.go`: provider routing for bound sockets and host-link dials.
- `server/server.go`, `server/mediaport.go`, `server/sfu.go`,
  `server/linkdial.go`: pass the selected provider through existing lifecycles.
- `tools/embedded-probe/`: nested module containing only a disposable prototype,
  adapter, CLI and its tests; not part of release artifacts.
- `docs/research/embedded-network-proof.md`: measured results and limitations.

Shared root boundary (Task 1 produces; Tasks 2-4 consume):

```go
type Network interface {
    DialContext(context.Context, string, string) (net.Conn, error)
    Listen(context.Context, string, string) (net.Listener, error)
    ListenPacket(context.Context, string, string) (net.PacketConn, error)
    PionNet() transport.Net
}
```

`netpath.Network` imports the pinned `github.com/pion/transport/v5`.
A nil Network means existing system behavior. A non-nil provider must return
a non-nil PionNet; reject invalid providers before opening connections.
Do not embed a system Net into the private adapter: unimplemented operations
return an explicit unsupported error instead of escaping to host networking.
Pion adapter methods used for UDP use the embedded node exclusively.

### Task 1: Route client signaling and ICE through a per-instance network

**Files:** Create `netpath/network.go`, `client/network.go`,
`client/network_test.go`; modify `client/client.go`, `client/voice.go` and
`client/warmpeer.go` only where the provider must be carried.

**Interfaces:** Produce
`client.DialWithNetwork(ctx context.Context, url string, id *identity.Identity, name, password string, o DialOpts, network netpath.Network) (*Client, error)`.
Existing Dial/DialPinned/DialWith delegate with nil; leave the handshake option
alias independent of networking. Client stores the validated provider for all
future PeerConnections, including warm peers.

- [ ] Write `TestDialWithNetworkUsesSelectedProvider`: real loopback HERMEC
  server and a recording provider; authenticated ServerID equals the fixture's
  pinned ID, all signaling dials use the provider. Add nil-provider regression.
- [ ] Write `TestPrivateProviderFailureDoesNotFallback` with provider returning
  a sentinel error while a system-reachable fixture exists: require that error
  and no authenticated session. Add invalid-nil-PionNet rejection before dial.
- [ ] Run `go test ./client -run 'Test(DialWithNetwork|PrivateProvider)' -count=1`;
  record expected failing behavior before implementation.
- [ ] Add the interface and function above. Preserve TLS validation and HTTP
  refusal handling from the current dial path. Apply the provider Net to Pion
  without changing codecs, media lanes or test-only loopback restrictions.
- [ ] Add `TestNetworkPeerCreationAfterCloseReleasesResources`: a controlled
  late dial/create completion closes the resulting socket instead of retaining
  it. Verify both active and warm PeerConnection setup use the provider.
- [ ] Run focused tests plus `go test ./client ./integration`; commit
  `feat: allow per-client network providers`.

### Task 2: Route server sockets and host links through the same provider

**Files:** Create `server/network.go`, `server/network_test.go`; modify
`server/server.go`, `server/mediaport.go`, `server/sfu.go`, `server/linkdial.go`.

**Interfaces:** Add `Network netpath.Network` to `server.Config`. Keep
`server.New`, `Server.Start`, `server.NewHost` and `Host.Start` signatures.
Host links use the Config provider, never a global dialer. Server owns its
listeners/mux, not the borrowed Network lifetime.

- [ ] Write `TestHostNetworkRoutesTCPUDPAndLinks`: record provider operations
  around real loopback nodes; verify TCP listening, UDP listening and outbound
  authenticated host link all use the configured instance. Include startup
  failure that closes already-open listeners without closing the provider.
- [ ] Write `TestNetworkMediaUsesSelectedAddresses`: a single-provider fixture
  yields SDP candidates only for its test interfaces; system address candidates
  cannot appear. Add separate IPv4 and IPv6 fixture cases and an unsupported-
  family failure that leaves no half-started host.
- [ ] Run `go test ./server -run 'Test(HostNetwork|NetworkMedia)' -count=1` and
  record RED. Add nil-provider regression coverage to existing media-port tests.
- [ ] Implement provider dispatch at socket creation and host-link dialing,
  preserving media/discovery socket sharing and shutdown order. A private
  provider disables LAN broadcast discovery in the probe config; it does not
  change the existing discoverable default for system networking.
- [ ] Run focused tests and `go test ./server ./integration`; commit
  `feat: route host sockets and links through a network provider`.

### Task 3: Build the isolated tsnet adapter with strict address handling

**Files:** Create `tools/embedded-probe/go.mod`, `go.sum`, `network.go`,
`network_test.go`, `pionnet.go`, `pionnet_test.go`, `README.md`.

**Interfaces:** Nested module `hermec-embedded-probe`, requiring the root module
via `replace github.com/medeirosvictor/hermec => ../..` and the selected pinned
tsnet version. Produce `Open(ctx context.Context, cfg ProfileConfig) (*Node, error)`,
`(*Node).Close() error`, and the netpath.Network methods. `ProfileConfig` contains
`StateDir, Hostname, ControlURL string` and
`OnLoginURL func(string)`; no credentials go in reports. One Node per directory.

- [ ] Write `TestProfilesDoNotShareStateByControlURL`: two profiles with the
  same control URL get separate state paths and distinct adapter instances.
  Reject the same resolved state directory being opened twice. Test a profile
  stopped while another is active does not close the other's sockets.
- [ ] Write `TestNotReadyProviderFailsClosed` and
  `TestPionNetRejectsNonProfileBind`: no node-ready address means no socket;
  a host/LAN bind or a UDP destination outside the selected test profile's
  permitted peers is rejected. Unsupported operations return an error.
- [ ] Write UDP adapter round-trip tests with an in-memory packet backend:
  `ReadFrom`, `WriteTo`, deadlines and close unblock Pion-facing operations.
  Assert returned LocalAddr is a specific `*net.UDPAddr`, never unspecified.
- [ ] Run `go test ./...` from the nested module; record RED before adding
  implementation. Dependency download may require normal tool approval.
- [ ] Implement the adapter with tsnet Dial/Listen/ListenPacket and Pion
  transport/v5 interfaces. Enumerate only node-assigned interface addresses;
  disable mDNS/STUN/TURN and system candidate fallback in private test config.
  Keep authentication callbacks in memory, not debug logs. Document why
  websocket-private traffic and Pion media cannot escape through the OS Net.
- [ ] Run adapter tests and root suite; commit
  `feat: add isolated embedded-network probe adapter`.

### Task 4: Headless end-to-end harness and repeatable acceptance procedure

**Files:** Create `tools/embedded-probe/main.go`, `scenario.go`,
`scenario_test.go`; extend its README with exact operator steps.

**Interfaces:** `RunScenario(ctx context.Context, cfg ScenarioConfig) (ScenarioReport, error)`.
`ScenarioConfig` supplies three distinct temporary profiles, generated test-only
HERMEC identities, and explicit peer addresses/coordination URL. The report
contains per-check result/duration, candidate addresses and resource measures;
never private keys, auth URLs or account tokens. Default port is 17697.

- [ ] Write a provider-backed loopback `TestScenarioRequiresMediaBothWays`:
  encoded synthetic Opus and VP8 fixtures flow through actual HERMEC client/SFU
  paths and receiver callbacks. A receive-only success cannot mark the scenario
  passed. Require camera and screen delivery independently, not just RTP bytes.
- [ ] Write `TestScenarioStandbyIsNotReadyFromSignalingOnly`: a standby with
  blocked media is reported unready. For an all-working fixture, authenticated
  takeover retains the original group ID, mute flag, active camera, screen and
  selected viewer. Include bounded cleanup after cancellation mid-join.
- [ ] Run `go test ./...` in the probe module; observe RED. Implement scenario
  orchestration using root public APIs, without app GUI/capture devices and
  without exporting test-only internals from production packages.
- [ ] Add CLI flags `-state`, `-control-url`, `-hostname`, `-role`, `-peers`,
  `-port`, `-report`; require explicit `-state`. Role is host/client/standby.
  Reject non-empty state directories not created by this probe. Enrollment URLs
  are displayed only as an interactive action, never written to report files.
- [ ] Write the manual test matrix: disposable Tailscale and Headscale networks,
  no installed system client; direct and relay paths; two concurrent profiles;
  restart, denied enrollment and unavailable coordination. Require operator
  consent before real enrollment; record unavailable rows as NOT RUN.
- [ ] Run harness tests and commit
  `test: prove embedded call media and standby lifecycle`.

### Task 5: Record measurements and produce the next phase plans

**Files:** Create `docs/research/embedded-network-proof.md`; update `HANDOFF.md`
and roadmap in place. Create separate production implementation plans for
private UI/lifecycle, dedicated UI/packaging and hosting-method migration, each
linked to the approved design and concrete interfaces established here.

- [ ] Run `go vet ./...` and `go test -race -count=1 ./...` in root and probe
  module; failures must be named. Check formatting and `git diff --check`.
- [ ] Build the probe on supported release OS/architectures; record compiled
  size against the baseline and measured idle/active CPU and memory on available
  devices. Do not infer untested platform behavior from cross-compilation.
- [ ] With an explicitly supplied disposable network, run available acceptance
  rows and record commands, versions, actual topology and results. Do not mark
  direct/relay tested without evidence of the selected path.
- [ ] Write a GO / REVISE recommendation based on authenticated media delivery,
  takeover, profile isolation, platform packaging and resource measurements.
  Missing real-network evidence blocks shipping approval, not local plumbing.
- [ ] Map each remaining approved-design requirement into its next phase's
  task. Preserve founder-only policy changes, voice-empty switching, signed
  transition/restart recovery, state transfer without identity-key copying,
  UI validation/share/retry states and offline-client recovery limits.
- [ ] Draft the separate RNNoise trial plan around the approved local/default-
  off toggle behavior, with actual code/model versions and license evidence.
  Keep algorithm integration independent of networking and avoid adding a
  runtime dependency before the trial is measured.
- [ ] Update documentation to reflect only measured current behavior; commit
  `docs: record embedded-network proof and next delivery plans`.

## Self-review and requirement coverage

Transport/dialing and lifecycle: Tasks 1-3. Actual media, standby and concurrent
profiles: Tasks 3-4. Fail-closed routing and late shutdown: Tasks 1-4. Evidence,
platform checks and scope handoff: Task 5. The approved creation/settings,
dedicated packaging, migration and suppression requirements intentionally get
their own phase plans in Task 5: their final adapter/runtime decisions depend
on this measured proof. None are dropped from the roadmap or claimed shipped.

Execution method already established in project instructions: cost-conscious
subagent-driven implementation with scoped reviews and one final branch review.
Review this plan before execution. This document does not authorize cloud
provisioning, public deployment or enrollment in Victor's real networks.
