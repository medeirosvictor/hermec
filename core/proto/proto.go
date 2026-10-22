// Package proto defines the Hermec wire protocol: a versioned JSON envelope
// and the payload structs exchanged over WebSocket.
package proto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	// Version is the protocol version carried in every envelope.
	Version = 2
	// LegacyVersion is the previous protocol version. Only the handshake
	// (DecodeHandshake / EncodeV) still speaks it, to tell old peers to update.
	LegacyVersion = 1
	// MaxMessageSize is the largest accepted raw message, in bytes.
	MaxMessageSize = 64 * 1024
	// MaxRosterMembers caps the members listed in one Roster frame.
	MaxRosterMembers = 200
)

// Message types.
const (
	TypeChallenge     = "challenge"
	TypeAuth          = "auth"
	TypeAuthOK        = "auth_ok"
	TypeError         = "error"
	TypeJoin          = "join"
	TypeLeave         = "leave"
	TypePresence      = "presence"
	TypeChatSend      = "chat_send"
	TypeChatMessage   = "chat_message"
	TypeChannelList   = "channel_list"
	TypeHistory       = "history"
	TypeHistoryBefore = "history_before"
	TypeRoster        = "roster"

	TypeVoiceJoin    = "voice_join"
	TypeVoiceLeave   = "voice_leave"
	TypeVoiceState   = "voice_state"
	TypeVoiceMute    = "voice_mute"
	TypeRTCOffer     = "rtc_offer"
	TypeRTCAnswer    = "rtc_answer"
	TypeRTCCandidate = "rtc_candidate"

	// Handshake and signed server chain (protocol version 2).
	TypeHostProofReq = "host_proof_req"
	TypeHostProof    = "host_proof"
	TypeChain        = "chain"
	TypeChainAppend  = "chain_append"

	// Screen sharing.
	TypeShareStart = "share_start"
	TypeShareStop  = "share_stop"
	TypeWatch      = "watch"
	// TypeVideoSource (S) marks the boundary of a viewer's video stream.
	TypeVideoSource = "video_source"

	// Host failover: the host list, liveness, the host-to-host link and warm
	// standby sessions (WebRTC signalling for a standby's pre-connected media).
	TypeHosts          = "hosts"
	TypeHeartbeat      = "hb"
	TypeTakeover       = "takeover"
	TypeMove           = "move"
	TypeLinkHello      = "link_hello"
	TypeLinkSync       = "link_sync"
	TypeLinkRole       = "link_role"
	TypeLinkProbe      = "link_probe"
	TypeLinkProbeReply = "link_probe_reply"
	TypeReach          = "reach"
	TypeHandover       = "handover"
	TypeWarmOffer      = "warm_offer"
	TypeWarmAnswer     = "warm_answer"
	TypeWarmCandidate  = "warm_candidate"
)

// CodeNotSharing is the error code for a watch whose target is not sharing in
// the sender's voice channel.
const CodeNotSharing = "not_sharing"

// CodeVoiceFull is the error code for a voice_join to a channel at its
// participant cap (server config max_voice).
const CodeVoiceFull = "voice_full"

// CodeServerFull is the error code for an authentication refused because the
// server is at its authenticated-session cap (server config max_clients).
const CodeServerFull = "server_full"

// CodeAuthFailed is the error code for a rejected authentication (bad
// signature, wrong password, banned key). Retrying cannot succeed.
const CodeAuthFailed = "auth_failed"

// Error codes added with protocol version 2.
const (
	CodeUpdateRequired    = "update_required"
	CodeServerOutdated    = "server_outdated"
	CodeServerIDMismatch  = "server_id_mismatch"
	CodeHostNotAuthorized = "host_not_authorized"
	CodeBadChain          = "bad_chain"
	CodeChainConflict     = "chain_conflict"
	CodeChainFull         = "chain_full"
	CodeBanned            = "banned"
)

// Error codes added with the host failover messages.
const (
	CodeNotActive   = "not_active"
	CodeActiveAlive = "active_alive"
	CodeNotStandby  = "not_standby"
	CodeChainLost   = "chain_lost"
	CodeForbidden   = "forbidden"
	CodeBadRequest  = "bad_request"
)

