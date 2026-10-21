# HERMEC always-on: design

Date: 2026-10-21. Approved in brainstorming with Victor, section by section.

## 1. Goal

Today a server lives on one machine: when Victor's PC is off, his friends lose
voice and chat. After this round a server keeps running as long as any granted
host has HERMEC open, and losing the active host mid-call costs about
1 to 1.5 s of audio, with no reconnect screen and no clicks.

Success, in Victor's words: automatic failover (option a), with the best
second host "always ready to go" so the switch has no sudden drop.

Constraints:
- Pure Go, no new C dependencies (portability goal).
- The design must not block external hosting later (a VPS or game-server style
  box running the headless `hermec-server`).
- Docs describe the app as it is now: README and `docs/protocol.md` sections
  are rewritten in place, and this spec is deleted when the round finishes.

Out of scope: invite links and a signed member allowlist (full Plan A),
signed or encrypted chat (Plan C), measured host scores (needs the bandwidth
metrics from the friends adjustments round), TLS and reachability for public
hosts.

## 2. Delivery

One spec, three PRs, each shippable on its own:

1. **Identity and chain** (section 3): server ID, the chain, mutual auth,
   migration, protocol version 2.
2. **Chat history** (section 4).
3. **Host link, warm standby, failover** (section 5), built on 1 and 2.

## 3. Server identity and the chain

### 3.1 Server ID

Creating a server writes chain entry 0, `genesis`: server name, founder
fingerprint, creation time and 16 random salt bytes, signed by the founder's
normal personal key. The server ID is the hash of that entry (SHA-256 of its
canonical bytes), shown in the fingerprint style with a `srv-` prefix.

- Clients pin the server ID per saved server (trust on first use).
- There is no separate server key file. The founder key is the person's
  existing key; losing it means no new hosts can be granted, and the server
  keeps running.

### 3.2 Entries

An entry is `{seq, prev, time, author, op, body, sig}`. `prev` is the hash of
the previous entry; `sig` is the author's Ed25519 signature over the canonical
encoding of every other field, so entries cannot be edited, reordered or
dropped without breaking the chain.

| Op | Body | Who may sign |
|---|---|---|
| `genesis` | name, founder, salt | the founder |
| `config` | name, channels, voice_channels, limits (`max_voice`, `max_clients`, `max_per_ip`), password as an scrypt hash with salt, `history_days`, `history_mb` | founder or a role with `manage` |
| `grant` / `revoke` | fingerprint, roles | founder or `manage`; the founder cannot be revoked |
| `ban` / `unban` | fingerprint | founder or `manage` |
| `host_add` / `host_remove` | fingerprint, priority | founder only |

- A `config` entry carries the full group config (the latest one wins), not a
  diff.
- Authority is checked against the chain state just before the entry.
- Storage: `<config>/hermec/chains/<server-id>.jsonl` on every host.
- Machine-local settings stay in TOML because they differ per host: port, TLS,
  public IP, media port, discoverable.
- The password hash uses scrypt from `golang.org/x/crypto` (already in the
  module graph). The password itself never goes into the chain.
- Admins and the founder change the chain by sending a `chain_append` request
  to the active host, which validates, appends and broadcasts.

### 3.3 Mutual auth (protocol version 2)

- The server's `challenge` stays. The client's `auth` adds a `nonce` of its
  own.
- `auth_ok` adds the host's `pubkey`, a signature over the client's nonce, and
  the chain (a few KB for a friend group).
- The client checks: the genesis hash equals the pinned server ID (or pins it
  on first connect), every entry verifies, and the host's fingerprint is on the
  current host list. Any failure ends the connection with a clear error and no
  automatic retry.
- Bans are enforced at auth. A ban appended during a session disconnects that
  member.
- `proto.Version` becomes 2. A v1 envelope gets one v1 `error` frame,
  "update HERMEC to connect to this server", then the connection closes.

### 3.4 Migration

On the first v0.6 launch, an existing `hosted.toml` becomes a genesis with its
admin as founder, then a `config` entry copied from it (the password is hashed),
`grant` entries copied from its grants, and `host_add` for the founder at
priority 1. The group fields leave `hosted.toml`; the machine-local ones stay.
Friends' saved tiles pin the server ID on their first connect.

The headless `hermec-server` with a `server.toml` and no chain keeps working as
a single host: on start it creates a chain from its config the same way, signed
by its own key as founder.

## 4. Chat history

### 4.1 Storage

- `<config>/hermec/history/<server-id>.jsonl`, one line per `ChatMessage`,
  which gains an `id` (16 random bytes, set by the host that accepts it).
- Caps come from the chain `config`: `history_days` (default 30, `0` = keep
  nothing) and `history_mb` (default 8).
- The host trims at startup and every hour by rewriting the file without the
  oldest lines.
- Text channels only.

### 4.2 Serving it

- After auth the host sends each text channel's latest messages as
  `history {channel, messages[], more}`, one frame of at most 64 KiB per
  channel.
