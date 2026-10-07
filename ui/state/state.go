// Package state is the pure, headless core of the GUI: a reducer over client
// events plus input editing and text wrapping. It must not import Ebiten.
package state

import (
	"fmt"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

// MaxMessages is the per-channel scrollback cap.
const MaxMessages = 500

type Phase int

const (
	PhaseConnect Phase = iota
	PhaseMain
	PhaseDisconnected
)

type Message struct {
	FromName, FromFP, Text string
	TS                     time.Time
}

type State struct {
	Phase       Phase
	Status      string
	ConnectErr  string
	Channels    []string
	Active      int
	Messages    map[string][]Message
	Members     map[string][]proto.Member
	Input       InputBuffer
	fingerprint string
}

func New() *State {
	return &State{
		Phase:    PhaseConnect,
		Messages: map[string][]Message{},
		Members:  map[string][]proto.Member{},
	}
}

// SetConnected enters PhaseMain with the given channels.
func (s *State) SetConnected(channels []proto.ChannelInfo, fingerprint string) {
	s.Phase = PhaseMain
	s.Channels = make([]string, len(channels))
	for i, ch := range channels {
		s.Channels[i] = ch.Name
	}
	s.Active = 0
	s.fingerprint = fingerprint
	s.ConnectErr = ""
	s.Status = ""
}

// Apply reduces one client event. open=false means the Events channel closed.
func (s *State) Apply(ev client.Event, open bool) {
	if !open {
		s.Phase = PhaseDisconnected
		s.Status = "disconnected"
		return
	}
	switch {
	case ev.Chat != nil:
		c := ev.Chat
		ms := append(s.Messages[c.Channel], Message{
			FromName: c.From.Name, FromFP: c.From.Fingerprint, Text: c.Text, TS: c.TS,
		})
		if len(ms) > MaxMessages {
			ms = append([]Message(nil), ms[len(ms)-MaxMessages:]...)
		}
		s.Messages[c.Channel] = ms
	case ev.Presence != nil:
		s.Members[ev.Presence.Channel] = append([]proto.Member(nil), ev.Presence.Members...)
	case ev.Err != nil:
		s.Status = fmt.Sprintf("error: %v", ev.Err)
	}
}

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
