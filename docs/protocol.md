# Hermec Wire Protocol, version 2

This document specifies the Hermec signaling/chat protocol, version 2. It
covers the mutual handshake, the server chain (section 9), channels, presence,
text chat and its history (sections 4.9 and 4.9a), the server roster, voice
signaling (section 4.12), screen share signaling (section 4.15), and host
failover: the host list, warm standby sessions and the host-to-host link
(sections 4.16 to 4.18). Webcam is not specified yet.

The reference implementation is `core/proto` (types and codec) and `server`
(behavior). Where this document and the code disagree, that is a bug in one of
them; please report it.

## 1. Transport

- WebSocket (RFC 6455). Plain `ws://` or, when the server has a TLS keypair
  configured, `wss://`. Any path is accepted.
- Every protocol message is one WebSocket **text** frame containing one JSON
  envelope (section 2).
- Maximum message size: **65536 bytes (64 KiB)**, measured on the raw frame.
  The server's WebSocket read limit is set to this value, so a larger frame
  terminates the connection. `proto.Decode` also rejects larger input.
  Servers never send a frame over this limit: an outbound frame that would be
  too large is dropped and logged, not sent. Lists that could grow past it
  (`presence`, `roster`) are trimmed to fit, see sections 4.7 and 4.14.
- Keepalive: the server sends a WebSocket ping every 15 seconds and closes the
  connection on the tick after two consecutive unanswered pings (about 45
  seconds after the last pong), without sending a third. Clients must
  answer pings (standard WebSocket libraries do so automatically).
- The default port is 7697.

## 2. Envelope

```json
{"v": 2, "type": "chat_send", "data": {"channel": "general", "text": "hi"}}
```

| Field  | Type    | Description                                              |
|--------|---------|----------------------------------------------------------|
| `v`    | integer | Protocol version. Must be `2`, except for the `challenge` and the `update_required` error, which are sent as `1` (section 7). |
| `type` | string  | Message type, non-empty. See section 4.                  |
| `data` | object  | Type-specific payload. Omitted when the payload is empty. |

A message is malformed if it exceeds the size limit, is not valid JSON, has
an unsupported `v`, or has an empty `type`.

Conventions:

- `[]byte` fields (`nonce`, `pubkey`, `sig`) are encoded as standard base64
  JSON strings.
- Timestamps are RFC 3339 (UTC from the server).
- Unknown fields inside `data` are ignored.

## 3. Connection flow

```
Client                                   Server
  | -------- WebSocket upgrade --------> |
  | <-------------- challenge ---------- |   (1) server nonce, 32 bytes (v1 envelope)
  | ----------- host_proof_req --------> |   (2) client nonce, 32 bytes
  | <--------------- chain ------------- |   (3) the signed chain, in 1+ frames
  | <------------- host_proof ---------- |       host key's proof over both nonces
  |   client verifies the host           |   (4) before anything secret is sent
  | ---------------- auth -------------> |   (5) pubkey, name, sig, proof?
  | <-------------- auth_ok ------------ |   (6) or error + close
  |                                      |
  | ---------------- join -------------> |
  | <------------- presence ------------ |   broadcast to all channel members
  | <-------------- history ------------ |   to the joiner, if the channel has stored messages
  | ------------- chat_send -----------> |
  | <----------- chat_message ---------- |   broadcast to all channel members
```

### 3.1 Handshake

The host proves it belongs to the server before the client sends its password.
Nonces are 32 random bytes. The whole handshake must finish within **10
seconds** of the upgrade. `server_id` is the raw 32 bytes of the server ID
(section 9.1) in both signed strings. Each signed string starts with its own
domain prefix, ending in a NUL byte (`\x00`), so a signature made for one
purpose cannot be replayed as another. A version 1 client sends `auth` straight
after the `challenge`; if that `auth` is well formed the server answers
`update_required` and closes (section 7).

1. **`challenge`** (S). Right after the upgrade the server sends a fresh server
   nonce. The challenge is a version 1 envelope (section 7); every other
   handshake message is version 2.
2. **`host_proof_req`** (C), carrying the client's own nonce.
3. **`chain`** and **`host_proof`** (S). The server sends its whole chain as
   one or more `chain` frames (section 4.2a), then `host_proof`: the host key
   and its Ed25519 signature over

   ```
   "hermec-host-proof\x00" || client_nonce || server_nonce || server_id
   ```
4. **The client verifies the host.** The chain's every entry and link must
   verify (section 9), its genesis hash must equal the server ID the client
   has pinned (a client with no pin trusts and records the first one it sees),
   the proving key must be on the chain's host list, and the proof signature
   must be valid. A failure ends the connection before `auth` is sent, with
   `server_id_mismatch`, `host_not_authorized` or `bad_chain` (section 5);
   clients do not retry these automatically.
5. **`auth`** (C). The client signs

   ```
   "hermec-client-auth\x00" || server_nonce || client_nonce || server_id
   ```

   Binding the server ID stops a host from replaying the signature to log in as
   the client on another server. `auth` also carries the public key, a display
   name and, if the server has a password, a `proof` of it (section 3.1a).
6. **`auth_ok`** (S), or one `error` and a close. `auth_ok` carries the
   `server_id`; the client checks it against the ID it verified in step 4 and
   ends the connection with `server_id_mismatch` if it differs. If entries were
   appended after the server sent the chain in step 3, it follows `auth_ok`
   with one or more `chain` frames holding exactly those entries, so the
   client's copy has no gap before the first live frame (section 4.14a).

