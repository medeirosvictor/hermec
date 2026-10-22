# Hermec

Hermec is a small, open-source, censorship-resilient communications app,
"simple like TeamSpeak": voice channels with screensharing and group text chat,
served by lightweight servers that anyone can host. The project owns no
infrastructure; resilience comes from many small, independently hosted servers
rather than one central service that can be blocked.

Media is relayed by the server, so participants' IPs stay hidden from each
other. Hermec is also meant to be a reusable base for communications apps, with
a role-gated Go core and a versioned, documented wire protocol. See
[PLAN.md](PLAN.md) for the roadmap and [docs/protocol.md](docs/protocol.md) for
the protocol.

## How people actually reach each other (plain-language version)

Think of a Hermec server as a small meeting room that one friend keeps open on
their own computer. Everyone else opens the Hermec app and connects to that
room. The app opens one long-lived connection to the server, called a
WebSocket, on port 7697. Before anything else happens, the server sends a
random number and the app signs it with your personal key file (like an SSH
key). That proves "this is really the same person as last time" without any
account or password. After that, the same connection carries everything that
isn't sound: chat messages, who is in which channel, who is muted, and so on.
The server simply passes each chat message on to everyone in that channel. It
does not save chat history, so if you were not there, you missed it.

Voice works a little differently. When you click a voice channel, the app and
the server use that same WebSocket to agree on how to send audio (this
"negotiation" is standard WebRTC). Your microphone audio then travels to the
server over a separate fast UDP link, and the server copies it out to everyone
else in the call. This is called an SFU, or "relay": nobody connects directly
to anybody else, so friends never see each other's IP addresses, only the
host's. The trade-off is that the host can technically see all the traffic, so
you pick a host the way you'd pick a group admin.

The hard part of all this is the internet itself: home routers normally block
anyone from the outside from reaching your computer. Hermec does not use any
outside helper servers (no STUN/TURN services) to sneak around that, because
the project owns no infrastructure. Instead we rely on three simple
workarounds. On the **same Wi-Fi/LAN**, it just works, and the app even finds
servers automatically by shouting a small "anyone there?" message on the
network. Across the internet, the easiest option is **Tailscale**: the host
shares their machine with friends, everyone gets a private address that
already reaches the host, and the app finds tailnet servers automatically too.
The last option is the **classic way**: the host forwards port 7697 (TCP and
UDP) on their router and sets `public_ip` in the server config. In
every case the app only needs to be able to reach the host; the host does the
rest.

## Status

Current release: v0.5.1. This repository ships a Go server with Ed25519
key-based authentication, roles, channels, presence and text chat, a headless
client library, a desktop GUI client, and integration tests. Voice works, with
audio relayed through the self-hosted server. Screen share works too (sharing
from Windows, watching on Windows, macOS or Linux). Camera video and direct
peer-to-peer media are not built yet. The wire protocol is specified in
[docs/protocol.md](docs/protocol.md).

## Install

