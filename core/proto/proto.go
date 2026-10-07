// Package proto defines the Hermec wire protocol: a versioned JSON envelope
// and the payload structs exchanged over WebSocket.
package proto

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	// Version is the protocol version carried in every envelope.
	Version = 1
	// MaxMessageSize is the largest accepted raw message, in bytes.
	MaxMessageSize = 64 * 1024
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

	TypeVoiceJoin    = "voice_join"
	TypeVoiceLeave   = "voice_leave"
	TypeVoiceState   = "voice_state"
	TypeVoiceMute    = "voice_mute"
	TypeRTCOffer     = "rtc_offer"
	TypeRTCAnswer    = "rtc_answer"
	TypeRTCCandidate = "rtc_candidate"
)

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
// "bad_request", "not_joined", "rate_limited".
type ErrorMsg struct {
	Code    string `json:"code"`
	Message string `json:"message"`
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
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("proto: marshal payload: %w", err)
	}
	return json.Marshal(Envelope{V: Version, Type: msgType, Data: data})
}

// Decode parses and validates a raw envelope. It returns an error on
// oversized input, invalid JSON, a version mismatch, or an empty type.
func Decode(raw []byte) (Envelope, error) {
	if len(raw) > MaxMessageSize {
		return Envelope{}, fmt.Errorf("proto: message too large (%d > %d bytes)", len(raw), MaxMessageSize)
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Envelope{}, fmt.Errorf("proto: invalid envelope: %w", err)
	}
	if env.V != Version {
		return Envelope{}, fmt.Errorf("proto: unsupported version %d", env.V)
	}
	if env.Type == "" {
		return Envelope{}, errors.New("proto: empty message type")
	}
	return env, nil
}
