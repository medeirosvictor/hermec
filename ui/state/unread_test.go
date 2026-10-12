package state

import "testing"

func viewedState() *State {
	s := New()
	s.SetConnected(chans("general", "random"), "me")
	s.SetViewed(true)
	return s
}

func TestUnreadOtherChannel(t *testing.T) {
	s := viewedState()
	s.Apply(chat("random", "hi"), true)
	if !s.Unread["random"] || !s.AnyUnread() {
		t.Fatalf("Unread = %v", s.Unread)
	}
}

func TestUnreadActiveViewedNotMarked(t *testing.T) {
	s := viewedState()
	s.Apply(chat("general", "hi"), true)
	if s.Unread["general"] || s.AnyUnread() {
		t.Fatalf("Unread = %v", s.Unread)
	}
}

func TestUnreadActiveNotViewed(t *testing.T) {
	s := viewedState()
	s.SetViewed(false)
	s.Apply(chat("general", "hi"), true)
	if !s.Unread["general"] {
		t.Fatalf("Unread = %v", s.Unread)
	}
}

func TestUnreadOwnMessageIgnored(t *testing.T) {
	s := viewedState()
	ev := chat("random", "mine")
	ev.Chat.From.Fingerprint = "me"
	s.Apply(ev, true)
	if s.AnyUnread() {
		t.Fatalf("Unread = %v", s.Unread)
	}
}

func TestSetViewedClearsActive(t *testing.T) {
	s := viewedState()
	s.SetViewed(false)
	s.Apply(chat("general", "a"), true)
	s.Apply(chat("random", "b"), true)
	s.SetViewed(true)
	if s.Unread["general"] || !s.Unread["random"] || !s.Viewed {
		t.Fatalf("Unread = %v viewed = %v", s.Unread, s.Viewed)
	}
}

func TestMarkRead(t *testing.T) {
	s := viewedState()
	s.Apply(chat("random", "b"), true)
	s.MarkRead("random")
	if s.AnyUnread() {
		t.Fatalf("Unread = %v", s.Unread)
	}
}

func TestReconnectKeepsScrollbackAndUnread(t *testing.T) {
	s := viewedState()
	s.Apply(chat("general", "old"), true)
	s.Apply(chat("random", "b"), true)
	s.SetConnected(chans("general", "random"), "me")
	if len(s.Messages["general"]) != 1 || !s.Unread["random"] {
		t.Fatalf("messages = %v unread = %v", s.Messages, s.Unread)
	}
}
