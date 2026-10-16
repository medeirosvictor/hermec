# Hermec — Session Handoff

> Living brief for picking up work in a fresh Claude session. Update at every milestone.
> Last updated: 2026-10-16 (**B.2 implemented on branch `worktree-hermec-b2`, not merged (merge is Victor's call)**: spec `docs/superpowers/specs/2026-10-16-hermec-server-names-roster-design.md`, plan `docs/superpowers/plans/2026-10-16-hermec-server-names-roster.md`; Victor's B.2 real-mouse checklist (spec section 5) is queued below next to B.1's, **B.3 (icon editor) and the v0.2.0 release steps (spec section 6) are next**. **B.1 merged to main at 338cc40 (2026-10-12, not pushed)**: spec `docs/superpowers/specs/2026-10-11-hermec-concurrent-sessions-design.md`, plan `docs/superpowers/plans/2026-10-11-hermec-concurrent-sessions.md`, all 8 tasks done. Before that: Round B started 2026-10-11, decomposed into B.1/B.2/B.3. Round A merged at a162ac1; audio round still awaiting Victor's listening test).
>
> **Round B decomposition (agreed 2026-10-11):** B.1 sessions = items (a)+(b) below; B.2 server identity/management = (d), (e), then (c) roster; B.3 = (f) icon editor. B.1 decisions: every server opened this session stays connected (no startup auto-dial), right-click tile → Disconnect, unread = rail dot + bright channels, one `session` struct per server. README gained a plain-language "how people reach each other" section (4ec31a6).

## Start here (fresh session)

1. Read this file, then `.vibe-wise/progress.md` (Victor's learning state) and the memory note `hermec-project`.
2. Repo state: `main` was pushed at 554bf3f; the B.2 spec/plan commits (72d6c0e, 7fc6eba) are local on `main` only (not pushed). Clean apart from untracked local files (`primos.server.toml`, `hermec2.key`, `.claude/`), which are never committed. The four `.claude/worktrees/agent-*` worktrees are old, from earlier rounds, and left alone.
3. Run the app: `go run ./cmd/hermec -local` (PowerShell live reload: `$env:HERMEC_DEV=1; go run ./cmd/hermec -local`). In the owner's real settings, **CRT and curvature are currently off**: F1 toggles CRT, F3 toggles curvature, F2 cycles themes.
4. Order of work: Victor's B.1 + B.2 real-mouse checklists (pipeline item 0), then the merge decision for `worktree-hermec-b2` (Victor's call), then the v0.2.0 release plus the old-build banner test (B.2 spec section 6), then B.3 (icon editor) or screen share, per Victor.
5. Never run the GUI from an agent against `%APPDATA%\hermec` (see Gotchas). Build the separate-config-dir flag first if automated GUI runs are needed.

## Where things stand

- **Round A merged to main (a162ac1, pushed 2026-10-11)**, pending Victor's eyeball/real-mouse checklist; spec `docs/superpowers/specs/2026-10-11-hermec-visual-refresh-design.md`, plan `docs/superpowers/plans/2026-10-11-hermec-visual-refresh.md`: CRT curvature + scan bar + F3 toggle, parchment theme, soft panels, hand cursor, speaking glow, signal bars + connection-quality panel (`ui/quality.go`, `ui/state/grade.go`), Discord-style title bar via a replaced window procedure (WM_NCCALCSIZE + WM_NCHITTEST), envelope-with-angel-wings icon (`ui/icon`, regenerate with `go run ./cmd/hermec-icon -out assets`), HERMEC_DEV live reload. Also: the tailscale discovery probe no longer flashes a console window on Windows.
- **Footprint:** ~17% idle CPU on this machine, mostly Present/vsync. The old build measured ~15% the same day. See `docs/footprint.md`.

- **Shipped on main, all reviewed (CI is NOT green: the `cross-build` job on main fails because `go build ./...` with cgo off cannot compile hraban/opus, for both GOOS=windows and linux/arm64; see pipeline item 7):** foundation (chat server + headless client) → GUI (Ebiten, amber CRT, VT323, servers rail, settings) → voice (SFU relay, call bar, occupancy) → discovery (DISCOVERED list: LAN + tailnet + saved) → release & update (**v0.1.0 released**; update banner Ctrl+U; GUI idle CPU 34%→6.5%).
- **v0.1.0:** https://github.com/medeirosvictor/hermec/releases/tag/v0.1.0 — NOTE: repo was PRIVATE at last check; Victor said he'll flip it public (blocks friends' downloads + the update banner until then).
- **Deployment state:** primos config = `primos.server.toml` (repo root, untracked). vitoserver has the server installed + systemd user service, currently **disabled/parked** (re-enable: `ssh vito "systemctl --user enable --now hermec"`; linger already on). Victor hosts from desktop meanwhile. Desktop firewall: "Hermec Server" Allow-rule installed (he had denied the prompt once — that was the connection-refused bug).
- **MERGED, PENDING EAR TEST: Audio Quality & Devices plan** (`docs/superpowers/plans/2026-10-07-hermec-audio-quality.md`), on main (b66c73b..bf7f28b, 9 commits; all task reviews + final whole-branch review clean; full suite + -race green). What changed: capture discards stale audio only after a genuine pause ≥100ms (chop fix; hard 80ms latency cap); playback moved oto→malgo with 20ms periods + per-sender jitter buffer (start 2 / target 3 / trim at 5 / Opus PLC budget 3 — buzzsaw fix; latency bounded at 100ms); decode moved off the audio callback's lock; `audio.ListDevices()` + `input_device`/`output_device` settings rows (cycle on click, persist immediately, unknown device → default + notice, never crash); chat input now sits under the chat pane (single-source geometry held). **Acceptance gate = Victor's repeat Mac↔desktop listening test — the plan closes only on his "clean audio" report.** End-to-end one-way latency estimate: 110–170ms.

## Process conventions (keep these)

- Subagent-driven development per plan: worktree via EnterWorktree, SDD ledger + pre-flight scan, Sonnet implementers / Haiku small reviews, fix rounds with scoped re-reviews, final whole-branch review on the most capable model, merge menu to Victor, controller tags releases on main only.
- Commit trailer follows whatever the session's attribution instruction gives (the visual-refresh round used `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`).
- cgo env on this machine: PATH needs `C:\msys64\ucrt64\bin` (user PATH has it; shells opened before 2026-10-07 don't), CGO_ENABLED=1; `-race` works locally now. Pure-Go server cross-compiles to Pi.
- Victor is in vibe-wise learning mode: he owns design decisions; brainstorm checkpoints before architectural work; teach inner workings junior-style on request. State lives in `.vibe-wise/`.

## Next-session pipeline (Victor's queue, in his priority order — reordered 2026-10-07: screenshare before the trust plans)

(Standing gate: the audio round closes only on Victor's listening test, see above.)

0. **B.1 real-mouse checklist (merged to main; run `go run ./cmd/hermec -local` plus a second server):** spec section 5 (call on A, click B, chat on B, back to A with the call uninterrupted; right-click tile, Disconnect on the call server ends the call; unread dot and bright channel; bust placement; kill a server and watch the tile go reconnecting and back), plus: Ctrl+M while viewing another server, the call bar look on the connect scene, rail marks at 100% and 150% DPI, viewed + reconnecting tile look. Then the **B.2 checklist** (spec section 5, last bullet; B.2 lives on branch `worktree-hermec-b2`, not merged). Also on the B.2 checklist: clicking the dim members toggle in a narrow window turns the saved setting off � decide whether that click should do nothing.

1. **Owner real-mouse checklist for the title bar:** drag, snap, Win+arrows, edge/corner resize, double-click maximize, buttons, maximized layout, 150% DPI. Also the Round A eyeball checks (four presets, glow strength, curvature subtlety).
2. **Round B (decomposed into B.1/B.2/B.3; status per item):**
   - (a) DONE in B.1 (merged to main): stay connected across servers (concurrent sessions; switching servers keeps the current call);
   - (b) DONE in B.1 (merged to main): rail "you are here" bust (top half of a stick figure) on the server where you're in voice;
   - (c) DONE in B.2 (branch `worktree-hermec-b2`, not merged): right-side member roster of everyone on the server (needs server-wide presence; protocol change);
   - (d) DONE in B.2 (branch `worktree-hermec-b2`, not merged): server names: the server sends its configured `server_name` on connect, and creating/adding a server asks for a name. This fixes the "two lo tiles" confusion (hermec://local "local" + a stale ws://localhost:7697/, both initials "lo");
   - (e) DONE in B.2 (branch `worktree-hermec-b2`, not merged): remove/rename saved servers (right-click menu, from the old UI/UX batch);
   - (f) = B.3, still open: in-app pixel-art editor for a server icon.
3. **Remaining old UI/UX batch items:** resizable panels, "user X is typing…". (The right-click menu on rail tiles now has Rename, Disconnect and Leave Server, from B.2 on branch `worktree-hermec-b2`.)
4. **Idle CPU:** redraw-on-demand / idle frame cap is the CPU lever (the profile shows ~93% in Present, i.e. vsync-bound; drawing only when something changed, or capping idle frames, is what cuts it).
5. **Screen sharing + webcam** (Victor-prioritized, unchanged phasing), then Plans A/B/C (unchanged, below).
   Screenshare detail: Phasing agreed in summary form: (i) SFU video support — codec-agnostic RTP fanout mostly exists; the NEW work is keyframe handling (relay RTCP PLI from new subscribers to the publisher) + bandwidth accounting; integration-testable with synthetic frames. (ii) Windows capture (DXGI duplication) + VP8 encode via libvpx (second cgo dep — MSYS2 + CI packages + the DLL walk handles distribution) with v1 caps ~15fps/~2Mbps (host relay cost is ~60× voice — the kbps log matters). (iii) Receive: VP8 decode → Ebiten texture in the GUI's reserved video pane, click-to-enlarge, CRT shader applies free. (iv) Webcam = same pipeline, camera source, call-bar toggle; macOS/Linux capture are follow-ups. Risks: libvpx plumbing (same dance as opus), sharer-side encode CPU (measure + cap), PLI = the SFU's first codec-aware logic.
   - **Plan A — trust layer (adopted design):** server identity keypair; founder-signed append-only membership/config chain (permissioned sigchain — "blockchain minus consensus"); unique signed invite links (revocable, invite-tree); bans/roles as signed ops. SUBSUMES the old moderation plan.
   - **Plan B — member hosting:** authorized-hosts list in the chain; any trusted member can host; discovery by server fingerprint; split-brain rule (discovery-first + deterministic priority).
   - **Plan C — synced encrypted history:** gossip append-only log, group key rotated on ban (reopens E2EE as at-rest first).
6. **Deferred minors from Round A** (triaged by the final Opus review as not blocking merge; the SDD ledger was deleted with the worktree, so this list is the record):
   - Theme/dev reload: `Presets()` re-parses embedded TOML per call (cache it); `-theme` file edits apply after F2 moved to a preset, and a preset edited while inactive is lost on cycle; dev-reload error in `st.Status` is not cleared after a later successful reload.
   - CRT/render: glow is 3 full-screen passes (first place to look for idle CPU); `Time` float32 loses precision after hours; the GUI goroutine launch will not work on macOS (main-thread requirement).
   - Animation/panels: per-tick `Rows()` + string-key allocations in `anim.go` and O(rows^2) pruning; muted occupant glow cuts instead of fading; panel fade over-draw slop; settings name row shows a pointer, not a text cursor.
   - Quality/RTT/health: unsolicited pong can inject a huge sample (clamp to timeout); `Senders` allocates under the mixer lock; quality panel does per-frame work even when closed.
   - Title bar: hover can stick on a bar button after the mouse leaves the window; brief native-caption flash at startup.
   - Tests missing: connect-form hits, banner click, rowHL easing, addBarHits clipping, pinger interval seam, jitter-reset/TryLock paths.
   - Icon: `TestICO` checks only header/width and does not compare committed assets with the generator.
7. Backlog beyond: **fix the `cross-build` CI job** (red on main): restrict it to the cgo-free packages (`./server/... ./cmd/hermec-server ./client/... ./discover/...` plus other pure packages) or set up cgo cross toolchains; auto-connect-on-launch; GUI polish/identity trust (avatars, key pinning); direct-P2P opt-in; macOS packaging; quickstart paragraph on node-sharing vs tailnet invites; RenderKey extraction to ui/state; hardening list in PLAN.md roadmap item 3 (correlation IDs first).
8. Audio deferred minors (from the audio plan's reviews, none blocking): mid-call device unplug is silent one-way audio until leave (wants a malgo stop-callback/watchdog); jitter depth can settle at 4-5 frames after a hiccup with no decay back to 3 (slow-decay drop would recover 40ms); `resolveDevices`/notice-to-Status path untested (needs a seam); long device names overflow the settings row (no clip); CJK device names render blank (font lacks glyphs); a TryLock-miss PLC slot burns budget and plays silence (rare, bounded); add a ±25-30ms harsh-jitter test variant; no sequence numbers so a truly lost frame splices undetected (note for loss signalling); startup with a saved device does a synchronous enumeration before the window opens.

## Open decisions / gotchas

- Repo public flip = Victor's action; until then update banner can't fire (404 on unauthenticated API).
- Tailscale for friends: recommend NODE SHARING of the hosting machine (not tailnet invites) — friends see one machine only.
- Chat history is deliberately not persisted anywhere yet (design round pending; Plan C covers the distributed version).
- Two clients on ONE machine with the same key silence each other by design (self-echo filter keys on fingerprint) — second client needs `-key <other file>`.
- No AEC: headphones mandatory in calls.
- **Measurement runs must use a separate config dir.** The Task 10 agent's runs touched the owner's real `%APPDATA%\hermec` (`servers.toml` last_seen), and its cleanup closed the owner's running instance. Add a `-config-dir`-style flag or an env var before the next automated GUI run (backlog item).
- **Firewall prompt:** test runs keep WebRTC ICE on loopback (Task 7b, commit ec5a0fb), so `go test` never triggers the Windows Firewall prompt. `go run` still builds a new exe each time, so to run the app itself build to a stable path (`go build -o bin\hermec.exe ./cmd/hermec`) and allow the firewall once.
- **Release exe is console-subsystem**, so double-clicking shows a terminal window behind the app. Consider `-H windowsgui` plus file logging (backlog item).