The server checks `auth` in this order: it is a well-formed version 2 `auth`
with a 32-byte `pubkey`; the signature is valid; the key is not banned; the
password proof if a password is configured (a warm `auth` from a key on the
chain's host list skips this check, see section 4.17); the key's fingerprint
is on the allow list if one is configured (the founder and every listed host
always pass the allow list). A bad signature, wrong password or
unlisted key gets a single `error` with code `auth_failed` and the connection
closes; the error never reveals which check failed. A banned key, once its
signature has proven it holds the key, gets `error` `banned` instead.

Any message other than `host_proof_req` and `auth`, in that order, is an
authentication failure. Timeout or disconnect before `auth` simply closes the
connection.

### 3.1a Password proof

The password never goes over the wire. A server with a password holds a 32-byte
key instead:

```
key = scrypt(password, "hermec-pw\x00" || server_id, N=32768, r=8, p=1, 32 bytes)
```

where `server_id` is the raw 32 bytes. The client derives the same key and
sends `auth.proof`:

```
proof = HMAC-SHA256(key, "hermec-pw\x00" || server_nonce || client_nonce || server_id)
```

The server compares it in constant time. The nonces and the server ID make
every proof single-use and specific to one server. A server with a password
and no key (an empty stored key) refuses every login.

A v0.6 client sends `auth.password` (the plain password) and no `proof`. The
server derives the key from it and compares keys in constant time. This
fallback exists for one release; from v0.8 a server refuses `auth.password`.
Hosts pass the key to each other over the host link (section 4.18), never the
password.

**Pre-auth chain budget.** Sending the chain costs the host far more than the
`host_proof_req` costs the client. A host therefore sends at most 2 MiB of
chain frames per minute to one remote IP before it authenticates. When an IP is
over that budget, the host sends one `error` `server_busy` (as a version 1
envelope, so any client can read it) instead of the chain, and closes. A client
that gets it retries later and does not count the host as dead. HTTP 503
refusals (section 3.3) are mapped to the same code on the client side.

**Failed logins.** After 5 failed logins in a row from one IP (forgotten after a quiet minute), that IP
is refused for 1 s. Each further failure doubles the wait, up to 60 s. A
successful login resets the count. While an IP is refused, its connection
attempts get HTTP 503 instead of a WebSocket.

**Fingerprint.** A key's identity is its fingerprint: the lowercase RFC 4648
base32 (no padding) encoding of the first 10 bytes of `SHA-256(pubkey)`,
split into hyphen-separated groups of four characters, for example
`k7mv-q3xp-9dfw-02hj`. Allow lists, role grants and presence use fingerprints.

### 3.2 After authentication

The client may send `join`, `leave`, `chat_send` and `history_before`. The
server sends `presence`, `chat_message` and `history` for channels the client
has joined. A
connection that sends anything else receives `error` (`bad_request`).
Disconnecting leaves all channels; remaining members receive a new `presence`.

### 3.3 Connection and name limits

- **Browser origins.** An upgrade request that carries an `Origin` header is
  refused. Native clients send none.
- **Connection caps.** The server allows at most 64 connections waiting for
  `auth`, `max_per_ip` concurrent connections per remote IP (default 16; raise
  it for a LAN party), and `max_clients` authenticated sessions (default 256).
  A new connection over the pre-auth or per-IP cap is refused with HTTP 503,
  before any WebSocket opens. A client over `max_clients` gets `server_full`
  (section 5).
- **Names.** The server cleans every `auth` name: it removes format, control
  and line or paragraph separator characters, trims spaces, and keeps at most
  64 characters. A name that is empty after cleaning becomes `anon`.
  `presence`, `roster` and `chat_message` carry the cleaned name.

## 4. Messages

Direction: C is client to server, S is server to client.

### 4.1 `challenge` (S)

| Field   | Type   | Description                        |
|---------|--------|------------------------------------|
| `nonce` | base64 | The 32-byte server nonce. Sent as a version 1 envelope (section 7). |

### 4.2 `auth` (C)

| Field      | Type   | Required | Description                               |
|------------|--------|----------|-------------------------------------------|
| `pubkey`   | base64 | yes      | 32-byte Ed25519 public key.               |
| `name`     | string | yes      | Display name, cleaned and capped by the server (see 3.3). |
| `sig`      | base64 | yes      | Ed25519 signature over the client-auth string of section 3.1. |
| `password` | string | no       | The plain password. Only v0.6 clients send it; refused from v0.8 (section 3.1a). |
| `proof`    | base64 | no       | Password proof, if the server has a password (section 3.1a). |
| `warm`     | bool   | no       | True for a standby pre-connection (section 4.17). |

### 4.2a `host_proof_req` (C), `chain` (S) and `host_proof` (S)

`host_proof_req` is `{nonce}`: the client's 32-byte nonce, base64.

`chain` carries chain entries in order:

| Field     | Type     | Description                                           |
|-----------|----------|-------------------------------------------------------|
| `entries` | Entry[]  | Chain entries, each as in section 9.2.                |
| `more`    | bool     | Optional. True when further `chain` frames of the same transfer follow. |

In the handshake the server sends the whole chain from genesis, split into
frames that each hold under 60 KiB of entries (so the frame stays under the
64 KiB limit). A client that receives anything but `chain` frames and then
`host_proof` ends the handshake. After authentication a `chain` frame carries
newly appended entries (section 4.14a).

`host_proof` is `{pubkey, sig}`: the host's 32-byte Ed25519 public key and its
signature over the host-proof string of section 3.1.

### 4.3 `auth_ok` (S)

| Field         | Type          | Description                          |
|---------------|---------------|--------------------------------------|
| `fingerprint` | string        | The authenticated key's fingerprint. |
| `roles`       | string[]      | Roles assigned to this fingerprint.  |
| `channels`    | ChannelInfo[] | All channels on the server.          |
| `server_name` | string        | Optional. The host's configured `server_name`; omitted when the host set none. Untrusted: clients strip control and format characters and cap it at 64 runes before display. Omitted means "no name". |
| `server_id`   | string        | The server ID (section 9.1) in its 52-character lowercase base32 form. Must equal the ID the client verified in the handshake. |

`channels` is an array of `ChannelInfo` objects:

| Field  | Type   | Description                              |
|--------|--------|------------------------------------------|
| `name` | string | Channel name.                            |
| `type` | string | `"text"` or `"voice"`.                   |

### 4.4 `error` (S)

| Field     | Type   | Description                                         |
|-----------|--------|-----------------------------------------------------|
| `code`    | string | Machine-readable code, see section 5.               |
| `message` | string | Human-readable detail; not stable, do not parse it. |
| `ref`     | string | Optional. The `ref` of the `chain_append` this error answers (section 4.14a). |
| `gen`     | int    | Optional. The `gen` of the `watch` this error answers (section 4.15). |

### 4.5 `join` (C)

| Field     | Type   | Description         |
|-----------|--------|---------------------|
| `channel` | string | Channel to join.    |

Requires the `join_channel` permission. On success the server broadcasts
`presence` for the channel, including to the joiner, and then, for a text
channel with stored messages, sends the joiner a `history` frame (section 4.9a).
Joining a channel the client is already in is not an error; it produces another
`presence` and `history`. Unknown channels yield `error` `bad_request`.

### 4.6 `leave` (C)

| Field     | Type   | Description         |
|-----------|--------|---------------------|
| `channel` | string | Channel to leave.   |

On success the remaining members receive `presence`; the leaver does not.
If the client was not in the channel the server replies `error` `not_joined`.

### 4.7 `presence` (S)

| Field     | Type     | Description                                      |
|-----------|----------|--------------------------------------------------|
| `channel` | string   | Channel name.                                    |
| `members` | Member[] | Full current member list, sorted by name then fingerprint. |
| `total`   | int      | Optional. The real member count. Omitted when zero. |

`presence` always carries the complete member list, not a delta. If the list
would not fit in one frame (section 1), `members` holds only the first members
that fit, and `total` still gives the real count, so a client can show
"and N more". A `Member`:

| Field         | Type     | Description                       |
|---------------|----------|-----------------------------------|
| `fingerprint` | string   | Member's key fingerprint.         |
| `name`        | string   | Member's display name.            |
| `roles`       | string[] | Member's roles.                   |
| `pub_key`     | base64   | Optional. The member's 32-byte public key. The founder needs it to approve the member as a host (`host_add` takes the key). Omitted when empty. |

### 4.8 `chat_send` (C)

| Field     | Type   | Description                  |
|-----------|--------|------------------------------|
| `channel` | string | Target channel.              |
| `text`    | string | Message body (UTF-8 text).   |

The sender must be a member of the channel (`not_joined` otherwise) and hold
the `send_chat` permission (`forbidden` otherwise). `text` may be at most
**4096 bytes** (UTF-8). A longer text is refused with `bad_request` and the
message `message too long`.

### 4.9 `chat_message` (S)

| Field     | Type   | Description                                   |
|-----------|--------|-----------------------------------------------|
| `id`      | string | Message id: 32 lowercase hex characters (16 random bytes), set by the host. |
| `channel` | string | Channel name.                                 |
| `from`    | Member | Sender (see 4.7).                             |
| `text`    | string | Message body.                                 |
| `ts`      | string | Host receive time, RFC 3339 UTC. Strictly increasing per server: a message never has the same or an earlier `ts` than the one before it. |

Broadcast to every member of the channel, including the sender. Messages are
delivered in a single consistent order to all members. The host stores text
channel messages for later `history` (section 4.9a). Messages are not signed
by their authors; there are no edits or deletes. A client orders messages by
`(ts, id)` and drops a message whose `id` it already has.

### 4.9a `history` (S) and `history_before` (C)

The host keeps text-channel chat in a file, capped by the chain `config` entry's
`history_days` and `history_mb` (section 9.3). Voice channels have no history.

`history` (S):

| Field      | Type          | Description                                          |
|------------|---------------|------------------------------------------------------|
| `channel`  | string        | Channel name.                                        |
| `messages` | ChatMessage[] | `chat_message` payloads (4.9), oldest first.         |
| `before`   | string        | Optional. The `before_id` of the request this answers; omitted for the page sent on join. |
| `more`     | bool          | True when older messages than the first one exist.   |

`history_before` (C):

| Field       | Type   | Description                                           |
|-------------|--------|-------------------------------------------------------|
| `channel`   | string | A text channel the sender has joined.                 |
| `before_id` | string | Id of a message; the host returns the messages just older than it. |
| `before_ts` | string | Optional. The `ts` of that message (RFC 3339), used when the host no longer holds `before_id`. |

Semantics:

- **On join.** Right after the `presence` of a join (section 4.5), the host
  sends `history` with the newest messages of the channel, at most 100, oldest
  first. Nothing is sent when the channel has no stored messages.
- **Frame size.** A `history` frame never exceeds 64 KiB encoded: when 100
  messages would not fit, the page is cut to the newest messages that do, and
  `more` is true.
- **Older pages.** `history_before` is answered with a `history` whose
  `before` echoes `before_id` and whose `messages` are the up to 100 messages
  just older than that one (same size rule), oldest first. "Older" is by
  `(ts, id)`. A `before_id` the host holds is placed by its own `ts`. One it
  no longer holds (for instance a message trimmed since) is placed by
  `before_ts` together with `before_id`; without `before_ts` the answer has
  empty `messages` and `more` false. A client sends the `ts` of the message it
  names.
- **Errors.** A `history_before` for a channel the sender has not joined gets
  `error` `not_joined`; one from a sender who no longer holds the permission
  to join channels gets `error` `forbidden`; a malformed one (not a JSON object, or an empty
  `channel` or `before_id`) gets `bad_request`. It counts against the normal
  message rate limit.
- **Client behavior.** A client merges messages by `id` and may receive the same
  message from `history` and as a live `chat_message`. If the join page after a
  reconnect shares no message with what the client already holds, the client
  replaces its list for that channel and loads older pages again on demand. A
  client sends `history_before` only after its `join` of that channel has
  succeeded, and stops asking when an older page adds no new message.
- **Retention.** The host drops lines older than `history_days` at start,
  every hour, and at once when a `config` entry changes the limits.
  `history_mb` is enforced on every stored message: when the stored lines
  would pass it, the oldest leave the host's memory (and so every page) at
  once, and the file is rewritten whenever it has grown past twice
  `history_mb`. `history_days` 0 keeps nothing.

### 4.10 `channel_list`

Defined in `core/proto` but not used: the channel list is delivered in
`auth_ok`. Reserved for dynamic channel lists. Servers must not send it, and a
server that receives it treats it as an unsupported type (`bad_request`).

### 4.11 Reserved: `e2ee_key_exchange` and `e2ee_frame`

Reserved for future end-to-end encryption. These types are **reserved and
servers must reject them**: a server answers with `error` `bad_request` and
does not relay them. Clients never send them.

### 4.12 Voice messages

Voice signaling rides the same WebSocket as chat; audio media travels over
WebRTC (negotiated as in section 4.13). The server acts as an SFU (selective forwarding unit).

| Type            | Dir | Payload                                                        |
|-----------------|-----|----------------------------------------------------------------|
| `voice_join`    | C   | `{"channel": string}` join a voice channel.                    |
| `voice_leave`   | C   | `{}` leave the current voice channel.                          |
| `voice_mute`    | C   | `{"muted": bool}` set the sender's mute state.                 |
| `voice_state`   | S   | `{"channel": string, "members": VoiceMember[]}`                |
| `rtc_offer`     | C/S | `{"sdp": string}` SDP offer.                                   |
| `rtc_answer`    | C/S | `{"sdp": string}` SDP answer.                                  |
| `rtc_candidate` | C/S | `{"candidate": string}` one ICE candidate (JSON candidate init). |

`VoiceMember`: `fingerprint` (string), `name` (string), `muted` (bool),
`sharing` (bool, omitted when false; true while the member shares their
screen, see section 4.15).

Semantics:

- `voice_join` targets a channel whose `type` is `voice`; unknown or non-voice
  channels yield `bad_request`. It requires the `join_channel` permission.
  A channel already at its participant cap (`max_voice`, default 25, maximum
  50) refuses the join with `voice_full`.
- **One call at a time.** A connection is in at most one voice channel.
  Sending `voice_join` for another channel while in a call moves the client:
  it leaves the old channel (whose members get a fresh `voice_state`) and
  joins the new one. Re-joining the current channel is not an error.
- `voice_leave` while not in a call is a no-op. Disconnecting leaves the call.
- `voice_state` always carries the complete participant list of the channel
  (not a delta), including the recipient, and is sent to every participant
  on any join, leave or mute change. A newly joined member is unmuted.
- `voice_state` is sent to every authenticated connection, not only
  participants. On successful auth, right after `auth_ok`, the server sends a
  `voice_state` for every currently occupied voice channel.
- `voice_mute` while not in a call yields `error` `not_joined`.
- Voice membership is separate from chat membership (`join`/`leave`).

### 4.13 Voice negotiation flow

```
Client                                   Server (SFU)
  | ------------ voice_join -----------> |
  | <---------- voice_state ------------ |   client is now a member
  | ------------ rtc_offer ------------> |   sent after the client's first
  | <----------- rtc_answer ------------ |   voice_state listing itself
  | <-------- rtc_candidate ------------ |   candidates flow both ways
  | --------- rtc_candidate -----------> |   (trickle ICE)
  |                                      |
  | <----------- rtc_offer ------------- |   server-initiated renegotiation
  | ------------ rtc_answer -----------> |   when topology changes
```

1. After `voice_join`, the client waits for the first `voice_state` in which it
   is itself a member, then sends `rtc_offer` with its audio send track.
2. The server replies with `rtc_answer`. Both sides then exchange
   `rtc_candidate` messages as ICE candidates are gathered.
3. The server forwards each participant's audio to the other participants.
   When the topology changes (a participant joins or leaves, so tracks are
   added or removed), the **server** initiates renegotiation by sending
   `rtc_offer`; the client replies with `rtc_answer`. Clients must therefore
   handle server-originated offers at any time during a call.
4. `rtc_*` messages outside a call are answered with `error` `not_joined`.
5. **Glare.** If the client sends `rtc_offer` while a server offer is
   outstanding, the server rejects it with `error` `bad_request` and the exact
   message `offer pending — answer first`. The client must answer the
   outstanding server offer and may then send its offer again.
6. **Recovery.** If the server cannot apply an `rtc_answer`, cannot produce a
   renegotiation offer, or receives no `rtc_answer` within 15 seconds of a
   server offer, it discards that participant's media state, sends `error`
   `bad_request`, and keeps the participant in the call. The client recovers by
   building a new PeerConnection and sending a fresh `rtc_offer`. The server's
   error message always ends with the exact hint `send a new rtc_offer`; clients
   may rely on this suffix to trigger recovery.

**Rate limiting.** Servers may rate-limit voice and signaling messages. A
violation is answered with `error` `rate_limited`, after which the server
closes the connection.

**No echo cancellation.** Hermec does not perform acoustic echo cancellation.
Using speakers will feed other participants' audio back into your microphone;
headsets are strongly recommended.

### 4.14 `roster` (S)

Server-wide list of every authenticated connection, for the member list. It is
independent of channels: it covers everyone on the server, not only the members
of one channel.

| Field     | Type     | Description                                                        |
|-----------|----------|--------------------------------------------------------------------|
| `members` | Member[] | Members, sorted by name then fingerprint. `Member` as in section 4.7. |
| `total`   | int      | Number of authenticated connections, including those not listed.  |

Semantics:

- The list is complete, not a delta. Each `roster` replaces the previous one.
- Sent to every authenticated connection right after `auth_ok` and after any
  `voice_state` snapshots, and again whenever a connection authenticates or an
  authenticated connection drops.
- `members` is capped at 200 entries and trimmed further if needed so the frame
  fits the 64 KiB message limit. `total` still counts everyone, so a client can
  show "and N more" when `total` exceeds the number of listed members.
- Names in `roster` are capped at 64 runes (by runes, not bytes) so one long
  name cannot crowd the others out; `presence` and chat carry names unchanged.

### 4.14a `chain_append` (C) and live `chain` (S)

`chain_append` carries one signed chain entry (`{entry, ref}`, the entry as it
appears in a `chain` frame, section 9.2), built on the head the sender holds.
`ref` is an optional string the client chooses; the server echoes it in
`error.ref` on every refusal, so the client can tell which append failed.
It needs the authority the chain rules give the author for that op (section
9.3). The server requires the entry's author to be the
connection's own key, then appends it under the chain's rules. On success it
sends a `chain` frame holding just that entry (`more` unset) to every
authenticated connection, the author included; clients append it to their
verified copy, and a client that cannot do so ends the connection with
`bad_chain`. Refusals are `error` messages: `chain_conflict` (the entry does not
extend the head; fetch the new head and retry), `chain_full`, `forbidden`
(author mismatch or a rule refusal; the message is the reason) and
`bad_request` (malformed or otherwise rejected). Each carries the request's
`ref` (section 5).

Effects of an accepted entry: `grant`, `revoke` and the roles/default roles of
`config` apply to the next permission check; `allowed_keys` of a `config`
applies to the next authentication (members already connected stay); `ban`
sends every connection of that key `error {banned}`, removes it from voice and
closes it, and later authentications are refused with `banned`. A connection
that logs in while an entry is being appended still receives it: the server
registers the connection for live frames and sends it every entry after the
head it sent in the handshake, in one step that no append can interleave with.
The server name, channels and limits of a `config` entry are read at start-up
and apply from the next start; the exception is `history_days` and
`history_mb`, which apply at once (section 4.9a).

### 4.15 Screen share messages

A member of a voice call can share their screen. Signaling rides the same
WebSocket; the video travels over the same WebRTC connection as the audio
(negotiated as in section 4.13).

| Type          | Dir | Payload                                                          |
|---------------|-----|------------------------------------------------------------------|
| `share_start` | C   | `{}` the sender starts sharing their screen.                     |
| `share_stop`  | C   | `{}` the sender stops sharing.                                   |
| `watch`       | C   | `{"fingerprint": string}` watch that member's share. `""` stops watching. |

Semantics:

- `share_start` and `share_stop` only work while the sender is in a voice
  call; outside a call they are silently ignored. Each one changes the
  sender's `sharing` flag and the server re-broadcasts `voice_state`
  (section 4.12) to everyone.
- `watch` outside a call yields `error` `not_joined`.
- Leaving the call (or disconnecting) stops the share.
- `watch` names a member by fingerprint. The target must be sharing in the
  same voice channel as the sender, or the server answers `error`
  `not_sharing`. A client watches at most one share at a time; a new `watch`
  replaces the old one. `watch` with an empty fingerprint stops watching.
- A watch ends by itself when the sharer stops or leaves, or when the watcher
  leaves the call.
- Sharing and watching need no permission beyond being in the call (which
  itself needs the usual `join_channel` permission); there is no separate
  share permission.
- A client adds its video track to the call only after the server's
  `voice_state` shows the client's own `sharing: true`, so a refused share
  never puts an unanswerable video section into the call's negotiation.

Media:

- Video is **VP8**, RTP payload type **96**, with RTCP `nack` and `pli`
  feedback. The sharer sends one video track to the server.
- The server forwards a sharer's video **only to the members watching it**.
  Members who are not watching receive no video packets.
- When someone starts watching, the server asks the sharer for a keyframe
  (PLI), so the new watcher can start decoding quickly.
- Each viewer has one video transceiver. The server renegotiates with
  `rtc_offer` (section 4.13) only the first time a viewer gets video; after
  that it swaps the source on the same media section without renegotiating
  (when a viewer switches to another sharer, or the sharer stops and starts
  again). The stream id of the track (`video-<fingerprint>`) therefore names
  only the first sharer that viewer received. A client must identify the
  sharer from its own `watch` request, never from the track's stream id or
  msid, and should expect a keyframe after each switch (the server requests
  it from the new sharer). Clients must handle server offers at any time.
- `video_source` (S): `{"fingerprint": string, "seq": uint16}`. Because the
  source behind a viewer's single track changes without renegotiation, and
  packets of the old source can still be in flight, the server marks the
  boundary explicitly. Whenever it attaches a source to a viewer's video
  (first attach or a switch), it sends `video_source` with that sharer's
  fingerprint and the RTP sequence number, exactly as the viewer receives it,
  of the new source's first packet. The server numbers the viewer's packets
  itself (continuous across switches) and sends no further packet of the
  previous source once the boundary is fixed. The message is queued before any
  packet of the new source is written, but the WebSocket and RTP travel
  separately, so it may arrive after the first packets. A client therefore
  waits for it after each `watch`, discards packets whose sequence number is
  before `seq` (16-bit serial-number order), restarts its depacketizer and
  decoder at `seq`, waits for a keyframe, and labels frames with `fingerprint`.
  `watch` may carry `"gen": uint32` (a client-chosen number, new for each
  effective watch request; omitted = 0). The server echoes the newest gen it
  has accepted in `video_source.gen`, and a client ignores a `video_source`
  whose gen is not its current one (0 = server or client without gens), so a
  boundary announced for an earlier watch of the same sharer is not mistaken
  for the current one. A repeated `watch` of the sharer already watched
  changes nothing except that, with a new gen, the server repeats the current
  `video_source` under it. An `error` that refuses a `watch` (`not_sharing`,
  `not_joined`) carries the request's gen in `error.gen` (omitted when the
  request had none); a client acts on it only if it matches its pending
  watch. A `watch` refused with `not_sharing` changes
  nothing: the previous source keeps streaming, and the client keeps (or
  restores) its previous target.
  When a viewer's source is detached (`watch` with `""`, or the sharer
  stopped) the server sends `{"fingerprint": "", "seq": 0}`; clients may
  ignore it. A client that gets no
  `video_source` for about 3 seconds after `watch` may fall back to restarting
  on the first packet it receives.
