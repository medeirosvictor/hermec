# Hermec Wire Protocol, version 2

This document specifies the Hermec signaling/chat protocol, version 2. It
covers the mutual handshake, the server chain (section 9), channels, presence,
text chat, the server roster, voice signaling (section 4.12) and screen share
signaling (section 4.15). Webcam is not specified yet.

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
  | ---------------- auth -------------> |   (5) pubkey, name, sig, password?
  | <-------------- auth_ok ------------ |   (6) or error + close
  |                                      |
  | ---------------- join -------------> |
  | <------------- presence ------------ |   broadcast to all channel members
  | ------------- chat_send -----------> |
  | <----------- chat_message ---------- |   broadcast to all channel members
```

### 3.1 Handshake

The host proves it belongs to the server before the client sends its password.
Nonces are 32 random bytes. The whole handshake must finish within **10
seconds** of the upgrade. `server_id` is the raw 32 bytes of the server ID
(section 9.1) in both signed strings. Each signed string starts with its own
domain prefix, ending in a NUL byte (`\x00`), so a signature made for one
purpose cannot be replayed as another.

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
   name and, if the server has one, the password.
6. **`auth_ok`** (S), or one `error` and a close. `auth_ok` carries the
   `server_id`; the client checks it against the ID it verified in step 4 and
   ends the connection with `server_id_mismatch` if it differs.

The server checks `auth` in this order: it is a well-formed version 2 `auth`
with a 32-byte `pubkey`; the signature is valid; the key is not banned; the
password (constant-time comparison) if one is configured; the key's fingerprint
is on the allow list if one is configured. A bad signature, wrong password or
unlisted key gets a single `error` with code `auth_failed` and the connection
closes; the error never reveals which check failed. A banned key, once its
signature has proven it holds the key, gets `error` `banned` instead.

Any message other than `host_proof_req` and `auth`, in that order, is an
authentication failure. Timeout or disconnect before `auth` simply closes the
connection.

**Failed logins.** After 5 failed logins in a row from one IP (forgotten after a quiet minute), that IP
is refused for 1 s. Each further failure doubles the wait, up to 60 s. A
successful login resets the count. While an IP is refused, its connection
attempts get HTTP 503 instead of a WebSocket.

**Fingerprint.** A key's identity is its fingerprint: the lowercase RFC 4648
base32 (no padding) encoding of the first 10 bytes of `SHA-256(pubkey)`,
split into hyphen-separated groups of four characters, for example
`k7mv-q3xp-9dfw-02hj`. Allow lists, role grants and presence use fingerprints.

### 3.2 After authentication

The client may send `join`, `leave` and `chat_send`. The server sends
`presence` and `chat_message` for channels the client has joined. A
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
| `password` | string | no       | Server password, if one is configured.    |

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

### 4.5 `join` (C)

| Field     | Type   | Description         |
|-----------|--------|---------------------|
| `channel` | string | Channel to join.    |

Requires the `join_channel` permission. On success the server broadcasts
`presence` for the channel, including to the joiner. Joining a channel the
client is already in is not an error; it produces another `presence`.
Unknown channels yield `error` `bad_request`.

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
| `channel` | string | Channel name.                                 |
| `from`    | Member | Sender (see 4.7).                             |
| `text`    | string | Message body.                                 |
| `ts`      | string | Server receive time, RFC 3339 UTC.            |

Broadcast to every member of the channel, including the sender. Messages are
delivered in a single consistent order to all members. The server keeps no
history; members who join later do not receive earlier messages.

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

`chain_append` carries one signed chain entry (`{entry}`, the entry as it
appears in a `chain` frame, section 9.2), built on the head the sender holds.
It needs the authority the chain rules give the author for that op (section
9.3). The server requires the entry's author to be the
connection's own key, then appends it under the chain's rules. On success it
sends a `chain` frame holding just that entry (`more` unset) to every
authenticated connection, the author included; clients append it to their
verified copy, and a client that cannot do so ends the connection with
`bad_chain`. Refusals are `error` messages: `chain_conflict` (the entry does not
extend the head; fetch the new head and retry), `chain_full`, `forbidden`
(author mismatch or a rule refusal; the message is the reason) and
`bad_request` (malformed or otherwise rejected). Like other errors they carry no
correlation id (section 5).

Effects of an accepted entry: `grant`, `revoke` and the roles/default roles of
`config` apply to the next permission check; `allowed_keys` of a `config`
applies to the next authentication (members already connected stay); `ban`
sends every connection of that key `error {banned}`, removes it from voice and
closes it, and later authentications are refused with `banned`. The server name,
channels and limits of a `config` entry are read at start-up and apply from the
next start.

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
| `not_joined`  | The operation needs channel membership the client does not have.     |
| `not_sharing` | `watch` named a member who is not sharing in the sender's voice channel. |
| `voice_full`  | `voice_join` named a channel already at its participant cap (`max_voice`, default 25). |
| `server_full` | Authentication was refused because the server is at its `max_clients` cap (default 256); the connection is closed after this message. |
| `chain_conflict` | `chain_append` did not extend the chain head. |
| `chain_full`  | `chain_append` would exceed the chain's size cap. |
| `banned`      | The key is banned; sent on authentication and to a live member when banned, then the connection is closed. |
| `rate_limited`| The client is sending too fast; the connection is closed after this message. |

Except for `auth_failed`, `update_required`, `banned`, `server_full` and
`rate_limited`, errors do not close the connection. `server_outdated`,
`server_id_mismatch`, `host_not_authorized` and `bad_chain` end the attempt on
the client side; clients do not retry them automatically.

**Known limitation: no error correlation.** Messages carry no request or
correlation ID, so an `error` cannot be tied to the request that caused it.
Clients attribute each error to their oldest pending request (FIFO), which is
correct only because the server processes a connection's messages in order
and answers each failing request with at most one error. Clients should not
pipeline requests whose failure they must distinguish. A future protocol
version may add optional request IDs.

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
| `config`      | the full group config: `name`, `channels`, `voice_channels`, `max_voice`, `max_clients`, `max_per_ip`, `allowed_keys`, `roles`, `default_roles` | founder, or a key with `manage` |
| `grant`       | `fingerprint`, `roles`; replaces that key's roles            | founder, or `manage` |
| `revoke`      | `fingerprint`; removes that key's grant. The founder cannot be revoked | founder, or `manage` |
| `ban`         | `fingerprint`. The founder cannot be banned                  | founder, or `manage` |
| `unban`       | `fingerprint`                                                | founder, or `manage` |
| `host_add`    | `pub_key`, `priority` (at least 1); re-adding a key updates its priority | founder only |
| `host_remove` | `fingerprint` of a listed host                               | founder only |

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
