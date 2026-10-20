package state

import (
	"errors"
	"fmt"
	"testing"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

func TestDialErrorText(t *testing.T) {
	full := fmt.Errorf("client: auth rejected: %w", &client.ServerError{Code: proto.CodeServerFull, Message: "server is full"})
	if got := DialErrorText(full); got != ServerFullNotice {
		t.Errorf("server_full = %q, want %q", got, ServerFullNotice)
	}
	if got := DialErrorText(errors.New("boom")); got != "boom" {
		t.Errorf("other = %q", got)
	}
}

func TestDialErrorTextRefusals(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{client.CodeServerBusy, "server is busy, try again in a moment"},
		{client.CodeForbidden, "server refused the connection"},
	}
	for _, tc := range cases {
		err := fmt.Errorf("client: dial: %w", &client.ServerError{Code: tc.code, Message: "x"})
		if got := DialErrorText(err); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.code, got, tc.want)
		}
	}
}
