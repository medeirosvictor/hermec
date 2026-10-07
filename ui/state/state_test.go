package state

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

func chat(ch, text string) client.Event {
	return client.Event{Chat: &proto.ChatMessage{Channel: ch, From: proto.Member{Fingerprint: "fp1", Name: "ana"}, Text: text, TS: time.Unix(100, 0)}}
}

func TestApplyChatAppendsToItsChannel(t *testing.T) {
	s := New()
	s.SetConnected(chans("general", "dev"), "me")
	s.Apply(chat("general", "oi"), true)
	if len(s.Messages["dev"]) != 0 || len(s.Messages["general"]) != 1 {
		t.Fatalf("bad routing: %+v", s.Messages)
	}
	m := s.Messages["general"][0]
	if m.FromName != "ana" || m.FromFP != "fp1" || m.Text != "oi" || !m.TS.Equal(time.Unix(100, 0)) {
		t.Fatalf("bad mapping: %+v", m)
	}
}

func TestApplyCapsAt500(t *testing.T) {
	s := New()
	s.SetConnected(chans("general"), "me")
	for i := 0; i < 501; i++ {
		s.Apply(chat("general", fmt.Sprint(i)), true)
	}
	ms := s.Messages["general"]
	if len(ms) != MaxMessages || ms[0].Text != "1" || ms[len(ms)-1].Text != "500" {
		t.Fatalf("len=%d first=%s last=%s", len(ms), ms[0].Text, ms[len(ms)-1].Text)
	}
}

func TestApplyPresenceReplacesMembers(t *testing.T) {
	s := New()
	s.SetConnected(chans("general"), "me")
	s.Apply(client.Event{Presence: &proto.Presence{Channel: "general", Members: []proto.Member{{Name: "a"}, {Name: "b"}}}}, true)
	s.Apply(client.Event{Presence: &proto.Presence{Channel: "general", Members: []proto.Member{{Name: "c"}}}}, true)
	if len(s.Members["general"]) != 1 || s.Members["general"][0].Name != "c" {
		t.Fatalf("got %+v", s.Members["general"])
	}
}

func TestApplyErrSetsStatus(t *testing.T) {
	s := New()
	s.SetConnected(chans("general"), "me")
	s.Apply(client.Event{Err: errors.New("boom")}, true)
	if !strings.Contains(s.Status, "boom") || s.Phase != PhaseMain {
		t.Fatalf("status=%q phase=%v", s.Status, s.Phase)
	}
}

func TestEventsClosedMeansDisconnected(t *testing.T) {
	s := New()
	s.SetConnected(chans("general"), "me")
	s.Apply(client.Event{}, false)
	if s.Phase != PhaseDisconnected {
		t.Fatalf("phase=%v", s.Phase)
	}
}

func TestChannelNav(t *testing.T) {
	s := New()
	s.SetConnected(chans("a", "b", "c"), "me")
	s.PrevChannel()
	if s.Active != 2 {
		t.Fatalf("prev wrap: %d", s.Active)
	}
	s.NextChannel()
	if s.Active != 0 {
		t.Fatalf("next wrap: %d", s.Active)
	}
	s.NextChannel()
	if s.Active != 1 {
		t.Fatalf("next: %d", s.Active)
	}
}

func chans(names ...string) []proto.ChannelInfo {
	out := make([]proto.ChannelInfo, len(names))
	for i, n := range names {
		out[i] = proto.ChannelInfo{Name: n, Type: "text"}
	}
	return out
}
