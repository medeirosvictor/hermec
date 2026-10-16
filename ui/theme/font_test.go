package theme

import (
	"testing"

	"golang.org/x/image/font/sfnt"
)

// The tooltip and header ellipsis is U+2026; the embedded face must draw it.
func TestFaceHasEllipsis(t *testing.T) {
	f, err := sfnt.Parse(vt323TTF)
	if err != nil {
		t.Fatal(err)
	}
	var b sfnt.Buffer
	if idx, err := f.GlyphIndex(&b, '…'); err != nil || idx == 0 {
		t.Fatalf("VT323 has no U+2026 glyph (idx=%d, err=%v)", idx, err)
	}
}
