# Hermec Round B.2: Server Names, Leave Server, Member List — Design

Date: 2026-10-16. Status: awaiting Victor's review.
Round B was decomposed into B.1 (sessions, merged at 338cc40), B.2 (this spec)
and B.3 (pixel-art icon editor).

## 1. Intent

**What Victor wants:** get Hermec to a state friends can install and use. B.2
removes the confusing parts of having several servers: tiles named by address
initials ("two lo tiles"), no way to remove a saved server, and no way to see
who is on a server without opening every channel.

**Decisions Victor made in the brainstorm:**

- Place things where Discord places them.
- The server's own name (`server_name` in its config) is what everyone sees.
  Only the host changes it, by editing the config. Remote admin renames wait
  for the signed changelog (Plan A).
- Right-click → **Rename** sets a nickname only you see.
- Right-click → **Leave Server**, with a confirm step, removes it from your list.
- An online **member list** on the right of the chat, grouped by role, with a
  toggle.
- **No server keys this round.** Server identity, key pinning and the
  "key changed" warning move to Plan A, where anyone can host. Tailscale
  already authenticates the machine behind an address.

**Proposed details Victor confirmed with the design:** role groups Admins /
Members / Bots (custom roles count as Members); a name tooltip on tile hover;
the display name as a header above the channel column; a "members" toggle at
the top right of the chat, saved in `settings.toml`; the list hides itself in
narrow windows; online members only.

**Success looks like:** two tiles for two servers show "Primos" and "Lab" on
hover and in the channel header, not "lo" and "lo". You rename "Lab" to
"Victor's lab" and only your app shows that. You leave a server you no longer
use and its tile is gone after a restart. With the member list open on Primos,
a friend connecting appears under Members within a second and disappears when
they quit.

**Out of scope:** server keys/pinning, admin renames over the network,
offline members, avatars, roster entries for voice state (the voice channel
list already shows that), B.3's icon editor.

## 2. Current shape (what changes)

- `proto.AuthOK` carries fingerprint, roles and channels. The server's
  `ServerName` is used only for discovery replies (`server/discovery.go`).
- `state.ServerEntry` has `URL`, `Label` (written as host:port, or "local"),
  `LastChannel`, `LastSeen`. `state.Initials` uses `Label`, else the host.
- `ui/menu.go` opens only for tiles with a session and holds one item,
  Disconnect.
- Presence exists per text channel (`presence`), plus server-wide
  `voice_state` sent to every logged-in connection (`Server.authed`). There
  is no server-wide member list.
- The main scene is rail | channel column | chat (`ui/main_scene.go`
  geometry). There is no header above the channel column.

## 3. Design

### 3.1 Protocol (protocol version stays 1)

Both changes are additive. Old clients ignore unknown JSON fields and unknown
message types (`client/client.go` read loop `default:`); new clients treat a
missing field or message as "old server".

```go
// AuthOK gains:
ServerName string `json:"server_name,omitempty"`

// New message type.
const TypeRoster = "roster"

// Roster lists every logged-in member of the server, sorted by name then
// fingerprint. Total counts all of them; Members may be truncated to
// MaxRosterMembers so the message stays under MaxMessageSize.
type Roster struct {
    Members []Member `json:"members"`
    Total   int      `json:"total"`
}

const MaxRosterMembers = 200
```

`Member` is reused as is (fingerprint, name, roles).

### 3.2 Server

- `authenticate` sets `ServerName: c.srv.cfg.ServerName` in `auth_ok`. Empty
  stays empty: the client chooses the fallback, not the server.
- Roster broadcast hooks into the existing `authed` set under `chMu`:
  - `markAuthed(c)`: after adding `c`, broadcast the new roster to every
    authed connection (including `c`, which gets it after `auth_ok` and the
    voice snapshot).
  - `leaveAll(c)`: after `delete(s.authed, c)`, broadcast the roster to the
    rest.
  - A `rosterLocked()` helper builds and encodes it, like `presenceLocked`.
    Slow connections are collected and closed with `dropSlow`, as today.
- Display names are whatever the client sent in `auth`, as for presence.
  The existing rate limits and message-size cap apply.

### 3.3 Client library

- `client.Client` stores the `auth_ok` server name: `func (c *Client)
  ServerName() string`.
- `Event` gains `Roster *proto.Roster`; the read loop decodes `roster` like
  `presence`.

### 3.4 Saved servers (`ui/state/servers.go`)

`ServerEntry` gains two fields; `Label` stays as the address fallback written
today, so old files load unchanged.

```go
Name string `toml:"name,omitempty"` // last server_name the server sent
Nick string `toml:"nick,omitempty"` // the user's own nickname; "" = none
```

- `DisplayName(e)` = `Nick`, else `Name`, else `Label`, else the host:port.
  `Initials` uses `DisplayName`.
- `CleanName(s, max)` drops control characters, trims spaces and caps runes.
  The server's name is untrusted input: the client cleans it to 64 runes
  before storing or drawing it. Nicknames are capped at 32 runes.
