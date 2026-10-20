package proto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestShareProtoRoundTrip(t *testing.T) {
	if TypeShareStart != "share_start" || TypeShareStop != "share_stop" || TypeWatch != "watch" || CodeNotSharing != "not_sharing" {
		t.Fatal("share constants changed")
	}
	raw, err := Encode(TypeWatch, Watch{Fingerprint: "ab:cd"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := Decode(raw)
	if err != nil || env.Type != TypeWatch {
		t.Fatalf("decode: %v %+v", err, env)
	}
	var w Watch
	if err := json.Unmarshal(env.Data, &w); err != nil || w.Fingerprint != "ab:cd" {
		t.Fatalf("watch = %+v, %v", w, err)
	}
	raw, _ = Encode(TypeShareStart, ShareStart{})
	if env, err := Decode(raw); err != nil || env.Type != TypeShareStart {
		t.Fatalf("share_start: %v", err)
	}
	raw, _ = Encode(TypeShareStop, ShareStop{})
	if env, err := Decode(raw); err != nil || env.Type != TypeShareStop {
		t.Fatalf("share_stop: %v", err)
	}
}

func TestVoiceMemberSharingOmitEmpty(t *testing.T) {
	b, _ := json.Marshal(VoiceMember{Fingerprint: "f", Name: "n"})
	if strings.Contains(string(b), "sharing") {
		t.Fatalf("sharing=false must be omitted: %s", b)
	}
	b, _ = json.Marshal(VoiceMember{Fingerprint: "f", Name: "n", Sharing: true})
	if !strings.Contains(string(b), `"sharing":true`) {
		t.Fatalf("sharing=true missing: %s", b)
	}
	var m VoiceMember
	if err := json.Unmarshal([]byte(`{"fingerprint":"f","name":"n","muted":false}`), &m); err != nil || m.Sharing {
		t.Fatalf("old payload: %+v %v", m, err)
	}
}
