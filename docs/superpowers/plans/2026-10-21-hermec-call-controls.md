# Call and Stream Controls Implementation Plan

> **For agentic workers:** Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Discord-style call and stream controls: a way out of fullscreen, on-video controls, a self-preview of your own share, a "voice connected" panel with disconnect, and clicking your own voice channel no longer leaving the call.

**Architecture:** All GUI work in `ui/` (Ebiten). Hit regions go through the existing `state.Hits` registry built each tick by `buildHits()` from the same geometry the draw code uses. Self-preview is client-side only (no protocol change): the sharer shows its own outgoing frames locally.

**Tech Stack:** Go, Ebiten v2, govpx VP8.

**Spec:** none — Victor's request 2026-10-20 (HANDOFF.md, "NEXT ROUND ... call and stream controls"), approved to implement without a design gate ("no need for my approval here, implement first"). Rulings are provisional against that text.

## Global Constraints

- Never run the GUI against the real `%APPDATA%\hermec` config; never kill hermec.exe; never bind port 7697 in tests.
- PowerShell; prepend `C:\msys64\ucrt64\bin` to PATH; `$env:CGO_ENABLED=1`. Run `gofmt -w` on changed Go files (CRLF checkout).
- Commit trailer: `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- No protocol change, no server change in this round.
- Match surrounding code: hit kinds in `ui/state/hits.go`, layout helpers shared by update and draw (like `barLayout()`), pure decisions in `ui/state` with unit tests.
- `go vet ./...` and `go test ./...` pass.

## Review Focus

1. Fullscreen entered then the watched sharer stops sharing (or you leave the call / switch server): the window must leave fullscreen, not strand you in a fullscreen chat.
2. Esc order: in fullscreen, Esc leaves fullscreen only; a second Esc goes back to chat.
3. Self-preview while you stop sharing, change monitor, or leave voice: preview ends cleanly, no stale frame.
4. Clicking your own voice channel while a join/leave is in flight (`g.vc.busy`) does nothing.
5. Overlay controls must not steal the double-click-to-fullscreen on the rest of the picture.

---

### Task 1: Fullscreen exit + on-video controls

**Files:** `ui/video.go`, `ui/titlebar.go`, `ui/app.go`, `ui/state/hits.go`, `ui/hits.go`, tests in `ui/video_test.go`.

- Fix: `drawTitleBar` must not draw while fullscreen (today the CRT path paints a dead bar at `ui/app.go:489`).
- Esc while fullscreen: leave fullscreen, keep watching. Esc while not fullscreen: back to chat (existing).
- F11 toggles fullscreen while the video is active.
- Whenever the video view ends (Esc, sharer stops, watch refused, leave call, switch server/channel, settings opened) and the window is fullscreen, leave fullscreen. Put the "should be fullscreen" decision in one place called from the update tick.
- On-video control strip: drawn at the bottom-centre of the video pane while the mouse moved over the pane in the last 2 s (and always while fullscreen and the mouse is in the bottom 15% of the pane). Buttons (text labels in the existing bar button style): `back to chat` (same as Esc out of everything), `fullscreen` / `exit fullscreen`, `stop watching` (ends the watch, same as Esc today). New hit kinds `HitVideoChat`, `HitVideoFull`, `HitVideoStop` registered before `HitVideo` so they win; clicks on them do not count toward the double-click.
- Tests: Esc in fullscreen exits fullscreen and keeps watching; watch end while fullscreen exits fullscreen; strip buttons hit and act; strip hidden after 2 s idle (inject the clock the way existing tests do). Fullscreen calls go through a seam (a func field) so tests do not touch the real window.

### Task 2: Self-preview of your own share

**Files:** `ui/video.go`, `ui/share.go`, `client/share.go` (client side only), tests.

- Clicking your own LIVE tag opens the video view showing your own outgoing stream (today `clickLive` returns at `ui/video.go:113` for `h.Key == g.fp`).
- Source of frames: tap the local share pipeline in the client (the frame after scaling, before or after encode — prefer the already-scaled RGBA frame so no decode is needed; drop frames if the UI is behind, latest wins). No network watch request is sent for self.
- Header text: `your stream (preview)  (Esc: chat, double-click: fullscreen)`.
- The overlay strip from Task 1, when previewing yourself, shows `stop sharing` and `change screen` (opens the existing share menu) in place of `stop watching`.
- Preview ends when you stop sharing or leave the call; fullscreen rules from Task 1 apply.
- Tests: own LIVE click opens preview without calling Watch; stop share ends the preview; preview frames come from the local tap.

### Task 3: Voice channel click + "voice connected" panel

**Files:** `ui/state/voice.go` (+ test), `ui/callbar.go`, `ui/main_scene.go`, `ui/state/hits.go`, `ui/hits.go`, tests.

- `VoiceClickAction(ch)` returns `"none"` when `s.InCall == ch`; `clickVoice` does nothing then (it never leaves). Update `ui/state/voice_test.go:85-94`.
- Discord-style "voice connected" panel at the bottom of the channel column (inside `chanR`, above its bottom edge; channel list shrinks to make room) while in a call on any server:
  - line 1: `voice connected` (accent colour) + signal bars / ping ms;
  - line 2: `<channel> / <server name>` (clipped);
  - buttons row: `disconnect` (leave voice = existing `leaveCall`) and `share` / `stop share` (existing share action).
  - Shown on whichever server is being viewed (the call can be on another server — B.1 concurrent sessions).
- The call bar keeps working as it is; this panel is an extra spot, Discord's spot.
- Tests: own-channel click does not leave; panel hits for disconnect and share; channel rows do not overlap the panel (geom test).

### Task 4: Compat test flake

**Files:** `integration/compat_test.go`.

- `TestAudioOnlyClientInCallWithShare` (~line 101, "client: no answer to watch") flakes on macOS CI. Apply the same warmUp/watchOK helper pattern the other share integration tests already use.
- Run it 20× locally: `go test ./integration -run TestAudioOnlyClientInCallWithShare -count=20`.