Download the latest build from the
[Releases page](https://github.com/medeirosvictor/hermec/releases):

Pick the app that matches your computer:

| Download | For |
|---|---|
| `hermec-windows-amd64.zip` | Windows, most Intel/AMD PCs (x64) |
| `hermec-windows-arm64.zip` | Windows on ARM, such as Snapdragon laptops |
| `hermec-linux-amd64.tar.gz` | Linux, most Intel/AMD PCs (x64) |
| `hermec-linux-arm64.tar.gz` | Linux on ARM, such as Raspberry Pi and other ARM boards |

Needs a 64-bit OS. Linux builds need glibc 2.35 or newer (Ubuntu 22.04+, Debian 12+); 32-bit Raspberry Pi OS won't work.

Mac: there is no download. Build from source, see
[macOS: run from Terminal](#macos-run-from-terminal) below.

**Windows:**

1. Download the zip for your PC.
2. Unzip it anywhere.
3. Run `hermec.exe`.

It is one file. No toolchain and no extra DLLs are needed.
The exe is unsigned, so Windows SmartScreen may warn: click **More info**, then
**Run anyway** (see [the friends quickstart](docs/quickstart-friends.md#what-friends-receive)).

**Linux:**

1. Download the `.tar.gz` for your machine.
2. Unpack it: `tar xzf hermec-linux-amd64.tar.gz` (or `hermec-linux-arm64.tar.gz` on ARM)
3. Run `./hermec`.

You need a desktop with X11 or Wayland, and PulseAudio or PipeWire for sound.
Copy and paste need one of `xclip`, `xsel` or `wl-clipboard` (for example,
`sudo apt install xclip`).

**Servers and checks:** each release also ships servers (`hermec-server-linux-amd64`,
`hermec-server-linux-arm64`, `hermec-server-windows-amd64.exe`) and a
`SHA256SUMS.txt` to verify downloads. Release notes are generated from the
commits in each release.

**Update check:** on startup, release builds ask GitHub's public releases API
whether a newer version exists (development builds never make this request) and, if so, show a banner linking to the Releases page.
It sends no identifiers beyond a normal HTTPS request. The check always runs in
release builds; the "update" row in Settings (gear tile or F10) shows its status
and re-checks, or opens the Releases page when a newer version is available.
The old `update_check` key in `settings.toml` is ignored.

## Development

To run the GUI client in one command with an embedded server:

```sh
go run ./cmd/hermec -local
```

This launches the full Hermec experience locally: a server and GUI in one process,
with a default theme.

**Dev live reload.** Set `HERMEC_DEV=1` and run from the repo root to reload
`ui/crt.kage`, `ui/panel.kage`, the active `ui/theme/presets/*.toml` and any `-theme` file
within about a second of saving them. A file that fails to compile or parse
shows `dev reload <file>: <error>` in the status line and the previous version
stays active. Without the variable nothing is polled.

```sh
HERMEC_DEV=1 go run ./cmd/hermec -local
```

In a multi-machine setup, run the server on one host:

```sh
go run ./cmd/hermec-server
```

Then connect from other machines, specifying the server URL:

```sh
go run ./cmd/hermec -server ws://hostname:7697/
```

Customize the look with a theme file (see [example.theme.toml](example.theme.toml)):

```sh
go run ./cmd/hermec -theme ~/.config/hermec/myTheme.toml
```

### Keys

- **Enter**: Connect to server or send message
- **Ctrl+V** (Cmd+V on macOS): Paste into any text field. **Left**, **Right**, **Home**, **End** and **Delete** move and edit at the caret. On Linux, copy and paste need `xclip`, `xsel` or `wl-clipboard`.
- **Tab** or **Ctrl+Up/Down**: Switch channel
- **PgUp/PgDn** or **mouse wheel**: Scroll chat
- **F1**: Toggle CRT effect
- **F3**: Toggle CRT screen curvature (persisted; the rest of the CRT effect stays on)
- **F2**: Cycle color palettes (amber, green, blue, parchment light theme, slate modern dark)
- **Ctrl+M**: Mute or unmute voice
- **Ctrl+R**: Refresh discovered servers (connect screen)
- **Click rail tile**: Switch servers; every server you have opened stays connected, and switching keeps you in your voice call
- **Right-click a rail tile**: Rename (a nickname only you see; empty resets to the server's name), Disconnect (when you have a session to that server; ends your call too if it is the call's server), Leave Server (asks first; removes the tile and ends a call on it). Esc or a click elsewhere closes the menu. Hover a tile to see its full name.
- **Members button** (top right of the chat): Show or hide the member list of everyone on the server. The list hides itself in narrow windows; your choice is saved in `settings.toml`
- **Click "+" tile**: Add a server
- **Click the signal bars** (status line, or the call bar in a call): Show connection quality (ping, jitter, per-participant concealment); Esc or a second click closes it
- **Click a voice channel in the pane**: Join it (moves your call if you are in another one). Clicking the channel you are already in does nothing; leave with the call bar or the "voice connected" panel.
- **Click share** (call bar or "voice connected" panel): Share a screen, see [Screen share](#screen-share). **Click LIVE** next to a person in the voice list to watch them. **Esc** goes back to chat (out of fullscreen first); **F11** or **double-click** the video toggles fullscreen.
- **Title bar** (Windows): a Discord-style app bar replaces the default frame. Drag it to move the window (double-click to maximize or restore), use the buttons on the right to minimize, maximize and close, and drag the window edges and corners to resize.
- **Click gear tile** or **F10**: Settings page (name, palette, font, scanlines, curvature, input/output audio device, text size as a percentage, share quality (720p or 1080p), logs (open folder), update status, a help section; F10 works when the rail is hidden). Choices, including F1/F2/F3, persist to `<user config dir>/hermec/settings.toml`; `-name` and `-theme` flags override it. New installs start on the slate palette with JetBrains Mono; the font row cycles VT323, JetBrains Mono, IBM Plex Mono and Geist Mono, and existing settings files keep VT323.

Bundled fonts (all monospace, under the SIL Open Font License 1.1, licences in `ui/theme/`): VT323, JetBrains Mono, IBM Plex Mono and Geist Mono.

## Build

Most people should use the [prebuilt release](#install) instead.

**Prerequisites:** Go 1.27 or newer. The voice codec (Opus) is pure Go, so you
need no audio libraries and no pkg-config. The GUI client (`cmd/hermec`) still
needs a C compiler for the audio backend (miniaudio):

- **Windows:** MSYS2 with the UCRT64 gcc only.
- **Linux:** `gcc`, plus the development headers for the window and sound
  libraries (on Debian or Ubuntu: `sudo apt install gcc libgl1-mesa-dev xorg-dev libasound2-dev`).
- **macOS:** Xcode command line tools (`xcode-select --install`).

**Windows (MSYS2):**

```sh
winget install MSYS2.MSYS2
pacman -S mingw-w64-ucrt-x86_64-gcc
export PATH="/c/msys64/ucrt64/bin:$PATH"
CGO_ENABLED=1 go build ./cmd/hermec
```

The server (`cmd/hermec-server`) is pure Go and does not require cgo on any
platform.

**Linux/macOS:**

```sh
CGO_ENABLED=1 go build ./cmd/hermec    # GUI client (voice enabled)
go build ./cmd/hermec-server           # or ./... for everything
```

Check which version you built with `hermec -version`.

## macOS: run from Terminal

There is no Mac download. Apple signing costs money, and HERMEC stays free.
So you build it yourself. It takes a few minutes.

1. Install the command line tools: `xcode-select --install`
2. Install Homebrew from <https://brew.sh>.
3. Install Go and git: `brew install go git`
4. Get the code:
   `git clone https://github.com/medeirosvictor/hermec.git && cd hermec`
5. Build the latest release: `scripts/build-mac.sh`
   The script fetches the release tags, checks out the newest one, and builds
   `./hermec` with its version number set, so the update banner works.
   The first build downloads Go 1.27 automatically if Homebrew's Go is older.
6. Run it: `./hermec`
7. The first time you use voice, macOS asks to let Terminal use the microphone.
   Allow it. You can change it later in System Settings → Privacy & Security →
   Microphone.

To update: run `scripts/build-mac.sh` again.

The script checks out a release tag, so the repo is on a detached HEAD. To go
back to the development branch, run `git checkout main`.

## Join a friend's server

Any OS, once HERMEC is running:

1. Get on the same network as your friend. Usually that means Tailscale:
   install it from <https://tailscale.com/download>, sign in, and accept the
   share invite your friend sent. Their PC shows up in your Tailscale menu.
2. Ask your friend for their **invite address** (they copy it from their
   server tile's right-click menu). It looks like `ws://100.x.x.x:7697/`.
3. Optional check (macOS/Linux Terminal): `nc -vz 100.x.x.x 7697` should say
   "succeeded".
4. In HERMEC click **+**, then **Join**. The address field already starts with
   `ws://`. Paste the full address (Ctrl+V, or Cmd+V on a Mac), or type the
   rest after the `ws://`, then press Enter. Pasting a full `ws://` address
   replaces the prefilled text, so you never get `ws://ws://`.
5. Put on headphones (there is no echo cancellation yet) and click a voice channel.

## How safe is this? (Tailscale)

**What is protected today:**

- **Encrypted on the wire.** Tailscale wraps all traffic between devices in
  WireGuard encryption, even when it goes through Tailscale's relay servers
  (the relays cannot read it). Voice is also encrypted by WebRTC on top.
- **Friends reach one machine only.** A friend you share your PC with sees only
  that PC, not your other devices. Your PC cannot open connections into theirs.
- **You can take access back.** Tailscale admin → Machines → your PC → Share →
  Revoke invite.
- **Keys, not passwords.** Every person has a key file. The server checks a
  signature from that key when they connect.
- **The host proves itself first.** Before your app sends anything secret
  (including the server password), the host sends the server's signed chain and
  proves it holds a key that the chain approves as a host. A machine that
  merely copied the address cannot pass.
- **Your app remembers the server.** Each server has an ID (the hash of the
  first entry of its chain). The app pins it the first time you connect and, if
  the ID ever changes, refuses to connect, tells you so and does not retry. Your
  login signature also covers the server ID, so once the real server's ID is
  pinned, one host cannot replay your login to log in as you on another server.
  On first use of a server you add, a malicious server could still relay another
  server's handshake (see known gap 6).
- **Bans are enforced by the server.** The server and protocol support banning
  a key: a banned key is disconnected at once and refused when it comes back.
  There is no app or command-line control for bans yet.

**Known gaps (we are working on these):**

1. **A shared friend can reach every port on your PC, not just HERMEC.**
   Tailscale's default policy allows all ports. You can narrow it to HERMEC,
   because all voice and video media now uses one UDP port, the same number
   as the server's port (UDP 7697 next to TCP 7697). In the Tailscale admin
   console, open **Access controls**. The default grant lets everyone reach
   everything. Do not delete it (that would lock out your own devices).
   Instead, change its `src` from `"*"` to `"autogroup:member"` (people in your
   own tailnet), and add a second grant for shared users. The `grants` list
   then looks like this:

   ```json
   "grants": [
     {"src": ["autogroup:member"], "dst": ["*"], "ip": ["*"]},
     {"src": ["autogroup:shared"], "dst": ["*"], "ip": ["tcp:7697", "udp:7697"]}
   ]
   ```

   Shared friends can then reach only TCP and UDP 7697 on your devices. This
   needs HERMEC v0.5.0 or newer on the host. If you run on another port, use
   that port number instead of 7697; `media_port` in the server config picks a
   different media port if you need one.
2. **No password or invite list in the app.** Anyone who can reach your
   server's port can join, including other devices on your home Wi-Fi. The
   server config already has `password` and `allowed_keys` (see
   [example.server.toml](example.server.toml)); the app cannot set them yet.
3. **Windows Firewall on public Wi-Fi.** If you allowed HERMEC on "Public"
   networks, your server is reachable on any café Wi-Fi your PC joins. To allow
   only Tailscale and your home network, run PowerShell as admin (use your own
   path to `hermec.exe`):

   ```powershell
   Get-NetFirewallApplicationFilter -Program "C:\Apps\hermec\hermec.exe" |
     Get-NetFirewallRule |
     Set-NetFirewallRule -RemoteAddress 100.64.0.0/10,LocalSubnet
   ```

   Drop `,LocalSubnet` to allow Tailscale only.
4. **The host can read everything.** Chat and voice pass through the host's
   server. Only join servers run by people you trust.
5. **Names are not verified.** Anyone can pick any name. The fingerprint (hover
   a server or person) is the real identity.
6. **The handshake does not bind the connection.** On plain `ws://`, someone
   on the network path between you and the host could relay a real host's
   proof and so read the password you send. Use TLS (set `tls_cert` and
   `tls_key` in the server config; both are in
   [example.server.toml](example.server.toml); the server then serves `wss://`)
   or a network that is already encrypted. Tailscale is, and it is what this
   friend group uses. Without one of them, treat the password as visible to
   anyone on the path.
7. **Not ready for strangers on the open internet.** There are no invite links
   yet, so a public server cannot tell welcome visitors from unwelcome ones
   (`allowed_keys` helps, and bans work after the fact, though the app has no ban control yet). Reach servers
   through Tailscale or your LAN.

## Run

```sh
./hermec-server                                # defaults: ws on :7697, channel "general"
./hermec-server -config example.server.toml    # with a config file
./hermec-server -config server.toml -print-config   # show effective config and exit
./hermec-server -config server.toml -apply-config   # append the TOML's group settings to the chain and exit (server stopped)
```

The server's identity lives in a signed chain. On first start the server
creates the chain from its config, with its own key as founder. `-key` is the
host key file and `-chain` is the chain file. Both default to
`hermec-server.key` and `chain.jsonl` next to the config file, or in the current
directory without `-config`; the key is generated when missing. Back both up;
the key is what lets you change the group later. The server refuses to start
when its key is not an approved host of the chain it loads. If a crash left a
half-written last entry in the chain file, the server drops it, logs a
warning and keeps the entries before it.

Once a chain exists, the group settings in the TOML (`server_name`, `channels`,
`voice_channels`, `max_voice`, `max_clients`, `max_per_ip`, `allowed_keys`,
`roles`, `default_roles` and `[grants]`) are ignored, and the server logs a
warning when it sees them. To change them from the file, edit the TOML and run
`hermec-server -config server.toml -apply-config`. Stop the server first: the
command refuses to run while something listens on the config's port, and the
changes apply when the server starts. It needs `-config`, appends only the
group keys the TOML actually sets (grants are compared only when `[grants]` is
set), signed by the server's key, then prints what it appended and exits.
Entries that an admin's app sends to a running server with `chain_append`
(grants, bans, a new config) are different: `allowed_keys`, roles and bans
apply at once, and the name, channels and limits from a later config apply at
the next start. The founder always keeps every permission and always passes
`allowed_keys`, as does every approved host. The password, port, TLS and the
other machine settings stay in the TOML and never go into the chain.

Everyone in a group needs HERMEC v0.6 or newer: an older app is told to update
HERMEC, and a v0.6 app that meets an older server reports that the host runs an
older version.

Servers announce themselves on the LAN and on Tailscale (if the `tailscale` CLI
is on PATH), so friends see them in the app's DISCOVERED list. To label your
server or hide it from discovery, see the `server_name` and `discoverable` keys
below.

See [example.server.toml](example.server.toml) for every option. The server logs
its listen address on start and shuts down cleanly on Ctrl-C. Note: the desktop
GUI does not yet have a password field; password-protected servers are
currently reachable only from headless clients. A password field is planned.

### Voice

To use voice channels, configure them in your server config:

```toml
voice_channels = ["voice", "private-call"]
```

Click a voice channel in the pane to join. The call bar appears at the bottom
showing the channel name, elapsed time, mute button (Ctrl+M), share button,
leave button, and your mic level. The "voice connected" panel at the bottom of
the channel column shows the channel and server, your ping, and buttons to
disconnect and share; its signal bars open the connection quality panel.
Participants, occupancy, mute icons, and speaking indicators live in the channel
pane under each voice channel; empty voice channels show only in the list.

**Important: Hermec does not perform acoustic echo cancellation. Using speakers
will feed other participants' audio back into your microphone; headsets are
strongly recommended.**

For internet-facing servers, configure `public_ip` (your server's public IPv4)
and, for firewall policy, note that all media uses one UDP port (the server's
port number by default; `media_port` overrides it, and `udp_port_min`/
`udp_port_max` select the older random-port range instead). See
[example.server.toml](example.server.toml) for details.

### Screen share

Show your screen to the people in your voice call.

**Share your screen** (Windows only for now):

1. Join a voice channel.
2. Click **share** on the call bar.
3. Pick a monitor from the list, like `screen 1 (2560×1440)`.
4. The button now says **stop share**. Click it to stop.

Leaving the voice call stops your share too.

To see what you are sharing, click your own **LIVE** tag. The preview is drawn
on your computer, so it costs no bandwidth. While you share, the strip on the
video has **stop sharing** and **change screen**. Change screen swaps the
monitor without ending the share; people watching keep watching (the picture is
letterboxed if the new screen has a different shape).

**Watch someone's screen** (Windows, macOS and Linux):

1. Look at the voice channel list. People who share have a red **LIVE** tag.
2. Click **LIVE**.
3. The video replaces the chat.
4. Move the mouse over the video to show a strip with **fullscreen** and
   **close**.
5. Press **Esc** to go back to chat. In fullscreen, the first **Esc** leaves
   fullscreen and the second goes back to chat.
6. **F11** or a **double-click** on the video toggles fullscreen.

You can watch one screen at a time. The video is drawn clean, without the CRT
effects, so text stays readable.

**Quality.** In Settings, the row `share quality` switches between `720p` and
`1080p`. 720p uses about 1.5 Mbps (15 frames a second). 1080p uses about 3 Mbps.

**Bandwidth.** The server relays the video:

- The sharer uploads one stream.
- The host uploads one copy for each person watching, about 1.5 Mbps each at 720p.
- The server only sends a stream to people who are watching it.

So a host with a slow upload should keep the number of watchers small.

**Not yet:** sound from the shared screen, sharing a single window, and sharing
from macOS or Linux.

**Ports.** Voice and video use one UDP port, the same number as the server
port (7697). To let shared friends reach only that port, see the Tailscale
grant in [How safe is this?](#how-safe-is-this-tailscale).

### Logs

Each time you start HERMEC it writes one log file. It helps when something goes
wrong. The log never contains chat text or keys.

Where the files are:

| System | Folder |
|---|---|
| Windows | `%APPDATA%\hermec\logs` |
| macOS | `~/Library/Application Support/hermec/logs` |
| Linux | `~/.config/hermec/logs` |

Files are named like `hermec-2026-10-19_21-30-05.log`. HERMEC keeps the newest
10 and deletes older ones.

To find the folder fast, open Settings and click the row `logs: open folder`.
It opens the folder and shows the path.

**To send a log to a friend:** open the folder, then attach the newest file
to a message.

## Chat with friends

Want to try Hermec with a few friends? One person runs the server, everyone
else just runs the app. The easiest private setup is Tailscale, with no router
changes and no public IP exposed. See the step-by-step
[friends quickstart](docs/quickstart-friends.md).

You can also host from the app itself: click the **+** tile, choose
**Create my own**, and give the server a name and a port (7697 by default).
Servers made this way have no password, because the app has no password field
for joining. The app starts the server on your computer, opens it as a
tile, and shows an invite address (your Tailscale address if you have one,
otherwise your LAN address) that friends paste into **Join a server**. Windows
asks once to allow HERMEC on your network; choose Allow so friends can connect.
The server runs while the app is open. Its signed chain (the server's identity
and group settings, with you as founder) is kept in a `chains` folder next to
`hosted.toml`; a server created before v0.6 gets its chain the first time v0.6
starts it.

## Use as a library

```go
id, _ := identity.Generate()
ctx := context.Background()
c, err := client.Dial(ctx, "ws://localhost:7697/", id, "alice", "")
if err != nil {
	log.Fatal(err)
}
defer c.Close()
if err := c.Join(ctx, "general"); err != nil {
	log.Fatal(err)
}
_ = c.SendChat(ctx, "general", "hello")
for ev := range c.Events() {
	if ev.Chat != nil {
		fmt.Printf("%s: %s\n", ev.Chat.From.Name, ev.Chat.Text)
	}
}
```

## Security

- **Use TLS.** Transport encryption is required for Hermec's threat model.
  Configure `tls_cert` and `tls_key` for any non-loopback deployment, unless a
  network layer already encrypts and restricts access (a Tailscale/WireGuard
  network or a trusted LAN, as in the
  [friends quickstart](docs/quickstart-friends.md)). Plain `ws://` over the
  public internet means an on-path observer can read everything, including chat.
- **The handshake does not bind the connection.** On plain `ws://`, someone on
  the network path could relay a real host's proof and read your password. Use
  TLS (`wss://`) or an encrypted network such as Tailscale.
- **The host is trusted by design.** The room host can read all chat and
  relayed traffic, the same way a TeamSpeak host can. Choose hosts like you
  choose group admins. The host also sees who connects and when.
- **End-to-end encryption is not built.** Voice is encrypted between you and the
  host (WebRTC), and chat is encrypted only if the server uses TLS or the
  network layer does. The protocol reserves message types for end-to-end
  encryption later.

## Troubleshooting

### Windows Firewall prompts

**Expected:** The real app prompts once for firewall access (UDP socket for LAN
discovery and voice media relay).

**During development:** Running with `go run` or `go test` creates temporary
executables, each triggering a new prompt. To avoid repeated prompts, build a
stable executable once and run it instead:

```sh
go build -o bin/hermec.exe ./cmd/hermec
./bin/hermec.exe -local
```

**Denying the prompt:** If you deny firewall access, loopback (localhost,
127.0.0.1) and Tailscale still work. LAN discovery, and anyone else on your LAN
reaching a server you host, are the only things that
require the firewall exception. The machine hosting the server must have its
server executable (`hermec-server.exe`, or `hermec.exe` when using `-local`)
**Allowed**, or LAN peers cannot connect or hear voice; clients that only join
someone else's server need no inbound exception.

## Test

CI (`.github/workflows/ci.yml`) runs gofmt, vet and `-race` tests on Linux, tests on macOS and Linux ARM, a fuzz smoke run of the protocol decoder, and cross-builds of the pure-Go packages. The release workflow (`release.yml`) also builds all four client assets on every pull request as a dry run.

```sh
go vet ./...
go test ./...
go test -race ./...    # needs cgo (a C compiler)
```
