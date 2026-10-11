# Footprint snapshot

Dated measurements of Hermec's resource usage, taken so future changes can be
compared against a baseline. Re-measure after each major plan and append a new
section; don't overwrite history.

## 2026-10-07 — after the discovery plan (main @ db700e4)

Measured on Windows 11, AMD RX 6900 XT rig, Go 1.27, CGO on for the client
(opus/miniaudio), off for the server. RAM figures are Windows working set.

### Binary sizes

| Binary | Stock | `-ldflags "-s -w"` |
|---|---|---|
| hermec-server (pure Go, CGO off) | 17.1 MB | 11.9 MB |
| hermec (GUI client, CGO on) | 43.2 MB | 21.7 MB |

### Runtime

| Scenario | RAM | CPU |
|---|---|---|
| Server, idle (loopback, no calls) | 56 MB | ~0% |
| Server, relaying a live 2-client voice call | 65 MB | **0.7% of one core** |
| GUI client, connect screen | 70 MB | — |
| GUI client, connected (-local: includes the embedded server in-process) | 87 MB | ~28% of one core |

2-client synthetic voice call: 20ms frames, ~40kbps per stream, sampled over
15s. Server threads: 7 idle → 17 in-call.

### Reading the numbers

- The server comfortably clears the Raspberry Pi bar: a live call costs under
  1% of one core and ~65 MB. Dozens of concurrent voice streams are plausible
  on small hardware.
- Reference point: Electron-based chat apps typically idle at 300–500 MB and
  a full browser engine. The Hermec GUI sits at 70–87 MB including Go runtime,
  WebRTC stack, and GPU textures.
- **Known hot spot (SUPERSEDED, historical; see the 2026-10-11 section):** the GUI burns ~28% of one core while sitting idle in a
  chat — the Ebiten render loop redraws at 60fps and relays out the scrollback
  layout every tick (a known deferred item: cache the layout on
  (message count, last message, cols)) plus the CRT shader pass. This is the
  top performance follow-up; target is single-digit % idle. _Annotation: the layout
  cache landed afterwards (59a2a29 "cache scrollback layout and render", release
  round), and the 2026-10-11 profile shows layout is no longer visible and the
  CRT pass costs nothing measurable (CRT on == off). That figure was an earlier
  build measured in a populated chat; the later numbers are the main scene with
  an almost empty chat, so they are not strictly like-for-like._
- The connected GUI figure includes the `-local` embedded server; a client
  connected to a remote server will sit lower.

## 2026-10-11 — after the visual refresh (worktree-hermec-visual @ e5f2426)

Same rig. Release-style build (`go build -ldflags "-s -w"`, CGO on, 22.2 MB).
Method: `hermec.exe -local`, amber preset, window shown on top, Enter posted to
the connect form, main scene confirmed by screenshot, 8s settle, then CPU time
(`Process.TotalProcessorTime`) over 60s wall divided by 60s, as % of one core.
RAM is working set (private bytes in parentheses). CPU was profiled via a
temporary `net/http/pprof` listener (not committed): `go tool pprof` on a 25s
`/debug/pprof/profile`.

| Scenario (main scene, `-local`, idle, 60s) | CPU | RAM |
|---|---|---|
| Refresh build, CRT on (curvature + scanlines) | 16.0 / 19.3 / 20.1 / 17.5 / 14.1% (mean ~17.4) | 73 MB (123) |
| Refresh build, CRT off | 16.0 / 20.3 / 16.1% (mean ~17.5) | 72 MB (115) |
| Pre-refresh build (55cbe82), scanlines on, same session | 17.0 / 14.6 / 13.3% (mean ~15.0) | 71 MB (108) |

### Reading the numbers

- **The 6.5% baseline did not reproduce.** Today the pre-refresh build also
  idles at 13-17% on this machine (other processes running, noisy to +/-3).
  Compared back-to-back in the same session the refresh costs about +2.5
  points over the old build, inside the noise band, so no fix was made.
- CRT on and off cost the same, so the CRT shader and glow passes are not the
  idle cost.
- CPU profile (CRT on): ~93% of samples sit in `IDXGISwapChain.Present`
  (driver/vsync wait, not our code). The app's own `Draw` is 3.8% of a core,
  mostly text (`drawTextF` 2.3%); panels and CRT are well below 1%. The
  deferred cached-panel layer and the `anim.go` allocation fix are therefore
  not justified by this measurement.
- RAM is +2 MB working set (+15 MB private) over the pre-refresh build, from
  offscreen images and shaders.
- Re-measure on a quiet machine before treating the absolute numbers as the
  new baseline; the relative comparison is the reliable part.

### Outcome against the brief, method details, follow-up

- **Sample counts:** CRT on n=5, CRT off n=3, pre-refresh build n=3 (means above).
- **Enter mechanism:** `PostMessage(WM_KEYDOWN/WM_KEYUP, VK_RETURN)` to the
  window handle from `Process.MainWindowHandle` (the window is first set
  topmost via `SetWindowPos`); no foreground focus needed. Main scene
  confirmed by a window screenshot each run.
- **pprof:** temporary `import _ "net/http/pprof"` plus
  `go http.ListenAndServe("127.0.0.1:6061", nil)` in `cmd/hermec/main.go`
  (reverted, not committed); profile fetched with
  `curl "http://127.0.0.1:6061/debug/pprof/profile?seconds=25"`.
- **Pre-refresh build:** commit 55cbe82, extracted with `git archive` to a
  scratch dir and built with the same command (`go build -ldflags "-s -w"`,
  CGO_ENABLED=1, msys2 ucrt64 PATH, Go 1.27), same script, same shell, runs
  interleaved with the refresh build in one session (23.2 MB binary).
- **Result vs the ~2 point rule:** the 6.5% baseline could not be reproduced
  today (the old build idles ~15%). The relative delta of ~2.5 points
  nominally crosses the "~2 points" threshold but sits inside the +/-3 noise
  band, and the profile shows the app's own draw code is only ~3.8% of a core
  in total, so no draw-path fix could recover it. No fix was made.
- **Single-digit idle target is still unmet** at ~17%.
- **Follow-up (not implemented):** ~93% of the cost is `Present`, i.e. a 60fps
  loop paced against vsync. The next lever is redraw-on-demand / an idle frame
  cap (reduced TPS when nothing animates, `ebiten.SetVsyncEnabled` and
  screen-clear trade-offs), not draw-code optimisation. Note the CRT scan bar
  animates continuously, so with CRT on the cap would need to be a modest
  rate rather than zero.
