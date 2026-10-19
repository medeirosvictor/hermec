# Portable Builds Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The app needs no external libraries on any OS, and every release ships self-contained app builds for Windows and Linux on amd64 and arm64.

**Architecture:** Swap the cgo libopus binding for the pure-Go `gopus` codec; keep malgo (miniaudio compiled in). Build the client on four native GitHub runners, statically linked on Windows, with per-build smoke and dependency checks; the same jobs run on pull requests as a dry run.

**Tech Stack:** Go 1.27, `github.com/thesyncim/gopus` v0.2.2, malgo v0.11.26, Ebiten v2.10.4, GitHub Actions (msys2/setup-msys2@v2, actions/setup-go@v5).

**Spec:** `docs/superpowers/specs/2026-10-19-hermec-portable-builds-design.md`

## Global Constraints

- Opus settings unchanged: 48000 Hz, 1 channel, 960-sample (20 ms) frames, `ApplicationVoIP`, bitrate 32000, FEC on, packet loss 10.
- `go.mod` go directive becomes `1.27.0` (gopus requires it). README prerequisite becomes Go 1.27+.
- Asset names exactly: `hermec-windows-amd64.zip`, `hermec-windows-arm64.zip`, `hermec-linux-amd64.tar.gz`, `hermec-linux-arm64.tar.gz`, plus the unchanged `hermec-server-linux-amd64`, `hermec-server-linux-arm64`, `hermec-server-windows-amd64.exe`, `SHA256SUMS.txt`.
- Version stamping unchanged: `-ldflags "-s -w -X github.com/medeirosvictor/hermec/version.Version=$TAG"`.
- Local env (PowerShell): prepend `C:\msys64\ucrt64\bin` to PATH, `CGO_ENABLED=1`. Never launch the GUI, never touch `%APPDATA%\hermec`, never kill hermec.exe, never bind port 7697.
- The checkout uses CRLF (autocrlf); judge gofmt on committed content, not `gofmt -l .` on the working tree.
- Commit trailer: a blank line then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. Interop with old v0.3.0 (libopus) clients in the same call: no automated test can hold both codecs once hraban leaves go.mod, so this is Victor's ear test (spec section 5); reviewers confirm the encoder settings match the Global Constraints exactly (Task 1's `TestEncoderSettings`).
2. PLC buffer sizes: gopus derives PLC duration from the buffer length, which must be a positive multiple of 2.5 ms; `s.cur` and `s.pcm[:frameSize]` must be exactly 960 — Task 1 tests a PLC call returns 960 samples.
3. Decode into the 5760-sample `decodeMax` buffer must still return 960 for a 20 ms packet — Task 1 test.
4. `-version` must exit before any window, audio or network init, or headless CI runners fail — Task 2.
5. Windows binaries accidentally importing `libwinpthread-1.dll`/`libgcc_s` (static flag missing on one matrix leg) — Task 3's import check fails the job.

---

### Task 1: Swap libopus for gopus

**Files:**
- Modify: `audio/capture.go:11, 81-89, 236`, `audio/playback.go:11, 127, 166, 171, 218`, `audio/opus_test.go`, `go.mod`, `go.sum`

**Interfaces:**
- Produces: no API change outside `audio/`. Encoder field type becomes `*gopus.Encoder`, decoder `*gopus.Decoder`.

- [ ] **Step 1: Write the failing tests** in `audio/opus_test.go` (switch its import to gopus; keep every existing test):
  - `TestEncoderSettings`: the encoder built by the same constructor capture uses (extract `newVoiceEncoder() (*gopus.Encoder, error)` in capture.go) has `Bitrate() == 32000`, `FECEnabled() == true`, `PacketLoss() == 10`, `Application() == gopus.ApplicationVoIP`.
  - `TestDecodePLCLength`: after decoding one real packet into a `[decodeMax]int16` buffer (expect n == 960), `DecodeInt16(nil, buf[:frameSize])` returns 960 and no error.
- [ ] **Step 2: Run** `go test ./audio/ -run 'EncoderSettings|DecodePLCLength'`. Expected: compile FAIL (no `newVoiceEncoder`, gopus not in go.mod).
- [ ] **Step 3: Implement.** `go get github.com/thesyncim/gopus@v0.2.2`; `newVoiceEncoder` = `gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: 1, Application: gopus.ApplicationVoIP})` then `SetBitrate(bitrate)` (error checked), `SetFEC(true)`, `SetPacketLoss(10)`. Encode via `EncodeInt16(pcm, obuf)`. Decoder: `gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, 1))`, decode via `DecodeInt16`, PLC via `DecodeInt16(nil, buf)` with the same buffers as today. Drop `hraban/opus` (`go mod tidy`); go directive `1.27.0`.
- [ ] **Step 4: Run** `go test -race -count=1 ./audio/ ./client/ ./integration/`, then `go vet ./...`, `go test -count=1 ./...`, and `go list -deps ./... | Select-String hraban` (expected: no output). Expected: PASS.
- [ ] **Step 5: Commit** `audio: pure-Go Opus (gopus) replaces libopus`.

### Task 2: `-version` flag and static Windows link

**Files:**
- Modify: `cmd/hermec/main.go`
- Create: `cmd/hermec/main_test.go`

**Interfaces:**
- Produces: `hermec -version` prints exactly `hermec <version.Version>\n` to stdout and exits 0; used by Task 3.