- Scrolling to the top of a channel sends `history_before {channel, before_id}`;
  the host answers with the next older page.
- The client deduplicates by `id` and orders by `(ts, id)`, so reconnects,
  failovers and split-brain merges never show a line twice.

### 4.3 Honest limits

Messages are not signed by their authors, so a host can forge or drop lines;
this is Plan C. No edits or deletes; a ban keeps past messages. README: "the
active host and its standby can read and keep chat for up to `history_days`;
set it to 0 to keep nothing."

## 5. Host link, warm standby, failover

### 5.1 Roles

- **Active**: runs the session.
- **Standby**: the highest-priority linked host. Kept ready.
- **Idle hosts**: other granted hosts with the app open and the "let my PC host
  this server" toggle on. They keep a host link too, so they can become standby.

The active host sends clients `hosts {active, standby}` with fingerprints,
names and addresses. Clients save known host addresses per server ID in
`servers.toml`.

**Cold start:** a host with nobody active tries the known hosts for about 3 s;
if none answers, it becomes active. Simultaneous starts are settled by the
split-brain rule (5.6).

**Priority:** the order in the chain's host list, founder first. A measured
score (upload, ping to members) can replace it later without changing the link
protocol.

### 5.2 Host link

A WebSocket from each non-active host to the active one. The linking host
authenticates with its key and the chain proof (3.3, both directions), then
sends `link_hello {chain_seq, history_last_id, addrs}`. The active host sends
the missing chain entries and history, then streams new ones live. Heartbeats
every 500 ms in both directions.

### 5.3 Warm connection on clients

- Each client keeps a paused session on the standby: authenticated and
  chain-checked, but not in presence and not sent broadcasts.
- A client in voice also holds a WebRTC peer connection to the standby with one
  data channel and no media tracks, so ICE and DTLS are already done. It costs
  keepalive traffic only.
- When the standby changes, clients drop the old warm session and open one to
  the new standby.

### 5.4 Failover

1. A client misses the active host's heartbeats for 1.5 s and sends `takeover`
   on its warm session.
2. The standby double-checks. If its host link still sees the active host, it
   answers `active_alive`, and the client reconnects to the active host normally
   (its own network blipped).
3. Otherwise the standby promotes itself. It turns warm sessions into full
   ones (presence and broadcasts), and clients rejoin their voice channel with
   the same mute state. Media resumes on the open peer connection after one
   offer/answer.
4. The new active host picks a new standby, other hosts re-link to it, and
   chat gets a system line "host changed to <name>".

If both the active host and the standby are gone, clients fall back to the
normal reconnect loop over the known host addresses.

### 5.5 Return and handback (sticky)

A returning higher-priority host finds the active one through cold start and
links as standby; it never takes over a running call. Once a higher-priority
host is linked and voice has been empty for 2 minutes, the active host does a
planned `move`: clients switch through their warm sessions, and the hosts swap
roles.

### 5.6 Split-brain

Each active host retries the other known hosts every 30 s. When two active
hosts meet, the one with fewer voice members yields (ties: fewer clients, then
higher priority number, then fingerprint order). The loser sends its chat
history to the winner (merged by `id`) and sends its clients `move` to the
winner. The winner's chain is kept; entries the loser appended after the fork
are dropped, and their authors get a notice: "your change was lost during a
network split; please redo it".

### 5.7 UI

- The voice panel shows "hosted by <name>".
- Settings: "let my PC host this server" per server (default on for granted
  hosts).
- Founder: roster right-click gets Allow hosting, Stop hosting and Move up
  (priority).
- A failed chain or server ID check shows on the tile as a failure with the
  reason, and does not auto-retry.

### 5.8 External hosting fit

A VPS or game-server box runs the headless `hermec-server` with its own key
file, granted in the chain with a high priority, and is first choice whenever
it is up. Reachability (public address, TLS) is a later round; nothing here
assumes hosts are on the tailnet.

## 6. Testing

- Chain: unit tests for every op's authority rule, the canonical encoding and
  tampering (edited, reordered, dropped entries); fuzz the verifier.
- Migration: an old `hosted.toml` becomes the expected chain; `server.toml`
  servers start.
- Auth: a wrong server ID, a host not on the list and a v1 client each get the
  right error.
- History: caps and trimming, paging, dedup, the 64 KiB frame limit.
- Integration (2 to 3 servers in one process, synthetic media): kill the active
  host and see the client switch within 2 s with voice and chat continuing; a
  client-only blip causes no failover; handback only when voice is empty;
  split-brain merge of chat and the chain notice.
- Real test (Victor): a friend call where Victor quits HERMEC mid-call with a
  friend's machine as standby.

## 7. Docs

README ("How people reach each other", "How safe is this?", hosting) and
`docs/protocol.md` (auth, new messages, version 2) are rewritten in place.
HANDOFF is updated, and this spec and its plans are deleted at round end.
