package cameracap

import (
	"image"
	"image/color"
	"testing"

	"github.com/medeirosvictor/hermec/video"
)

func TestConvertImageFillsFixedI420Frame(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{G: 255, A: 255})
	img.Set(0, 1, color.RGBA{B: 255, A: 255})
	img.Set(1, 1, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	frame := video.NewFrame(640, 360)
	convertImage(frame, img)
	if frame.Y[0] == frame.Y[639] || frame.Y[0] == frame.Y[len(frame.Y)-1] {
		t.Fatalf("conversion lost source colors: first=%d top-right=%d last=%d", frame.Y[0], frame.Y[639], frame.Y[len(frame.Y)-1])
	}
	if len(frame.U) != 320*180 || len(frame.V) != 320*180 {
		t.Fatalf("chroma sizes = %d/%d", len(frame.U), len(frame.V))
	}
}
