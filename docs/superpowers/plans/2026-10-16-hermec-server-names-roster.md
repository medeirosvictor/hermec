# Hermec Round B.2: Server Names, Leave Server, Member List Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rail tiles, channel header and call bar show the server's own name (or your private nickname); right-click a tile → Rename / Disconnect / Leave Server; an online member list sits right of the chat, grouped Admins / Members / Bots, with a saved toggle.

**Architecture:** Two additive protocol changes (`AuthOK.ServerName`, a server-wide `roster` message broadcast from the existing `authed` set under `chMu`). The client library surfaces both. Pure rules (name precedence and cleaning, nick/remove, roster grouping and rows, tooltip timing and placement) live in `ui/state` and are tested headlessly; the UI layer adds a `dialog`, a header row, a members column and a toggle, all driven from the one `geometry` struct that hits and drawing already share.

**Tech Stack:** Go 1.26, Ebiten v2, gorilla/websocket, BurntSushi/toml. cgo (libopus) is needed for `./ui/...` and `-race`.

**Spec:** `docs/superpowers/specs/2026-10-16-hermec-server-names-roster-design.md`

**Verification prefix (PowerShell, used by every `Run:` below):**
`$env:CGO_ENABLED=1; $env:PATH="C:\msys64\ucrt64\bin;$env:PATH"; <go command>` — abbreviated as `[cgo]` in this plan. CI additionally runs `gofmt -l .` (must print nothing), `go vet ./...`, `go test -race -count=1 ./...`, and a 60 s `FuzzDecode`.

## Global Constraints

