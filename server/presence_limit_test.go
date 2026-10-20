package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/discover"
)

func TestPresenceTrimmedToBudget(t *testing.T) {
	emoji := strings.Repeat(string(rune(0x1F600)), discover.MaxNameRunes) // 4 bytes each
	var members []proto.Member
	for i := 0; i < 400; i++ {
		members = append(members, proto.Member{
			Fingerprint: strings.Repeat("f", 64), Name: emoji, Roles: []string{"user"},
		})
	}
	raw := encodePresence("general", members)
	if len(raw) > proto.MaxMessageSize {
		t.Fatalf("presence frame %d bytes over budget", len(raw))
	}
	env, err := proto.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	var p proto.Presence
	if err := json.Unmarshal(env.Data, &p); err != nil {
		t.Fatal(err)
	}
	if p.Total != 400 || p.Channel != "general" {
		t.Errorf("Total = %d channel %q, want 400 general", p.Total, p.Channel)
	}
	if len(p.Members) == 0 || len(p.Members) >= 400 {
		t.Fatalf("kept %d members", len(p.Members))
	}
	more, _ := proto.Encode(proto.TypePresence, proto.Presence{Channel: "general", Members: members[:len(p.Members)+1], Total: 400})
	if len(more) <= proto.MaxMessageSize {
		t.Errorf("trimmed too far: %d kept but %d fit", len(p.Members), len(p.Members)+1)
	}
}
