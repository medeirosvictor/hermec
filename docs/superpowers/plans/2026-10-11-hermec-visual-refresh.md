# Hermec Visual Refresh (Round A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the GUI a 40k-style curved CRT look with soft panels, a parchment light theme, pointer cursor, smooth speaking highlights and a connection-quality indicator, with a dev live-reload mode so Victor can steer the look while it is built.

**Architecture:** Theme presets move to embedded TOML (live-reloadable in dev mode). The CRT shader gains barrel distortion, vignette and a rolling scan bar; a pure Go `Barrel` mirrors it for click correction. Panels are drawn by an SDF shader; clicks and cursor share a per-frame hit registry. Pure animation helpers drive fades. The client measures RTT with WebSocket pings; the audio mixer keeps per-sender health; a pure `Grade` turns both into signal bars.

**Tech Stack:** Go 1.26, Ebiten v2 (Kage shaders, `vector`, `text/v2`), gorilla/websocket, malgo + libopus (cgo), BurntSushi/toml.

**Spec:** `docs/superpowers/specs/2026-10-11-hermec-visual-refresh-design.md`

## Global Constraints

- Every commit: `gofmt` clean, `go vet ./...` clean, `go test -count=1 ./...` green, and `go test -race -count=1 ./audio/... ./ui/... ./client/...` green. Shell setup: PATH includes `/c/msys64/ucrt64/bin`, `CGO_ENABLED=1`.
- Commit trailer: `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- No wire-protocol changes; v0.1.0 servers must keep working with the new client.
- Pure logic lives in `ui/state` or `ui/theme` (no Ebiten imports there) and is TDD'd.
- Nothing on the draw path writes files or blocks; dev polling runs from Update at most once per second.
- Theme: unknown keys stay errors; missing keys keep the preset's values.
- After each task the app must build and run: `go build -o bin/hermec.exe ./cmd/hermec && ./bin/hermec.exe -local`.
- Spec values are authoritative: curvature 0.06, vignette 0.35, scanline_strength 0.12, scan_bar_period 8s, scan_bar_strength 0.04, glow 0.12, radius 6px, pad ≈14px, attack 80ms, release 400ms, fade τ ≈150ms, ping every 2s, EWMA α 0.2, pong timeout 6s, health window ~5s, grade thresholds 80/150/300ms and 1/3/8%.

## Review Focus

1. **Window resized while CRT is on** → `Barrel` uses the live `w,h`; clicks stay accurate after resize (Task 3 test with two sizes).
2. **Dev reload with a broken shader or TOML mid-edit** → previous good version stays, error in status line, no crash, and the next good save recovers (Task 2 test).
3. **Leaving a call or a sender going quiet** → health rows for departed senders disappear; a sender who simply stops talking is not counted as concealing (Task 8 test).
4. **Server disconnect while the detail panel is open** → bars show 0, panel shows "no data", no stale numbers or panics (Task 9 test).
5. **Saved `palette = "amber"` from v0.1.0 settings, or a custom `-theme` file without the new keys** → loads with defaults for the new keys; F2 from a custom theme goes to the first preset (Task 1 test).

---

### Task 1: Theme keys, TOML presets, parchment

**Files:**
- Modify: `ui/theme/theme.go`, `ui/theme/default.toml`, `ui/theme/theme_test.go`
- Create: `ui/theme/presets/amber.toml`, `green.toml`, `blue.toml`, `parchment.toml` (embedded with `//go:embed presets/*.toml`)
- Modify: `ui/settings.go` (`presetByName`, `presetName`, `cyclePalette`), `example.theme.toml`, `README.md` (F2 line mentions parchment)

**Interfaces:**
- Produces: `theme.Theme` gains `Name string; Panel color.RGBA; Curvature, Vignette, ScanlineStrength, ScanBarStrength, ScanBarPeriod, Glow, Radius float64; ScanBar bool`. `theme.Parse(name string, src []byte) (Theme, error)` (used by dev reload). `theme.PresetSource(name string) ([]byte, bool)` returns the embedded TOML. `NextPreset` matches on `Name` (empty name → first preset). `theme.Load(path)` sets `Name = ""`.

