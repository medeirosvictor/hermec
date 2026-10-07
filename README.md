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

This repository currently ships the **foundation only**: a Go server with
Ed25519 key-based authentication, roles, channels, presence and text chat, plus
a headless client library, a desktop GUI chat client, and integration tests.
Voice, screenshare, relay, and peer-to-peer media remain unimplemented. The
wire protocol for what exists is specified in [docs/protocol.md](docs/protocol.md).

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
- **Click rail tile**: Switch servers
- **Click "+" tile**: Add a server
- **Click voice channel in the pane**: Join or leave voice call
- **Click gear tile** or **F10**: Settings page (name, palette, scanlines; F10 works when the rail is hidden). Choices, including F1/F2, persist to `<user config dir>/hermec/settings.toml`; `-name` and `-theme` flags override it.

## Build

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

See [example.server.toml](example.server.toml) for every option (password, key
allow list, channels, TLS, roles). The server logs its listen address on start
and shuts down cleanly on Ctrl-C. Note: the desktop GUI does not yet have a
password field; password-protected servers are currently reachable only from
headless clients. A password field is planned.

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
  Configure `tls_cert` and `tls_key` for any non-loopback deployment; plain
  `ws://` means an on-path observer can read everything, including chat.
- **The host is trusted by design.** The room host can read all chat and
  relayed traffic, the same way a TeamSpeak host can. Choose hosts like you
  choose group admins. See section 8 of
  [the design spec](docs/specs/2026-10-06-hermec-design.md).

## Test

CI (`.github/workflows/ci.yml`) runs vet, `-race` tests, a fuzz smoke run and Windows/arm64 cross-builds.

```sh
go vet ./...
go test ./...
go test -race ./...    # needs cgo
```
