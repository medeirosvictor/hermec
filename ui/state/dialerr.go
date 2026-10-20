package state

import (
	"errors"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

// ServerFullNotice is the dial status shown when the server is at max_clients.
const ServerFullNotice = "server is full"

// ServerBusyNotice is shown when the server refuses before the upgrade because
// it is busy (pre-auth or per-IP cap, or a temporarily blocked IP).
const ServerBusyNotice = "server is busy, try again in a moment"

// ServerRefusedNotice is shown when the server refuses the request outright
// (403, e.g. an Origin header is present).
const ServerRefusedNotice = "server refused the connection"

// DialErrorText turns a dial or auth failure into the text shown on the tile
// and in the failed pane. Unrecognized errors keep their own text.
func DialErrorText(err error) string {
	var se *client.ServerError
	if errors.As(err, &se) {
		switch se.Code {
		case proto.CodeServerFull:
			return ServerFullNotice
		case client.CodeServerBusy:
			return ServerBusyNotice
		case client.CodeForbidden:
			return ServerRefusedNotice
		}
	}
	return err.Error()
}
