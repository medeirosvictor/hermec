# Chat with friends in 5 minutes

This is the canonical onboarding guide for released LAN/Tailscale hosting.
Dedicated/public hosting is unreleased; do not use its development guide for
public onboarding until its release gate passes.

## Choose how friends reach the host

- **Same LAN:** start a server and friends select it in **+ → Join →
  DISCOVERED**. If needed, enter `ws://<LAN-IP>:7697/`.
- **Tailscale (recommended for a private group):** host and friends install
  [Tailscale](https://tailscale.com/download); the host shares only the hosting
  machine with each friend. The discovered list should show it, or friends use
  `ws://<machine>.<tailnet>.ts.net:7697/`. Tailscale encrypts transport and
  avoids router changes. Voice/video need UDP 7697 as well as TCP 7697; see the
  [README security guidance](../README.md#how-safe-is-this-tailscale) before
  narrowing Tailscale grants.

## Host a private group from the app

1. Click **+ → Create my own**, choose a name and port (7697 by default), and
   allow the Windows firewall prompt when appropriate.
2. Share the invite address from the server tile's right-click menu. On
   Tailscale, share the host machine first.
3. Friends use **+ → Join**, select a discovered server or paste that address.

App-created local servers have no password. For standalone/server configuration,
security details, and source builds, see the [README](../README.md#chat-with-friends).

## What friends do

1. Download the app from [Releases](https://github.com/medeirosvictor/hermec/releases).
   Choose `hermec-windows-amd64.zip` or `hermec-windows-arm64.zip` on Windows;
   Linux uses `hermec-linux-amd64.tar.gz` or `hermec-linux-arm64.tar.gz`.
   macOS users build as described in the [README](../README.md#macos-run-from-terminal).
2. Extract and run it (`hermec.exe` on Windows; `./hermec` on Linux). Windows
   may require **More info → Run anyway** because the executable is unsigned.
3. Join using the connection method above, then use headphones and select a
   voice channel.

The first run creates `<user config dir>/hermec/identity.key`. Back it up: it is
your identity. The app checks GitHub for updates in release builds and links to
the Releases page; replacement remains manual.

## Friend-host standby (optional)

The founder can right-click a member and choose **Allow hosting**. That friend
shares their PC with the group on Tailscale, then enables **let my PC host this
server** from the server tile. Their app hosts only while it is open. A standby
host can read retained chat and holds the password proof key, so authorize only
trusted people. See [hosting and failover](../README.md#hosting-standby-and-failover).

## Privacy reminders

- Participants do not see one another's IPs; the server relays media.
- The host can read chat and relayed traffic. Choose hosts like group admins.
- Password login sends a proof, not the plaintext password. Do not share a
  standalone server password in the same channel as connection details.
- Hermec has no echo cancellation: headphones prevent feedback.
