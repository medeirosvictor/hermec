# Add a Server Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Friends can host a reachable HERMEC server from inside the app and join one, with tiles that show up immediately and say clearly when a connect failed.

**Architecture:**
- Hosting reuses the embeddable `server` package. A `hosted.toml` in the user config dir, written with the existing `server.Config` format, makes the server persist and auto-start. The GUI owns the running server through a `hostedServer` handle on `game`, keyed on the rail as `hermec://hosted` (the same placeholder-key idea as `hermec://local`).
- Pending tiles are ordinary `state.ServerEntry` values with an unsaved `Pending` flag, so every rail path keeps indexing `g.servers`.
- A failed dial no longer deletes a first-time session; it marks it failed. The rail draws a ✕ and a ↻ badge from `TileFacts`.

**Tech Stack:** Go, Ebiten v2.10.4 (vector package), BurntSushi toml, pion (unchanged). New dependency: `github.com/atotto/clipboard v0.1.4` (Windows uses syscalls, no cgo).

**Spec:** `docs/superpowers/specs/2026-10-16-hermec-add-server-page-design.md`

## Global Constraints

- Commit trailer: a blank line, then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- CRLF checkout: check gofmt on committed blobs (`git show HEAD:path | gofmt -l` prints nothing). Run `go vet ./...` and `go test -race -count=1 ./...` with `$env:CGO_ENABLED=1; $env:PATH="C:\msys64\ucrt64\bin;$env:PATH"`.
- Never launch the GUI, never use `%APPDATA%\hermec`, never kill hermec.exe. UI tests are headless with paths under `t.TempDir()`.
- `ui/state` must not import Ebiten or `server`.
- The wire protocol is unchanged. `-local` is unchanged (loopback, random port, `hermec://local`).
- The hosted server never sets `AllowLoopbackICE`. Its `Addr` is `":<port>"`, with a default port of 7697. There is one hosted server per app.
- Hosted file: `<os.UserConfigDir()>/hermec/hosted.toml` via a new `DefaultHostedPath()` next to `DefaultServersPath()` in `ui/app.go`.
- Exact copy, used verbatim:
  - page title `Add a Server`; choices `Create my own` and `Join a server`;
  - port error `port <n> is in use (is hermec-server running?)`;
  - firewall line `Windows will ask to allow HERMEC on your network — choose Allow so friends can connect.`;
  - failed pane hint `click ↻ to retry`;
  - stopped-hosting error `not hosting — click ↻ to start`;
  - menu items `Retry`, `Copy invite address`, `Stop hosting`, `Start hosting`;
  - hosted leave suffix `This stops hosting and deletes this server's settings.`;
  - already hosting `already hosting <name>`;
  - no address found `no network address found`.
- Ruling (controller): since B.1, saved servers don't auto-dial at launch. The hosted tile is the single exception: when the hosted server starts at launch, the app also opens a session to it, because it is the user's own server.

## Review Focus

1. **The port is busy at auto-start** (hermec-server already running on 7697). The app must still open, with the hosted tile in the failed state showing the port-in-use message, and no crash. Test in Task 4.
2. **Leave on a pending tile while its dial is in flight.** The late dial result must not bring back the tile or write it to `servers.toml`. Test in Task 2.
3. **Joining a URL that already has a tile (pending or saved)** must view the existing tile, not add a duplicate. Test in Task 2.
4. **Pressing Create while already hosting.** The Create choice must show `already hosting <name>` and do nothing, so `hosted.toml` is never silently overwritten. Test in Task 5.
5. **No Tailscale and no private LAN address.** The invite shows `no network address found`, and copying is a no-op that doesn't error. Test in Task 3.

---

### Task 1: State and theme: pending entries, failed tile facts, warn colour, retry hit

**Files:**
- Modify: `ui/state/servers.go` (ServerEntry, SaveServers, Touch), `ui/state/tile.go`, `ui/state/hits.go`, `ui/theme/theme.go` (+ `default.toml`, every preset file stays valid)
- Test: `ui/state/servers_test.go`, `ui/state/tile_test.go`, `ui/theme/theme_test.go`

**Interfaces:**
- Produces:
  - `ServerEntry.Pending bool` with `toml:"-"`. `SaveServers` writes only entries with `!Pending`.
  - `Touch` sets `Pending = false` on the upserted entry.
  - `TileFacts.Failed bool`, and `TileState.FailX bool`, `TileState.Retry bool`.
  - `const HitRailRetry` (a new HitKind) with `ID` = tile index.
  - `Theme.Warn color.RGBA` (toml `warn`), defaulting to `#E5534B` in `default.toml`. Parchment overrides it to `#B3261E`.
