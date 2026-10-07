# Hermec Wire Protocol, version 1

This document specifies the Hermec signaling/chat protocol, version 1. It
covers the chat subset implemented by the foundation release: authentication,
channels, presence and text chat, plus voice signaling (section 4.12).
Screenshare is specified separately, later.

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
- Keepalive: the server sends a WebSocket ping every 15 seconds and closes the
  connection on the tick after two consecutive unanswered pings (about 45
  seconds after the last pong), without sending a third. Clients must
  answer pings (standard WebSocket libraries do so automatically).
- The default port is 7697.

## 2. Envelope

```json
{"v": 1, "type": "chat_send", "data": {"channel": "general", "text": "hi"}}
```

| Field  | Type    | Description                                              |
|--------|---------|----------------------------------------------------------|
| `v`    | integer | Protocol version. Must be `1`.                           |
| `type` | string  | Message type, non-empty. See section 4.                  |
| `data` | object  | Type-specific payload. Omitted when the payload is empty. |

A message is malformed if it exceeds the size limit, is not valid JSON, has
`v` other than 1, or has an empty `type`.

Conventions:

- `[]byte` fields (`nonce`, `pubkey`, `sig`) are encoded as standard base64
  JSON strings.
- Timestamps are RFC 3339 (UTC from the server).
- Unknown fields inside `data` are ignored.

## 3. Connection flow

```
Client                                   Server
  | -------- WebSocket upgrade --------> |
  | <-------------- challenge ---------- |   random 32-byte nonce
  | ---------------- auth -------------> |   pubkey, name, sig(nonce), password?
  | <-------------- auth_ok ------------ |   or error(auth_failed) + close
  |                                      |
  | ---------------- join -------------> |
  | <------------- presence ------------ |   broadcast to all channel members
  | ------------- chat_send -----------> |
  | <----------- chat_message ---------- |   broadcast to all channel members
```

### 3.1 Authentication

1. Immediately after the upgrade the server sends `challenge` with a fresh
   32-byte random nonce.
2. The client must reply within **10 seconds** with `auth`:
   - `pubkey`: its 32-byte Ed25519 public key.
   - `sig`: an Ed25519 signature over the raw nonce bytes.
   - `name`: a display name (not unique, not authenticated).
   - `password`: required only if the server has a password configured.
3. The server verifies, in this order: the message is a well-formed `auth`;
   `pubkey` is 32 bytes; the signature is valid; the password (constant-time
   comparison) if one is configured; the key's fingerprint is on the allow
   list if one is configured.
4. On success the server sends `auth_ok`. On any failure it sends a single
   `error` with code `auth_failed` and closes the connection. The error never
   reveals which check failed.

Any message other than `auth` before authentication is an authentication
failure. Timeout or disconnect before `auth` simply closes the connection.

**Fingerprint.** A key's identity is its fingerprint: the lowercase RFC 4648
base32 (no padding) encoding of the first 10 bytes of `SHA-256(pubkey)`,
split into hyphen-separated groups of four characters, for example
`k7mv-q3xp-9dfw-02hj`. Allow lists, role grants and presence use fingerprints.

### 3.2 After authentication

The client may send `join`, `leave` and `chat_send`. The server sends
`presence` and `chat_message` for channels the client has joined. A
connection that sends anything else receives `error` (`bad_request`).
Disconnecting leaves all channels; remaining members receive a new `presence`.

## 4. Messages

Direction: C is client to server, S is server to client.

### 4.1 `challenge` (S)

| Field   | Type   | Description                        |
|---------|--------|------------------------------------|
| `nonce` | base64 | 32 random bytes to be signed.      |

### 4.2 `auth` (C)

| Field      | Type   | Required | Description                               |
|------------|--------|----------|-------------------------------------------|
| `pubkey`   | base64 | yes      | 32-byte Ed25519 public key.               |
| `name`     | string | yes      | Display name.                             |
| `sig`      | base64 | yes      | Ed25519 signature of the challenge nonce. |
| `password` | string | no       | Server password, if one is configured.    |

