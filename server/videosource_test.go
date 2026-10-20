package server

import (
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
)

func (p *testPeer) nextSource(t *testing.T) proto.VideoSource {
	t.Helper()
	select {
	case m := <-p.vsrc:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no video_source")
		return proto.VideoSource{}
	}
}

// A repeated watch of the same sharer adopts the new gen and repeats the
// current boundary under it, so a restarted client watch is not left waiting;
// a repeat with an unchanged gen sends nothing.
func TestDuplicateWatchResendsVideoSource(t *testing.T) {
	sharer, viewer, _ := shareScene(t)
	viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: sharer.fp, Gen: 1})
	first := viewer.nextSource(t)
	if first.Fingerprint != sharer.fp || first.Gen != 1 {
		t.Fatalf("first video_source = %+v, want fp %s gen 1", first, sharer.fp)
	}
	viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: sharer.fp, Gen: 2})
	second := viewer.nextSource(t)
	if second.Fingerprint != sharer.fp || second.Gen != 2 || second.Seq != first.Seq {
		t.Fatalf("duplicate watch answered %+v, want same fp and seq %d under gen 2", second, first.Seq)
	}
	viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: sharer.fp, Gen: 2})
	select {
	case m := <-viewer.vsrc:
		t.Fatalf("unchanged gen produced another video_source: %+v", m)
	case <-time.After(400 * time.Millisecond):
	}
}
