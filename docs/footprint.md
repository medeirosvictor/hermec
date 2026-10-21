# Footprint

How much Hermec costs to run. Each section says when it was measured.

## Binary sizes (measured 2026-10-21, v0.5.1 source)

Windows x64, `go build -trimpath -ldflags "-s -w"`.

| Binary | Size |
|---|---|
| `hermec-server` (pure Go, CGO off) | 12.0 MB |
| `hermec` (GUI client, CGO on for audio) | 27.2 MB |

## Server (measured 2026-10-07, voice only, older build)

Windows 11, 2 synthetic clients, 20 ms frames, about 40 kbps per stream.
RAM is Windows working set.

| Scenario | RAM | CPU |
|---|---|---|
| Idle, no calls | 56 MB | about 0% |
| Relaying a live 2-client voice call | 65 MB | 0.7% of one core |

A call costs under 1% of one core, so a Raspberry Pi is plenty for voice. Video
costs far more: the host uploads one copy of a share per watcher (about
1.5 Mbps each at 720p). This was not measured again after screen share landed.

## GUI client (measured 2026-10-11, older build)

`hermec -local`, main scene, idle, 60 s, as a share of one core:

| Build | CPU | RAM |
|---|---|---|
| CRT on | about 17% | 73 MB |
| CRT off | about 17% | 72 MB |

- The CRT effect is not the idle cost. CRT on and off cost the same.
- About 93% of the samples are in `IDXGISwapChain.Present`, which is the 60 fps
  loop waiting on vsync. The app's own drawing is about 4% of a core.
- Numbers swing by about 3 points with other programs running.
- The lever for lower idle CPU is drawing only when something changed, or
  capping idle frames. Faster draw code would not help. Keep in mind that the
  CRT scan bar animates all the time, so with CRT on the cap has to be a modest
  rate, not zero.

Re-measure before treating these as a baseline. Method: build to a stable path,
run `-local` against a separate config dir, wait 8 s, then compare
`Process.TotalProcessorTime` over 60 s.
