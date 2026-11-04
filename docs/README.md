# Hermec documentation

## Guides

| Guide | What it covers |
|---|---|
| [Getting started](getting-started.md) | Install per OS, first run, joining a server, hosting from the app |
| [Using Hermec](using-hermec.md) | The app: servers, chat, voice, volume, devices, screen share, camera, settings, themes, shortcuts, updates |
| [Hosting](hosting.md) | Choosing a hosting method: from the app, friend hosting and failover, LAN/Tailscale, VPS |
| [Server configuration](server-configuration.md) | `hermec-server` flags, `server.toml`, signed group config, roles, history, ports, TLS |
| [Security](security.md) | Threat model, what the host can see, identity and keys, Tailscale grants, known gaps |
| [Troubleshooting](troubleshooting.md) | No audio, blocked UDP, firewall prompts, version mismatch, logs |
| [Development](development.md) | Build from source, run locally, tests, releasing, package layout |

## Operators and releases

| Document | What it covers |
|---|---|
| [Dedicated hosting](dedicated-hosting.md) | Running Hermec as an operator-owned Linux systemd service |
| [AWS EC2 from zero](aws-ec2-from-zero.md) | Click-by-click disposable EC2 test setup |
| [Release signing](release-signing.md) | How releases are signed, verified by hand, and the key rotated |

## Reference and design

| Document | What it covers |
|---|---|
| [Protocol](protocol.md) | The versioned wire protocol |
| [research/](research/) | Measurements and feasibility studies (footprint, embedded networking, hosting and noise suppression) |
| [superpowers/](superpowers/) | Design specs and implementation plans |
| [PLAN.md](../PLAN.md) | Roadmap |
| [example.server.toml](../example.server.toml), [example.theme.toml](../example.theme.toml) | Commented config examples |
