# Hermec

Hermec is a small, open-source, censorship-resilient communications app,
"simple like TeamSpeak": voice channels with screensharing and group text chat,
served by lightweight servers that anyone can host. The project owns no
infrastructure; resilience comes from many small, independently hosted servers
rather than one central service that can be blocked.

The design is relay-by-default, so participants' IPs stay hidden from each
other, with direct peer-to-peer media as a per-participant opt-in. Hermec is
also meant to be a reusable base for communications apps, with a role-gated Go
core and a versioned, documented wire protocol. See
[the design spec](docs/specs/2026-10-06-hermec-design.md) and [PLAN.md](PLAN.md).

## Status

This repository currently ships a Go server with Ed25519 key-based
authentication, roles, channels, presence and text chat, plus a headless client
library, a desktop GUI client, and integration tests. Voice chat now works in
the GUI, with audio relayed through the self-hosted server. Screenshare, camera
video, and the direct peer-to-peer opt-in remain unimplemented. The
wire protocol for what exists is specified in [docs/protocol.md](docs/protocol.md).

## Install

Download the latest build from the
[Releases page](https://github.com/medeirosvictor/hermec/releases):

1. Download `hermec-windows-amd64.zip`.
2. Unzip it anywhere.
3. Run `hermec.exe`.

No toolchain is needed; the zip includes the runtime DLLs voice requires.
The exe is unsigned, so Windows SmartScreen may warn: click **More info**, then
**Run anyway** (see [the friends quickstart](docs/quickstart-friends.md#what-friends-receive)).
Each release also ships Linux and Windows servers (`hermec-server-*`) and a
`SHA256SUMS.txt` to verify downloads. Release notes are generated from the
commits in each release.

**Update check:** on startup the app asks GitHub's public releases API whether a
newer version exists and, if so, shows a banner linking to the Releases page.
It sends no identifiers beyond a normal HTTPS request. To turn it off, toggle
"update check" in Settings (gear tile or F10), or set `update_check = false` in
`<user config dir>/hermec/settings.toml`.

## Development

To run the GUI client in one command with an embedded server:

```sh
go run ./cmd/hermec -local
```

This launches the full Hermec experience locally: a server and GUI in one process,
with a default theme. In a multi-machine setup, run the server on one host:

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
- **Tab** or **Ctrl+Up/Down**: Switch channel
- **PgUp/PgDn** or **mouse wheel**: Scroll chat
- **F1**: Toggle CRT effect
- **F2**: Cycle color palettes
- **Ctrl+M**: Mute or unmute voice
- **Ctrl+R**: Refresh discovered servers (connect screen)
- **Click rail tile**: Switch servers
- **Click "+" tile**: Add a server
- **Click voice channel in the pane**: Join or leave voice call
- **Click gear tile** or **F10**: Settings page (name, palette, scanlines; F10 works when the rail is hidden). Choices, including F1/F2, persist to `<user config dir>/hermec/settings.toml`; `-name` and `-theme` flags override it.

## Build

Most people should use the [prebuilt release](#install) instead.

**Prerequisites:** Go 1.26 or newer. If building `cmd/hermec` (GUI client) with
voice support, also install a C compiler and Opus audio libraries:

**Windows (MSYS2):**

```sh
winget install MSYS2.MSYS2
pacman -S mingw-w64-ucrt-x86_64-{gcc,opus,opusfile,pkgconf}
export PATH="/c/msys64/ucrt64/bin:$PATH"
CGO_ENABLED=1 go build ./cmd/hermec
```

**Windows runtime note:** Running a voice-enabled `hermec.exe` requires the MSYS2
runtime DLLs on PATH. Either add `C:\msys64\ucrt64\bin` to your system PATH, or
copy the needed DLLs (`libopus-0.dll`, `libopusfile-0.dll`, and their dependencies)
next to the executable.

The server (`cmd/hermec-server`) is pure Go and does not require cgo on any
platform.

**Linux/macOS:**

```sh
# Install development libraries (e.g., libopus-dev on Debian/Ubuntu, opus via Homebrew)
CGO_ENABLED=1 go build ./cmd/hermec    # GUI client (voice enabled)
go build ./cmd/hermec-server           # or ./... for everything
```

## Run

```sh
./hermec-server                                # defaults: ws on :7697, channel "general"
./hermec-server -config example.server.toml    # with a config file
./hermec-server -config server.toml -print-config   # show effective config and exit
```

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

Click a voice channel in the pane to join; the call bar appears at the bottom
showing the channel name, elapsed time, mute button (Ctrl+M), leave button, and
your mic level. Participants, occupancy, mute icons, and speaking indicators live
in the channel pane under each voice channel; empty voice channels show only in
the list.

**Important: Hermec does not perform acoustic echo cancellation. Using speakers
will feed other participants' audio back into your microphone; headsets are
strongly recommended.**

For internet-facing servers, configure `public_ip` (your server's public IPv4)
and optionally `udp_port_min`/`udp_port_max` for firewall policy. See
[example.server.toml](example.server.toml) for details.

## Chat with friends

Want to try Hermec with a few friends? One person runs the server, everyone
else just runs the app. The easiest private setup is Tailscale, with no router
changes and no public IP exposed. See the step-by-step
[friends quickstart](docs/quickstart-friends.md).

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
- **The host is trusted by design.** The room host can read all chat and
  relayed traffic, the same way a TeamSpeak host can. Choose hosts like you
  choose group admins. See section 8 of
  [the design spec](docs/specs/2026-10-06-hermec-design.md).

## Troubleshooting

### Build error: `undefined: Stream` in hraban/opus

If building `cmd/hermec` fails with an undefined reference in the opus package,
your C compiler is not on PATH, and Go silently set `CGO_ENABLED=0`. This breaks
the voice-enabled build.

**Fix:** ensure your C compiler is on PATH. On Windows with MSYS2:

```sh
export PATH="/c/msys64/ucrt64/bin:$PATH"
CGO_ENABLED=1 go build ./cmd/hermec
```

Add `C:\msys64\ucrt64\bin` to your system PATH (via Settings > Environment
Variables) to make it permanent.

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
127.0.0.1) and Tailscale still work. LAN discovery is the only feature that
requires the firewall exception.

## Test

CI (`.github/workflows/ci.yml`) runs vet, `-race` tests, a fuzz smoke run and Windows/arm64 cross-builds.

```sh
go vet ./...
go test ./...
go test -race ./...    # needs cgo
```
