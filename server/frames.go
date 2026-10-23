package server

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/hshake"
	"github.com/medeirosvictor/hermec/core/proto"
)

// readFrames reads messages from ws and passes each decodable one to fn until
// fn reports done or fails. Messages that do not decode are skipped; an error
// frame from the peer ends the read with that error. A read failure is
// wrapped as "server: <what>: ...".
func readFrames(ws *websocket.Conn, what string, fn func(env proto.Envelope) (done bool, err error)) error {
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return fmt.Errorf("server: %s: %w", what, err)
		}
		env, err := proto.Decode(raw)
		if err != nil {
			continue
		}
		if env.Type == proto.TypeError {
			return remoteError(env)
		}
		if done, err := fn(env); done || err != nil {
			return err
		}
	}
}

// decodeChainFrame returns the chain entries a chain frame carries.
func decodeChainFrame(env proto.Envelope) ([]chain.Entry, error) {
	var cf proto.ChainFrame
	if json.Unmarshal(env.Data, &cf) != nil {
		return nil, errors.New("server: malformed chain frame")
	}
	return hshake.DecodeEntries(cf.Entries)
}

func remoteError(env proto.Envelope) error {
	var em proto.ErrorMsg
	_ = json.Unmarshal(env.Data, &em)
	return &hshake.ServerError{Code: em.Code, Message: em.Message}
}
