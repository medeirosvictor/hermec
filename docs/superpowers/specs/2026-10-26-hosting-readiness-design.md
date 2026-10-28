# Hosting readiness: approved architecture decisions

**Approved 2026-10-26.** This is durable design guidance, not an implementation
claim. Active execution lives in the [private-network plan](../plans/2026-10-28-private-network-lifecycle.md),
[hosting-method migration plan](../plans/2026-10-28-hosting-method-migration.md),
and [dedicated-hosting plan](../plans/2026-10-28-dedicated-hosting.md).

## Boundaries and security

HERMEC owns no coordination, relay, or cloud infrastructure. Owners supply a
Tailscale network, an existing Headscale service, or an operator-owned server;
the app never provisions cloud instances or accepts cloud credentials. Existing
server ID, signed permissions, retained history, SFU media and standby failover
remain the model. Identity/enrollment are distinct: network access grants no
HERMEC membership or hosting authority.

Public dedicated hosting requires trusted `wss://`, separately reachable media
UDP, and admission (password proof or allowlist). Do not present open public
hosting as safe. The host is still trusted; embedded networking is not an
end-to-end-encryption promise. Same-build peers are expected before v1; no
mixed-version call/media bridge, direct peer media, federation, or
project-operated service is in scope.

## Private-network architecture

Use a provider boundary: a system provider for current LAN/public networking and
an embedded tsnet provider for Tailscale/Headscale, with Headscale supplying
`ControlURL`. Keep tsnet outside core protocol types. Persist one independent
profile per private group per machine; do not deduplicate by coordination URL.
Profiles own enrollment, cancellation and lifetime; sessions and host links
borrow them, unused profiles stop, and app shutdown closes their workers/sockets.
Network state stays local and excluded from group exports, history and
diagnostics. Never put reusable admin/pre-auth keys in invitations or logs.

Provider-owned TCP/UDP dialing/listening must feed signaling, host links and
Pion/ICE explicitly, without process-global WebSocket changes. Private mode
advertises and uses only its selected profile: no silent public/LAN fallback,
subnet routing, exit nodes or Funnel. Shipping requires real direct and relay
Tailscale/Headscale evidence, including media and standby takeover, not merely
TCP signaling.

## Create, join, and migration invariants

Create/join flows keep a draft until validation; cancellation removes draft
resources and no failed flow saves a tile. Join/import details carry a versioned
mode, server ID, relevant host hints and (where applicable) coordination
endpoint. Pin the server ID and require an authorized-host proof. Dedicated
joining uses WSS, admission, and an actual signaling/media check before save.

A hosting-method change is founder-authorized, prepares and validates the
destination before commit, and changes nothing silently during a call. Recheck
that voice is empty atomically at final switch. Preserve server ID, signed
membership and history. A new VPS makes its own key and is explicitly
host-authorized; never copy founder/member private keys. Keep credentials and
machine paths out of the chain. Persist a transition record so restart and lost
acknowledgement recovery distinguish prepared from committed; failure leaves
the old host active and exposes a concrete recovery action. Offline friends may
need refreshed connection details.

## Diagnostics and evidence

Use actionable UI states for enrollment, approval, TLS, media reachability and
migration failures; retain credential-free technical logs. Operations are
bounded/cancelable and ignore stale results. Use disposable configurations for
automated tests and never touch a real user config. Real acceptance must cover
provider direct/relay paths, concurrent profiles, private failover, migration,
denied/revoked enrollment, restart/offline recovery and blocked UDP.

## Related research

Noise suppression is a separate default-off experiment with no cloud audio
processing; see [candidate and evaluation research](../../research/hosting-and-noise-suppression.md).
