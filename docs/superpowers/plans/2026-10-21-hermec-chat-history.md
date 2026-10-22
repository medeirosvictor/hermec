# HERMEC chat history Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The active host keeps each text channel's chat on disk (30 days / 8 MB by default, set in the chain config) and serves it to members when they join a channel, with older pages on scroll-up, deduplicated by message id.

**Architecture:** A per-server JSON-lines store (`server/history.go`) holds every stored `ChatMessage` in memory, sorted by `(ts, id)`, and appends each new one to the file. The server stamps an id on each chat message, stores it, and sends a `history` frame when a connection joins a text channel and on each `history_before`. The client emits `history` frames as events. The UI state merges them by id, and the chat view asks for the next older page when scrolled to the top.

**Tech Stack:** Go, gorilla/websocket, Ebiten (UI), BurntSushi/toml.

**Spec:** `docs/superpowers/specs/2026-10-21-hermec-always-on-design.md` (section 4; section 3 for the chain config).

## Global Constraints

- Pure Go, no new dependencies.
- Text channels only; voice channels have no history.
- `ChatMessage` gains `id`: 16 random bytes set by the host that accepts the message, written as 32 lowercase hex characters.
- Caps come from the chain `config` entry: `history_days` (default 30, `0` = keep nothing) and `history_mb` (default 8).
- The host trims at startup and every hour by rewriting the file without the oldest lines.
- Each `history` frame is at most `proto.MaxMessageSize` (64 KiB) encoded.
- The client deduplicates by `id` and orders by `(ts, id)`; reconnects never show a line twice.
- Storage: the app uses `<config>/hermec/history/<server-id>.jsonl`; `hermec-server` takes `-history` (default `history.jsonl` next to `-config`).
- Docs describe the app as it is now: README and `docs/protocol.md` sections are rewritten in place, not appended to.
- Never run the GUI from an agent against `%APPDATA%\hermec`; never kill `hermec.exe`. UI tests use temp dirs.
- Never commit `primos.server.toml`, `hermec2.key` or `.claude/`.
- Commit trailer on every commit: `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Rulings made while planning (deviations from the spec text)

- **History is sent on channel join, not right after auth.** Joining is the existing read gate (`join_channel` permission), the app joins a text channel when it is viewed, and sending under the same `chMu` hold as the join keeps the history frame ahead of any live message. Costs: a channel never opened has no history loaded; that matches today, since no live messages arrive for it either.
- **`history` echoes `before`** (the `before_id` it answers; empty for the join page) so the client can tell the newest page from an older one.
- **An empty `HistoryPath` keeps history in memory only** (tests and `-local`).
- **Malformed lines in the history file are skipped**, not fatal; the next trim rewrites the file without them. Chat history is not security-critical, and one bad line should not stop a host from starting.
- **The UI keeps at most `MaxMessages = 2000` per channel** (was 500) and stops asking for older pages at that cap.

## Review Focus

1. A member reconnects after missing more than one page of messages: the latest page does not overlap what they have, and they should see no silent gap. Pinned in Task 4 (`TestHistoryLatestPageWithGapReplaces`).
2. An admin sets `history_days = 0`: the next trim must delete the file and no new message may be stored. Pinned in Task 2 (`TestTrimZeroDaysDeletesFile`) and Task 3 (`TestChatNotStoredWhenDaysZero`).
3. The history file has a torn or garbage line (crash mid-write, hand edit): the host must still start and serve the good lines. Pinned in Task 2 (`TestOpenSkipsMalformedLines`).
4. A page holds messages whose JSON grows past the frame limit (4096 control characters each become `\u0001`): the frame must still fit 64 KiB. Pinned in Task 3 (`TestHistoryFrameFitsLimit`).
5. `history_before` for a channel the sender has not joined must not leak it; one with an unknown id must answer an empty final page rather than an error loop. Pinned in Task 3 (`TestHistoryBeforeNotJoined`, `TestHistoryBeforeUnknownID`).

---

### Task 1: Wire types and history config fields

**Files:**
- Modify: `core/proto/proto.go`
- Modify: `core/chain/entry.go` (`ConfigBody`)
- Modify: `server/server.go` (`Config`), `server/config.go` (`fileConfig`, `LoadConfig`, `MarshalTOML`)
- Modify: `server/chain.go` (`groupKeys`, `configBody`, `normConfig`, `ApplyTOML`, `chainCfg`)
- Test: `core/proto/proto_test.go`, `server/config_test.go`, `server/chain_test.go` (add to the existing files)

**Interfaces:**
- Produces:
  - `proto.TypeHistory = "history"`, `proto.TypeHistoryBefore = "history_before"`
  - `proto.ChatMessage.ID string \`json:"id,omitempty"\``
  - `proto.History{Channel string \`json:"channel"\`; Before string \`json:"before,omitempty"\`; Messages []ChatMessage \`json:"messages"\`; More bool \`json:"more,omitempty"\`}`
  - `proto.HistoryBefore{Channel string \`json:"channel"\`; BeforeID string \`json:"before_id"\`}`
  - `chain.ConfigBody.HistoryDays *int \`json:"history_days,omitempty"\``, `HistoryMB *int \`json:"history_mb,omitempty"\``
  - `server.Config.HistoryDays, HistoryMB *int` (nil = default), `server.Config.HistoryPath string`
  - `server.DefaultHistoryDays = 30`, `server.DefaultHistoryMB = 8`, `server.MaxHistoryDays = 3650`, `server.MaxHistoryMB = 1024`
  - `func (c Config) HistoryLimits() (days, mb int)`: defaults for nil, values clamped to `[0, MaxHistoryDays]` and `[1, MaxHistoryMB]`