- A server refuses an `rtc_offer` that contains a video section it has no
  codec for with `error` `bad_request` "no common video codec; media reset,
  send a new rtc_offer", and the client recovers as in section 4.13.
- Hermec does not send audio from the shared screen.

### 4.16 Host list and moving: `hosts`, `hb`, `move`, `reach`

A server can have several hosts (section 9.3, `host_add`). Exactly one is
**active** and serves clients. At most one of the others is the **standby**.

`hosts` (S): `{active: HostInfo, standby?: HostInfo}`. A `HostInfo` is
`fingerprint`, `name`, `addrs` (the `ws://` or `wss://` URLs the host can be
reached at) and `priority` (the chain priority; lower is preferred). The active
host sends `hosts` right after `auth_ok` (not to a warm session), whenever the
standby changes, and after a failover. A standby or idle host that refuses a
login answers with `hosts` and then `error` `not_active`, so the client knows
where to go. Clients keep the addresses they learn and show the active host's
name ("hosted by").

`hb` (S): `{seq}`. The active host sends it to every authenticated client, warm
session and link every **500 ms**. A client that sees no `hb` for **1500 ms**
(three missed beats) treats the active host as lost. A client waits for its
first `hb` before it starts counting, so a host that sends none is never judged
lost.

`reach` (C): `{fingerprint, ok}`. A client reports whether its warm dial to the
host with that fingerprint worked. The active host picks the standby as the
best-priority linked host that at least half of its authenticated clients can
reach (a client that has not reported counts as reaching it). `reach` for a key
that is not a host is ignored.