- [ ] **Step 1: Failing tests:** `TestPresetsOrder` (names amber, green, blue, parchment); `TestParchmentValues` (bg `#EDE4D3`, fg `#3B2414`, dim `#8A7560`, bright `#7A1F1F`, panel `#E3D7C1`, Glow 0, ScanlineStrength 0.04); `TestDerivedPanel` (a TOML without `panel` gives BG mixed 6% toward FG, per channel `bg + (fg-bg)*0.06` rounded); `TestNewKeyDefaults` (old-style custom file with only bg/fg/dim/bright/scanlines/font_size → Curvature 0.06, Vignette 0.35, ScanlineStrength 0.12, ScanBar true, ScanBarPeriod 8, ScanBarStrength 0.04, Glow 0.12, Radius 6); `TestUnknownKeyStillErrors`; `TestNextPresetCyclesByName` (parchment → amber; custom/unnamed → amber).
- [ ] **Step 2:** `go test ./ui/theme/ -run 'Preset|Parchment|Derived|NewKey|Unknown' -count=1` → FAIL.
- [ ] **Step 3:** Implement. Muted dark presets: keep each preset's fg/bright, soften `dim` ~15% toward bg, and set `panel` explicitly (amber `#15120C`, green `#0F160F`, blue `#10141B`). `panel` stays optional in the file struct (empty string = derive).
- [ ] **Step 4:** Update `ui/settings.go` so palette persistence uses `Theme.Name`; `go test -count=1 ./ui/...` → PASS.
- [ ] **Step 5:** Commit `feat(theme): TOML presets, CRT/panel theme keys, parchment light preset`.

### Task 2: Dev live-reload (`HERMEC_DEV=1`)

**Files:**
- Create: `ui/state/watch.go`, `ui/state/watch_test.go`, `ui/devreload.go`
- Modify: `ui/crt.go` (shader source swappable), `ui/app.go` (poll from Update), `README.md` (Development section: dev mode)

**Interfaces:**
- Produces: `state.Watcher{Stat func(path string) (time.Time, error)}` with `Add(path string)`, `Changed(now time.Time) []string` (checks at most once per second; first call records baselines and returns nil; a missing file is not a change). `(*crt).setSource(src []byte) error` compiles first and only swaps on success. `g.dev *devReload` (nil when `HERMEC_DEV` unset).
- Watched paths: `ui/crt.kage`, `ui/panel.kage` (Task 4 registers its shader with the same mechanism), `ui/theme/presets/<g.th.Name>.toml` when the active theme is a preset, and the `-theme` file if given.

- [ ] **Step 1: Failing tests:** `TestWatcherBaselineNoChange`, `TestWatcherDetectsMtimeChange`, `TestWatcherThrottlesToOncePerSecond`, `TestWatcherMissingFileNotChange`, all with a fake `Stat` and injected `now`.
- [ ] **Step 2:** `go test ./ui/state/ -run Watcher -count=1` → FAIL.
- [ ] **Step 3:** Implement watcher, then `devreload.go`: on change, read the file; `.kage` → `setSource`; `.toml` → `theme.Parse(g.th.Name, src)` (or `theme.Load` for the `-theme` file), keeping font size and `crtOn`. Errors go to `g.st.Status` as `dev reload <file>: <err>`, and the old version stays. Faces are rebuilt only if `font_size` changed.
- [ ] **Step 4: Review Focus #2 test:** `TestDevReloadBadShaderKeepsOld` in `ui/devreload_test.go`: `setSource` with invalid Kage returns an error and the previous shader pointer is unchanged; a following valid source swaps. → PASS, along with the full suite.
- [ ] **Step 5:** Manual: `HERMEC_DEV=1 ./bin/hermec.exe -local`, edit `ui/theme/presets/amber.toml` bg → window changes within ~1s. Commit `feat(ui): HERMEC_DEV live reload of shaders and theme`.

### Task 3: CRT shader rewrite + click correction

**Files:**
- Modify: `ui/crt.kage`, `ui/crt.go`, `ui/app.go`
- Create: `ui/state/barrel.go`, `ui/state/barrel_test.go`, `ui/cursor.go`, `ui/crt_test.go`
- Modify every `ebiten.CursorPosition()` call site: `ui/main_scene.go:174,238`, `ui/callbar.go:286`, `ui/connect.go:95,202`, `ui/rail.go:167`, `ui/settings.go:265`

