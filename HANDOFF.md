# Hermec — Session Handoff

> Living brief for picking up work in a fresh Claude session. Update at every milestone.
> Last updated: 2026-10-07 (audio-quality plan implemented + reviewed; awaiting merge decision and Victor's listening test).

## Where things stand

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

1. **Finish/verify the audio round** if not closed (his listening test is the gate).
2. **UI/UX batch:** resizable panels; "user X is typing…" indicator; right-click context menu on channels/voice/users; right-side member roster showing everyone signed into the server (not just voice occupants — needs server-wide member presence, distinct from today's channel presence); multi-server: switching servers in the rail should NOT disconnect the current room (concurrent connections, Discord-like).
3. **Screen sharing + webcam** (Victor-prioritized). Phasing agreed in summary form: (i) SFU video support — codec-agnostic RTP fanout mostly exists; the NEW work is keyframe handling (relay RTCP PLI from new subscribers to the publisher) + bandwidth accounting; integration-testable with synthetic frames. (ii) Windows capture (DXGI duplication) + VP8 encode via libvpx (second cgo dep — MSYS2 + CI packages + the DLL walk handles distribution) with v1 caps ~15fps/~2Mbps (host relay cost is ~60× voice — the kbps log matters). (iii) Receive: VP8 decode → Ebiten texture in the GUI's reserved video pane, click-to-enlarge, CRT shader applies free. (iv) Webcam = same pipeline, camera source, call-bar toggle; macOS/Linux capture are follow-ups. Risks: libvpx plumbing (same dance as opus), sharer-side encode CPU (measure + cap), PLI = the SFU's first codec-aware logic.
4. **Plan A — trust layer (adopted design):** server identity keypair; founder-signed append-only membership/config chain (permissioned sigchain — "blockchain minus consensus"); unique signed invite links (revocable, invite-tree); bans/roles as signed ops. SUBSUMES the old moderation plan.
5. **Plan B — member hosting:** authorized-hosts list in the chain; any trusted member can host; discovery by server fingerprint; split-brain rule (discovery-first + deterministic priority).
6. **Plan C — synced encrypted history:** gossip append-only log, group key rotated on ban (reopens E2EE as at-rest first).
7. Backlog beyond: auto-connect-on-launch; GUI polish/identity trust (avatars, key pinning); direct-P2P opt-in; macOS packaging; quickstart paragraph on node-sharing vs tailnet invites; RenderKey extraction to ui/state; hardening list in PLAN.md roadmap item 3 (correlation IDs first).
8. Audio deferred minors (from the audio plan's reviews, none blocking): mid-call device unplug is silent one-way audio until leave (wants a malgo stop-callback/watchdog); jitter depth can settle at 4-5 frames after a hiccup with no decay back to 3 (slow-decay drop would recover 40ms); `resolveDevices`/notice-to-Status path untested (needs a seam); long device names overflow the settings row (no clip); CJK device names render blank (font lacks glyphs); a TryLock-miss PLC slot burns budget and plays silence (rare, bounded); add a ±25-30ms harsh-jitter test variant; no sequence numbers so a truly lost frame splices undetected (note for loss signalling); startup with a saved device does a synchronous enumeration before the window opens.

## Open decisions / gotchas

- Repo public flip = Victor's action; until then update banner can't fire (404 on unauthenticated API).
- Tailscale for friends: recommend NODE SHARING of the hosting machine (not tailnet invites) — friends see one machine only.
- Chat history is deliberately not persisted anywhere yet (design round pending; Plan C covers the distributed version).
- Two clients on ONE machine with the same key silence each other by design (self-echo filter keys on fingerprint) — second client needs `-key <other file>`.
- No AEC: headphones mandatory in calls.