`move` (S): `{to: HostInfo, reason}`. The active host sends it to every client
and then closes their connections when it stops being active. `reason` is
`"handback"` (the role goes back to a higher-priority host) or `"merge"` (a
split-brain yield, section 4.18). A client dials the host in `to`. If the
client's own chain entry was dropped in a merge, `error` `chain_lost` comes
first.

When a standby becomes active it sends `hosts` to its clients and adds a system
line "host changed to `<name>`" to every text channel (`chat_message` with
`system: true`).

### 4.17 Warm sessions and takeover

A **warm session** is a client's second connection, to the standby. It lets the
client switch hosts without a full login.

1. The client dials the standby and runs the normal handshake with
   `auth.warm = true`. A key that is on the chain's host list needs no password
   proof when it authenticates warm (the chain vouches for it); any other key
   must prove the password as usual.
2. The standby holds the session apart from the live ones. It counts against
   its own warm cap, which is `max_clients`, not the live cap. Promotion takes a
   live slot; a session that finds no slot gets `server_full` and is closed.
3. A warm session receives only `chain` and `hb` frames. It may send
   `takeover`, `link_probe`, `link_hello` and `warm_*`; any other message gets
   `error` `not_active`.

**Warm media.** To have ICE and DTLS done before they are needed, the client
may open a data-channel-only WebRTC connection to the standby. `warm_offer`
(C, `{sdp}`) is answered by `warm_answer` (S, `{sdp}`), which carries the
host's candidates. The client's candidates go as `warm_candidate` (C,
`{candidate}`). The data channel is labelled `hermec-warm`; the client sends
the text `ping` every 5 s and the host answers `pong`, which keeps the NAT
binding open. The server never sends `warm_offer`. After promotion, the
client's next `voice_join` adopts this connection, and one `rtc_offer` and
`rtc_answer` add the media (section 4.13). `warm_*` on a session that is not
warm gets `bad_request`.

