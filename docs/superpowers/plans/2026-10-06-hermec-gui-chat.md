# Hermec GUI Chat Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A runnable Ebiten desktop client — connect screen, channel list, live chat — in the amber-CRT aesthetic, plus one command that runs server and client together (`go run ./cmd/hermec -local`).

**Architecture:** The GUI is a thin Ebiten shell over the existing `client` package. All UI state and event handling live in a pure, unit-testable `ui/state` package (a reducer over `client.Event` plus input editing); the Ebiten layer only draws state and forwards input. Theming is a TOML file over an embedded amber default. `-local` starts `server.New` in-process on a loopback port and connects to it — the run-everything dev command.

**Tech Stack:** Go, Ebiten v2 (no cgo on Windows), text/v2 with gofont/gomono (golang.org/x/image), BurntSushi/toml (already present).

**Spec:** `docs/specs/2026-10-06-hermec-design.md` (§6.1–6.2 client + aesthetic; §4 identity UX; §5 theming door 3)

## Global Constraints

- New dependencies this plan: `github.com/hajimehoshi/ebiten/v2` and `golang.org/x/image` only.
- gofmt-clean; `go vet ./...` clean; full suite `go test -count=1 ./...` passes (no `-race` on this machine — cgo disabled; Linux CI covers it).
- `ui/state` and `ui/theme` stay free of Ebiten imports — they must be testable headlessly. Only `ui/` root (app/scenes/draw) and `cmd/hermec` may import Ebiten.
- Default key path: `os.UserConfigDir()/hermec/identity.key` (created on first run; connect screen shows the fingerprint and a one-line "back up this file" notice with the path).
- Default server URL `ws://localhost:7697/`; default channel focus: first channel from auth_ok.
- Chat history kept per channel, capped at 500 messages (oldest dropped).
- Default amber palette (theme file overrides): bg `#0A0A08`, fg `#FFB000`, dim `#7A5500`, bright `#FFCF40`.
- Ebiten tasks that cannot be unit-tested end with a build check plus a scripted manual checklist instead of test steps; everything in `ui/state`/`ui/theme` is TDD.
- Commit after every task; messages end with the trailer `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

## Review Focus

1. Server unreachable or auth rejected at connect → readable error on the connect screen, app stays usable (retry), never a panic or silent freeze — test in Task 1 (reducer handles Err before any join), manual check in Task 3.
2. Connection dies mid-session (server stops) → main scene shows a disconnect notice and offers reconnect, no deadlock on the closed Events channel — reducer test in Task 1 (Events close ⇒ Disconnected state), manual check in Task 4.
3. Non-ASCII input (Portuguese accents: ç, ã, é) must survive the input buffer and render — rune-based editing tests in Task 1; manual check in Task 4.
4. Long messages and tiny windows: text wraps, never overflows panes, resize doesn't crash — wrap-function tests in Task 1, manual resize check in Task 4.
5. A flood of events (hundreds of chat messages) must not grow memory unboundedly or stall the draw loop — cap test in Task 1 (501st message drops the first), drain-per-frame bound in Task 3's wiring.

---

### Task 1: UI state core (`ui/state`)

**Files:**
- Create: `ui/state/state.go`, `ui/state/input.go`, `ui/state/wrap.go`
- Test: `ui/state/state_test.go`, `ui/state/input_test.go`, `ui/state/wrap_test.go`

**Interfaces:**
- Consumes: `client.Event` (`Chat *proto.ChatMessage`, `Presence *proto.Presence`, `Err error` — exactly one non-nil), `proto.Member`.
- Produces (Tasks 3–5 draw from exactly these):
  - `type Phase int` — `PhaseConnect`, `PhaseMain`, `PhaseDisconnected`
  - `type Message struct { FromName, FromFP, Text string; TS time.Time }`
  - `type State struct { Phase Phase; Status string; ConnectErr string; Channels []string; Active int; Messages map[string][]Message; Members map[string][]proto.Member; Input InputBuffer }`
  - `func New() *State`
  - `func (s *State) SetConnected(channels []string, fingerprint string)` — enters PhaseMain
  - `func (s *State) Apply(ev client.Event, open bool)` — reducer; `open=false` means the Events channel closed → PhaseDisconnected
  - `func (s *State) NextChannel() / PrevChannel()` — clamp/wrap Active
  - `const MaxMessages = 500`
  - `type InputBuffer struct` with `AppendRunes([]rune)`, `Backspace()`, `Submit() string` (returns trimmed text, clears buffer), `String() string` — rune-based, not byte-based
  - `func Wrap(text string, cols int) []string` — rune-aware greedy wrap; cols ≤ 0 returns the text unsplit

- [ ] **Step 1: Write failing tests**

```go
// state_test.go
func TestApplyChatAppendsToItsChannel(t *testing.T)   // chat for "general" lands only in Messages["general"], fields mapped
func TestApplyCapsAt500(t *testing.T)                 // 501 chats → len==500, first message gone, order kept
func TestApplyPresenceReplacesMembers(t *testing.T)   // presence replaces (not appends) Members[channel]
func TestApplyErrSetsStatus(t *testing.T)             // Err event in PhaseMain → Status contains the error, Phase stays Main
func TestEventsClosedMeansDisconnected(t *testing.T)  // Apply(zero, open=false) → PhaseDisconnected
func TestChannelNav(t *testing.T)                     // Next/Prev wrap around len(Channels)
// input_test.go
func TestInputRunes(t *testing.T)                     // AppendRunes([]rune("olá ção")) → String()=="olá ção"; Backspace removes 'o' not a byte
func TestSubmitTrimsAndClears(t *testing.T)           // "  oi  " → Submit()=="oi", String()==""
func TestSubmitEmptyReturnsEmpty(t *testing.T)        // whitespace-only → ""
// wrap_test.go
func TestWrapAscii(t *testing.T)                      // "aaaa bb cc" cols 5 → ["aaaa", "bb cc"]
func TestWrapLongWordHardBreaks(t *testing.T)         // 12-rune word, cols 5 → 3 lines
func TestWrapUnicodeRunes(t *testing.T)               // accented text counts runes, not bytes
```

- [ ] **Step 2: Run `go test ./ui/state/ -v` — expect FAIL (package missing)**
- [ ] **Step 3: Implement the three files** — no Ebiten imports anywhere in this package.
- [ ] **Step 4: Run `go test ./ui/state/ -v` — expect PASS**
- [ ] **Step 5: Commit** — `feat: UI state core (reducer, input buffer, text wrap)`

### Task 2: Theme (`ui/theme`)

**Files:**
- Create: `ui/theme/theme.go`, `ui/theme/default.toml` (embedded), `ui/theme/font.go`
- Test: `ui/theme/theme_test.go`

**Interfaces:**
- Consumes: BurntSushi/toml; `golang.org/x/image/font/gofont/gomono` + ebiten `text/v2` for the font face (font.go is the one theme file allowed to import ebiten text — it returns a `*text.GoTextFace`).
- Produces:
  - `type Theme struct { BG, FG, Dim, Bright color.RGBA; Scanlines bool; FontSize float64 }`
  - `func Default() Theme` — the amber palette from Global Constraints, Scanlines true, FontSize 14
  - `func Load(path string) (Theme, error)` — TOML keys `bg`, `fg`, `dim`, `bright` (hex strings "#RRGGBB"), `scanlines` (bool), `font_size` (float); missing keys keep Default values; unknown keys rejected (same stance as server config); bad hex → error
  - `func Face(size float64) (*text.GoTextFace, error)` in font.go — gomono face, cached source

- [ ] **Step 1: Write failing tests**

```go
func TestDefaultPalette(t *testing.T)      // Default().FG == #FFB000 etc. (all four), Scanlines true
func TestLoadPartialOverride(t *testing.T) // file with only fg="#00FF00" → FG green, rest default
func TestLoadRejects(t *testing.T)         // unknown key errors; "#GGGGGG" errors; empty path returns Default with no error
```

- [ ] **Step 2: Run `go test ./ui/theme/ -v` — expect FAIL**
- [ ] **Step 3: Implement theme.go (+ embed default.toml), font.go**
- [ ] **Step 4: Run `go test ./ui/theme/ -v` — expect PASS**
- [ ] **Step 5: Commit** — `feat: amber theme with TOML overrides and gomono face`

### Task 3: App shell, connect scene, -local (`ui`, `cmd/hermec`)

**Files:**
- Create: `ui/app.go`, `ui/connect.go`, `cmd/hermec/main.go`
- Test: build checks + manual checklist (Ebiten layer)

**Interfaces:**
- Consumes: Tasks 1–2; `client.Dial/Join/SendChat/Events/Channels/Fingerprint/Close`; `identity.Load/Generate/Save`; `server.New/Start/Addr` (for -local).
- Produces:
  - `func Run(opts Options) error` in ui/app.go — `Options{ServerURL, Name, KeyPath, ThemePath string, Local bool}`; owns the Ebiten game loop
  - `cmd/hermec` flags: `-server ws://localhost:7697/`, `-name` (default "anon-"+short fingerprint), `-key` (default per Global Constraints), `-theme` (optional path), `-local` (ignore -server; start in-process `server.New(Config{Addr:"127.0.0.1:0", Channels:[]string{"general"}, Roles: default with DefaultRoles ["user"]})`, connect to its Addr; Shutdown on exit)
  - Game wiring contract for Task 4: the game struct holds `*state.State`, drains at most 64 events from `client.Events()` per Update tick (non-blocking select), forwards typed runes/keys to the state, and triggers Dial on connect-submit in a goroutine delivering the result via a channel read in Update (the Ebiten loop never blocks on network)
  - Connect scene: name field (prefilled), server field (prefilled, disabled under -local), fingerprint + "back up <keypath>" line, Enter = connect, error line on failure (stays on connect scene)

