# Hermec Round B.1: Concurrent Server Sessions — Design

Date: 2026-10-11. Status: awaiting Victor's review.
Round B was decomposed into three sub-projects; this spec covers only B.1.

| Sub-project | Items (HANDOFF Round B list) |
|-------------|------------------------------|
| **B.1 Sessions (this spec)** | (a) stay connected across servers, (b) rail "you are here" bust |
| B.2 Server identity & management | (d) server names on connect, (e) rename/remove saved servers, (c) member roster |
| B.3 Icon editor | (f) pixel-art server icon editor |

## 1. Intent

**What Victor wants:** switching servers on the rail must not drop anything.
Today a tile click closes the current connection, including the voice call.
Hermec should feel like Discord here: hop between servers, keep the call, and
see at a glance where new messages arrived.

**Decisions Victor made in the brainstorm:**

- Every server opened this session stays connected (option B). Saved servers
  are *not* dialed at startup; that is a later add-on.
- You leave a server via **right-click on its rail tile → Disconnect**.
- Unread shows as a **dot on the rail tile plus brighter text channels** inside
  the server (option C).
- Architecture: **one `session` per server** (approach 1).

**Assumptions stated to Victor and not contested (override in review):**

- One voice call at a time across all servers. Joining voice on server Y while
  in a call on X leaves X first.
- A server that drops reconnects by itself in the background, without pulling
  the view away from what you're looking at.

**Success looks like:** in a call on server A, click server B's tile, read and
send chat on B, click back to A. The call never stutters or drops, B's tile
shows a dot when someone writes there while you're on A, and A's tile shows the
bust the whole time.

## 2. Current shape (what changes)

`ui.game` holds exactly one connection: `g.c` (client), `g.st` (`state.State`),
`g.ms` (`mainScene`), plus `curKey`, `lastURL`, `lastName`, `dialing`, and the
result channels `dialCh`, `joinCh`, `sendCh`. `switchServer` calls
`leaveServer`, which drops voice and closes the client. `Update` drains events
only from `g.c`. The client joins a text channel lazily, when you open it, and
the server only sends `chat_message` for joined channels.

## 3. Design

### 3.1 The session

New file `ui/session.go`:

```go
type session struct {
    key     string         // saved-list key (localKey for -local)
    url     string         // real dial URL
    name    string         // display name used for this dial
    c       *client.Client // nil while dialing or disconnected
    st      *state.State
    ms      mainScene
    dialing bool
    retry   backoff        // reconnect schedule, see 3.4
    user    bool           // the user asked to disconnect: never auto-retry
}
```

`game` replaces `c, st, ms, curKey, lastURL, lastName, dialing` with:

```go
sess map[string]*session // by key
cur  string              // key of the viewed session; "" = connect scene
```

plus `g.cur()` returning the viewed `*session`, or a shared empty placeholder
whose `st` is in `PhaseConnect` so existing draw code keeps working with no nil
checks. All existing `g.c` / `g.st` / `g.ms` uses become `g.cur().c` and so on.
The rename is mechanical. Behaviour for a single server must not change.

The connect scene (the "+" tile, or before the first connect) is shown when
`cur == ""`. Submitting it creates a session (or reuses one for the same key)
and dials.

### 3.2 Result routing

Background results must say which session they belong to.

- `dialResult`, `joinResult` and the chat-send result gain a `key string`.
  Results for a key with no session (it was disconnected meanwhile) are
  discarded, and any client in a stale `dialResult` is closed.
- A `dialResult` for a live session applies to that session, viewed or not. On
  success it runs `onConnected` *for that session*.
- `voiceResult` already carries `c`; the check `r.c != g.c` becomes "`r.c` is
  not the call session's client".

### 3.3 Event flow

Each `Update` drains every session's `Events()` with a per-session cap of
`maxEventsPerTick` (64), visiting sessions in a fixed order (sorted by key) so
the cap cannot starve anyone. Events apply to that session's `state.State`. A
closed event channel marks only that session disconnected (see 3.4).

**Join every text channel on connect.** `onConnected` joins all text channels,
not just the remembered one, so chat for every channel arrives and can be
counted as unread. The remembered channel is still the one shown. Visible side
effect: you appear in every text channel's presence list, which is also what
the roster (B.2) will want.

### 3.4 Disconnect and reconnect

- **Right-click → Disconnect** sets `user = true`, leaves the call if it is on
  this session, closes the client, and deletes the session. If it was the
  viewed one, the view goes to another connected session (top-most on the
  rail), or to the connect scene if none remain.
- **Connection lost** (events channel closed): if the session holds the call,
  the call is dropped (devices freed, "voice call ended" status, same as
  today). The call is **not** auto-rejoined: rejoining voice is always a click.
  The session then retries in the background with backoff of 1s, 2s, 4s, 8s,
  16s, then every 30s, with no limit, until it connects or is disconnected by
  the user. The pure schedule lives in `ui/state` (`NextBackoff(attempt int)
  time.Duration`).
- The viewed disconnected session keeps today's screen ("CONNECTION LOST"),
  with the message now saying `reconnecting in Ns — Enter to retry now`. Enter
  resets the backoff and dials immediately.
- A successful reconnect re-runs `onConnected` (re-joins channels). Chat
  scrollback from before the drop is kept in the session's state.

### 3.5 Voice across sessions

`game` keeps one `voiceCall`, plus a new field `callKey string` naming the
session that owns it.

