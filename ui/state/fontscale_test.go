package state

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestFontScale(t *testing.T) {
	for in, want := range map[int]int{0: 100, -5: 100, 80: 80, 110: 110, 150: 150} {
		if got := (Settings{FontScale: in}).FontScalePct(); got != want {
			t.Errorf("FontScalePct(%d) = %d, want %d", in, got, want)
		}
	}
	if !reflect.DeepEqual(FontScales, []int{80, 90, 100, 110, 125, 150}) {
		t.Fatalf("FontScales = %v", FontScales)
	}
	// 0 means 100, so the first press goes to 110; wraps; unknown goes to 100.
	cases := [][2]int{{0, 110}, {80, 90}, {90, 100}, {100, 110}, {110, 125}, {125, 150}, {150, 80}, {73, 100}, {-1, 100}}
	for _, c := range cases {
		if got := NextFontScale(c[0]); got != c[1] {
			t.Errorf("NextFontScale(%d) = %d, want %d", c[0], got, c[1])
		}
	}
	p := filepath.Join(t.TempDir(), "settings.toml")
	if err := SaveSettings(p, Settings{FontScale: 125}); err != nil {
		t.Fatal(err)
	}
	if s, err := LoadSettings(p); err != nil || s.FontScale != 125 {
		t.Fatalf("round trip %+v %v", s, err)
	}
}