- [ ] **Step 1: Implement ui/app.go (window 960x600, resizable, title "Hermec", theme load, scene switch on state.Phase), ui/connect.go, cmd/hermec/main.go**
- [ ] **Step 2: Build check — `go build ./...` and `go vet ./...` clean**
- [ ] **Step 3: Manual checklist (run `go run ./cmd/hermec -local`):** window opens in amber; fingerprint + backup line visible; Enter connects to the embedded server; reaches main scene (blank is fine pre-Task 4); closing the window exits cleanly (server shut down, no hang). Then `go run ./cmd/hermec` with NO server running: readable error on connect screen, app alive, retry works once a server is started.
- [ ] **Step 4: Run full suite `go test -count=1 ./...` — expect PASS**
- [ ] **Step 5: Commit** — `feat: hermec GUI shell, connect scene, -local embedded server`

### Task 4: Main scene — channels, chat, input (`ui`)

**Files:**
- Create: `ui/main_scene.go`
- Modify: `ui/app.go` (route input/draw to the scene)
- Test: build check + manual checklist; any new pure logic goes into `ui/state` with tests

**Interfaces:**
- Consumes: Task 1 state exactly (Channels/Active/Messages/Members/Input, Wrap), Task 2 theme, Task 3 game wiring.
- Produces (layout contract for Task 5):
  - Left pane (fixed 180px): channel list, Active highlighted (bright), member count per channel; Ctrl+Up/Ctrl+Down (or Tab) switches channel → triggers `Join` for not-yet-joined channels via the non-blocking wiring
  - Right pane: scrollback of Wrap()ed messages, format `[HH:MM] name: text` (name in bright, text in fg, timestamps dim); PageUp/PageDown scroll, autoscroll pinned to bottom unless scrolled up
  - Bottom input line: `> ` prompt, blinking block cursor, Enter submits → `SendChat` (non-blocking), Esc clears; input rendered from `state.Input`
  - Status line (top, dim): server URL, own name + fingerprint, connection status; disconnect switches to a centered "CONNECTION LOST — Enter to reconnect" (PhaseDisconnected → re-Dial via connect flow)

