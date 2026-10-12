package state

import (
	"testing"
	"time"
)

func TestNextBackoff(t *testing.T) {
	s := time.Second
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{-3, s}, {0, s}, {1, 2 * s}, {2, 4 * s}, {3, 8 * s}, {4, 16 * s}, {5, 30 * s}, {6, 30 * s}, {100, 30 * s},
	}
	for _, c := range cases {
		if got := NextBackoff(c.attempt); got != c.want {
			t.Errorf("NextBackoff(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
}
