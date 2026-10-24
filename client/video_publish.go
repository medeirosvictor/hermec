package client

import (
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
)

func newVideoPublisher(kind proto.MediaKind, src FrameSource, enc frameEncoder, w, h, fps int, write func([]byte, time.Duration) error) *videoPublisher {
	p := &videoPublisher{kind: kind, src: src, enc: enc, w: w, h: h, fps: fps, write: write, quit: make(chan struct{})}
	p.forceKey.Store(true)
	return p
}
