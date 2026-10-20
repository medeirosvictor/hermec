package server

import (
	"encoding/json"
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

// A refused watch's error carries the request's gen.
func TestRefusedWatchErrorCarriesGen(t *testing.T) {
	url := startServer(t, shareCfg())
	b, _ := authed(t, url)
	joinLounge(t, b)
	sendEnv(t, b, proto.TypeWatch, proto.Watch{Fingerprint: "nobody", Gen: 7})
	_ = b.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		env := readEnvSkipRoster(t, b)
		if env.Type != proto.TypeError {
			continue
		}
		var e proto.ErrorMsg
		if err := json.Unmarshal(env.Data, &e); err != nil {
			t.Fatal(err)
		}
		if e.Code != proto.CodeNotSharing || e.Gen != 7 {
			t.Fatalf("error = %+v, want not_sharing gen 7", e)
		}
		return
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
