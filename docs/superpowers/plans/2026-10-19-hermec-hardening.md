# Hardening for v0.5.0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the input, media, connection and release gaps found in the v0.4.0 review so v0.5.0 (screen share) ships with guards, plus the paste fix every new friend needs.

**Architecture:** Server-side limits at ingress and before every send; SFU caps per peer and transceiver reuse; HTTP-upgrade and listener guards; a shared UI text-input helper; CI/release gates; small client fixes; docs.

**Tech Stack:** Go 1.27, gorilla/websocket, Pion WebRTC v4, Ebiten v2.10, atotto/clipboard, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-10-19-hermec-hardening-design.md`

## Global Constraints

- Names: shared `CleanName` rules, cap `discover.MaxNameRunes`, empty → `anon`. Chat cap 4096 bytes → `bad_request` "message too long".
- No outbound frame may exceed `proto.MaxMessageSize` (65536); encoding never HTML-escapes.
- Media: ≤1 inbound audio + ≤1 inbound video track per peer; `max_voice` default 25 → error code `voice_full`, status `voice channel is full`.
- Connections: reject upgrades with an `Origin` header; ≤64 pre-auth, ≤16 per remote IP, `max_clients` default 256 → `server_full`; auth backoff after 5 failures/min per IP: 1 s doubling to 60 s; success resets.
- Paste: Ctrl+V (Cmd+V on darwin); strip newlines/control chars; field cap; Left/Right/Home/End/Delete; Join field: pasted `ws://`/`wss://` value replaces the prefilled `ws://`.
- Release URL prefix `https://github.com/medeirosvictor/hermec/releases/`. Backoff jitter ±25%. macOS Tailscale fallback `/Applications/Tailscale.app/Contents/MacOS/Tailscale`.
- Additive protocol only (Version stays 1); new error codes documented in docs/protocol.md.
- Commit messages are neutral (describe the guard, not an attack).
- Env (PowerShell): prepend `C:\msys64\ucrt64\bin` to PATH, `CGO_ENABLED=1`. Never launch the GUI, never touch `%APPDATA%\hermec`, never bind 7697 (tests use port 0), never kill hermec.exe, never bare `git stash`. CRLF checkout: gofmt-check changed files with CRs stripped. Commit trailer: blank line then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. Limits must not break normal use: a 64-rune emoji/CJK name, a 4096-byte chat message, 25 people in voice, and screen share (video track) all still work — Tasks 9, 10.
2. Behind Tailscale/NAT many friends may share one remote IP (e.g. a LAN) — the per-IP cap (16) and auth backoff must not lock out a household — Task 11 `TestSameIPManyClientsUnderCap`.
3. Paste of a multi-line or huge clipboard must not freeze or overflow a field — Task 12.
4. The release gate must not block the legitimate tag flow (tag on main, tests green) — Task 13 (verified with actionlint + a PR dry run).
5. Existing v0.4 clients still interoperate (no new required messages) — Tasks 9-11 keep the existing integration tests green.

---

### Task 9: Input limits (names and chat)

**Files:** move `CleanName` from `ui/state/name.go` into a shared package (e.g. `core/names`), keep `ui/state` delegating; `server/conn.go` (authenticate), `server/channels.go` (chat route, roster trim, broadcast send path), `server/voice.go` (mute no-op), `core/proto/proto.go` (Encode); client display paths `ui/state/state.go` (chat sender), `ui/state/voice.go` (occupants), `ui/connect.go` (discovered names). Tests alongside.

