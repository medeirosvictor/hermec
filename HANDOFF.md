# Hermec — Session Handoff

> Living brief for picking up work in a fresh Claude session. Update at every milestone.
> Last updated: 2026-10-11 (Round A visual refresh done on worktree-hermec-visual, merging to main; Round B brainstorm next; audio-quality plan still awaiting merge decision and Victor's listening test).

## Where things stand

- **Round A shipped on branch `worktree-hermec-visual`** (merging to main; pending Victor's eyeball checklist): CRT curvature + scan bar + F3 toggle, parchment theme, soft panels, hand cursor, speaking glow, signal bars + connection-quality panel (`ui/quality.go`, `ui/state/grade.go`), Discord-style title bar via a WM_NCHITTEST subclass, envelope-with-angel-wings icon (`ui/icon`, regenerate with `go run ./cmd/hermec-icon -out assets`), HERMEC_DEV live reload. Also: the tailscale discovery probe no longer flashes a console window on Windows.
- **Footprint:** ~17% idle CPU on this machine, mostly Present/vsync. The old build measured ~15% the same day. See `docs/footprint.md`.

- **Shipped on main, all reviewed, CI green:** foundation (chat server + headless client) → GUI (Ebiten, amber CRT, VT323, servers rail, settings) → voice (SFU relay, call bar, occupancy) → discovery (DISCOVERED list: LAN + tailnet + saved) → release & update (**v0.1.0 released**; update banner Ctrl+U; GUI idle CPU 34%→6.5%).
- **v0.1.0:** https://github.com/medeirosvictor/hermec/releases/tag/v0.1.0 — NOTE: repo was PRIVATE at last check; Victor said he'll flip it public (blocks friends' downloads + the update banner until then).
- **Deployment state:** primos config = `primos.server.toml` (repo root, untracked). vitoserver has the server installed + systemd user service, currently **disabled/parked** (re-enable: `ssh vito "systemctl --user enable --now hermec"`; linger already on). Victor hosts from desktop meanwhile. Desktop firewall: "Hermec Server" Allow-rule installed (he had denied the prompt once — that was the connection-refused bug).
- **DONE, PENDING MERGE + EAR TEST: Audio Quality & Devices plan** (`docs/superpowers/plans/2026-10-07-hermec-audio-quality.md`) on branch `worktree-hermec-audio` (b66c73b..bf7f28b, 9 commits; all task reviews + final whole-branch review clean; full suite + -race green). What changed: capture discards stale audio only after a genuine pause ≥100ms (chop fix; hard 80ms latency cap); playback moved oto→malgo with 20ms periods + per-sender jitter buffer (start 2 / target 3 / trim at 5 / Opus PLC budget 3 — buzzsaw fix; latency bounded at 100ms); decode moved off the audio callback's lock; `audio.ListDevices()` + `input_device`/`output_device` settings rows (cycle on click, persist immediately, unknown device → default + notice, never crash); chat input now sits under the chat pane (single-source geometry held). **Acceptance gate = Victor's repeat Mac↔desktop listening test — the plan closes only on his "clean audio" report.** End-to-end one-way latency estimate: 110–170ms.

## Process conventions (keep these)

- Subagent-driven development per plan: worktree via EnterWorktree, SDD ledger + pre-flight scan, Sonnet implementers / Haiku small reviews, fix rounds with scoped re-reviews, final whole-branch review on the most capable model, merge menu to Victor, controller tags releases on main only.
- Commit trailer everywhere: `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.
- cgo env on this machine: PATH needs `C:\msys64\ucrt64\bin` (user PATH has it; shells opened before 2026-10-07 don't), CGO_ENABLED=1; `-race` works locally now. Pure-Go server cross-compiles to Pi.
- Victor is in vibe-wise learning mode: he owns design decisions; brainstorm checkpoints before architectural work; teach inner workings junior-style on request. State lives in `.vibe-wise/`.

## Next-session pipeline (Victor's queue, in his priority order — reordered 2026-10-07: screenshare before the trust plans)

(Standing gate: the audio round closes only on Victor's listening test, see above.)

1. **Owner real-mouse checklist for the title bar:** drag, snap, Win+arrows, edge/corner resize, double-click maximize, buttons, maximized layout, 150% DPI. Also the Round A eyeball checks (four presets, glow strength, curvature subtlety).
2. **Round B brainstorm (one spec, decompose as needed):**
   - (a) stay connected across servers (concurrent sessions; switching servers keeps the current call);
   - (b) rail "you are here" bust (top half of a stick figure) on the server where you're in voice;
   - (c) right-side member roster of everyone on the server (needs server-wide presence; protocol change);
   - (d) server names: the server sends its configured `server_name` on connect, and creating/adding a server asks for a name. This fixes the "two lo tiles" confusion (hermec://local "local" + a stale ws://localhost:7697/, both initials "lo");
   - (e) remove/rename saved servers (right-click menu, from the old UI/UX batch);
   - (f) in-app pixel-art editor for a server icon.
3. **Remaining old UI/UX batch items:** resizable panels, "user X is typing…", right-click context menus.
4. **Idle CPU:** redraw-on-demand / idle frame cap (the profile shows ~93% in Present).
5. **Screen sharing + webcam** (Victor-prioritized, unchanged phasing), then Plans A/B/C (unchanged, below).
   Screenshare detail: Phasing agreed in summary form: (i) SFU video support — codec-agnostic RTP fanout mostly exists; the NEW work is keyframe handling (relay RTCP PLI from new subscribers to the publisher) + bandwidth accounting; integration-testable with synthetic frames. (ii) Windows capture (DXGI duplication) + VP8 encode via libvpx (second cgo dep — MSYS2 + CI packages + the DLL walk handles distribution) with v1 caps ~15fps/~2Mbps (host relay cost is ~60× voice — the kbps log matters). (iii) Receive: VP8 decode → Ebiten texture in the GUI's reserved video pane, click-to-enlarge, CRT shader applies free. (iv) Webcam = same pipeline, camera source, call-bar toggle; macOS/Linux capture are follow-ups. Risks: libvpx plumbing (same dance as opus), sharer-side encode CPU (measure + cap), PLI = the SFU's first codec-aware logic.
   - **Plan A — trust layer (adopted design):** server identity keypair; founder-signed append-only membership/config chain (permissioned sigchain — "blockchain minus consensus"); unique signed invite links (revocable, invite-tree); bans/roles as signed ops. SUBSUMES the old moderation plan.
   - **Plan B — member hosting:** authorized-hosts list in the chain; any trusted member can host; discovery by server fingerprint; split-brain rule (discovery-first + deterministic priority).
   - **Plan C — synced encrypted history:** gossip append-only log, group key rotated on ban (reopens E2EE as at-rest first).
6. **Deferred minors from Round A** (full list in the SDD ledger, `.superpowers/sdd/2026-10-11-hermec-visual-refresh/progress.md`, triaged by the final review; still open):
   - Theme/dev reload: `Presets()` re-parses embedded TOML per call (cache it); `-theme` file edits apply after F2 moved to a preset, and a preset edited while inactive is lost on cycle; dev-reload error in `st.Status` is not cleared after a later successful reload.
   - CRT/render: with Curvature=0 the corner mask still darkens the outer pixel ring; glow is 3 full-screen passes (first place to look for idle CPU); `Time` float32 loses precision after hours; `Barrel` cursor sub-pixel offset; the GUI goroutine launch will not work on macOS (main-thread requirement).
   - Animation/panels: per-tick `Rows()` + string-key allocations in `anim.go` and O(rows^2) pruning; muted occupant glow cuts instead of fading; panel fade over-draw slop; settings name row shows a pointer, not a text cursor.
   - Quality/RTT/health: unsolicited pong can inject a huge sample (clamp to timeout); `Health.Jitter` not decayed when not OK (gate display on OK); `Senders` allocates under the mixer lock; quality panel does per-frame work even when closed.
   - Title bar: hover can stick on a bar button after the mouse leaves the window; brief native-caption flash at startup.
   - Tests missing: connect-form hits, banner click, rowHL easing, addBarHits clipping, pinger interval seam, jitter-reset/TryLock paths.
   - Icon: `TestICO` checks only header/width and does not compare committed assets with the generator.
7. Backlog beyond: auto-connect-on-launch; GUI polish/identity trust (avatars, key pinning); direct-P2P opt-in; macOS packaging; quickstart paragraph on node-sharing vs tailnet invites; RenderKey extraction to ui/state; hardening list in PLAN.md roadmap item 3 (correlation IDs first).
8. Audio deferred minors (from the audio plan's reviews, none blocking): mid-call device unplug is silent one-way audio until leave (wants a malgo stop-callback/watchdog); jitter depth can settle at 4-5 frames after a hiccup with no decay back to 3 (slow-decay drop would recover 40ms); `resolveDevices`/notice-to-Status path untested (needs a seam); long device names overflow the settings row (no clip); CJK device names render blank (font lacks glyphs); a TryLock-miss PLC slot burns budget and plays silence (rare, bounded); add a ±25-30ms harsh-jitter test variant; no sequence numbers so a truly lost frame splices undetected (note for loss signalling); startup with a saved device does a synchronous enumeration before the window opens.

## Open decisions / gotchas

- Repo public flip = Victor's action; until then update banner can't fire (404 on unauthenticated API).
- Tailscale for friends: recommend NODE SHARING of the hosting machine (not tailnet invites) — friends see one machine only.
- Chat history is deliberately not persisted anywhere yet (design round pending; Plan C covers the distributed version).
- Two clients on ONE machine with the same key silence each other by design (self-echo filter keys on fingerprint) — second client needs `-key <other file>`.
- No AEC: headphones mandatory in calls.
- **Measurement runs must use a separate config dir.** The Task 10 agent's runs touched the owner's real `%APPDATA%\hermec` (`servers.toml` last_seen), and its cleanup closed the owner's running instance. Add a `-config-dir`-style flag or an env var before the next automated GUI run (backlog item).
- **Release exe is console-subsystem**, so double-clicking shows a terminal window behind the app. Consider `-H windowsgui` plus file logging (backlog item).
- Commit trailer for the visual-refresh round was `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
