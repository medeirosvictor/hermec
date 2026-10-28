# Embedded-network transport proof

Results recorded 2026-10-28. This report covers internal transport plumbing and
a disposable headless harness. It does **not** claim that embedded private
networking is available in the HERMEC application.

## Recommendation

**PROCEED with production planning; SHIPPING BLOCKED on real-network evidence.**

The local proof establishes a viable boundary: one HERMEC client/server/host
instance can retain an explicit network provider for WebSocket signaling, host
links, Pion interfaces and UDP media without process-global routing or system
fallback. A headless loopback scenario delivered audio, decoded cameras and
selected screens in both directions and carried mute/media through warm-standby
takeover.

No disposable Tailscale or Headscale network was available in this run. Direct
and DERP-relayed paths, actual tsnet enrollment, revocation, two real concurrent
profiles, and packaging behavior on physical target machines remain **NOT RUN**.
Those rows block a user-facing private-network release, but they do not block
work on the separate self-managed AWS/VPS hosting path.

## Implemented proof boundary

Root module:

- `netpath.Network` supplies per-instance dial, listen, packet-listen and Pion
  operations. Nil preserves existing system networking.
- Clients retain the validated Pion network for active and warm peers. Private
  WebSocket dials ignore global proxy/TLS dial hooks.
- Servers use the provider for TCP, UDP media, Pion APIs and authenticated host
  links. Provider mode rejects LAN discovery, `public_ip`, legacy UDP ranges and
  nil Pion networks before binding.
- Startup rollback and late peer creation close resources without closing the
  borrowed provider.

Nested `tools/embedded-probe` module:

- pins `tailscale.com v1.104.2`, whose module requires Go 1.27.3;
- persists one tsnet node per resolved state directory;
- exposes only synthetic Pion interfaces containing assigned profile addresses;
- rejects host/LAN binds, unknown literal destinations and DNS destinations in
  the proof stage;
- wraps packet writes so UDP cannot escape the explicitly refreshed peer snapshot;
- provides an interactive CLI whose auth URLs are printed but never written to
  reports;
- rejects non-empty state directories that lack the probe marker.

The CLI performs one explicit refresh after all three nodes enroll. That fixed
snapshot is deliberately not production-ready. A shipped adapter needs bounded live peer refresh and a designed MagicDNS policy without
weakening literal UDP checks.

## Automated evidence

Commands passed locally on Windows amd64:

```text
root:   go test -race -count=1 ./client ./server ./integration
probe:  go vet ./...
probe:  go test -race -count=1 ./...
```

The probe cross-built successfully for:

- Windows amd64 and arm64
- Linux amd64 and arm64

The loopback scenario uses three explicit providers and public HERMEC APIs. It
checks:

- founder and standby authenticated against one signed server ID;
- signaling readiness separately from warm Pion data-channel readiness;
- blocked standby media cannot be reported ready;
- Opus payload delivery in both directions;
- decoded camera frames in both directions;
- decoded selected-screen frames in both directions;
- primary shutdown, standby takeover and call adoption;
- retained mute state and continuing camera/screen delivery;
- cancellation releases both TCP and UDP listeners.

Typical complete local scenario duration was approximately 6.4 seconds.

## Artifact size

Windows amd64, `go build -ldflags "-s -w"`:

| Build | Bytes |
|---|---:|
| Full interactive tsnet probe CLI | 29,204,480 |
| Same module invoking the loopback HERMEC scenario without `Open` | 27,307,520 |
| Reachable tsnet/interactive delta in this comparison | 1,896,960 |

Unstripped cross-builds were approximately 37-41 MB. This is not a release-app
size prediction: the probe contains both client and server scenario machinery.
Measure the actual GUI and server artifacts again when the production provider
is linked. Active tsnet CPU and memory were not measured because no real node
was enrolled.

## Real-network matrix

All rows below remain **NOT RUN**:

| Coordination | Topology | Status |
|---|---|---|
| Tailscale | direct, no installed system client | NOT RUN |
| Tailscale | DERP relay | NOT RUN |
| Headscale | direct | NOT RUN |
| Headscale | configured DERP relay | NOT RUN |
| Either | two concurrent private groups | NOT RUN |
| Either | restart from marked state | NOT RUN |
| Either | denied enrollment and revoked node | NOT RUN |
| Either | coordination unavailable | NOT RUN |

Exact operator commands and safety rules are in
`tools/embedded-probe/README.md`. Reports must never contain auth URLs, tokens,
private keys or personal network identifiers.

## Known limitations and decisions

- No user-facing Create/Join/Profile UI exists.
- No real-network route has been observed, so direct versus relay claims are
  prohibited.
- The proof rejects DNS destinations; production MagicDNS support needs an
  authenticated peer-name mapping.
- Peers are admitted from an explicitly refreshed fixed snapshot. Production must continuously refresh it and
  define revocation behavior for existing sockets.
- A profile's tsnet backend may use Tailscale's normal diagnostic facilities;
  privacy and log controls require review before release integration.
- Root remains Go 1.27.0. The nested candidate uses the automatically downloaded
  Go 1.27.3 patch toolchain.
- Embedded networking does not add HERMEC E2EE and does not change the trusted
  host/SFU model.

## Next delivery order

1. Package and expose self-managed dedicated hosting first, so AWS/VPS hosting
   can become usable without waiting for private-network enrollment evidence.
2. Run the disposable real-network matrix when Victor can supply Tailscale and
   Headscale test environments.
3. Build private profile lifecycle and Create/Join UI using the measured
   provider boundary, gated by those real-network results.
4. Add guarded hosting-method migration only after both destinations exist.