- [ ] **Step 1: Write the failing tests**
  - `TestHistoryRoundTrip` (proto): encode and decode `History{Channel:"general", Before:"ab", Messages: []ChatMessage{{ID:"0f", Channel:"general", Text:"hi"}}, More:true}` and compare the result field by field.
  - `TestLoadConfigHistory` (server): a file with `history_days = 0` and `history_mb = 16` gives `HistoryLimits() == (0, 16)`; a file without the keys gives `(30, 8)`; `history_days = -1`, `history_days = 3651`, `history_mb = 0` and `history_mb = 1025` each make `LoadConfig` fail with an error that names the key.
  - `TestMarshalTOMLOmitsUnsetHistory`: `DefaultConfig().MarshalTOML()` output does not contain `history_`; with `HistoryDays` set to 0 it contains `history_days = 0`.
  - `TestApplyTOMLHistoryKeys` (server/chain_test.go): on a chain whose config has no history fields, `ApplyTOML` with `set = ["history_days"]` and `HistoryDays = 7` appends one config entry whose body has `HistoryDays == 7` and `HistoryMB == nil`. Running it again appends nothing.
  - `TestChainCfgHistory`: `chainCfg` copies `HistoryDays` and `HistoryMB` from the chain config.
  - `TestOldConfigBodyDecodes`: a config entry body JSON with no history keys decodes to nil pointers.

- [ ] **Step 2: Run them; expect compile failures for the missing names.**
  Run: `go test ./core/proto/ ./server/ -run 'History|ChainCfg|OldConfigBody'`

- [ ] **Step 3: Implement**
  - Add the types and constants above.
  - Add `history_days` and `history_mb` to `groupKeys`, `fileConfig` (as `*int`), `LoadConfig` (validate the ranges, set only when defined), `MarshalTOML` (write only when non-nil; older apps reject unknown keys), `configBody`, `ApplyTOML`'s per-key merge, and `chainCfg`.
  - `normConfig` leaves the pointers as they are.
  - `MarshalLocalTOML` does not write them, since they are group fields.

- [ ] **Step 4: Run all tests in the touched packages.**
  Run: `go test ./core/... ./server/`
  Expected: PASS.

- [ ] **Step 5: Commit** `feat: history wire types and history_days/history_mb config`

### Task 2: History store

**Files:**
- Create: `server/history.go`
- Test: `server/history_test.go`

