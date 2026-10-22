# Hermec: session handoff

Compact brief for picking up work in a fresh Claude session. It describes the
app as it is now. Update it at every milestone and delete what is resolved.
User-facing docs: [README.md](README.md), [docs/protocol.md](docs/protocol.md),
[docs/quickstart-friends.md](docs/quickstart-friends.md). Roadmap:
[PLAN.md](PLAN.md).

## Start here

1. Read this file, then `.vibe-wise/progress.md` (Victor's learning state) and
   the memory note `hermec-project`.
2. Repo state: `main` == `origin/main`, released through **v0.5.1**; chat
   history (PR #9) is merged but not released. PR 3 is built in the worktree
   `.claude/worktrees/hermec-failover` (branch `worktree-hermec-failover`).
   Untracked local files (`primos.server.toml`, `hermec2.key`, `.claude/`) are
   never committed. The `.claude/worktrees/agent-*` worktrees are old and left
   alone.
3. **Current round: always-on.** Spec (approved design):
   `docs/superpowers/specs/2026-10-21-hermec-always-on-design.md`. Three PRs:
   identity and chain, chat history, host link with warm standby and failover.
   **PR 1 (identity and chain) and PR 2 (chat history, PR #9) are merged.**
   PR 3 (host link, warm standby, failover; one PR by Victor's choice) is in
   progress: plan `docs/superpowers/plans/2026-10-22-hermec-host-failover.md`. After always-on and webcam, Victor moves to stress
   testing and use-case scenarios.
4. Run the app: `go run ./cmd/hermec -local` (PowerShell live reload:
   `$env:HERMEC_DEV=1; go run ./cmd/hermec -local`). In Victor's real settings,
   CRT and curvature are off: F1 toggles CRT, F3 curvature, F2 cycles themes.
5. Never run the GUI from an agent against `%APPDATA%\hermec` (see Gotchas).
6. Proven in real use (2026-10-19): voice over Tailscale node sharing (Canada
   host with Brazil friends) and the macOS from-source build. Known edge: at
   the `max_voice` cap of 50 the worst-case server offer is about 57 KB of the
   64 KiB frame limit (video not counted). Fine for the friend group; revisit
   before big rooms.

## Process conventions

- Subagent-driven development per plan: a worktree via EnterWorktree, an SDD
  ledger and pre-flight scan, Sonnet implementers and Haiku small reviews, fix
  rounds with scoped re-reviews, a final whole-branch review on the most
  capable model, a merge menu to Victor. The controller tags releases on `main`
  only.
- Commit trailer follows whatever the session's attribution instruction says.
- Victor owns design decisions (vibe-wise learning mode). Brainstorm checkpoints
  come before architectural work; teach inner workings junior-style on request.
  State lives in `.vibe-wise/`.
- Docs hygiene (Victor's rule): when a round finishes, delete its spec and plan
  and move anything still open into this file. Docs describe the app as it is
  now; stale text is removed, not annotated.
- cgo on this machine: PATH needs `C:\msys64\ucrt64\bin`, `CGO_ENABLED=1`;
  `-race` works locally. The server is pure Go and cross-compiles to a Pi.

## Next-session pipeline (Victor's order)

1. **Always-on (now).** See the spec. Later idea riding the same synced
   store: file sharing (a capped shared folder). vitoserver stays parked
   (Victor: do not bring it back for now).
   - The PR 1 review items (rollback pin, pre-auth chain budget, error refs,
     roster on grant) and the README standby line are tasks in the PR 3 plan.
   - **PR 3 decisions (Victor, 2026-10-22):** the password never travels
     (scrypt key + HMAC login; plain passwords accepted from v0.6 clients for
     one release); friend hosts node-share their PC on Tailscale, and the
     standby must be reachable by at least half the clients.
   - **PR 2 leftovers (low, from the fix re-reviews):** lines evicted during a
     rewrite are written back to the file and trimmed at the next start; the
     `gen` check in a rewrite is dead code; the rewrite swap fsyncs under the
     store lock (milliseconds of chat stall); untested: eviction or Close
     during a rewrite, Compact skipping while a rewrite runs.
   - **No UI yet** for ban, grant or host changes; the protocol and server
     enforce them (`chain_append`). PR 3 adds Allow hosting / Stop hosting /
     Move up; ban/grant UI is still to be placed.
   - **PR 1 parked minors** (can wait): the client accepts any server nonce
     length; voice_join has no ban check (a just-banned member can flash back
     into voice until its socket drains); an oversize single entry would be
     dropped silently by the frame writer (cannot arise via chain_append);
     grant role names and permission strings are not validated; a failed or
     crashed first-run migration orphans a chain file (next boot founds a new
     ID; nobody pinned the old one); whitespace-only file tails are reported
     as torn; missing tests for 0600 mode, mutation isolation, the sync/close
     rollback branches, a replayed host proof, and an `auth_ok.server_id`
     mismatch.
2. **Webcam + call view.** Same pipeline as screen share (govpx VP8), camera
   source, call-bar toggle. Camera capture per OS is new work. Victor
   (2026-10-22): clicking the voice channel you are in opens a Discord-style
   call view with participant tiles (speaking glow, mute, webcam or avatar,
   LIVE streams); today that click does nothing.
3. **Signed membership + connection modes** (Victor, 2026-10-22; brainstorm
   first). Invite tree on the chain: the inviter signs, the invitee co-signs,
   so there is an audit trail of who brought whom; ban or review a whole
   branch; invite as a role; k-of-n admin signatures for host and role
   changes. Connection modes: group policy in the chain (`tailnet`, `public`,
   `lan`), per-host reachability; tailnet mode embeds Tailscale via tsnet
   (pure Go) on a group tailnet (Tailscale account or Headscale) so Victor's
   private tailnet stays separate; public mode (port forward or VPS) requires
   `wss://` and invites. Author-signed chat (Plan C) comes after.
4. **Friends adjustments** (designed in chat 2026-10-19, not yet specced):
   - Volume: per-person volume (right-click a person, 0 to 200%, saved per
     fingerprint), a mic gain settings row (0 to 200%), and a soft limiter.
     Friends were too quiet in both directions. Volume for a shared stream
     belongs here too.
   - Live names: a new `profile` client-to-server message (name now, avatars and
     icon later). The server re-broadcasts roster, presence and voice_state. A
     client "profile sync" pushes local edits to every connected server and
     writes server-announced changes into `servers.toml`.
   - Audio devices: the input and output settings rows become a dropdown, and
     switching applies immediately, including mid-call. Known cause of "switching
     does nothing": capture and playback open the device once per voice join
     (the device open in `audio/capture.go` and `audio/playback.go`), so a
     change applies only after leave and rejoin.
   - Bandwidth metrics: upload and download kbps per call and per person (pion
     stats plus WebSocket bytes) in the quality panel and the log.
   - Ping: clamp the RTT outlier. An unanswered ping adds a 6 s sample
     (`pongTimeout` in `client/rtt.go`, recorded by `check`). Victor saw 1400 ms
     spikes with fine voice. Check Tailscale DERP versus direct
     (`tailscale ping`, `tailscale status`, `tailscale netcheck`).
5. **B.3: icon editor.** In-app pixel-art editor for a server icon.

## Open checklists

Run these on a released build or `go run ./cmd/hermec -local` plus a second
server. Agents must not run the GUI against the real `%APPDATA%\hermec`.

### Screen-share real test

- Share your screen to the Brazil friend over Tailscale.
- Watch someone else share.
- Read small text on a shared screen (try both 720p and 1080p).
- Switch between two sharers.
- Send each other a log file (Settings, `logs: open folder`).
- Apply the Tailscale grant (README, "How safe is this?") and confirm voice and
  video still work.

### v0.5.1 call and stream controls (hand test)

- Watch a stream, then leave fullscreen with Esc, F11 and the strip button. In
  fullscreen the first Esc only leaves fullscreen; the second goes back to chat.
  The window can always be closed.
- Mouse over the video shows the strip (fullscreen, close). In fullscreen the
  strip stays up while the mouse rests in the bottom 15% of the video.
- Share your own screen, click your own LIVE tag: a preview of your stream
  opens. It shows `stop sharing` and `change screen`.
- Change screen while a friend watches: they keep watching; a screen with a
  different shape is letterboxed.
- Click the voice channel you are already in: nothing happens (you stay in).
- The "voice connected" panel at the bottom of the channel column: disconnect,
  share and stop share, the signal bars opening the quality panel, and the
  panel while viewing a different server than the call's.

### B.1 real-mouse checklist (several servers at once)

- Start a call on server A, click server B, chat on B, go back to A: the call
  was never interrupted.
- Right-click the tile of the call's server, Disconnect: the call ends.
- Unread dot on the rail and bright channel name for a channel with new
  messages (not for your own messages, not for the channel you are reading).
- The "you are here" bust on the rail tile of the call's server, at 100% and
  150% DPI.
- Kill a server process: its tile goes to reconnecting (pulsing), then back.
- Ctrl+M while viewing another server; the call bar look on the connect scene;
  rail marks at 100% and 150% DPI; the look of a viewed tile and a reconnecting
  tile.

### B.2 real-mouse checklist (names, Leave, member list)

- Tooltips on rail tiles and the header showing the server name.
- Rename: save, an empty name resets to the server's name, Esc cancels.
- Leave Server, with and without a call on it, and on the viewed server.
- The member list while a second client connects and quits.
- The members toggle survives a restart; the list hides in a narrow window;
  check at 150% DPI.
- Open question for Victor: clicking the dim members toggle in a narrow window
  turns the saved setting off. Should that click do nothing?

### Add a Server checklist

1. Create a server from the app (**+**, **Create my own**).
2. A friend joins over Tailscale using the copied invite address.
3. Close and reopen the app: the server comes back and the tile reconnects.
4. Start `hermec-server` on 7697 first, then Create: the port-in-use error
   shows.
5. Join a bad address: the tile shows the red x and the retry arrow; click the
   arrow.
6. Up and Down in the DISCOVERED list fill the address field.
7. Stop hosting, then Start hosting (tile right-click menu).
8. Leave the hosted server and check `hosted.toml` is gone.

### Title bar and Round A eyeball checklist (Windows)

- Title bar: drag, snap, Win+arrows, edge and corner resize, double-click
  maximize, minimize/maximize/close buttons, the maximized layout, 150% DPI.
- Curvature and click accuracy in all four corners; the scan bar visible but
  subtle; parchment readability; a pointer cursor on every clickable thing; the
  speaking glow fading in and out; the signal bars reacting under a network
  throttle or a second laptop on Wi-Fi.
- The four presets and glow strength.

### Voice, discovery and audio listening gate

- Audio gate: quality and delay are OK in real calls (Victor, 2026-10-21). It
  closes once per-person volume (Discord-style, friends adjustments round)
  ships and works in a call. Headphones are mandatory (no echo cancellation).
  One-way latency estimate: 110 to 170 ms.
- Voice: two instances on one machine need `-key <other file>` for the second.
  Join from both and hear each other, mute shows on both sides, the speaking
  glow tracks, leave and rejoin work, killing one instance mid-call makes it
  vanish for the other.
- Discovery: `-local` discovers itself; a second machine on the LAN sees the
  server; with Tailscale running, a tailnet server appears within about 2 s;
  Ctrl+R refreshes; a killed server drops on the next refresh; a click joins in
  one action; no stutter while probing; `discoverable = false` hides a server.

## Deferred and parked

Lists checked against the code on 2026-10-21; items already fixed were dropped.

**Hardening and server:**
- Wire correlation IDs for `rtc_*` messages and errors (retires the 300 ms glare
  heuristic and error misattribution); SFU PeerConnection rebuild past N dead
  m-lines (long-call SDP growth); client packet-loss concealment on RTP gaps;
  per-peer `rtc_offer` debounce.
- Discovery table eviction and hiding the version in replies. Discovery probing
  cost: probing should run only while the Join list is visible (it also calls
  `tailscale status --json` once).
- Per-key rate limits. Full-length fingerprints for allowlists and grants.
- Server config validation and logging. Mic-silence warning. macOS shortcuts.
  Auto-rejoin voice after a reconnect (today rejoining is always a click).
  Homebrew tap.
- Release workflow extras: draft releases and a protected environment, SHA-pinned
  actions, Dependabot, govulncheck, build attestations, a concurrency group.
- A `hosted.toml` written by a pre-fix v0.5 build may contain `max_voice`,
  `max_clients` or `max_per_ip` keys, which an older app (v0.4) rejects.
- Linux CI with a longer fuzz budget.

**UI (Add a Server and hosting):**
- The Create page shows the invite address without a copy action (the tile menu
  has "Copy invite address").
- `stopHosted` shuts the server down synchronously on the game goroutine (up to
  3 s freeze).
- `g.notice` is drawn only on the connect scene.
- A stale saved `ws://127.0.0.1:<port>/` entry can map to the hosted session as
  a second tile. The self-discovery filter misses second NICs and IPv6.

**UI (calls and streams):**
- The voice panel takes 3 to 4 rows from a channel list that cannot scroll; a
  short window or a busy server can push rows out of view. The channel list
  needs scrolling.
- An open quality panel stays invisible under the video. `previewOn` is one tick
  late. A single panel label wider than the pane at large font scale clips.
- After change screen an old-screen frame can take the forced keyframe
  (harmless, deltas stay valid). Same-aspect even-rounding can leave a 2 px
  black edge.
- Task 1 test nit: the `setFullscreen` bool is not asserted in the recorder.

**UI (theme, render, animation):**
- `theme.Presets()` re-parses the embedded TOML on every call (cache it). `-theme`
  file edits apply after F2 moved to a preset, and a preset edited while inactive
  is lost on cycle. A dev-reload error in `st.Status` is not cleared after a
  later successful reload.
- The CRT glow is 3 full-screen passes (first place to look for idle CPU).
  `Time` float32 loses precision after hours.
- `anim.go`: per-tick `Rows()` and string-key allocations, O(rows^2) pruning;
  a muted occupant's glow cuts instead of fading; panel fade over-draw slop; the
  settings name row shows a pointer, not a text cursor.
- The quality panel does per-frame work even when closed; `Senders` allocates
  under the mixer lock.
- An unsolicited pong can inject a huge RTT sample (clamp it, see the friends
  adjustments).
- Title bar: hover can stick on a bar button after the mouse leaves the window;
  a brief native-caption flash at startup.
- Old UI/UX batch: resizable panels, "user X is typing...".
- Missing tests: connect-form hits, banner click, rowHL easing, addBarHits
  clipping, the pinger interval seam, jitter-reset and TryLock paths. `TestICO`
  checks only the header and width, not that committed assets match the
  generator.
- Idle CPU: the lever is redraw-on-demand or an idle frame cap, not faster draw
  code (see `docs/footprint.md`).
- RenderKey extraction to `ui/state`.

**Audio:**
- Mid-call device unplug is silent one-way audio until leave (wants a malgo
  stop-callback or watchdog).
- Jitter depth can settle at 4 to 5 frames after a hiccup with no decay back to
  3.
- `resolveDevices` and the notice-to-Status path are untested (needs a seam).
- Long device names overflow the settings row; CJK device names render blank (the
  font lacks the glyphs).
- A TryLock-miss PLC slot burns budget and plays silence (rare, bounded).
- Add a harsh-jitter test variant (plus or minus 25 to 30 ms).
- No RTP sequence numbers are used for loss, so a truly lost frame splices
  undetected.
- Startup with a saved device does a synchronous enumeration before the window
  opens.

**Screen share:** sharing from macOS and Linux (the button is dim there), window
capture, shared-screen audio, DXGI capture, simulcast. Capture relies on
Ebiten's per-monitor DPI awareness (`screencap/gdi_windows.go`).

**Backlog:** auto-connect on launch, GUI polish and identity trust (avatars, key
pinning), macOS packaging and signing, a quickstart paragraph on node sharing
versus tailnet invites, `-H windowsgui` plus file logging for the Windows exe.

## Ideas and roadmap

- **Headscale** as an in-app option (self-hosted coordination instead of
  Tailscale's), for groups beyond the Tailscale free-plan user limit and
  friend-to-friend reachability. A tsnet and Headscale spike (2026-10-07) works
  but adds about 19 MB to both binaries and a large dependency tree, and moves
  hard infrastructure (public box, domain, TLS, own DERP) onto hosts. Revisit
  only as an opt-in tsnet listener behind a build tag. Tailscale clients have
  one active tailnet at a time.
- **External hosting:** let something else host the server (a paid game server
  or VPS) for better connections and to invite strangers without compromising
  security. Needs TLS (`wss://`) and invite links or an allowlist (server ID
  pinning is done). The headless `hermec-server` already runs on Linux and Windows.
- **Scale target:** screen share plus webcams plus about 10 people in one call is
  the "can home infra do this" test, after best-host and config sync. Rough
  host upload: voice about 4 Mbps, one share watched by 9 about 14 Mbps, 10 small
  webcams about 27 Mbps, so about 45 Mbps.
- **Security roadmap ("make it safe"):** a "Tailscale only" listen option for
  hosted servers; invite links and allowlists in the app; TLS for public hosting;
  an app UI for bans (the chain and protocol have them; ban-evasion barriers to
  pick from: invite gating, proof-of-work key minting, invite trees). Known gap:
  the v2 handshake does not bind the connection, so on plain `ws://` a path
  attacker can relay a host's proof and read the password; TLS or Tailscale
  closes it.
- **Plan A, trust layer (rest):** unique signed invite links (revocable, invite
  tree). The identity chain, bans and roles as signed ops are built.
- **Plan B, member hosting:** see always-on above.
- **Plan C, synced encrypted history:** a gossip append-only log with a group key
  rotated on ban; end-to-end encryption arrives first at rest. Interest in
  Discord's published DAVE protocol (MLS-based, MIT `libdave`, whitepaper) as the
  model. The protocol already reserves `e2ee_key_exchange` and `e2ee_frame`.
- **Noise suppression and echo cancellation** (removes "headphones mandatory"):
  RNNoise-class suppression; find a pure-Go or WASM option to keep zero external
  libs. webrtc-audio-processing (BSD) is the AEC3 Discord uses; echo
  cancellation needs the playback signal as reference, which the malgo playback
  path can provide.
- **Other feature ideas (Discord-inspired, ranked):** push-to-talk with a
  global hotkey and a voice-activity threshold; typing indicator and member
  presence (server-wide presence is separate from channel presence); per-channel
  permission overrides in `core/roles`; a slash-command style bot API; screen
  share quality presets. Skip Nitro-style extras, social discovery, game
  detection, overlays. Study Discord's behaviour, write it from scratch; never
  copy its code, constants or assets (its terms forbid reverse engineering).
- **Direct P2P opt-in:** per-participant, mixed calls allowed (a relay-choosing
  participant is relayed on every link). Also a localhost control API on the
  client for MCP and voice-agent control, and an MCP server as a separate
  project acting as an ordinary bot identity.
- **Open report:** "other apps cannot use the mic at the same time". Capture
  uses miniaudio's default shared mode (no exclusive request), so it is not by
  design. Needs a repro (which app, what it shows, in a call or not).

## How it works and where to find things

- **Packages:** `core/proto` (wire types and codec), `core/identity`,
  `core/roles`, `core/names`, `core/chain` (signed server chain: entries,
  state, server ID); `server` (WebSocket server, handshake in `conn.go` and
  `handshake.go`, chain bootstrap, `-apply-config` and live chain updates in
  `chain.go`, SFU in `sfu.go` and `voice.go`, limits in `guards.go`, UDP media mux in `mediaport.go`, discovery
  responder in `discovery.go`); `client` (headless library: connect, voice,
  share, RTT); `audio` (malgo capture and playback, jitter buffer, gopus);
  `video` (VP8 via govpx, letterbox); `screencap` (Windows GDI capture, stub
  elsewhere); `discover` (LAN and Tailscale probing); `update` (GitHub release
  check); `applog` (session log files); `ui` (Ebiten GUI) with `ui/state`
  (pure logic and tests), `ui/theme` (presets, bundled fonts), `ui/icon`.
  Commands: `cmd/hermec`, `cmd/hermec-server`, `cmd/hermec-icon` (regenerate
  icons: `go run ./cmd/hermec-icon -out assets`).
- **Files on the user's machine** (`<user config dir>/hermec/`): `identity.key`,
  `settings.toml`, `servers.toml`, `hosted.toml` (the server hosted from the
  app), `chains/` and `history/` (chain and chat history of the hosted
  server), `logs/`. Saved servers keep the last `server_name` the server sent
  (`Name`) and the user's own nickname (`Nick`).
- **Sessions:** every server opened this session stays connected; one `session`
  struct per server (`ui/session.go`). Connection lost: a call on that session
  drops (rejoining is a click), and the session retries with backoff 1, 2, 4,
  8, 16 s, then every 30 s (`state.NextBackoff`) until it connects or the user
  disconnects. An `auth_failed` stops automatic retry; the manual retry arrow
  still works. Unread is a rail dot plus a bright channel name, never counts.
- **Hosting from the app:** one hosted server per app, `hosted.toml` in the
  config dir, started at launch when it exists, stable key `hermec://hosted`
  mapped to `ws://127.0.0.1:<port>/`; `-local` uses `hermec://local`. The hosted
  server's media port equals its own port.
- **Tiles:** a new tile is pending (greyed, pulsing, not saved) until its first
  successful connect. A failed dial shows a red x (bottom right) and a retry
  arrow (bottom left); Retry and Leave Server are in the right-click menu.
- **Title bar (Windows):** the window stays decorated, but its native caption is
  hidden by replacing the window procedure (`WM_NCCALCSIZE`, `WM_NCHITTEST`), so
  Windows still does drag, snap and resize. The bar is 32 px, drawn outside the
  CRT pass; on Linux and macOS the native frame stays (`ui/winbar_windows.go`,
  `ui/titlebar.go`, `ui/state/titlebar.go`).
- **Media:** all WebRTC media uses one UDP port, equal to the server port
  unless `media_port` is set (`udp_port_min/max` is the legacy range). Video is
  VP8 PT 96, forwarded only to watchers. The server marks source switches with
  `video_source`. See `docs/protocol.md`.
- **Releases:** a `v*` tag publishes only if the tagged commit is on `main` and
  vet and tests pass. Client assets build on native runners (Windows x64 and
  ARM, Linux x64 and ARM); servers are cross-built with CGO off. The tag is
  stamped into `version.Version`. macOS: `scripts/build-mac.sh`.
- **Footprint:** `docs/footprint.md`.

## Gotchas

- Tailscale for friends: recommend node sharing of the hosting machine, not
  tailnet invites (friends see one machine only).
- Chat history is plain JSON lines on the host, unencrypted and unsigned (Plan C
  is the encrypted version). It lives in `<config>/hermec/history/<server-id>.jsonl`
  for a server hosted from the app, and at `-history` (default `history.jsonl`
  next to `-config`) for `hermec-server`. Caps `history_days` (30; at most
  3650) and `history_mb` (8; at most 64, since the store is held in memory and
  rewritten under its lock) come from the chain config entry and apply at once
  on a config append; code in `server/history.go` and `ui/state`. PR 3 must
  carry history to the standby.
- **Changing history caps of an app-hosted server:** the app has no group
  settings UI and ignores group keys in `hosted.toml` (it logs a warning and
  shows a notice). The way is `hermec-server -config <file>.toml -apply-config
  -key <config>/hermec/identity.key -chain
  <config>/hermec/chains/<server-id>.jsonl` with the app closed; the TOML holds
  `history_days`/`history_mb` and `addr` set to the hosted port so the
  running-server check works. Tested in
  `cmd/hermec-server/apply_test.go`. A wrong `-key` path makes a new key there
  and the append is then refused (not the founder). A wrong `-chain` path is
  worse: it founds a new chain there, prints "appended N entries" and leaves
  the real chain untouched (open: make this recipe refuse to create a chain).
- **Old config writers reset history caps.** A `config` entry is the full group
  config, and one written by a build from before chat history (its
  `hermec-server -apply-config`, or an admin's app sending `chain_append`)
  lacks `history_days`/`history_mb`, so it resets both to the defaults.
  Everyone who runs `-apply-config` on a group must be on v0.6+.
- Two clients on one machine with the same key silence each other by design
  (the self-echo filter keys on fingerprint). A second client needs `-key
  <other file>`.
- No echo cancellation: headphones are mandatory in calls.
- **Measurement and GUI runs must use a separate config dir.** An earlier agent's
  runs touched Victor's real `%APPDATA%\hermec` (`servers.toml` `last_seen`) and
  its cleanup closed his running instance. There is no `-config-dir` flag yet;
  add one (or an env var) before the next automated GUI run.
- **Firewall prompt:** test runs keep WebRTC ICE on loopback, so `go test` never
  triggers the Windows Firewall prompt. `go run` builds a new exe each time; to
  run the app itself, build to a stable path (`go build -o bin\hermec.exe
  ./cmd/hermec`) and allow the firewall once.
- The release exe is console-subsystem, so double-clicking shows a terminal
  window behind the app.
- `scripts/build-mac.sh` leaves the repo on a detached HEAD at the release tag;
  `git checkout main` goes back.
- Deployment: primos config is `primos.server.toml` (repo root, untracked).
  vitoserver has the server installed as a systemd user service, currently
  disabled and parked (re-enable: `ssh vito "systemctl --user enable --now
  hermec"`; linger is on). Victor hosts from his desktop meanwhile; the desktop
  has a "Hermec Server" Windows Firewall allow-rule.
- The repo is public, so friends' downloads and the update banner work.