- [ ] **Step 1: Write the failing tests**
  - `TestSaveServersSkipsPending`: save `[{URL:a}, {URL:b, Pending:true}]`, load → only `a`.
  - `TestTouchClearsPending`: Touch on a pending `b` → `Pending == false`.
  - `TestTileFailed`:
    - `Tile(TileFacts{HasSession:true, Failed:true})` gives `FailX && Retry && Dim && !Pulse`.
    - `Tile(TileFacts{HasSession:true})` gives `!FailX && !Retry`.
    - `Tile(TileFacts{HasSession:true, Reconnecting:true, Failed:true})` gives FailX, Retry and Pulse all true.
  - `TestWarnColour`: default theme Warn = `#E5534B`, parchment = `#B3261E`, and every preset loads.
- [ ] **Step 2: Run** `go test ./ui/state/ ./ui/theme/`. Expected: compile FAIL (unknown fields).
- [ ] **Step 3: Implement.** `Failed` implies `FailX`, `Retry` and `Dim`. `Pulse` stays `Reconnecting`.
- [ ] **Step 4: Run** the same command. Expected: PASS. Then the full verification.
- [ ] **Step 5: Commit** `state: pending entries, failed tile facts, warn colour`.

### Task 2: Sessions keep failed first connects; immediate tiles; ✕/↻ badges

**Files:**
- Modify: `ui/session.go` (session struct, openSession, applyDial ~:236-254, retryNow ~:149), `ui/rail.go` (railW, tileFacts, drawRail tile closure, addRailHits, updateRail, tooltipText), `ui/menu.go` (openTileMenu), `ui/main_scene.go` (the viewed pane for a failed session)
- Test: `ui/session_test.go`, `ui/rail_test.go`, `ui/menu_test.go`

**Interfaces:**
- Consumes (Task 1): Pending, TileFacts.Failed, TileState.FailX/Retry, HitRailRetry, Theme.Warn.
- Produces:
  - session fields `failed bool`, `lastErr string`.
  - `(g *game) openSession(url, name string) *session`, which now:
    - if a session for the key exists, returns it (no new dial);
    - if no `g.servers` entry has the key, prepends `ServerEntry{URL:key, Label:HostPort(url), Pending:true}` to `g.servers` without saving.
  - `(g *game) retryNow(key string)`, which also clears `failed`.
  - `tooltipText()` returns the name, plus `"\n" + lastErr` when failed. It relies on round 2's tooltip wrapping/multi-line; if that is single-line only, render the second line below the first.
- [ ] **Step 1: Write the failing tests** (headless, driving `applyDial` and `openSession` with a fake dial result as the existing session tests do):
  - `TestFirstConnectFailureKeepsSession`: openSession("ws://bad/") then `applyDial(dialResult{err: errors.New("refused")})`. The session still exists, `failed`, `lastErr == "refused"`, the entry is in `g.servers` and Pending, and `servers.toml` lacks it.
  - `TestPendingSavedOnFirstSuccess`: a successful applyDial then onConnected → the entry is no longer Pending and the file contains it.
  - `TestLeavePendingDuringDial` (Review Focus 2): openSession, then `dialogConfirm` with leave for that key, then deliver a success dialResult. Expect g.servers without the key, no session, and the file without the key. The late result must be dropped because its session is no longer in `g.sess`.
  - `TestJoinExistingKeyNoDuplicate` (Review Focus 3): openSession twice with the same URL → one entry and one session; and a saved entry gets no Pending duplicate.
  - `TestRetryHitRedials`:
    - a failed session's tile registers `HitRailRetry` at the bottom-left badge rect, which takes precedence over `HitRailTile`; check the hit-test order in `ui/state/hits.go` and register so the badge wins;
    - activating it sets `dialing` and clears `failed`;
    - activating again while dialing is a no-op (one dial).
  - `TestFailedTooltipShowsError`: tooltipText for a failed tile contains the name and "refused".
  - `TestMenuRetryItem`: openTileMenu on a failed key has `Retry` as its first item; a live key has none.
  - `TestRailVisibleWithOnlyPending`: `railW() > 0` when g.servers holds only a pending entry.
