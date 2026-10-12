# Hermec Round B.1: Concurrent Server Sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every server opened in a run stays connected; switching rail tiles never drops chat or the voice call; the rail shows unread, reconnecting and "you are here" (bust) states; right-click a tile → Disconnect.

**Architecture:** The single connection on `ui.game` (`c`, `st`, `ms`, `curKey`, `lastURL`, `lastName`, `dialing`) becomes a `map[string]*session` plus the viewed key. Background results carry their session key. Pure rules (backoff, stable rail order, unread, tile state, menu geometry) live in `ui/state` and are tested headlessly; the session layer is tested against real in-process servers without Ebiten input.

**Tech Stack:** Go 1.26, Ebiten v2, existing `client`/`server` packages. No protocol change.

**Spec:** `docs/superpowers/specs/2026-10-11-hermec-concurrent-sessions-design.md`

## Global Constraints

- No wire-protocol change; `docs/protocol.md` is untouched.
- Agents must never launch the GUI against the real `%APPDATA%\hermec`, nor kill `hermec.exe` processes. All tests use temp dirs and in-process servers.
- cgo: PATH must include `C:\msys64\ucrt64\bin` and `CGO_ENABLED=1` for `go test ./ui/...` and `-race`.
- `ui/state` must not import Ebiten.
- Single-server behaviour must be unchanged except "join every text channel on connect".
- Backoff schedule: 1s, 2s, 4s, 8s, 16s, then 30s forever.
- Per-session event drain cap: `maxEventsPerTick` (64), sessions visited in sorted key order.
- Commit trailer: `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. Disconnecting a server while its dial is still in flight: the late `dialResult` must close its client and must not resurrect the session. (Task 5 test `TestDisconnectDuringDialClosesLateClient`.)
2. The call's server drops while you are viewing another server: devices are freed, the call bar goes away, the viewed server is untouched. (Task 6 test `TestCallSessionDropFreesDevices`.)
3. Typing the `-local` server's real URL into the connect form must reuse the `hermec://local` session, not open a second one. (Task 4 test `TestOpenSessionLocalURLMapsToLocalKey`.)
4. The `MaxServers` cap must never evict a server with a live session. (Task 1 test `TestTouchCapKeepsLive`.)
5. Enter on the "CONNECTION LOST" screen dials now and resets backoff rather than waiting up to 30s. (Task 5 test `TestRetryNowResetsBackoff`.)

---

### Task 1: Backoff schedule and stable rail order

**Files:**
- Modify: `ui/state/servers.go` (`Touch`)
- Create: `ui/state/backoff.go`
- Test: `ui/state/servers_test.go`, `ui/state/backoff_test.go`

**Interfaces:**
- Produces: `func NextBackoff(attempt int) time.Duration`; `func Touch(entries []ServerEntry, url, label, channel string, live func(url string) bool) []ServerEntry` (`live` may be nil = nothing live).

- [ ] **Step 1: Write failing tests**
  - `TestNextBackoff`: attempts 0..6 → 1s, 2s, 4s, 8s, 16s, 30s, 30s; attempt 100 → 30s; negative → 1s.
  - Update `TestTouchUpsertOrder`: after `a`, `b`, then touching `a` again, order stays `[b, a]` and `a` has `LastChannel "z"`, `Label "a2"`.
  - `TestTouchNewGoesFirst`: a new URL lands at index 0.
  - `TestTouchCapKeepsLive`: 20 entries (=`MaxServers`), last one `ws://live/`; touching a new URL with `live` true for `ws://live/` keeps `ws://live/` and drops the last non-live entry; length stays `MaxServers`.
  - Existing cap/alias/empty-keeps-fields tests pass with `nil` live.
- [ ] **Step 2:** `go test ./ui/state/ -run 'Backoff|Touch'` → FAIL (compile).
- [ ] **Step 3: Implement.** `Touch` updates an existing entry in place (copying the slice, no aliasing); a new entry is prepended; when over the cap, remove from the end the last entries whose URL is not live. Update the one caller in `ui/rail.go` to pass `nil` for now (Task 4 passes the real predicate).
- [ ] **Step 4:** `go test ./ui/state/` → PASS.
- [ ] **Step 5: Commit** `ui/state: stable rail order, live-aware cap, reconnect backoff`.

### Task 2: Unread tracking and tile state

