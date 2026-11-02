package state

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestVolumePctDefaultsTo100(t *testing.T) {
	var s Settings
	if got := s.VolumePct("aa"); got != 100 {
		t.Fatalf("unset = %d", got)
	}
	s.Volumes = map[string]int{"aa": 130} // not a step: ignored
	if got := s.VolumePct("aa"); got != 100 {
		t.Fatalf("bad step = %d", got)
	}
}

func TestSetVolumeOmitsDefault(t *testing.T) {
	var s Settings
	s.SetVolume("aa", 150)
	s.SetVolume("bb", 0)
	if s.VolumePct("aa") != 150 || s.VolumePct("bb") != 0 {
		t.Fatalf("got %v", s.Volumes)
	}
	s.SetVolume("aa", 100)
	if _, ok := s.Volumes["aa"]; ok {
		t.Fatal("100% must not be stored")
	}
	s.SetVolume("bb", 100)
	if s.Volumes != nil {
		t.Fatalf("empty map should reset to nil, got %v", s.Volumes)
	}
}

func TestVolumesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	var s Settings
	s.SetVolume("ab:cd", 75)
	s.SetVolume("ef", 200)
	if err := SaveSettings(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Volumes, s.Volumes) {
		t.Fatalf("got %v want %v", got.Volumes, s.Volumes)
	}
}

func TestMicGainPctAndNext(t *testing.T) {
	var s Settings
	if s.MicGainPct() != 100 {
		t.Fatalf("unset = %d", s.MicGainPct())
	}
	s.MicGain = 130
	if s.MicGainPct() != 100 {
		t.Fatalf("bad step = %d", s.MicGainPct())
	}
	s.MicGain = 150
	if s.MicGainPct() != 150 {
		t.Fatalf("got %d", s.MicGainPct())
	}
	cur := 100
	var seen []int
	for range MicGains {
		cur = NextMicGain(cur)
		seen = append(seen, cur)
	}
	if want := []int{125, 150, 200, 50, 75, 100}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("cycle = %v, want %v", seen, want)
	}
	if NextMicGain(0) != 125 || NextMicGain(33) != 100 {
		t.Fatal("unset and off-step must start from 100")
	}
}