- [ ] **Step 2: Run** `go test ./ui/ -run 'FirstConnect|Pending|JoinExisting|RetryHit|FailedTooltip|MenuRetry|RailVisible'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - First-connect failure: keep the session, set `failed` and `lastErr`, and don't schedule a retry.
  - Failed reconnect of an `everUp` session: set `failed` and `lastErr`, and keep scheduling the retry.
  - A successful dial clears both.
  - `tileFacts` sets `Failed = s.failed`.
  - Badges, in the tile closure:
    - ✕ is two strokes in `th.Warn`, about 7 px, at the bottom-right corner (`r-6, b-6` centre);
    - ↻ is a ~300° arc plus an arrowhead stroked in `th.Bright`, at the bottom-left (`l+6, b-6`). Follow `drawBust`'s `vector.Path` + `StrokePath` pattern.
    - A failed tile is never InCall, so the bust never collides with the badges.
  - The viewed pane of a failed session shows `lastErr` plus `click ↻ to retry` where chat would be.
- [ ] **Step 4: Run** the same command, then the full verification. Expected: PASS.
- [ ] **Step 5: Commit** `ui: immediate pending tiles; failed tiles keep their session with ✕ and ↻`.

### Task 3: Hosting core: config, addresses, port-in-use detection

**Files:**
- Create: `ui/hosted.go`, `ui/hosted_test.go`, `discover/self.go`, `discover/self_test.go`, `integration/hosted_test.go`
- Modify: `server/discovery.go` (export the sentinel), `ui/app.go` (`DefaultHostedPath`)

**Interfaces:**
- Produces:
  - `server.ErrDiscoveryBind`: rename the unexported `errDiscoveryBind`, still wrapped with `%w`.
  - `func hostedConfig(name, password string, port int, adminFP string) server.Config`:
    - starts from `server.DefaultConfig()`;
    - `Addr = fmt.Sprintf(":%d", port)`;
    - `ServerName = state.CleanName(name, state.MaxServerNameRunes)`;
    - `Password = password`;
    - `VoiceChannels = []string{"voice"}`;
    - `Roles.Grants = map[string][]string{adminFP: {"admin"}}`.
  - `func saveHosted(path string, cfg server.Config) error` writes `MarshalTOML` atomically (temp file + rename, MkdirAll 0700, file 0600, because it holds the password).
  - `func loadHosted(path string) (cfg server.Config, ok bool, err error)`: a missing file gives `ok=false, err=nil`.
  - `func hostedPort(cfg server.Config) int` parses the port from Addr.
  - `func isPortInUse(err error) bool`: true for `errors.Is(err, server.ErrDiscoveryBind)`, and for a `syscall.Errno` that is `10048` (WSAEADDRINUSE) or equals `syscall.EADDRINUSE`, found via `errors.As`.
  - `func portInUseMsg(port int) string` returns `port <n> is in use (is hermec-server running?)`.
  - `func inviteAddress(tsIP, lanIP string, port int) string`:
    - with tsIP set: `ws://<tsIP>:<port>/`;
    - else with lanIP set: `ws://<lanIP>:<port>/`;
    - else: `no network address found`.
  - `func discover.SelfAddrs(ctx context.Context) (tailscaleIP, lanIP string)`:
    - the tailscale part reuses the `lookPath`/`runStatus` seam and decodes `Self.TailscaleIPs`, taking the first IPv4 in `cgnat`;
    - the LAN part comes from `net.Interfaces`: the first up, non-loopback interface with a private IPv4 (`ip.IsPrivate()`) outside `cgnat`;
    - either can be "".
  - `func DefaultHostedPath() (string, error)`.
- [ ] **Step 1: Write the failing tests**
  - `TestHostedConfigRoundTrip`: hostedConfig("  My\u202eServer ", "pw", 7697, "FP") → save to t.TempDir() → loadHosted.
    - ok and ServerName `MyServer` (cleaned); Addr `:7697`; Password `pw`; Channels `[general]`; VoiceChannels `[voice]`;
    - `Roles.RolesFor("FP")` contains "admin"; `RolesFor("other")` is `[user]`;
    - the file mode is 0600 on non-Windows (skip on Windows).
  - `TestLoadHostedMissing`: ok=false, err=nil.
  - `TestIsPortInUse`:
    - true for `fmt.Errorf("server: listen: %w", &net.OpError{Err: os.NewSyscallError("bind", syscall.Errno(10048))})`, and for `fmt.Errorf("x: %w", server.ErrDiscoveryBind)`;
    - false for `errors.New("boom")`.
  - `TestIsPortInUseReal`: listen on `127.0.0.1:0`, start `server.New` with `Addr` = that address and `Discoverable:false` → `isPortInUse(Start())` is true. This proves the real Windows error path.
  - `TestPortInUseMsg`: 7697 gives `port 7697 is in use (is hermec-server running?)`.
  - `TestInviteAddress`:
    - `("100.64.1.2","192.168.2.10",7697)` → `ws://100.64.1.2:7697/`;
    - `("","192.168.2.10",7697)` → `ws://192.168.2.10:7697/`;
    - `("","",7697)` → `no network address found` (Review Focus 5).
  - `TestSelfTailscaleIP` (discover): use the runStatus seam with JSON whose `Self.TailscaleIPs` is `["fd7a::1","100.101.102.103"]`, and expect `100.101.102.103`. With lookPath failing, expect "".
  - `TestHostedServerServesClients` (integration): build hostedConfig with port 0 → saveHosted → loadHosted → override `Addr = "127.0.0.1:0"` → server.New and Start. Two `dialJoined` clients see each other's presence, and the admin client's fingerprint is granted admin. Check it via the auth_ok/role info the client exposes; if the client exposes no roles, assert `cfg.Roles.RolesFor(fp)` only and note it.