**Files:**
- Modify: `ui/state/state.go`
- Create: `ui/state/tile.go`
- Test: `ui/state/state_test.go`, `ui/state/tile_test.go`

**Interfaces:**
- Produces on `State`: field `Unread map[string]bool` (initialised in `New`; `SetConnected` never clears it), field `Viewed bool`, `func (s *State) SetViewed(v bool)`, `func (s *State) MarkRead(ch string)`, `func (s *State) AnyUnread() bool`.
- Produces: `type TileFacts struct{ Viewed, HasSession, Reconnecting, InCall, Unread bool }`, `type TileState struct{ Accent, Dim, Pulse, Dot, Bust bool }`, `func Tile(f TileFacts) TileState`.

- [ ] **Step 1: Write failing tests**
  - `TestUnreadOtherChannel`: viewed, active `general`, chat on `random` → `Unread["random"]`, `AnyUnread()`.
  - `TestUnreadActiveViewedNotMarked`: viewed, chat on active → not unread.
  - `TestUnreadActiveNotViewed`: `Viewed=false`, chat on active → unread.
  - `TestUnreadOwnMessageIgnored`: chat from own fingerprint on another channel → not unread.
  - `TestSetViewedClearsActive`: unread on active, `SetViewed(true)` → cleared; others kept.
  - `TestMarkRead`: clears one channel.
  - `TestReconnectKeepsScrollbackAndUnread`: `SetConnected` called a second time with the same channels keeps `Messages` and `Unread` (scrollback survives reconnect, spec 3.4).
  - `TestTile`: table — `{}` → `Dim`; `{HasSession}` → all false; `{HasSession, Reconnecting}` → `Dim, Pulse`; `Viewed` → `Accent`; `Unread` → `Dot`; `InCall` → `Bust`.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement.** `Apply` marks unread unless `Viewed && Channels[Active] == ch`, and never for `From.Fingerprint == s.fingerprint`. `SetConnected` must stop wiping `Messages` (it already does not) and must not wipe `Unread`. Rules for `Tile`: `Accent=Viewed`, `Dim=!HasSession||Reconnecting`, `Pulse=Reconnecting`, `Dot=Unread`, `Bust=InCall`.
- [ ] **Step 4:** `go test ./ui/state/` → PASS.
- [ ] **Step 5: Commit** `ui/state: unread per channel and rail tile state`.

### Task 3: Context-menu geometry

**Files:**
- Create: `ui/state/menu.go`
- Modify: `ui/state/hits.go` (add `HitMenuItem`, ID = item index, appended after `HitDiscovered`)
- Test: `ui/state/menu_test.go`

**Interfaces:**
- Produces: `func MenuOrigin(ax, ay, w, h, winW, winH float64) (x, y float64)`; `HitMenuItem`.

- [ ] **Step 1: Write failing tests** `TestMenuOrigin`: anchor fits → unchanged; overflows right → `x = winW - w`; overflows bottom → `y = winH - h`; window smaller than menu → clamps to 0, never negative.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement** `MenuOrigin` and the new hit kind. Make `ui/cursor.go` give `HitMenuItem` the hand cursor.
- [ ] **Step 4:** `go test ./ui/state/ ./ui/` → PASS.
- [ ] **Step 5: Commit** `ui/state: context menu geometry`.

### Task 4: The session table (single-server behaviour unchanged)

The big mechanical task. After it, the app behaves as today except: clicking another tile no longer disconnects the current server, and every text channel is joined on connect. No reconnect, no menu, no rail visuals yet.

**Files:**
- Create: `ui/session.go`, `ui/session_test.go`
- Modify: `ui/app.go`, `ui/rail.go`, `ui/main_scene.go`, `ui/connect.go`, `ui/titlebar.go`, `ui/quality.go`, `ui/callbar.go` (only `g.c`/`g.st`/`g.ms` → session accessors; voice semantics change in Task 6), `ui/rail_test.go`, plus any other file the compiler flags.