// HostProofReq asks the peer to prove it holds a host key by signing Nonce.
type HostProofReq struct {
	Nonce []byte `json:"nonce"`
}

// HostProof answers a HostProofReq: PubKey is the host key, Sig is over the nonce.
type HostProof struct {
	PubKey []byte `json:"pubkey"`
	Sig    []byte `json:"sig"`
}

// ChainFrame carries chain entries, in order. Entries stay raw JSON so this
// package does not depend on core/chain. More is true when further frames follow.
type ChainFrame struct {
	Entries []json.RawMessage `json:"entries"`
	More    bool              `json:"more,omitempty"`
}

// ChainAppend proposes or announces one new chain entry (raw JSON).
type ChainAppend struct {
	Entry json.RawMessage `json:"entry"`
	Ref   string          `json:"ref,omitempty"`
}

// Reserved for future end-to-end encryption. They have no handler yet.
const (
	TypeE2EEKeyExchange = "e2ee_key_exchange"
	TypeE2EEFrame       = "e2ee_frame"
)

// Envelope is the single wire message shape.
type Envelope struct {
	V    int             `json:"v"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Challenge carries the server nonce the client must sign.
type Challenge struct {
	Nonce []byte `json:"nonce"`
}

// Auth is the client's reply to a Challenge. Sig is over the nonce.
type Auth struct {
	PubKey   []byte `json:"pubkey"`
	Name     string `json:"name"`
	Sig      []byte `json:"sig"`
	Password string `json:"password,omitempty"` // v0.6 clients only
	// Proof replaces Password: PasswordProof over the nonces and server ID.
	Proof []byte `json:"proof,omitempty"`
	// Warm marks a standby's pre-connection, which the host may hold open
	// before the client is promoted to a live session.
	Warm bool `json:"warm,omitempty"`
}

// ChannelInfo describes a server channel. Type is "text" or "voice".
type ChannelInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// AuthOK confirms authentication.
type AuthOK struct {
	Fingerprint string        `json:"fingerprint"`
	Roles       []string      `json:"roles"`
	Channels    []ChannelInfo `json:"channels"`
	// ServerName is the server's configured display name; empty if unset.
	ServerName string `json:"server_name,omitempty"`
	// ServerID is the server's chain identity (chain.ID.String() form).
	ServerID string `json:"server_id"`
}

// VoiceJoin requests joining a voice channel.
type VoiceJoin struct {
	Channel string `json:"channel"`
}

// VoiceLeave requests leaving the current voice channel.
type VoiceLeave struct{}

// VoiceMute sets the sender's mute state.
type VoiceMute struct {
	Muted bool `json:"muted"`
}

// VoiceMember is one participant of a voice channel.
type VoiceMember struct {
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name"`
	Muted       bool   `json:"muted"`
	Sharing     bool   `json:"sharing,omitempty"`
}

// ShareStart announces that the sender is sharing its screen.
type ShareStart struct{}

// ShareStop announces that the sender stopped sharing.
type ShareStop struct{}

// Watch says the sender now watches the sharer with this fingerprint; ""
// stops watching. A viewer watches one stream at a time.
type Watch struct {
	Fingerprint string `json:"fingerprint"`
	// Gen identifies this watch request; the server echoes the newest one in
	// VideoSource.Gen so a client can tell a stale boundary from a current one.
	// 0 (omitted) from clients that do not number their requests.
	Gen uint32 `json:"gen,omitempty"`
}

// VideoSource tells a viewer which sharer the packets on its (single) video
// track belong to from now on. Seq is the RTP sequence number, as the viewer
// receives it, of that source's first packet; every packet before it (serial
// number order) belongs to the previous source. Fingerprint "" means the
// viewer's video was detached (Seq is 0 and unused).
type VideoSource struct {
	Fingerprint string `json:"fingerprint"`
	Seq         uint16 `json:"seq"`
	// Gen is the Gen of the watch request this boundary answers (0 if the
	// client sent none).
	Gen uint32 `json:"gen,omitempty"`
}

// VoiceState is the full participant list of a voice channel.
type VoiceState struct {
	Channel string        `json:"channel"`
	Members []VoiceMember `json:"members"`
}

