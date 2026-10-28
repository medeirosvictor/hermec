# Hosting-Method Migration Plan

**Status:** Implement only after dedicated and private destinations both exist.

**Goal:** Change an existing server between local/LAN, embedded private network
and dedicated hosting while preserving server ID, membership and history.

## Tasks

1. Define a bounded signed connection-policy entry and persisted transition ID;
   keep credentials, machine paths and private network state out of the chain.
2. Add founder-only **Change hosting setup**: inspect, draft destination,
   prepare, validate, review impact, switch and verify.
3. Prepare the destination with its own authorized host identity. Synchronize
   chain/history and password proof while preventing it from becoming active.
4. Show affected clients, enrollment/address changes and standby readiness.
   Require voice empty at commit and recheck atomically against a new join.
5. Persist prepared/committed transition state. On restart or lost acknowledgement,
   inspect the signed chain and host state before retrying; never independently
   reactivate both endpoints.
6. Keep the old endpoint temporarily where practical to deliver verified move
   details. State clearly that offline clients may need to import refreshed
   details.
7. Test every failure boundary: destination loss, old-host loss, stale async
   result, history mismatch, chain fork, interrupted commit, rollback and
   corrective founder-authorized change.
8. Real-device acceptance covers private-to-dedicated, dedicated-to-private,
   unchanged-network host switch, offline friend recovery and active-call guard.
