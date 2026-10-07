# Footprint snapshot

Dated measurements of Hermec's resource usage, taken so future changes can be
compared against a baseline. Re-measure after each major plan and append a new
section; don't overwrite history.

## 2026-10-07 — after the discovery plan (main @ db700e4)

Measured on Windows 11, AMD RX 6900 XT rig, Go 1.27, CGO on for the client
(opus/miniaudio), off for the server. RAM figures are Windows working set.

### Binary sizes

| Binary | Stock | `-ldflags "-s -w"` |
|---|---|---|
| hermec-server (pure Go, CGO off) | 17.1 MB | 11.9 MB |
| hermec (GUI client, CGO on) | 43.2 MB | 21.7 MB |

### Runtime

| Scenario | RAM | CPU |
|---|---|---|
| Server, idle (loopback, no calls) | 56 MB | ~0% |
| Server, relaying a live 2-client voice call | 65 MB | **0.7% of one core** |
| GUI client, connect screen | 70 MB | — |
| GUI client, connected (-local: includes the embedded server in-process) | 87 MB | ~28% of one core |

2-client synthetic voice call: 20ms frames, ~40kbps per stream, sampled over
15s. Server threads: 7 idle → 17 in-call.

### Reading the numbers

- The server comfortably clears the Raspberry Pi bar: a live call costs under
  1% of one core and ~65 MB. Dozens of concurrent voice streams are plausible
  on small hardware.
- Reference point: Electron-based chat apps typically idle at 300–500 MB and
  a full browser engine. The Hermec GUI sits at 70–87 MB including Go runtime,
  WebRTC stack, and GPU textures.
- **Known hot spot:** the GUI burns ~28% of one core while sitting idle in a
  chat — the Ebiten render loop redraws at 60fps and relays out the scrollback
  layout every tick (a known deferred item: cache the layout on
  (message count, last message, cols)) plus the CRT shader pass. This is the
  top performance follow-up; target is single-digit % idle.
- The connected GUI figure includes the `-local` embedded server; a client
  connected to a remote server will sit lower.