// RTCOffer carries an SDP offer.
type RTCOffer struct {
	SDP string `json:"sdp"`
}

// RTCAnswer carries an SDP answer.
type RTCAnswer struct {
	SDP string `json:"sdp"`
}

// RTCCandidate carries one ICE candidate (JSON-encoded candidate init).
type RTCCandidate struct {
	Candidate string `json:"candidate"`
}

// Warm standby signalling reuses the RTC payloads.
type (
	WarmOffer     = RTCOffer
	WarmAnswer    = RTCAnswer
	WarmCandidate = RTCCandidate
)

// ErrorMsg reports a failure. Codes: "auth_failed", "forbidden",
// "bad_request", "not_joined", "rate_limited", "not_sharing", "voice_full",
// "server_full", "update_required", "server_outdated", "server_id_mismatch",
// "host_not_authorized", "bad_chain", "chain_conflict", "chain_full", "banned",
// "not_active", "active_alive", "not_standby", "chain_lost".
type ErrorMsg struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Gen is the Gen of the watch request this error answers (only set for
	// refused watches whose request carried a gen).
	Gen uint32 `json:"gen,omitempty"`
	// Ref is the ID of the client request this error answers, when it has one.
	Ref string `json:"ref,omitempty"`
}

// HostInfo describes one server host: its key fingerprint, display name,
// dialable addresses and failover priority (lower is preferred).
type HostInfo struct {
	Fingerprint string   `json:"fingerprint"`
	Name        string   `json:"name"`
	Addrs       []string `json:"addrs"`
	Priority    int      `json:"priority"`
}

// Hosts lists the active host and, when one is set, the standby.
type Hosts struct {
	Active  HostInfo  `json:"active"`
	Standby *HostInfo `json:"standby,omitempty"`
}

// Heartbeat is the periodic liveness ping between hosts.
type Heartbeat struct {
	Seq uint64 `json:"seq"`
}

// Move tells a client to reconnect to another host. Reason is "handback"
// (the active host returns to its place) or "merge" (two hosts join).
type Move struct {
	To     HostInfo `json:"to"`
	Reason string   `json:"reason"`
}

// LinkHello opens a host-to-host link: the sender's chain position, the ID and
// time of its newest stored chat line, and how to dial it.
type LinkHello struct {
	ChainSeq      uint64    `json:"chain_seq"`
	HistoryLastID string    `json:"history_last_id"`
	HistoryLastTS time.Time `json:"history_last_ts"`
	Addrs         []string  `json:"addrs"`
	Name          string    `json:"name"`
	// Yield is set on the link a split-brain loser opens to merge into the
	// winner: the loser's load, so the winner can check it still wins.
	Yield *YieldLoad `json:"yield,omitempty"`
}

// YieldLoad is a host's load for the split-brain yield rule.
type YieldLoad struct {
	VoiceMembers int `json:"voice_members"`
	Clients      int `json:"clients"`
	Priority     int `json:"priority"`
}

// LinkSync carries chat history between linked hosts. PasswordKey is the
// derived key (never the password itself); More says further frames follow.
type LinkSync struct {
	PasswordKey []byte        `json:"password_key,omitempty"`
	Messages    []ChatMessage `json:"messages,omitempty"`
	More        bool          `json:"more,omitempty"`
}

// LinkRole sets whether the linked host is the standby (true) or the active.
type LinkRole struct {
	Standby bool `json:"standby"`
}

// LinkProbeReply reports a host's state: whether it is active, its chain
// identity, its load (voice members and clients) and its failover priority.
type LinkProbeReply struct {
	Active       bool     `json:"active"`
	ServerID     string   `json:"server_id"`
	VoiceMembers int      `json:"voice_members"`
	Clients      int      `json:"clients"`
	Priority     int      `json:"priority"`
	Self         HostInfo `json:"self"`
}

// Reach reports whether a host could reach the peer with this fingerprint.
type Reach struct {
	Fingerprint string `json:"fingerprint"`
	OK          bool   `json:"ok"`
}

// Handover tells the active host to hand its role to the standby.
type Handover struct{}

