package proto

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMediaMessagesRoundTrip(t *testing.T) {
	want := MediaSource{Kind: MediaCamera, Fingerprint: "ab:cd", Slot: 9, Seq: 65000, Gen: 17}
	raw, err := Encode(TypeMediaSource, want)
	if err != nil {
		t.Fatal(err)
	}
	env, err := Decode(raw)
	if err != nil || env.Type != TypeMediaSource {
		t.Fatalf("Decode = type %q, err %v", env.Type, err)
	}
	var got MediaSource
	if err := json.Unmarshal(env.Data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MediaSource = %+v, want %+v", got, want)
	}

	for typ, payload := range map[string]any{
		TypeMediaStart:     MediaStart{Kind: MediaCamera},
		TypeMediaStop:      MediaStop{Kind: MediaScreen},
		TypeMediaSubscribe: MediaSubscribe{Kind: MediaScreen, Fingerprint: "ef:01", Gen: 23},
	} {
		raw, err := Encode(typ, payload)
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		env, err := Decode(raw)
		if err != nil || env.Type != typ {
			t.Fatalf("%s decoded as %q, %v", typ, env.Type, err)
		}
	}
}

func TestVoiceMemberMediaOmitEmpty(t *testing.T) {
	b, err := json.Marshal(VoiceMember{Fingerprint: "f", Name: "n"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "media") {
		t.Fatalf("empty media must be omitted: %s", b)
	}

	want := []MediaKind{MediaCamera, MediaScreen}
	b, err = json.Marshal(VoiceMember{Fingerprint: "f", Name: "n", Media: want})
	if err != nil {
		t.Fatal(err)
	}
	var got VoiceMember
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Media, want) {
		t.Fatalf("Media = %v, want %v", got.Media, want)
	}

	var old VoiceMember
	if err := json.Unmarshal([]byte(`{"fingerprint":"f","name":"n","muted":false,"sharing":true}`), &old); err != nil {
		t.Fatal(err)
	}
	if !old.Sharing || len(old.Media) != 0 {
		t.Fatalf("old payload = %+v", old)
	}
}

func TestMediaPriorityMetadata(t *testing.T) {
	if MediaAudio.Priority() >= MediaScreen.Priority() || MediaScreen.Priority() >= MediaCamera.Priority() {
		t.Fatalf("media priority order = audio:%d screen:%d camera:%d; want audio < screen < camera",
			MediaAudio.Priority(), MediaScreen.Priority(), MediaCamera.Priority())
	}
}