**Interfaces:**
- Consumes: `state.Touch(..., live)`, `State.SetViewed`, `State.MarkRead`.
- Produces (used by Tasks 5–8):
  ```go
  type session struct {
      key, url, name string
      c       *client.Client
      st      *state.State
      ms      mainScene
      dialing bool
      everUp  bool      // connected at least once
      attempt int       // reconnect attempts since last success (Task 5)
      retryAt time.Time // zero = no retry scheduled (Task 5)
  }
  func (g *game) cur() *session                 // viewed session or g.none (PhaseConnect placeholder, never nil)
  func (g *game) openSession(url, name string) *session // key via keyFor; reuse or create; dials unless connected/dialing; nil if local-not-running (sets g.none.st.ConnectErr)
  func (g *game) view(key string)               // sets g.curKey, SetViewed on old/new sessions; "" = connect scene
  func (g *game) pumpSessions()                 // dial/join/send results + per-session event drain
  func (g *game) sessionKeys() []string         // sorted
  ```
  `g.sess map[string]*session`, `g.curKey string` (kept name, now "viewed key"), `g.none *session`.
  `dialResult{key string; c *client.Client; err error}`, `joinResult{key, channel string; err error}`, `sendCh chan sendResult` with `sendResult{key string; err error}`.
  `func (g *game) saveServer(s *session, channel string)`, `func (g *game) onConnected(s *session)`.

- [ ] **Step 1: Write failing tests** in `ui/session_test.go` with a helper `startServer(t, addr string) (*server.Server, string)` (config like `-local`: channels `general`, `random`; voice `voice`; builtin roles; `Discoverable: false`) and `pumpUntil(t, g, cond func() bool)` (calls `g.pumpSessions()` every 10ms, fails after 5s). Build the game with `newTestGame(t)`: identity from `identity.Generate()`, `serversPath` in `t.TempDir()`, channels made like `Run`.
  - `TestTwoSessionsStayConnected`: open A, view A; open B, view B; both reach `PhaseMain`, A's client is still non-nil and not closed (send chat on A succeeds).
  - `TestJoinAllTextChannels`: after connect, `s.ms.joined` has `general` and `random`.
  - `TestBackgroundChatMarksUnread`: view A; a second raw `client.Dial` to B sends on `general` → B's `st.AnyUnread()`; A's not.
  - `TestRailClickExistingSessionDoesNotRedial`: `switchServer` on a connected session's entry only changes `curKey`; the client pointer is unchanged.
  - `TestOpenSessionLocalURLMapsToLocalKey`: with `g.localURL` set, `openSession(g.localURL, ...)` and the rail entry `hermec://local` resolve to the same session.
  - `TestFirstDialFailureDropsSession`: `openSession("ws://127.0.0.1:1/", ...)` → after pump, no session for that key, `curKey == ""`, `g.none.st.ConnectErr != ""`.
  - Update `TestStartDialLocalNotRunningDoesNotWedge` to the new API (`openSession(localKey, …)` returns nil, no session, `g.none.st.ConnectErr` set, `addServer` then works).
- [ ] **Step 2:** `go test ./ui/ -run 'Session|Join|Unread|RailClick|Local|FirstDial|Wedge'` → FAIL.
- [ ] **Step 3: Implement.**
  - Replace the listed fields with the session table; every `g.c`/`g.st`/`g.ms` becomes `g.cur().c` etc.; `g.dialing` becomes `g.cur().dialing`; status line uses `s.url`/`s.name`.
  - `Update` calls `g.pumpSessions()` where it used to read `dialCh`/`joinCh`/`sendCh`/events. A result whose key has no session is dropped; a stale `dialResult` client is closed.
  - Successful dial: `SetConnected`, `everUp = true`, `onConnected(s)` joins every text channel (remembered one shown as active) and calls `saveServer`. Failed **first** dial: delete the session, `view("")`, copy the error into `g.none.st.ConnectErr` (connect form shows it, as today). Failed dial with `everUp`: keep the session in `PhaseDisconnected` with the error in `ConnectErr` (Task 5 adds the retry schedule).
  - Closed events channel: `st.Apply(_, false)`, `c = nil`, and `dropVoice()` only if this is the viewed session (Task 6 makes this "the call session").
  - `switchServer(e)`: existing session → `view(key)`; otherwise `openSession` + `view`. `addServer`: `view("")` and fresh form; never closes anything. `leaveServer` is deleted.
  - Connect form submit: `openSession` then `view(key)`.
  - `saveServer` passes `live = func(u string) bool { _, ok := g.sess[u]; return ok }` to `Touch`.
  - `afterChannelChange` and `view` call `MarkRead`/`SetViewed` so the open channel never shows unread.
  - Shutdown in `Run`: leave voice as today, then close every session's client.
