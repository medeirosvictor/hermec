# HERMEC: Portable builds — design

Date: 2026-10-19. Status: design approved in chat by Victor ("your ideas sound great, proceed").
Goal: aim for 100% platform-agnostic. Every library ships inside the app, and the release has app builds for the major OS/arch pairs. No paid Apple developer account: macOS users build from source in Terminal.

## 1. Problem

- The client needs libopus (C) through `github.com/hraban/opus`. On Windows the release zip carries loose MSYS2 DLLs found by a DLL walk (`scripts/build-release.ps1`); on macOS and Linux users must install opus + opusfile + pkg-config.
- The release ships the app for Windows amd64 only. Linux and arm64 users, and Mac users, get nothing.

## 2. Research result (2026-10-19)

- Ebiten v2.10 needs no Cgo on Windows, macOS, Linux or BSD (runtime window/GL libraries are loaded from the system).
- Opus has mature pure-Go ports. Chosen: `github.com/thesyncim/gopus` v0.2.2 (BSD-3, encoder + decoder with SILK/CELT/hybrid, VoIP application, FEC, PLC via an empty packet; checked against libopus 1.6.1; needs Go 1.27). Fallback if it fails the gate: a WebAssembly build of libopus run by wazero (e.g. `jj11hh/opus`, a hraban fork).
- Mic/speaker I/O has no trustworthy pure-Go library yet (audio-go: 6 commits, ALSA-only Linux that fails when PulseAudio/PipeWire holds the device). `malgo` (miniaudio) stays. Its C source compiles into the binary and loads OS audio libraries at runtime, so it adds no install-time dependency; it needs a C compiler at build time only.
- Screen share (next round) will use the pure-Go VP8 codec `govpx` instead of libvpx, keeping zero external libraries.

## 3. Design

1. **Opus swap.** Replace `hraban/opus` with `gopus` in `audio/` only. Same settings: 48 kHz, mono, 20 ms frames (960 samples), VoIP application, 32 kbps, in-band FEC on, expected loss 10%. Decoder PLC: an empty packet into a 960-sample buffer. Wire format is unchanged (Opus packets), so old and new clients interoperate.
2. **Self-contained binaries.** Windows client links statically (`-extldflags=-static`): one `hermec.exe` with only Windows system DLL imports. The DLL walk and `scripts/build-release.ps1` go away.
3. **`-version` flag.** `hermec -version` prints `hermec <version>` and exits 0 without opening a window. Release smoke tests use it.
4. **Release matrix.** App builds on native GitHub runners (cgo for malgo):

   | Asset | Runner | C toolchain |
   |---|---|---|
   | `hermec-windows-amd64.zip` | `windows-latest` | MSYS2 UCRT64 gcc |
   | `hermec-windows-arm64.zip` | `windows-11-arm` | MSYS2 CLANGARM64 clang |
   | `hermec-linux-amd64.tar.gz` | `ubuntu-22.04` | system gcc |
   | `hermec-linux-arm64.tar.gz` | `ubuntu-22.04-arm` | system gcc |

   Ubuntu 22.04 keeps the glibc floor at 2.35 so the Linux build runs on older distros. Each archive holds the binary plus `README.txt`. The three server builds stay as they are. Each build job checks its binary: `-version` runs, and its dynamic imports are system-only (Windows: no DLL from the MSYS2 tree; Linux: only glibc libraries).
5. **Dry runs.** The release workflow also runs its build jobs on pull requests; only a `v*` tag publishes. Every PR proves all builds still work.
6. **README.** Releases section lists the new assets; build section drops opus/opusfile/pkg-config (a C compiler stays, for malgo); a **macOS from Terminal** section: `xcode-select --install`, `brew install go git`, clone, `go build -o hermec ./cmd/hermec`, `./hermec`, plus the Terminal microphone-permission note and `git pull` + rebuild for updates.
7. **CI.** `ci.yml` stops installing libopus packages.

## 4. Out of scope

A macOS release asset (needs Apple signing to avoid the unidentified-developer block); a pure-Go audio I/O backend; `-H windowsgui` and file logging; mobile.

## 5. Acceptance

- `go vet ./...` and `go test -race ./...` green; `go list -deps ./...` contains no `hraban/opus`.
- A PR run of the release workflow builds all four app assets and passes their checks.
- Victor's ear test: a call between the new build and an old v0.3.0 client sounds clean in both directions (proves wire compatibility and codec quality).
