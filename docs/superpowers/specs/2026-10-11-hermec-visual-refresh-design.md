# Hermec — Visual Refresh & Polish (Round A) Design

**Date:** 2026-10-11
**Status:** Approved in conversation; written spec awaiting review
**Follow-up:** Round B (stay connected across servers + rail "you are here" bust) gets its own spec.

## 1. Intent

Make Hermec prettier, more attractive and more usable without leaving its identity. Victor's direction:

- The current look reads as Fallout New Vegas (sharp corners, 1px cuts). The target is closer to Warhammer 40k Rogue Trader / Space Marine interfaces: still a CRT, but with tube curvature, rounded vignetted corners and a slow rolling scan bar.
- More negative space; panels separated by soft fades rather than hard 1px lines; more muted colours.
- A light theme for readability: **parchment** (warm off-white, dark-brown ink, deep-crimson highlights).
- Clickable things show the hand pointer.
- Speaking highlight fades in and out smoothly and faintly, so mic pickup and dropouts are visible (an audio-debugging aid).
- A connection-quality indicator: glanceable signal bars, with numbers on click.

**Success:** Victor runs the app and it feels like a 40k data-slate rather than a terminal; clicks land where the eye says they should, even on the curved screen; he can tell from the UI who is breaking up in a call; idle CPU stays near today's ~6.5% of one core.

**Out of scope (Round B or later):** concurrent server connections, rail presence bust, resizable panels, typing indicator, right-click menu, member roster, avatars.

## 2. Dev live-reload

`HERMEC_DEV=1` turns on a dev mode in the GUI client:

- Once per second the client checks the modification time of `ui/crt.kage`, `ui/panel.kage` and the active preset file `ui/theme/presets/<name>.toml` (paths relative to the working directory, i.e. run from the repo root), plus the `-theme` file if one is given.
- On change it recompiles the shader or re-parses the theme and swaps it in. A compile or parse error is shown in the status line and the previous good version stays active; dev mode never crashes the app.
- Without `HERMEC_DEV`, nothing is polled and the embedded copies are used.

Presets move from Go literals into embedded TOML files (`ui/theme/presets/{amber,green,blue,parchment}.toml`) so the palette, including the light theme, is tweakable live. The `Settings.Palette` name stays the persisted key; existing names keep working.

## 3. CRT shader (`ui/crt.kage` rewrite)

Applied as today to the offscreen scene; F1 toggles it as a whole.

