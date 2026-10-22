package ui

import (
	"fmt"
	"testing"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

func TestPinMismatchNoRetry(t *testing.T) {
	cases := []struct{ code, want string }{
		{proto.CodeServerIDMismatch, "this server's identity changed — remove and re-add it if you trust it"},
		{proto.CodeHostNotAuthorized, "the host's machine is not an approved host for this server"},
		{proto.CodeBadChain, "the server sent an invalid chain"},
		{proto.CodeServerOutdated, "the host runs an older HERMEC — ask them to update"},
		{proto.CodeUpdateRequired, "update HERMEC to connect to this server"},
		{proto.CodeBanned, "you are banned from this server"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			err := fmt.Errorf("client: %w", &client.ServerError{Code: tc.code, Message: "x"})
			g, s := failedDial(t, err)
			if !s.retryAt.IsZero() {
				t.Fatalf("%s scheduled an automatic retry", tc.code)
			}
			if !s.failed || s.lastErr != tc.want {
				t.Fatalf("failed=%v lastErr=%q, want %q", s.failed, s.lastErr, tc.want)
			}
			g.retryNow("k")
			if !s.dialing {
				t.Fatal("manual retry did not dial")
			}
		})
	}
}
