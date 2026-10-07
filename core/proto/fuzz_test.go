package proto

import (
	"strings"
	"testing"
)

func FuzzDecode(f *testing.F) {
	if valid, err := Encode(TypeChatSend, ChatSend{Channel: "general", Text: "hi"}); err == nil {
		f.Add(valid)
	}
	f.Add([]byte("not json"))
	f.Add([]byte(`{"v":2,"type":"x"}`))
	f.Add([]byte(`{"v":1,"type":""}`))
	f.Add([]byte(strings.Repeat("a", MaxMessageSize+1)))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = Decode(raw) // property: never panics
	})
}
