package state

import (
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

func TestDisplayNamesCleaned(t *testing.T) {
	bad := "al\u202eic\x00e"
	s := New()
	s.SetConnected(chans("general"), "me")

	s.Apply(client.Event{Chat: &proto.ChatMessage{
		Channel: "general", From: proto.Member{Fingerprint: "x", Name: bad}, Text: "hi", TS: time.Now(),
	}}, true)
	if got := s.Messages["general"][0].FromName; got != "alice" {
		t.Errorf("chat sender = %q, want alice", got)
	}

	s.VoiceChannels = []string{"voice"}
	s.Apply(vstate("voice", proto.VoiceMember{Fingerprint: "a", Name: bad, Sharing: true}), true)
	var occ *Row
	for _, r := range s.Rows() {
		if r.Kind == RowOccupant {
			r := r
			occ = &r
		}
	}
	if occ == nil || occ.Name != "alice" || !occ.Sharing {
		t.Errorf("occupant = %+v, want name alice and Sharing kept", occ)
	}

	rows := MergeDiscovered([]DiscoveredIn{{Name: bad, Source: "lan", Addr: "10.0.0.1:1"}}, nil)
	if len(rows) != 1 || rows[0].Name != "alice" {
		t.Errorf("discovered rows = %+v, want name alice", rows)
	}
}