### 4.3 `auth_ok` (S)

| Field         | Type          | Description                          |
|---------------|---------------|--------------------------------------|
| `fingerprint` | string        | The authenticated key's fingerprint. |
| `roles`       | string[]      | Roles assigned to this fingerprint.  |
| `channels`    | ChannelInfo[] | All channels on the server.          |

Changed in the voice release (breaking, protocol version stays 1):
`channels` was previously an array of channel-name strings and is now an array
of `ChannelInfo` objects. Clients and servers must be updated together; see
section 7.

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

| Field     | Type   | Description          |
|-----------|--------|----------------------|
| `channel` | string | Channel to leave.    |

On success the remaining members receive `presence`; the leaver does not.
If the client was not in the channel the server replies `error` `not_joined`.

### 4.7 `presence` (S)

| Field     | Type     | Description                                      |
|-----------|----------|--------------------------------------------------|
| `channel` | string   | Channel name.                                    |
| `members` | Member[] | Full current member list, sorted by name then fingerprint. |

`presence` always carries the complete member list, not a delta. A `Member`:

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
the `send_chat` permission (`forbidden` otherwise). The only length limit on
`text` is the 64 KiB message size limit.

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

Defined in `core/proto` but not used by v1 servers or clients: the channel
list is delivered in `auth_ok`. Reserved for dynamic channel lists. v1
servers must not send it, and a v1 server that receives it treats it as an
unsupported type (`bad_request`).

### 4.11 Reserved: `e2ee_key_exchange` and `e2ee_frame`

Reserved for future end-to-end encryption. In v1 these types are **reserved,
v1 servers must reject**: a v1 server answers with `error`
`bad_request` and does not relay them. They are never to be sent by v1 clients.

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

`VoiceMember`: `fingerprint` (string), `name` (string), `muted` (bool).

Semantics:

- `voice_join` targets a channel whose `type` is `voice`; unknown or non-voice
  channels yield `bad_request`. It requires the `join_channel` permission.
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

## 5. Errors

| Code          | Meaning                                                              |
|---------------|----------------------------------------------------------------------|
| `auth_failed` | Authentication failed; the connection is closed after this message. |
| `forbidden`   | The role lacks the required permission (`join_channel`, `send_chat`). |
| `bad_request` | Malformed message, unknown channel, or unsupported message type.     |
| `not_joined`  | The operation needs channel membership the client does not have.     |
| `rate_limited`| The client is sending too fast; the connection is closed after this message. |

Except for `auth_failed` and `rate_limited`, errors do not close the connection.

**Known v1 limitation: no error correlation.** Messages carry no request or
correlation ID, so an `error` cannot be tied to the request that caused it.
Clients attribute each error to their oldest pending request (FIFO), which is
correct only because the server processes a connection's messages in order
and answers each failing request with at most one error. Clients should not
pipeline requests whose failure they must distinguish. A future protocol
version may add optional request IDs.

## 6. Roles and permissions

Servers map fingerprints to roles and roles to permissions. The v1 permissions
are:

| Permission     | Grants                       |
|----------------|------------------------------|
| `join_channel` | Sending `join`.              |
| `send_chat`    | Sending `chat_send`.         |
| `manage`       | Reserved for administration. |

Builtin roles: `admin` (all three), `user` and `bot` (`join_channel`,
`send_chat`). A fingerprint with no explicit grant receives the server's
default roles (`user` unless configured otherwise). Roles are re-evaluated on
each request. Unknown role names grant nothing.

## 7. Versioning

The envelope carries `v`. A server that receives an unsupported version treats
the message as malformed (`bad_request`). Within a version, new optional
fields may be added to payloads and receivers must ignore unknown fields.
The voice release added new message types (section 4.12) and changed the
`auth_ok` `channels` shape (a breaking change) without bumping the version,
because the protocol is pre-release; future incompatible changes require a new
version number.

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