- [ ] **Step 1: Write the failing test** `TestVersionFlag`: builds the binary into `t.TempDir()` with `go build -o`, runs it with `-version` (timeout 10 s), asserts exit 0 and stdout == `"hermec dev\n"`. Skip under `testing.Short()`.
- [ ] **Step 2: Run** `go test ./cmd/hermec/ -run VersionFlag`. Expected: FAIL (flag provided but not defined).
- [ ] **Step 3: Implement** a `version` bool flag checked right after `flag.Parse()`, before the log line and `ui.Run`.
- [ ] **Step 4: Verify** the test passes, then build statically and check imports:
  `go build -trimpath -ldflags "-s -w -extldflags=-static" -o $env:TEMP\hermec-static.exe ./cmd/hermec` and `objdump -p $env:TEMP\hermec-static.exe | Select-String "DLL Name"` — expected: only Windows system DLLs (kernel32, user32, gdi32, winmm, ole32, ws2_32, msvcrt/api-ms-win-crt-*, etc.), none of `libwinpthread`, `libgcc`, `libopus`. Run `$env:TEMP\hermec-static.exe -version`. Report the DLL list.
- [ ] **Step 5: Commit** `cmd/hermec: -version flag`.

### Task 3: Release workflow: client matrix, checks, PR dry runs

**Files:**
- Modify: `.github/workflows/release.yml`, `.github/workflows/ci.yml`
- Delete: `scripts/build-release.ps1`

**Interfaces:**
- Consumes: `hermec -version` (Task 2).

- [ ] **Step 1: Rewrite `release.yml`.** Triggers: `push: tags: ['v*']` and `pull_request`. `TAG` = `github.ref_name` on tags, `dev-pr` otherwise.
  - Job `client` with a matrix over the four rows of spec section 3.4 (`os`, `goarch`, `asset`, and for Windows the msys2 `msystem`: `UCRT64` with packages `mingw-w64-ucrt-x86_64-gcc`, `CLANGARM64` with `mingw-w64-clang-aarch64-clang`). Windows legs put the msys2 env's `bin` on PATH (`steps.msys2.outputs.msys2-location`), set `CC` (`gcc` / `clang`), and build with `-extldflags=-static`. All legs: `CGO_ENABLED=1 go build -trimpath -ldflags "<stamp> [static]" -o <bin> ./cmd/hermec`.
  - Checks per leg (fail the job on violation): run `<bin> -version` and require output `hermec $TAG`; Windows: `objdump -p` DLL names must not resolve to any file under the msys2 install; Linux: `ldd <bin>` lines must all match `linux-vdso|ld-linux|libc\.so|libm\.so|libpthread|libdl|libresolv|librt`. If `-version` on Linux needs X/GL libraries at process start, install the runtime packages (`libgl1 libx11-6 libxrandr2 libxcursor1 libxinerama1 libxi6 libxxf86vm1`) rather than skipping the check.
  - Packaging: Windows zip = `hermec.exe` + `README.txt`; Linux tar.gz = `hermec` (mode 755) + `README.txt`. README.txt text: one line on what HERMEC is, "Use headphones (no echo cancellation yet).", Windows: the SmartScreen "More info → Run anyway" line and the firewall "choose Allow" line; Linux: "Needs a desktop with X11 or Wayland and PulseAudio or PipeWire." Upload as artifact named after the asset.
  - Job `server`: today's three server builds (CGO 0), unchanged names.
  - Job `publish`: `if: startsWith(github.ref, 'refs/tags/v')`, `needs: [client, server]`, today's checksum + `gh release create` logic unchanged.
  - If the `windows-11-arm` / CLANGARM64 leg cannot be made to work, do not drop it silently: report to the controller with the error.
- [ ] **Step 2: `ci.yml`:** remove the `libopus-dev libopusfile-dev pkg-config` install (keep the step only if something else still needs it). Delete `scripts/build-release.ps1`.
- [ ] **Step 3: Validate locally:** `go run github.com/rhysd/actionlint/cmd/actionlint@latest` (expected: no errors).
- [ ] **Step 4: Commit** `ci: release builds the app for windows/linux on amd64 and arm64; PR dry runs`.
- [ ] **Step 5 (controller):** push the branch, open a PR, watch the run; feed failures back to this task until all legs pass.

### Task 4: Docs

**Files:**
- Modify: `README.md` (:62-74 releases, :143-173 build, :267-281 opus troubleshooting, :312), `PLAN.md:55`, `docs/specs/2026-10-06-hermec-design.md:97`, `HANDOFF.md`

- [ ] **Step 1:** Releases section: the four app assets (which to pick: x64 vs ARM; "Mac: build from source, below"), the server assets, checksums. Build section: Go 1.27+; a C compiler is still needed for the audio backend (Windows: MSYS2 `mingw-w64-ucrt-x86_64-gcc` only; Linux: `gcc`; macOS: Xcode command line tools); no opus/pkg-config. Remove the Windows runtime-DLL note and the opus troubleshooting entry. `go test -race ./...` note stays.
- [ ] **Step 2:** New section **"macOS: run from Terminal"**, short numbered steps: `xcode-select --install`; install Homebrew (link https://brew.sh); `brew install go git`; `git clone https://github.com/medeirosvictor/hermec.git && cd hermec`; `go build -o hermec ./cmd/hermec`; `./hermec`; microphone permission for Terminal (System Settings → Privacy & Security → Microphone); update with `git pull` then the build line again; why there's no Mac download (no paid Apple signing; HERMEC stays free).
- [ ] **Step 3:** PLAN.md and the design doc: one line each recording that libopus was replaced by pure-Go gopus (2026-10-19), malgo is the remaining cgo piece. HANDOFF.md: new "Last updated" entry for this round and the screen-share note "use govpx (pure Go VP8), not libvpx".
- [ ] **Step 4: Commit** `docs: portable builds, macOS from Terminal`.
