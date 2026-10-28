# Dedicated AWS/VPS Hosting Implementation Plan

**Status:** Tasks 1–4 are complete locally; Task 5 is next. Remote hosting is
not available until Task 6 passes and the work is merged and released.

## Goal and constraints

Make an operator-owned Linux VPS usable without project-operated infrastructure
or cloud credentials. Public signaling uses trusted `wss://`; media UDP must be
reachable separately. Preserve server ID, signed chain and host authorization;
never copy founder or member private keys to a VPS. Admission (password proof or
allowlist), explicit persistent state/logs/backups/upgrades, and same-build
peers are required.

## Completed evidence (Tasks 1–4)

- Bootstrap/validation, service-ready Linux bundles and lifecycle materials.
- Provider-neutral and AWS operator guidance plus `hermec-verify` for pinned
  WSS signaling and two-endpoint media proof.
- Desktop Dedicated Add flow: WSS-only address, pinned server ID, password
  admission, temporary JoinVoice UDP/ICE check before save, proof-key-only
  persistence, and versioned share/import details.
- Commits: Tasks 1–2 `ca6ed69`, Task 3 `020492f`, Task 4 `5df0524`.

## Task 5: Authorize and prepare a VPS host

- Founder authorizes the VPS-generated public key as a host and transfers
  chain/history through the authenticated host-link model while the VPS remains
  prepared, not independently active.
- Never transfer member private keys. Verify destination server ID, chain head,
  history boundary and password proof before readiness.
- Require empty voice before activation. On failure retain the current active
  host and provide retry/recovery instructions.

## Task 6: Acceptance and release

- Automated gate: root vet/race suite, bootstrap/config/service, TLS/admission
  and transition rollback tests, release cross-builds, and `git diff --check`.
- Disposable-VPS gate: fresh install; DNS/TLS; join; two-way text/audio;
  camera/selected screen; restart persistence; backup/restore; update/rollback;
  blocked-UDP diagnosis; and host failover/recovery.
- AWS gate: security group permits required TCP and UDP, a real remote
  friend/client connects, and no project infrastructure is used.
- Only after these gates pass may docs describe availability and a minor release
  be published.