**Interfaces:**
- Consumes: `proto.ChatMessage` (with `ID`)
- Produces:
  - `type History struct` (safe for concurrent use; its own mutex)
  - `func OpenHistory(path string) (*History, error)`. `""` = memory only. A missing file is empty, not an error. Malformed lines are skipped, and `Skipped() int` reports how many. Duplicate ids keep the first.
  - `func (h *History) SetLimits(days, mb int)`
  - `func (h *History) Add(m proto.ChatMessage) error`. A no-op while `days == 0`. It appends one JSON line and does not fsync. Messages stay sorted per channel by `(TS, ID)`.
  - `func (h *History) Page(channel, beforeID string, n int) (msgs []proto.ChatMessage, more bool)`. Returns up to `n` messages, oldest first: the newest ones when `beforeID == ""`, otherwise the ones strictly older than `beforeID`. `more` reports whether older messages remain. An unknown `beforeID` returns `nil, false`.
  - `func (h *History) Trim(now time.Time) error`.
    - It drops messages with `TS` before `now - days*24h`, then drops the oldest messages across all channels until the file size is at most `mb << 20`.
    - It rewrites the file through a temp file in the same directory plus a rename, only when something was dropped or skipped.
    - With `days == 0` it empties the store and removes the file.
  - `func (h *History) Size() int`: bytes of the stored lines, newline included.

- [ ] **Step 1: Write the failing tests.** Each one uses `t.TempDir()`.
  - `TestHistoryAddReopen`: add 3 messages to two channels, reopen, and `Page("general", "", 10)` returns that channel's messages in `(TS, ID)` order with `more == false`.
  - `TestHistoryPaging`: 25 messages, `Page(ch, "", 10)` returns the newest 10 with `more == true`; `Page(ch, first.ID, 10)` returns the 10 before them; the last page returns `more == false`; `Page(ch, "nope", 10)` returns `nil, false`.
  - `TestHistoryOrderTiesByID`: two messages with equal `TS` order by `ID`.
  - `TestOpenSkipsMalformedLines` (Review Focus 3): the file is a valid line, then `{"broken`, then a valid line, then a torn last line with no newline. The store opens, holds 2 messages, and `Skipped() == 2`. After `Trim`, reopening gives `Skipped() == 0`.
  - `TestOpenDedupesIDs`: a file with the same id twice holds one message.
  - `TestTrimDays`: with `SetLimits(30, 8)`, a message 31 days old is dropped and a 29-day-old one is kept, and the file is rewritten without the old one.
  - `TestTrimSize`: with `SetLimits(30, 1)`, adding about 1.5 MiB of messages and then trimming leaves `Size() <= 1<<20`, with the newest messages kept.
  - `TestTrimZeroDaysDeletesFile` (Review Focus 2): with `SetLimits(0, 8)` after messages were added, `Trim` removes the file and `Page` is empty. `Add` then writes nothing and the file does not reappear.
  - `TestHistoryMemoryOnly`: `OpenHistory("")` supports `Add`, `Page` and `Trim` without touching disk.

- [ ] **Step 2: Run them; expect them to fail.** Run: `go test ./server/ -run 'History|Trim|OpenSkips|OpenDedupes'`

- [ ] **Step 3: Implement `server/history.go`.**
  - In memory: `map[string][]proto.ChatMessage` per channel, plus a set of ids.
  - Lines: `json.Marshal` of a `ChatMessage` plus `"\n"`.
  - Size: the sum of the line lengths.
  - Open: read the whole file with `os.ReadFile`, split on `\n`, and count an unparseable final segment as skipped.

- [ ] **Step 4: Run them; expect them to pass, including under the race detector.**
  Run: `go test -race ./server/ -run 'History|Trim|OpenSkips|OpenDedupes'`

- [ ] **Step 5: Commit** `feat(server): chat history store with day and size caps`

### Task 3: Server stores and serves history

