# Chat with friends in 5 minutes

This guide is for two kinds of people: the **host**, who runs a Hermec server
for a group of friends, and the **friends**, who just run the app and connect.

## What you'll need

- **One person hosts** a Hermec server (a small program that runs on any
  computer, a home server or a cheap VPS).
- **Everyone else** only runs the Hermec app (`hermec.exe` on Windows) and
  points it at the host's address.
- Headphones for everyone (see [Notes for friends](#what-friends-receive)).

## Connecting to a discovered server

Hermec servers announce themselves on the local network and Tailscale (if the
`tailscale` CLI is on PATH). When you run the app, the connect screen shows
a "DISCOVERED" list of nearby servers—just **click one and you're in** (no need
to type an address). Press **Ctrl+R** to refresh the list.

To label your server so friends recognize it, set `server_name` in your config:

```toml
server_name = "Alice's Game Room"
```

To hide your server from discovery, set `discoverable = false`.

## Fallback: type the address

If you don't see your server in the DISCOVERED list, you can still type its
address manually on the connect screen. This is also the way to reach servers
outside your local network and Tailscale (for example, a public VPS).

The rest of this guide covers the three network setups. Most of the time, you'll
just use discovery—but here's the nitty-gritty if you want to understand it:

## Three ways to connect (traditional paths)

### (a) Same network (LAN)

Everyone is on the same Wi-Fi or router, for example a LAN party. The server is
automatically discoverable; friends will see it in the DISCOVERED list. As a
fallback, find the host's LAN IP with `ipconfig` on Windows (it looks like
`192.168.1.20`), then friends type `ws://<that IP>:7697/` into the server field.

Nothing is exposed to the internet. Leave `public_ip` unset on a LAN.

### (b) Tailscale (recommended)

Tailscale gives your machines a private network on top of the internet. Your
public IP is never exposed, you do not touch your router, and everything
between machines is encrypted in transit by WireGuard.

1. The host and every friend install [Tailscale](https://tailscale.com/download)
   and sign in.
2. The host invites each friend to their tailnet (Tailscale admin console,
   share/invite users, or share the host machine with them).
3. The host starts the server (see below).
4. The server will appear in friends' DISCOVERED lists (if the `tailscale` CLI
   is on PATH). As a fallback, friends can connect to
   `ws://<your-machine-name>.<tailnet>.ts.net:7697/`, for example
   `ws://vito.tail1234.ts.net:7697/`. The host can read the exact name
   in the Tailscale app.

If voice does not connect, check that your Tailscale policy lets friends reach
the host's UDP port too: all voice and video uses UDP 7697, next to TCP 7697
(Tailscale allows all by default). To limit shared friends to just those two
ports, see the grant recipe in the README under "How safe is this?".

### (c) Public internet

Use this only if you cannot use Tailscale.

1. On the host's router, forward **TCP 7697** and **UDP 7697** to the host
   machine (all media uses that one UDP port).
2. In the server TOML set `public_ip` to the host's public IPv4. Optionally
   set `media_port` for a different UDP port, or (older style)
   `udp_port_min` / `udp_port_max` for a UDP range you forward instead.
3. Friends connect to `ws://<host-public-ip-or-domain>:7697/`.

**Honest warning:** chat and signaling travel over plain `ws://` unless you
configure `tls_cert` and `tls_key` (then the server serves `wss://`). Without
TLS, anyone on the path can read chat. Voice media is always encrypted
regardless. This setup exposes the **host's** IP only; participants never see
each other's IPs, because media is relayed through the server by default.

## What the host runs

Easiest: download the server for your machine from the
[Releases page](https://github.com/medeirosvictor/hermec/releases)
(`hermec-server-linux-amd64`, `hermec-server-linux-arm64` or
`hermec-server-windows-amd64.exe`). Check it against `SHA256SUMS.txt` from the
same release before you run it. On Linux:

```sh
sha256sum -c SHA256SUMS.txt --ignore-missing
chmod +x hermec-server-linux-amd64
```

On macOS, use `shasum -a 256 -c SHA256SUMS.txt --ignore-missing` instead
(macOS has no `sha256sum` by default).

On Windows, `certutil -hashfile hermec-server-windows-amd64.exe SHA256` prints
the hash to compare with the line in `SHA256SUMS.txt`.

Or build it yourself. The server is pure Go and cross-compiles, so you can
build it for the machine that will host it:

```sh
GOOS=linux GOARCH=amd64 go build ./cmd/hermec-server
```

Minimal `server.toml` (all options are in
[example.server.toml](../example.server.toml)):

```toml
channels = ["general"]
voice_channels = ["voice"]

# Optional gating (see Privacy below):
# password = "change-me"
# allowed_keys = ["k7mv-q3xp-9dfw-02hj"]
```

Run it:

```sh
./hermec-server -config server.toml
```

It listens on `:7697` by default and logs its address on start. Add
`-print-config` to check the effective config without starting it.

## What friends receive

The host sends each friend the Releases link:
<https://github.com/medeirosvictor/hermec/releases>. Friends download
`hermec-windows-amd64.zip` (or the arm64 zip on a Windows ARM laptop), unzip it
anywhere, and run `hermec.exe`. It is one file. No toolchain and no DLLs are
needed. Mac friends build from source with `scripts/build-mac.sh`; see
[macOS: run from Terminal](../README.md#macos-run-from-terminal).

*Fallback:* if a friend cannot reach GitHub, the host can send them their own
`hermec.exe` directly.

Then friends run:

```sh
hermec.exe -server ws://<address-from-above>:7697/ -name Alice
```

Notes:

- **SmartScreen:** the exe is unsigned, so Windows may warn. Click
  **More info**, then **Run anyway**.
- **Update check:** the app checks GitHub for a newer release at startup and
  shows a banner if there is one. Release builds always run this check; it
  cannot be turned off. The old `update_check` setting is ignored.
- **Use headphones.** Hermec has no echo cancellation; speakers feed other
  people's voices back into your mic.
- **Your identity key:** the first run creates your identity key at
  `<user config dir>/hermec/identity.key` (on Windows, typically under
  `%AppData%`). It *is* your account, so back it up. Lose it and you become a
  new person.
- **Passwords:** the GUI has no password field yet. If the host sets a
  `password`, GUI friends cannot join. Use the allowlist instead: the host
  adds each friend's key fingerprint to `allowed_keys` and restarts.
  (Your fingerprint is shown on the connect and settings screens; send it to the host.)

## Privacy and accountability

- Participants never see each other's IPs; voice media is relayed through the
  server by default.
- The host's address is shared only with the people you invite (or hidden
  entirely on Tailscale or a LAN).
- Every user is an unforgeable key fingerprint, so a host can always identify
  who did what, and can gate entry with `password` or `allowed_keys`.
- The host is trusted by design: they can read chat and relayed traffic, like a
  TeamSpeak host. Pick hosts like you pick group admins.
- There are **no runtime kick or ban commands yet**. Today, removing someone
  means editing the config (for example `allowed_keys`) and restarting the
  server. Runtime moderation is on the roadmap.
