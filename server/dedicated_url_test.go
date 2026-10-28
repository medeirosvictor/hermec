package server

import "testing"

func TestCanonicalDedicatedWSSRejectsEquivalentSpellings(t *testing.T) {
	for _, raw := range []string{
		"wss://example.test.:443/", "wss://example.test:0443/",
		"wss://[2001:DB8::1]/", "wss://[2001:0db8::1]/",
		"wss://user@example.test/", "wss://example.test/?q=1", "wss://example.test/#x",
	} {
		if _, err := CanonicalDedicatedWSS(raw); err == nil {
			t.Fatalf("accepted noncanonical URL %q", raw)
		}
	}
	for _, raw := range []string{"wss://example.test:443/", "wss://[2001:db8::1]/"} {
		if got, err := CanonicalDedicatedWSS(raw); err != nil || got != raw {
			t.Fatalf("canonical URL %q = %q, %v", raw, got, err)
		}
	}
}