- [ ] **Step 1: Implement main_scene.go + app.go routing; move any nontrivial pure helpers (scroll clamping, time formatting) into ui/state with unit tests added in the same commit**
- [ ] **Step 2: Build + vet clean; `go test -count=1 ./...` PASS (incl. any new state tests)**
- [ ] **Step 3: Manual checklist (two instances against one `-local`... note -local spawns its own server, so for two-client chat run `go run ./cmd/hermec-server` + two `go run ./cmd/hermec -name a / -name b`):** messages typed in A appear in B with name and timestamp; `olá ção é` renders correctly end-to-end; long paragraph wraps inside the pane; window resized very small doesn't crash and panes clamp; kill the server → CONNECTION LOST screen, restart server, Enter reconnects and rejoins; 200-message flood (paste repeatedly) stays smooth and history caps.
- [ ] **Step 4: Commit** — `feat: main chat scene (channels, scrollback, input)`

### Task 5: CRT treatment (`ui`)

**Files:**
- Create: `ui/crt.go` (Kage shader + offscreen pass)
- Modify: `ui/app.go` (final-draw hook, theme toggle respected)

**Interfaces:**
- Consumes: Theme.Scanlines; the whole scene rendered to an offscreen image.
- Produces: a post-process pass — subtle scanlines (every other line darkened ~12%), mild phosphor glow (cheap: one 1px-offset additive redraw at low alpha), vignette optional-skip; `Scanlines=false` in the theme bypasses the pass entirely; `F1` toggles at runtime for A/B checking.

- [ ] **Step 1: Implement the Kage shader + offscreen pipeline behind the toggle**
- [ ] **Step 2: Build + vet clean; full suite PASS**
- [ ] **Step 3: Manual checklist:** scanlines visible but text stays readable at font size 14; F1 toggles live; `scanlines=false` theme file bypasses; no visible FPS drop while scrolling a full pane (watch the window at ~60fps).
- [ ] **Step 4: Commit** — `feat: CRT scanline/glow post-process with theme toggle`

### Task 6: Run-everything docs + release checks

**Files:**
- Modify: `README.md` (Development section), `example.server.toml` untouched; Create: `example.theme.toml`
- Test: command checks

**Interfaces:**
- Produces: README "Run it" section — `go run ./cmd/hermec -local` as THE one-command dev experience; two-machine quickstart (server on one, `-server ws://host:7697/` on the other); theme customization pointer to example.theme.toml (all keys, commented, matching ui/theme).

- [ ] **Step 1: Write README section + example.theme.toml (must Load() cleanly — add a test in ui/theme asserting the example file parses)**
- [ ] **Step 2: Run `go test -count=1 ./...` — PASS; `go vet ./...` clean**
- [ ] **Step 3: Cross-build checks — `GOOS=windows go build ./...`; `GOOS=linux GOARCH=arm64 go build ./...` (Ebiten linux build may need the CI's X11 headers — if the local linux cross-build of ui fails on C headers, verify `GOOS=linux` builds in CI instead and note it in the report)**
- [ ] **Step 4: Commit** — `docs: one-command run, theme example`
