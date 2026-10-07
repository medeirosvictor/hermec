# Hermec — Session Handoff

> Living brief for picking up work in a fresh Claude session. Update at every milestone.
> Last updated: 2026-10-07 (audio-quality round in progress).

## Where things stand

- **Shipped on main, all reviewed, CI green:** foundation (chat server + headless client) → GUI (Ebiten, amber CRT, VT323, servers rail, settings) → voice (SFU relay, call bar, occupancy) → discovery (DISCOVERED list: LAN + tailnet + saved) → release & update (**v0.1.0 released**; update banner Ctrl+U; GUI idle CPU 34%→6.5%).
- **v0.1.0:** https://github.com/medeirosvictor/hermec/releases/tag/v0.1.0 — NOTE: repo was PRIVATE at last check; Victor said he'll flip it public (blocks friends' downloads + the update banner until then).
- **Deployment state:** primos config = `primos.server.toml` (repo root, untracked). vitoserver has the server installed + systemd user service, currently **disabled/parked** (re-enable: `ssh vito "systemctl --user enable --now hermec"`; linger already on). Victor hosts from desktop meanwhile. Desktop firewall: "Hermec Server" Allow-rule installed (he had denied the prompt once — that was the connection-refused bug).
- **IN PROGRESS: Audio Quality & Devices plan** (`docs/superpowers/plans/2026-10-07-hermec-audio-quality.md`): fixes the first real call's defects — chopped speech (capture ring discards under steady jitter) + buzzsaw (playback underrun/re-prime cycle) — plus input/output device selection (playback moves oto→malgo) and the input-under-chat UI tweak. **Acceptance gate = Victor's repeat Mac↔desktop listening test.** Check the plan's SDD ledger in the worktree for exact task state if the session died mid-plan.

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

## Open decisions / gotchas

- Repo public flip = Victor's action; until then update banner can't fire (404 on unauthenticated API).
- Tailscale for friends: recommend NODE SHARING of the hosting machine (not tailnet invites) — friends see one machine only.
- Chat history is deliberately not persisted anywhere yet (design round pending; Plan C covers the distributed version).
- Two clients on ONE machine with the same key silence each other by design (self-echo filter keys on fingerprint) — second client needs `-key <other file>`.
- No AEC: headphones mandatory in calls.
