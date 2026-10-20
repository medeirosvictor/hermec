package state

import (
	"path/filepath"
	"testing"

	"github.com/medeirosvictor/hermec/core/proto"
)

func TestShareQualityDefaultsAndCycles(t *testing.T) {
	var s Settings
	if s.ShareQualityID() != "720p" {
		t.Fatalf("default = %q", s.ShareQualityID())
	}
	if got := NextShareQuality(""); got != "1080p" {
		t.Fatalf("next of default = %q", got)
	}
	if got := NextShareQuality("1080p"); got != "720p" {
		t.Fatalf("next of 1080p = %q", got)
	}
	if got := NextShareQuality("garbage"); got != "1080p" {
		t.Fatalf("next of garbage (counts as 720p) = %q", got)
	}
	p := ShareProfileFor("1080p")
	if p.MaxW != 1920 || p.MaxH != 1080 || p.KBPS != 3000 || p.FPS != 15 {
		t.Fatalf("1080p profile = %+v", p)
	}
	p = ShareProfileFor("")
	if p.MaxW != 1280 || p.MaxH != 720 || p.KBPS != 1500 || p.FPS != 15 {
		t.Fatalf("720p profile = %+v", p)
	}
}

func TestShareQualityPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := SaveSettings(path, Settings{ShareQuality: "1080p"}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(path)
	if err != nil || got.ShareQuality != "1080p" {
		t.Fatalf("loaded %+v, %v", got, err)
	}
}

func TestLetterbox(t *testing.T) {
	// A 16:9 frame in a 4:3 pane (800x600 at 10,20): full width, bars above and below.
	x, y, w, h := Letterbox(1280, 720, 10, 20, 810, 620)
	if x != 10 || w != 800 || h != 450 || y != 20+75 {
		t.Fatalf("16:9 in 4:3 = %v %v %v %v", x, y, w, h)
	}
	// A 4:3 frame in a 16:9 pane (1600x900): full height, bars left and right.
	x, y, w, h = Letterbox(800, 600, 0, 0, 1600, 900)
	if y != 0 || h != 900 || w != 1200 || x != 200 {
		t.Fatalf("4:3 in 16:9 = %v %v %v %v", x, y, w, h)
	}
	// Degenerate input draws nothing.
	if _, _, w, h := Letterbox(0, 0, 0, 0, 100, 100); w != 0 || h != 0 {
		t.Fatalf("empty frame = %v x %v", w, h)
	}
	if _, _, w, h := Letterbox(10, 10, 5, 5, 5, 50); w != 0 || h != 0 {
		t.Fatalf("empty pane = %v x %v", w, h)
	}
}

func TestRowsCarrySharing(t *testing.T) {
	s := New()
	s.VoiceChannels = []string{"voice"}
	s.Voice["voice"] = []proto.VoiceMember{{Fingerprint: "a", Name: "ann", Sharing: true}, {Fingerprint: "b", Name: "bob"}}
	var got []bool
	for _, r := range s.Rows() {
		if r.Kind == RowOccupant {
			got = append(got, r.Sharing)
		}
	}
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("sharing flags = %v", got)
	}
}

func TestSharerIn(t *testing.T) {
	s := New()
	s.Voice["voice"] = []proto.VoiceMember{{Fingerprint: "a", Name: "ann", Sharing: true}, {Fingerprint: "b", Name: "bob"}}
	if n, ok := s.SharerIn("voice", "a"); !ok || n != "ann" {
		t.Fatalf("a = %q %v", n, ok)
	}
	if _, ok := s.SharerIn("voice", "b"); ok {
		t.Fatal("b is not sharing")
	}
	if _, ok := s.SharerIn("other", "a"); ok {
		t.Fatal("wrong channel")
	}
}