**Takeover.** `takeover` (C, `{}`) asks the standby to become active. The host
answers:

- success: it promotes every warm session to a full session and sends `hosts`
  (the new active host is itself). A repeated `takeover` on an already
  promoted host just gets `hosts`.
- `active_alive`: this host is the standby, but its link shows that the active
  host is still alive. The client should redial the active host.
- `not_standby`: this host is not the standby (or was never promoted and is
  active already).
- `host_not_authorized`: this host's key is no longer on the chain.

The standby judges the active host dead with the same 1500 ms heartbeat rule on
its link (section 4.18). A client therefore retries a refused `takeover` for a
few seconds.

### 4.18 Host link

A **link** connects a host to the active host. It mirrors the chain, the
password key and the chat history, and tells the active host that this host is
available.

**Opening.** A host dials the active host, runs the handshake with
`auth.warm = true` and its host key, and sends `link_hello` (C):

| Field             | Type     | Description                                    |
|-------------------|----------|------------------------------------------------|
| `chain_seq`       | integer  | Seq of the sender's chain head.                |
| `history_last_id` | string   | ID of the sender's newest stored chat line.    |
| `history_last_ts` | string   | Its `ts`. Both empty for a full history.       |
| `addrs`           | string[] | URLs the sender can be dialed at.              |
| `name`            | string   | The sender's host name.                        |
| `yield`           | object   | Optional. Set only on a merge link (below): `{voice_members, clients, priority}`, the loser's load. |

