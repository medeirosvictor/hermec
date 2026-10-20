# HERMEC: Screen share (+ session log, one media port) — design

Date: 2026-10-19. Status: design approved in chat by Victor (order: log file first, then the fixed media port, then screen share; webcam is a later round).
Context: first real friend call worked (Canada host, two friends incl. Brazil, Tailscale node sharing). Friends asked for screen share; debugging needs logs; README "How safe is this?" gap 1 needs a fixed media port.

## 1. Session log file

- One file per app run: `<user config dir>/hermec/logs/hermec-YYYY-MM-DD_HH-MM-SS.log`. Keep the newest 10; delete older ones at startup.
- Always on. Everything `log.Printf` and every `g.logf` breadcrumb goes to the file. The console still shows breadcrumbs only with `-v` (unconditional log lines keep going to the console as today).
- First lines: version, OS/arch, Go version, audio input/output device names.
- The in-app hosted server logs into the same file with a `[server] ` prefix.
- Call health: every 30 s while in a call, one line: ping, and per remote person jitter depth and concealed-frame count; while sharing or watching, video kbps and frames/s.
- Never logged: chat text, keys, passwords.
- Settings row `logs: open folder` opens the logs directory with the OS file browser (same mechanism as opening URLs).
- If the log directory can't be created, the app runs normally and logs to the console only.

## 2. One media port

- Server WebRTC media (voice now, video next) uses one UDP port through a Pion ICE UDP mux, by default the same port number as the server's listen port (7697 → UDP 7697). Config key `media_port` (0 = same as listen port). The existing `udp_port_min/max` keys keep working and take precedence when set (legacy ephemeral range).
- The in-app hosted server uses its own port for media.
- README "How safe is this?": gap 1 becomes a Tailscale policy recipe that allows shared users only TCP and UDP 7697:
  `{"src": ["autogroup:shared"], "dst": ["*"], "ip": ["tcp:7697", "udp:7697"]}` in `grants`, replacing the default allow-all for sharees.
- If the media port can't be bound (in use), the server fails to start with `media port <n> is in use` (same handling as the listen port today).

## 3. Screen share

### Behaviour (Discord-style)
- Anyone in a voice channel can share. Call bar gains **share**; clicking it opens a menu listing monitors (`screen 1 (2560×1440)`, …). Choosing one starts sharing; the button becomes **stop share**. Leaving voice stops sharing.
- Several people can share at once. Voice channel occupant rows show a red **LIVE** tag for sharers.
- Watching is opt-in: click **LIVE** to watch that person; the video replaces the chat area. **Esc** returns to chat. **Double-click** the video toggles window fullscreen. A viewer watches one stream at a time; clicking another LIVE switches.
- The video is drawn without CRT effects (no scanlines, curvature, glow) so text stays readable; it keeps the source aspect ratio (letterboxed).
- When the watched sharer stops or leaves, the view returns to chat with the status `<name> stopped sharing`.
- The mouse cursor is drawn into the captured frames.

### Quality and cost
- Default 720p (fit within 1280×720, aspect kept, even dimensions) at 15 fps, VP8 CBR 1500 kbps; keyframe every 5 s. Settings row `share quality: 720p / 1080p` (1080p = fit within 1920×1080, 3000 kbps).
- The server forwards a sharer's video only to people watching it. The sharer uploads one stream; the host uploads one copy per watcher.
- If encoding a frame takes longer than the frame interval, frames are skipped (never queued).

### Platforms
- Sharing: Windows in this round. On macOS/Linux the share button is shown dim with the tooltip `screen share from this OS is coming later`.
- Watching: Windows, macOS, Linux.

### Architecture
- **Codec:** VP8 via `github.com/thesyncim/govpx` (pure Go, BSD-3). Pion payloads/depayloads VP8 RTP. Both server and client MediaEngines register VP8 (PT 96, RTCP feedback `nack`, `nack pli`) next to Opus.
- **Gate:** before building on govpx, a benchmark on Victor's PC must show 720p encode ≤ 30 ms/frame average and decode ≤ 10 ms/frame on a desktop-like test image sequence. If it fails: try 1080p→720p at 10 fps, then `github.com/mendsec/vp8`; report before going further.
- **Capture (Windows):** pure Go via `golang.org/x/sys/windows` (already a dependency): enumerate monitors (EnumDisplayMonitors + GetMonitorInfo), capture with GDI BitBlt into a DIB section, draw the cursor (GetCursorInfo + DrawIconEx), convert BGRA → I420 with downscaling to the target size. Interface `Source{ Monitors() ([]Monitor, error); Capture(m Monitor, dst *Frame) error }` with a stub on other OSes. (DXGI desktop duplication is a later optimisation.)
- **Protocol (additive; old clients ignore unknown types):**
  - `VoiceMember` gains `sharing bool` (omitempty).
  - `share_start {}` / `share_stop {}` client→server: sets/clears the sender's sharing flag (only while in voice) and re-broadcasts `voice_state`.
  - `watch {fingerprint string}` client→server: the sender now watches that sharer (`""` = stop watching). Server validates the target is sharing in the same voice channel; otherwise replies with an error code `not_sharing`.
- **SFU:** video tracks are fanned out like audio but only to subscribers in the publisher's watcher set; changing the watcher set adds/removes the subscriber's track and renegotiates (server offers, as today). When a viewer starts watching, the server sends a PLI to the sharer; PLIs from viewers are relayed to the sharer, at most one per second per sharer. Server logs video kbps per channel with the existing stats line.
- **Client:** sharing adds a VP8 `TrackLocalStaticSample` (stream id `video-<fp>`); an encode loop takes captured frames at 15 fps; an incoming PLI (read from the sender's RTCP) forces the next frame to be a keyframe. Receiving: a video track's frames are depacketised, decoded off the game goroutine, and the newest decoded frame is handed to the UI (older undrawn frames are dropped).

## 4. Out of scope
Webcam (next-but-one round); window capture; sharing audio; macOS/Linux capture; simulcast/multiple qualities per stream; watching several streams at once; DXGI capture.

## 5. Testing
- Unit: log rotation (keep 10) and fallback when the dir is unwritable; I420 conversion and scaler (dimensions, aspect, even sizes); keyframe interval and PLI-forced keyframes; server watcher-set filtering and PLI rate limit; protocol validation (`watch` a non-sharer → `not_sharing`; share_start outside voice ignored).
- Integration: server + three clients; A shares synthetic frames, B watches and decodes a keyframe-first stream, C (not watching) receives no video; B joins late and still gets a keyframe within 1 s; A stops → B's view ends. Media flows over the single UDP port.
- Windows capture test: capturing monitor 1 yields a frame of the monitor's size (skipped when no display).
- Victor's real check: share to the Brazil friend over Tailscale, watch someone else share, read text on a shared screen, send each other a log file.