- [ ] **Step 2: Run** `go test ./ui/ ./discover/ ./integration/ -run 'Hosted|PortInUse|Invite|SelfTailscale'`. Expected: FAIL.
- [ ] **Step 3: Implement** per the Interfaces block.
- [ ] **Step 4: Run** the same command, then the full verification. Expected: PASS.
- [ ] **Step 5: Commit** `hosting: hosted.toml config, invite address, port-in-use detection`.

### Task 4: Hosting lifecycle in the GUI

**Files:**
- Modify: `ui/hosted.go`, `ui/app.go` (Run startup and shutdown), `ui/rail.go` (`realURL`/`keyFor`, `saveServer` label), `ui/discovery.go` (`savedForDiscovery`), `ui/session.go` (openSession guard, retryNow), `ui/menu.go`, `ui/dialog.go`, `go.mod`/`go.sum` (atotto/clipboard v0.1.4)
- Test: `ui/hosted_test.go`, `ui/menu_test.go`, `ui/dialog_test.go`

**Interfaces:**
- Consumes (Task 3): hostedConfig, saveHosted, loadHosted, hostedPort, isPortInUse, portInUseMsg, inviteAddress, discover.SelfAddrs, DefaultHostedPath. Consumes (Task 2): openSession, retryNow, failed/lastErr.
- Produces:
  - `const hostedKey = "hermec://hosted"`.
  - `type hostedServer struct{ srv *server.Server; cfg server.Config; path string; url string; err string }`, plus `g.hosted *hostedServer` (nil = no hosted.toml).
  - `func (g *game) startHosted() error`: starts from `g.hosted.cfg` and sets `url = "ws://127.0.0.1:<port>/"`. On failure, `err` = portInUseMsg(port) if isPortInUse, otherwise err.Error().
  - `func (g *game) stopHosted()`: shuts down with a 3 s timeout, sets `srv=nil` and `err = "not hosting — click ↻ to start"`, and marks the hosted session failed with that text.
  - `func (g *game) createHosted(name, password string, port int) error`: saves, starts and opens a session. Task 5 uses it.
  - `func (g *game) leaveHosted()`.
  - `var clipboardWrite = clipboard.WriteAll`, a seam.
- [ ] **Step 1: Write the failing tests**
  - `TestRealURLHosted`: with g.hosted.url set, realURL(hostedKey) == url and keyFor(url) == hostedKey.
  - `TestStartupHostedAutoStart`: write hosted.toml with port 0 (via hostedConfig + override Addr `127.0.0.1:0` in the test only), then run the startup helper you extract from Run (e.g. `g.bootHosted(path)`). Expect srv running, a session for hostedKey dialing, and a hostedKey entry in g.servers labelled with the server name. Without the file: g.hosted == nil and no session.
  - `TestStartupHostedPortBusy` (Review Focus 1): occupy a port, write hosted.toml with it, then bootHosted. No panic; the hostedKey tile is failed with `lastErr == portInUseMsg(port)`.
  - `TestStopStartHosted`: stopHosted → the session is failed with `not hosting — click ↻ to start` and srv is nil. retryNow(hostedKey) → server running again and dialing.
  - `TestHostedMenu`:
    - the hosted live tile menu has `Copy invite address` and `Stop hosting`; the stopped one has `Start hosting`; non-hosted tiles have neither.
    - Copy calls clipboardWrite with inviteAddress(...). With "no network address found" it does not call it (Review Focus 5).
  - `TestLeaveHostedDeletesFile`: the leave dialog for hostedKey shows the suffix `This stops hosting and deletes this server's settings.`. Confirm stops the server, removes hosted.toml and removes the tile.
