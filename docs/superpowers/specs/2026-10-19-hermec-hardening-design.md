# HERMEC: Hardening for v0.5.0 — design

Date: 2026-10-19. Status: requested by Victor ("prioritize them so we ship screen sharing with those guards"), from a review batch of v0.4.0 (commit 8c2776f) by a friend's session. Ships with the screen-share round as v0.5.0.
Scope note: some items came from a private security review; this document states the guards, not how to abuse their absence.

## 1. Input limits (names and chat)
- The server cleans and caps every display name at auth: the same rules as the client's `CleanName` (strip format/control/line- and paragraph-separator characters, trim) moved to a shared package, capped at `discover.MaxNameRunes`. An empty result becomes `anon`.
- Chat text over 4096 bytes is rejected with `bad_request` (`message too long`); the sender's client shows it as a status.
- Protocol encoding stops HTML-escaping (`<`, `>`, `&` sent as-is); the envelope is encoded the same way.
- Every outbound frame is checked against `MaxMessageSize` before it is queued; an oversize frame is dropped and logged, never sent.
- `voice_mute` with an unchanged value does not re-broadcast voice_state.
- Roster trimming to the size budget is a single pass.
- The client also cleans externally sourced names on every display path: chat senders, voice occupants, discovered servers.

## 2. Voice media limits
- At most one inbound audio track and one inbound video track per peer; extra tracks are ignored and logged.
- Each subscriber reuses audio transceivers (free list + ReplaceTrack), so join/leave churn no longer grows the SDP.
- A server `rtc_offer` whose encoded size exceeds `MaxMessageSize` is not sent; that peer's media is reset and the event logged.
- Voice channels have a participant cap, config `max_voice` (default 25); joining a full channel returns error code `voice_full`, shown as status `voice channel is full`.

## 3. Connection guards
- WebSocket upgrades that carry an `Origin` header are rejected (native clients send none; web pages always do).
- Limits: at most 64 connections waiting for auth at once, at most 16 concurrent connections per remote IP, and `max_clients` (default 256) authenticated sessions. Over a limit: the connection is closed (pre-auth) or rejected with error code `server_full`.
- Failed auth slows down per remote IP: after 5 failures within a minute, each further attempt waits 1 s, doubling up to 60 s, before the reply; a success resets it.

## 4. Paste and cursor editing
- One shared text-input helper for every field (join address, name fields, chat input, Create form, settings name, dialogs): Ctrl+V (Cmd+V on macOS) pastes from the clipboard with newlines and control characters stripped and the field's length cap applied; Left, Right, Home, End and Delete move/edit at a caret; the caret is drawn.
- In the Join address field, pasting a value that starts with `ws://` or `wss://` replaces the field's prefilled `ws://` (never `ws://ws://`).

## 5. Release and repo hygiene
- `.gitignore`: `*.key`, `*.pem`, `*.server.toml` (except `example.server.toml`), `server.toml`, `hosted.toml`, `.claude/`. CI fails if a file containing a HERMEC private key header is committed.
- Workflows: top-level `permissions: contents: read`; `go-version-file: go.mod`; the release publishes only if the tagged commit is on `main` and `go vet ./...` + `go test ./...` pass for it.
- Tests that fail on macOS/arm64 are fixed: float comparisons use a tolerance, graphics tests are skipped on darwin, the voice-timeout test no longer depends on wall-clock speed. CI also runs tests on `macos-latest` and `ubuntu-24.04-arm`.

## 6. Client polish
- Ping: a pong for a ping already counted as timed out is ignored; the send timestamp is taken under the client's write lock.
- Reconnect: an `auth_failed` stops automatic retries (manual ↻ still works, with the error in the tooltip); backoff gets ±25% jitter.
- Update check: the release link must start with `https://github.com/medeirosvictor/hermec/releases/`.
- macOS: when `tailscale` is not on PATH, use `/Applications/Tailscale.app/Contents/MacOS/Tailscale` if it exists; when no CLI is found, log it once and show a one-line hint on the Join page.

## 7. Docs
- README: Join steps no longer say "including ws://" (the field already has it) and mention paste; Linux needs `xclip`, `xsel` or `wl-clipboard` for copy/paste; macOS steps build the latest release tag with the version stamped (via `scripts/build-mac.sh`) so Mac builds see the update banner; "How safe is this?" gains: the host of a server you join could impersonate you on other servers until server identity keys land (always-on round); existing options `allowed_keys`, `password`, `tls_cert`/`tls_key` are mentioned where gaps are listed.
- quickstart: update-check wording, download server binaries + verify SHA256SUMS instead of building.

## 8. Deferred (not in v0.5.0)
Auth bound to server identity (needs Plan A server keys + protocol version bump); discovery table eviction and version hiding; macOS shortcuts; mic-silence warning; server config validation/logging; auto-rejoin voice after reconnect; Homebrew tap.

## 9. Testing
Each section ships with tests: oversize name/chat never produce a frame > MaxMessageSize; mute no-op; two-track peer; 50 join/leave cycles keep SDP bounded; oversize offer → reset; voice_full; Origin rejected / absent accepted; per-IP and pre-auth caps; auth backoff timing (fake clock); paste/caret behaviour and the ws:// rule; late pong ignored; auth_failed no auto-retry; jitter bounds; update URL prefix; Tailscale path fallback (seam).
