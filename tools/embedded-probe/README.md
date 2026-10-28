# HERMEC embedded-network probe

This nested Go module is an experimental transport adapter for the approved
embedded Tailscale/Headscale proof. It is not linked into HERMEC release
binaries and does not provide a user-facing hosting mode yet.

## Boundaries

- One `Node` owns one persisted tsnet state directory.
- `ProfileConfig.ControlURL` selects Tailscale or an operator-supplied Headscale
  coordination server.
- `Open` waits for an assigned overlay address. Login URLs are delivered only
  through `OnLoginURL`; the adapter does not log them.
- TCP signaling, server listeners and UDP media use that node. Pion sees only a
  synthetic interface containing the node's assigned addresses.
- Binds to host/LAN addresses and writes to addresses absent from the profile's
  startup peer snapshot fail closed. There is no system-network fallback.
- Closing one node releases only its state-directory registration and backend.

The peer snapshot is intentionally proof-stage behavior. A production adapter
must refresh peer reachability as membership changes and define how MagicDNS
names are admitted without weakening literal UDP destination checks.

## Local tests

```sh
go test ./...
go test -race ./...
```

Tests use disposable fake backends and an in-memory packet network. They do not
enroll a device, contact Tailscale, or use the installed system client.

## Disposable real-network acceptance

The CLI runs the same authenticated HERMEC chat/media/failover path used by the
loopback test. It requires disposable state and an operator-owned disposable
Tailscale or Headscale network. Never point it at a personal tailnet or a real
HERMEC configuration.

```sh
go run ./cmd/embedded-probe \
  -state ./tmp/disposable-proof \
  -control-url https://controlplane.example \
  -hostname hermec-proof \
  -role all \
  -port 17697 \
  -report ./tmp/proof-report.json
```

Omit `-control-url` for Tailscale. The command opens three independent profiles
(host, client and standby), prints each enrollment URL only to the interactive
terminal, and waits for approval. Enrollment URLs and credentials are never
written to the JSON report. `-state` is mandatory; a non-empty directory without
the probe marker is rejected. Individual `-role host|client|standby` runs can be
used to enroll and inspect one profile before the complete `-role all` run.
`-peers` accepts comma-separated overlay IPs that must appear in the startup
peer snapshot.

Run and record each available row; leave unavailable rows as **NOT RUN**:

| Coordination | Path | Required checks |
|---|---|---|
| Tailscale | direct | no installed system client; authenticated signaling; audio both ways; both cameras; selected screens; standby takeover |
| Tailscale | DERP relay | same checks, with relay path confirmed independently |
| Headscale | direct | same checks against an operator-supplied Headscale URL |
| Headscale | relay | same checks with the configured DERP path identified |
| Either | two groups | two `-role all` state roots concurrently; no shared state or cross-group address |
| Either | restart | rerun marked state; no new identity or silent fallback |
| Either | denied/revoked | actionable enrollment failure; no system socket fallback |
| Either | coordination unavailable | bounded failure and clean socket release |

The local automated scenario already checks that signaling alone cannot mark a
standby ready, requires decoded audio/camera/screen delivery in both directions,
preserves mute and selected media over takeover, and releases ports after
cancellation. Real-network rows remain required before shipping embedded
networking to users.

## Dependency and license

The probe pins `tailscale.com v1.104.2` and uses its required Go 1.27.3 patch
toolchain. Tailscale's module is BSD-3-Clause; transitive notices remain governed
by their respective modules. Before any code moves into release artifacts, the
proof report must record the complete dependency/license inventory, binary-size
cost, and supported-platform build results.