- Protocol version stays 1 (`proto.Version`); changes are additive only: old clients ignore `server_name` and `roster`, new clients treat their absence as "old server".
- `MaxRosterMembers = 200`.
- Server name is cleaned and capped to 64 runes client-side (`MaxServerNameRunes`); nicknames to 32 runes (`MaxNickRunes`).
- Members panel is 180 px at 100 % DPI (`membersPaneW = 180`, a plain constant like `channelPaneW`; the UI has no per-DPI scaling of its own).
- The members list auto-hides when the chat column would drop below 40 text columns (`minChatCols = 40`); the saved setting is untouched.
- Tooltip appears after 400 ms of hover (`state.TooltipDelay`).
- `Settings.Members *bool` with `toml:"members,omitempty"`; nil = shown. Global, not per server.
- Single-source geometry: hit rects and drawing both come from `geom()` / the dialog and menu rect helpers; no second copy of any rect.
- `ui/state` must not import Ebiten.
- Never run the GUI against the owner's real `%APPDATA%\hermec`, never kill `hermec.exe`. UI tests stay headless like the existing ones (`newTestGame`, `withLayout`, `testGame`; settings and servers paths in `t.TempDir()`; `g.settingsPath = ""` is a no-op for `persistSettings`).
- No new server keys, key pinning, offline members, avatars or voice roster rows (spec out of scope).
- gofmt clean; commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` (the session's attribution line, whatever model implements the task).
- A dialog or menu being open never stops `pumpSessions`, `tickReconnect`, voice or any other per-tick work; it only takes mouse and keyboard input away from the scene.

## Review Focus

1. **Hostile server name** (control characters / ANSI escapes / only spaces / 10 000 runes in `auth_ok.server_name`): stored and drawn only after `CleanName(…, 64)`; a name that cleans to "" falls back to `Label`/host, never a blank tile. (Task 3 `TestCleanName`, Task 5 `TestSaveServerCleansHostileName`.)
2. **Oversized roster by name length, not count:** auth names are uncapped up to the 64 KiB frame, so 200 members can exceed `MaxMessageSize`, and the client's `SetReadLimit` would then kill every connection on the server. The encoded roster must always fit. (Task 1 `TestRosterStaysUnderMaxMessageSize`.)
3. **Hostile member names in the panel** (control characters, very long, empty): cleaned, clipped to the panel width, never blank. (Task 4 `TestRosterRowsCleansNames`.)
4. **Same identity connected twice** (reconnect race, two devices): each connection is one roster entry, `Total` counts both, and the leaver's departure shrinks it by exactly one. (Task 1 `TestRosterSameIdentityTwice`.)
5. **Tile removed or renamed while it is hovered / while the dialog or tooltip refers to it** (Leave on the viewed server, tooltip index past the end of the shortened list, whitespace-only or 40-rune nickname): no index panic, tooltip cleared, whitespace nick clears the nick, nick capped at 32. (Task 3 `TestSetNickCleans`, Task 5 `TestTooltipSurvivesRemoval`, Task 6 `TestRenameWhitespaceClearsNick`.)

---

### Task 1: Protocol and server (auth_ok name, roster broadcast)

**Files:**
- Modify: `core/proto/proto.go` (constants block at :20-39, `AuthOK` at :74-78, add `Roster` after `Presence` at :146-149)
- Modify: `core/proto/fuzz_test.go` (seed map at :12-24), `core/proto/proto_test.go` (near `TestAuthOKTypedChannels`, :98)
- Modify: `server/conn.go` (`authenticate`, the `AuthOK{…}` literal at ~:208-212), `server/channels.go` (`leaveAll` at :113-129, new `rosterLocked`), `server/voice.go` (`markAuthed` at :57-72)
- Test: `server/channels_test.go` (+ new `server/roster_test.go`); helper fallout in `server/channels_test.go`, `server/voice_test.go`, `server/ratelimit_test.go`, `server/tls_test.go`

**Interfaces:**
- Produces:
  ```go
  // core/proto
  const TypeRoster = "roster"
  const MaxRosterMembers = 200
  type Roster struct { Members []Member `json:"members"`; Total int `json:"total"` }
  // AuthOK gains: ServerName string `json:"server_name,omitempty"`
  // server (unexported)
  func (s *Server) rosterLocked() []byte // encoded roster of s.authed; caller holds chMu
  ```

**Real-code finding:** every authed connection now receives a `roster` right after `auth_ok`, and every other connection receives one per connect/disconnect. Existing server tests read frames in strict order (`readPresence`, `requireErrCode`, `requireNothing`, `readEnv` at `server/channels_test.go:34,56,98`, `server/voice_test.go:23`, plus raw `ReadMessage` loops in `server/ratelimit_test.go:129,200,253,284` and `server/tls_test.go:76`) and will break. Fix them in this task, not by weakening assertions.

- [ ] **Step 1: Write failing tests**
  - `core/proto/proto_test.go` `TestRosterRoundTrip`: `Encode(TypeRoster, Roster{Members: []Member{{Fingerprint:"fp",Name:"a",Roles:[]string{"admin"}}}, Total: 3})` decodes back equal; `TestAuthOKServerName`: `AuthOK{ServerName:"Primos"}` round-trips, and `AuthOK{}` encodes without a `server_name` key (omitempty). Fuzz seed map gains `TypeRoster: Roster{Members: []Member{{Fingerprint:"fp", Name:"a", Roles:[]string{"user"}}}, Total: 1}`.
  - `server/roster_test.go` (use `startServer`, `chatCfg()`, `authed`, `authAs`, `readEnv`, `sendEnv` from the existing tests):
    - `TestAuthOKCarriesServerName`: config `ServerName: "Primos"` → decoded `AuthOK.ServerName == "Primos"`; config with empty `ServerName` → `""` (server does not invent a fallback).
    - `TestRosterOnConnectBroadcastsToBoth`: `a` authed; read a's own roster (1 member, `Total 1`). `b` authed → both `a` and `b` receive a roster with 2 members, `Total 2`, sorted by name then fingerprint, with `Roles` filled from `cfg.Roles.RolesFor`.
    - `TestRosterOnDisconnect`: after `b.Close()`, `a` receives a roster with 1 member, `Total 1`.
    - `TestRosterTruncatesAtMax`: connect `MaxRosterMembers+5` raw clients (each with a distinct identity, a short name); the last roster read on the first client has `len(Members) == 200`, `Total == 205`, and the 200 are the first 200 by name then fingerprint. (Give clients names `n000`…`n204` so order is deterministic.)
    - `TestRosterStaysUnderMaxMessageSize` (Review Focus 2): 4 clients with 25 000-byte names; every roster frame received has `len(raw) <= proto.MaxMessageSize`, `Total == 4`, `len(Members) < 4`, and the members kept are a prefix of the sorted list.
    - `TestRosterSameIdentityTwice` (Review Focus 4): the same `*identity.Identity` authenticates twice → roster `Total == 2` with two entries; closing one → `Total == 1`.
  - Existing-test fallout (no new assertions): add a helper `readEnvSkipRoster(t, c)` in `server/auth_test.go` that loops `readEnv` while `Type == TypeRoster`; use it in `readPresence`, `requireErrCode` and the other order-sensitive readers; make `requireNothing` ignore roster frames; have `authed()` drain its own roster. Inspect each raw `ReadMessage` loop in `ratelimit_test.go` / `tls_test.go` and skip or tolerate roster frames where they count or match frames.
- [ ] **Step 2: Run to verify failure**
  Run: `[cgo]; go test ./core/proto/ ./server/ -run 'Roster|ServerName' -v`
  Expected: FAIL (compile errors: `TypeRoster`, `Roster`, `ServerName` undefined).
- [ ] **Step 3: Implement proto**: `TypeRoster`, `MaxRosterMembers`, `Roster`, `AuthOK.ServerName` exactly as in Interfaces. No change to `Version`.
- [ ] **Step 4: Implement server**
  - `authenticate`: add `ServerName: c.srv.cfg.ServerName` to the `AuthOK` literal.
  - `rosterLocked()`: members from `s.authed` via `c.member()`, sorted by name then fingerprint (same comparator as `presenceLocked`; reuse it via a small shared helper rather than copying), `Total = len(s.authed)`, `Members` cut to the first `MaxRosterMembers`, then, while the encoded frame exceeds `proto.MaxMessageSize` and `Members` is non-empty, drop the last member and re-encode. Returns the encoded bytes.
  - `markAuthed`: after `s.authed[c] = …` and the voice snapshot loop, enqueue `rosterLocked()` to every conn in `s.authed` (including `c`); collect failures and `dropSlow` after unlock, as the existing code does. Keep the current "c is slow" handling.
  - `leaveAll`: after `delete(s.authed, c)`, append `broadcastAuthedLocked(rosterLocked())` failures to `dead`. Add the small `broadcastAuthedLocked(raw []byte) []*conn` helper next to `broadcastVoiceLocked` (`server/voice.go:91`) if that function does not already expose the loop; reuse rather than duplicate.
  - Note: `leaveAll` runs for conns that never authenticated; `delete` on an absent key is fine but do not broadcast a roster for them (guard on `_, was := s.authed[c]`).
- [ ] **Step 5: Run to verify pass**
  Run: `[cgo]; go test -race -count=1 ./core/proto/ ./server/` and `gofmt -l core server` (empty), then `[cgo]; go test ./core/proto -run='^$' -fuzz=FuzzDecode -fuzztime=10s`
  Expected: PASS, no gofmt output, fuzz finds nothing.
- [ ] **Step 6: Commit** `server: auth_ok server_name and server-wide roster broadcast`

### Task 2: Client library (ServerName, Event.Roster)

**Files:**
- Modify: `client/client.go` (`Client` struct :54, `Event` :33, `Dial` struct literal :119, `readLoop` switch :355-395)
- Test: `client/client_test.go`

**Interfaces:**
- Consumes: `proto.AuthOK.ServerName`, `proto.TypeRoster`, `proto.Roster`.
- Produces: `func (c *Client) ServerName() string`; `Event.Roster *proto.Roster`.

- [ ] **Step 1: Write failing tests** (`startServer`, `testCtx` helpers exist in `client_test.go`)
  - `TestServerName`: server config `ServerName: "Primos"` → `c.ServerName() == "Primos"`; empty config → `""`.
  - `TestRosterEvent`: dial as `alice`; read `c.Events()` until an event with `Roster != nil` (5 s timeout); it has `Total == 1` and `Members[0].Name == "alice"`. Dial a second client; the first receives another `Roster` event with `Total == 2`.
  - `TestBadRosterBecomesErrEvent`: with the stub server in `client/voice_test.go` (`newStub`/`serve`) or a tiny raw ws server, send `{"v":1,"type":"roster","data":"nope"}` → an `Event{Err}` mentioning roster, and the client stays usable (next event still delivered).
- [ ] **Step 2: Run** `[cgo]; go test ./client/ -run 'ServerName|Roster' -v` → FAIL (compile).
- [ ] **Step 3: Implement.** `Client.serverName` set from `ok.ServerName` in `Dial`; `ServerName()` returns it (immutable after Dial, no lock). `readLoop`: `case proto.TypeRoster:` unmarshal into `proto.Roster`, on error `emit(Event{Err: fmt.Errorf("client: bad roster: %w", err)})` and `continue`, else `emit(Event{Roster: &r})`. Update the `Event` doc comment ("Exactly one field is non-nil" stays true).
- [ ] **Step 4: Run** `[cgo]; go test -race -count=1 ./client/` → PASS; `gofmt -l client` empty.
- [ ] **Step 5: Commit** `client: ServerName() and roster events`

### Task 3: Saved-server names (`ui/state/servers.go`) and the members setting

**Files:**
- Modify: `ui/state/servers.go` (`ServerEntry` :16-21, `Touch` :67-92, `Initials` :105-116), `ui/state/settings.go` (:12-26)
- Modify (signature fallout only): `ui/rail.go:96` (`state.Touch` call), `ui/state/servers_test.go` existing `Touch` calls (:50-120)
- Test: `ui/state/servers_test.go`, `ui/state/settings_test.go`

**Interfaces:**
- Produces:
  ```go
  const MaxServerNameRunes = 64
  const MaxNickRunes = 32
  // ServerEntry gains: Name string `toml:"name,omitempty"`; Nick string `toml:"nick,omitempty"`
  func DisplayName(e ServerEntry) string                    // Nick, else Name, else Label, else HostPort(URL)
  func CleanName(s string, max int) string                  // drops control runes, trims spaces, caps runes
  func SetNick(entries []ServerEntry, url, nick string) []ServerEntry   // new slice; nick cleaned to MaxNickRunes; unknown url = unchanged copy
  func Remove(entries []ServerEntry, url string) []ServerEntry          // new slice without url
  func Touch(entries []ServerEntry, url, label, name, channel string, live func(url string) bool) []ServerEntry // name: "" keeps existing
  // Settings gains: Members *bool `toml:"members,omitempty"`; func (s Settings) MembersShown() bool
  ```
  `Initials` now takes its source string from `DisplayName`.

**Real-code finding:** the spec says `onConnected` records the name "through `Touch`", but `Touch` has no name parameter and `onConnected` itself only calls `saveServer` (`ui/rail.go:81-98, 107-134`). So `Touch` gains a `name` parameter (after `label`) and `saveServer` supplies it (Task 5).

- [ ] **Step 1: Write failing tests**
  - `TestDisplayNamePrecedence`: table — `{Nick:"N",Name:"S",Label:"L"}`→`N`; `{Name:"S",Label:"L"}`→`S`; `{Label:"L"}`→`L`; `{URL:"ws://h:9/"}`→`h:9`.
  - `TestInitialsUsesDisplayName`: `{Name:"Primos",Label:"lo"}`→`Pr`; `{Nick:"Lab",Name:"Primos"}`→`La`; existing cases (`Label "local"`→`lo`, `"é"`→`é`, bare host→`ch`) unchanged.
  - `TestCleanName`: control runes removed (`"a\x1bb\tc\n"` → `"abc"`), leading/trailing spaces trimmed (`"  x  "` → `"x"`), cap by runes not bytes (70×`"é"` with max 64 → 64 runes), all-control/space input → `""`, max 32 cap works.
  - `TestSetNickCleans` (Review Focus 5): `SetNick(e,"u","  "+strings.Repeat("a",40)+"  ")` → Nick is 32 `a`; `SetNick(e,"u","   ")` → `Nick == ""`; input slice not mutated; unknown url returns an equal copy.
  - `TestRemove`: removes only the matching url, preserves order, input untouched, absent url no-op.
  - `TestTouchKeepsName`: `Touch(nil,"ws://a/","a","Primos","c",nil)` then `Touch(e,"ws://a/","","", "",nil)` → `Name == "Primos"`; a later non-empty name replaces it; `Nick` is never touched by `Touch`.
  - `TestServersFileOldFormatLoads`: a TOML file with only `url/label/last_channel/last_seen` loads with `Name == ""`, `Nick == ""`; a saved entry with `Name`/`Nick` round-trips through `SaveServers`/`LoadServers`, and empty ones are omitted from the file text (omitempty).
  - `settings_test.go`: `TestMembersShownDefaultsOn` (zero `Settings` → true; `Members` = ptr(false) → false); `TestMembersRoundTrip` (false survives `SaveSettings`/`LoadSettings`; nil writes no `members` key; an old settings file without the key loads shown).
  - Update existing `Touch` calls in `servers_test.go` for the new parameter (`""` name).
- [ ] **Step 2: Run** `[cgo]; go test ./ui/state/ -run 'DisplayName|Initials|CleanName|SetNick|Remove|TouchKeeps|ServersFile|Members' -v` → FAIL (compile).
- [ ] **Step 3: Implement** the items in Interfaces. `CleanName` drops every rune that is `unicode.IsControl` (Cc) or in `unicode.Cf` (format runes, which include the bidi overrides U+202A-202E and U+2066-2069), `unicode.Zl` or `unicode.Zp`; then `strings.TrimSpace`; then caps runes. Add `"a‮b"` → `"ab"` to `TestCleanName`. Update `ui/rail.go:96` to pass `""` for name for now (Task 5 passes the real one).
- [ ] **Step 4: Run** `[cgo]; go test -race -count=1 ./ui/state/ ./ui/` → PASS; `gofmt -l ui` empty.
- [ ] **Step 5: Commit** `ui/state: server names, nicknames, leave, members setting`

### Task 4: Roster state, groups, rows

**Files:**
- Modify: `ui/state/state.go` (`State` :35-60, `SetConnected` :63-82, `Apply` :85-122)
- Create: `ui/state/roster.go`
- Test: `ui/state/state_test.go`, `ui/state/roster_test.go`

**Interfaces:**
- Consumes: `client.Event.Roster`, `CleanName`, `MaxServerNameRunes` (Task 3).
- Produces:
  ```go
  // State gains: Roster []proto.Member; RosterTotal int; HasRoster bool
  type RosterGroup struct { Title string; Members []proto.Member } // Title: "Admins" | "Members" | "Bots"
  func RosterGroups(members []proto.Member) []RosterGroup           // admin > bot > member; order Admins, Members, Bots; empty skipped; input order kept inside a group
  type RosterRowKind int  // RosterHeading, RosterMember, RosterMore
  type RosterRow struct { Kind RosterRowKind; Text string; FP string } // Text: "ADMINS — 2" / cleaned name / "and 3 more"
  func RosterRows(members []proto.Member, total int) []RosterRow
  ```

- [ ] **Step 1: Write failing tests**
  - `TestRosterGroups`: members with roles `[admin]`, `[admin,bot]`, `[bot]`, `[user]`, `[]`, `[custom]` → Admins has the first two, Members has `[user]`, `[]`, `[custom]`, Bots has `[bot]`; group order Admins, Members, Bots; a list with no bots has no Bots group; nil input → nil; a member with both `admin` and `bot` appears once (Admins).
  - `TestRosterRows`: two admins + one member, `total` 3 → rows `[Heading "ADMINS — 2", Member, Member, Heading "MEMBERS — 1", Member]`; with `total 7` and 3 members a final `{RosterMore, "and 4 more"}`; `total <= len(members)` adds no More row; `FP` set on member rows.
  - `TestRosterRowsCleansNames` (Review Focus 3): member name `"a\x1b[0mb"` → `Text "a[0mb"` (controls dropped); 200-rune name → capped at 64; name `"\x00"` → `"(unnamed)"`.
  - `TestApplyRoster`: `Apply(client.Event{Roster:&proto.Roster{Members:ms,Total:5}}, true)` → `HasRoster`, `Roster` is a copy (mutating the event's slice later does not alter state), `RosterTotal == 5`.
  - `TestSetConnectedClearsRoster`: after an Apply, `SetConnected(...)` → `Roster == nil`, `RosterTotal == 0`, `HasRoster == false`. `New()` starts with `HasRoster == false`.
- [ ] **Step 2: Run** `[cgo]; go test ./ui/state/ -run 'Roster' -v` → FAIL.
- [ ] **Step 3: Implement.** Headings read `strings.ToUpper(title) + " — " + count` (em dash, U+2014, as in the spec). `Apply` branch `case ev.Roster != nil`. `SetConnected` resets the three fields. `RosterRows` takes names through `CleanName(name, MaxServerNameRunes)` with the `(unnamed)` fallback.
- [ ] **Step 4: Run** `[cgo]; go test -race -count=1 ./ui/state/` → PASS; `gofmt -l ui` empty.
- [ ] **Step 5: Commit** `ui/state: roster state, grouping and rows`

### Task 5: Names on screen (stored name, tooltip, channel header)

**Files:**
- Modify: `ui/rail.go` (`saveServer` :81-98, `drawRail` :243-283, `updateRail`, add tooltip draw/step), `ui/titlebar.go` (`serverTitle` :101-111), `ui/main_scene.go` (`geometry` :47-61, `geom` :63-108, `drawMain` :317-343, `drawChannels` top), `ui/app.go` (`game` struct, `Update` after `updateCursorShape` :335, `drawScene` :393-407)
- Create: `ui/state/tooltip.go`
- Test: `ui/state/tooltip_test.go`, `ui/rail_test.go`, `ui/geom_test.go`, `ui/hits_test.go` (only if a row offset shifts; see below)

**Interfaces:**
- Consumes: `state.DisplayName`, `state.CleanName`, `state.MaxServerNameRunes`, `Touch(..., name, ...)`, `client.Client.ServerName()`.
- Produces:
  ```go
  // ui/state
  const TooltipDelay = 400 * time.Millisecond
  type Hover struct{ /* id, since */ }
  func (h *Hover) Step(id int, now time.Time) bool           // id < 0 resets; true once the same id has been held >= TooltipDelay
  func TooltipOrigin(tileX1, tileY0, tileY1, w, h, winW, winH float64) (x, y float64) // right of the tile (6 px gap), vertically centred on it, clamped inside the window
  func Ellipsize(s string, cols int) string                   // <= cols runes unchanged; else first cols-1 runes + "…"; cols < 1 -> ""
