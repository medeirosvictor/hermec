package server

import (
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
)

// Only a merge link (a split-brain loser) may push chat lines or chain
// entries at the active host; a standby's link may not.
func TestPlainLinkCannotPushHistory(t *testing.T) {
	c, a, b := twoHostChain(t)
	s, url := startLinkServer(t, c, a, "pw")
	lk := linkUp(t, url, b, proto.LinkHello{Name: "b"})
	waitFor(t, "link", func() bool { return len(s.Links()) == 1 })
	msg := proto.ChatMessage{ID: "m1", Channel: "general", Text: "forged", TS: time.Now()}
	sendEnv(t, lk, proto.TypeLinkSync, proto.LinkSync{Messages: []proto.ChatMessage{msg}})
	if code := errCode(readType(t, lk, proto.TypeError)); code != proto.CodeForbidden {
		t.Fatalf("link_sync from a standby link: code %q, want forbidden", code)
	}
	if n := len(histAll(s)); n != 0 {
		t.Fatalf("history has %d lines after a standby's link_sync", n)
	}
	sendEnv(t, lk, proto.TypeChain, proto.ChainFrame{})
	if code := errCode(readType(t, lk, proto.TypeError)); code != proto.CodeForbidden {
		t.Fatalf("chain from a standby link: code %q, want forbidden", code)
	}
}