- [ ] **Step 2: Run** `go test ./ui/ -run 'Hosted|RealURLHosted|StartupHosted|StopStart|LeaveHosted'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - In Run: right after the `-local` block, call `g.bootHosted(path)` with DefaultHostedPath (or `opts.HostedPath` if set, so tests and dev runs can redirect it; add `HostedPath` to `ui.Options` and a `-hosted` flag in cmd/hermec beside `-servers`, if such a flag exists; otherwise only Options).
  - bootHosted runs after loadServers, so the hostedKey entry can be found or added.
  - `defer` stops the hosted server like -local's Shutdown.
  - openSession's localKey guard gets a hosted twin: for hostedKey with srv nil, it creates the session already failed with `g.hosted.err`.
  - retryNow(hostedKey) with srv nil calls startHosted first.
  - saveServer's label for hostedKey is `g.hosted.cfg.ServerName`.
- [ ] **Step 4: Run** the same command, then the full verification. Expected: PASS.
- [ ] **Step 5: Commit** `ui: host a server from the app; auto-start, stop/start, invite, leave`.

### Task 5: The Add a Server page

**Files:**
- Create: `ui/addpage.go`, `ui/addpage_test.go`
- Modify: `ui/connect.go` (Up/Down fill; Esc back to the choices), `ui/rail.go` (`addServer`), `ui/app.go` (Update routing: which page is drawn while `Phase == PhaseConnect`), `ui/state/hits.go` (`HitAddChoice`, `HitCreateField`, `HitCreateBtn`), `ui/help.txt` (append the firewall line to "Reaching your friends"), `README.md` (one paragraph on Create my own)

**Interfaces:**
- Consumes (Task 4): createHosted, g.hosted, inviteAddress, discover.SelfAddrs.
- Produces:
  - `type addMode int` with `addChoose`, `addCreate`, `addJoin`, plus `g.add addMode` and `g.addPrev string` (the view key to return to).
  - `type createForm struct{ name, pass, port field; focus int; err string }`. Port starts at "7697" and accepts digits only. Valid range 1–65535, else err `port must be 1–65535`. An empty name gives `server name is required`.
- [ ] **Step 1: Write the failing tests**
  - `TestAddPageEntry`: with no servers at startup → add == addChoose. addServer() from a viewed server → addChoose, addPrev = that key.
  - `TestAddChoiceHits`: choice 0 → addCreate, choice 1 → addJoin (the existing connect form, focus on server).
  - `TestAddEscNavigation`:
    - Esc in addCreate or addJoin → addChoose;
    - Esc in addChoose with addPrev set → `g.view(addPrev)`;
    - Esc in addChoose with no servers → stays.
    - Uses `escPressed`/`escSpent` as the menu does.
  - `TestCreateValidates`: empty name → `server name is required`; port "70000" → `port must be 1–65535`; letters are ignored when typed into port.
  - `TestCreateSubmits`: with createHosted behind a seam, a valid submit calls it with (name, pass, 7697) and the view switches to hostedKey. When it returns the port-in-use error, the form stays with err = that message.
  - `TestCreateWhileHosting` (Review Focus 4): with g.hosted != nil, choice 0 renders `already hosting <name>` and activating it does not change the mode; hosted.toml is untouched.
  - `TestJoinArrowsFillAddress`: with disc = [a, b], Down → `f.server.runes` == a.URL; Down → b.URL; Up → a.URL.
  - `TestHelpHasFirewallLine`: help.txt contains the firewall copy verbatim.
- [ ] **Step 2: Run** `go test ./ui/ -run 'AddPage|AddChoice|AddEsc|Create|JoinArrows|HelpHasFirewall'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - Choice page: the title `Add a Server`, with two large soft panels side by side (`drawPanelFade`), labelled `Create my own` and `Join a server`, each with a one-line hint (create: `host a server on this computer`, join: `connect to a friend's server`).
  - The create form draws name / password (masked with `•`) / port, the firewall line, a `Create` button, and the invite address beneath it as `invite: <address>`, with SelfAddrs resolved once on entering addCreate, in a goroutine with a 2 s timeout so it doesn't block the frame.
  - Reuse the connect form's `field` editing and focus/Tab handling.
- [ ] **Step 4: Run** the same command, then the full verification. Expected: PASS.
- [ ] **Step 5: Commit** `ui: Add a Server page with Create my own and Join`.
