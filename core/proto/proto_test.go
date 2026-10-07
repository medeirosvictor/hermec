package proto

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := ChatSend{Channel: "general", Text: "hello"}
	raw, err := Encode(TypeChatSend, in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	env, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if env.V != 1 {
		t.Errorf("V = %d, want 1", env.V)
	}
	if env.Type != TypeChatSend {
		t.Errorf("Type = %q, want %q", env.Type, TypeChatSend)
	}
	var out ChatSend
	if err := json.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("Unmarshal data: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("payload = %+v, want %+v", out, in)
	}
}

func TestDecodeRejects(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{"not json", []byte("not json")},
		{"wrong version", []byte(`{"v":2,"type":"x"}`)},
		{"empty type", []byte(`{"v":1,"type":""}`)},
		{"oversized", []byte(strings.Repeat("a", MaxMessageSize+1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode(tt.raw); err == nil {
				t.Errorf("Decode(%s) = nil error, want error", tt.name)
			}
		})
	}
}
