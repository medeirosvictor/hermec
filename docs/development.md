# Development

Most people should use the [prebuilt release](getting-started.md#install)
instead of building. The wire protocol is specified in [protocol.md](protocol.md)
and the roadmap is [PLAN.md](../PLAN.md).

## Build from source

**Prerequisites:** Go 1.27 or newer. The voice codec (Opus) is pure Go, so you
need no audio libraries and no pkg-config. The GUI client (`cmd/hermec`) still
needs a C compiler for the audio backend (miniaudio):

- **Windows:** MSYS2 with the UCRT64 gcc only.
- **Linux:** `gcc`, plus the development headers for the window and sound
  libraries (on Debian or Ubuntu:
  `sudo apt install gcc libgl1-mesa-dev xorg-dev libasound2-dev`).
- **macOS:** Xcode command line tools (`xcode-select --install`); or use
  `scripts/build-mac.sh`, see
  [Getting started](getting-started.md#macos-build-and-run-from-terminal).

**Windows (MSYS2):**

```sh
winget install MSYS2.MSYS2
pacman -S mingw-w64-ucrt-x86_64-gcc
export PATH="/c/msys64/ucrt64/bin:$PATH"
CGO_ENABLED=1 go build ./cmd/hermec
```

**Linux/macOS:**

```sh
CGO_ENABLED=1 go build ./cmd/hermec    # GUI client (voice enabled)
go build ./cmd/hermec-server           # or ./... for everything
```

The server (`cmd/hermec-server`) is pure Go and does not require cgo on any
platform. Check which version you built with `hermec -version`.

## Run locally

Run the GUI client with an embedded server in one process:

```sh
go run ./cmd/hermec -local
```

For a multi-machine setup, run the server on one host and connect from the
others:

```sh
go run ./cmd/hermec-server
go run ./cmd/hermec -server ws://hostname:7697/
```

Client flags: `-server`, `-name`, `-key` (identity key path, default
`<user config dir>/hermec/identity.key`), `-theme`, `-local`, `-v` (log
breadcrumbs) and `-version`.

**Second client on one machine.** Two clients using one key silence each other.
Start the second with `-key` pointing at another file:

```sh
go run ./cmd/hermec -local -key hermec2.key
```

Do not run GUI automation against your real user config folder; there is no
`-config-dir`, so use isolated configuration.

**Dev live reload.** Set `HERMEC_DEV=1` and run from the repo root to reload
`ui/crt/crt.kage`, `ui/panel.kage`, the active `ui/theme/presets/*.toml` and any
`-theme` file within about a second of saving them. A file that fails to compile
or parse shows `dev reload <file>: <error>` in the status line and the previous
version stays active. Without the variable nothing is polled.

```sh
HERMEC_DEV=1 go run ./cmd/hermec -local
```

**Stable executable.** `go run` and `go test` create temporary executables, each
triggering a new Windows Firewall prompt. Build one once and run that:

```sh
go build -o bin/hermec.exe ./cmd/hermec
./bin/hermec.exe -local
```

## Using Hermec as a library

The headless client library (`client`) dials a server and exposes events:

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

## Tests

```sh
go vet ./...
go test ./...
go test -race ./...    # needs cgo (a C compiler)
```

Run only the tests for the packages you changed (for example
`go test ./server/...`). CI (`.github/workflows/ci.yml`) runs gofmt, vet and
`-race` tests on Linux, tests on macOS and Linux ARM, a fuzz smoke run of the
protocol decoder, and cross-builds of the pure-Go packages. The release workflow
(`release.yml`) also builds all four client assets on every pull request as a
dry run.

## Releasing

Releases are tagged from `main`. CI builds the four client assets and the
dedicated server bundles, signs the combined `SHA256SUMS.txt` with the
`HERMEC_RELEASE_KEY` secret, and refuses to publish a release no installed app
could verify. Key setup, hand verification and rotation are in
[Release signing](release-signing.md). The client archives carry a short
`README.txt`; the server bundle carries
[dedicated-hosting.md](dedicated-hosting.md) as its `README.md`.

## Package layout

| Path | What it holds |
|---|---|
| `cmd/hermec` | Desktop GUI client |
| `cmd/hermec-server` | Server binary (pure Go) |
| `cmd/hermec-verify` | Remote reachability verifier for dedicated hosting |
| `cmd/hermec-release` | Release key generation, signing and verification |
| `cmd/hermec-icon` | Icon generator |
| `core/` | `chain`, `hshake`, `identity`, `names`, `proto`, `roles`: the role-gated core and the wire protocol |
| `server/` | Server: auth, channels, SFU, history, host failover; subpackages `history`, `host`, `serverctl`, `dedicated` |
| `client/` | Headless client library: handshake, voice, video, screen share, failover |
| `ui/` | Ebiten GUI; `ui/crt` is the CRT shader, `ui/theme` themes and fonts |
| `audio/`, `cameracap/`, `screencap/`, `video/` | Capture, playback, codecs |
| `discover/`, `netpath/` | LAN/Tailscale discovery, per-instance network |
| `update/`, `version/`, `applog/` | Updater, build version, per-run log |
| `internal/` | `fsutil`, `knownhosts`, `ratelimit` |
| `packaging/dedicated/` | Install, update and backup scripts of the VPS bundle |
| `integration/` | Integration tests |
| `tools/embedded-probe/` | Private-networking probe |
| `scripts/build-mac.sh` | macOS build from the newest release tag |

Design notes and plans are in [research/](research/) and
[superpowers/](superpowers/).
