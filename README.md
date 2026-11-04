# Hermec

Hermec is a small, open-source, censorship-resilient communications app,
"simple like TeamSpeak": voice channels with screen sharing and group text chat,
served by lightweight servers that anyone can host. The project owns no
infrastructure; resilience comes from many small, independently hosted servers
rather than one central service that can be blocked.

Hermec is also meant to be a reusable base for communications apps, with a
role-gated Go core and a versioned, documented wire protocol.

**Principles**

- Anyone can self-host; Hermec operates no infrastructure.
- The server relays media, so participants' IPs stay hidden from each other. The
  host is trusted, the way a TeamSpeak host is.
- Identity is an Ed25519 key file, not an account or a name.
- Every peer runs the same build; there is no cross-version compatibility before
  1.0.
- Small codebase and small downloads (Go, Pion, Ebiten).

## Features

- Voice channels, screen sharing (watch on Windows, macOS and Linux; share from
  Windows) and Windows webcam publishing, with a multi-participant call view
- Text channels with server-side history
- Keys instead of passwords or accounts; optional server password and allowlist
- Per-person volume, mic gain, mid-call device switching, auto-rejoin
- Host from the app, with friend hosts, standby and automatic failover
- Finds servers on your LAN and Tailscale; dedicated Linux server tooling
- Signed in-app updates on Windows and Linux
- A retro CRT look with themes, or turn it off

## Quick start

1. Download the build for your computer from the
   [Releases page](https://github.com/medeirosvictor/hermec/releases) (Windows
   and Linux, x64 and ARM; macOS builds from source).
2. Unzip or unpack it and run `hermec` (`hermec.exe` on Windows).
3. Click **+** to join a friend's server, or **Create my own** to host one.
4. Click a voice channel, and put on headphones: there is no echo cancellation.

The full walkthrough is in [Getting started](docs/getting-started.md).

## Documentation

- [Getting started](docs/getting-started.md): install per OS, first run, joining
  and hosting.
- [Using Hermec](docs/using-hermec.md): servers, chat, voice, screen share,
  settings, themes, shortcuts, updates.
- [Hosting](docs/hosting.md): pick a hosting method; friend hosting and failover.
- [Server configuration](docs/server-configuration.md): `hermec-server` flags,
  config keys, roles, history, ports, TLS.
- [Security](docs/security.md): threat model, what the host can see, known gaps.
- [Troubleshooting](docs/troubleshooting.md): audio, firewall, updates, logs.
- [Development](docs/development.md): build, run, test, release, code layout.
- [Dedicated hosting](docs/dedicated-hosting.md) and
  [AWS EC2 from zero](docs/aws-ec2-from-zero.md): operator-run VPS servers.
- [Release signing](docs/release-signing.md): how releases are signed and
  verified.
- [Protocol](docs/protocol.md): the wire protocol.
- [All docs](docs/README.md), including design notes and research.

## Roadmap

See [PLAN.md](PLAN.md).

## License

The repository does not carry a license file yet. The bundled fonts are under
the SIL Open Font License 1.1 (licences in `ui/theme/`).
