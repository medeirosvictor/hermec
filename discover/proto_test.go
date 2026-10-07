package discover

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	in := Announce{Hermec: 1, Name: "héllo", Port: 7697, Ver: "dev"}
	b, err := EncodeAnnounce(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ParseAnnounce(b)
	if err != nil || out != in {
		t.Fatalf("got %+v, %v", out, err)
	}
}

func TestParseRejects(t *testing.T) {
	long := strings.Repeat("x", 65)
	cases := map[string]string{
		"garbage":    "not json",
		"empty":      "",
		"hermec 2":   `{"hermec":2,"name":"a","port":1,"ver":"x"}`,
		"hermec 0":   `{"name":"a","port":1,"ver":"x"}`,
		"port 0":     `{"hermec":1,"name":"a","port":0,"ver":"x"}`,
		"port big":   `{"hermec":1,"name":"a","port":65536,"ver":"x"}`,
		"long name":  `{"hermec":1,"name":"` + long + `","port":1,"ver":"x"}`,
		"oversized":  `{"hermec":1,"name":"a","port":1,"ver":"` + strings.Repeat("v", 600) + `"}`,
		"wrong type": `{"hermec":"1","name":"a","port":1,"ver":"x"}`,
	}
	for name, in := range cases {
		if _, err := ParseAnnounce([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ParseAnnounce([]byte(`{"hermec":1,"name":"` + strings.Repeat("é", 64) + `","port":65535,"ver":"x"}`)); err != nil {
		t.Errorf("64-rune name rejected: %v", err)
	}
}

func TestEncodeOversized(t *testing.T) {
	if _, err := EncodeAnnounce(Announce{Hermec: 1, Name: "a", Port: 1, Ver: strings.Repeat("v", 600)}); err == nil {
		t.Fatal("oversized accepted")
	}
}