**Interfaces:**
- Produces: `state.Barrel(x, y, w, h, k float64) (float64, float64)`: the scene coordinate shown at screen pixel (x,y). Formula: `u = 2x/w − 1, v = 2y/h − 1, f = 1 + k·(u²+v²)`, result `((u·f+1)·w/2, (v·f+1)·h/2)`. `crt.kage` must use the identical formula. `g.cursor() (float64, float64)`: `ebiten.CursorPosition()` passed through `Barrel` when `g.crtOn && g.th.Curvature > 0`, otherwise raw. All hit tests use `g.cursor()`.
- Shader uniforms: `Curvature, Vignette, ScanlineStrength, ScanBarStrength, ScanBarPeriod, Time float`, `ScanBar float` (0/1), `Size vec2`, `BG vec4`. Corner mask radius = `Curvature·0.5·min(w,h)`, soft edge 2px. Out-of-range source → `BG`. Scan bar: band centred at `y = h·fract(Time/ScanBarPeriod)`, height 6% of h, smoothstep falloff, adds `ScanBarStrength`. Glow pass alpha = `Theme.Glow` (skip the pass when 0).

- [ ] **Step 1: Failing tests:** `TestBarrelCentreFixed` (w/2,h/2 unchanged); `TestBarrelZeroIdentity` (k=0 returns input); `TestBarrelCornerPullsOutward` (k=0.06, w=960,h=600, (0,0) → (−57.6, −36), since f = 1 + 0.06·2 = 1.12); `TestBarrelResize` (Review Focus #1: same relative point at 960×600 and 1920×1080 maps to the same relative position); `TestCRTShaderCompiles` (`ebiten.NewShader(crtShaderSrc)` succeeds).
- [ ] **Step 2:** `go test ./ui/state/ -run Barrel -count=1` → FAIL.
- [ ] **Step 3:** Implement `Barrel`, the shader, `crt.apply(screen, th, t)` passing uniforms (`t` = seconds since start), and `g.cursor()`; replace all call sites.
- [ ] **Step 4:** Full suite → PASS. Manual: click a rail tile and a channel near each corner with CRT on; they hit.
- [ ] **Step 5:** Commit `feat(ui): curved CRT with vignette and rolling scan bar; curvature-corrected clicks`.

### Task 4: Panels and negative space

**Files:**
- Create: `ui/panel.kage`, `ui/panel.go`
- Modify: `ui/main_scene.go` (`geom`, `drawMain`, `drawChannels`), `ui/callbar.go` (bar on a panel), `ui/connect.go`, `ui/settings.go` (draw on panels), `ui/rail.go` (tiles on panels), `ui/geom_test.go`

**Interfaces:**
- Produces: `(g *game) drawPanel(dst *ebiten.Image, x0, y0, x1, y1 float64, fill color.RGBA)`: SDF rounded rect, radius `g.th.Radius`, ~10px soft outer fade into BG, compiled once, registered with dev reload. Constants: `pad = 14`, `margin = 16` (outer gap from the window edge).
- Removes: the three 1px separator `vector.FillRect` calls in `drawMain` and the one above the call bar.

- [ ] **Step 1: Failing tests:** extend `geom_test.go`: `TestGeomMargins` (no pane starts before `margin` or ends after `w−margin`/`h−margin`); `TestGeomPanelsDontOverlap` (channel pane, chat pane, input, bar rects pairwise disjoint with ≥ `pad` gap) at 960×600 and with the call bar visible; `TestPanelShaderCompiles`.
- [ ] **Step 2:** `go test ./ui/ -run 'Geom|Panel' -count=1` → FAIL.
- [ ] **Step 3:** Implement. The voice divider becomes a dim `voice` label with rules faded via 3 alpha steps. The status line sits on its own slim panel. The scrollback cache key gains the panel colour.
- [ ] **Step 4:** Full suite → PASS. Manual look in all four presets.
- [ ] **Step 5:** Commit `feat(ui): soft rounded panels and wider spacing replace 1px separators`.

### Task 5: Hit registry + pointer cursor

**Files:**
- Create: `ui/state/hits.go`, `ui/state/hits_test.go`
- Modify: `ui/app.go` (rebuild registry each Update before scene updates; set cursor shape), `ui/main_scene.go`, `ui/callbar.go`, `ui/rail.go`, `ui/settings.go`, `ui/connect.go`, `ui/update.go` (register targets; click handlers query the registry)

**Interfaces:**
- Produces: `state.HitKind` constants `HitChannel, HitVoice, HitRailTile, HitRailAdd, HitGear, HitSettingsRow, HitBarMute, HitBarLeave, HitBanner, HitSignal, HitText`; `state.Hit{X0, Y0, X1, Y1 float64; Kind HitKind; ID int; Key string}`; `state.Hits` with `Reset()`, `Add(Hit)`, `At(x, y float64) (Hit, bool)` (last added wins, so overlays registered later take priority). `HitText` → `CursorShapeText`; any other hit → `CursorShapePointer`; none → `CursorShapeDefault`. `ebiten.SetCursorShape` is called only when the shape changes.

- [ ] **Step 1: Failing tests:** `TestHitsAtInside`, `TestHitsAtOutside`, `TestHitsLaterWins`, `TestHitsResetClears`, `TestHitsEdgesHalfOpen` (x1/y1 exclusive).
- [ ] **Step 2:** `go test ./ui/state/ -run Hits -count=1` → FAIL.
- [ ] **Step 3:** Implement; migrate each click handler to `g.hits.At(g.cursor())`, keeping behaviour identical (existing ui tests stay green).
- [ ] **Step 4:** Full suite → PASS. Manual: hand cursor on every clickable listed in spec §6, text cursor over the chat input and connect fields.
- [ ] **Step 5:** Commit `feat(ui): shared hit registry; pointer cursor on clickables`.

### Task 6: Animation helpers, speaking envelope, transitions

**Files:**
- Create: `ui/state/anim.go`, `ui/state/anim_test.go`
- Modify: `ui/main_scene.go` (occupant glow, active/hover highlight), `ui/callbar.go` (slide in/out), `ui/app.go` (`dt` from `1/ebiten.ActualTPS()` clamped to [1/240, 1/20])

**Interfaces:**
- Produces: `state.Approach(cur, target, dt, tau float64) float64` = `target + (cur−target)·exp(−dt/tau)`. `state.Envelope{Attack, Release float64; v float64}` with `Step(level, dt float64) float64` (moves toward `level` with τ=Attack when rising, τ=Release when falling). Constants in `ui`: `fadeTau = 0.15`, `speakAttack = 0.08`, `speakRelease = 0.4`, `speakMaxAlpha = 0.35`. Per-occupant envelopes are kept in `map[string]*state.Envelope` keyed by fingerprint, pruned when the occupant leaves.

- [ ] **Step 1: Failing tests:** `TestApproachConverges` (after 5τ within 1% of target); `TestApproachNoOvershoot`; `TestEnvelopeAttackFasterThanRelease` (rise to 63% in ≈0.08s, fall to 37% in ≈0.4s, ±10%); `TestEnvelopeZeroDtNoChange`.
- [ ] **Step 2:** `go test ./ui/state/ -run 'Approach|Envelope' -count=1` → FAIL.
- [ ] **Step 3:** Implement. Glow alpha = `min(1, sqrt(env)·k)·speakMaxAlpha` over the occupant row (text keeps `th.FG`/`th.Bright`, no colour inversion); muted → envelope target 0. The call bar's height animates via `Approach` and `geom()` reads the animated value.
- [ ] **Step 4:** Full suite → PASS. Manual two-window call: glow fades in and out, choppy mic visibly flickers.
- [ ] **Step 5:** Commit `feat(ui): smooth speaking glow and eased panel transitions`.

### Task 7: Client RTT measurement

**Files:**
- Create: `client/rtt.go`, `client/rtt_test.go`
- Modify: `client/client.go` (start pinger after handshake; stop on Close)

**Interfaces:**
- Produces: `(*Client).RTT() (rtt time.Duration, ok bool)` (ok=false until the first pong). Pure `rttEWMA{v time.Duration; ok bool}` with `observe(sample time.Duration)` (α 0.2; first sample sets v directly). The pinger sends `WriteControl(PingMessage, 8-byte big-endian UnixNano, deadline now+1s)` every 2s; `SetPongHandler` parses the payload and observes `now − sent`; no pong within 6s observes 6s as a sample.

- [ ] **Step 1: Failing tests:** `TestEWMAFirstSample`, `TestEWMASmooths` (100ms then 200ms → 120ms), `TestClientRTTAgainstServer` (in-process `server.New` on 127.0.0.1:0, dial, `RTT()` ok within 5s and < 100ms), `TestPongHandlerIgnoresGarbage` (payload ≠ 8 bytes is ignored, no panic).
- [ ] **Step 2:** `go test ./client/ -run 'EWMA|RTT|Pong' -count=1` → FAIL.
- [ ] **Step 3:** Implement. Make sure `SetPongHandler` doesn't break the existing read loop or the server's keepalive pings.
- [ ] **Step 4:** `go test -race -count=1 ./client/... ./integration/...` → PASS.
- [ ] **Step 5:** Commit `feat(client): measure RTT with WebSocket pings`.

### Task 8: Per-sender audio health

**Files:**
- Create: `audio/health.go`, `audio/health_test.go`
- Modify: `audio/playback.go` (sender counters in `Write` and `fill`; `Meters.Health`, `Meters.Senders`)

**Interfaces:**
- Produces: `audio.Health{Concealed float64; Jitter time.Duration; OK bool}`. `(*Meters).Health(fp string) Health`, `(*Meters).Senders() []string`. Pure `healthWindow` that stores 1-second buckets of `{played, concealed int}` for the last 5 buckets; `Concealed = concealed/(played+concealed)`; `OK=false` when the total is 0. Jitter: `j += (|gap−20ms| − j)/16` (RFC 3550 style) updated in `Write`. "Concealed" counts PLC slots plus TryLock-miss silence slots **only while `js.started`**. Silence before start or after the sender goes quiet is not counted (Review Focus #3). Atomics or `s.mu`-guarded fields only; no new allocations on the audio thread.

- [ ] **Step 1: Failing tests:** `TestHealthWindowRatio` (90 played + 10 concealed → 0.10); `TestHealthWindowRollsOff` (after 6 empty seconds → OK=false); `TestJitterSteady20msIsZero`; `TestJitterAlternating10_30` (converges near 10ms); `TestMixerQuietSenderNotConcealing` (sender stops writing → concealed fraction doesn't rise past the PLC budget, then OK=false once the sender is swept); `TestMetersSendersDropsDeparted`.
- [ ] **Step 2:** `go test ./audio/ -run 'Health|Jitter|Quiet|Senders' -count=1` → FAIL.
- [ ] **Step 3:** Implement with an injectable clock (reuse `mixer.now`).
- [ ] **Step 4:** `go test -race -count=1 ./audio/...` → PASS.
- [ ] **Step 5:** Commit `feat(audio): per-sender concealment and jitter health`.

### Task 9: Grade, signal bars, detail panel

**Files:**
- Create: `ui/state/grade.go`, `ui/state/grade_test.go`, `ui/quality.go`
- Modify: `ui/callbar.go`, `ui/main_scene.go` (`drawStatus`), `ui/app.go` (Esc closes the panel), `README.md` (Keys: click the bars), `HANDOFF.md`

**Interfaces:**
- Consumes: `(*Client).RTT()` (Task 7), `(*Meters).Health/Senders` (Task 8), `state.Hits` `HitSignal` (Task 5), `drawPanel` (Task 4), `Approach` (Task 6).
- Produces: `state.Grade(rtt time.Duration, rttOK bool, concealed float64, jitter time.Duration) int`: 0 when !rttOK; 4 if rtt<80ms && concealed<0.01; 3 if <150ms && <0.03; 2 if <300ms && <0.08; else 1. In a call, `concealed` = the worst sender's ratio; out of a call it's 0. `(g *game) drawSignal(dst, x, y, h float64, bars int)`: four ascending bars, lit = `th.Bright`, unlit = `th.Dim`. Detail panel lines: `ping 42 ms`, `jitter 6 ms`, `concealed 0.8%`, then `<name>  1.2%  8 ms` per participant (names from voice occupancy), or `no data` when !rttOK.

- [ ] **Step 1: Failing tests:** table test `TestGrade` covering each boundary (79/80ms, 0.99%/1%, 299/300ms, rttOK=false → 0); `TestGradeWorstSenderDominates`; `TestQualityPanelNoDataOnDisconnect` (Review Focus #4: a pure `state.QualityLines(...) []string` with rttOK=false and no senders → `["no data"]`).
- [ ] **Step 2:** `go test ./ui/state/ -run 'Grade|QualityLines' -count=1` → FAIL.
- [ ] **Step 3:** Implement `Grade`, `QualityLines`, and the drawing. Bars sit on the call bar (right of the mic level) in a call, otherwise at the right end of the status line. Clicking `HitSignal` toggles the panel; Esc or a second click closes it; the panel opens with `Approach`.
- [ ] **Step 4:** Full suite + race → PASS. Manual: bars show 4 on localhost; the panel lists the second window's participant.
- [ ] **Step 5:** Update HANDOFF.md (round A done pending Victor's eyeball checklist; Round B next). Commit `feat(ui): connection-quality signal bars and detail panel`.

### Task 11: Custom title bar (Windows)

> Execution order: Tasks 11 and 12 run after Task 9 and before Task 10, so the footprint check measures the final pipeline.

**Files:**
- Create: `ui/titlebar.go` (layout + drawing, all platforms), `ui/state/titlebar.go`, `ui/state/titlebar_test.go`
- Modify: `ui/winbar_windows.go` (replace `darkTitleBar` with `nativeDrag(ht uintptr)` and `hasCustomBar() bool { return true }`), `ui/winbar_other.go` (`nativeDrag` no-op, `hasCustomBar` false), `ui/app.go` (`SetWindowDecorated(!hasCustomBar())` before `RunGame`; Draw renders the scene to an offscreen of `w × (h−barH)` and blits it at `y = barH`; Layout gives the scene `h − barH`), `ui/cursor.go` (subtract `barH` before Barrel), `README.md`

**Interfaces:**
- Consumes: `state.Hits` (Task 5), `g.cursor()` (Task 3), `theme.Face`.
- Produces: `const titleBarH = 32`; `(g *game) barH() float64` (0 when `!hasCustomBar()`). Pure `state.TitleZone(x, y, w, h, barH, edge float64, maximized bool) Zone` with `Zone` constants `ZoneNone, ZoneCaption, ZoneMin, ZoneMax, ZoneClose, ZoneLeft, ZoneRight, ZoneTop, ZoneBottom, ZoneTopLeft, ZoneTopRight, ZoneBottomLeft, ZoneBottomRight`. Edge band 6px; buttons 46×32 at the right (close rightmost). No edge zones when maximized. Buttons win over the top edge band. `state.HTFor(Zone) uintptr` maps to Win32 HT codes (HTCAPTION 2, HTLEFT 10, HTRIGHT 11, HTTOP 12, HTTOPLEFT 13, HTTOPRIGHT 14, HTBOTTOM 15, HTBOTTOMLEFT 16, HTBOTTOMRIGHT 17; 0 for buttons/none).
- Colours (fixed, not themed): bg `#1E1F22`, text `#DBDEE1`, hover `#2E3035`, close hover `#DA373C`. Title: icon (Task 12; until then a 16px placeholder square) + "HERMEC" + " — <server name>" when connected (server name = the label the rail tile already shows for the current server).
- Behaviour: a left press in a caption/edge zone calls `nativeDrag(HTFor(zone))` (Windows: `ReleaseCapture` then `SendMessageW(hwnd, WM_NCLBUTTONDOWN=0xA1, ht, 0)`; hwnd found as `darkTitleBar` did). A double-click (two presses within 400ms in the caption zone) toggles `ebiten.MaximizeWindow` / `RestoreWindow`. Buttons call `MinimizeWindow`, maximize toggle, and close (`ebiten.Termination` from Update, so the existing shutdown path runs). Cursor shapes: `CursorShapeEWResize`, `NSResize`, `NWSEResize`, `NESWResize` over edges; pointer over buttons.

- [ ] **Step 1: Failing tests:** `TestTitleZoneButtons` (at w=960: close spans x∈[914,960), max [868,914), min [822,868), all y<32); `TestTitleZoneCaption` (x=400,y=10 → Caption); `TestTitleZoneEdges` (each of the 8 edge/corner zones at 960×600 with the 6px band); `TestTitleZoneButtonBeatsTopEdge` (x=930,y=2 → Close); `TestTitleZoneMaximizedNoEdges`; `TestHTFor` (all mappings above); `TestCursorSubtractsBar` (with barH 32 and curvature off, screen (100,132) → scene (100.5,100.5)).
- [ ] **Step 2:** `go test ./ui/state/ -run 'TitleZone|HTFor' -count=1` → FAIL.
- [ ] **Step 3:** Implement. Keep every existing scene test green (scene geometry now uses the scene height). The CRT corner mask applies to the scene area only.
- [ ] **Step 4:** Full suite + race → PASS. Manual (owner): drag by the bar, snap to the screen edges and Win+arrows, resize from all edges and corners, double-click maximize, the three buttons, and clicks inside the scene still accurate with the CRT on.
- [ ] **Step 5:** If `WM_NCLBUTTONDOWN` resizing or snap doesn't work on the undecorated GLFW window, record exactly what fails in the report (DONE_WITH_CONCERNS) rather than subclassing the window procedure; the controller will rule. Commit `feat(ui): Discord-style custom title bar with native drag, snap and resize`.

### Task 12: App icon

**Files:**
- Create: `ui/icon/icon.go`, `ui/icon/icon_test.go`, `cmd/hermec-icon/main.go`, `assets/icon-256.png`, `assets/icon.ico`
- Modify: `ui/app.go` (`ebiten.SetWindowIcon` with 16/32/48/256), `ui/titlebar.go` (use the 16px icon), `go.mod` (`golang.org/x/image` becomes a direct dependency)

**Interfaces:**
- Produces: `icon.Render(size int) *image.RGBA`. Geometry in units of `s = size`:
  - background: rounded square, full size, radius 0.22s, colour `#1E1F22`;
  - envelope: white (`#FFFFFF`) stroke outline, centred, width 0.46s, height 0.32s, centre y 0.53s, stroke `max(1.5px, 0.055s)`, with the flap as two lines from the top corners to (0.5s, envelope top + 0.6·height);
  - wings: three filled white feather lobes per side, attached at the envelope's side midpoints, fanning up and outward, outermost tip at x = 0.06s / 0.94s, lobes shrinking 100% / 75% / 55%, mirror images of each other;
  - all geometry built for the left half and mirrored about x = 0.5s, so the shape is symmetric by construction.
- `cmd/hermec-icon -out assets` writes `icon-256.png` and a multi-size `icon.ico` (16/32/48/256 PNG-in-ICO).

- [ ] **Step 1: Failing tests:** `TestIconSizes` (16/32/48/256 give the right bounds); `TestIconSymmetric` (for 32 and 256, every pixel equals its mirror within ±2 per channel); `TestIconCornersTransparent` (pixel (0,0) alpha 0, rounded corner); `TestIconHasWhiteAndDark` (the centre column contains near-white and `#1E1F22` pixels).
- [ ] **Step 2:** `go test ./ui/icon/ -count=1` → FAIL.
- [ ] **Step 3:** Implement `Render`, the cmd, `SetWindowIcon`, and the title-bar icon; run `go run ./cmd/hermec-icon -out assets` and commit the outputs.
- [ ] **Step 4:** Full suite → PASS. Include `assets/icon-256.png` in the report for the owner to eyeball.
- [ ] **Step 5:** Commit `feat: envelope-with-wings app icon`.

### Task 10: Footprint check

**Files:**
- Modify: `docs/footprint.md`

- [ ] **Step 1:** Build a release-style binary, run `-local` idle for 60s on the main scene with CRT on, and measure CPU % of one core as in the existing doc's method. Repeat with CRT off.
- [ ] **Step 2:** If CRT-on idle is more than ~2 points above 6.5%, profile with the method recorded in `docs/footprint.md` and cache the panel layer into an offscreen image keyed like `sbKey`; re-measure.
- [ ] **Step 3:** Record the numbers in `docs/footprint.md`. Commit `docs: footprint after visual refresh`.
