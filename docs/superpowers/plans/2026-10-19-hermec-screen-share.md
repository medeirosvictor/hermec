# Screen Share (+ Session Log, One Media Port) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Friends can share a Windows screen into a voice call and anyone can watch it, with per-run log files for debugging and all media on one UDP port.

**Architecture:** VP8 via pure-Go govpx; Windows GDI capture via x/sys/windows; Pion SFU forwards video only to watchers and relays PLIs; additive protocol messages (`share_start`, `share_stop`, `watch`, `VoiceMember.sharing`). A small `applog` package owns per-run log files.

**Tech Stack:** Go 1.27, Pion WebRTC v4 (+ pion/ice/v4 UDP mux), `github.com/thesyncim/govpx` (VP8), `golang.org/x/sys/windows`, Ebiten v2.10.

**Spec:** `docs/superpowers/specs/2026-10-19-hermec-screen-share-design.md`

## Global Constraints

- Log files: `<user config dir>/hermec/logs/hermec-YYYY-MM-DD_HH-MM-SS.log`, keep newest 10; never log chat text, keys or passwords; hosted-server lines prefixed `[server] `; call-health line every 30 s.
- Media port: config key `media_port` (0 = same as listen port); `udp_port_min/max` take precedence when set; error text `media port <n> is in use`.
- Video: VP8 PT 96 with RTCP feedback `nack` and `nack pli`; 720p = fit within 1280×720 at 15 fps, CBR 1500 kbps; 1080p = fit within 1920×1080, 3000 kbps; keyframe every 5 s; PLI relay at most 1/s per sharer; video stream id `video-<fp>`.
- Protocol: `share_start {}`, `share_stop {}`, `watch {"fingerprint": string}` (`""` stops), `VoiceMember.sharing` (json `sharing`, omitempty), error code `not_sharing`. Additive only; protocol Version stays 1.
- UI copy: `share`, `stop share`, `LIVE`, `screen N (WxH)`, `<name> stopped sharing`, `share quality: 720p` / `1080p`, `logs: open folder`, tooltip `screen share from this OS is coming later`.
- Env (PowerShell): prepend `C:\msys64\ucrt64\bin` to PATH, `CGO_ENABLED=1`. Never launch the GUI, never touch `%APPDATA%\hermec` (tests use temp dirs), never kill hermec.exe, never bind 7697 in tests (use port 0). Never bare `git stash`.
- Checkout is CRLF (autocrlf): check gofmt on changed files with CRs stripped.
- Commit trailer: blank line then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. A viewer who starts watching mid-stream must see a picture within ~1 s (PLI on watch + 5 s keyframes) — Task 6 integration test `TestLateWatcherGetsKeyframe`.
2. Unwatched sharers must cost the host nothing: no video RTP to non-watchers — Task 5 `TestVideoOnlyToWatchers`.
3. Encoder slower than real time must skip frames, never queue (memory/latency growth) — Task 6 `TestShareSkipsWhenEncoderBusy`.
4. A sharer leaving voice / disconnecting must clear `sharing` and end every watcher's view — Task 5 `TestShareClearedOnLeave`, Task 7 `TestWatchEndsWhenSharerStops`.
5. Log file write failures (unwritable dir, disk full) must never crash or block the app — Task 1 `TestOpenFallsBackWhenDirUnwritable`.

---

### Task 1: Session log file

**Files:**
- Create: `applog/applog.go`, `applog/applog_test.go`
- Modify: `cmd/hermec/main.go`, `ui/app.go` (logf, Options), `ui/settings.go` + `ui/state/settings.go` rows if needed, `ui/hosted.go`, `server/config.go` (+ where server logs: `server/sfu.go`, `cmd/hermec-server/main.go` unaffected), `ui/callbar.go` or a new `ui/health.go` for the 30 s line

