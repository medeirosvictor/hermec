// Package icon draws the Hermec app icon in code: a white envelope with tiny
// angel wings on a dark rounded square. It is pure Go (no Ebiten) so it can be
// tested headlessly and used by cmd/hermec-icon.
package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"

	"golang.org/x/image/vector"
)

type pt struct{ x, y float64 }

var (
	bgColour = color.RGBA{0x1E, 0x1F, 0x22, 0xFF}
	white    = color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
)

// Geometry, in units of the icon size.
const (
	bgRadius = 0.22
	envW     = 0.46
	envH     = 0.32
	envCY    = 0.53
	flapFrac = 0.6
	tipX     = 0.06 // outermost wing tip
	wingGap  = 0.04 // clearance between wing root and envelope outline
	// wingAttach is the wing root's height as a fraction of the envelope
	// height below its top edge.
	wingAttach = 0.15
)

// Render draws the icon at size x size pixels.
func Render(size int) *image.RGBA {
	s := float64(size)
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	fill(img, roundedSquare(s, bgRadius*s), bgColour)

	// Left-half shapes, each mirrored about x = 0.5s.
	for _, p := range leftShapes(s) {
		fill(img, p, white)
		fill(img, mirror(p, s), white)
	}
	return img
}

func mirror(p []pt, s float64) []pt {
	m := make([]pt, len(p))
	for i, q := range p {
		m[len(p)-1-i] = pt{s - q.x, q.y}
	}
	return m
}

// leftShapes is the envelope outline and the wing lobes of the left half.
func leftShapes(s float64) [][]pt {
	var out [][]pt
	left := 0.5*s - envW*s/2
	top := envCY*s - envH*s/2
	bot := top + envH*s
	mid := 0.5 * s
	sw := math.Max(1.5, 0.055*s)
	h := sw / 2

	// Outline bars (outer edge at the nominal rectangle, stroke inside it).
	out = append(out,
		[]pt{{left, top}, {mid, top}, {mid, top + sw}, {left, top + sw}},
		[]pt{{left, bot - sw}, {mid, bot - sw}, {mid, bot}, {left, bot}},
		[]pt{{left, top}, {left + sw, top}, {left + sw, bot}, {left, bot}},
	)
	// Flap: top corner to the centre point, as a thick line.
	a := pt{left + h, top + h}
	b := pt{mid, top + flapFrac*envH*s}
	out = append(out, thickLine(a, b, sw))

	// Wings: three feathers sweeping up and outward from near the upper side
	// corner, stepping down in length so the lower edge is scalloped. The top
	// feather's leading edge rises ~30 degrees; each tip sits lower and nearer
	// the envelope than the one above.
	base := pt{left - wingGap*s, top + wingAttach*envH*s}
	angles := []float64{32, 8, -16}
	scales := []float64{1, 0.75, 0.55}
	l1 := (base.x - tipX*s) / math.Cos(angles[0]*math.Pi/180)
	for i := range angles {
		out = append(out, feather(base, angles[i], l1*scales[i]))
	}
	return out
}

// thickLine is the quad around segment a-b with the given width.
func thickLine(a, b pt, w float64) []pt {
	dx, dy := b.x-a.x, b.y-a.y
	n := math.Hypot(dx, dy)
	nx, ny := -dy/n*w/2, dx/n*w/2
	return []pt{{a.x + nx, a.y + ny}, {b.x + nx, b.y + ny}, {b.x - nx, b.y - ny}, {a.x - nx, a.y - ny}}
}

// feather is a leaf shape from base, pointing up and to the left at deg
// degrees above horizontal, of the given length.
func feather(base pt, deg, length float64) []pt {
	const n = 12
	rad := deg * math.Pi / 180
	ax, ay := -math.Cos(rad), -math.Sin(rad) // axis (up-left)
	px, py := -ay, ax                        // perpendicular
	halfW := 0.2 * length
	var side1, side2 []pt
	for i := 0; i <= n; i++ {
		t := float64(i) / n
		w := halfW * math.Pow(math.Sin(math.Pi*math.Pow(t, 0.8)), 0.9)
		cx, cy := base.x+ax*length*t, base.y+ay*length*t
		side1 = append(side1, pt{cx + px*w, cy + py*w})
		side2 = append(side2, pt{cx - px*w, cy - py*w})
	}
	poly := side1
	for i := n; i >= 0; i-- {
		poly = append(poly, side2[i])
	}
	return poly
}

func roundedSquare(s, r float64) []pt {
	const n = 16
	corners := []struct {
		c      pt
		a0, a1 float64
	}{
		{pt{r, r}, math.Pi, 1.5 * math.Pi},
		{pt{s - r, r}, 1.5 * math.Pi, 2 * math.Pi},
		{pt{s - r, s - r}, 0, 0.5 * math.Pi},
		{pt{r, s - r}, 0.5 * math.Pi, math.Pi},
	}
	var p []pt
	for _, c := range corners {
		for i := 0; i <= n; i++ {
			a := c.a0 + (c.a1-c.a0)*float64(i)/n
			p = append(p, pt{c.c.x + r*math.Cos(a), c.c.y + r*math.Sin(a)})
		}
	}
	return p
}

// fill composites the polygon over img in colour c with anti-aliasing.
func fill(img *image.RGBA, poly []pt, c color.RGBA) {
	b := img.Bounds()
	var r vector.Rasterizer
	r.Reset(b.Dx(), b.Dy())
	for i, q := range poly {
		if i == 0 {
			r.MoveTo(float32(q.x), float32(q.y))
		} else {
			r.LineTo(float32(q.x), float32(q.y))
		}
	}
	r.ClosePath()
	r.Draw(img, b, image.NewUniform(c), image.Point{})
}

// PNG encodes the icon at size as PNG bytes.
func PNG(size int) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, Render(size)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ICO builds a multi-size .ico with PNG-compressed entries.
func ICO(sizes []int) ([]byte, error) {
	pngs := make([][]byte, len(sizes))
	for i, s := range sizes {
		b, err := PNG(s)
		if err != nil {
			return nil, err
		}
		pngs[i] = b
	}
	var buf bytes.Buffer
	w := func(v any) { _ = binary.Write(&buf, binary.LittleEndian, v) }
	w(uint16(0)) // reserved
	w(uint16(1)) // type: icon
	w(uint16(len(sizes)))
	off := 6 + 16*len(sizes)
	for i, s := range sizes {
		d := byte(s)
		if s >= 256 {
			d = 0
		}
		buf.Write([]byte{d, d, 0, 0})
		w(uint16(1))  // planes
		w(uint16(32)) // bpp
		w(uint32(len(pngs[i])))
		w(uint32(off))
		off += len(pngs[i])
	}
	for _, b := range pngs {
		buf.Write(b)
	}
	return buf.Bytes(), nil
}
