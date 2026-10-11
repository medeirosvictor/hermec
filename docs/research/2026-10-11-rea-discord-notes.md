# rea + Discord: what to borrow for Hermec

Notes from evaluating [morluto/rea](https://github.com/morluto/rea) on 2026-10-11, kept for later planning.

## What rea is

An MCP server and CLI (Node 22+, MIT) that lets a coding agent reverse engineer software and report evidence. Targets include Electron/JS apps (modules, IPC, native add-ons), native binaries (via Ghidra, IDA or Hopper), HAR/mitmproxy captures, APKs, .NET and firmware. Setup: `npx rea-agents setup` registers it with Claude Code. Runs locally; it is a dev tool and never enters Hermec's stack. Evaluated from the README only, not yet installed or run.

## Fit with Discord

- Discord desktop is Electron, so rea's JS analysis works on the UI layer.
- The interesting parts are native C++ modules: `discord_voice` (a WebRTC fork), Krisp noise suppression, and the global-hotkey utilities. Analysing those needs Ghidra; slow going.
- **Legal:** Discord's terms forbid reverse engineering the client, and Hermec is public open source. Study behaviour, then write it from scratch. Never copy Discord's code, constants or assets into the repo.

## Most of what we want needs no reverse engineering

| Feature | Source to use instead |
|---|---|
| Echo cancellation, gain control, noise suppression | `webrtc-audio-processing` (BSD, in MSYS2), the same AEC3 Discord uses; RNNoise (BSD) for noise suppression. Krisp is proprietary: skip it. |
| End-to-end encryption for voice/video | Discord's DAVE protocol: published whitepaper and MIT-licensed `libdave`, built on MLS. Matches the MLS-style slots reserved in spec §8; read before Plans A and C. |
| Typing indicator, presence, member list | Discord's public gateway docs (typing events expire after ~10s; server-wide member presence is separate from channel presence). |
| Permissions | Discord's documented permission bitfield with per-channel allow/deny overrides; a model for extending `core/roles`. |
| Bot commands | Discord's slash-command/interactions model (registered commands with typed options); fits the integration-first goal. |
| Screenshare SFU | Discord engineering blog posts on their WebRTC media servers; our phasing already covers PLI forwarding and bandwidth caps. |

## Ranked feature ideas for Hermec

1. Echo cancellation + noise suppression (removes "headphones mandatory"). Echo cancellation needs the playback signal as its reference, which the malgo playback path can provide.
2. Push-to-talk (global hotkey) and voice-activity threshold.
3. Per-user volume and local mute, through the planned right-click menu.
4. DAVE-style end-to-end encryption when the trust plans land.
5. Typing indicator, member roster (already queued).
6. Permission overrides per channel.
7. Slash-command style bot API.
8. Screenshare quality presets.
9. Connection-quality indicator in the call bar (started 2026-10-11).

Skip: Nitro-style extras, discovery/social graph, game/activity detection, in-game overlay injection, Krisp.

## Where rea would actually earn its keep

Small undocumented client behaviours, studied on a Discord install we are allowed to analyse:

- how the automatic input-sensitivity threshold adapts
- the audio-processing defaults (which of AEC/AGC/NS are on, and when)
- the push-to-talk release delay
- which encoder settings the screenshare quality presets map to

Have the agent describe the behaviour, then implement it fresh in Go.
