// Package state is the pure, headless core of the GUI: a reducer over client
// events plus input editing and text wrapping. It must not import Ebiten.
package state

import (
	"errors"
	"fmt"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

// MaxMessages is the per-channel scrollback cap.
const MaxMessages = 2000

// HistoryPage is the most messages a host sends in one history page.
const HistoryPage = 100

type Phase int

const (
	PhaseConnect Phase = iota
	PhaseMain
	PhaseDisconnected
)

type Message struct {
	ID                     string // host-assigned; empty from a pre-history host
	FromName, FromFP, Text string
	TS                     time.Time
}

type State struct {
	Phase      Phase
	Status     string
	ConnectErr string
	Channels   []string
	Active     int
	Messages   map[string][]Message
	Members    map[string][]proto.Member
	// VoiceChannels are the server's voice channel names (not part of
	// Channels, which stays text-only for keyboard navigation).
	VoiceChannels []string
	// Voice is the latest occupant list per voice channel; empty channels
	// have no key.
	Voice map[string][]proto.VoiceMember
	// InCall is the voice channel this client is in, maintained from its own
	// actions (SetInCall), never from events.
	InCall string
	// Unread marks text channels with messages not yet seen. SetConnected
	// never clears it, so unread survives a reconnect.
	Unread map[string]bool
	// Older marks channels whose host has messages older than the oldest one
	// shown; Loading marks channels with a history_before in flight.
	Older   map[string]bool
	Loading map[string]bool
	// Roster is the server-wide member list (possibly truncated); RosterTotal
	// is the full member count. HasRoster is false until one arrives.
	Roster      []proto.Member
	RosterTotal int
	HasRoster   bool
	// Viewed is whether this state's server is the one on screen.
	Viewed      bool
	Input       InputBuffer
	fingerprint string
}

func New() *State {
	return &State{
		Phase:    PhaseConnect,
		Messages: map[string][]Message{},
		Members:  map[string][]proto.Member{},
		Voice:    map[string][]proto.VoiceMember{},
		Unread:   map[string]bool{},
		Older:    map[string]bool{},
		Loading:  map[string]bool{},
	}
}

// SetConnected enters PhaseMain with the given channels.
func (s *State) SetConnected(channels []proto.ChannelInfo, fingerprint string) {
	s.Phase = PhaseMain
	s.Channels = nil
	s.VoiceChannels = nil
	s.Voice = map[string][]proto.VoiceMember{}
	s.InCall = ""
	s.Roster, s.RosterTotal, s.HasRoster = nil, 0, false
	s.Loading = map[string]bool{} // a request in flight is lost on reconnect
	for _, ch := range channels {
		if ch.Type == "voice" {
			s.VoiceChannels = append(s.VoiceChannels, ch.Name)
		} else {
			s.Channels = append(s.Channels, ch.Name)
		}
	}
	s.Active = 0
	s.fingerprint = fingerprint
	s.ConnectErr = ""
	s.Status = ""
}

// cleanMembers copies ms with every name cleaned (a v0.4 server sends them raw).
func cleanMembers(ms []proto.Member) []proto.Member {
	out := append([]proto.Member(nil), ms...)
	for i := range out {
		out[i].Name = CleanName(out[i].Name, MaxServerNameRunes)
	}
	return out
}

// Apply reduces one client event. open=false means the Events channel closed.
func (s *State) Apply(ev client.Event, open bool) {
	if !open {
		s.Phase = PhaseDisconnected
		s.Status = "disconnected"
		s.InCall = ""
		return
	}
	switch {
	case ev.Chat != nil:
		c := ev.Chat
		s.mergeMessages(c.Channel, []proto.ChatMessage{*c})
		onScreen := s.Viewed && s.Active >= 0 && s.Active < len(s.Channels) && s.Channels[s.Active] == c.Channel
		if !onScreen && c.From.Fingerprint != s.fingerprint {
			s.Unread[c.Channel] = true
		}
	case ev.History != nil:
		s.applyHistory(ev.History)
	case ev.Presence != nil:
		s.Members[ev.Presence.Channel] = cleanMembers(ev.Presence.Members)
	case ev.Roster != nil:
		s.Roster = cleanMembers(ev.Roster.Members)
		s.RosterTotal = ev.Roster.Total
		s.HasRoster = true
	case ev.Voice != nil:
		if len(ev.Voice.Members) == 0 {
			delete(s.Voice, ev.Voice.Channel)
		} else {
			ms := append([]proto.VoiceMember(nil), ev.Voice.Members...)
			for i := range ms {
				ms[i].Name = CleanName(ms[i].Name, MaxServerNameRunes)
			}
			s.Voice[ev.Voice.Channel] = ms
		}
	case errors.Is(ev.Err, client.ErrShareUnsupported):
		s.Status = ev.Err.Error() // a plain explanation, not an error code
	case ev.Err != nil:
		s.Status = fmt.Sprintf("error: %v", ev.Err)
	}
}

// SetInCall records the voice channel this client is in ("" = none).
func (s *State) SetInCall(ch string) { s.InCall = ch }

// SetViewed records whether this server is on screen. Becoming viewed reads
// the active channel.
func (s *State) SetViewed(v bool) {
	s.Viewed = v
	if v && s.Active >= 0 && s.Active < len(s.Channels) {
		s.MarkRead(s.Channels[s.Active])
	}
}

// MarkRead clears the unread flag of one channel.
func (s *State) MarkRead(ch string) { delete(s.Unread, ch) }

// AnyUnread reports whether any channel is unread.
func (s *State) AnyUnread() bool { return len(s.Unread) > 0 }

func (s *State) NextChannel() {
	if n := len(s.Channels); n > 0 {
		s.Active = (s.Active + 1) % n
	}
}

func (s *State) PrevChannel() {
	if n := len(s.Channels); n > 0 {
		s.Active = (s.Active - 1 + n) % n
	}
}

// CanLoadOlder reports whether an older page can be requested for ch: one is
// due, none is in flight, and a whole page fits under MaxMessages.
func (s *State) CanLoadOlder(ch string) bool {
	return s.Older[ch] && !s.Loading[ch] && len(s.Messages[ch])+HistoryPage <= MaxMessages
}

// Oldest is the id and timestamp of the oldest message shown in ch (id ""
// if none).
func (s *State) Oldest(ch string) (id string, ts time.Time) {
	if ms := s.Messages[ch]; len(ms) > 0 {
		return ms[0].ID, ms[0].TS
	}
	return "", time.Time{}
}

// MarkLoading records that a history_before for ch is in flight.
func (s *State) MarkLoading(ch string) { s.Loading[ch] = true }

func (s *State) applyHistory(h *proto.History) {
	ch := h.Channel
	if h.Before != "" {
		s.Older[ch] = h.More // set first: the cap may raise it
		if s.mergeMessages(ch, h.Messages) == 0 {
			// Nothing new: asking again would fetch the same page forever.
			s.Older[ch] = false
		}
		delete(s.Loading, ch)
		return
	}
	// A join page (after a rejoin) supersedes any older-page request in flight.
	delete(s.Loading, ch)
	if len(h.Messages) == 0 {
		return
	}
	if h.More && !s.hasID(ch, h.Messages[0].ID) {
		// The join page does not touch what we hold: there is a gap, so the
		// local lines can be fetched again. Unread is left alone.
		s.Messages[ch] = nil
		s.Older[ch] = true
		s.mergeMessages(ch, h.Messages)
		return
	}
	if len(s.Messages[ch]) == 0 {
		s.Older[ch] = h.More
	}
	s.mergeMessages(ch, h.Messages)
}

func (s *State) hasID(ch, id string) bool {
	if id == "" {
		return false
	}
	for _, m := range s.Messages[ch] {
		if m.ID == id {
			return true
		}
	}
	return false
}

func msgBefore(a, b Message) bool { // a sorts strictly before b
	if !a.TS.Equal(b.TS) {
		return a.TS.Before(b.TS)
	}
	return a.ID < b.ID
}

// mergeMessages inserts in into ch in (TS, ID) order, skipping non-empty ids
// already present, and enforces MaxMessages (keeping the newest). It returns
// how many messages it inserted, before the cap.
func (s *State) mergeMessages(ch string, in []proto.ChatMessage) (added int) {
	ms := s.Messages[ch]
	have := make(map[string]bool, len(ms))
	for _, m := range ms {
		if m.ID != "" {
			have[m.ID] = true
		}
	}
	for _, c := range in {
		if c.ID != "" {
			if have[c.ID] {
				continue
			}
			have[c.ID] = true
		}
		nm := Message{
			ID: c.ID, FromName: CleanName(c.From.Name, MaxServerNameRunes), FromFP: c.From.Fingerprint, Text: c.Text, TS: c.TS,
		}
		// Insert after every message that does not sort after nm.
		i := len(ms)
		for i > 0 && msgBefore(nm, ms[i-1]) {
			i--
		}
		ms = append(ms, Message{})
		copy(ms[i+1:], ms[i:])
		ms[i] = nm
		added++
	}
	if len(ms) > MaxMessages {
		ms = append([]Message(nil), ms[len(ms)-MaxMessages:]...)
		s.Older[ch] = true
	}
	s.Messages[ch] = ms
	return added
}
