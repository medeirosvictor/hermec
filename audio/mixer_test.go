package audio

import (
	"math"
	"reflect"
	"testing"
)

func TestMixSums(t *testing.T) {
	dst := make([]int16, 3)
	Mix(dst, []int16{100, -200, 300}, []int16{1, 2, 3})
	if want := []int16{101, -198, 303}; !reflect.DeepEqual(dst, want) {
		t.Fatalf("got %v want %v", dst, want)
	}
}

func TestMixSaturates(t *testing.T) {
	dst := make([]int16, 2)
	Mix(dst, []int16{math.MaxInt16, math.MinInt16}, []int16{math.MaxInt16, math.MinInt16}, []int16{5, -5})
	if dst[0] != math.MaxInt16 || dst[1] != math.MinInt16 {
		t.Fatalf("no saturation: %v", dst)
	}
}

func TestMixShortAndEmpty(t *testing.T) {
	dst := []int16{9, 9, 9}
	Mix(dst, []int16{4})
	if want := []int16{4, 0, 0}; !reflect.DeepEqual(dst, want) {
		t.Fatalf("got %v want %v", dst, want)
	}
	Mix(dst)
	if want := []int16{0, 0, 0}; !reflect.DeepEqual(dst, want) {
		t.Fatalf("got %v want %v", dst, want)
	}
}
