package state

import (
	"errors"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

// ServerFullNotice is the dial status shown when the server is at max_clients.
const ServerFullNotice = "server is full"

// DialErrorText turns a dial or auth failure into the text shown on the tile
// and in the failed pane. Unrecognized errors keep their own text.
func DialErrorText(err error) string {
	var se *client.ServerError
	if errors.As(err, &se) && se.Code == proto.CodeServerFull {
		return ServerFullNotice
	}
	return err.Error()
}
