package cameracap

import (
	"image"
	"sync"

	"github.com/medeirosvictor/hermec/video"
)

type frameReader interface {
	Read() (image.Image, func(), error)
	Close() error
}

type latestFrameSource struct {
	reader frameReader

	mu      sync.Mutex
	latest  *video.Frame
	gen     uint64
	seen    uint64
	pending error
	closed  bool
	once    sync.Once
	done    chan struct{}
}

func newLatestFrameSource(reader frameReader) Source {
	s := &latestFrameSource{reader: reader, done: make(chan struct{})}
	go s.readLoop()
	return s
}

func (s *latestFrameSource) readLoop() {
	defer close(s.done)
	for {
		img, release, err := s.reader.Read()
		if err != nil {
			s.mu.Lock()
			if !s.closed && s.pending == nil {
				s.pending = err
			}
			s.mu.Unlock()
			return
		}
		frame := video.NewFrame(Width, Height)
		convertImage(frame, img)
		s.mu.Lock()
		closed := s.closed
		if !closed {
			s.latest = frame
			s.gen++
		}
		s.mu.Unlock()
		if release != nil {
			release()
		}
		if closed {
			return
		}
	}
}

func (s *latestFrameSource) Next(dst *video.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != s.seen && s.latest != nil {
		s.seen = s.gen
		dst.W, dst.H = s.latest.W, s.latest.H
		dst.Y = append(dst.Y[:0], s.latest.Y...)
		dst.U = append(dst.U[:0], s.latest.U...)
		dst.V = append(dst.V[:0], s.latest.V...)
		return nil
	}
	if s.pending != nil {
		err := s.pending
		s.pending = nil
		return err
	}
	return video.ErrNoFrame
}

func (s *latestFrameSource) Close() error {
	var err error
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		err = s.reader.Close()
		<-s.done
	})
	return err
}

func convertImage(dst *video.Frame, src image.Image) {
	bounds := src.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	if sw <= 0 || sh <= 0 {
		return
	}
	sample := func(x, y int) (int32, int32, int32) {
		sx := bounds.Min.X + x*sw/dst.W
		sy := bounds.Min.Y + y*sh/dst.H
		r, g, b, _ := src.At(sx, sy).RGBA()
		return int32(r >> 8), int32(g >> 8), int32(b >> 8)
	}
	for y := 0; y < dst.H; y++ {
		for x := 0; x < dst.W; x++ {
			r, g, b := sample(x, y)
			dst.Y[y*dst.W+x] = clampByte(((66*r + 129*g + 25*b + 128) >> 8) + 16)
		}
	}
	cw := (dst.W + 1) / 2
	for y := 0; y < dst.H; y += 2 {
		for x := 0; x < dst.W; x += 2 {
			var r, g, b int32
			var n int32
			for dy := 0; dy < 2 && y+dy < dst.H; dy++ {
				for dx := 0; dx < 2 && x+dx < dst.W; dx++ {
					rr, gg, bb := sample(x+dx, y+dy)
					r, g, b, n = r+rr, g+gg, b+bb, n+1
				}
			}
			r, g, b = r/n, g/n, b/n
			i := (y/2)*cw + x/2
			dst.U[i] = clampByte(((-38*r - 74*g + 112*b + 128) >> 8) + 128)
			dst.V[i] = clampByte(((112*r - 94*g - 18*b + 128) >> 8) + 128)
		}
	}
}

func clampByte(v int32) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}