**Files:**
- Modify: `server/server.go`: a `hist *History` field. `Start` opens the store at `cfg.HistoryPath`, sets the limits, trims, and starts the hourly trimmer. `Shutdown` stops the trimmer through the existing wait group.
- Modify: `server/channels.go`: `chat`, `join` and `route`.
- Modify: `server/chain.go`: after `refreshGroup` in `handleChainAppend`, re-apply `SetLimits` from the new chain config.
- Modify: `cmd/hermec-server/main.go`: the `-history` flag, defaulting to `history.jsonl` next to `-config`. Log `Skipped()` when it is above 0.
- Modify: `ui/hosted.go`: `historyDir(hostedPath) = filepath.Join(filepath.Dir(hostedPath), "history")`, then set `cfg.HistoryPath = filepath.Join(historyDir(path), h.id+".jsonl")` in `startHosted`. Create the directory with `0o700`.
- Test: `server/history_serve_test.go`, `cmd/hermec-server` (add a flag-default test if one already exists for `-chain`; otherwise skip)

**Interfaces:**
- Consumes: Task 1 types and `Config.HistoryLimits()`; Task 2 `History`
- Produces:
  - `historyPage = 100`, the most messages read for one frame.
  - `trimInterval = time.Hour`.
  - `func encodeHistory(h proto.History) []byte`. It drops messages from the oldest end, and sets `More = true` when it drops any, until the frame fits `proto.MaxMessageSize`. It uses the `fitMembers` and `encodedLen` approach from `channels.go`.
  - The limits are read from the chain on each trim and on each config append, so changes apply live.
  - The history frame is enqueued to the joining connection under the same `chMu` hold that adds it to the channel. Joining a voice channel sends nothing.
  - `chat` sets `ID` (16 bytes from `crypto/rand`, hex) and `TS`, and stores the message before broadcasting, both under `chMu`. A store error is logged and the message is still broadcast.
  - `history_before`:
    - Not joined to that channel: `not_joined`.
    - Malformed: `bad_request`.
    - Otherwise: one `history` frame with `Before` set to the requested id.
    - An unknown id gets `Messages: []` with `More: false`.
    - The request counts against the normal per-connection message rate.

- [ ] **Step 1: Write the failing tests** in `server/history_serve_test.go`, using the existing helpers in `channels_test.go` (`sendEnv` and the frame readers).
  - `TestJoinSendsHistory`: client A sends 3 chat messages to `general`. Client B joins and receives `presence`, then one `history` frame with those 3 messages in order, each with a 32-character hex `ID`, `More == false` and `Before == ""`.
  - `TestJoinNoHistoryWhenEmpty`: joining an empty channel sends no `history` frame.
  - `TestHistorySurvivesRestart`: with a `HistoryPath` in `t.TempDir()`, send 2 messages, shut down, start a new server on the same path and chain, and a joiner gets both messages.
  - `TestHistoryBeforePages`: 150 messages. The join frame has the newest 100 and `More == true`. `history_before` with the oldest id gets the remaining 50, `More == false`, and `Before` equal to that id.
  - `TestHistoryBeforeNotJoined` (Review Focus 5): `history_before` for `general` without joining gets error `not_joined` and no `history` frame.
  - `TestHistoryBeforeUnknownID` (Review Focus 5): gets `history{Messages: [], More: false}`.
  - `TestHistoryFrameFitsLimit` (Review Focus 4): store 20 messages of 4096 `\x01` characters directly in the store, and confirm `len(encodeHistory(...)) <= proto.MaxMessageSize`, with `More == true` and at least 1 message kept.
  - `TestChatNotStoredWhenDaysZero` (Review Focus 2): with `HistoryDays` set to 0, chat is still broadcast live (with an `ID`), and a later joiner gets no `history` frame.
  - `TestConfigAppendAppliesHistoryLimits`: the founder appends a config with `HistoryDays` set to 0 through `chain_append`, and a message sent afterwards is not stored.
- Existing server and client tests that read frames strictly in order after a join may now see an extra `history` frame. Change those tests to skip `history` frames; do not change the server's order.

- [ ] **Step 2: Run them; expect them to fail.** Run: `go test ./server/ -run 'History|JoinSends|JoinNo|ChatNotStored|ConfigAppendApplies'`

- [ ] **Step 3: Implement** as specified in Files and Interfaces.

