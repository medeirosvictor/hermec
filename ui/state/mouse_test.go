package state

import "testing"

func TestRowAt(t *testing.T) {
	cases := []struct {
		name         string
		y, top, rowH float64
		count, want  int
	}{
		{"first row", 10, 10, 20, 3, 0},
		{"second row", 30, 10, 20, 3, 1},
		{"last row edge", 69.9, 10, 20, 3, 2},
		{"past end", 70, 10, 20, 3, -1},
		{"above top", 9.9, 10, 20, 3, -1},
		{"empty", 15, 10, 20, 0, -1},
		{"bad height", 15, 10, 0, 3, -1},
	}
	for _, c := range cases {
		if got := RowAt(c.y, c.top, c.rowH, c.count); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestInRect(t *testing.T) {
	if !InRect(5, 5, 0, 0, 10, 10) || InRect(10, 5, 0, 0, 10, 10) || InRect(-1, 5, 0, 0, 10, 10) {
		t.Fatal("InRect bounds wrong")
	}
}

func TestWheelLines(t *testing.T) {
	lines, carry := WheelLines(0, 1, 3)
	if lines != 3 || carry != 0 {
		t.Fatalf("got %d %v", lines, carry)
	}
	lines, carry = WheelLines(0, 0.2, 3) // 0.6 lines
	if lines != 0 {
		t.Fatalf("lines %d", lines)
	}
	lines, _ = WheelLines(carry, 0.2, 3) // 1.2 lines total
	if lines != 1 {
		t.Fatalf("lines %d", lines)
	}
	lines, _ = WheelLines(0, -1, 3)
	if lines != -3 {
		t.Fatalf("lines %d", lines)
	}
	lines, carry = WheelLines(0, -0.5, 3) // -1.5
	if lines != -1 || carry != -0.5 {
		t.Fatalf("lines %d carry %v", lines, carry)
	}
}