// Before relying on "…" (U+2026), check the embedded VT323 face has the glyph (e.g. text/v2 GoTextFace glyph lookup in a ui test); if it does not, use "~" as the one-rune marker and say so in the task report.
  // ui
  func (g *game) stepTooltip(h state.Hit, ok bool, now time.Time)   // sets g.tip{idx int; show bool}; show is false while g.menu.open or the dialog (Task 6) is open
  func (g *game) drawTooltip(screen *ebiten.Image)
  // geometry gains: headY float64 // y of the one-line header row above the channel list (and the chat toggle row, Task 7)
  ```
  `serverTitle` now returns `state.DisplayName(e)`, so the call bar and window title follow nicknames and server names with no other change.

**Real-code finding:** the spec says the channel list "moves down by one row". The least invasive way is to keep `paneY0` as "first text row" and shift it down: `headY = top + pad`, `paneY0 = headY + lh`. Channel rows, hits and `gm.visible` all derive from `paneY0`, so `ui/hits_test.go:28,60,64` (relative to `gm.paneY0`) keep passing, and the chat scrollback also starts one row lower, which is exactly the toggle row Task 7 needs. `gm.visible` shrinks by one line; no existing test pins the old value (verify by running the suite).

- [ ] **Step 1: Write failing tests**
  - `tooltip_test.go`: `TestHoverStep`: id 3 first seen at t0 → false; t0+399ms → false; t0+400ms → true and stays true while held; id changes to 4 at t1 → false and timing restarts from t1; `Step(-1,…)` resets (a following `Step(4,…)` is false again). `TestTooltipOrigin`: tile `(x1=56,y0=14,y1=70)`, tooltip 100×20, window 960×600 → `(62, 32)`; tooltip taller than space below → `y` clamped so `y+h <= winH`; near top → `y >= 0`; tooltip wider than room to the right → `x` clamped so `x+w <= winW`; window smaller than tooltip → `(0,0)`, never negative. `TestEllipsize`: `("abc",5)`→`abc`; `("abcdef",4)`→`abc…`; `("é",1)`→`é`; `("abc",0)`→`""`.
  - `ui/rail_test.go`:
    - `TestSaveServerStoresCleanedServerName`: a live session whose client reports ServerName `"Primos"` (use a real in-process server via `startServer` from `session_test.go` with `Config.ServerName`; see how `TestTwoSessionsStayConnected` builds sessions) → after `onConnected`, `g.servers[0].Name == "Primos"` and the file reloaded from `serversPath` agrees; a server with empty `ServerName` after a previous `"Primos"` keeps `"Primos"`.
    - `TestSaveServerCleansHostileName` (Review Focus 1): extract the cleaning into `func serverNameOf(c *client.Client) string { return state.CleanName(c.ServerName(), state.MaxServerNameRunes) }` and test it through a stub server that sends `auth_ok` with `server_name` `"\x1b[2J"+strings.Repeat("x",100)` → result is 64 runes, no control runes; a stub name of `"  \x1b "` → `""` and the entry's `DisplayName` is its host.
    - `TestStepTooltip`: entry 0 hit at t0 → `show == false`; at t0+400ms → `show == true, idx == 0`; with `g.menu.open = true` → false; hit not a `HitRailTile` → false.
    - `TestTooltipSurvivesRemoval` (Review Focus 5): `g.tip = {idx: 1, show: true}`, then `g.servers = state.Remove(g.servers, …)` leaving one entry, assert `g.tooltipText()` (a pure helper returning `(string, bool)`) is `"", false` for an out-of-range `idx` instead of indexing. Provide `func (g *game) tooltipText() (string, bool)` for this.
  - `ui/geom_test.go`: `TestGeomHeaderRow`: `gm.paneY0 == gm.headY + gm.lh`; `gm.headY == gm.chanR.y0 + pad`; the first channel hit (`addMainHits`) starts at `gm.paneY0`; header height fits inside `chanR`.
- [ ] **Step 2: Run** `[cgo]; go test ./ui/state/ ./ui/ -run 'Hover|Tooltip|Ellipsize|SaveServer|StepTooltip|GeomHeader' -v` → FAIL.
- [ ] **Step 3: Implement**
  - `saveServer`: `name := serverNameOf(s.c)`; `state.Touch(g.servers, s.key, label, name, channel, live)`. Empty name keeps the old one (Task 3).
  - `serverTitle`: return `state.DisplayName(e)`.
  - Header: `drawMain` draws, inside `gm.chanR` clipped to it, `state.Ellipsize(title, gm.lcols)` in `th.Bright` at `(chanR.x0+pad, gm.headY)`, where `title` is `serverTitle(g.servers, g.curKey)` falling back to `state.HostPort(s.url)`. Draw it before `drawChannels`.
  - `geom()`: add `headY`, set `paneY0 = headY + lh`.
  - Tooltip: `Update` calls `g.stepTooltip(hit, ok, time.Now())` right after `updateCursorShape` (same hit the cursor used; use `g.hitAt()`); `drawTooltip` (called from `drawScene` after `drawRail`, before `drawMenu`) draws a `drawPanelFade` panel of `Advance(text)+2*pad/2` × `lh+pad/2` at `state.TooltipOrigin(railWidth, tileY0, tileY0+railTileH, w, h, g.w, g.h)` using `g.hover state.Hover` and `g.tip`. Reset `g.hover` and `g.tip` whenever `g.servers` shrinks (call from the Leave path in Task 6) and in `closeMenu`-adjacent paths is not needed.
- [ ] **Step 4: Run** `[cgo]; go test -race -count=1 ./ui/... ` and `go vet ./...` → PASS; `gofmt -l ui` empty.
- [ ] **Step 5: Commit** `ui: show server names on tile tooltip, header, call bar`

### Task 6: Menu rows and the Rename / Leave dialog

**Files:**
- Modify: `ui/menu.go` (`openTileMenu` :27-45, `menuItem`), `ui/hits.go` (`buildHits` :52-72), `ui/state/hits.go` (append kinds), `ui/app.go` (`game` struct, `Update` :319-361, `drawScene`), `ui/main_scene.go` (nothing unless Esc ordering needs it), `ui/connect.go` (only if `field` needs a `setString`; prefer a local helper in `dialog.go`)
- Create: `ui/dialog.go`
- Test: `ui/menu_test.go`, `ui/dialog_test.go`, `ui/state/hits_test.go` (kind order only if tested)

**Interfaces:**
- Consumes: `state.DisplayName/SetNick/Remove/CleanName/MaxNickRunes`, `g.disconnect(key)`, `state.SaveServers`, `g.notice` / `s.st.Status` error path used by `saveServer`.
- Produces:
  ```go
  // ui/state/hits.go, appended after HitMenuItem:
  HitMembersToggle // Task 7
  HitDialogBtn     // dialog button; ID 0 = primary (Save / Leave), 1 = Cancel
  // menuItem gains: sep bool (a thin separator is drawn above this row); warn bool (draw in th.Bright)
  type dialogMode int // dlgNone, dlgRename, dlgLeave
  type dialog struct { mode dialogMode; key string; nick field }
  func (g *game) openRenameDialog(key string)
  func (g *game) openLeaveDialog(key string)
  func (g *game) dialogOpen() bool
  func (g *game) dialogRect() rectF                 // centred panel, clamped inside the window
  func (g *game) dialogButtonRect(i int) rectF      // 0 primary, 1 Cancel
  func (g *game) dialogConfirm()
  func (g *game) dialogCancel()
  func (g *game) dialogPress(x, y float64)          // button -> confirm/cancel; inside panel elsewhere -> ignored; outside -> cancel; always spends the press (g.hits.Reset())
  func (g *game) updateDialog() bool                // reads Esc / Enter / typed runes / Backspace / left press; returns true while a dialog is open
  func (g *game) drawDialog(screen *ebiten.Image)
  ```

**Real-code finding:** `openTileMenu` currently returns early for a tile with no session (`ui/menu.go:30-33`) and `TestTileMenuOnlyWithSession` pins that (`ui/menu_test.go:24-36`); both change. The spec's separator and warning colour need two new `menuItem` fields; row height and `menuSize/menuItemRect` must account for the separator (a fixed `sepH = 5` px above the Leave row, still rendered from the one `menuItemRect`).

- [ ] **Step 1: Write failing tests**
  - Replace `TestTileMenuOnlyWithSession` with `TestTileMenuItems`: tile without session → labels `["Rename","Leave Server"]`; with session → `["Rename","Disconnect","Leave Server"]`; `items[len-1].warn && .sep`; no other item has `sep`/`warn`. `TestMenuItemRectsNoOverlap`: consecutive `menuItemRect(i)` never overlap and all lie inside `[menu.x, menu.y, menu.x+w, menu.y+h]` from `menuSize` (the separator is accounted for).
  - Keep `TestMenuDisconnectItem` (find Disconnect by label, not index 0), `TestMenuHitsWinOverRail`, `TestMenuDismissClickIsSwallowed`.
  - `ui/dialog_test.go` (use `menuGame(t, withSession)` and `withLayout`; `g.serversPath` in a temp dir):
    - `TestMenuRenameOpensDialogPrefilled`: entry `{URL:"k", Nick:"Old", Name:"Primos"}`; picking "Rename" closes the menu and opens `dlgRename` with `nick.String() == "Old"`; title text (a pure `func (d dialog) title(e state.ServerEntry) string`) is `"Rename Old"`.
    - `TestRenameSaveSetsNickAndPersists`: type `"Lab"` after clearing, `dialogConfirm()` → `g.servers[0].Nick == "Lab"`, the dialog is closed, and `state.LoadServers(g.serversPath)` shows it.
    - `TestRenameWhitespaceClearsNick` (Review Focus 5): field set to `"   "` → Save clears `Nick`; 40 typed runes → `Nick` has 32; typing is capped at `MaxNickRunes` in the field itself.
    - `TestRenameEmptyResets`: field emptied → `Nick == ""`, `DisplayName` is the server `Name` again.
    - `TestRenameCancelKeepsNick`: `dialogCancel()` leaves the entry unchanged and writes nothing (file mtime/absence).
    - `TestLeaveRemovesWithSession`: sessionful `k` viewed, other session `m` present; confirm → `g.sess` has no `k`, `g.servers` has no `k`, `curKey == "m"`, file reloaded has no `k`; `g.hover`/`g.tip` reset.
    - `TestLeaveWithoutSession`: only the entry is removed; no panic on `disconnect` of an absent key.
    - `TestLeaveEndsCall`: build the call setup used by `TestDisconnectCallSessionEndsCall` (`callOn(g, a, "voice")` in `ui/session_test.go`), confirm Leave on `a` → closers closed (`waitClosed`), `callKey == ""`, entry gone.
    - `TestLeaveViewedFallsBackToConnectScene`: leaving the only/viewed session sets `curKey == ""`.
    - `TestLeaveLocalServer`: key `localKey` removable like any other.
    - `TestRenameSaveFailureStatus`: `g.serversPath` pointing at a directory that cannot be written (e.g. a path under a regular file) → in-memory `Nick` set, `g.notice` non-empty (error on the status line as `saveServer` does).
    - `TestDialogPressRules`: press on button 0 confirms, on button 1 cancels, inside the panel but not on a button does nothing and keeps it open, outside the panel cancels; in all cases `g.hits.At(x, y)` is empty afterwards (press spent) and a voice-row hit under the click is not acted on (`g.vc.busy == false`), mirroring `TestMenuDismissClickIsSwallowed`.
    - `TestDialogEscEnterKeys`: expose the key logic as `func (g *game) dialogKey(esc, enter bool, typed []rune, backspace bool)` called by `updateDialog`; Esc cancels and sets `g.escSpent`; Enter confirms; typed runes below `' '` and `0x7f` are ignored.
    - `TestDialogRectInsideSmallWindow`: with `g.w, g.h = 200, 150`, `dialogRect()` lies fully inside the window.
- [ ] **Step 2: Run** `[cgo]; go test ./ui/ -run 'TileMenu|MenuItemRects|Rename|Leave|Dialog' -v` → FAIL.
- [ ] **Step 3: Implement**
  - `openTileMenu(key, ax, ay)`: no longer requires a session. Items: Rename (`openRenameDialog(key)`), Disconnect only `if _, ok := g.sess[key]`, then Leave Server (`sep`, `warn`, `openLeaveDialog(key)`). The existing "closeMenu then act" wrapper stays so picking an item closes the menu first. `drawMenu` draws `warn` text in `th.Bright` (non-hover rows are `th.FG`) and a 1 px `th.Dim` rule in the separator gap. Update `updateRail`'s right-press path: it already calls `openTileMenu` for any `HitRailTile`.
  - `openRenameDialog` is a no-op for an unknown key; prefill via the entry's `Nick`. `dialogConfirm` Rename: `g.servers = state.SetNick(g.servers, key, nick)` then `state.SaveServers`, error → `g.notice` + viewed session status exactly as `saveServer` does; Leave: `g.disconnect(key)` if `g.sess[key] != nil`, then `g.servers = state.Remove(...)`, save, reset `g.hover`, `g.tip`; then close the dialog.
  - Panel look: `drawPanelFade` with `th.Panel`, title in `th.Bright`, field drawn like the connect form's field (reuse its drawing helper if one exists, else rect + text + blinking cursor as `drawInput` does), hint `"Only you see this. Leave empty to use the server's name."` in `th.Dim`, Leave text `"Leave <display name>? You can add it back later."` (display name via `state.Ellipsize`).
  - Wiring in `Update`: dialog hits (`HitDialogBtn` 0/1) are added in `buildHits` after the menu (deferred so they win; `defer g.addDialogHits()` before `defer g.addMenuHits()` runs in LIFO order, put it so the dialog is last). Read `Update` first and separate per-tick work (`pumpSessions`, `tickReconnect`, voice/mute result draining, update-check results, dev reload, anything not driven by input) from input handlers (rail, menu, banner, call-bar buttons, scene `updateMain`/connect/settings). The per-tick work always runs. `g.updateDialog()` runs before the input handlers, and while it returns true only the input handlers are skipped. Add `TestDialogKeepsSessionsPumping`: with a dialog open, a dial result queued on `g.dialCh` is still applied by one `Update` tick (or by the extracted per-tick helper, if `Update` needs a window). `updateDialog` takes Esc through `g.escPressed()` and marks `g.escSpent`; Enter uses `enterPressed()`. `drawScene` calls `g.drawDialog` last.
  - `stepTooltip` (Task 5) already suppresses while the dialog is open: its guard uses `g.dlg.mode != dlgNone` (the stub from Task 5).
- [ ] **Step 4: Run** `[cgo]; go test -race -count=1 ./ui/...` and `go vet ./...` → PASS; `gofmt -l ui` empty.
- [ ] **Step 5: Commit** `ui: rename and leave server from the tile menu`

### Task 7: Members panel, toggle, geometry and auto-hide

**Files:**
- Modify: `ui/main_scene.go` (`geometry`, `geom` :63-108, `addMainHits` :112-130, `updateMain` mouse/wheel :216-279, `drawMain` :332-342, `mainScene` :29-40), `ui/app.go` (`drawScene` unchanged; `game` has `set`), `ui/settings.go` (`toggleMembers`), `ui/cursor.go`/`ui/hits.go` (nothing: `HitMembersToggle` is a pointer via the default branch of `shapeAt`)
- Create: `ui/members.go`
- Test: `ui/geom_test.go`, `ui/hits_test.go`, `ui/members_test.go`

**Interfaces:**
- Consumes: `State.Roster/RosterTotal/HasRoster`, `state.RosterRows`, `Settings.MembersShown()`, `state.HitMembersToggle` (declared in Task 6), `state.ClampScroll`, `state.WheelLines`.
- Produces:
  ```go
  const (
      membersPaneW = 180 // px at 100 % DPI, like channelPaneW
      minChatCols  = 40
  )
  // geometry gains:
  //   membersR rectF   // zero rect when the list is not drawn
  //   toggleR  rectF   // "members" button, right end of the chat pane's header row
  //   showMembers bool // true when membersR is drawn (setting on AND fits)
  // mainScene gains: rosterScroll int
  func (g *game) toggleMembers()                    // flips g.set.Members, persistSettings()
  func (g *game) drawMembers(screen *ebiten.Image, gm geometry)
  func (g *game) drawMembersToggle(screen *ebiten.Image, gm geometry)
  ```

**Real-code finding / choice:** the spec puts the toggle "at the top right of the chat area" and the panel "at the right edge", but `geom()` has no header row there. Task 5 already reserves one row (`headY` .. `paneY0`) above the chat text, so `toggleR` is the right-aligned label `"members"` in that row at the window's right content edge, a fixed spot whether or not the panel is drawn (Victor's B.1 rule: things must not move under the mouse). `membersR` spans from `chatR.y0` down to `inputR.y1` (full column height) because `inputR` shrinks too.

- [ ] **Step 1: Write failing tests**
  - `TestMembersGeomShown` (`testGame`, 960×600, `g.set.Members == nil`, a main-phase session via `mainGame` helper in `hits_test.go`): `gm.showMembers`; `membersR.x1 - membersR.x0 == 180`; `chatR.x1 == membersR.x0 - pad`; `inputR.x1 == chatR.x1`; `membersR.y0 == chatR.y0` and `membersR.y1 == inputR.y1`; `gm.rightW` and `gm.cols` are smaller than with the list hidden by exactly `membersPaneW + pad` px / the matching column count.
  - `TestMembersGeomSettingOff`: `Members = ptr(false)` → `membersR` empty, `showMembers == false`, `chatR.x1` equals the pre-members right edge; `toggleR` still non-empty.
  - `TestMembersAutoHide`: lower `g.w` from 960 until `showMembers` flips to false; assert at the last width where it is true `(chatRight-shrunk text width)/adv >= 40` (i.e. `gm.cols >= minChatCols`) and at the first width where it is false the cols it *would* have had are `< 40`; at that point `membersR` is empty, `g.set.Members` is still nil (setting untouched), and `gm.cols >= minChatCols` is not required (it is the full-width layout). Also assert hiding never leaves a width where `showMembers` is true and `gm.cols < 40`.
  - `TestMembersToggleHit`: `buildHits` registers a `HitMembersToggle` over exactly `gm.toggleR` (centre hit; a point just outside is not it); `shapeAt` over it is `CursorShapePointer`; with the list auto-hidden the toggle is still hit-testable.
  - `TestToggleMembersPersists`: `g.settingsPath = filepath.Join(t.TempDir(),"settings.toml")`; `toggleMembers()` → `g.set.MembersShown() == false` and `state.LoadSettings(path)` agrees; toggling again → shown (`Members` pointer to true is fine); with `g.settingsPath == ""` nothing is written and nothing panics.
  - `TestMembersPanelRows` (pure part in `members.go`): `func (g *game) memberRows() (rows []state.RosterRow, note string)`: `HasRoster == false` → no rows and `note == "member list needs a newer server"`; `HasRoster` with 2 members and `RosterTotal 5` → rows ending with `RosterMore "and 3 more"`.
  - `TestMembersScrollClamped`: `rosterVisible(gm)` = rows that fit in `membersR` minus padding; with more rows than that, wheel input of any size leaves `ms.rosterScroll` within `state.ClampScroll(len(rows), visible, …)`; a short list keeps it 0; switching sessions keeps each session's own scroll.
  - `TestOwnRowBright`: `func (g *game) memberRowColor(r state.RosterRow) color.RGBA` returns `th.Bright` when `r.FP == g.fp` for member rows, `th.Dim` for headings and More, `th.FG` otherwise.
  - Update `TestCursorShapeMainScene` expectations only if the chat/members columns move any case point (run first; the "empty chat area" case is `chatR.x0+pad, chatR.y0+lh`, still empty).
- [ ] **Step 2: Run** `[cgo]; go test ./ui/ -run 'Members|ToggleMembers|OwnRow' -v` → FAIL.
- [ ] **Step 3: Implement**
  - `geom()`: compute `want := g.set.MembersShown()`. Tentative chat right edge `cr = right - membersPaneW - pad`; `cols = int((cr - pad - rightX)/adv)`; `showMembers = want && cols >= minChatCols`. If shown: `chatR.x1 = inputR.x1 = cr` and `membersR = {cr+pad, chatR.y0, right, inputR.y1}`; else both extend to `right`. `rightW` and `cols` derive from the final edge. `toggleR` = right-aligned `Advance("members")+pad` × `lh` rect at `headY`, its right edge at `right` (the far right of the window's content area, above the members panel), so the button never moves when the list shows, hides or auto-hides. The members panel and chat start below the header row, so the toggle overlaps neither.
  - `addMainHits`: add the `HitMembersToggle` rect from `gm.toggleR` before the call-bar and signal hits (the quality panel still wins last).
  - `updateMain`: mouse switch gains `case state.HitMembersToggle: g.toggleMembers()`; the wheel block routes to `ms.rosterScroll` (step `wheelLinesPerNotch`) when the cursor is inside `gm.membersR` and `gm.showMembers`, else the existing chat path.
  - `drawMain`: after `drawPanelR(chatR)` etc., `if gm.showMembers { drawPanelR(membersR); drawMembers }`; always `drawMembersToggle` (text `members`, `th.Bright` when `gm.showMembers`, `th.Dim` otherwise, including the auto-hidden case). `drawMembers` clips to `membersR`, rows from `memberRows()`, heading/More in `th.Dim`, names clipped with `state.Ellipsize` to the panel's text columns; the old-server note is one dim line.
- [ ] **Step 4: Run** `[cgo]; go test -race -count=1 ./...` and `go vet ./...`; `gofmt -l .` empty.
- [ ] **Step 5: Commit** `ui: online member list with toggle and narrow-window auto-hide`

### Task 8: Docs touch

**Files:**
- Modify: `docs/protocol.md` (message table near :222 and `auth_ok` at section 4.3 :115, plus a short "4.x roster (S)" section), `README.md` (Keys list at :131-132), `HANDOFF.md` (header line 4, pipeline item 0 :37, item 3 :47)

**Interfaces:** none.

- [ ] **Step 1: Edit `docs/protocol.md`.** `auth_ok` gains optional `server_name` (string, omitted when the host set none; clients clean and cap it, absent = old server). New message `roster` (S): `{"members": Member[], "total": int}`, sent to every authenticated connection right after `auth_ok` (after any `voice_state` snapshots) and whenever a connection authenticates or drops; complete list, not a delta; sorted by name then fingerprint; `members` truncated to 200 (and further if needed to fit 64 KiB) while `total` counts everyone. State that v1 clients ignore both additions.
- [ ] **Step 2: Edit `README.md` Keys list.** Replace the right-click line with: Rename (nickname only you see), Disconnect, Leave Server (asks first; removes the tile). Add a line for the **members** button at the top right of the chat and that the list hides itself in narrow windows.
- [ ] **Step 3: Edit `HANDOFF.md`.** Header: B.2 implemented (spec and plan paths), Victor's B.2 real-mouse checklist (spec section 5, last bullet) queued next to the B.1 one, **B.3 (icon editor) and the v0.2.0 release steps (spec section 6) are next**. Pipeline item 3: Rename/Remove now exist in the right-click menu. Keep each edit to a couple of lines.
- [ ] **Step 4: Verify** `[cgo]; go vet ./...; go test -race -count=1 ./...` → PASS; `gofmt -l .` empty; `git status` shows only the files listed in Tasks 1-8.
- [ ] **Step 5: Commit** `docs: roster protocol, menu and members keys, B.2 handoff`

---

## Self-Review

**Spec coverage:** 3.1 → Task 1 (proto) + Task 8 (docs); 3.2 → Task 1; 3.3 → Task 2; 3.4 → Task 3 (+ Task 5 `saveServer` stores the name); 3.5 → Task 6; 3.6 → Task 5; 3.7 state/groups → Task 4, panel/toggle/geometry/auto-hide → Task 7, `Settings.Members` → Task 3. Section 5: proto round trip and fuzz seed (T1); server auth_ok name, connect/leave broadcasts, truncation (T1); client `ServerName()` and roster event (T2); state tests listed in section 5 (T3, T4: `DisplayName`, `CleanName`, `SetNick`, `Remove`, `Touch` keeps `Name`, `RosterGroups`, `Apply`/`SetConnected`, old `servers.toml` loading); ui tests: menu items per tile state and dialog Enter/Esc/outside click without the press reaching the scene (T6), members geometry, auto-hide, toggle hit (T7). The real-mouse checklist is Victor's and is queued in Task 8's HANDOFF edit.

**Deliberate additions beyond the spec text (each needed to make a spec rule hold):** the size-based roster trim (spec 3.1 says the message must stay under `MaxMessageSize`, which a count cap alone does not guarantee); the `Ellipsize` helper (spec 3.6 "clipped with …"); the `(unnamed)` fallback row.

**Type consistency:** `Touch(entries, url, label, name, channel, live)` is defined in Task 3 and called with that order in Task 5; `RosterRow`/`RosterRows`/`RosterGroups` defined in Task 4 and consumed in Task 7; `HitMembersToggle` and `HitDialogBtn` are both appended in Task 6 and used in Tasks 6-7; `headY` defined in Task 5 and used by `toggleR` in Task 7; `g.hover`/`g.tip` defined in Task 5 and reset in Task 6; `stepTooltip` (Task 5) guards on `g.dlg.mode != dlgNone`, so Task 5's step 3 declares the `dialog` struct, `dialogMode` and `dlgNone` as a minimal stub that Task 6 completes (`dialogOpen()` in Task 6 is then `g.dlg.mode != dlgNone`).

**Proportion:** about 2.4x the spec's length; no function bodies except where the algorithm is not fixed by signature and tests (roster trim, geometry).