- **Barrel distortion.** Normalised coordinates centred on the screen, `uv' = uv · (1 + k·|uv|²)`, `k = curvature`. Pixels whose source falls outside the scene render as background (the rounded tube edge).
- **Rounded corners + vignette.** A rounded-rectangle mask (corner radius scales with curvature) with a soft dark falloff toward the edges, strength `vignette`.
- **Scanlines.** As today, strength `scanline_strength` (today's 0.12 for dark presets).
- **Rolling scan bar.** A soft horizontal band, about 6% of screen height, sweeping top to bottom with period `scan_bar_period` (default 8s), adding `scan_bar_strength` (default 0.04) brightness. Needs a `Time` uniform. `scan_bar = false` disables it.
- **Phosphor glow.** The existing additive 1px glow pass, strength from `glow` (0.12 dark presets, 0 for parchment because additive glow washes out a light background).

**Click correction.** Because the scene is sampled at `distort(p)` for screen pixel `p`, the content under the cursor is at `distort(cursor)`. A pure Go function `state.Barrel(x, y, w, h, k)` implements exactly the shader's formula; every hit test runs on the corrected position when the CRT is on. A unit test pins Go and shader together with known points (centre unchanged; corners pulled outward by the expected amount).

## 4. Theme

New keys (all optional; missing keys keep the preset defaults; unknown keys stay errors):

| Key | Type | Meaning |
|---|---|---|
| `panel` | colour | Panel surface. If absent, derived as BG mixed 6% toward FG. |
| `curvature` | float | Barrel strength (default 0.06; 0 = flat). |
| `vignette` | float | Edge darkening (default 0.35). |
| `scanline_strength` | float | Row darkening (default 0.12). |
| `scan_bar` | bool | Rolling scan bar on/off (default true). |
| `scan_bar_strength`, `scan_bar_period` | float | Band brightness and seconds per sweep. |
| `glow` | float | Additive phosphor glow (default 0.12). |
| `radius` | float | Panel corner radius in px (default 6). |

The existing `scanlines` key keeps working as the on/off switch for the whole CRT pass.

**Muted presets.** Amber, green and blue get softened `dim` tones and a `panel` colour.

**Parchment preset:** bg `#EDE4D3`, fg `#3B2414` (dark-brown ink), dim `#8A7560`, bright `#7A1F1F` (deep crimson), panel `#E3D7C1`, glow 0, scanline_strength 0.04, curvature and vignette kept. F2 cycles amber → green → blue → parchment; the Settings palette row includes it.

## 5. Panels and negative space

- Each area (channel pane, chat pane, input line, call bar, status line) sits on a panel surface: the `panel` colour, rounded corners (`radius`), and a soft gradient edge (~10px) into the background. Drawn by a small SDF rounded-rectangle shader `ui/panel.kage` via `DrawRectShader`.
- The 1px separator lines are removed; the gaps between panels (background showing through) do the separating.
- `pad` grows from 8 to about 14px, with an outer margin so panels never touch the curved edge (curvature would bend them).
- The voice divider in the channel list becomes a dim label with faded rules on either side.
- Geometry stays single-source in `geom()`; `geom_test.go` extends to cover the new margins.

## 6. Interaction

**Hit registry.** Each Update builds a list of click targets (`state.Hit{Rect, Kind, ID}`) from the same geometry that drawing uses: channel rows, voice rows, rail tiles, the "+" tile, the gear, settings rows, call-bar buttons, the update banner and the signal bars. Clicks and the cursor both query the registry (after curvature correction), so they can't disagree. Hovering a hit sets `ebiten.CursorShapePointer`, otherwise `CursorShapeDefault`; text input areas use `CursorShapeText`.

**Animation helpers** (pure, in `ui/state`, driven by a frame `dt`):
- `Approach(cur, target, dt, tau)`: exponential easing for fades and slides (~150ms).
- `Envelope`: attack ~80ms, release ~400ms, for the speaking highlight.

**Speaking highlight.** Each occupant's glow alpha = `Envelope(level)`, capped faint (max alpha ~0.35 of `bright`), drawn on the panel. Today's threshold-and-snap behaviour is replaced. Muted occupants never glow.

**Transitions.** Hover highlight, active-channel highlight, call bar slide-in on join/out on leave, and the quality detail panel opening all ease with `Approach`.

## 7. Connection-quality indicator

**Ping.** The client sends a WebSocket ping control frame every 2s with an 8-byte timestamp payload and times the matching pong (the server's gorilla default ping handler already replies; no protocol change, works with v0.1.0 servers). RTT is smoothed with an EWMA (α 0.2); a missing pong after 6s counts as a spike. Exposed as `client.(*Client).RTT() (time.Duration, bool)`.

**Audio health** (while in a call), per sender, in the mixer:
- counters: frames played, frames concealed (PLC), underrun slots (silence after the stream had started while the sender is active);
- inter-arrival jitter: smoothed |gap − 20ms|;
- exposed as `Meters.Health(fp) Health{Concealed float64 /*0..1 over last ~5s*/, Jitter time.Duration}` and `Meters.Worst()`.

**Grade.** A pure function `state.Grade(rtt, concealed, jitter) int` returns 0–4 bars:
4 = rtt < 80ms and concealed < 1%; 3 = rtt < 150ms and < 3%; 2 = rtt < 300ms and < 8%; 1 = worse; 0 = no data / disconnected. Table-tested.

**Display.** Four signal bars on the call bar while in a call, otherwise at the right end of the status line. Clicking opens a small panel: ping ms, jitter ms, concealed %, then one row per call participant with their concealed % and jitter. It reports what *you* receive: if everyone sees one person breaking up, that person's uplink is the problem. Click again or Esc to close.

## 8. Testing

- TDD for the pure logic: `Barrel`, `Grade`, `Approach`, `Envelope`, hit-registry lookup, preset TOML parsing (including parchment and derived `panel`), the dev-reload change detector (with an injected clock and stat function), RTT EWMA, per-sender health windows.
- Shader compile tests for `crt.kage` and `panel.kage` (Ebiten's `NewShader` compiles headlessly).
- The existing suite stays green, including `-race` for `audio/...` and `ui/...`.
- Idle CPU is re-measured against `docs/footprint.md` (target: within ~2 points of 6.5%).
- Victor's eyeball checklist: curvature and click accuracy in all four corners; scan bar visible but subtle; parchment readability; pointer cursor on every clickable; speaking glow fades in and out; bars react when a network throttle or a second laptop on Wi-Fi is used.

## 9. Risks

- **Click mismatch from curvature:** mitigated by the shared `Barrel` function and its pinning test.
- **CPU from animation:** the shader already runs every frame; the new cost is a few ALU ops plus panel shader draws. Panels are cached into the existing offscreen render where possible.
- **Text legibility on curvature:** curvature defaults low (0.06), configurable to 0.
- **Light theme versus CRT effects:** parchment turns off glow and lightens scanlines; tuned live through dev reload.
