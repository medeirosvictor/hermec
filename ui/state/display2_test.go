package state

import (
	"testing"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

func TestPresenceRosterNamesCleaned(t *testing.T) {
	bad := "al" + string(rune(0x202e)) + "ice"
	s := New()
	s.SetConnected(chans("general"), "me")
	s.Apply(client.Event{Presence: &proto.Presence{Channel: "general", Members: []proto.Member{{Name: bad}}}}, true)
	s.Apply(client.Event{Roster: &proto.Roster{Members: []proto.Member{{Name: bad}}, Total: 1}}, true)
	if s.Members["general"][0].Name != "alice" || s.Roster[0].Name != "alice" {
		t.Errorf("presence %+v roster %+v", s.Members["general"], s.Roster)
	}
}
