# Noise-suppression research

Historical hosting/UI discussion was superseded by the approved
[hosting-readiness design](../superpowers/specs/2026-10-26-hosting-readiness-design.md)
and its active [dedicated-hosting plan](../superpowers/plans/2026-10-28-dedicated-hosting.md).
Current operator instructions are in [the dedicated guide](../dedicated-hosting.md).
This note retains durable noise-suppression research only; it does not claim an
implementation or benchmark.

## Candidates

| Candidate | License/evidence to verify | Evaluation note |
| --- | --- | --- |
| RNNoise | Xiph upstream states BSD terms; verify exact code/model revision. | First candidate; compare portable embedded/static routes. |
| `VoiceBlender/rnnoise-go` | Upstream states BSD-3-Clause, pure Go, embedded model, 10 ms/48 kHz. | Promising first probe; verify provenance, quality and performance. |
| SpeexDSP | Official preprocessor offers denoising, gain control and VAD. | Traditional baseline; verify revision license, artifacts and CPU. |
| DeepFilterNet | Repository code is MIT/Apache-2.0; model terms require separate review. | Consider only if RNNoise is inadequate; measure Rust/runtime complexity. |

Sources: [RNNoise](https://github.com/xiph/rnnoise),
[terms](https://raw.githubusercontent.com/xiph/rnnoise/main/COPYING),
[rnnoise-go](https://github.com/VoiceBlender/rnnoise-go),
[Speex preprocessor](https://www.speex.org/docs/manual/speex-manual/node7.html),
and [DeepFilterNet](https://github.com/Rikorose/DeepFilterNet). The
[Shiguredo WASM wrapper](https://github.com/shiguredo/rnnoise-wasm) is a
browser/JavaScript integration, not a ready Go binding.

## Evaluation gate

HERMEC captures 48 kHz mono PCM and encodes 20 ms frames. A trial should process
PCM on the encoder worker before Opus, not in the device callback, with explicit
frame adaptation and measured buffering. Any feature is default-off,
switchable during a call, preserves mute/gain, keeps processor state on that
worker, visibly falls back on initialization failure, bundles notices/assets,
and sends no audio to a cloud service or raw-audio recorder.

Compare off/on conversation, fan, keyboard and competing speech for clipping
and coloration. Measure CPU, memory, processing time, added latency and binary
size across supported releases. Exercise toggle, mute, device changes,
leave/rejoin and failover. Do not make quality or performance claims until this
passes. Echo cancellation needs playback-reference timing and remains separate;
headphones remain recommended.
