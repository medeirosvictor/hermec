package server

import (
	"encoding/json"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/proto"
)

// maxChainFrameEntries bounds the entry bytes of one chain frame, leaving
// room for the envelope under proto.MaxMessageSize.
const maxChainFrameEntries = 60 << 10

// chainFrames encodes entries, in order, as chain frames: each carries under
// maxChainFrameEntries bytes of entries and More is set on all but the last.
// An empty list still yields one (final) frame.
func chainFrames(entries []chain.Entry) ([][]byte, error) {
	var groups [][]json.RawMessage
	var cur []json.RawMessage
	size := 0
	for _, e := range entries {
		raw, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		if len(cur) > 0 && size+len(raw)+1 > maxChainFrameEntries {
			groups = append(groups, cur)
			cur, size = nil, 0
		}
		cur = append(cur, raw)
		size += len(raw) + 1
	}
	groups = append(groups, cur)
	frames := make([][]byte, 0, len(groups))
	for i, g := range groups {
		if g == nil {
			g = []json.RawMessage{}
		}
		raw, err := proto.Encode(proto.TypeChain, proto.ChainFrame{Entries: g, More: i < len(groups)-1})
		if err != nil {
			return nil, err
		}
		frames = append(frames, raw)
	}
	return frames, nil
}