- `onConnected` records the cleaned `ServerName()` in `Name` through `Touch`
  (an empty server name keeps the old value, so an old server doesn't wipe it).
- `SetNick(entries, url, nick)` and `Remove(entries, url)` return new slices,
  like `Touch`.

### 3.5 Right-click menu and dialog

- The tile menu now opens on **every** saved tile. Items, top to bottom:
  **Rename**, **Disconnect** (only while the tile has a session), then
  **Leave Server** drawn in `th.Bright` as the warning colour, after a thin
  separator.
- One small modal, `ui/dialog.go`, with two modes, drawn centred over the
  scene with the same panel fade as the menu. While open it owns the keyboard
  and mouse (clicks outside cancel; Esc cancels; Enter confirms), using the
  same "spend the press" rule as the menu.
  - **Rename:** title "Rename <display name>", one text field (the connect
    form's `field` type) prefilled with the current nickname, buttons
    **Save** and **Cancel**, and a dim hint "Only you see this. Leave empty
    to use the server's name." Save runs `SetNick` and saves `servers.toml`.
  - **Leave:** "Leave <display name>? You can add it back later." Buttons
    **Leave** and **Cancel**. Leave runs `disconnect(key)` if there is a
    session (ending its call), then `Remove` and saves `servers.toml`. If the
    viewed server is left, the view falls back the way `disconnect` already
    does.
- Save failures go to the status line, like `saveServer`.

### 3.6 Names on screen

- **Tile tooltip:** hovering a rail tile for 400 ms shows the display name in
  a small panel to the right of the tile, vertically centred on it, clamped
  inside the window. No tooltip while a menu or dialog is open.
- **Channel header:** the display name in `th.Bright` at the top of the
  channel column, one line, clipped with "…" if too long. The channel list
  moves down by one row.

### 3.7 Member list

- **State:** `state.State` gains `Roster []proto.Member`, `RosterTotal int`
  and `HasRoster bool` (false until the first roster arrives, so the UI can
  tell an old server apart). `SetConnected` clears them; `Apply` stores
  `ev.Roster`.
- **Groups:** `state.RosterGroups(members)` sorts each member into exactly
  one group: Admins (has `admin`), else Bots (has `bot`), else Members. Groups
  come back in the order Admins, Members, Bots; empty groups are skipped.
  Headings read "ADMINS — 2" style, in `th.Dim`.
- **Panel:** a column at the right edge of the chat area, 180 px at 100 % DPI,
  with the same soft panel look. One row per member: name in `th.FG`, your own
  row in `th.Bright`. If `RosterTotal > len(Roster)`, a last dim row "and N
  more". With an old server (`!HasRoster`), the panel shows one dim line:
  "member list needs a newer server". It scrolls with the wheel when longer
  than the panel.
- **Toggle:** a "members" text button at the top right of the chat area,
  bright while the list is shown. `Settings` gains
  `Members *bool  toml:"members,omitempty"` (nil = shown). The choice is global,
  not per server, as in Discord.
- **Narrow windows:** when the chat column would drop below 40 text columns
  with the list shown, the list is not drawn and the toggle draws dim. The
  saved setting is untouched.
- **Geometry:** the main-scene geometry gains `membersR`; `chatR`, `inputR`
  and the text columns shrink by its width. All hit tests come from the same
  geometry, as today (single-source geometry).

## 4. Error handling and compatibility

| Case | Behaviour |
|------|-----------|
| Old server (no `server_name`, no `roster`) | Tile and header fall back to `Label`/host; member panel shows its "needs a newer server" line |
| Old client against new server | Ignores `server_name` and `roster`; nothing else changes |
| Server name with control characters or over 64 runes | Cleaned and capped by the client before storing or drawing |
| Roster over 200 members | First 200 by name plus "and N more" |
| Leave on a tile in a call | The call ends (same path as Disconnect), then the entry is removed |
| `servers.toml` write fails on rename/leave | The in-memory change stays, the error shows on the status line |
| Leave the local (`-local`) server | Removed like any other; it comes back on the next `-local` run |

## 5. Testing

- **proto:** `Roster` and `AuthOK.ServerName` round trip; fuzz corpus gets a
  roster sample.
- **server:** `auth_ok` carries the configured name; a second client
  connecting causes a roster broadcast to both; a client leaving causes one to
  the rest; roster truncation at `MaxRosterMembers` with the right `Total`.
- **client:** `ServerName()`; a `roster` frame becomes an `Event.Roster`.
- **ui/state:** `DisplayName` precedence, `CleanName`, `SetNick`, `Remove`,
  `Touch` keeping `Name` on an empty server name, `RosterGroups` ordering,
  `Apply` and `SetConnected` for the roster fields, old `servers.toml` files
  loading unchanged.
- **ui:** menu items per tile state (with and without session), the dialog
  confirming and cancelling on Enter/Esc/outside click without the press
  reaching the scene, members geometry and its auto-hide threshold, hit tests
  for the toggle.
- **Victor's real-mouse checklist:** tooltips, the header, Rename (save, empty
  reset, Esc), Leave (with and without a call, the viewed server), the member
  list against a second client connecting and quitting, the toggle surviving a
  restart, narrow window and 150 % DPI. Plus the B.1 checklist still queued in
  HANDOFF.

## 6. After B.2

Release v0.2.0 from main, then Victor runs the old v0.1.0 zip and confirms the
update banner offers v0.2.0 and Ctrl+U opens its release page. Then screen
share and camera, then Plans A/B.
