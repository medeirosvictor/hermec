package state

import (
	"testing"
	"time"
)

func msgsAt(secs ...int) []Message {
	var out []Message
	for _, s := range secs {
		out = append(out, Message{FromName: "a", Text: "hi", TS: time.Unix(int64(s), 0)})
	}
	return out
}

func TestLayoutCacheInvalidation(t *testing.T) {
	base := msgsAt(1, 2, 3)
	tests := []struct {
		name       string
		ch         string
		msgs       []Message
		cols       int
		font       float64
		recomputed bool
	}{
		{"identical", "general", base, 80, 16, false},
		{"new message", "general", msgsAt(1, 2, 3, 4), 80, 16, true},
		{"channel switch", "random", base, 80, 16, true},
		{"resize", "general", base, 60, 16, true},
		{"font size", "general", base, 80, 18, true},
		{"capped window slid", "general", msgsAt(2, 3, 4), 80, 16, true},
		{"last ts changed same count", "general", msgsAt(1, 2, 9), 80, 16, true},
		{"empty after nonempty", "general", nil, 80, 16, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c LayoutCache
			if _, re := c.Lines("general", base, 80, 16); !re {
				t.Fatal("first call must compute")
			}
			lines, re := c.Lines(tc.ch, tc.msgs, tc.cols, tc.font)
			if re != tc.recomputed {
				t.Fatalf("recomputed=%v want %v", re, tc.recomputed)
			}
			if want := len(Scrollback(tc.msgs, tc.cols)); len(lines) != want {
				t.Fatalf("lines=%d want %d", len(lines), want)
			}
		})
	}
}

func TestLayoutCacheReusesSlice(t *testing.T) {
	var c LayoutCache
	m := msgsAt(1, 2)
	a, _ := c.Lines("g", m, 80, 16)
	b, re := c.Lines("g", m, 80, 16)
	if re || &a[0] != &b[0] {
		t.Fatal("expected same backing slice without recompute")
	}
}
