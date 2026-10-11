package state

import (
	"reflect"
	"testing"
	"time"
)

func ms(n float64) time.Duration { return time.Duration(n * float64(time.Millisecond)) }

func TestGrade(t *testing.T) {
	tests := []struct {
		name string
		rtt  time.Duration
		ok   bool
		conc float64
		want int
	}{
		{"no pong", ms(10), false, 0, 0},
		{"fast clean", ms(79), true, 0.0099, 4},
		{"rtt boundary 80", ms(80), true, 0, 3},
		{"conceal boundary 1%", ms(10), true, 0.01, 3},
		{"149/2.9%", ms(149), true, 0.029, 3},
		{"rtt 150", ms(150), true, 0, 2},
		{"conceal 3%", ms(10), true, 0.03, 2},
		{"299/7.9%", ms(299), true, 0.079, 2},
		{"rtt 300", ms(300), true, 0, 1},
		{"conceal 8%", ms(10), true, 0.08, 1},
		{"awful", ms(900), true, 0.5, 1},
	}
	for _, tc := range tests {
		if got := Grade(tc.rtt, tc.ok, tc.conc, 0); got != tc.want {
			t.Errorf("%s: Grade = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestGradeWorstSenderDominates(t *testing.T) {
	parts := []Participant{
		{Name: "a", Concealed: 0.001, OK: true},
		{Name: "b", Concealed: 0.05, Jitter: ms(30), OK: true},
		{Name: "c", Concealed: 0.9, OK: false}, // stale: ignored
	}
	c, j := WorstHealth(parts)
	if c != 0.05 || j != ms(30) {
		t.Fatalf("WorstHealth = %v, %v", c, j)
	}
	if g := Grade(ms(10), true, c, j); g != 2 {
		t.Errorf("grade = %d, want 2", g)
	}
	if c, j := WorstHealth(nil); c != 0 || j != 0 {
		t.Errorf("empty WorstHealth = %v, %v", c, j)
	}
}

func TestQualityPanelNoDataOnDisconnect(t *testing.T) {
	got := QualityLines(0, false, nil)
	if !reflect.DeepEqual(got, []string{"no data"}) {
		t.Errorf("got %q", got)
	}
}

func TestQualityLines(t *testing.T) {
	parts := []Participant{
		{Name: "ann", Concealed: 0.008, Jitter: ms(6), OK: true},
		{Name: "bob", Concealed: 0.012, Jitter: ms(8), OK: true},
		{Name: "cy", OK: false},
	}
	got := QualityLines(ms(42), true, parts)
	want := []string{"ping 42 ms", "jitter 8 ms", "concealed 1.2%", "ann  0.8%  6 ms", "bob  1.2%  8 ms", "cy  --"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if got := QualityLines(ms(42), true, nil); !reflect.DeepEqual(got, []string{"ping 42 ms"}) {
		t.Errorf("out of call: %q", got)
	}
}
