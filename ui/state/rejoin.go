package state

import "slices"

// Rejoin remembers the voice call a session had when its connection dropped,
// so the call can be rejoined once the session reconnects. The zero value
// holds nothing. It is per session and owned by the update goroutine.
//
// States: idle -> pending (Lost, with a call) -> idle (Reconnected, or Cancel).
// A deliberate leave never reaches pending (Lost sees no call), and a
// failover or move carries the call itself, so it never calls Lost at all.
type Rejoin struct {
	channel string
	muted   bool
}

// RejoinKind says what a reconnect should do about the remembered call.
type RejoinKind int

const (
	RejoinNone RejoinKind = iota // nothing was remembered
	RejoinJoin                   // join Channel again, muted as before
	RejoinGone                   // Channel no longer exists: tell the user and stop
)

// RejoinAction is the outcome of Reconnected.
type RejoinAction struct {
	Kind    RejoinKind
	Channel string
	Muted   bool
}

// Lost records that the connection dropped while the session was in call
// channel (empty: it was not in a call, which forgets any earlier memory).
func (r *Rejoin) Lost(channel string, muted bool) {
	*r = Rejoin{channel: channel, muted: muted}
}

// Pending reports whether a call is waiting to be rejoined.
func (r Rejoin) Pending() bool { return r.channel != "" }

// Cancel forgets the remembered call: the user joined elsewhere, left or
// disconnected, or the server refused the session for good.
func (r *Rejoin) Cancel() { *r = Rejoin{} }

// Reconnected consumes the memory when the session is connected again, given
// the voice channels the server now has. Whatever it returns, nothing stays
// pending: a refused join is reported once and not retried.
func (r *Rejoin) Reconnected(voiceChannels []string) RejoinAction {
	if !r.Pending() {
		return RejoinAction{}
	}
	a := RejoinAction{Kind: RejoinGone, Channel: r.channel, Muted: r.muted}
	if slices.Contains(voiceChannels, r.channel) {
		a.Kind = RejoinJoin
	}
	r.Cancel()
	return a
}

// RejoinFailNotice is the status when the automatic rejoin was refused.
func RejoinFailNotice(err error) string {
	return "voice not rejoined: " + VoiceJoinNotice(err)
}

// RejoinGoneNotice is the status when the call's channel no longer exists.
func RejoinGoneNotice(channel string) string {
	return "voice not rejoined: " + channel + " no longer exists"
}

// RejoinedNotice is the status after a successful automatic rejoin.
const RejoinedNotice = "rejoined voice"
