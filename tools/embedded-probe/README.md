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

## Real-network use

There is no real-network CLI yet. Task 4 of
`../../docs/superpowers/plans/2026-10-26-embedded-network-proof.md` adds the
headless scenario and explicit operator steps. Never point that scenario at a
personal tailnet or persistent HERMEC configuration.

## Dependency and license

The probe pins `tailscale.com v1.104.2` and uses its required Go 1.27.3 patch
toolchain. Tailscale's module is BSD-3-Clause; transitive notices remain governed
by their respective modules. Before any code moves into release artifacts, the
proof report must record the complete dependency/license inventory, binary-size
cost, and supported-platform build results.
