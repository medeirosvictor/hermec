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
