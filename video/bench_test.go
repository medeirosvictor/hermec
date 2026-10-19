package video

import "testing"

func BenchmarkEncode720p(b *testing.B) {
	enc, err := NewEncoder(1280, 720, 15, 1500)
	if err != nil {
		b.Fatal(err)
	}
	defer enc.Close()
	frames := make([]*Frame, 30)
	for i := range frames {
		frames[i] = NewFrame(1280, 720)
		synthFrame(frames[i], i)
	}
	var bytes int
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pkt, _, err := enc.Encode(frames[i%len(frames)], false)
		if err != nil {
			b.Fatal(err)
		}
		bytes += len(pkt)
	}
	b.ReportMetric(float64(bytes)/float64(b.N), "bytes/frame")
}

func BenchmarkDecode720p(b *testing.B) {
	enc, err := NewEncoder(1280, 720, 15, 1500)
	if err != nil {
		b.Fatal(err)
	}
	defer enc.Close()
	f := NewFrame(1280, 720)
	var pkts [][]byte
	for i := 0; i < 60; i++ {
		synthFrame(f, i)
		pkt, _, err := enc.Encode(f, false)
		if err != nil {
			b.Fatal(err)
		}
		pkts = append(pkts, append([]byte(nil), pkt...))
	}
	dec, err := NewDecoder()
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := dec.Decode(pkts[i%len(pkts)]); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFromBGRA1440to720(b *testing.B) {
	src := synthBGRA(2560, 1440, 3)
	dst := NewFrame(1280, 720)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FromBGRA(dst, src, 2560, 1440, 2560*4)
	}
}
