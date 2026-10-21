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
	TypeChallenge   = "challenge"
	TypeAuth        = "auth"
	TypeAuthOK      = "auth_ok"
	TypeError       = "error"
	TypeJoin        = "join"
	TypeLeave       = "leave"
	TypePresence    = "presence"
	TypeChatSend    = "chat_send"
	TypeChatMessage = "chat_message"
	TypeChannelList = "channel_list"
	TypeRoster      = "roster"

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
}

// Reserved for future end-to-end encryption. They have no handler in v1.
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
	Password string `json:"password,omitempty"`
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

// ErrorMsg reports a failure. Codes: "auth_failed", "forbidden",
// "bad_request", "not_joined", "rate_limited", "not_sharing", "voice_full",
// "server_full", "update_required", "server_outdated", "server_id_mismatch",
// "host_not_authorized", "bad_chain", "chain_conflict", "chain_full", "banned".
type ErrorMsg struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Gen is the Gen of the watch request this error answers (only set for
	// refused watches whose request carried a gen).
	Gen uint32 `json:"gen,omitempty"`
}

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
	Channel string    `json:"channel"`
	From    Member    `json:"from"`
	Text    string    `json:"text"`
	TS      time.Time `json:"ts"`
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
