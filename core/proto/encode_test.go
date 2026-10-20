package proto

import (
	"bytes"
	"testing"
)

func TestEncodeNoHTMLEscape(t *testing.T) {
	raw, err := Encode(TypeChatMessage, ChatMessage{Channel: "c", Text: "<a>&"})
	if err != nil {
		t.Fatal(err)
	}
	bs := "\\"
	if bytes.Contains(raw, []byte(bs+"u003c")) || bytes.Contains(raw, []byte(bs+"u0026")) || bytes.Contains(raw, []byte(bs+"u003e")) {
		t.Errorf("HTML-escaped output: %s", raw)
	}
	if bytes.HasSuffix(raw, []byte("\n")) {
		t.Error("trailing newline")
	}
	env, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(env.Data, []byte("<a>&")) {
		t.Errorf("data = %s", env.Data)
	}
}
