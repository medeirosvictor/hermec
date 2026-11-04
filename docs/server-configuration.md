# Server configuration

Reference for running `hermec-server` by hand: flags, the config file, the
signed group configuration, roles, history, ports and public mode. For choosing
how to host, see [Hosting](hosting.md). Every config key, with comments, is in
[example.server.toml](../example.server.toml); unknown keys are rejected.

## Running

```sh
./hermec-server                                # defaults: ws on 127.0.0.1:7697, channel "general"
./hermec-server -config example.server.toml    # with a config file
./hermec-server -config server.toml -print-config   # show effective config and exit
./hermec-server -config server.toml -apply-config   # append the TOML's group settings to the chain and exit (server stopped)
./hermec-server -config server.toml -name pi -advertise ws://100.64.0.7:7697/ -hosts ws://100.64.0.9:7697/
```

The server logs its listen address on start and shuts down cleanly on Ctrl-C.
Voice channels need configuring (`voice_channels = ["voice", "private-call"]`);
the example config has one.

### Flags

| Flag | Meaning |
|---|---|
| `-config` | Path to a TOML config file (defaults apply without it) |
| `-print-config` | Print the effective config (password redacted) and exit |
| `-check` | Validate config, TLS and persistent state without binding sockets |
| `-apply-config` | Append the config file's group fields to the chain, print the count, and exit; see [below](#signed-group-configuration) |
| `-key` | Host key file (default `hermec-server.key` next to `-config`; generated when missing) |
| `-chain` | Server chain file (default `chain.jsonl` next to `-config`) |
| `-history` | Chat history file (default `history.jsonl` next to `-config`) |
| `-advertise`, `-hosts`, `-name` | Multi-host flags, see [Host flags](#host-flags) |
| `-version` | Print the version and exit |
| `-unsafe-public-ws` | Development only: allow public or wildcard listeners without TLS/admission |
| `-init-dedicated`, `-dedicated-*` | Dedicated VPS bootstrap, cutover and attestation; see [Dedicated hosting](dedicated-hosting.md) |

Without `-config`, the default files live in the current directory.

### Host flags

These matter when a server has more than one host (see
[failover](hosting.md#friend-hosting-standby-and-failover)):

- `-advertise`: the `ws://` URLs where clients and other hosts reach this host.
  Default: one URL per non-loopback IP address, with the listen port.
- `-hosts`: URLs of the other hosts of this server.
- `-name`: the name shown as "hosted by". Default: the machine's hostname.
- `known_hosts.txt`, next to the config file: the host URLs this host has
  learned, one per line. The host adds to it by itself. It is merged with
  `-hosts` and the `known_hosts` key of the config.

A host's key must be on the chain's host list. On first start the server's key
becomes the founder and host number 1; the founder adds others with **Allow
hosting**.

## Identity: key and chain

The server's identity lives in a signed chain. On first start the server creates
the chain from its config, with its own key as founder. Back up both the key and
the chain; the key is what lets you change the group later. The server refuses
to start when its key is not an approved host of the chain it loads. If a crash
left a half-written last entry in the chain file, the server drops it, logs a
warning and keeps the entries before it.

## Signed group configuration

The group settings are `server_name`, `channels`, `voice_channels`, `max_voice`,
`max_clients`, `max_per_ip`, `allowed_keys`, `roles`, `default_roles`,
`history_days`, `history_mb` and `[grants]`. They seed the chain on first start.
Once a chain exists they are ignored in the TOML, and the server logs a warning
when it sees them.

To change them from the file, edit the TOML and run
`hermec-server -config server.toml -apply-config`. Stop the server first: the
command refuses to run while something listens on the config's port, and the
changes apply when the server starts. It needs `-config`, appends only the group
keys the TOML actually sets (grants are compared only when `[grants]` is set),
signed by the server's key, then prints what it appended and exits.

Entries that an admin's app sends to a running server with `chain_append`
(grants, bans, a new config) are different: `allowed_keys`, roles, bans,
`history_days` and `history_mb` apply at once, and the name, channels and the
other limits from a later config apply at the next start. The founder always
keeps every permission and always passes `allowed_keys`, as does every approved
host.

The password key (`password_key`, hex), port, TLS and the other machine settings
stay in the TOML and never go into the chain. Plaintext passwords are never
accepted on the wire or in server configuration.

## Roles, permissions and bans

`default_roles` (default `["user"]`) go to fingerprints with no entry in
`[grants]`. The builtin roles are `admin`, `user` and `bot`; `[roles]` entries
add new roles or replace a builtin role's permissions. Permissions are
`join_channel`, `send_chat` and `manage`. `[grants]` maps a fingerprint to
roles:

```toml
[roles]
listener = ["join_channel"]

[grants]
"k7mv-q3xp-9dfw-02hj" = ["admin"]
```

The server and protocol support banning a key: a banned key is disconnected at
once and refused when it comes back. Founders and admins ban, unban and
make or remove admins from the person and server-tile menus (see
[Moderation](using-hermec.md#moderation)); there is no kick apart from a ban.
A ban keeps the banned member's past messages. `allowed_keys` restricts who may
connect at all (empty or omitted means an open server).

## Limits

| Key | Default | Meaning |
|---|---|---|
| `max_voice` | 25 (max 50) | Most participants in one voice channel; a further `voice_join` is refused with `voice_full` |
| `max_clients` | 256 | Most authenticated sessions; a further client is refused with `server_full` |
| `max_per_ip` | 16 | Most simultaneous connections from one remote IP (raise for a LAN party or an office behind one NAT) |
| `msg_rate`, `msg_burst` | 30, 60 | Messages per second sustained and burst bucket; either 0 disables rate limiting |

Also fixed: 64 connections awaiting auth, and an IP is refused for a growing
time after 5 failed logins in a minute.

## Chat history

Chat in text channels is kept in a JSON-lines file (`-history`). The group keys
`history_days` (default 30; 0 keeps nothing and deletes the file; at most 3650)
and `history_mb` (default 8; 1 to 64) cap it.

- Lines older than `history_days` are dropped at start, every hour, and at once
  when a config entry changes the limits.
- The size cap holds all the time: once the stored chat passes `history_mb`,
  each new message pushes out the oldest, and the file is rewritten whenever it
  has grown past twice `history_mb`.
- A member who joins a text channel receives the newest 100 messages, and the
  app asks for older pages as you scroll up.
- Lines in the file that cannot be read are skipped and dropped at the next
  trim.

Chat history is plain unsigned JSONL on the host.

## Admission and password

- `password_key` (hex): every client must prove it knows the password behind
  this key (an HMAC proof over both sides' random numbers and the server ID).
  The server stores only a scrypt-derived key.
- `allowed_keys`: only these key fingerprints may connect.

Ordinary `ws://` Join and app-created local-server flows do not provide
password setup or join UI.

## Ports and firewall

- The listen address is `addr` (default `127.0.0.1:7697`, safe for
  development). Bind a specific private LAN address for LAN development.
- All WebRTC media uses **one** UDP port, by default the same number as the
  listen port (UDP 7697 next to TCP 7697). `media_port` picks a different one
  (0 = default). Allow both TCP and UDP through any firewall; TCP-only rules let
  signaling through but calls will not work.
- `public_ip`: the public IPv4 to advertise for media on an internet-facing host
  (1:1 NAT); omit it on a LAN.
- Discovery: servers answer UDP probes on the same port number as `addr`
  (`discoverable = true` by default; `false` binds no discovery socket) and
  announce on Tailscale if the `tailscale` CLI is on `PATH`. `server_name` (at
  most 64 characters) labels the server in discovery replies.

For a classic home setup, forward 7697 TCP and UDP on the router and set
`public_ip`.

## Public mode, TLS and admission

- `tls_cert` and `tls_key`: set both to serve `wss://`, or neither for
  loopback/LAN development.
- A wildcard or public listen address is fail-closed unless the server has TLS,
  admission (password or `allowed_keys`), `public_dns`, and
  `discoverable = false` (or the explicitly unsafe development override
  `-unsafe-public-ws`).
- Public listeners require a trusted certificate and a DNS name it covers
  (`public_dns`). `public = true` is also required for a dedicated
  configuration.

Use TLS for any non-loopback deployment, unless a network layer already encrypts
and restricts access (Tailscale/WireGuard or a trusted LAN). See
[Security](security.md#known-gaps) and, for a full operator setup,
[Dedicated hosting](dedicated-hosting.md).