The active host refuses with `error` `forbidden` (the key is not on the host
list, or is banned), `not_active` (it is not active, or, for a merge link, it
would itself yield to the sender) or `bad_chain` (the sender's chain is ahead of
its own). Otherwise the warm session becomes a link: it counts against neither
cap. The active host then sends, in order:

1. `chain` frames with every entry after `chain_seq`.
2. `link_sync` with `password_key` (the 32-byte key of section 3.1a; empty means
  the server has no password) and `more: true`. A standby takes the key from
  this first `link_sync`.
3. `link_sync` frames with `messages` (chat lines, oldest first), each under
  60 KiB, after `history_last_ts` and `history_last_id`. `more` is true while
  frames follow. Lines accepted meanwhile also arrive live, so a receiver merges
  by `id`.
4. `link_role` and `hosts`.

From then on the link carries every new `chain` entry, every new chat line as a
one-message `link_sync`, `hb`, and `link_role` and `hosts` whenever they change.

`link_role` (S): `{standby: bool}`. Tells the linked host whether it is now the
standby.

`hb` (both ways): each side sends `hb` every 500 ms. A host calls the active
host dead after 1500 ms without any frame from it.

**Probe.** `link_probe` (C, `{}`) can be sent on any warm session by a host. The
reply `link_probe_reply` (S) is `{active, server_id, voice_members, clients,
priority, self: HostInfo}`. A probe by a key that is not a host gets `forbidden`.
Hosts probe to find the active host at start-up and to look for a second active
host (split brain). The probe's `server_id` must match the prober's own.