- [ ] **Step 4: Run the full suite under the race detector.** Run: `go test -race ./server/ ./client/ ./ui/... ./cmd/... ./integration/`. Expected: PASS.

- [ ] **Step 5: Commit** `feat(server): store chat and serve history on join and history_before`

### Task 4: Client events and UI state merge

**Files:**
- Modify: `client/client.go`: `Event.History *proto.History`, a read-loop case for `TypeHistory`, and `func (c *Client) HistoryBefore(channel, beforeID string) error` (sends one `history_before`; the answer arrives as an event).
- Modify: `ui/state/state.go`
- Test: `client/history_test.go`, `ui/state/history_test.go`

**Interfaces:**
- Consumes: Task 1 types; Task 3 server behaviour
- Produces:
  - `state.Message.ID string`
  - `state.MaxMessages = 2000`
  - `State.Older map[string]bool`: the host has messages older than the oldest one shown.
  - `State.Loading map[string]bool`: a `history_before` is in flight.
  - `func (s *State) CanLoadOlder(ch string) bool`: true when `Older[ch] && !Loading[ch] && len(s.Messages[ch]) < MaxMessages`.
  - `func (s *State) OldestID(ch string) string`
  - `func (s *State) MarkLoading(ch string)`
  - Merge rules in `Apply`:
    - Chat event: skip it when its non-empty `ID` is already in the channel. Otherwise insert it in `(TS, ID)` order; a live message normally lands at the end.
    - History with `Before != ""` (an older page): merge it with dedup, set `Older[ch] = More`, clear `Loading[ch]`.
    - History with `Before == ""` (the join page): when `More` is true and the frame's oldest id is not in the channel, replace the channel's messages with the frame (the local ones can be fetched again) and set `Older[ch] = true`. Otherwise merge with dedup, and set `Older[ch] = More` only when the channel was empty before.
    - Over `MaxMessages`: keep the newest `MaxMessages` and set `Older[ch] = true`.
    - History frames never set `Unread`.
  - `SetConnected` keeps `Messages`, as it does today. It clears `Loading`, since a request in flight is lost on reconnect.

- [ ] **Step 1: Write the failing tests**
  - `TestClientHistoryEvent` (client): against a real `server.New`, send 2 chat messages, join from a second client, and get an `Event` whose `History.Messages` has length 2. `HistoryBefore(ch, oldestID)` then produces a second `History` event with `Before == oldestID`.
  - `TestHistoryMergeDedup` (state): a join page with ids a, b, c, then live chat c (a duplicate), then live chat d, gives a, b, c, d.
  - `TestHistoryOrderByTSID` (state): out-of-order frames end up sorted by `(TS, ID)`.
  - `TestHistoryOlderPagePrepends` (state): a join page with `More` set, then `CanLoadOlder` is true, then `MarkLoading` makes it false, then an older page with `Before` set and `More` false prepends and makes `Older` false.
  - `TestHistoryLatestPageWithGapReplaces` (Review Focus 1): the channel holds old local messages 1–3 from before a reconnect. A join page holds 200–299 with `More` set, and none of its ids are local. The channel then holds exactly 200–299, and `Older` is true.
  - `TestHistoryNoUnread`: a history frame for a channel that is not on screen leaves `Unread` false.
  - `TestHistoryCap`: merging past 2000 keeps the newest 2000, and `CanLoadOlder` is false at the cap.

- [ ] **Step 2: Run them; expect them to fail.** Run: `go test ./client/ ./ui/state/ -run History`

- [ ] **Step 3: Implement** to the rules above.

- [ ] **Step 4: Run the tests and expect PASS.** Run: `go test -race ./client/ ./ui/...`

- [ ] **Step 5: Commit** `feat(client,ui): history events, dedup by id, older-page state`

### Task 5: Chat view loads older pages on scroll-up

