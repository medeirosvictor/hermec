# Embedded Private-Network Lifecycle Plan

**Status:** Planned after disposable Tailscale/Headscale evidence is available.

**Goal:** Turn the proven provider boundary into private Create/Join flows where
friends need no separately installed Tailscale client.

## Tasks

1. **Production provider package:** move the accepted adapter into the root
   module, add live peer refresh/revocation, authenticated MagicDNS mapping,
   bounded shutdown and privacy/log controls. Keep system fallback impossible.
2. **Profile storage:** one marked profile directory per HERMEC server, encrypted
   or OS-protected state where practical, reference-counted borrowers, idle
   shutdown, app-shutdown ordering, logout and leave-versus-revoke semantics.
3. **Enrollment UI:** Tailscale or owner-supplied Headscale URL, explicit browser
   action, waiting/approval/expired/denied/cancel states, no reusable auth keys,
   credentials or auth URLs in logs/reports.
4. **Private Create Server:** enroll, create the signed HERMEC group, bind only
   profile addresses, validate signaling/media separately, and share versioned
   connection details with server ID and coordination endpoint.
5. **Private Join:** import details, enroll before dialing, pin server ID, require
   both network reachability and HERMEC admission, and leave no phantom tile on
   failure.
6. **Host/standby integration:** all active/warm sessions and friend hosts borrow
   the profile; standby readiness requires media, not signaling alone.
7. **Acceptance:** Tailscale and Headscale, direct and relay paths, no installed
   system client, two groups concurrently, restart, denial/revocation,
   coordination outage, media and failover on physical endpoints.

Shipping remains blocked until the real-network rows in
`docs/research/embedded-network-proof.md` pass.