**Interfaces:**
- Produces: `applog.Open(dir string, now time.Time, keep int) (f *os.File, path string, err error)` — creates dir, prunes to `keep-1` older `hermec-*.log` files before creating the new one; `applog.Dir(configDir string) string` = `<configDir>/hermec/logs`. `server.Config.Logf func(format string, args ...any)` (nil → `log.Printf`), used for every server log line. `ui.Options.LogDir string` (empty = no file).
- [ ] **Step 1: Tests** in `applog_test.go`: `TestOpenCreatesNamedFile` (name matches `hermec-2026-10-19_18-30-05.log` for that `now`), `TestOpenKeepsNewestTen` (12 existing → 10 remain incl. new), `TestOpenIgnoresOtherFiles`, `TestOpenFallsBackWhenDirUnwritable` (returns error, no panic). In `ui`: `TestLogfAlwaysWritesFile` (non-verbose game with a buffer log writer receives breadcrumbs), `TestHostedServerLinesPrefixed` (hosted server's Logf output starts with `[server] `), `TestHealthLineFormat` for the pure formatter (ping + per-person jitter/concealed; no chat text).
- [ ] **Step 2: Run** `go test ./applog/ ./ui/ -run 'Open|Logf|Prefixed|HealthLine'`. Expected: FAIL (compile).
- [ ] **Step 3: Implement.** main.go opens the file (on error: console only, one warning line) and passes it via Options; the file receives every line exactly once (unconditional `log` lines and all breadcrumbs); console behaviour unchanged. First lines: version, GOOS/GOARCH, Go version, audio device names. Health ticker every 30 s while in a call. Settings row `logs: open folder` uses the existing open-URL helper on the directory.
- [ ] **Step 4: Run** focused tests, then `go vet ./...` and `go test -race -count=1 ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `applog: per-run log file, server prefix, call health`.

### Task 2: One media port

**Files:**
- Modify: `server/config.go`, `server/sfu.go:165-202` (newRTCAPI), `server/server.go` (lifecycle: create/close the mux), `example.server.toml`, `README.md` ("How safe is this?" gap 1)
- Test: `server/sfu_test.go` or new `server/mediaport_test.go`

**Interfaces:**
- Produces: `Config.MediaPort int` (`toml:"media_port"`); effective port = MediaPort, else the listen port actually bound (so port 0 in tests gives a real port). Uses `ice.NewMultiUDPMuxFromPort(port)` + `SettingEngine.SetICEUDPMux`; AllowLoopbackICE filtering still applies; closed on server Shutdown.
- [ ] **Step 1: Tests:** `TestMediaUsesListenPort` (server on 127.0.0.1:0 → a UDP socket is bound on the same port number; existing voice integration tests still pass, proving media flows), `TestMediaPortInUse` (pre-bind UDP port → Start returns error containing `media port <n> is in use`), `TestLegacyUDPRangeStillWorks` (udp_port_min/max set → no mux).
- [ ] **Step 2: Run** `go test ./server/ -run 'MediaUses|MediaPort|LegacyUDP'`. Expected: FAIL.
- [ ] **Step 3: Implement**; README gap 1 rewritten: the grant recipe from the spec (section 2), how to paste it in the Tailscale admin Access controls page, and that it needs v0.5.0+ on the host.
- [ ] **Step 4: Run** focused tests, then `go test -race -count=1 ./server/ ./integration/ ./client/`. Expected: PASS.
- [ ] **Step 5: Commit** `server: all media on one UDP port (media_port)`.

### Task 3: Video codec package + speed gate

**Files:**
- Create: `video/frame.go` (I420 `Frame{W, H int; Y, U, V []byte}`, `FitSize`, `FromBGRA` with scaling), `video/vp8.go` (Encoder/Decoder over govpx), tests and `video/bench_test.go`

**Interfaces:**
- Produces: `video.FitSize(sw, sh, maxW, maxH int) (w, h int)` (aspect kept, even, never upscales); `video.FromBGRA(dst *Frame, src []byte, sw, sh, stride int)` (resizes dst to dst.W×dst.H set by caller; box/bilinear downscale); `video.NewEncoder(w, h, fps, kbps int) (*Encoder, error)`; `(*Encoder).Encode(f *Frame, forceKey bool) (pkt []byte, key bool, err error)` (keyframe at least every `5*fps` frames); `(*Encoder).Close()`; `video.NewDecoder() (*Decoder, error)`; `(*Decoder).Decode(pkt []byte) (*Frame, error)` (nil frame when none ready); `(*Frame).RGBA(dst *image.RGBA)`.
- [ ] **Step 1: Tests:** `TestFitSize` table (2560×1440→1280×720; 1920×1200→1152×720; 800×600→800×600; odd → even), `TestFromBGRAColors` (solid red/green/blue BGRA → expected Y/U/V within ±2), `TestRoundTrip` (encode 30 synthetic frames 1280×720, first is key, decode all, PSNR of last ≥ 30 dB), `TestForceKey`, `TestKeyEvery5s` (frame 75 at 15 fps is key). Benchmarks `BenchmarkEncode720p`, `BenchmarkDecode720p`, `BenchmarkFromBGRA1440to720` using a desktop-like synthetic sequence (text-like stripes + a moving window).
- [ ] **Step 2: Run** `go test ./video/`. Expected: FAIL.
- [ ] **Step 3: Implement** with govpx realtime/CBR options (fastest speed setting that keeps PSNR ≥ 30 dB).
- [ ] **Step 4: Run** tests, then `go test ./video/ -run '^$' -bench . -benchtime 60x`. **Gate (report numbers):** encode ≤ 30 ms/op, decode ≤ 10 ms/op. If the gate fails, stop and report (controller rules per spec).
- [ ] **Step 5: Commit** `video: VP8 encode/decode and I420 conversion (govpx)`.

### Task 4: Windows screen capture

**Files:**
- Create: `screencap/screencap.go` (types + `New() Source`), `screencap/gdi_windows.go`, `screencap/stub_other.go`, `screencap/screencap_test.go`

**Interfaces:**
- Produces: `type Monitor struct{ Index int; Name string; X, Y, W, H int }` (Name = `screen N (WxH)`); `type Source interface{ Monitors() ([]Monitor, error); Capture(m Monitor, dst *BGRA) error; Close() error }`; `type BGRA struct{ W, H, Stride int; Pix []byte }` (reused between captures); `screencap.Supported bool` (true on Windows); stub returns `ErrUnsupported`.
- [ ] **Step 1: Tests:** `TestMonitorName` (pure); Windows-only `TestCaptureMonitorSize` (first monitor → dst W/H equal monitor W/H, Pix non-zero; `t.Skip` when Monitors() is empty or errors with no desktop); `TestCaptureReusesBuffer` (second capture doesn't reallocate).
- [ ] **Step 2: Run** `go test ./screencap/`. Expected: FAIL.
- [ ] **Step 3: Implement** with x/sys/windows: EnumDisplayMonitors/GetMonitorInfo, GetDC(0)+CreateCompatibleDC+CreateDIBSection+BitBlt(SRCCOPY|CAPTUREBLT), cursor via GetCursorInfo+DrawIconEx; physical pixels (DPI-aware coordinates; note the app's DPI awareness).
- [ ] **Step 4: Run** tests; `go vet ./...`; cross-compile check `GOOS=linux go vet ./screencap/` and `GOOS=darwin go vet ./screencap/`. Expected: PASS.
- [ ] **Step 5: Commit** `screencap: Windows GDI monitor capture`.

### Task 5: Protocol + SFU video

**Files:**
- Modify: `core/proto/proto.go` (types, payloads, error code), `server/voice.go` (share_start/stop/watch handling, sharing flag in voice_state, clear on leave/disconnect), `server/sfu.go` (VP8 codec, watcher-filtered fan-out, PLI on watch + relay with 1/s limit, video kbps in stats)
- Test: `server/share_test.go`, `core/proto` tests

**Interfaces:**
- Produces: `proto.TypeShareStart = "share_start"`, `TypeShareStop = "share_stop"`, `TypeWatch = "watch"`, `proto.Watch{Fingerprint string}`, `VoiceMember.Sharing bool`, `proto.CodeNotSharing = "not_sharing"`.
- [ ] **Step 1: Tests** (pion peers in-process, loopback ICE): `TestShareStartSetsSharing` (voice_state shows sharing), `TestShareStartOutsideVoiceIgnored`, `TestWatchNonSharerErrors` (`not_sharing`), `TestVideoOnlyToWatchers` (publisher sends VP8 RTP; watcher receives packets; non-watcher's PC has no video track/receives none), `TestWatchSendsPLI` (sharer's PC receives a PLI within 1 s of a new watch), `TestPLIRelayRateLimited` (5 viewer PLIs in 1 s → ≤ 2 reach sharer), `TestShareClearedOnLeave`, proto round-trip tests for new types.
- [ ] **Step 2: Run** `go test ./server/ ./core/proto/ -run 'Share|Watch|VideoOnly|PLI'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** focused tests, then `go test -race -count=1 ./server/ ./core/... ./integration/`. Expected: PASS.
- [ ] **Step 5: Commit** `server: screen share signalling and watcher-only VP8 relay`.

### Task 6: Client share + watch

**Files:**
- Modify: `client/voice.go` (VP8 in MediaEngine, video sender track, PLI reading → force key, video receive path), `client/client.go` (API)
- Create: `client/share.go`; Test: `client/share_test.go`, `integration/share_test.go`

**Interfaces:**
- Consumes: `video.*` (Task 3), proto types (Task 5).
- Produces: `type FrameSource interface{ Next(dst *video.Frame) error }` (blocking-free: returns the latest captured frame); `(*Client).StartShare(src FrameSource, w, h, fps, kbps int) error`; `(*Client).StopShare() error`; `(*Client).Watch(fp string) error`; `type VideoSink interface{ WriteVideoFrame(fp string, f *video.Frame) }` set via `(*Client).SetVideoSink(VideoSink)`; `(*Client).VideoStats() (sendKbps, recvKbps, fps float64)`.
- [ ] **Step 1: Tests:** integration `TestShareWatchEndToEnd` (A shares synthetic frames, B watches → B's sink gets frames, first decoded frame non-nil; C gets none), `TestLateWatcherGetsKeyframe` (B watches 3 s after start → first frame within 1 s), `TestStopShareEndsWatch` (B's sink/voice_state shows A not sharing), unit `TestShareSkipsWhenEncoderBusy` (slow fake encoder → frames dropped, queue never grows past 1), `TestPLIForcesKeyframe`.
- [ ] **Step 2: Run** `go test ./client/ ./integration/ -run 'Share|Watch|Keyframe|PLI'`. Expected: FAIL.
- [ ] **Step 3: Implement** (encode loop on its own goroutine with a 15 fps ticker; decode off the caller goroutine; sink receives newest frame only).
- [ ] **Step 4: Run** focused tests, then `go test -race -count=1 ./client/ ./integration/ ./server/`. Expected: PASS.
- [ ] **Step 5: Commit** `client: share a screen and watch a sharer`.

### Task 7: UI

**Files:**
- Modify: `ui/callbar.go` (share/stop share button + monitor menu via `ui/menu.go`), `ui/state/hits.go` (HitBarShare, HitLive, HitVideo), `ui/state/voice.go` + channel drawing (LIVE tag), `ui/main_scene.go` (video view replaces chat; Esc; double-click fullscreen; drawn after the CRT pass), `ui/settings.go` + `ui/state/settings.go` (`ShareQuality string toml:"share_quality"`), health line video stats
- Create: `ui/video.go` (watch view, frame → ebiten image upload on the game goroutine), tests `ui/video_test.go`

**Interfaces:**
- Consumes: `screencap` (Task 4), client share/watch API (Task 6).
- [ ] **Step 1: Tests (headless, existing helpers):** `TestShareButtonOpensMonitorMenu` (fake Source with 2 monitors → menu items `screen 1 (2560×1440)`, `screen 2 (1920×1080)`), `TestShareButtonDimOffWindows` (Supported=false → no menu, tooltip text), `TestLiveTagHit` (sharing occupant → HitLive registered; click → Watch(fp) called and view = video), `TestEscLeavesVideo`, `TestWatchEndsWhenSharerStops` (voice_state without sharing → back to chat, status `<name> stopped sharing`), `TestShareQualityRowCycles` (720p ↔ 1080p, persisted), `TestVideoLetterbox` (pure geometry: 16:9 frame in 4:3 pane).
- [ ] **Step 2: Run** `go test ./ui/ ./ui/state/ -run 'Share|Live|Esc|Watch|Quality|Letterbox'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** focused tests, then `go vet ./...`, `go test -race -count=1 ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `ui: share button, LIVE tags and the watch view`.

### Task 8: Docs

**Files:** `README.md` (Screen share section: how to share/watch, quality setting, Windows-only sharing, bandwidth note "the host uploads one copy per watcher, ~1.5 Mbps each at 720p"; Logs section: where files are, how to send one), `HANDOFF.md` (round entry, Victor's real-test checklist from spec section 5, deferred items).
- [ ] **Step 1:** Write both, plain short sentences.
- [ ] **Step 2: Commit** `docs: screen share and logs`.
