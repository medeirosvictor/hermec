package theme

import (
	"reflect"
	"testing"

	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/sfnt"
)

// uiRunes are the non-ASCII runes that appear in string or rune literals in
// the non-test .go files under ui/ (found by grepping for [^\x00-\x7F]):
// U+2014 em dash (main_scene.go "CONNECTION LOST — ...", titlebar.go,
// update.go, state/roster.go) and U+2026 ellipsis (state/tooltip.go
// Ellipsis), U+203A › and U+2039 ‹ (settings rows: update.go, help.go, settings.go). U+00B2, U+00B7 (state/barrel.go) and U+2500 (state/voice.go) occur
// only in comments, so they are not required.
var uiRunes = []rune{'…', '—', '›', '‹', '•'}

func fontBytes(t *testing.T, id string) []byte {
	t.Helper()
	f := fontByID(id)
	if f == nil {
		t.Fatalf("no embedded data for font %q", id)
	}
	return f.data
}

// Every bundled font must draw the runes the UI hard-codes.
func TestFacesHaveUIGlyphs(t *testing.T) {
	for _, id := range Fonts() {
		t.Run(id, func(t *testing.T) {
			f, err := sfnt.Parse(fontBytes(t, id))
			if err != nil {
				t.Fatal(err)
			}
			var b sfnt.Buffer
			for _, r := range uiRunes {
				if idx, err := f.GlyphIndex(&b, r); err != nil || idx == 0 {
					t.Errorf("%s has no glyph for %q U+%04X (idx=%d, err=%v)", id, r, r, idx, err)
				}
			}
		})
	}
}

func TestFontsOrder(t *testing.T) {
	want := []string{"vt323", "jetbrains-mono", "ibm-plex-mono", "geist-mono"}
	if got := Fonts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Fonts() = %v, want %v", got, want)
	}
	labels := map[string]string{"vt323": "VT323", "jetbrains-mono": "JetBrains Mono", "ibm-plex-mono": "IBM Plex Mono", "geist-mono": "Geist Mono"}
	for id, l := range labels {
		if got := FontLabel(id); got != l {
			t.Errorf("FontLabel(%q) = %q, want %q", id, got, l)
		}
	}
}

func TestNextFont(t *testing.T) {
	ids := Fonts()
	for i, id := range ids {
		if got, want := NextFont(id), ids[(i+1)%len(ids)]; got != want {
			t.Errorf("NextFont(%q) = %q, want %q", id, got, want)
		}
	}
	for _, id := range []string{"", "comic-sans"} {
		if got := NextFont(id); got != "jetbrains-mono" {
			t.Errorf("NextFont(%q) = %q, want jetbrains-mono", id, got)
		}
	}
}

func TestFaceEveryFont(t *testing.T) {
	for _, id := range Fonts() {
		f, err := Face(id, 18)
		if err != nil || f == nil {
			t.Fatalf("Face(%q): %v", id, err)
		}
		if adv, _ := text.Measure("M", f, 0); adv <= 0 {
			t.Errorf("%s: advance of M = %v", id, adv)
		}
		// The layout assumes monospace: narrow and wide letters match.
		wi, _ := text.Measure("i", f, 0)
		wm, _ := text.Measure("M", f, 0)
		if wi != wm {
			t.Errorf("%s is not monospace: i=%v M=%v", id, wi, wm)
		}
	}
	a, _ := Face("jetbrains-mono", 18)
	b, _ := Face("jetbrains-mono", 24)
	c, _ := Face("vt323", 18)
	if a.Source != b.Source || a.Source == c.Source {
		t.Error("expected one cached source per font")
	}
	d, _ := Face("nope", 18)
	if d.Source != c.Source {
		t.Error("unknown font id should fall back to vt323")
	}
	e, _ := Face("", 18)
	if e.Source != c.Source {
		t.Error("empty font id should fall back to vt323")
	}
}