- [ ] **Step 1: Tests:** `TestAuthNameCleanedAndCapped` (bidi/control stripped, 1000-rune name capped, empty → `anon`), `TestChatTooLongRejected` (4097 bytes → bad_request; 4096 ok), `TestNoFrameExceedsMax` (max name full of `<` in voice + max chat → every frame a client reads ≤ MaxMessageSize, nobody disconnected), `TestEncodeNoHTMLEscape`, `TestMuteNoopNoBroadcast`, `TestRosterTrimSinglePass` (size budget respected), client `TestDisplayNamesCleaned` (chat sender, occupant, discovered row).
- [ ] **Step 2: Run** `go test ./server/ ./core/... ./ui/... -run 'Name|Chat|Frame|Encode|Mute|Roster|Display'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** focused, then `go test -race -count=1 ./server/ ./core/... ./client/ ./integration/ ./ui/...`. Expected: PASS.
- [ ] **Step 5: Commit** `server: clean and cap names, cap chat, bound frame size`.

### Task 10: Voice media limits

**Files:** `server/sfu.go` (OnTrack per-kind cap, audio transceiver reuse per subscriber, offer size check → reset), `server/voice.go` + `server/config.go` (`max_voice`, `voice_full`), `core/proto/proto.go` (error code), `ui` status mapping for `voice_full`, `example.server.toml`.

- [ ] **Step 1: Tests:** `TestSecondAudioTrackIgnored`, `TestAudioTransceiversReusedOverChurn` (50 join/leave cycles → a staying member's SDP m-line count stays ≤ members+2), `TestOversizeOfferResetsPeer` (seam to inflate the offer), `TestVoiceFull` (max_voice=2, third gets voice_full), UI `TestVoiceFullStatus`; existing share/voice tests stay green.
- [ ] **Step 2: Run** focused. Expected: FAIL.
- [ ] **Step 3: Implement** (mirror the video transceiver-reuse approach already in sfu.go).
- [ ] **Step 4: Run** focused, then `go test -race -count=1 ./server/ ./client/ ./integration/ ./ui/`. Expected: PASS.
- [ ] **Step 5: Commit** `server: per-peer track caps, reuse audio transceivers, voice channel cap`.

### Task 11: Connection guards

**Files:** `server/server.go` (upgrader CheckOrigin, listener/registration caps), `server/conn.go` (pre-auth slot, auth backoff), `server/config.go` (`max_clients`), `core/proto` (`server_full`), docs/protocol.md error codes.

- [ ] **Step 1: Tests:** `TestOriginHeaderRejected` / `TestNoOriginAccepted`, `TestPreAuthCap` (65th idle pre-auth connection closed), `TestPerIPCap` (17th from one IP refused), `TestSameIPManyClientsUnderCap` (16 authed clients from 127.0.0.1 all work), `TestMaxClients` (server_full), `TestAuthBackoff` (fake clock: 6th failure within a minute delayed 1 s, then 2 s…; success resets).
- [ ] **Step 2: Run** focused. Expected: FAIL.
- [ ] **Step 3: Implement** with an injectable clock for backoff.
- [ ] **Step 4: Run** focused, then `go test -race -count=1 ./server/ ./integration/ ./client/`. Expected: PASS (existing tests may need loopback-IP awareness of the per-IP cap — raise it in test configs only if a test legitimately opens >16 connections, and say so).
- [ ] **Step 5: Commit** `server: connection limits, origin check, auth backoff`.

### Task 12: Paste and cursor editing

**Files:** create `ui/textinput.go` (+ test); use it in `ui/connect.go`, `ui/main_scene.go` (chat), `ui/addpage.go`, `ui/settings.go` (name), `ui/dialog.go`; caret drawing.

**Interfaces:** `type textField struct{ text []rune; caret, max int }` with `update(keys, chars, paste func() (string, error))` (pure, testable) and caret position for drawing.

- [ ] **Step 1: Tests:** `TestPasteStripsNewlinesAndCaps`, `TestPasteHugeClipboardCapped`, `TestCaretMovesAndEdits` (Left/Right/Home/End/Delete/Backspace mid-text), `TestJoinPasteReplacesWSPrefix` (`ws://` + paste `ws://100.1.2.3:7697/` → single prefix), `TestPasteShortcutDarwinUsesMeta` (key mapping per GOOS via seam).
- [ ] **Step 2: Run** `go test ./ui/ -run 'Paste|Caret|JoinPaste'`. Expected: FAIL.
- [ ] **Step 3: Implement** and wire into every field.
- [ ] **Step 4: Run** focused, then `go test -race -count=1 ./ui/...`. Expected: PASS.
- [ ] **Step 5: Commit** `ui: paste and caret editing in text fields`.

### Task 13: Release, CI and test hygiene

**Files:** `.gitignore`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `ui/titlebar_test.go`, `ui/menu_test.go`, `ui/devreload_test.go` (darwin skip), `integration/voice_test.go` (timeout test).

- [ ] **Step 1:** `.gitignore` entries per spec section 5; CI step that fails if any tracked file contains the HERMEC private key header (pattern written so the workflow doesn't match itself).
- [ ] **Step 2:** both workflows: top-level `permissions: contents: read` (publish keeps write), `go-version-file: go.mod`; release: a `gate` job (`fetch-depth: 0`; tag commit is an ancestor of `origin/main`; `go vet ./...`; `go test ./...`) that `publish` needs; CI test jobs on `macos-latest` and `ubuntu-24.04-arm` (`go vet ./...`, `go test ./...`).
- [ ] **Step 3:** fix tests: epsilon float comparisons (titlebar, menu), skip `withGraphics` tests on darwin, voice-timeout test uses an already-expired context.
- [ ] **Step 4:** `go run github.com/rhysd/actionlint/cmd/actionlint@latest` clean; `go test -race -count=1 ./...` green. The controller pushes and checks the CI/PR run (macOS + arm legs).
- [ ] **Step 5: Commit** `ci: gate releases on main and tests; macOS and arm64 test runs; ignore local keys`.

### Task 14: Client polish

**Files:** `client/rtt.go`, `client/client.go` (timestamp under writeMu), `ui/session.go` + `ui/state/backoff.go`, `update/update.go`, `discover/tailscale.go` + `discover/self.go` (+ a darwin path seam), `ui/addpage.go` or join view (hint line).

- [ ] **Step 1: Tests:** `TestLatePongAfterTimeoutIgnored`, `TestAuthFailedStopsAutoRetry` (manual retry still works), `TestBackoffJitterBounds` (within ±25%), `TestReleaseURLPrefix`, `TestTailscaleAppFallback` (seam: LookPath fails, app path exists → used), `TestNoTailscaleHint`.
- [ ] **Step 2: Run** focused. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** focused, then `go test -race -count=1 ./client/ ./ui/... ./update/ ./discover/`. Expected: PASS.
- [ ] **Step 5: Commit** `client: steadier ping, no retry loop on auth failure, update link and tailscale path checks`.

### Task 15: Docs

**Files:** `README.md`, `docs/quickstart-friends.md`, `docs/protocol.md` (new error codes, limits), `scripts/build-mac.sh` (new), `HANDOFF.md`.

- [ ] **Step 1:** per spec section 7: Join steps (paste; no "including ws://"), Linux clipboard tools, macOS steps via `scripts/build-mac.sh` (fetch tags, check out the latest release tag, build with `-ldflags "-X github.com/medeirosvictor/hermec/version.Version=<tag>"`), "How safe is this?" impersonation line + existing options, quickstart drift, protocol error codes `voice_full`, `server_full`, limits section. HANDOFF: hardening entry listing what shipped and the deferred list from spec section 8 (neutral wording).
- [ ] **Step 2:** `bash -n scripts/build-mac.sh` (syntax check); commit `docs: limits, paste, macOS build script`.
