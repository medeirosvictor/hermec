package audio

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestMixSums(t *testing.T) {
	dst := make([]int16, 3)
	Mix(dst, [][]int16{{100, -200, 300}, {1, 2, 3}}, nil)
	if want := []int16{101, -198, 303}; !reflect.DeepEqual(dst, want) {
		t.Fatalf("got %v want %v", dst, want)
	}
}

func TestMixGains(t *testing.T) {
	dst := make([]int16, 2)
	Mix(dst, [][]int16{{1000, -1000}, {1000, 1000}}, []float64{2, 0})
	if want := []int16{2000, -2000}; !reflect.DeepEqual(dst, want) {
		t.Fatalf("got %v want %v", dst, want)
	}
}

func TestMixLimitsWithoutWrapping(t *testing.T) {
	dst := make([]int16, 2)
	Mix(dst, [][]int16{{math.MaxInt16, math.MinInt16}, {math.MaxInt16, math.MinInt16}, {5, -5}}, nil)
	if dst[0] < 32000 || dst[1] > -32000 {
		t.Fatalf("overload collapsed or wrapped: %v", dst)
	}
}

func TestMixKeepsInRangeSumsUntouched(t *testing.T) {
	loud := []int16{30000, math.MinInt16, math.MaxInt16, 25000}
	dst := make([]int16, len(loud))
	Mix(dst, [][]int16{loud}, nil)
	if !reflect.DeepEqual(dst, loud) {
		t.Fatalf("lone speaker changed: %v want %v", dst, loud)
	}
	Mix(dst[:1], [][]int16{{30000}, {2000}}, nil) // sums to 32000: in range, above the knee
	if dst[0] != 32000 {
		t.Fatalf("in-range sum limited: %d", dst[0])
	}
}

func TestMixShortAndEmpty(t *testing.T) {
	dst := []int16{9, 9, 9}
	Mix(dst, [][]int16{{4}}, nil)
	if want := []int16{4, 0, 0}; !reflect.DeepEqual(dst, want) {
		t.Fatalf("got %v want %v", dst, want)
	}
	Mix(dst, nil, nil)
	if want := []int16{0, 0, 0}; !reflect.DeepEqual(dst, want) {
		t.Fatalf("got %v want %v", dst, want)
	}
}

func TestSoftLimitIdentityBelowKnee(t *testing.T) {
	for _, v := range []int16{0, 1, -1, 12000, -24000, 24575} {
		if got := softLimit(float64(v)); got != v {
			t.Fatalf("softLimit(%d) = %d, want unchanged", v, got)
		}
	}
}

func TestSoftLimitMonotonicAndBounded(t *testing.T) {
	prev := int16(math.MinInt16)
	for v := -200000.0; v <= 200000; v += 97 {
		got := softLimit(v)
		if got < prev {
			t.Fatalf("not monotonic at %v: %d after %d", v, got, prev)
		}
		prev = got
	}
	if got := softLimit(1e9); got != math.MaxInt16 {
		t.Fatalf("huge input = %d, want %d", got, math.MaxInt16)
	}
	if got := softLimit(-1e9); got != -math.MaxInt16 {
		t.Fatalf("huge negative input = %d", got)
	}
}

func TestApplyGain(t *testing.T) {
	pcm := []int16{1000, -1000, 30000, -30000}
	ApplyGain(pcm, 1)
	if want := []int16{1000, -1000, 30000, -30000}; !reflect.DeepEqual(pcm, want) {
		t.Fatalf("unity changed samples: %v", pcm)
	}
	ApplyGain(pcm, 0.5)
	if pcm[0] != 500 || pcm[1] != -500 {
		t.Fatalf("half gain: %v", pcm)
	}
	loud := []int16{20000, -20000}
	ApplyGain(loud, 2) // 40000 would clip; the limiter keeps it inside
	if loud[0] <= 24575 || loud[0] >= math.MaxInt16 || loud[1] != -loud[0] {
		t.Fatalf("limited gain: %v", loud)
	}
	ApplyGain(loud, 0)
	if loud[0] != 0 || loud[1] != 0 {
		t.Fatalf("zero gain: %v", loud)
	}
}

func TestMixerVolumeScalesOneSpeaker(t *testing.T) {
	pull := func(vol map[string]int) float64 {
		m := newMixer()
		clock := time.Unix(1000, 0)
		m.now = func() time.Time { return clock }
		for fp, pct := range vol {
			m.SetVolume(fp, pct)
		}
		frames := encodeFrames(t, 20)
		dstA := make([]int16, frameSize)
		for _, f := range frames {
			m.Write("a", f)
			m.Pull(dstA)
			clock = clock.Add(20 * time.Millisecond)
		}
		return RMS(dstA)
	}
	base := pull(nil)
	half := pull(map[string]int{"a": 50})
	muted := pull(map[string]int{"a": 0})
	other := pull(map[string]int{"b": 0})
	if base < 0.01 {
		t.Fatalf("baseline silent: %v", base)
	}
	if math.Abs(half/base-0.5) > 0.05 {
		t.Fatalf("50%% volume: ratio %v", half/base)
	}
	if muted != 0 {
		t.Fatalf("0%% volume not silent: %v", muted)
	}
	if other != base {
		t.Fatalf("another speaker's volume changed this one: %v vs %v", other, base)
	}
}

func TestMixerSetVolumeHundredForgets(t *testing.T) {
	m := newMixer()
	m.SetVolume("a", 150)
	if m.gains["a"] != 1.5 {
		t.Fatalf("gain = %v", m.gains["a"])
	}
	m.SetVolume("a", 100)
	if _, ok := m.gains["a"]; ok {
		t.Fatal("100% should clear the entry")
	}
}
