# Getting started

Install Hermec, join a friend's server or host your own, and make a first call.
For a tour of the app afterwards see [Using Hermec](using-hermec.md).

## Install

Download the latest build from the
[Releases page](https://github.com/medeirosvictor/hermec/releases). Pick the app
that matches your computer:

| Download | For |
|---|---|
| `hermec-windows-amd64.zip` | Windows, most Intel/AMD PCs (x64) |
| `hermec-windows-arm64.zip` | Windows on ARM, such as Snapdragon laptops |
| `hermec-linux-amd64.tar.gz` | Linux, most Intel/AMD PCs (x64) |
| `hermec-linux-arm64.tar.gz` | Linux on ARM, such as Raspberry Pi and other ARM boards |

Hermec needs a 64-bit OS. Linux builds need glibc 2.35 or newer (Ubuntu 22.04+,
Debian 12+); 32-bit Raspberry Pi OS won't work. There is no macOS download, see
[macOS](#macos-build-and-run-from-terminal) below.

### Windows

1. Download the zip for your PC.
2. Unzip it anywhere.
3. Run `hermec.exe`.

It is one file. No toolchain and no extra DLLs are needed. The exe is unsigned,
so Windows SmartScreen may warn: click **More info**, then **Run anyway**. If
Windows Firewall asks, choose **Allow** (see
[Troubleshooting](troubleshooting.md#windows-firewall-prompts)).

### Linux

1. Download the `.tar.gz` for your machine.
2. Unpack it: `tar xzf hermec-linux-amd64.tar.gz` (or
   `hermec-linux-arm64.tar.gz` on ARM).
3. Run `./hermec`.

You need a desktop with X11 or Wayland, and PulseAudio or PipeWire for sound.
Copy and paste need one of `xclip`, `xsel` or `wl-clipboard` (for example,
`sudo apt install xclip`).

### macOS: build and run from Terminal

There is no Mac download. Apple signing costs money, and Hermec stays free. So
you build it yourself. It takes a few minutes.

1. Install the command line tools: `xcode-select --install`
2. Install Homebrew from <https://brew.sh>.
3. Install Go and git: `brew install go git`
4. Get the code:
   `git clone https://github.com/medeirosvictor/hermec.git && cd hermec`
5. Build the latest release: `scripts/build-mac.sh`
   The script fetches the release tags, checks out the newest one, and builds
   `./hermec` with its version number set, so the update banner works. The
   first build downloads Go 1.27 automatically if Homebrew's Go is older.
6. Run it: `./hermec`
7. The first time you use voice, macOS asks to let Terminal use the microphone.
   Allow it. You can change it later in System Settings → Privacy & Security →
   Microphone.

To update, run `scripts/build-mac.sh` again. The script checks out a release
tag, so the repo is on a detached HEAD; to go back to the development branch,
run `git checkout main`. Other build details are in
[Development](development.md).

### Verifying a download

Releases ship a `SHA256SUMS.txt` and a detached Ed25519 signature,
`SHA256SUMS.txt.sig`. The app checks them itself before it self-updates; to
check a download by hand, see [Release signing](release-signing.md#checking-a-release-by-hand).

## First run

The first run creates your identity key at
`<user config dir>/hermec/identity.key` (`%APPDATA%` on Windows). It is your
identity: servers recognise you by the fingerprint made from it, not by your
name, so back it up. If you lose it, you start over as a new person. A new
install opens on the green CRT look with scanlines ([themes](using-hermec.md#themes)).

Put on headphones: Hermec has no echo cancellation, so speakers feed other
people's audio back into your microphone.

`hermec -version` prints the version of your build. Every client and host in a
group must run the same build, so update together (see
[In-app updates](using-hermec.md#in-app-updates)).

## Join a server

Click the **+** tile and choose **Join a server**.

- **Same LAN:** servers appear by themselves under **DISCOVERED** (**Ctrl+R**
  refreshes). If needed, enter `ws://<LAN-IP>:7697/`.
- **Tailscale (recommended for a private group):** the host and friends install
  [Tailscale](https://tailscale.com/download) and the host shares only the
  hosting machine with each friend. The discovered list should show it, or use
  `ws://<machine>.<tailnet>.ts.net:7697/`. Tailscale encrypts transport and
  avoids router changes. Voice and video need UDP 7697 as well as TCP 7697; see
  [Security](security.md#tailscale-grants) before narrowing Tailscale grants.
- **Operator-run VPS:** choose **+ → Dedicated server** and follow
  [Dedicated hosting](dedicated-hosting.md).
- **Anything else:** paste the `ws://` or `wss://` address the host gave you.
- **An invite link:** if the host sent a `hermec://join?...` link, click **+**,
  click the **invite link** line and press **Ctrl+V** (Cmd+V on macOS). A
  `ws://` link joins at once with the server's ID pinned. A `wss://` link opens
  **Dedicated server** with the address, server ID and (if the link has one)
  password filled in; press **Validate / Connect** to run the usual checks. A
  link that does not parse shows a short message and nothing is dialed. Enter
  your name under **Join a server** first if the app has none saved.

Then click a voice channel to join the call. Privacy reminders: participants do
not see one another's IPs (the server relays media), but the host can read chat
and relayed traffic, so choose hosts like group admins. Password login sends a
proof, not the plaintext password; do not share a server password in the same
channel as the connection details. A hand-written invite link with `pw=` holds
the server password and is a secret: send it only over a private channel.

## Host a server from the app

1. Click **+ → Create my own**, choose a name and a port (7697 by default), and
   allow the Windows Firewall prompt when it appears.
2. The app starts the server on your computer and opens it as a tile. Share the
   invite link from the tile's right-click menu: it is your Tailscale address
   if you have one, otherwise your LAN address. On Tailscale, share the host
   machine with your friends first.
   The menu item is **Copy invite link**: it copies one
   `hermec://join?v=1&addr=...&id=...&name=...` line holding the address, the
   server's ID and its name.
3. Friends paste that link into the **invite link** line on **+**, or use
   **+ → Join a server** and select the discovered server or paste the address.

The server runs while the app is open. App-created servers have no password.
To keep a server up while you are away, or to host on a VPS, see
[Hosting](hosting.md).

## Next

- [Using Hermec](using-hermec.md): servers, voice, screen share, settings.
- [Hosting](hosting.md): host methods, friend hosting and failover.
- [Troubleshooting](troubleshooting.md): no audio, firewall, version mismatch.
