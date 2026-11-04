# Using Hermec

A tour of the desktop app. To install and join a server first, see
[Getting started](getting-started.md).

## How it works, briefly

The app opens one long-lived WebSocket connection to the server (port 7697 by
default). Before anything else the server sends a random number and the app
signs it with your key file, which proves you are the same person as last time
without any account (see [Security](security.md)). The same connection then
carries everything that isn't sound: chat, who is in which channel, who is
muted.

Voice uses that connection only to negotiate (standard WebRTC). Your microphone
audio then travels to the server over one UDP port, and the server copies it to
everyone else in the call (an SFU, or relay). Nobody connects directly to
anybody else, so friends see only the host's IP address.

## Servers and channels

Each icon in the rail on the left is a server. Every server you have opened
stays connected, and switching servers keeps you in your voice call.

- **Click a tile** to switch servers. Hover it to see its full name.
- **Right-click a tile:** **Rename** (a nickname only you see; empty resets to
  the server's name), **Disconnect** (when you have a session to that server;
  ends your call too if it is the call's server), **Leave Server** (asks first;
  removes the tile and ends a call on it). **Esc** or a click elsewhere closes
  the menu.
- **Click the "+" tile** to add a server: **Create my own**, **Join a server**
  or **Dedicated server**, or paste an invite link into the **invite link** line
  below them (see [Invite links](#invite-links)).
- **Tab** or **Ctrl+Up/Down** switch channel.
- The **Members** button (top right of the chat) shows or hides the member
  list. The list hides itself in narrow windows; your choice is saved in
  `settings.toml`.
- On connect, the **DISCOVERED** list shows LAN and Tailscale servers;
  **Ctrl+R** refreshes it.
- Hover a server or person to see their fingerprint, the real identity (names
  are not verified).

### Invite links

An invite link is one line, `hermec://join?v=1&addr=<ws or wss address>&id=<server
ID>&name=<name>`, that carries everything a friend needs.

- **Invite someone:** right-click the server's tile and choose **Copy invite
  link**. It works on the server you host from the app (using your Tailscale
  address if you have one, otherwise your LAN address) and on dedicated `wss://`
  servers you have joined, which are shared with their pinned ID. The link
  contains no password. The app keeps only a derived password key, never the
  password, so there is no "copy with password" item; a server's password is
  shared separately.
- **Join from a link:** click **+**, click the **invite link** line, press
  **Ctrl+V** (Cmd+V on macOS). `ws://` links join with the server ID pinned, so
  a different server at that address is refused. `wss://` links open the
  Dedicated form prefilled; press **Validate / Connect**. On the Dedicated form
  **Paste invite** reads the clipboard the same way.
- **Links with a password:** a link may also hold `&pw=<password>` (only for
  `wss://`; a `ws://` link with a password is refused because it would cross
  the network in cleartext). Treat such a link as a secret. The app never logs
  a link or its password and never writes the password to `servers.toml`.
- The URL scheme is not registered with the operating system yet, so clicking a
  `hermec://` link in a chat app does nothing; copy it and paste it in Hermec.

## Chat and history

**Enter** sends a message. Text channels keep their messages on the server (30
days and 8 MB by default; see
[history caps](server-configuration.md#chat-history)), so when you join a
channel you see the newest 100 messages you missed. **PgUp/PgDn** or the mouse
wheel scroll the chat; at the top the next older page loads (the app keeps up
to 2000 messages per channel).

**Ctrl+V** (Cmd+V on macOS) pastes into any text field; **Left**, **Right**,
**Home**, **End** and **Delete** move and edit at the caret. On Linux, copy and
paste need `xclip`, `xsel` or `wl-clipboard`.

## Voice

Click a voice channel in the pane to join it (this moves your call if you are in
another one). Clicking the channel you are already in opens the call view.
Participants, occupancy, mute icons and speaking indicators live in the channel
pane under each voice channel; empty voice channels show only in the list.

- **Call bar** (bottom): channel name, elapsed time, mute button (**Ctrl+M**),
  camera and share buttons, leave button, and your mic level.
- **"Voice connected" panel** (bottom of the channel column): the channel and
  server, your ping, and buttons to disconnect and share. When the host's name
  differs from the server's name it shows "hosted by `<name>`".
- **Signal bars** (status line, or the call bar in a call): click to show
  connection quality (ping, jitter, per-participant concealment); **Esc** or a
  second click closes it.
- Leave the call with the call bar, the call view, or the "voice connected"
  panel.

Hermec does not perform acoustic echo cancellation: **use headphones**.

### Per-person volume and mic gain

Right-click a person (member list, a voice channel's occupant row, or a call
tile) to set how loud they are for you: 0 to 200% in eight steps, current one
marked. It applies at once, mid-call, and is saved per person in
`settings.toml` (100% is not stored). The founder also sees the host controls
in that menu (see [Hosting](hosting.md#hosting-from-the-app)).

### Moderation

If you are the founder or hold the `manage` permission (the builtin `admin`
role does), the same person menu also shows **Make admin** or **Remove admin**
and **Ban…**. Ban asks first, then the server disconnects that person at once
and refuses them until they are unbanned; their past messages stay. You cannot
act on yourself or on the founder, and nobody else sees these items. There is
no separate kick: a ban is the only removal, and **Unban** undoes it.

Right-click the server's tile and choose **Banned members…** (managers only)
for a list of banned people, by last-known name when the app has seen one and
by fingerprint otherwise. Pick a row to unban. The status line reports each
action ("banned Sam", or "couldn't ban Sam: you don't have permission"). The
server checks every request, so hiding the items is a convenience, not the
security.

The Settings row `mic gain` cycles your own microphone between 50% and 200%;
your level meter and what others hear both reflect it. Boosted audio passes
through a soft limiter, so loud peaks round off instead of crackling.

### Push-to-talk

Settings row `voice mode` switches between `open mic` (default) and
`push to talk`; `ptt key` cycles Right Ctrl (default), grave (`` ` ``), Caps
Lock, F13 and Mouse 4. In push-to-talk your microphone transmits only while the
key is held (plus a 150 ms tail so word endings are not clipped); audio from
before the press is never sent. The call bar and the "voice connected" panel
show `PTT` while idle and `TALK` (bright) while transmitting, and your level
meter stays flat when not transmitting. Others see you speaking only while you
transmit.

- **Mute still wins:** Ctrl+M or the mute button stops all transmission, key
  held or not. The bar then shows `PTT muted`.
- **Local gate:** push-to-talk is applied on your machine and sends nothing to
  the server, so others do not see a mute icon while you are idle.
- **Windows:** the key works system-wide, even while another app has focus. It
  reads only the state of the one chosen key (no keyboard hook, nothing is
  recorded). The grave key is the one left of `1` on a US layout.
- **Linux and macOS:** the key works only while the Hermec window has focus;
  a system-wide hotkey is not implemented yet.
- While Hermec is focused and a text field is active (chat, settings name, add
  page), the grave key is ignored so typing never transmits. The other keys do
  not type, so they work while you chat.

### Devices and auto-rejoin

The `input` and `output` rows in Settings take effect immediately during a
call (a brief gap while the device reopens; if the new device fails, the old
one stays and a status says so).

If your connection to a server drops during a call and later comes back, Hermec
rejoins the same voice channel, muted as you were, with camera and screen share
off, and shows "rejoined voice". It does not rejoin if you left or
disconnected yourself, if the channel is gone, or if the server refused you.
If the server's host changes (see [failover](hosting.md#friend-hosting-standby-and-failover)),
the call moves over by itself.

## Call view

The call view shows participant tiles, cameras, mute and speaking state, and a
selected-screen presentation area. Microphone, camera and screen share have
independent controls and can all run at once. Audio is the highest semantic
priority, then the selected screen, then cameras; that ordering is metadata only
and does not adapt bitrate or packet scheduling.

## Camera

On Windows, join a voice channel and click **camera** in the call bar or call
view. The first use may prompt for camera permission. Choose the saved camera in
Settings; camera capture never starts automatically. The local tile is a local
preview, while other participants receive VP8 at 640x360, 15 fps and about 300
kbps. Up to ten cameras can be active in one room. Leaving the call, disabling
the camera, closing the app, or losing the device stops capture. Camera
publishing from macOS and Linux is not implemented yet, though those clients
can view cameras.

## Screen share

**Share your screen** (Windows only for now):

1. Join a voice channel.
2. Click **share** on the call bar (or the "voice connected" panel).
3. Pick a monitor from the list, like `screen 1 (2560×1440)`.
4. The button now says **stop share**. Click it to stop.

Leaving the voice call stops your share too. To see what you are sharing, click
your own **LIVE** tag; the preview is drawn on your computer, so it costs no
bandwidth. While you share, the strip on the video has **stop sharing** and
**change screen**; change screen swaps the monitor without ending the share, and
people watching keep watching (the picture is letterboxed if the new screen has
a different shape).

**Watch someone's screen** (Windows, macOS and Linux):

1. Look at the voice channel list. People who share have a red **LIVE** tag.
2. Click **LIVE**. The video replaces the chat.
3. Move the mouse over the video to show a strip with **fullscreen** and
   **close**.
4. **Esc** goes back to chat; in fullscreen, the first **Esc** leaves
   fullscreen and the second goes back to chat.
5. **F11** or a **double-click** on the video toggles fullscreen.

You can watch one screen at a time. The video is drawn clean, without the CRT
effects, so text stays readable.

**Quality.** In Settings, `share quality` switches between `720p` (about 1.5
Mbps at 15 frames a second) and `1080p` (about 3 Mbps).

**Bandwidth.** The sharer uploads one stream. The host uploads one copy for each
person watching (about 1.5 Mbps each at 720p), and only to people who are
watching it. A host with a slow upload should keep the number of watchers small.

**Ports.** Voice and video use one UDP port, the same number as the server port
(7697); see [ports](server-configuration.md#ports-and-firewall) and the
[Tailscale grant](security.md#tailscale-grants).

**Not yet:** sound from the shared screen, sharing a single window, and sharing
the screen from macOS or Linux.

## Settings

Click the **gear tile** or press **F10** (works when the rail is hidden). The
page has: name, palette, font, scanlines, curvature, input/output audio device,
mic gain, voice mode and push-to-talk key, text size as a percentage, share quality (720p or 1080p), logs (open
folder), update status, and a help section. Choices, including F1/F2/F3,
persist to `<user config dir>/hermec/settings.toml`; the `-name` and `-theme`
flags override it.

### Themes

- **F1** toggles the CRT effect.
- **F3** toggles CRT screen curvature (persisted; the rest of the CRT effect
  stays on).
- **F2** cycles color palettes: amber, green, blue, parchment (light) and slate
  (modern dark).

New installs start on the green palette with scanlines and curvature on, in
JetBrains Mono. The font row cycles VT323, JetBrains Mono, IBM Plex Mono and
Geist Mono (all monospace, under the SIL Open Font License 1.1, licences in
`ui/theme/`). To customise the look further, pass a theme file with `-theme`
(every key is in [example.theme.toml](../example.theme.toml)):

```sh
hermec -theme ~/.config/hermec/myTheme.toml
```

## Keyboard and mouse reference

| Input | Action |
|---|---|
| **Enter** | Connect to a server, or send a message |
| **Ctrl+V** (Cmd+V on macOS) | Paste |
| **Tab**, **Ctrl+Up/Down** | Switch channel |
| **PgUp/PgDn**, mouse wheel | Scroll chat (loads older history at the top) |
| **F1** / **F2** / **F3** | CRT effect / next palette / screen curvature |
| **F10** | Settings |
| **F11**, double-click on video | Toggle fullscreen when watching a screen |
| **Ctrl+M** | Mute or unmute voice |
| **Ctrl+R** | Refresh discovered servers (connect screen) |
| **Ctrl+U** | Update now / restart to update, when the banner offers it |
| **Esc** | Close a menu or panel; leave fullscreen; back to chat from a screen |
| Right-click a tile or person | Menus described above |

**Title bar (Windows):** a Discord-style app bar replaces the default frame.
Drag it to move the window (double-click to maximize or restore), use the
buttons on the right to minimize, maximize and close, and drag the window edges
and corners to resize.

## In-app updates

On startup, release builds ask GitHub's public releases API whether a newer
version exists (development builds never make this request or download
anything). It sends no identifiers beyond a normal HTTPS request.

On Windows and Linux the app then downloads the new release in the background
and verifies it: the release's `SHA256SUMS.txt` must carry a valid Ed25519
signature for that release's tag (`SHA256SUMS.txt.sig`, key embedded in the
app), the tag must be newer than the running version, and the download's SHA-256
must match it, otherwise nothing is installed. Only one running copy downloads
or installs at a time. See [Release signing](release-signing.md).

When it is ready, the banner and the "update" row in Settings say "restart to
update". Clicking (or **Ctrl+U**) swaps the new binary in and restarts into it;
the old one is kept as `hermec.exe.old` / `hermec.old` until the new build has
started, then removed. If the download or check fails, or the install folder is
not writable, the banner links to the Releases page instead. If a server refuses
you with a version mismatch and its build is newer than yours, the banner offers
"update now" (**Ctrl+U**) to fetch it.

macOS builds never update themselves: the banner links to the Releases page and
you rebuild with `scripts/build-mac.sh`. Dedicated servers are updated by hand
with the installed `update.sh`, which verifies the signed release by default
(see [Dedicated hosting](dedicated-hosting.md#updating-a-signed-release)).