- [ ] **Step 4:** `go test ./ui/...` and `go vet ./...` → PASS; `go test -race ./ui/...` → PASS.
- [ ] **Step 5: Commit** `ui: one session per server; switching tiles keeps connections`.

### Task 5: Disconnect and background reconnect

**Files:**
- Modify: `ui/session.go`, `ui/main_scene.go` (`updateDisconnected`, disconnected screen copy), `ui/app.go` (call `tickReconnect(time.Now())` each Update)
- Test: `ui/session_test.go`

**Interfaces:**
- Consumes: `NextBackoff`, Task 4 session API.
- Produces: `func (g *game) disconnect(key string)`; `func (g *game) tickReconnect(now time.Time)`; `func (g *game) retryNow(key string)`.

- [ ] **Step 1: Write failing tests**
  - `TestDisconnectViewedFallsBack`: A, B connected, viewing A; `disconnect(A)` → A's client closed, no A session, `curKey == B` (top-most on rail with a session). Disconnecting the last one → `curKey == ""`.
  - `TestDisconnectDuringDialClosesLateClient`: open A, `disconnect(A)` before the dial result is pumped; after pumping, still no session for A and the late client was closed (check via the server: its connection count returns to zero, or `client.Events()` channel closes).
  - `TestReconnectAfterServerRestart`: A connected; shut A's server down → session goes `PhaseDisconnected` with `retryAt` set to ~1s ahead; restart a server on the same addr; `pumpUntil` with `tickReconnect(time.Now())` in the loop → `PhaseMain`, `attempt == 0`, all text channels re-joined, earlier scrollback still present.
  - `TestRetryNowResetsBackoff`: a disconnected session with `attempt = 5` → `retryNow` sets `attempt = 0`, `dialing = true`.
  - `TestBackgroundFailureNoStatusNoise`: a non-viewed session's failed retry does not change the viewed session's `Status`.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement.** On events closed or a failed dial with `everUp`: `retryAt = now + NextBackoff(attempt)`, `attempt++`. `tickReconnect` dials every session with `!dialing && c == nil && !retryAt.IsZero() && now >= retryAt`. Success resets `attempt`, clears `retryAt`. `disconnect`: leave call if `callKey == key` (until Task 6 lands, `dropVoice` if viewed), close client, delete, fall back the view. `updateDisconnected` → Enter calls `retryNow(curKey)`. Disconnected screen text: `CONNECTION LOST — reconnecting in Ns, Enter to retry now` (N = ceil seconds until `retryAt`), `reconnecting...` while dialing.
- [ ] **Step 4:** `go test ./ui/...` and `-race` → PASS.
- [ ] **Step 5: Commit** `ui: per-session disconnect and background reconnect`.

### Task 6: Voice across sessions

**Files:**
- Modify: `ui/callbar.go`, `ui/quality.go`, `ui/anim.go` (speaking glow), `ui/session.go`, `ui/main_scene.go`
- Create: `ui/state/call.go`
- Test: `ui/session_test.go`, `ui/state/call_test.go`

**Interfaces:**
- Consumes: Task 4/5 session API.
- Produces: `g.callKey string`; `func (g *game) callSess() *session` (nil when no call or joining); `func CallTitle(channel, server string, elsewhere bool) string` in `ui/state` → `"general"` or `"general @ primos"`.

- [ ] **Step 1: Write failing tests**
  - `TestCallTitle`: `("voice","primos",false)` → `"voice"`; `("voice","primos",true)` → `"voice @ primos"`.
  - `TestCallSessionDropFreesDevices`: sessions A (call) and B (viewed); set `g.callKey = A`, `g.vc` with two fake closers, A's `st.InCall = "voice"`; shut down A's server and pump → both closers closed, `callKey == ""`, A's `InCall == ""`, B's state (phase, `InCall`, `Status`) untouched.
  - `TestDisconnectCallSessionEndsCall`: same setup, `disconnect(A)` → closers closed (devices freed), `callKey == ""`.
  - `TestStaleVoiceResultDiscarded`: a `voiceResult` whose `c` is not the call session's client gets its closers closed and changes nothing.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement.**
  - `joinCall(ch)` on the viewed session: capture the old call session's client (if any) as the one to `LeaveVoice` in the goroutine; set `callKey` on success. `leaveCall`, `toggleMute`, Ctrl+M, `updateBar`, `addBarHits` act on `callSess()`. `barVisible` = a call exists or is joining, on any viewed server.
  - The dead-call poll and `dropVoice` use the call session, not the viewed one. Closed events on the call session drop the call; on other sessions they never touch voice.
  - Call bar title uses `CallTitle(InCall, serverTitle(g.servers, callKey), callKey != curKey)`.
  - Quality: RTT from `callSess().c` while a call exists, else `cur().c`.
  - Speaking glow only for rows of `callSess()`'s `InCall` channel when viewing the call session.
