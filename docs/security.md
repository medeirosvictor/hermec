# Security

Hermec's threat model, what is protected, what the host can see, and the known
gaps. The wire-level details are in [the protocol](protocol.md) (handshake in
[§3.1](protocol.md#31-handshake)).

## Threat model

- **The host is trusted by design.** The room host can read all chat and relayed
  traffic, the same way a TeamSpeak host can. The host also sees who connects
  and when. Choose hosts like you choose group admins. Only join servers run by
  people you trust.
- **Use TLS or an encrypted network.** Transport encryption is required for
  Hermec's threat model. Configure `tls_cert` and `tls_key` for any non-loopback
  deployment, unless a network layer already encrypts and restricts access (a
  Tailscale/WireGuard network or a trusted LAN). Plain `ws://` over the public
  internet means an on-path observer can read everything, including chat.
- **End-to-end encryption is not built.** Voice is encrypted between you and the
  host (WebRTC), and chat is encrypted only if the server uses TLS or the
  network layer does.
- **Participants' IPs stay hidden from each other.** The server relays media, so
  friends see only the host's address.
- **No echo cancellation or noise suppression** is in the app: use headphones.

## What is protected today

- **Encrypted on the wire.** Tailscale wraps all traffic between devices in
  WireGuard encryption, even when it goes through Tailscale's relay servers (the
  relays cannot read it). Voice is also encrypted by WebRTC on top.
- **Friends reach one machine only.** A friend you share your PC with sees only
  that PC, not your other devices. Your PC cannot open connections into theirs.
- **You can take access back.** Tailscale admin → Machines → your PC → Share →
  Revoke invite.
- **Keys, not passwords.** Every person has a key file. The server checks a
  signature from that key when they connect.
- **Clients do not send the password.** The server keeps only a key made from the
  password with scrypt (a slow, memory-hungry function, so guessing is
  expensive). Clients send an HMAC proof over both sides' random numbers and the
  server ID, so a recorded proof cannot be reused. Plaintext passwords are never
  accepted on the wire or in server configuration.
- **The host proves itself first.** Before your app sends anything secret
  (including a password proof), the host sends the server's signed chain and
  proves it holds a key that the chain approves as a host. A machine that merely
  copied the address cannot pass.
- **Your app remembers the server.** Each server has an ID (the hash of the first
  entry of its chain). The app pins it the first time you connect and, if the ID
  ever changes, refuses to connect, tells you so and does not retry. Your login
  signature also covers the server ID, so once the real server's ID is pinned,
  one host cannot replay your login to log in as you on another server. On first
  use of a server you add, a malicious server could still relay another server's
  handshake (see [gap 6](#known-gaps)).
- **Bans are enforced by the server.** A banned key is disconnected at once and
  refused when it comes back. There is no app or command-line control for bans
  yet.
- **Signed releases.** The app installs updates only from artifacts signed by
  the project; see [Release signing](release-signing.md).

## Identity and keys

The first run creates an Ed25519 identity key at
`<user config dir>/hermec/identity.key`. Your **fingerprint**, a short code made
from that key, is your identity; your name is only a label you can change and is
not verified. Hover a server or person to see the fingerprint. Back the key up:
if you lose it you start over as a new person, and anyone with it can be you.
Two clients using one key silence each other; pass `-key` with another file for
a second client.

A server's identity is its signed chain, with its own key as founder; the key
and chain are what let you change the group later (see
[Server configuration](server-configuration.md#identity-key-and-chain)). Never
transfer founder or member private keys to a VPS.

## What the host can see

Chat and voice pass through the host's server. Chat is kept: the active host and
its standby can read and keep chat for up to `history_days`; set it to 0 to keep
nothing. Every host also holds the server's login key (the scrypt key above), so
any approved host can check passwords. The founder decides who is a host. Active
camera and screen media move with the call on failover. Messages are not signed
by their authors, so a host can forge or drop lines. There are no edits or
deletes, and a ban keeps the banned member's past messages. The Hermec log never
contains chat text or keys.

## Tailscale grants

Tailscale's default policy lets a shared friend reach every port on your PC, not
just Hermec. You can narrow it to Hermec, because all voice and video media uses
one UDP port, the same number as the server's port (UDP 7697 next to TCP 7697).
In the Tailscale admin console, open **Access controls**. The default grant lets
everyone reach everything. Do not delete it (that would lock out your own
devices). Instead, change its `src` from `"*"` to `"autogroup:member"` (people
in your own tailnet), and add a second grant for shared users. The `grants` list
then looks like this:

```json
"grants": [
  {"src": ["autogroup:member"], "dst": ["*"], "ip": ["*"]},
  {"src": ["autogroup:shared"], "dst": ["*"], "ip": ["tcp:7697", "udp:7697"]}
]
```

Shared friends can then reach only TCP and UDP 7697 on your devices. If you run
on another port, use that port number instead of 7697; `media_port` in the
server config picks a different media port if you need one.

## Known gaps

1. **A shared friend can reach every port on your PC, not just Hermec**, unless
   you narrow Tailscale as [above](#tailscale-grants).
2. **Admission depends on how the server was created.** Standalone configuration
   supports `password_key` and `allowed_keys` (see
   [example.server.toml](../example.server.toml)). App-created local servers have
   no password setup UI, so anyone who can reach that port can join, including
   other devices on your home Wi-Fi.
3. **Windows Firewall on public Wi-Fi.** If you allowed Hermec on "Public"
   networks, your server is reachable on any café Wi-Fi your PC joins. To allow
   only Tailscale and your home network, run PowerShell as admin (use your own
   path to `hermec.exe`):

   ```powershell
   Get-NetFirewallApplicationFilter -Program "C:\Apps\hermec\hermec.exe" |
     Get-NetFirewallRule |
     Set-NetFirewallRule -RemoteAddress 100.64.0.0/10,LocalSubnet
   ```

   Drop `,LocalSubnet` to allow Tailscale only.
4. **The host can read everything**, see [above](#what-the-host-can-see).
5. **Names are not verified.** Anyone can pick any name. The fingerprint is the
   real identity.
6. **The handshake does not bind the connection.** On plain `ws://`, someone on
   the network path between you and the host could relay a real host's proof and
   so read your chat. Use TLS (set `tls_cert` and `tls_key` in the server config;
   the server then serves `wss://`) or a network that is already encrypted.
   Tailscale is, and it is what this friend group uses. Without one of them,
   treat chat as visible to anyone on the path.
7. **Not ready for strangers on the open internet.** There are no invite links
   yet, so a public server cannot tell welcome visitors from unwelcome ones
   (`allowed_keys` helps, and bans work after the fact, though the app has no ban
   control yet). Reach servers through Tailscale or your LAN. Remote dedicated
   hosting has not passed its acceptance run (see
   [Dedicated hosting](dedicated-hosting.md)).