// Join requests joining a channel.
type Join struct {
	Channel string `json:"channel"`
}

// Leave requests leaving a channel.
type Leave struct {
	Channel string `json:"channel"`
}

// Member describes a connected user.
type Member struct {
	Fingerprint string   `json:"fingerprint"`
	Name        string   `json:"name"`
	Roles       []string `json:"roles"`
	PubKey      []byte   `json:"pub_key,omitempty"`
}

// Presence lists the members of a channel.
type Presence struct {
	Channel string   `json:"channel"`
	Members []Member `json:"members"`
	// Total is the real member count; Members may be a prefix of the list
	// when the full frame would not fit MaxMessageSize. Zero from old servers.
	Total int `json:"total,omitempty"`
}

// Roster lists the connected members server-wide, sorted by name then
// fingerprint. Members may be a prefix of the full list (at most
// MaxRosterMembers, and small enough to fit MaxMessageSize); Total is the
// real number of connections.
type Roster struct {
	Members []Member `json:"members"`
	Total   int      `json:"total"`
}

// ChatSend is a client's outgoing chat message.
type ChatSend struct {
	Channel string `json:"channel"`
	Text    string `json:"text"`
}

// ChatMessage is a chat message broadcast by the server.
type ChatMessage struct {
	ID      string    `json:"id,omitempty"`
	Channel string    `json:"channel"`
	From    Member    `json:"from"`
	Text    string    `json:"text"`
	TS      time.Time `json:"ts"`
	// System marks a host-change line (not a message from a member).
	System bool `json:"system,omitempty"`
}

// History is a page of stored chat lines for a text channel, oldest first.
// Before echoes the before_id it answers (empty for the join page); More says
// older lines remain.
type History struct {
	Channel  string        `json:"channel"`
	Before   string        `json:"before,omitempty"`
	Messages []ChatMessage `json:"messages"`
	More     bool          `json:"more,omitempty"`
}

// HistoryBefore asks for the page of lines older than BeforeID. BeforeTS is
// that line's ts, optional: the host uses it to place BeforeID when it no
// longer holds that line.
type HistoryBefore struct {
	Channel  string    `json:"channel"`
	BeforeID string    `json:"before_id"`
	BeforeTS time.Time `json:"before_ts,omitzero"`
}

// Encode wraps payload in a versioned envelope and marshals it.
func Encode(msgType string, payload any) ([]byte, error) {
	return EncodeV(Version, msgType, payload)
}

// EncodeV is Encode with an explicit envelope version, for the handshake
// frames that must be readable by a peer speaking LegacyVersion.
func EncodeV(v int, msgType string, payload any) ([]byte, error) {
	data, err := marshalNoEscape(payload)
	if err != nil {
		return nil, fmt.Errorf("proto: marshal payload: %w", err)
	}
	return marshalNoEscape(Envelope{V: v, Type: msgType, Data: data})
}

// marshalNoEscape is json.Marshal without HTML escaping: '<', '>' and '&' stay
// one byte instead of growing to six, so frames stay predictably small. The
// envelope is encoded the same way because marshalling a json.RawMessage
// re-escapes it.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// Decode parses and validates a raw envelope. It returns an error on
// oversized input, invalid JSON, a version mismatch, or an empty type.
func Decode(raw []byte) (Envelope, error) {
	return decode(raw, false)
}

// DecodeHandshake is Decode but also accepts LegacyVersion envelopes, so the
// handshake can answer an old peer with update_required instead of dropping it.
func DecodeHandshake(raw []byte) (Envelope, error) {
	return decode(raw, true)
}

func decode(raw []byte, allowLegacy bool) (Envelope, error) {
	if len(raw) > MaxMessageSize {
		return Envelope{}, fmt.Errorf("proto: message too large (%d > %d bytes)", len(raw), MaxMessageSize)
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Envelope{}, fmt.Errorf("proto: invalid envelope: %w", err)
	}
	if env.V != Version && !(allowLegacy && env.V == LegacyVersion) {
		return Envelope{}, fmt.Errorf("proto: unsupported version %d", env.V)
	}
	if env.Type == "" {
		return Envelope{}, errors.New("proto: empty message type")
	}
	return env, nil
}