- [ ] **Step 4:** `go test ./ui/...` and `-race` → PASS.
- [ ] **Step 5: Commit** `ui: voice call follows its own server across tile switches`.

### Task 7: Rail and channel visuals

**Files:**
- Modify: `ui/rail.go` (`drawRail`, new `drawBust`), `ui/main_scene.go` (channel rows)
- Test: `ui/rail_test.go`

**Interfaces:**
- Consumes: `state.Tile`, `State.Unread`, `callKey`.
- Produces: `func (g *game) tileFacts(e state.ServerEntry) state.TileFacts`; `func drawBust(dst *ebiten.Image, x, y, size float32, c color.RGBA)`.

- [ ] **Step 1: Write failing test** `TestTileFacts`: game with sessions — viewed connected, background reconnecting (`everUp`, `c == nil`), call session, unread session, saved entry with no session → expected `TileFacts` for each.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement.** `drawRail` paints from `state.Tile(g.tileFacts(e))`: `Accent` = existing tint; `Dim` = `th.Dim` label; `Dot` = filled circle radius 3 in `th.Bright` at the tile's inner top-right; `Bust` = `drawBust` at inner bottom-right (size ≈ 12px at 100% scale: circle head on a shoulders arc, `vector` only); `Pulse` = small dot at inner bottom-left whose alpha follows `0.5+0.5*sin(2π·t/1.2s)`. Text channel rows with `Unread[ch]` draw in `th.Bright`; the active row keeps its highlight.
- [ ] **Step 4:** `go test ./ui/...` → PASS.
- [ ] **Step 5: Commit** `ui: rail shows unread, reconnecting and in-call bust`.

### Task 8: Right-click menu with Disconnect, docs

**Files:**
- Create: `ui/menu.go`, `ui/menu_test.go`
- Modify: `ui/rail.go` (right press on a tile opens the menu), `ui/app.go` (menu update before rail; hits added last in `buildHits`), `README.md` (Keys list), `HANDOFF.md`

**Interfaces:**
- Consumes: `state.MenuOrigin`, `state.HitMenuItem`, `g.disconnect`.
- Produces: `type ctxMenu struct{ open bool; key string; x, y float64; items []menuItem }`, `type menuItem struct{ label string; act func() }`, `func (g *game) openTileMenu(key string, ax, ay float64)`, `func (g *game) closeMenu()`.

- [ ] **Step 1: Write failing tests**
  - `TestTileMenuOnlyWithSession`: `openTileMenu` for a saved entry without a session leaves `open == false`; with a session → one item `"Disconnect"`.
  - `TestMenuDisconnectItem`: invoking item 0's `act` removes the session and closes the menu.
  - `TestMenuHitsWinOverRail`: with the menu open over a rail tile, `g.hits.At` at the item's centre returns `HitMenuItem`.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3: Implement.** Right mouse press (`inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonRight)`) over `HitRailTile` → `openTileMenu(g.servers[h.ID].URL, cursor)`. Menu is a soft panel (`drawPanelFade`), one `lineH()+pad` row per item, width = widest label + 2·pad, origin from `MenuOrigin`. Left click on an item runs it; left click elsewhere or Esc closes (Esc handled before the quality panel's Esc). While open, menu hits are added last in `buildHits`. README Keys: add **Right-click a rail tile**: Disconnect from that server; amend **Click rail tile** to say switching keeps you connected and in your call. HANDOFF: B.1 implemented, Victor's real-mouse checklist from spec section 5 queued, B.2 next.
- [ ] **Step 4:** `go vet ./...`, `go test ./...`, `go test -race ./...` → PASS.
- [ ] **Step 5: Commit** `ui: right-click tile menu with Disconnect; docs`.
