# Hosting

A Hermec server is a small meeting room that one friend keeps open on their own
computer. Everyone else opens the app and connects to it. This page helps you
choose how to host. Flags and config keys are in
[Server configuration](server-configuration.md).

## How people reach the host

Home routers normally block anyone from the outside from reaching your
computer. Hermec uses no outside helper servers (no STUN/TURN), because the
project owns no infrastructure. The app only needs to be able to reach the
host; the host does the rest. Three ways:

- **Same Wi-Fi/LAN:** it just works. The app finds servers automatically by
  sending a small "anyone there?" message on the network.
- **Tailscale (easiest across the internet):** the host shares their machine
  with friends, everyone gets a private address that already reaches the host,
  and the app finds tailnet servers automatically too (when the `tailscale` CLI
  is on `PATH`). Share only the hosting machine with each friend. See
  [Security](security.md#tailscale-grants) to narrow what a shared friend can
  reach.
- **Classic port forwarding:** the host forwards port 7697 (TCP and UDP) on
  their router and sets `public_ip` in the server config.

Voice and video need UDP as well as TCP; see
[ports and firewall](server-configuration.md#ports-and-firewall).

## Choosing a method

| Method | Good for | Notes |
|---|---|---|
| [Host from the app](#hosting-from-the-app) | A private group on LAN or Tailscale | The server runs while the app is open; no password |
| [Friend hosting, standby and failover](#friend-hosting-standby-and-failover) | Keeping the server up when the founder's PC is off | Every friend host must also be node-shared on Tailscale |
| [Standalone `hermec-server`](server-configuration.md) | A machine that stays on (a Raspberry Pi, a home server) | Config file, password, allowed keys, TLS |
| [Dedicated VPS](#dedicated-vps) | A public, always-on, TLS-fronted server run by an operator | Remote acceptance has not been run yet |

## Hosting from the app

Click **+ → Create my own** and give the server a name and a port (7697 by
default). The app starts the server on your computer, opens it as a tile, and
shows an invite address (your Tailscale address if you have one, otherwise your
LAN address) that friends paste into **Join a server**. Windows asks once to
allow Hermec on your network; choose Allow so friends can connect. The server
runs while the app is open. Servers made this way have no password setup UI, so
anyone who can reach the port can join, including other devices on your home
Wi-Fi.

Its signed chain (the server's identity and group settings, with you as
founder) is kept in a `chains` folder next to `hosted.toml` in your user config
folder. Its chat history is kept in a `history` folder there too, one
`<server-id>.jsonl` file per server.

The app has no settings page for the group yet. To change `history_days` or
`history_mb` of a server you host from the app, close the app, write a small
TOML with the keys to change and `addr` set to the server's port, and run
`hermec-server` with your app key and the server's chain file. With `<config>`
your user config folder (`%APPDATA%` on Windows) and `<server-id>` the full ID
(right-click the tile, **id srv-...** copies it):

```sh
# history.toml:
#   addr = ":7697"
#   history_days = 0
hermec-server -config history.toml -apply-config \
  -key <config>/hermec/identity.key \
  -chain <config>/hermec/chains/<server-id>.jsonl
```

The command refuses while the app still listens on that port, and the change
applies the next time the app starts the server.

**Roster controls.**

- **Let my PC host this server.** If the founder approved you as a host,
  right-click the server's tile and switch on "let my PC host this server". The
  app starts a small server beside it (on the first free port from 7697 to
  7707), follows the active host as standby and takes over when needed. The
  choice is saved per server. The first time, the app reminds you that your
  friends must be able to reach your PC (share it on Tailscale).
- **Allow hosting (founder).** Right-click a member in the member list. A member
  who is not a host gets **Allow hosting**. A host gets **Stop hosting** and
  **Move up**. Move up swaps the host with the one above it, but never with the
  founder: priority 1 stays with the founder.

## Friend hosting, standby and failover

A server does not have to depend on one computer. The founder can approve other
members as **hosts**. The approved hosts are listed in the server's signed
chain, each with a priority number (1 is the founder; a lower number is
preferred). A friend who hosts must **node-share their PC on Tailscale** with
the group, the same way the first host does. Their app hosts only while it is
open. A standby host can read retained chat and holds the password proof key, so
authorize only trusted people.

At any moment one host is **active**. Clients connect to it: it relays voice,
passes on chat and keeps the chat history. The other hosts are in one of two
states:

- **Standby.** One host is picked as the standby. It keeps a live copy of the
  chain, the chat history and the server's login key. It also holds a quiet,
  pre-connected session with each client, so taking over needs almost no setup.
- **Idle.** Any other host keeps a link to the active host and a copy of the
  data, but clients do not use it.

The standby is the best-priority host that at least half of the clients can
reach. Clients report this, because a standby that most people cannot reach is
useless.

**Failover.** The active host sends a heartbeat to its clients and linked hosts
every 500 ms. A client that hears nothing for 1.5 seconds asks the standby to
take over. The standby checks that the active host really is silent, becomes
active and sends everyone the new host list. Clients move over by themselves,
and a voice call carries on over a connection that was already set up. The whole
switch takes about a second and needs no clicks. Chat gets a "host changed to
`<name>`" line. Active camera and screen publications, plus the selected screen,
move with the call onto the standby connection.

**Cold start.** A host that starts looks for an active host for a few seconds.
If it finds one, it links to it. If it finds none, it becomes active. A friend
host is seeded with the password key its own session logged in with, so even
when it cold-starts as the active host it still requires the server password. A
host also ignores an active host whose chain is behind its own or no longer
lists that host, and starts as active itself instead.

**Split brain.** If the network splits, two hosts can both think they are
active. Every 30 seconds an active host checks the other hosts it knows. When
two active hosts meet, the one with fewer people in voice yields (a tie goes to
fewer clients, then to the larger priority number). The loser sends its chat
lines to the winner, tells its clients to move to the winner and goes idle. A
chain change made on the losing side (for example a grant) can be lost. Its
author gets a `chain_lost` notice and has to do it again.

**Handback.** When the founder's PC comes back, it does not grab the role in the
middle of a call. After voice has been empty for 2 minutes, the active host
hands the role back to the best-priority host linked to it, and clients move
over.

For a host started by hand, the host flags (`-advertise`, `-hosts`, `-name`) and
`known_hosts.txt` are in
[Server configuration](server-configuration.md#host-flags). A host's key must be
on the chain's host list; on first start the server's key becomes the founder
and host number 1.

## LAN and Tailscale node sharing

1. Host and friends install [Tailscale](https://tailscale.com/download).
2. The host shares the hosting machine (not the whole tailnet) with each friend:
   Tailscale admin → Machines → the PC → Share. To take access back, use
   **Revoke invite** there.
3. Friends pick the server from **DISCOVERED**, or use
   `ws://<machine>.<tailnet>.ts.net:7697/`.

Tailscale encrypts transport and avoids router changes. On a shared LAN no setup
is needed. Optionally restrict shared friends to Hermec's ports with the grant in
[Security](security.md#tailscale-grants).

## Dedicated VPS

For an operator-owned Linux server under systemd, with TLS and admission, see
[Dedicated hosting](dedicated-hosting.md). For a click-by-click disposable AWS
EC2 test setup, see [From no AWS account to a disposable Hermec
VPS](aws-ec2-from-zero.md). Hermec does not create cloud resources or take cloud
credentials. Choose **+ → Dedicated server** in the app to join or manage one.