**Handover.** `handover` (S, `{}`) on a link tells the linked host to become
active at once (no `active_alive` check). The sender first stops accepting
writes (it is idle), sends `handover`, and waits up to 3 s for the receiver to
report itself active in a probe; if that does not happen it resumes the active
role. After it succeeds, the old active host sends `move` (reason `"handback"`)
to its clients and links to the new active host as idle or standby. Handback
happens when voice has been empty for 2 minutes and a linked host with a
better priority exists.

**Split brain and merge.** Every 30 s an active host probes the hosts it knows.
If it finds another active host of the same server that is also on its chain,
and its own load loses (fewer voice members; tie: fewer clients; tie: the
larger priority number), it yields to that host:

1. It opens a link with `link_hello.yield` set, which carries its load. The
   winner refuses (`not_active`) if the winner would lose by that load.
   The loser re-probes just before committing and stops if it no longer loses.
2. It becomes idle, so nothing is written after this point.
3. If the winner's chain is a strict prefix of the loser's, the loser sends its
   extra entries as `chain` frames on the link (the winner appends those that
   extend its head). Otherwise it keeps the winner's chain and drops entries
   the winner lacks.
4. It sends its chat lines to the winner as `link_sync` with `messages`. The
   winner merges them by `id` and `ts`, and passes them to its other links.
5. It sends `move` (reason `"merge"`) to its clients, preceded by `chain_lost`
   for the authors of dropped entries, closes them, then links to the winner as
   idle or standby.

Chat lines the winner receives this way show up for its clients when they next
join the channel.

## 5. Errors

| Code          | Meaning                                                              |
|---------------|----------------------------------------------------------------------|
| `auth_failed` | Authentication failed; the connection is closed after this message. |
| `update_required` | Sent by a server to a version 1 client (section 7); the connection is closed. |
| `server_outdated` | Raised by a client that finds the host speaks an older protocol version. Never sent on the wire. |
| `server_id_mismatch` | Raised by a client when the host's genesis hash is not the server ID it has pinned, or `auth_ok.server_id` differs from the verified one. Never sent on the wire. |
| `host_not_authorized` | Raised by a client when the host's key is not on the chain's host list or its proof signature is invalid. Never sent on the wire. |
| `bad_chain` | Raised by a client when the chain it received does not verify (handshake or a live frame). Never sent on the wire. |
| `forbidden`   | The role lacks the required permission (`join_channel`, `send_chat`). |
| `bad_request` | Malformed message, unknown channel, unsupported message type, or chat text over 4096 bytes (`message too long`). |
| `not_joined`  | The operation needs channel membership the client does not have (`leave`, `chat_send`, `history_before`, voice messages). |
| `not_sharing` | `watch` named a member who is not sharing in the sender's voice channel. |
| `voice_full`  | `voice_join` named a channel already at its participant cap (`max_voice`, default 25). |
| `server_full` | Authentication was refused because the server is at its `max_clients` cap (default 256); the connection is closed after this message. |
| `chain_conflict` | `chain_append` did not extend the chain head. |
| `chain_full`  | `chain_append` would exceed the chain's size cap. |
| `banned`      | The key is banned; sent on authentication and to a live member when banned, then the connection is closed. |
| `server_busy` | The host will not send its chain to this IP now (section 3.1a), or an HTTP 503 refusal. Retry later. |
| `not_active`  | The host is not the active host. Sent after authentication, preceded by a `hosts` frame that names the active host (section 4.16), and for any client message other than the few a standby allows; the connection is closed after the refusal at login. |
| `active_alive` | `takeover` was refused because the active host is still alive (section 4.17). |
| `not_standby` | `takeover` was refused because this host is not the standby (section 4.17). |
| `chain_lost`  | A split-brain merge dropped a chain entry the receiver authored: "your change was lost during a network split; please redo it". Sent just before `move` (section 4.16). |
| `rate_limited`| The client is sending too fast; the connection is closed after this message. |

Except for `auth_failed`, `update_required`, `banned`, `server_full`,
`server_busy` and `rate_limited`, errors do not close the connection (a
`not_active` at login does). `server_outdated`,
`server_id_mismatch`, `host_not_authorized` and `bad_chain` end the attempt on
the client side; clients do not retry them automatically.

**Error correlation.** `chain_append` carries a `ref` and its errors echo it.
Other requests carry no ID, so a client attributes their errors to its oldest
pending request (FIFO). That is correct only because the server processes a
connection's messages in order and answers each failing request with at most
one error. Clients should not pipeline other requests whose failure they must
distinguish.

## 6. Roles and permissions

Servers map fingerprints to roles and roles to permissions. The permissions
are:

| Permission     | Grants                       |
|----------------|------------------------------|
| `join_channel` | Sending `join`.              |
| `send_chat`    | Sending `chat_send`.         |
| `manage`       | Appending `config`, `grant`, `revoke`, `ban` and `unban` chain entries (section 9.3). |

Builtin roles: `admin` (all three), `user` and `bot` (`join_channel`,
`send_chat`). A fingerprint with no explicit grant receives the server's
default roles (`user` unless configured otherwise). Roles are re-evaluated on
each request. Unknown role names grant nothing.

## 7. Versioning

The envelope carries `v`. The current version is 2. A server that
receives an unsupported version treats the message as malformed
(`bad_request`). Within a version, new optional fields may be added to payloads
and receivers must ignore unknown fields and unknown message types. An
incompatible change needs a new version number.

Version 1 peers are handled during the handshake only:

