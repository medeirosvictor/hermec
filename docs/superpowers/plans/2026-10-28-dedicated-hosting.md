# Dedicated AWS/VPS Hosting Implementation Plan

**Status:** Next user-facing hosting milestone after the transport-proof branch.

**Goal:** Make an operator-owned Linux VPS or AWS EC2 instance usable like a
Minecraft server: download HERMEC, create a persistent config/state directory,
run a service, open TCP/UDP, and share verified connection details. HERMEC does
not provision cloud resources or receive cloud credentials.

**Availability definition:** AWS/remote hosting is available when Tasks 1-6 are
merged and a release build passes the disposable-VPS checklist. Documentation
alone is not availability.

## Constraints

- Public signaling is `wss://`; media UDP must be reachable separately.
- Keep server ID, signed chain and host authorization. Never copy a founder or
  member private key from a desktop to the VPS; the VPS creates its own key and
  is authorized as a host.
- Admission is required. First release supports the existing password proof and
  allowlist; it must not present an open public server as safe.
- No AWS SDK, access keys, billing API or in-app VM creation.
- Same-build peers are expected before v1.
- State, logs, backups and upgrades must be explicit and recoverable.

## Task 1: Dedicated server bootstrap and validation

- Add a non-interactive bootstrap command that creates a dedicated state layout,
  host identity, strict example config and service-ready paths without starting
  an open server.
- Refuse public/wildcard startup without TLS and admission unless the operator
  passes a plainly named unsafe development override.
- Validate DNS/TLS paths, TCP/media UDP ports, history/chain paths, file modes
  and conflicting options before binding.
- Add tests for first run, repeat run, partial failure, permissions, invalid TLS,
  open admission refusal and no identity replacement.

## Task 2: Packaging and operator lifecycle

- Ship Linux amd64/arm64 dedicated bundles containing `hermec-server`, an example
  config, systemd unit template, update/rollback script and backup/restore guide.
- Add an OCI container option only if persistent identity/chain/history mounts
  and UDP exposure remain obvious; do not make Docker mandatory.
- Support graceful service stop, logs, health/status output and version checks.
- Test upgrade and rollback while preserving identity, chain and history.

## Task 3: AWS/VPS guide and reachability verifier

- Add a provider-neutral VPS guide plus an AWS EC2 example: instance type
  guidance, DNS, certificate, security-group TCP/UDP rules, persistent disk,
  systemd, logs, backups and egress-cost warning.
- Add a verifier that checks authenticated `wss://` signaling and an actual
  media path from a second endpoint. A successful local bind is not remote
  reachability.
- Report friendly remediation and retain raw diagnostics without passwords,
  password keys or private keys.

## Task 4: Desktop Join/Create Dedicated flow

- In Add a Server, add **Dedicated server**. Explain that the operator prepares
  the machine outside HERMEC, then enters/imports its address and server ID.
- Add a password field and password-proof persistence appropriate for joined
  servers; the plaintext password must not travel or be written to logs.
- Validate `wss://`, pinned server identity, admission and media reachability
  before saving the tile. Failed drafts leave no phantom server.
- Finish with copyable/shareable versioned connection details.

## Task 5: Authorize and prepare a VPS host

- Provide a founder-controlled flow to authorize the VPS-generated public key
  as a host, transfer chain/history through the existing authenticated host-link
  model, and keep it prepared rather than independently active.
- Never transfer member private keys. Verify destination server ID, chain head,
  history boundary and password proof before readiness.
- Voice must be empty before activation. Failure leaves the current host active
  and offers retry/recovery instructions.

## Task 6: Acceptance and release

- Automated: root vet/race suite, bootstrap/config/service tests, TLS/admission
  tests, transition rollback tests, release cross-builds and `git diff --check`.
- Disposable VPS: fresh install, DNS/TLS, client join, text, audio both ways,
  camera, selected screen, restart persistence, backup/restore, update/rollback,
  blocked UDP diagnosis and host failover/recovery.
- AWS row: EC2 security group with required TCP and UDP, no project-owned
  infrastructure, actual remote friend/client connection.
- Update README/HANDOFF only with behavior actually shipped; publish the next
  minor release after the gate passes.
