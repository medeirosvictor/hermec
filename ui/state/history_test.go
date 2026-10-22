package state

import (
	"fmt"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

func hmsg(id string, sec int) proto.ChatMessage {
	return proto.ChatMessage{ID: id, Channel: "general", From: proto.Member{Fingerprint: "fp1", Name: "ana"}, Text: "t" + id, TS: time.Unix(int64(sec), 0)}
}

func hist(before string, more bool, ms ...proto.ChatMessage) client.Event {
	return client.Event{History: &proto.History{Channel: "general", Before: before, Messages: ms, More: more}}
}

func chatID(id string, sec int) client.Event {
	m := hmsg(id, sec)
	return client.Event{Chat: &m}
}

func ids(s *State, ch string) string {
	out := ""
	for _, m := range s.Messages[ch] {
		out += m.ID + ","
	}
	return out
}

func newHS() *State {
	s := New()
	s.SetConnected(chans("general", "dev"), "me")
	return s
}

func TestHistoryMergeDedup(t *testing.T) {
	s := newHS()
	s.Apply(hist("", false, hmsg("a", 1), hmsg("b", 2), hmsg("c", 3)), true)
	s.Apply(chatID("c", 3), true)
	s.Apply(chatID("d", 4), true)
	if got := ids(s, "general"); got != "a,b,c,d," {
		t.Fatalf("got %s", got)
	}
}

func TestHistoryOrderByTSID(t *testing.T) {
	s := newHS()
	s.Apply(hist("", false, hmsg("c", 3), hmsg("b2", 2)), true)
	s.Apply(hist("x", false, hmsg("b1", 2), hmsg("a", 1)), true)
	if got := ids(s, "general"); got != "a,b1,b2,c," {
		t.Fatalf("got %s", got)
	}
}

func TestHistoryOlderPagePrepends(t *testing.T) {
	s := newHS()
	s.Apply(hist("", true, hmsg("c", 3), hmsg("d", 4)), true)
	if !s.CanLoadOlder("general") {
		t.Fatal("want CanLoadOlder after More page")
	}
	if s.OldestID("general") != "c" {
		t.Fatalf("oldest = %q", s.OldestID("general"))
	}
	s.MarkLoading("general")
	if s.CanLoadOlder("general") {
		t.Fatal("loading should block")
	}
	s.Apply(hist("c", false, hmsg("a", 1), hmsg("b", 2)), true)
	if got := ids(s, "general"); got != "a,b,c,d," {
		t.Fatalf("got %s", got)
	}
	if s.Older["general"] || s.Loading["general"] || s.CanLoadOlder("general") {
		t.Fatalf("older=%v loading=%v", s.Older["general"], s.Loading["general"])
	}
}

func TestHistoryLatestPageWithGapReplaces(t *testing.T) {
	s := newHS()
	s.Apply(hist("", false, hmsg("l1", 1), hmsg("l2", 2), hmsg("l3", 3)), true)
	var page []proto.ChatMessage
	for i := 200; i < 300; i++ {
		page = append(page, hmsg(fmt.Sprintf("m%d", i), i))
	}
	s.Apply(hist("", true, page...), true)
	ms := s.Messages["general"]
	if len(ms) != 100 || ms[0].ID != "m200" || ms[99].ID != "m299" {
		t.Fatalf("len=%d first=%s", len(ms), ids(s, "general"))
	}
	if !s.Older["general"] {
		t.Fatal("Older should be true")
	}
}

func TestHistoryNoUnread(t *testing.T) {
	s := newHS()
	s.Apply(hist("", true, hmsg("a", 1)), true)
	s.Apply(hist("a", false, hmsg("z", 0)), true)
	if s.Unread["general"] || s.AnyUnread() {
		t.Fatal("history set Unread")
	}
}

func TestHistoryCap(t *testing.T) {
	s := newHS()
	var page []proto.ChatMessage
	for i := 0; i < MaxMessages+50; i++ {
		page = append(page, hmsg(fmt.Sprintf("m%05d", i), i+1))
	}
	s.Apply(hist("", false, page...), true)
	ms := s.Messages["general"]
	if len(ms) != MaxMessages || ms[len(ms)-1].ID != fmt.Sprintf("m%05d", MaxMessages+49) || ms[0].ID != "m00050" {
		t.Fatalf("len=%d first=%s", len(ms), ms[0].ID)
	}
	if !s.Older["general"] || s.CanLoadOlder("general") {
		t.Fatal("at cap: Older true, CanLoadOlder false")
	}
}

// An older page with nothing new must not re-arm Older, or the view at the
// top would ask for the same page forever.
func TestHistoryOlderPageNothingNewStopsOlder(t *testing.T) {
	for name, page := range map[string][]proto.ChatMessage{
		"empty":     nil,
		"dups only": {hmsg("c", 3)},
	} {
		s := newHS()
		s.Apply(hist("", true, hmsg("c", 3), hmsg("d", 4)), true)
		s.MarkLoading("general")
		s.Apply(hist("c", true, page...), true)
		if s.Older["general"] || s.CanLoadOlder("general") || s.Loading["general"] {
			t.Errorf("%s: older=%v loading=%v", name, s.Older["general"], s.Loading["general"])
		}
	}
	s := newHS()
	s.Apply(hist("", true, hmsg("c", 3)), true)
	s.Apply(hist("c", true, hmsg("b", 2)), true)
	if !s.Older["general"] {
		t.Error("a page that adds a message keeps More")
	}
}

// A join page answers any request in flight (it arrives after a rejoin).
func TestHistoryJoinPageClearsLoading(t *testing.T) {
	for name, page := range map[string][]proto.ChatMessage{
		"empty": nil,
		"lines": {hmsg("a", 1)},
	} {
		s := newHS()
		s.MarkLoading("general")
		s.Apply(hist("", false, page...), true)
		if s.Loading["general"] {
			t.Errorf("%s: Loading survived the join page", name)
		}
	}
}

func TestHistorySetConnectedClearsLoading(t *testing.T) {
	s := newHS()
	s.MarkLoading("general")
	s.SetConnected(chans("general"), "me")
	if s.Loading["general"] {
		t.Fatal("Loading survived reconnect")
	}
}