- The `challenge` is always sent as a version 1 envelope, so a version 1 client
  can read it. A version 1 client answers with `auth` (version 1); the server
  replies with one version 1 `error` `update_required` ("update HERMEC to
  connect to this server") and closes. Anything else sent as version 1 is an
  ordinary authentication failure.
- A version 2 client that meets a version 1 host gets no version 2 `chain`
  frame after `host_proof_req`; it ends the attempt with `server_outdated`
  ("the host runs an older HERMEC").
- A server accepts version 1 envelopes only for that first handshake message.
  After it, every message must be version 2.

Every member of a group therefore needs a release that speaks version 2.

## 8. Discovery

Servers may announce themselves to clients on the same LAN over UDP. Discovery
is independent of the websocket protocol and its `v` envelope.

**Probe.** A client sends one UDP datagram whose payload is exactly the 17
ASCII bytes `HERMEC_DISCOVER_1` to the server's port (the same port number as
the websocket listener, UDP instead of TCP), typically by broadcast or to a
known host. Any other payload, including one with extra bytes, is dropped
without a reply.

**Reply.** The server answers the probe's source address, and only that
address, with exactly one UDP datagram of at most 512 bytes containing a JSON
object:

```json
{"hermec":1,"name":"Lab","port":7697,"ver":"dev"}
```

| Field    | Meaning                                                              |
|----------|----------------------------------------------------------------------|
| `hermec` | Discovery format version; must be `1`.                               |
| `name`   | Human-readable server name, at most 64 characters.                   |
| `port`   | The websocket port, 1-65535. Connect to the reply's source IP on it. |
| `ver`    | The server's software version string.                                |

Clients must reject replies that are larger than 512 bytes, have `hermec` other
than `1`, a port outside 1-65535, or a name over 64 characters. A reply
carries no secrets and does not imply the client may connect; authentication
is unchanged.

**Amplification.** UDP source addresses can be forged, so a responder can be
used to reflect traffic at a victim. The probe is 17 bytes and a reply may be
up to 512 bytes, a worst-case amplification of about 30x (a typical reply is
roughly 70 bytes, about 4x). Mitigations: replies go only to the probe's
source address (never broadcast), one reply per probe, at most 10 replies per
second per source IP (burst 10), and operators can disable the responder
entirely with `discoverable = false`.

**Configuration.** Two server TOML keys control it: `discoverable` (boolean,
default `true`; `false` binds no UDP socket at all) and `server_name` (string,
at most 64 characters; default is the server's listen `host:port`). The
responder binds after the TCP listener, to the same port number and on the same interface as the server address (a
loopback server answers only on loopback; only a wildcard server address binds
the responder to all interfaces).

## 9. Server chain

A server's identity and group configuration live in a signed, append-only
chain. The reference implementation is `core/chain`.

### 9.1 Server ID

The server ID is the SHA-256 hash of the genesis entry (section 9.2). Its
string form is the 32 bytes in lowercase RFC 4648 base32 without padding (52
characters); that form is what `auth_ok.server_id` and client pins carry. Apps
show a short form, `srv-` plus the first 10 bytes in hyphenated groups of four,
like a fingerprint. In the handshake signatures the ID is the raw 32 bytes.

### 9.2 Entries

An entry is a JSON object:

| Field    | Type    | Description                                              |
|----------|---------|----------------------------------------------------------|
| `seq`    | integer | 0 for genesis, then 1, 2, and so on.                     |
| `prev`   | base64  | Hash of the previous entry; omitted in genesis.          |
| `time`   | integer | Unix milliseconds.                                       |
| `author` | base64  | The signer's 32-byte Ed25519 public key.                 |
| `op`     | string  | The operation, see section 9.3.                          |
| `body`   | base64  | The compact JSON of the op's body struct, base64 encoded, so the exact bytes that were signed survive any re-encoding. |
| `sig`    | base64  | Ed25519 signature by `author` over the signed bytes.     |

The **signed bytes** are `"hermec-chain-v1\x00"` followed by the compact JSON
(no whitespace, no HTML escaping) of the entry's fields except `sig`, in the
order `seq`, `prev`, `time`, `author`, `op`, `body`. The **entry hash** is
SHA-256 over the signed bytes followed by `sig`; `prev` is the hash of the
previous entry. An entry must carry a valid signature and a `body` that is
compact JSON. Anyone can verify the whole chain from the genesis entry alone.

### 9.3 Operations

Authority is checked against the chain state just before the entry. The founder
(the genesis author) may sign every op after genesis. A banned key may sign
nothing.

| Op            | Body                                                         | Who may sign |
|---------------|--------------------------------------------------------------|--------------|
| `genesis`     | `name`, `founder` (the author's key), `salt` (random bytes)  | the founder; entry 0 only |
| `config`      | the full group config: `name`, `channels`, `voice_channels`, `max_voice`, `max_clients`, `max_per_ip`, `allowed_keys`, `roles`, `default_roles`, `history_days` (default 30; 0 keeps no history; at most 3650), `history_mb` (default 8; 1 to 64) | founder, or a key with `manage` |
| `grant`       | `fingerprint`, `roles`; replaces that key's roles. A grant naming the founder is refused unless the founder signs it | founder, or `manage` |
| `revoke`      | `fingerprint`; removes that key's grant. The founder cannot be revoked | founder, or `manage` |
| `ban`         | `fingerprint`. The founder cannot be banned                  | founder, or `manage` |
| `unban`       | `fingerprint`                                                | founder, or `manage` |
| `host_add`    | `pub_key`, `priority` (at least 1); re-adding a key updates its priority | founder only |
| `host_remove` | `fingerprint` of a listed host                               | founder only |

A server treats the founder as holding every permission whatever the grants say.
The latest `config` wins; it is the whole config, not a diff. The password is
never in the chain: the chain is sent to every client before it authenticates.
The state after replaying the chain also holds the grants, the ban list and the
host list (ordered by priority, then fingerprint).

### 9.4 Caps

A chain holds at most 2048 entries (genesis included) and 512 KiB (the sum of
the entries' encoded JSON lines, newline included). An append past either cap
is refused with `chain_full`, and a received chain that exceeds them is
rejected. The handshake sends the chain in frames of under 60 KiB of entries.
Before it has proven itself, a host may send a client at most 64 chain frames
and 2 MiB of entries; beyond that the client ends the attempt with `bad_chain`.