**Files:**
- Modify: `ui/main_scene.go` (`updateMain` and the session's `ms` struct)
- Test: `ui/history_scroll_test.go` (headless; follow the style of the existing `ui/*_test.go` tests that build a `game` with temp paths)

**Interfaces:**
- Consumes: Task 4 `CanLoadOlder`, `OldestID`, `MarkLoading`, and `client.HistoryBefore`
- Produces:
  - When the active channel's view is at its top, the UI requests the next older page. That covers the case where every line already fits on screen, so a short channel keeps filling until the screen is full or nothing older remains. The top is `s.ms.scroll == state.ClampScroll(total, gm.visible, math.MaxInt)`.
  - The request fires only when `CanLoadOlder(ch)` is true. It calls `MarkLoading(ch)` first, then sends `HistoryBefore(ch, OldestID(ch))` in a goroutine, so the UI never blocks. A send error clears `Loading[ch]`, through the existing `sendCh` path or a dedicated one.
  - Scroll stability:
    - `s.ms.scroll` counts lines from the bottom.
    - The existing "keep the view stable while scrolled up" adjustment applies only when the channel's newest message changed since the last tick; track it in `s.ms.prevNewest` (ID, or TS for messages without one).
    - Prepended older lines leave the offset from the bottom unchanged, so the view stays put.
  - Check that `relayout`'s cache key changes when messages are prepended (for example the count, or the first message). If it only looks at the last message, extend it.

- [ ] **Step 1: Write the failing tests**
  - `TestScrollTopRequestsOlder`: a session whose state has `Older` set for the active channel, with the view at its top, gets `Loading` set after one `updateMain` tick, and the fake or real client receives one `history_before`. A second tick sends nothing more.
  - `TestPrependKeepsScroll`: scroll is 5 with messages at their newest. After an older page is prepended, scroll is still 5.
  - `TestAppendWhileScrolledShifts`: scroll is 5. After 1 new live message, scroll grows by that message's line count, as it does today.

- [ ] **Step 2: Run them; expect them to fail.** Run: `go test ./ui/ -run 'ScrollTop|Prepend|AppendWhile'`

- [ ] **Step 3: Implement** as described above.

- [ ] **Step 4: Run the full suite.** Run: `go vet ./... && go test -race ./...`. Expected: PASS.

- [ ] **Step 5: Commit** `feat(ui): load older chat when scrolled to the top`

### Task 6: Docs and end-to-end check

**Files:**
- Modify: `README.md`. In "How safe is this?", add this line verbatim: "the active host and its standby can read and keep chat for up to `history_days`; set it to 0 to keep nothing." Also say that messages are not signed by their authors (a host can forge or drop lines), there are no edits or deletes, and a ban keeps past messages. Then describe chat history in the user-facing feature text, where the app explains chat.
- Modify: `docs/protocol.md`. Document the `id` on `chat_message`, `history` and `history_before` (fields, the 64 KiB rule, `before` echo, `not_joined`, unknown id), and that history is sent on join. Add `history_days` and `history_mb` to the chain section's config body.
- Modify: `example.server.toml`. Add commented `history_days` and `history_mb` with their defaults and meaning, and the `-history` flag where the example lists file locations.
- Modify: `HANDOFF.md`. In the current round, mark PR 2 done and PR 3 next, and record where history lives (`history/<server-id>.jsonl`). Rewrite in place.
- Create: `integration/history_test.go`

- [ ] **Step 1: Write the failing integration test** `TestHistoryReconnectNoDuplicates`:
  - Start a server with a temp `HistoryPath`. Client A joins `general` and sends 5 messages.
  - Client B connects, joins, and applies its events to a `state.State`.
  - Close B, have A send 2 more messages, reconnect B, and rejoin.
  - B's state shows exactly 7 messages, with ids unique and in order.

- [ ] **Step 2: Run it; expect it to pass** (it exercises Tasks 1–4). If it fails, fix the cause in the owning code, not in the test.
  Run: `go test -race ./integration/ -run TestHistoryReconnectNoDuplicates`

- [ ] **Step 3: Update the docs** listed above. Delete any text that has become wrong; for example, any README line saying chat is not kept.

- [ ] **Step 4: Run the full suite.** Run: `go vet ./... && go test ./...`. Expected: PASS.

- [ ] **Step 5: Commit** `docs: chat history in README, protocol and HANDOFF; integration test`