- `clickVoice` acts on the viewed session. If a call exists on another
  session, `joinCall` leaves that one first (its client's `LeaveVoice`, then
  close devices), then joins on the viewed session. This is the same
  leave-then-join goroutine as today, with the old client passed in
  explicitly.
- The **call bar is shown whenever a call exists**, on any viewed server. Its
  title becomes `channel @ server-label` when the call is on a different
  server than the one viewed. Mute and leave act on `callKey`'s session.
- Ctrl+M acts on the call wherever it is.
- `InCall` stays on the owning session's `state.State`. The voice pane of the
  viewed server reads its own `InCall`, so "you're in this call" highlighting is
  correct per server.
- Speaking glow (`speakEnv`) keeps keying on fingerprint and only applies to
  rows of the call session's voice channel.
- **Signal bars:** the call bar's bars and quality panel show the call
  session's RTT and health. The status-line bars, when there is no call, show
  the viewed session's RTT.

### 3.6 Unread tracking (pure, in `ui/state`)

`state.State` gains `Unread map[string]bool` (text channel → has new messages).

- `Apply` of a chat message sets `Unread[ch] = true` unless that channel is the
  one being read: it is the active channel **and** the session is viewed. The
  game tells the state via a new `Viewed bool` field, which `game` sets when
  `cur` changes.
- Own messages never mark unread (the sender fingerprint is the session's own).
- Opening a channel, or viewing a session whose active channel is unread,
  clears that channel's flag.
- `(*State).AnyUnread() bool` drives the rail dot.

Channel rows with `Unread` draw in `th.Bright` (the active row keeps its
existing highlight). No counts.

### 3.7 Rail

- **Order is stable.** `state.Touch` stops moving an existing entry to the
  front. It updates the existing entry in place, and only a *new* server is
  inserted at the top. Without this, tiles would jump under the mouse every
  time you switch. The `MaxServers` cap never evicts a server with a live
  session.
- **Tile states**, drawn in `drawRail`:
  - viewed: the existing accent tint (keyed on `cur` instead of `curKey`).
  - connected or dialing: normal text.
  - saved but no session: dimmed text (`th.Dim`). This is new; today every
    non-viewed tile looks the same.
  - reconnecting (session exists, not connected): dimmed text plus a small
    pulsing dot at the bottom-left.
  - unread: a small filled dot in `th.Bright` at the top-right corner.
  - in call: the **bust** at the bottom-right, a stick figure's top half (a
    circle head over a shoulders arc) drawn with `vector`, in `th.Bright`. It
    shows only on the session that holds the call.
- **Left-click** on a tile: if it has a session, view it (no dial). Otherwise,
  create a session and dial it, and view it.

### 3.8 Right-click menu

A small generic context menu, `ui/menu.go`, that B.2 will extend with
Rename and Remove:

- Opens on a right mouse press over a rail tile, anchored at the cursor and
  clamped inside the window. It's a panel with one row per item, using the
  existing hover highlight and hand cursor.
- B.1 items: **Disconnect**, shown only when the tile has a session.
  A tile with no session opens no menu.
- Closes on click outside, Esc, or after picking an item. While open, its hit
  targets are registered last, so they win over everything under them (the
  same pattern as the quality panel).
- Pure parts in `ui/state`: the menu's geometry (item rects from anchor, item
  count and window size, with clamping) and hit testing, tested headlessly.

## 4. Error handling

- Dial failures for a non-viewed session go into that session's backoff, with
  no status-line noise. For the viewed session, they show as today.
- A session-scoped error event (`ev.Err`) writes to that session's `Status`,
  so it only shows when you look at that server.
- Disconnecting the call session while a voice join/leave is in flight: the
  result arrives for a client that is no longer the call session's, so the
  devices are closed and the result discarded (existing guard, generalized).
- Quitting the app leaves the call on its session (as today, with the 2-second
  timeout), then closes every session's client.

## 5. Testing

- **Pure (`ui/state`):** `NextBackoff`; `Touch` keeping order and not evicting
  live keys (it takes a `keep func(url string) bool`); unread rules (other
  channel, viewed vs not viewed, own message, clear on open, `AnyUnread`);
  context-menu geometry and clamping; tile-state derivation (a function from
  session facts to a small `TileState` struct, so `drawRail` only paints).
- **Session layer without Ebiten:** a test in `ui` that starts two in-process
  servers (`server.New` on `127.0.0.1:0`), drives the session table through
  its own functions (not `Update`, which reads the keyboard): connects both,
  verifies chat on B marks B unread while A is viewed, disconnects A and
  verifies B is untouched, kills a server and verifies the session
  reconnects when it comes back on the same port. Voice is covered by a fake
  `voiceCall` path: no audio devices in tests.
- **Existing suite** stays green, including `-race`. The single-server
  behaviour is unchanged apart from "join all text channels on connect".
- **Victor's real-mouse checklist** (cannot be automated): call on server A →
  click B → chat on B → back to A, call uninterrupted; right-click → Disconnect
  on the call server ends the call; unread dot and bright channel; bust
  placement at 100% and 150% DPI; kill the server process and watch the tile go
  to reconnecting and back. **Agents must not run the GUI against the real
  `%APPDATA%\hermec`.**

## 6. Out of scope (B.1)

Auto-connect at startup; server names (B.2); rename/remove (B.2, though the
menu is built here); roster (B.2); notifications or sounds for unread; unread
counts; mention detection; persisting sessions across restarts; any protocol
change (none needed for B.1).
